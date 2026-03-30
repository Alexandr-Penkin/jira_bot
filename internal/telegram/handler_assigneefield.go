package telegram

import (
	"context"
	"fmt"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"SleepJiraBot/internal/format"
	"SleepJiraBot/internal/locale"
)

// handleAssigneeFieldStart fetches people-type fields from Jira and shows a picker.
func (h *Handler) handleAssigneeFieldStart(ctx context.Context, chatID, userID int64) {
	lang := h.getLang(ctx, userID)

	user, err := h.requireAuth(ctx, userID)
	if err != nil {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected")))
		return
	}

	fields, err := h.jiraAPI.GetFields(ctx, user)
	if err != nil {
		h.log.Error().Err(err).Msg("assigneefield: failed to get fields")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "assigneefield.failed")))
		return
	}

	// Filter to people-type custom fields.
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, f := range fields {
		if f.Schema == nil {
			continue
		}
		if f.Schema.Type != "user" {
			continue
		}
		// Skip the standard assignee field — it's the default.
		if f.Schema.System == "assignee" {
			continue
		}
		label := f.Name
		if f.ID == user.AssigneeFieldID {
			label = "✅ " + label
		}
		data := fmt.Sprintf("af_select:%s:%s", f.ID, f.Name)
		// Telegram callback data limit is 64 bytes; truncate if needed.
		if len(data) > 64 {
			data = fmt.Sprintf("af_select:%s", f.ID)
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, data),
		))
	}

	if len(rows) == 0 {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "assigneefield.no_fields")))
		return
	}

	// Add reset button.
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "assigneefield.reset_btn"), "af_reset"),
	))

	msg := tgbotapi.NewMessage(chatID, locale.T(lang, "assigneefield.choose"))
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	h.sendMessage(msg)
}

// handleAssigneeFieldCallback handles af_select and af_reset callbacks.
func (h *Handler) handleAssigneeFieldCallback(ctx context.Context, cq *tgbotapi.CallbackQuery, parts []string) {
	_, _ = h.api.Request(tgbotapi.NewCallback(cq.ID, ""))

	chatID := cq.Message.Chat.ID
	userID := cq.From.ID
	lang := h.getLang(ctx, userID)

	if len(parts) < 2 {
		return
	}

	fieldID := parts[1]
	fieldName := fieldID
	if len(parts) >= 3 {
		fieldName = strings.Join(parts[2:], ":")
	}

	if err := h.userRepo.SetAssigneeField(ctx, userID, fieldID); err != nil {
		h.log.Error().Err(err).Msg("failed to save assignee field")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.generic")))
		return
	}

	text := locale.T(lang, "assigneefield.saved", format.EscapeMarkdown(fieldName))
	editMsg := tgbotapi.NewEditMessageTextAndMarkup(chatID, cq.Message.MessageID,
		text, menuButtonKeyboard(lang))
	editMsg.ParseMode = tgbotapi.ModeMarkdown
	_, _ = h.api.Send(editMsg)
}

// handleAssigneeFieldReset resets the assignee field to default.
func (h *Handler) handleAssigneeFieldReset(ctx context.Context, cq *tgbotapi.CallbackQuery) {
	_, _ = h.api.Request(tgbotapi.NewCallback(cq.ID, ""))

	chatID := cq.Message.Chat.ID
	userID := cq.From.ID
	lang := h.getLang(ctx, userID)

	if err := h.userRepo.SetAssigneeField(ctx, userID, ""); err != nil {
		h.log.Error().Err(err).Msg("failed to clear assignee field")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.generic")))
		return
	}

	editMsg := tgbotapi.NewEditMessageTextAndMarkup(chatID, cq.Message.MessageID,
		locale.T(lang, "assigneefield.cleared"),
		menuButtonKeyboard(lang))
	editMsg.ParseMode = tgbotapi.ModeMarkdown
	_, _ = h.api.Send(editMsg)
}
