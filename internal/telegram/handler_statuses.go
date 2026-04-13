package telegram

import (
	"context"
	"fmt"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"SleepJiraBot/internal/locale"
)

// statusKind distinguishes between done and hold status configuration.
type statusKind string

const (
	statusKindDone statusKind = "done"
	statusKindHold statusKind = "hold"
)

func (k statusKind) statePrefix() string {
	if k == statusKindDone {
		return "ds"
	}
	return "hs"
}

func (k statusKind) localePrefix() string {
	if k == statusKindDone {
		return "donestatuses"
	}
	return "holdstatuses"
}

// handleStatusesStart begins the status selection flow for the given kind.
func (h *Handler) handleStatusesStart(ctx context.Context, chatID, userID int64, kind statusKind) {
	lang := h.getLang(ctx, userID)

	user, err := h.requireAuth(ctx, userID)
	if err != nil {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected")))
		return
	}

	if user.DefaultProject != "" {
		h.showStatusPicker(ctx, chatID, userID, user.DefaultProject, kind)
		return
	}

	h.states.Set(userID, kind.statePrefix()+"_project", nil)
	h.sendPrompt(chatID, locale.T(lang, kind.localePrefix()+".enter_project"), lang)
}

// showStatusPicker fetches statuses from Jira and shows a multi-select keyboard.
func (h *Handler) showStatusPicker(ctx context.Context, chatID, userID int64, projectKey string, kind statusKind) {
	lang := h.getLang(ctx, userID)

	user, err := h.requireAuth(ctx, userID)
	if err != nil {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected")))
		return
	}

	statuses, err := h.jiraAPI.GetProjectStatuses(ctx, user, projectKey)
	if err != nil {
		h.log.Error().Err(err).Str("project", projectKey).Msg("statuses: failed to get project statuses")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, kind.localePrefix()+".failed")))
		return
	}

	if len(statuses) == 0 {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, kind.localePrefix()+".failed")))
		return
	}

	// Build current selection from user settings.
	var currentStatuses []string
	if kind == statusKindDone {
		currentStatuses = user.DoneStatuses
	} else {
		currentStatuses = user.HoldStatuses
	}

	selected := make(map[string]string)
	for _, s := range currentStatuses {
		selected[strings.ToLower(s)] = "1"
	}

	prefix := kind.statePrefix()
	stateData := map[string]string{"project": projectKey, "kind": string(kind)}
	for _, s := range statuses {
		stateData["status:"+s] = "available"
		if selected[strings.ToLower(s)] == "1" {
			stateData["sel:"+s] = "1"
		}
	}
	h.states.Set(userID, prefix+"_select", stateData)

	h.sendStatusPickerMessage(chatID, lang, stateData, kind)
}

// buildStatusKeyboard builds the inline keyboard for status selection.
func buildStatusKeyboard(lang locale.Lang, stateData map[string]string, kind statusKind) tgbotapi.InlineKeyboardMarkup {
	prefix := kind.statePrefix()
	var rows [][]tgbotapi.InlineKeyboardButton

	for key := range stateData {
		if !strings.HasPrefix(key, "status:") {
			continue
		}
		statusName := strings.TrimPrefix(key, "status:")
		label := statusName
		if stateData["sel:"+statusName] == "1" {
			label = "✅ " + statusName
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, prefix+"_toggle:"+statusName),
		))
	}

	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, kind.localePrefix()+".save_btn"), prefix+"_save"),
		tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, kind.localePrefix()+".clear_btn"), prefix+"_clear"),
	))

	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// handleStatusToggle toggles one status in the selection.
func (h *Handler) handleStatusToggle(ctx context.Context, cq *tgbotapi.CallbackQuery, statusName string, kind statusKind) {
	_, _ = h.api.Request(tgbotapi.NewCallback(cq.ID, ""))

	userID := cq.From.ID
	lang := h.getLang(ctx, userID)

	prefix := kind.statePrefix()
	step, data := h.states.Get(userID)
	if step != prefix+"_select" {
		return
	}

	if data["sel:"+statusName] == "1" {
		delete(data, "sel:"+statusName)
	} else {
		data["sel:"+statusName] = "1"
	}
	h.states.Set(userID, prefix+"_select", data)

	keyboard := buildStatusKeyboard(lang, data, kind)
	edit := tgbotapi.NewEditMessageReplyMarkup(cq.Message.Chat.ID, cq.Message.MessageID, keyboard)
	_, _ = h.api.Send(edit)
}

// handleStatusSave saves the selected statuses to the user profile.
func (h *Handler) handleStatusSave(ctx context.Context, cq *tgbotapi.CallbackQuery, kind statusKind) {
	_, _ = h.api.Request(tgbotapi.NewCallback(cq.ID, ""))

	chatID := cq.Message.Chat.ID
	userID := cq.From.ID
	lang := h.getLang(ctx, userID)

	prefix := kind.statePrefix()
	step, data := h.states.Get(userID)
	if step != prefix+"_select" {
		return
	}

	var selected []string
	for key, val := range data {
		if strings.HasPrefix(key, "sel:") && val == "1" {
			selected = append(selected, strings.TrimPrefix(key, "sel:"))
		}
	}

	h.states.Clear(userID)

	var saveErr error
	if kind == statusKindDone {
		saveErr = h.userRepo.SetDoneStatuses(ctx, userID, selected)
	} else {
		saveErr = h.userRepo.SetHoldStatuses(ctx, userID, selected)
	}

	if saveErr != nil {
		h.log.Error().Err(saveErr).Msg("failed to save " + string(kind) + " statuses")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.generic")))
		return
	}

	if len(selected) == 0 {
		editMsg := tgbotapi.NewEditMessageTextAndMarkup(chatID, cq.Message.MessageID,
			locale.T(lang, kind.localePrefix()+".cleared"),
			menuButtonKeyboard(lang))
		editMsg.ParseMode = tgbotapi.ModeMarkdown
		_, _ = h.api.Send(editMsg)
		return
	}

	text := locale.T(lang, kind.localePrefix()+".saved", strings.Join(selected, ", "))
	editMsg := tgbotapi.NewEditMessageTextAndMarkup(chatID, cq.Message.MessageID,
		text,
		menuButtonKeyboard(lang))
	editMsg.ParseMode = tgbotapi.ModeMarkdown
	_, _ = h.api.Send(editMsg)
}

// handleStatusClear clears the status configuration (resets to default).
func (h *Handler) handleStatusClear(ctx context.Context, cq *tgbotapi.CallbackQuery, kind statusKind) {
	_, _ = h.api.Request(tgbotapi.NewCallback(cq.ID, ""))

	chatID := cq.Message.Chat.ID
	userID := cq.From.ID
	lang := h.getLang(ctx, userID)

	h.states.Clear(userID)

	var saveErr error
	if kind == statusKindDone {
		saveErr = h.userRepo.SetDoneStatuses(ctx, userID, nil)
	} else {
		saveErr = h.userRepo.SetHoldStatuses(ctx, userID, nil)
	}

	if saveErr != nil {
		h.log.Error().Err(saveErr).Msg("failed to clear " + string(kind) + " statuses")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.generic")))
		return
	}

	editMsg := tgbotapi.NewEditMessageTextAndMarkup(chatID, cq.Message.MessageID,
		locale.T(lang, kind.localePrefix()+".cleared"),
		menuButtonKeyboard(lang))
	editMsg.ParseMode = tgbotapi.ModeMarkdown
	_, _ = h.api.Send(editMsg)
}

// formatStatusesList formats user's selected statuses for display.
func formatStatusesList(statuses []string, lang locale.Lang, kind statusKind) string {
	if len(statuses) == 0 {
		return locale.T(lang, kind.localePrefix()+".none")
	}
	return strings.Join(statuses, ", ")
}

// sendStatusPickerMessage sends the initial picker message with keyboard.
func (h *Handler) sendStatusPickerMessage(chatID int64, lang locale.Lang, stateData map[string]string, kind statusKind) {
	keyboard := buildStatusKeyboard(lang, stateData, kind)
	msg := tgbotapi.NewMessage(chatID, locale.T(lang, kind.localePrefix()+".choose"))
	msg.ReplyMarkup = keyboard

	var current []string
	for key, val := range stateData {
		if strings.HasPrefix(key, "sel:") && val == "1" {
			current = append(current, strings.TrimPrefix(key, "sel:"))
		}
	}
	if len(current) > 0 {
		msg.Text += fmt.Sprintf("\n\n_%s_", strings.Join(current, ", "))
	}

	msg.ParseMode = tgbotapi.ModeMarkdown
	h.sendMessage(msg)
}
