package telegram

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"SleepJiraBot/internal/format"
	"SleepJiraBot/internal/jira"
	"SleepJiraBot/internal/locale"
	"SleepJiraBot/internal/storage"
)

const (
	dailyMaxResults     = 50
	userSearchMaxResult = 10
)

var jiraAccountIDRe = regexp.MustCompile(`^[a-zA-Z0-9:_-]+$`)

// handleDaily generates a daily standup for the current user.
func (h *Handler) handleDaily(ctx context.Context, chatID, userID int64) tgbotapi.MessageConfig {
	lang := h.getLang(ctx, userID)

	user, err := h.requireAuth(ctx, userID)
	if err != nil {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected"))
	}

	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")

	doneJQL := user.DailyDoneJQL
	if doneJQL == "" {
		doneJQL = fmt.Sprintf(
			"\"Developer[User Picker (single user)]\" = currentUser() AND updated >= %q AND status IN (Closed, \"Code review\", \"Ready for testing\", \"Ready for release\") ORDER BY updated DESC",
			yesterday,
		)
	}
	doingJQL := user.DailyDoingJQL
	if doingJQL == "" {
		doingJQL = "\"Developer[User Picker (single user)]\" = currentUser() AND status = \"In Progress\" ORDER BY updated DESC"
	}
	planJQL := user.DailyPlanJQL
	if planJQL == "" {
		planJQL = "assignee = currentUser() AND status = \"Ready for development\" AND type IN (\"Dev Task\", Bug) ORDER BY updated DESC"
	}

	return h.buildDailyMessage(ctx, chatID, lang, user, "", doneJQL, doingJQL, planJQL)
}

// handleDailyUser generates a daily standup for a specific Jira user by accountId.
func (h *Handler) handleDailyUser(ctx context.Context, chatID, userID int64, accountID, displayName string) tgbotapi.MessageConfig {
	lang := h.getLang(ctx, userID)

	if !jiraAccountIDRe.MatchString(accountID) {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "daily.user_not_found"))
	}

	user, err := h.requireAuth(ctx, userID)
	if err != nil {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected"))
	}

	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")

	doneJQL := fmt.Sprintf(
		"status changed BY %q AFTER %q ORDER BY updated DESC",
		accountID, yesterday,
	)
	doingJQL := fmt.Sprintf(
		"assignee=%q AND statusCategory=\"In Progress\" ORDER BY updated DESC",
		accountID,
	)

	return h.buildDailyMessage(ctx, chatID, lang, user, displayName, doneJQL, doingJQL, "")
}

func (h *Handler) buildDailyMessage(ctx context.Context, chatID int64, lang locale.Lang, user *storage.User, displayName, doneJQL, doingJQL, planJQL string) tgbotapi.MessageConfig {
	doneResult, doneErr := h.jiraAPI.SearchIssues(ctx, user, doneJQL, dailyMaxResults)
	doingResult, doingErr := h.jiraAPI.SearchIssues(ctx, user, doingJQL, dailyMaxResults)

	var planResult *jira.SearchResult
	var planErr error
	if planJQL != "" {
		planResult, planErr = h.jiraAPI.SearchIssues(ctx, user, planJQL, dailyMaxResults)
	}

	if doneErr != nil && doingErr != nil && (planJQL == "" || planErr != nil) {
		h.log.Error().Err(doneErr).Msg("daily: failed to search done issues")
		h.log.Error().Err(doingErr).Msg("daily: failed to search in-progress issues")
		return tgbotapi.NewMessage(chatID, locale.T(lang, "daily.failed"))
	}

	text := formatDaily(lang, user.JiraSiteURL, displayName, doneResult, doingResult, planResult)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.DisableWebPagePreview = true
	return msg
}

// handleDailySearch searches for Jira users and shows a selection keyboard.
func (h *Handler) handleDailySearch(ctx context.Context, chatID, userID int64, query string) tgbotapi.MessageConfig {
	lang := h.getLang(ctx, userID)

	user, err := h.requireAuth(ctx, userID)
	if err != nil {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected"))
	}

	users, err := h.jiraAPI.SearchUsers(ctx, user, query, userSearchMaxResult)
	if err != nil {
		h.log.Error().Err(err).Str("query", query).Msg("daily: failed to search users")
		return tgbotapi.NewMessage(chatID, locale.T(lang, "daily.search_failed"))
	}

	// Filter only active users.
	active := users[:0]
	for _, u := range users {
		if u.Active {
			active = append(active, u)
		}
	}

	if len(active) == 0 {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "daily.user_not_found"))
	}

	// Single match — show daily immediately.
	if len(active) == 1 {
		return h.handleDailyUser(ctx, chatID, userID, active[0].AccountID, active[0].DisplayName)
	}

	// Multiple matches — show selection keyboard.
	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(active))
	for _, u := range active {
		data := fmt.Sprintf("daily:%s", u.AccountID)
		label := u.DisplayName
		if u.Email != "" {
			label += " (" + u.Email + ")"
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, data),
		))
	}

	msg := tgbotapi.NewMessage(chatID, locale.T(lang, "daily.choose_user"))
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	return msg
}

// handleDailyCallback handles user selection from the daily search keyboard.
func (h *Handler) handleDailyCallback(ctx context.Context, cq *tgbotapi.CallbackQuery, parts []string) {
	_, _ = h.api.Request(tgbotapi.NewCallback(cq.ID, ""))
	lang := h.getLang(ctx, cq.From.ID)

	if len(parts) < 2 || parts[1] == "" {
		return
	}

	accountID := parts[1]

	user, err := h.requireAuth(ctx, cq.From.ID)
	if err != nil {
		msg := tgbotapi.NewMessage(cq.Message.Chat.ID, locale.T(lang, "error.not_connected"))
		h.sendMessage(msg)
		return
	}

	// Look up display name for the header.
	displayName := ""
	users, err := h.jiraAPI.SearchUsers(ctx, user, accountID, 1)
	if err == nil && len(users) > 0 {
		displayName = users[0].DisplayName
	}

	msg := h.handleDailyUser(ctx, cq.Message.Chat.ID, cq.From.ID, accountID, displayName)
	h.sendMessage(withMenuButton(msg, lang))
}

func formatDaily(lang locale.Lang, siteURL, displayName string, done, doing, plan *jira.SearchResult) string {
	var sb strings.Builder

	if displayName != "" {
		fmt.Fprintf(&sb, "#daily (%s)\n", format.EscapeMarkdown(displayName))
	} else {
		sb.WriteString("#daily\n")
	}

	// Done
	sb.WriteString("\n")
	sb.WriteString(locale.T(lang, "daily.done"))
	sb.WriteString(":\n")
	if done == nil || len(done.Issues) == 0 {
		sb.WriteString(locale.T(lang, "daily.no_done"))
		sb.WriteString("\n\n")
	} else {
		for i := range done.Issues {
			writeIssueLink(&sb, siteURL, &done.Issues[i])
		}
	}

	// Doing\
	sb.WriteString("\n")
	sb.WriteString(locale.T(lang, "daily.doing"))
	sb.WriteString(":\n")
	if doing == nil || len(doing.Issues) == 0 {
		sb.WriteString(locale.T(lang, "daily.no_doing"))
		sb.WriteString("\n\n")
	} else {
		for i := range doing.Issues {
			writeIssueLink(&sb, siteURL, &doing.Issues[i])
		}
	}

	// Plan
	sb.WriteString("\n")
	sb.WriteString(locale.T(lang, "daily.plan"))
	sb.WriteString(":\n")
	if plan != nil && len(plan.Issues) > 0 {
		for i := range plan.Issues {
			writeIssueLink(&sb, siteURL, &plan.Issues[i])
		}
	} else if plan != nil {
		sb.WriteString(locale.T(lang, "daily.no_plan"))
		sb.WriteString("\n\n")
	}

	return sb.String()
}

func writeIssueLink(sb *strings.Builder, siteURL string, issue *jira.Issue) {
	issueURL := fmt.Sprintf("%s/browse/%s", siteURL, issue.Key)
	fmt.Fprintf(sb, "- [%s](%s) %s\n", issue.Key, issueURL, format.EscapeMarkdown(issue.Fields.Summary))
}
