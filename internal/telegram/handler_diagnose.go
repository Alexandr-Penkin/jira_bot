package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"SleepJiraBot/internal/format"
	"SleepJiraBot/internal/jira"
	"SleepJiraBot/internal/locale"
	"SleepJiraBot/internal/storage"
)

const (
	diagnoseProbeBodyLen      = 200
	diagnoseMaxMissing        = 20
	diagnoseMaxGranted        = 60
	diagnoseMaxWebhooks       = 10
	diagnoseMaxFailedWebhooks = 5
	diagnoseFailedWebhookCap  = 50
)

// diagnoseClassicScopes are the legacy "classic" Jira OAuth scopes. Their
// presence in a granted-scope string means the token was issued in hybrid
// mode — even one of these flips Atlassian into a state where granular
// endpoint scope checks fail with 401 "scope does not match" on /search/jql
// and similar, despite the granular set being fully granted.
var diagnoseClassicScopes = []string{
	"read:jira-work",
	"write:jira-work",
	"read:jira-user",
	"manage:jira-project",
	"manage:jira-configuration",
	"manage:jira-webhook",
	"manage:jira-data-provider",
}

// handleDiagnose runs an admin-only diagnostic on a Jira connection. With no
// args, diagnoses the admin's own user. With a Telegram user ID arg, looks up
// that user. The output covers: connection state, granted vs. required OAuth
// scopes, and two live API probes (/myself and /search/jql) — these are the
// two endpoints whose failure modes (401 with "scope does not match") are
// what the command exists to surface.
func (h *Handler) handleDiagnose(ctx context.Context, chatID, adminID int64, args string) tgbotapi.MessageConfig {
	lang := h.getLang(ctx, adminID)

	targetID := adminID
	args = strings.TrimSpace(args)
	if args != "" {
		parsed, err := strconv.ParseInt(strings.Fields(args)[0], 10, 64)
		if err != nil {
			return tgbotapi.NewMessage(chatID, locale.T(lang, "admin.diagnose.usage"))
		}
		targetID = parsed
	}

	user, err := h.userRepo.GetByTelegramID(ctx, targetID)
	if err != nil {
		h.log.Error().Err(err).Int64("target_id", targetID).Msg("diagnose: failed to load user")
		return tgbotapi.NewMessage(chatID, locale.T(lang, "error.generic"))
	}
	if user == nil {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "admin.diagnose.user_not_found", targetID))
	}
	if user.AccessToken == "" {
		msg := tgbotapi.NewMessage(chatID, locale.T(lang, "admin.diagnose.not_connected", targetID))
		msg.ParseMode = tgbotapi.ModeMarkdown
		return msg
	}

	var sb strings.Builder
	sb.WriteString(locale.T(lang, "admin.diagnose.header", targetID))
	sb.WriteString("\n\n")

	site := user.JiraSiteURL
	if site == "" {
		site = "—"
	}
	sb.WriteString(locale.T(lang, "admin.diagnose.site", format.EscapeMarkdown(site)))
	sb.WriteString("\n")

	if user.JiraDisplayName != "" {
		sb.WriteString(locale.T(lang, "admin.diagnose.account", format.EscapeMarkdown(user.JiraDisplayName)))
		sb.WriteString("\n")
	}

	if !user.TokenExpiresAt.IsZero() {
		sb.WriteString(locale.T(lang, "admin.diagnose.expires", user.TokenExpiresAt.UTC().Format("2006-01-02 15:04:05 UTC")))
		sb.WriteString("\n")
	}

	required := h.oauth.Scopes()
	missing := diagnoseMissingScopes(user.GrantedScopes, required)
	grantedCount := 0
	if user.GrantedScopes != "" {
		grantedCount = len(strings.Fields(user.GrantedScopes))
	}

	hybridMarkers := diagnoseHybridMarkers(user.GrantedScopes)

	sb.WriteString("\n")
	if user.GrantedScopes == "" {
		sb.WriteString(locale.T(lang, "admin.diagnose.scopes_unknown"))
		sb.WriteString("\n")
	} else {
		sb.WriteString(locale.T(lang, "admin.diagnose.scopes_summary", grantedCount, len(required)))
		sb.WriteString("\n")

		if len(hybridMarkers) > 0 {
			sb.WriteString(locale.T(lang, "admin.diagnose.scopes_hybrid", strings.Join(hybridMarkers, ", ")))
			sb.WriteString("\n")
		}

		if len(missing) == 0 {
			sb.WriteString(locale.T(lang, "admin.diagnose.scopes_ok"))
			sb.WriteString("\n")
		} else {
			shown := missing
			if len(shown) > diagnoseMaxMissing {
				shown = shown[:diagnoseMaxMissing]
			}
			sb.WriteString(locale.T(lang, "admin.diagnose.scopes_missing", len(missing), strings.Join(shown, "\n")))
			sb.WriteString("\n")
		}

		grantedList := strings.Fields(user.GrantedScopes)
		grantedTrimmed := grantedList
		if len(grantedTrimmed) > diagnoseMaxGranted {
			grantedTrimmed = grantedTrimmed[:diagnoseMaxGranted]
		}
		sb.WriteString(locale.T(lang, "admin.diagnose.scopes_list", strings.Join(grantedTrimmed, "\n")))
		sb.WriteString("\n")
	}

	sb.WriteString("\n")
	sb.WriteString(locale.T(lang, "admin.diagnose.probes_header"))
	sb.WriteString("\n")

	myselfOK := h.diagnoseProbe(ctx, &sb, lang, "GET /myself", func() error {
		_, err := h.jiraAPI.GetMyself(ctx, user)
		return err
	})

	jqlOK := h.diagnoseProbe(ctx, &sb, lang, "GET /search/jql", func() error {
		_, err := h.jiraAPI.SearchIssues(ctx, user, "assignee = currentUser()", 1)
		return err
	})

	filterOK := h.diagnoseProbe(ctx, &sb, lang, "GET /filter/my", func() error {
		_, err := h.jiraAPI.GetMyFilters(ctx, user)
		return err
	})

	sb.WriteString("\n")
	h.diagnoseWebhooks(ctx, &sb, lang, user, targetID)

	sb.WriteString("\n")
	switch {
	case len(hybridMarkers) > 0:
		sb.WriteString(locale.T(lang, "admin.diagnose.advice_hybrid"))
	case len(missing) > 0 || user.GrantedScopes == "":
		sb.WriteString(locale.T(lang, "admin.diagnose.advice_reconnect"))
	case !myselfOK || !jqlOK || !filterOK:
		sb.WriteString(locale.T(lang, "admin.diagnose.advice_console"))
	default:
		sb.WriteString(locale.T(lang, "admin.diagnose.advice_ok"))
	}

	msg := tgbotapi.NewMessage(chatID, sb.String())
	msg.ParseMode = tgbotapi.ModeMarkdown
	return msg
}

// diagnoseProbe runs a single API probe and appends a status line. Returns
// true on success so the caller can decide which advice line to print.
func (h *Handler) diagnoseProbe(_ context.Context, sb *strings.Builder, lang locale.Lang, label string, fn func() error) bool {
	err := fn()
	if err == nil {
		sb.WriteString(locale.T(lang, "admin.diagnose.probe_ok", label))
		sb.WriteString("\n")
		return true
	}

	var hErr *jira.HTTPError
	if errors.As(err, &hErr) {
		body := hErr.Body
		if len(body) > diagnoseProbeBodyLen {
			body = body[:diagnoseProbeBodyLen] + "…"
		}
		sb.WriteString(locale.T(lang, "admin.diagnose.probe_fail", label, hErr.Status, format.EscapeMarkdown(body)))
		sb.WriteString("\n")
		return false
	}

	sb.WriteString(locale.T(lang, "admin.diagnose.probe_error", label, format.EscapeMarkdown(err.Error())))
	sb.WriteString("\n")
	return false
}

// diagnoseHybridMarkers returns any classic Jira OAuth scopes found in the
// granted-scope string. A non-empty result means the user's *current* token
// was issued in hybrid mode, regardless of what the Developer Console looks
// like right now — the token only picks up console changes when it is
// re-minted via /disconnect + /connect.
func diagnoseHybridMarkers(granted string) []string {
	if granted == "" {
		return nil
	}
	have := make(map[string]struct{}, 32)
	for _, s := range strings.Fields(granted) {
		have[s] = struct{}{}
	}
	var found []string
	for _, s := range diagnoseClassicScopes {
		if _, ok := have[s]; ok {
			found = append(found, s)
		}
	}
	return found
}

// diagnoseWebhooks reports what dynamic webhooks Jira has registered for
// this user vs. what we have in the local webhook_registrations collection.
// Drift between the two surfaces problems that the counter alone cannot —
// a webhook deleted Jira-side, an expired registration not yet refreshed,
// or a local row whose Jira twin is gone.
func (h *Handler) diagnoseWebhooks(ctx context.Context, sb *strings.Builder, lang locale.Lang, user *storage.User, targetID int64) {
	sb.WriteString(locale.T(lang, "admin.diagnose.webhooks_header"))
	sb.WriteString("\n")

	jiraHooks, err := h.jiraAPI.ListWebhooks(ctx, user)
	if err != nil {
		var hErr *jira.HTTPError
		if errors.As(err, &hErr) {
			body := hErr.Body
			if len(body) > diagnoseProbeBodyLen {
				body = body[:diagnoseProbeBodyLen] + "…"
			}
			sb.WriteString(locale.T(lang, "admin.diagnose.webhooks_jira_fail", hErr.Status, format.EscapeMarkdown(body)))
		} else {
			sb.WriteString(locale.T(lang, "admin.diagnose.webhooks_jira_error", format.EscapeMarkdown(err.Error())))
		}
		sb.WriteString("\n")
		return
	}

	var localHooks []storage.WebhookRegistration
	if h.webhookRepo != nil {
		localHooks, err = h.webhookRepo.GetByUser(ctx, targetID)
		if err != nil {
			h.log.Warn().Err(err).Int64("target_id", targetID).Msg("diagnose: failed to read local webhook registrations")
		}
	}

	sb.WriteString(locale.T(lang, "admin.diagnose.webhooks_count", len(jiraHooks), len(localHooks)))
	sb.WriteString("\n")

	// Drift detection by webhook ID — Jira-side that's not in our DB is a
	// registration we forgot about; local-side that's not in Jira is a
	// stale row we should clean up.
	jiraIDs := make(map[int64]struct{}, len(jiraHooks))
	for i := range jiraHooks {
		jiraIDs[jiraHooks[i].ID] = struct{}{}
	}
	localIDs := make(map[int64]struct{}, len(localHooks))
	for i := range localHooks {
		localIDs[localHooks[i].WebhookID] = struct{}{}
	}
	var onlyJira, onlyLocal []int64
	for id := range jiraIDs {
		if _, ok := localIDs[id]; !ok {
			onlyJira = append(onlyJira, id)
		}
	}
	for id := range localIDs {
		if _, ok := jiraIDs[id]; !ok {
			onlyLocal = append(onlyLocal, id)
		}
	}
	if len(onlyJira) > 0 {
		sb.WriteString(locale.T(lang, "admin.diagnose.webhooks_only_jira", formatWebhookIDs(onlyJira)))
		sb.WriteString("\n")
	}
	if len(onlyLocal) > 0 {
		sb.WriteString(locale.T(lang, "admin.diagnose.webhooks_only_local", formatWebhookIDs(onlyLocal)))
		sb.WriteString("\n")
	}

	if len(jiraHooks) == 0 {
		sb.WriteString(locale.T(lang, "admin.diagnose.webhooks_none"))
		sb.WriteString("\n")
		return
	}

	shown := jiraHooks
	if len(shown) > diagnoseMaxWebhooks {
		shown = shown[:diagnoseMaxWebhooks]
	}
	for i := range shown {
		wh := &shown[i]
		expires := wh.ExpirationDate
		// Jira returns expirationDate as a millisecond epoch string in
		// newer responses and an ISO-8601 string in older ones; render
		// the epoch form as a readable UTC date when we can parse it.
		if ms, parseErr := strconv.ParseInt(wh.ExpirationDate, 10, 64); parseErr == nil {
			expires = time.UnixMilli(ms).UTC().Format("2006-01-02 15:04 UTC")
		}
		jql := wh.JqlFilter
		if jql == "" {
			jql = "—"
		}
		sb.WriteString(locale.T(lang, "admin.diagnose.webhooks_entry",
			wh.ID,
			format.EscapeMarkdown(expires),
			format.EscapeMarkdown(strings.Join(wh.Events, ", ")),
			format.EscapeMarkdown(jql),
		))
		sb.WriteString("\n")
	}
	if len(jiraHooks) > diagnoseMaxWebhooks {
		sb.WriteString(locale.T(lang, "admin.diagnose.webhooks_truncated", len(jiraHooks)-diagnoseMaxWebhooks))
		sb.WriteString("\n")
	}

	sb.WriteString("\n")
	h.diagnoseFailedWebhooks(ctx, sb, lang, user)
}

// diagnoseFailedWebhooks queries Jira's failed-webhook log to surface
// delivery attempts that did not receive a 2xx from our receiver. Atlassian
// keeps these for ~30 days. Zero entries + zero in-process events means
// Jira simply did not fire anything matching our JQL — the bug is "the
// change you made does not match the webhook filter", not the transport.
func (h *Handler) diagnoseFailedWebhooks(ctx context.Context, sb *strings.Builder, lang locale.Lang, user *storage.User) {
	sb.WriteString(locale.T(lang, "admin.diagnose.failed_webhooks_header"))
	sb.WriteString("\n")

	failed, err := h.jiraAPI.ListFailedWebhooks(ctx, user, diagnoseFailedWebhookCap)
	if err != nil {
		var hErr *jira.HTTPError
		if errors.As(err, &hErr) {
			body := hErr.Body
			if len(body) > diagnoseProbeBodyLen {
				body = body[:diagnoseProbeBodyLen] + "…"
			}
			sb.WriteString(locale.T(lang, "admin.diagnose.failed_webhooks_jira_fail", hErr.Status, format.EscapeMarkdown(body)))
		} else {
			sb.WriteString(locale.T(lang, "admin.diagnose.failed_webhooks_jira_error", format.EscapeMarkdown(err.Error())))
		}
		sb.WriteString("\n")
		return
	}

	if len(failed) == 0 {
		sb.WriteString(locale.T(lang, "admin.diagnose.failed_webhooks_none"))
		sb.WriteString("\n")
		return
	}

	sb.WriteString(locale.T(lang, "admin.diagnose.failed_webhooks_count", len(failed)))
	sb.WriteString("\n")

	shown := failed
	if len(shown) > diagnoseMaxFailedWebhooks {
		shown = shown[:diagnoseMaxFailedWebhooks]
	}
	for i := range shown {
		fw := &shown[i]
		when := time.UnixMilli(fw.FailureTime).UTC().Format("2006-01-02 15:04:05 UTC")
		sb.WriteString(locale.T(lang, "admin.diagnose.failed_webhooks_entry",
			format.EscapeMarkdown(fw.ID),
			format.EscapeMarkdown(when),
			format.EscapeMarkdown(fw.URL),
		))
		sb.WriteString("\n")
	}
	if len(failed) > diagnoseMaxFailedWebhooks {
		sb.WriteString(locale.T(lang, "admin.diagnose.failed_webhooks_truncated",
			len(failed)-diagnoseMaxFailedWebhooks, diagnoseFailedWebhookCap))
		sb.WriteString("\n")
	}
}

func formatWebhookIDs(ids []int64) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, fmt.Sprintf("%d", id))
	}
	return strings.Join(parts, ", ")
}

// handleWebhooksResync reconciles the user's webhook_registrations rows
// with what Jira actually has. With no arg it resyncs the admin; with a
// telegram_user_id arg it resyncs that user. Drift typically appears
// after a /disconnect that could not reach Jira (orphan hooks remain)
// or a registration whose DB write failed silently.
func (h *Handler) handleWebhooksResync(ctx context.Context, chatID, adminID int64, args string) tgbotapi.MessageConfig {
	lang := h.getLang(ctx, adminID)

	if h.webhookMgr == nil || h.subRepo == nil {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "error.generic"))
	}

	targetID := adminID
	args = strings.TrimSpace(args)
	if args != "" {
		parsed, err := strconv.ParseInt(strings.Fields(args)[0], 10, 64)
		if err != nil {
			return tgbotapi.NewMessage(chatID, locale.T(lang, "admin.webhooks_resync.usage"))
		}
		targetID = parsed
	}

	subs, err := h.subRepo.GetActiveByUser(ctx, targetID)
	if err != nil {
		h.log.Error().Err(err).Int64("target_id", targetID).Msg("webhooks_resync: failed to load subscriptions")
		return tgbotapi.NewMessage(chatID, locale.T(lang, "error.generic"))
	}

	result, err := h.webhookMgr.ResyncForUser(ctx, targetID, subs)
	if err != nil {
		return tgbotapi.NewMessage(chatID,
			locale.T(lang, "admin.webhooks_resync.failed", format.EscapeMarkdown(err.Error())))
	}

	var sb strings.Builder
	sb.WriteString(locale.T(lang, "admin.webhooks_resync.header", targetID))
	sb.WriteString("\n\n")
	sb.WriteString(locale.T(lang, "admin.webhooks_resync.summary",
		result.JiraCount, result.LocalCount,
		len(result.Adopted), len(result.DroppedLocal), len(result.Unmatched)))
	sb.WriteString("\n")

	if len(result.Adopted) > 0 {
		sb.WriteString("\n")
		sb.WriteString(locale.T(lang, "admin.webhooks_resync.adopted", formatWebhookIDs(result.Adopted)))
	}
	if len(result.DroppedLocal) > 0 {
		sb.WriteString("\n")
		sb.WriteString(locale.T(lang, "admin.webhooks_resync.dropped", formatWebhookIDs(result.DroppedLocal)))
	}
	if len(result.Unmatched) > 0 {
		sb.WriteString("\n")
		sb.WriteString(locale.T(lang, "admin.webhooks_resync.unmatched", formatWebhookIDs(result.Unmatched)))
	}

	msg := tgbotapi.NewMessage(chatID, sb.String())
	msg.ParseMode = tgbotapi.ModeMarkdown
	return msg
}

// diagnoseMissingScopes returns scopes present in required but absent from
// the space-separated granted string. Order follows required so the output
// matches the source-of-truth list in oauth.go.
func diagnoseMissingScopes(granted string, required []string) []string {
	if granted == "" {
		return append([]string(nil), required...)
	}
	have := make(map[string]struct{}, 32)
	for _, s := range strings.Fields(granted) {
		have[s] = struct{}{}
	}
	var missing []string
	for _, s := range required {
		if _, ok := have[s]; !ok {
			missing = append(missing, s)
		}
	}
	return missing
}
