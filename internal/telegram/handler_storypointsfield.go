package telegram

import (
	"context"
	"fmt"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"SleepJiraBot/internal/locale"
)

// handleStoryPointsFieldStart fetches number-type fields from Jira and shows a picker.
func (h *Handler) handleStoryPointsFieldStart(ctx context.Context, chatID, userID int64) {
	lang := h.getLang(ctx, userID)

	user, err := h.requireAuth(ctx, userID)
	if err != nil {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected")))
		return
	}

	fields, err := h.jiraAPI.GetFields(ctx, user)
	if err != nil {
		h.log.Error().Err(err).Msg("storypointsfield: failed to get fields")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "spfield.failed")))
		return
	}

	// Filter to number-type custom fields (story points are always "number").
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, f := range fields {
		if f.Schema == nil {
			continue
		}
		if f.Schema.Type != "number" {
			continue
		}
		label := f.Name
		if f.ID == user.StoryPointsFieldID {
			label = "\u2705 " + label
		}
		data := fmt.Sprintf("sp_select:%s:%s", f.ID, f.Name)
		// Telegram callback data limit is 64 bytes; truncate if needed.
		if len(data) > 64 {
			data = fmt.Sprintf("sp_select:%s", f.ID)
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, data),
		))
	}

	if len(rows) == 0 {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "spfield.no_fields")))
		return
	}

	// Add reset button.
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "spfield.reset_btn"), "sp_reset"),
	))

	msg := tgbotapi.NewMessage(chatID, locale.T(lang, "spfield.choose"))
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	h.sendMessage(msg)
}

// handleStoryPointsFieldCallback handles sp_select callbacks.
func (h *Handler) handleStoryPointsFieldCallback(ctx context.Context, cq *tgbotapi.CallbackQuery, parts []string) {
	h.handleFieldSelectCallback(ctx, cq, parts, h.userRepo.SetStoryPointsField, "spfield.saved", "failed to save story points field")
}

// handleStoryPointsFieldReset resets the story points field to default.
func (h *Handler) handleStoryPointsFieldReset(ctx context.Context, cq *tgbotapi.CallbackQuery) {
	_, _ = h.api.Request(tgbotapi.NewCallback(cq.ID, ""))

	chatID := cq.Message.Chat.ID
	userID := cq.From.ID
	lang := h.getLang(ctx, userID)

	if err := h.userRepo.SetStoryPointsField(ctx, userID, ""); err != nil {
		h.log.Error().Err(err).Msg("failed to clear story points field")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.generic")))
		return
	}

	editMsg := tgbotapi.NewEditMessageTextAndMarkup(chatID, cq.Message.MessageID,
		locale.T(lang, "spfield.cleared"),
		menuButtonKeyboard(lang))
	editMsg.ParseMode = tgbotapi.ModeMarkdown
	_, _ = h.api.Send(editMsg)
}
