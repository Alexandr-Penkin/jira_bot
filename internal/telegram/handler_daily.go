package telegram

import (
	"context"
	"fmt"
	"regexp"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"SleepJiraBot/internal/daily"
	"SleepJiraBot/internal/locale"
	"SleepJiraBot/internal/storage"
)

func yesterdayStr() string {
	return time.Now().AddDate(0, 0, -1).Format("2006-01-02")
}

const userSearchMaxResult = 10

var jiraAccountIDRe = regexp.MustCompile(`^[a-zA-Z0-9:_-]+$`)

// handleDaily generates a daily standup for the current user.
func (h *Handler) handleDaily(ctx context.Context, chatID, userID int64) tgbotapi.MessageConfig {
	lang := h.getLang(ctx, userID)

	user, err := h.requireAuth(ctx, userID)
	if err != nil {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected"))
	}

	return h.buildDailyReply(ctx, chatID, lang, user, "")
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

	doneJQL := fmt.Sprintf(
		"status changed BY %q AFTER %q ORDER BY updated DESC",
		accountID, yesterdayStr(),
	)
	doingJQL := fmt.Sprintf(
		"assignee=%q AND statusCategory=\"In Progress\" ORDER BY updated DESC",
		accountID,
	)

	text, err := daily.BuildWithJQL(ctx, h.jiraAPI, user, lang, displayName, doneJQL, doingJQL, "")
	if err != nil {
		h.log.Error().Err(err).Str("account_id", accountID).Msg("daily: failed to build standup for user")
		return tgbotapi.NewMessage(chatID, locale.T(lang, "daily.failed"))
	}
	return dailyMessage(chatID, text)
}

func (h *Handler) buildDailyReply(ctx context.Context, chatID int64, lang locale.Lang, user *storage.User, displayName string) tgbotapi.MessageConfig {
	text, err := daily.Build(ctx, h.jiraAPI, user, lang, displayName)
	if err != nil {
		h.log.Error().Err(err).Msg("daily: failed to build standup")
		return tgbotapi.NewMessage(chatID, locale.T(lang, "daily.failed"))
	}
	return dailyMessage(chatID, text)
}

func dailyMessage(chatID int64, text string) tgbotapi.MessageConfig {
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
