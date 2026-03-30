package telegram

import (
	"context"
	"fmt"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"SleepJiraBot/internal/format"
	"SleepJiraBot/internal/locale"
)

func (h *Handler) handleFilters(ctx context.Context, chatID, userID int64) tgbotapi.MessageConfig {
	lang := h.getLang(ctx, userID)

	user, err := h.requireAuth(ctx, userID)
	if err != nil {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected"))
	}

	filters, err := h.jiraAPI.GetFavouriteFilters(ctx, user)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to get favourite filters")
		return tgbotapi.NewMessage(chatID, locale.T(lang, "filters.failed"))
	}

	if len(filters) == 0 {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "filters.no_filters"))
	}

	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(filters)+1)
	for _, f := range filters {
		data := fmt.Sprintf("filters:%s", f.ID)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(f.Name, data),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.cancel"), "a:cancel"),
	))

	msg := tgbotapi.NewMessage(chatID, locale.T(lang, "filters.choose"))
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	return msg
}

func (h *Handler) handleFiltersCallback(ctx context.Context, cq *tgbotapi.CallbackQuery, parts []string) {
	_, _ = h.api.Request(tgbotapi.NewCallback(cq.ID, ""))

	chatID := cq.Message.Chat.ID
	userID := cq.From.ID
	lang := h.getLang(ctx, userID)
	filterID := parts[1]

	user, err := h.requireAuth(ctx, userID)
	if err != nil {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected")))
		return
	}

	filters, err := h.jiraAPI.GetFavouriteFilters(ctx, user)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to get favourite filters")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "filters.failed")))
		return
	}

	var filterName, filterJQL string
	for _, f := range filters {
		if f.ID == filterID {
			filterName = f.Name
			filterJQL = f.JQL
			break
		}
	}

	if filterJQL == "" {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "filters.not_found")))
		return
	}

	result, err := h.jiraAPI.SearchIssues(ctx, user, filterJQL, listMaxResults)
	if err != nil {
		h.log.Error().Err(err).Str("filter", filterName).Msg("failed to search filter issues")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "list.failed")))
		return
	}

	if len(result.Issues) == 0 {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "list.no_issues")))
		return
	}

	count := len(result.Issues)
	var sb strings.Builder
	sb.WriteString(locale.T(lang, "filters.issues_title", format.EscapeMarkdown(filterName), count))
	for i := range result.Issues {
		issue := &result.Issues[i]
		status := "?"
		if issue.Fields.Status != nil {
			status = issue.Fields.Status.Name
		}
		issueURL := fmt.Sprintf("%s/browse/%s", user.JiraSiteURL, issue.Key)
		sb.WriteString(fmt.Sprintf("[%s](%s) \\[%s] %s\n", issue.Key, issueURL, format.EscapeMarkdown(status), format.EscapeMarkdown(issue.Fields.Summary)))
	}

	if result.Total > count {
		sb.WriteString(locale.T(lang, "filters.more", result.Total-count))
	}

	msg := tgbotapi.NewMessage(chatID, sb.String())
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.DisableWebPagePreview = true
	msg.ReplyMarkup = menuButtonKeyboard(lang)
	h.sendMessage(msg)
}
