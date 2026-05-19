package telegram

import (
	"context"
	"errors"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"SleepJiraBot/internal/format"
	"SleepJiraBot/internal/jira"
	"SleepJiraBot/internal/locale"
)

const (
	diagnoseProbeBodyLen = 200
	diagnoseMaxMissing   = 20
	diagnoseMaxGranted   = 60
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

	sb.WriteString("\n")
	switch {
	case len(hybridMarkers) > 0:
		sb.WriteString(locale.T(lang, "admin.diagnose.advice_hybrid"))
	case len(missing) > 0 || user.GrantedScopes == "":
		sb.WriteString(locale.T(lang, "admin.diagnose.advice_reconnect"))
	case !myselfOK || !jqlOK:
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
