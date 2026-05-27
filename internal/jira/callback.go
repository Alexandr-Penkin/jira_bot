package jira

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"SleepJiraBot/internal/format"
	"SleepJiraBot/internal/locale"
	"SleepJiraBot/internal/middleware"
	"SleepJiraBot/internal/storage"
	eventsv1 "SleepJiraBot/pkg/events/v1"
)

const pendingSiteMaxAge = 10 * time.Minute

// PendingSiteSelection holds OAuth tokens and available Jira sites while
// the user picks which site to connect to.
type PendingSiteSelection struct {
	TokenResponse *TokenResponse       `json:"token_response"`
	Resources     []AccessibleResource `json:"resources"`
	CreatedAt     time.Time            `json:"created_at"`
}

// PendingSiteStore persists at most one pending multi-site selection per
// Telegram user. It deals in opaque JSON payloads so the storage layer
// stays free of any jira type dependency. The OAuth callback (cmd/bot)
// writes; the Telegram site-selection button handler (telegram-svc)
// reads — hence a shared Mongo-backed store rather than process memory.
type PendingSiteStore interface {
	Save(ctx context.Context, telegramUserID int64, payload string, createdAt time.Time) error
	Consume(ctx context.Context, telegramUserID int64) (payload string, createdAt time.Time, err error)
}

// SiteConnector owns the post-OAuth account-linking logic shared between
// the HTTP callback (single-site finalize + multi-site staging) and the
// Telegram site-selection button (consume + finalize). Both hosts
// construct one against the same Mongo pending store so multi-site
// selection works regardless of which process serves which step.
type SiteConnector struct {
	oauth      *OAuthClient
	userRepo   *storage.UserRepo
	subRepo    *storage.SubscriptionRepo
	webhookMgr *WebhookManager
	pending    PendingSiteStore
	tgAPI      *tgbotapi.BotAPI
	pub        eventsv1.Publisher
	log        zerolog.Logger
}

func NewSiteConnector(
	oauth *OAuthClient,
	userRepo *storage.UserRepo,
	subRepo *storage.SubscriptionRepo,
	webhookMgr *WebhookManager,
	pending PendingSiteStore,
	tgAPI *tgbotapi.BotAPI,
	log zerolog.Logger,
) *SiteConnector {
	return &SiteConnector{
		oauth:      oauth,
		userRepo:   userRepo,
		subRepo:    subRepo,
		webhookMgr: webhookMgr,
		pending:    pending,
		tgAPI:      tgAPI,
		pub:        eventsv1.NoopPublisher{},
		log:        log,
	}
}

// SetEventPublisher installs a domain event publisher. Finalize emits
// UserAuthenticated alongside the Upsert.
func (sc *SiteConnector) SetEventPublisher(p eventsv1.Publisher) {
	if p == nil {
		sc.pub = eventsv1.NoopPublisher{}
		return
	}
	sc.pub = p
}

// UserLang resolves the user's preferred language for OAuth-flow messages,
// falling back to the default when the user is unknown.
func (sc *SiteConnector) UserLang(ctx context.Context, telegramUserID int64) locale.Lang {
	user, err := sc.userRepo.GetByTelegramID(ctx, telegramUserID)
	if err != nil || user == nil {
		return locale.Default
	}
	return locale.FromString(user.Language)
}

// StorePending stages a multi-site selection for the user. The payload
// (which carries the freshly-issued tokens) is encrypted at rest by the
// store implementation.
func (sc *SiteConnector) StorePending(ctx context.Context, telegramUserID int64, token *TokenResponse, resources []AccessibleResource) error {
	now := time.Now()
	payload, err := json.Marshal(PendingSiteSelection{
		TokenResponse: token,
		Resources:     resources,
		CreatedAt:     now,
	})
	if err != nil {
		return err
	}
	return sc.pending.Save(ctx, telegramUserID, string(payload), now)
}

// ConsumePending retrieves and removes a pending selection. Returns
// (nil, nil) when none exists or it has expired, so the caller can show a
// "selection expired" message; a non-nil error signals a real failure.
func (sc *SiteConnector) ConsumePending(ctx context.Context, telegramUserID int64) (*PendingSiteSelection, error) {
	payload, createdAt, err := sc.pending.Consume(ctx, telegramUserID)
	if errors.Is(err, storage.ErrOAuthPendingNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if time.Since(createdAt) > pendingSiteMaxAge {
		return nil, nil
	}
	var pending PendingSiteSelection
	if err := json.Unmarshal([]byte(payload), &pending); err != nil {
		return nil, err
	}
	pending.CreatedAt = createdAt
	return &pending, nil
}

// Finalize persists the selected Jira site for the user, (re)registers
// webhooks for existing subscriptions, publishes UserAuthenticated, and
// sends a Telegram confirmation.
func (sc *SiteConnector) Finalize(ctx context.Context, telegramUserID int64, tokenResp *TokenResponse, resource AccessibleResource) error {
	accountID := ""
	displayName := ""
	if myself, myselfErr := fetchMyself(ctx, resource.ID, tokenResp.AccessToken); myselfErr == nil {
		accountID = myself.AccountID
		displayName = myself.DisplayName
	} else {
		sc.log.Warn().Err(myselfErr).Msg("failed to fetch Jira account ID during OAuth")
	}

	user := &storage.User{
		TelegramUserID:  telegramUserID,
		JiraCloudID:     resource.ID,
		JiraAccountID:   accountID,
		JiraDisplayName: displayName,
		JiraSiteURL:     resource.URL,
		AccessToken:     tokenResp.AccessToken,
		RefreshToken:    tokenResp.RefreshToken,
		TokenExpiresAt:  sc.oauth.TokenExpiresAt(tokenResp.ExpiresIn),
		GrantedScopes:   tokenResp.Scope,
	}

	if err := sc.userRepo.Upsert(ctx, user); err != nil {
		return err
	}

	sc.log.Info().
		Int64("telegram_user_id", telegramUserID).
		Str("jira_site", resource.Name).
		Msg("user connected to Jira")

	if pubErr := sc.pub.Publish(ctx, eventsv1.UserAuthenticated{
		TelegramID:      telegramUserID,
		JiraAccountID:   accountID,
		CloudID:         resource.ID,
		SiteURL:         resource.URL,
		AuthenticatedAt: time.Now().Unix(),
	}, ""); pubErr != nil {
		sc.log.Warn().Err(pubErr).Int64("telegram_user_id", telegramUserID).Msg("publish user_authenticated failed")
	}

	// Re-fetch the persisted user so the webhook manager gets a copy
	// with decrypted tokens (Upsert leaves the input struct's tokens
	// untouched but requires the repo's decrypt path).
	if sc.subRepo != nil && sc.webhookMgr != nil {
		if subs, subErr := sc.subRepo.GetActiveByUser(ctx, telegramUserID); subErr == nil && len(subs) > 0 {
			sc.webhookMgr.RegisterForExistingSubscriptions(ctx, telegramUserID, subs)
		} else if subErr != nil {
			sc.log.Warn().Err(subErr).Int64("user_id", telegramUserID).Msg("failed to read subscriptions for webhook registration")
		}
	}

	if sc.tgAPI != nil {
		lang := sc.UserLang(ctx, telegramUserID)
		msg := tgbotapi.NewMessage(telegramUserID, locale.T(lang, "connect.success", format.EscapeMarkdown(resource.Name)))
		msg.ParseMode = tgbotapi.ModeMarkdown
		if _, err := sc.tgAPI.Send(msg); err != nil {
			sc.log.Error().Err(err).Msg("failed to send connect confirmation")
		}
	}

	return nil
}

// CallbackServer serves the OAuth 2.0 redirect endpoint plus the static
// web pages. Account-linking logic lives in SiteConnector so the Telegram
// site-selection button (hosted in a different process) can finalize a
// connection started here.
type CallbackServer struct {
	oauth     *OAuthClient
	connector *SiteConnector
	tgAPI     *tgbotapi.BotAPI
	log       zerolog.Logger
	server    *http.Server
	mux       *http.ServeMux
}

func NewCallbackServer(ctx context.Context, addr string, oauth *OAuthClient, connector *SiteConnector, tgAPI *tgbotapi.BotAPI, log zerolog.Logger) *CallbackServer {
	cs := &CallbackServer{
		oauth:     oauth,
		connector: connector,
		tgAPI:     tgAPI,
		log:       log,
	}

	callbackRL := middleware.NewRateLimiter(10, 20, time.Minute, ctx)
	callbackRL.SetLogger(log)

	cs.mux = http.NewServeMux()
	cs.mux.Handle("/callback", callbackRL.WrapFunc(cs.handleCallback))
	cs.mux.HandleFunc("/health", cs.handleHealth)

	cs.server = &http.Server{
		Addr:              addr,
		Handler:           otelhttp.NewHandler(cs.mux, "bot.callback"),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	return cs
}

// Handle registers an additional handler on the callback server's mux.
func (cs *CallbackServer) Handle(pattern string, handler http.Handler) {
	cs.mux.Handle(pattern, handler)
}

// HandleFunc registers a function as a handler on the callback server's mux.
func (cs *CallbackServer) HandleFunc(pattern string, handler http.HandlerFunc) {
	cs.mux.HandleFunc(pattern, handler)
}

func (cs *CallbackServer) Start() error {
	cs.log.Info().Str("addr", cs.server.Addr).Msg("starting OAuth callback server")
	return cs.server.ListenAndServe()
}

func (cs *CallbackServer) Shutdown(ctx context.Context) error {
	return cs.server.Shutdown(ctx)
}

func (cs *CallbackServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	// A bare GET with no query params is almost certainly an operator
	// poking the URL by hand to check the endpoint is alive — show a
	// human-readable status page instead of a confusing 400.
	if code == "" && state == "" && r.Method == http.MethodGet && r.URL.RawQuery == "" {
		writeStatusPage(w, "SleepJiraBot — OAuth callback",
			"This endpoint receives the OAuth 2.0 redirect from Atlassian after a user approves the Sleep Jira Bot. Visit it manually only for diagnostics — the real flow is triggered by the /connect command in Telegram.")
		return
	}

	if code == "" || state == "" {
		http.Error(w, "missing code or state", http.StatusBadRequest)
		return
	}

	telegramUserID, ok := cs.oauth.ValidateState(state)
	if !ok {
		http.Error(w, "invalid or expired state", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	tokenResp, err := cs.oauth.ExchangeCode(ctx, code)
	if err != nil {
		cs.log.Error().Err(err).Msg("failed to exchange code for token")
		http.Error(w, "authorization failed", http.StatusInternalServerError)
		return
	}

	// Atlassian silently drops scopes that aren't enabled for the OAuth
	// app in the developer console — surface the actually-granted set so
	// missing-scope failures (e.g. "scope does not match" on /webhook)
	// can be diagnosed by reading the callback log.
	cs.log.Info().
		Int64("telegram_user_id", telegramUserID).
		Str("granted_scopes", tokenResp.Scope).
		Msg("oauth: token exchange successful")

	resources, err := cs.oauth.GetAccessibleResources(ctx, tokenResp.AccessToken)
	if err != nil {
		cs.log.Error().Err(err).Msg("failed to get accessible resources")
		http.Error(w, "failed to get Jira sites", http.StatusInternalServerError)
		return
	}

	if len(resources) == 0 {
		http.Error(w, "no Jira sites found for this account", http.StatusBadRequest)
		return
	}

	if len(resources) > 1 {
		if err := cs.connector.StorePending(ctx, telegramUserID, tokenResp, resources); err != nil {
			cs.log.Error().Err(err).Msg("failed to stage pending site selection")
			http.Error(w, "failed to save authorization", http.StatusInternalServerError)
			return
		}
		lang := cs.connector.UserLang(ctx, telegramUserID)

		rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(resources))
		for i, res := range resources {
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(
					res.Name+" ("+res.URL+")",
					"site_select:"+strconv.Itoa(i),
				),
			))
		}

		msg := tgbotapi.NewMessage(telegramUserID, locale.T(lang, "connect.choose_site"))
		msg.ParseMode = tgbotapi.ModeMarkdown
		msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
		if _, sendErr := cs.tgAPI.Send(msg); sendErr != nil {
			cs.log.Error().Err(sendErr).Msg("failed to send site selection message")
		}

		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, `<!DOCTYPE html><html><body><h2>Almost done!</h2><p>Please choose your Jira site in Telegram.</p></body></html>`)
		return
	}

	resource := resources[0]
	if err = cs.connector.Finalize(ctx, telegramUserID, tokenResp, resource); err != nil {
		cs.log.Error().Err(err).Msg("failed to finalize connection")
		http.Error(w, "failed to save authorization", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	_, _ = fmt.Fprint(w, `<!DOCTYPE html><html><body><h2>Authorization successful!</h2><p>You can close this tab and return to Telegram.</p></body></html>`)
}

func (cs *CallbackServer) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, "ok")
}

// fetchMyself calls the Jira /myself endpoint to get the current user's account ID.
func fetchMyself(ctx context.Context, cloudID, accessToken string) (*JiraUser, error) {
	myselfURL := fmt.Sprintf("https://api.atlassian.com/ex/jira/%s/rest/api/3/myself", cloudID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, myselfURL, http.NoBody)
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

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("myself request failed: %d %s", resp.StatusCode, string(body))
	}

	var user JiraUser
	if err := json.Unmarshal(body, &user); err != nil {
		return nil, err
	}
	return &user, nil
}

// writeStatusPage renders a small operator-facing HTML page so a manual
// browser visit to /callback shows a clear "the endpoint is alive"
// confirmation instead of a raw 400.
func writeStatusPage(w http.ResponseWriter, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>%s</title>
<style>body{font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif;max-width:640px;margin:48px auto;padding:0 16px;color:#222}
h1{font-size:1.4rem;margin-bottom:.3rem}p{line-height:1.5;color:#444}.ok{color:#0a7d3b;font-weight:600}</style>
</head><body><h1>%s</h1><p class="ok">&#x2705; Endpoint is up</p><p>%s</p></body></html>`,
		title, title, body)
}
