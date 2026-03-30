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

	"github.com/rs/zerolog"
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
			"offline_access",
		}
	}

	return &OAuthClient{
		cfg:    cfg,
		log:    log,
		states: make(map[string]oauthState),
	}
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
				o.cleanExpiredStates()
			}
		}
	}()
}

func (o *OAuthClient) cleanExpiredStates() {
	o.mu.Lock()
	defer o.mu.Unlock()

	now := time.Now()
	for state, entry := range o.states {
		if now.Sub(entry.createdAt) > stateMaxAge {
			delete(o.states, state)
		}
	}
}

func (o *OAuthClient) GenerateAuthURL(state string, telegramUserID int64) string {
	o.mu.Lock()
	o.states[state] = oauthState{
		telegramUserID: telegramUserID,
		createdAt:      time.Now(),
	}
	o.mu.Unlock()

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
	entry, ok := o.states[state]
	if ok {
		delete(o.states, state)
	}
	o.mu.Unlock()

	if !ok {
		return 0, false
	}

	if time.Since(entry.createdAt) > stateMaxAge {
		return 0, false
	}

	return entry.telegramUserID, true
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
		return nil, fmt.Errorf("token request failed: %d %s", resp.StatusCode, string(body))
	}

	var tokenResp TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, err
	}

	return &tokenResp, nil
}
