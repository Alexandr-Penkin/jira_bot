package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/rs/zerolog"

	"SleepJiraBot/internal/format"
	"SleepJiraBot/internal/locale"
	"SleepJiraBot/internal/middleware"
	"SleepJiraBot/internal/storage"
)

const pendingSiteMaxAge = 10 * time.Minute

// PendingSiteSelection holds OAuth tokens and available Jira sites while
// the user picks which site to connect to.
type PendingSiteSelection struct {
	TokenResponse *TokenResponse
	Resources     []AccessibleResource
	CreatedAt     time.Time
}

type CallbackServer struct {
	oauth        *OAuthClient
	userRepo     *storage.UserRepo
	subRepo      *storage.SubscriptionRepo
	webhookMgr   *WebhookManager
	tgAPI        *tgbotapi.BotAPI
	log          zerolog.Logger
	server       *http.Server
	mux          *http.ServeMux
	pendingMu    sync.Mutex
	pendingSites map[int64]*PendingSiteSelection // keyed by TelegramUserID
}

func NewCallbackServer(ctx context.Context, addr string, oauth *OAuthClient, userRepo *storage.UserRepo, subRepo *storage.SubscriptionRepo, webhookMgr *WebhookManager, tgAPI *tgbotapi.BotAPI, log zerolog.Logger) *CallbackServer {
	cs := &CallbackServer{
		oauth:        oauth,
		userRepo:     userRepo,
		subRepo:      subRepo,
		webhookMgr:   webhookMgr,
		tgAPI:        tgAPI,
		log:          log,
		pendingSites: make(map[int64]*PendingSiteSelection),
	}

	callbackRL := middleware.NewRateLimiter(10, 20, time.Minute, ctx)
	callbackRL.SetLogger(log)

	cs.mux = http.NewServeMux()
	cs.mux.Handle("/callback", callbackRL.WrapFunc(cs.handleCallback))
	cs.mux.HandleFunc("/health", cs.handleHealth)

	cs.server = &http.Server{
		Addr:              addr,
		Handler:           cs.mux,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go cs.cleanupPendingSites(ctx)

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
		cs.storePendingSite(telegramUserID, tokenResp, resources)
		lang := cs.getUserLang(ctx, telegramUserID)

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
	if err = cs.finalizeSiteConnection(ctx, telegramUserID, tokenResp, resource); err != nil {
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

func (cs *CallbackServer) getUserLang(ctx context.Context, telegramUserID int64) locale.Lang {
	user, err := cs.userRepo.GetByTelegramID(ctx, telegramUserID)
	if err != nil || user == nil {
		return locale.Default
	}
	return locale.FromString(user.Language)
}

func (cs *CallbackServer) cleanupPendingSites(ctx context.Context) {
	ticker := time.NewTicker(pendingSiteMaxAge)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cs.pendingMu.Lock()
			for id, p := range cs.pendingSites {
				if time.Since(p.CreatedAt) > pendingSiteMaxAge {
					delete(cs.pendingSites, id)
				}
			}
			cs.pendingMu.Unlock()
		}
	}
}

func (cs *CallbackServer) storePendingSite(telegramUserID int64, token *TokenResponse, resources []AccessibleResource) {
	cs.pendingMu.Lock()
	defer cs.pendingMu.Unlock()
	cs.pendingSites[telegramUserID] = &PendingSiteSelection{
		TokenResponse: token,
		Resources:     resources,
		CreatedAt:     time.Now(),
	}
}

// ConsumePendingSite retrieves and removes a pending site selection for the
// given user. Returns nil if no pending selection exists or if it has expired.
func (cs *CallbackServer) ConsumePendingSite(telegramUserID int64) *PendingSiteSelection {
	cs.pendingMu.Lock()
	defer cs.pendingMu.Unlock()
	pending, ok := cs.pendingSites[telegramUserID]
	if !ok {
		return nil
	}
	delete(cs.pendingSites, telegramUserID)
	if time.Since(pending.CreatedAt) > pendingSiteMaxAge {
		return nil
	}
	return pending
}

// FinalizeSiteConnection saves the selected Jira site for the user and sends
// a confirmation message in Telegram.
func (cs *CallbackServer) FinalizeSiteConnection(ctx context.Context, telegramUserID int64, tokenResp *TokenResponse, resource AccessibleResource) error {
	return cs.finalizeSiteConnection(ctx, telegramUserID, tokenResp, resource)
}

func (cs *CallbackServer) finalizeSiteConnection(ctx context.Context, telegramUserID int64, tokenResp *TokenResponse, resource AccessibleResource) error {
	accountID := ""
	displayName := ""
	if myself, myselfErr := fetchMyself(ctx, resource.ID, tokenResp.AccessToken); myselfErr == nil {
		accountID = myself.AccountID
		displayName = myself.DisplayName
	} else {
		cs.log.Warn().Err(myselfErr).Msg("failed to fetch Jira account ID during OAuth")
	}

	user := &storage.User{
		TelegramUserID:  telegramUserID,
		JiraCloudID:     resource.ID,
		JiraAccountID:   accountID,
		JiraDisplayName: displayName,
		JiraSiteURL:     resource.URL,
		AccessToken:     tokenResp.AccessToken,
		RefreshToken:    tokenResp.RefreshToken,
		TokenExpiresAt:  cs.oauth.TokenExpiresAt(tokenResp.ExpiresIn),
	}

	if err := cs.userRepo.Upsert(ctx, user); err != nil {
		return err
	}

	cs.log.Info().
		Int64("telegram_user_id", telegramUserID).
		Str("jira_site", resource.Name).
		Msg("user connected to Jira")

	// Re-fetch the persisted user so the webhook manager gets a copy
	// with decrypted tokens (Upsert leaves the input struct's tokens
	// untouched but requires the repo's decrypt path).
	if cs.subRepo != nil && cs.webhookMgr != nil {
		if subs, subErr := cs.subRepo.GetActiveByUser(ctx, telegramUserID); subErr == nil && len(subs) > 0 {
			cs.webhookMgr.RegisterForExistingSubscriptions(ctx, telegramUserID, subs)
		} else if subErr != nil {
			cs.log.Warn().Err(subErr).Int64("user_id", telegramUserID).Msg("failed to read subscriptions for webhook registration")
		}
	}

	lang := cs.getUserLang(ctx, telegramUserID)
	msg := tgbotapi.NewMessage(telegramUserID, locale.T(lang, "connect.success", format.EscapeMarkdown(resource.Name)))
	msg.ParseMode = tgbotapi.ModeMarkdown
	if _, err := cs.tgAPI.Send(msg); err != nil {
		cs.log.Error().Err(err).Msg("failed to send connect confirmation")
	}

	return nil
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
