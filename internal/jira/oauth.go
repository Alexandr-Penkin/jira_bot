package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"errors"

	"github.com/rs/zerolog"

	"SleepJiraBot/internal/storage"
)

const (
	authURL     = "https://auth.atlassian.com/authorize"
	tokenURL    = "https://auth.atlassian.com/oauth/token"
	resourceURL = "https://api.atlassian.com/oauth/token/accessible-resources"

	stateMaxAge     = 10 * time.Minute
	stateCleanupInt = 1 * time.Minute
)

type OAuthConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	Scopes       []string
}

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
}

type AccessibleResource struct {
	ID   string `json:"id"`
	URL  string `json:"url"`
	Name string `json:"name"`
}

type oauthState struct {
	telegramUserID int64
	createdAt      time.Time
}

type OAuthClient struct {
	cfg    OAuthConfig
	log    zerolog.Logger
	mu     sync.Mutex
	states map[string]oauthState
	// stateStore, when set, persists OAuth state tokens in Mongo so
	// processes that generate the auth URL (telegram-svc /connect) and
	// the process that handles the callback (cmd/bot) can share state.
	// In-memory states map remains a fallback for single-process tests
	// and dev runs without a configured store.
	stateStore *storage.OAuthStateRepo
}

func NewOAuthClient(cfg OAuthConfig, log zerolog.Logger) *OAuthClient {
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{
			"read:jira-work",
			"write:jira-work",
			"read:jira-user",
			"read:sprint:jira-software",
			"read:board-scope:jira-software",
			"read:project:jira",
			"read:webhook:jira",
			"write:webhook:jira",
			"delete:webhook:jira",
			// Required by POST /rest/api/3/webhook to read and validate
			// the jqlFilter in the registration payload. Without these
			// Jira returns 401 "scope does not match" even though the
			// webhook scopes themselves are granted.
			"read:jql:jira",
			"validate:jql:jira",
			"offline_access",
		}
	}

	return &OAuthClient{
		cfg:    cfg,
		log:    log,
		states: make(map[string]oauthState),
	}
}

// SetStateStore installs a Mongo-backed state repository. Once set,
// GenerateAuthURL persists state there and ValidateState reads from it,
// so multiple processes can hand off the OAuth handshake to each other.
// The in-memory map is kept as a fallback if the store is unreachable
// at consume time (e.g. transient Mongo error) — but the canonical
// source is the store when configured.
func (o *OAuthClient) SetStateStore(store *storage.OAuthStateRepo) {
	o.stateStore = store
}

// StartCleanup starts a background goroutine that removes expired OAuth states.
// It stops when the context is cancelled.
func (o *OAuthClient) StartCleanup(ctx context.Context) {
	ticker := time.NewTicker(stateCleanupInt)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				o.cleanExpiredStates(ctx)
			}
		}
	}()
}

func (o *OAuthClient) cleanExpiredStates(ctx context.Context) {
	o.mu.Lock()
	now := time.Now()
	for state, entry := range o.states {
		if now.Sub(entry.createdAt) > stateMaxAge {
			delete(o.states, state)
		}
	}
	o.mu.Unlock()

	if o.stateStore != nil {
		// Best-effort sweep — a TTL index on the collection prunes
		// these regardless, but doing it here keeps the window tight.
		if err := o.stateStore.DeleteExpired(ctx, now.Add(-stateMaxAge)); err != nil {
			o.log.Debug().Err(err).Msg("oauth: failed to sweep expired states from store")
		}
	}
}

func (o *OAuthClient) GenerateAuthURL(state string, telegramUserID int64) string {
	now := time.Now()

	o.mu.Lock()
	o.states[state] = oauthState{
		telegramUserID: telegramUserID,
		createdAt:      now,
	}
	o.mu.Unlock()

	if o.stateStore != nil {
		// Tight timeout so a Mongo hiccup does not stall /connect — the
		// in-memory map already has the state for same-process callbacks.
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := o.stateStore.Save(ctx, state, telegramUserID, now); err != nil {
			o.log.Error().Err(err).
				Int64("telegram_user_id", telegramUserID).
				Msg("oauth: failed to persist state; cross-process callback will fail")
		}
	}

	params := url.Values{
		"audience":      {"api.atlassian.com"},
		"client_id":     {o.cfg.ClientID},
		"scope":         {strings.Join(o.cfg.Scopes, " ")},
		"redirect_uri":  {o.cfg.RedirectURI},
		"state":         {state},
		"response_type": {"code"},
		"prompt":        {"consent"},
	}

	return authURL + "?" + params.Encode()
}

func (o *OAuthClient) ValidateState(state string) (int64, bool) {
	o.mu.Lock()
	entry, hasLocal := o.states[state]
	if hasLocal {
		delete(o.states, state)
	}
	o.mu.Unlock()

	if hasLocal {
		if time.Since(entry.createdAt) > stateMaxAge {
			return 0, false
		}
		// Also drop the persisted copy so a duplicate replay can't
		// consume it later.
		if o.stateStore != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if _, err := o.stateStore.Consume(ctx, state); err != nil && !errors.Is(err, storage.ErrOAuthStateNotFound) {
				o.log.Debug().Err(err).Msg("oauth: failed to drop persisted state after local hit")
			}
		}
		return entry.telegramUserID, true
	}

	if o.stateStore == nil {
		return 0, false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	doc, err := o.stateStore.Consume(ctx, state)
	if err != nil {
		if !errors.Is(err, storage.ErrOAuthStateNotFound) {
			o.log.Error().Err(err).Msg("oauth: failed to consume state from store")
		}
		return 0, false
	}

	if time.Since(doc.CreatedAt) > stateMaxAge {
		return 0, false
	}
	return doc.TelegramUserID, true
}

func (o *OAuthClient) ExchangeCode(ctx context.Context, code string) (*TokenResponse, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {o.cfg.ClientID},
		"client_secret": {o.cfg.ClientSecret},
		"code":          {code},
		"redirect_uri":  {o.cfg.RedirectURI},
	}

	return o.requestToken(ctx, data)
}

func (o *OAuthClient) RefreshTokens(ctx context.Context, refreshToken string) (*TokenResponse, error) {
	data := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {o.cfg.ClientID},
		"client_secret": {o.cfg.ClientSecret},
		"refresh_token": {refreshToken},
	}

	return o.requestToken(ctx, data)
}

func (o *OAuthClient) GetAccessibleResources(ctx context.Context, accessToken string) ([]AccessibleResource, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resourceURL, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("accessible resources request failed: %d %s", resp.StatusCode, string(body))
	}

	var resources []AccessibleResource
	if err := json.NewDecoder(resp.Body).Decode(&resources); err != nil {
		return nil, err
	}

	return resources, nil
}

func (o *OAuthClient) TokenExpiresAt(expiresIn int) time.Time {
	return time.Now().Add(time.Duration(expiresIn) * time.Second)
}

func (o *OAuthClient) requestToken(ctx context.Context, data url.Values) (*TokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		bodyStr := string(body)
		// Jira returns 400/401/403 with "unauthorized_client" or
		// "invalid_grant" when the refresh token has been revoked or
		// the OAuth app credentials changed.
		if strings.Contains(bodyStr, "unauthorized_client") || strings.Contains(bodyStr, "invalid_grant") {
			return nil, fmt.Errorf("token request failed: %d %s: %w", resp.StatusCode, bodyStr, ErrTokenInvalid)
		}
		return nil, fmt.Errorf("token request failed: %d %s", resp.StatusCode, bodyStr)
	}

	var tokenResp TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, err
	}

	return &tokenResp, nil
}
