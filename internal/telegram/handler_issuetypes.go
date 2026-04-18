package telegram

import (
	"context"
	"fmt"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"SleepJiraBot/internal/locale"
)

// handleIssueTypesStart begins the issue type selection flow.
// If the user has a default project, loads types immediately; otherwise asks for project key.
func (h *Handler) handleIssueTypesStart(ctx context.Context, chatID, userID int64) {
	lang := h.getLang(ctx, userID)

	user, err := h.requireAuth(ctx, userID)
	if err != nil {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected")))
		return
	}

	if user.DefaultProject != "" {
		h.showIssueTypePicker(ctx, chatID, userID, user.DefaultProject)
		return
	}

	h.states.Set(userID, "it_project", nil)
	h.sendPrompt(chatID, locale.T(lang, "issuetypes.enter_project"), lang)
}

// showIssueTypePicker fetches issue types from Jira and shows a multi-select keyboard.
func (h *Handler) showIssueTypePicker(ctx context.Context, chatID, userID int64, projectKey string) {
	lang := h.getLang(ctx, userID)

	user, err := h.requireAuth(ctx, userID)
	if err != nil {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected")))
		return
	}

	issueTypes, err := h.jiraAPI.GetProjectIssueTypes(ctx, user, projectKey)
	if err != nil {
		h.log.Error().Err(err).Str("project", projectKey).Msg("issuetypes: failed to get issue types")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "issuetypes.failed")))
		return
	}

	if len(issueTypes) == 0 {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "issuetypes.failed")))
		return
	}

	// Store available types and current selection in state.
	selected := make(map[string]string)
	for _, t := range user.SprintIssueTypes {
		selected[t] = "1"
	}

	stateData := map[string]string{"project": projectKey}
	for _, t := range issueTypes {
		stateData["type:"+t.Name] = "available"
		if selected[t.Name] == "1" {
			stateData["sel:"+t.Name] = "1"
		}
	}
	h.states.Set(userID, "it_select", stateData)

	h.sendIssueTypePickerMessage(chatID, lang, stateData)
}

// buildIssueTypeKeyboard builds the inline keyboard for issue type selection.
func buildIssueTypeKeyboard(lang locale.Lang, stateData map[string]string) tgbotapi.InlineKeyboardMarkup {
	var rows [][]tgbotapi.InlineKeyboardButton

	for key := range stateData {
		if !strings.HasPrefix(key, "type:") {
			continue
		}
		typeName := strings.TrimPrefix(key, "type:")
		label := typeName
		if stateData["sel:"+typeName] == "1" {
			label = "✅ " + typeName
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, "it_toggle:"+typeName),
		))
	}

	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "issuetypes.save_btn"), "it_save"),
		tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "issuetypes.clear_btn"), "it_clear"),
	))

	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// handleIssueTypeToggle toggles one issue type in the selection.
func (h *Handler) handleIssueTypeToggle(ctx context.Context, cq *tgbotapi.CallbackQuery, typeName string) {
	_, _ = h.api.Request(tgbotapi.NewCallback(cq.ID, ""))

	userID := cq.From.ID
	lang := h.getLang(ctx, userID)

	step, data := h.states.Get(userID)
	if step != "it_select" {
		return
	}

	if data["sel:"+typeName] == "1" {
		delete(data, "sel:"+typeName)
	} else {
		data["sel:"+typeName] = "1"
	}
	h.states.Set(userID, "it_select", data)

	keyboard := buildIssueTypeKeyboard(lang, data)
	edit := tgbotapi.NewEditMessageReplyMarkup(cq.Message.Chat.ID, cq.Message.MessageID, keyboard)
	_, _ = h.api.Send(edit)
}

// handleIssueTypeSave saves the selected issue types to the user profile.
func (h *Handler) handleIssueTypeSave(ctx context.Context, cq *tgbotapi.CallbackQuery) {
	_, _ = h.api.Request(tgbotapi.NewCallback(cq.ID, ""))

	chatID := cq.Message.Chat.ID
	userID := cq.From.ID
	lang := h.getLang(ctx, userID)

	step, data := h.states.Get(userID)
	if step != "it_select" {
		return
	}

	var selected []string
	for key, val := range data {
		if strings.HasPrefix(key, "sel:") && val == "1" {
			selected = append(selected, strings.TrimPrefix(key, "sel:"))
		}
	}

	h.states.Clear(userID)

	if err := h.prefs.SetSprintIssueTypes(ctx, userID, selected); err != nil {
		h.log.Error().Err(err).Msg("failed to save sprint issue types")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.generic")))
		return
	}

	if len(selected) == 0 {
		editMsg := tgbotapi.NewEditMessageTextAndMarkup(chatID, cq.Message.MessageID,
			locale.T(lang, "issuetypes.cleared"),
			menuButtonKeyboard(lang))
		editMsg.ParseMode = tgbotapi.ModeMarkdown
		_, _ = h.api.Send(editMsg)
		return
	}

	text := locale.T(lang, "issuetypes.saved", strings.Join(selected, ", "))
	editMsg := tgbotapi.NewEditMessageTextAndMarkup(chatID, cq.Message.MessageID,
		text,
		menuButtonKeyboard(lang))
	editMsg.ParseMode = tgbotapi.ModeMarkdown
	_, _ = h.api.Send(editMsg)
}

// handleIssueTypeClear clears the issue type filter.
func (h *Handler) handleIssueTypeClear(ctx context.Context, cq *tgbotapi.CallbackQuery) {
	_, _ = h.api.Request(tgbotapi.NewCallback(cq.ID, ""))

	chatID := cq.Message.Chat.ID
	userID := cq.From.ID
	lang := h.getLang(ctx, userID)

	h.states.Clear(userID)

	if err := h.prefs.SetSprintIssueTypes(ctx, userID, nil); err != nil {
		h.log.Error().Err(err).Msg("failed to clear sprint issue types")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.generic")))
		return
	}

	editMsg := tgbotapi.NewEditMessageTextAndMarkup(chatID, cq.Message.MessageID,
		locale.T(lang, "issuetypes.cleared"),
		menuButtonKeyboard(lang))
	editMsg.ParseMode = tgbotapi.ModeMarkdown
	_, _ = h.api.Send(editMsg)
}

// formatIssueTypesList formats user's selected issue types for display.
func formatIssueTypesList(types []string, lang locale.Lang) string {
	if len(types) == 0 {
		return locale.T(lang, "issuetypes.none")
	}
	return strings.Join(types, ", ")
}

// sendIssueTypePickerMessage sends the initial picker message with keyboard.
func (h *Handler) sendIssueTypePickerMessage(chatID int64, lang locale.Lang, stateData map[string]string) {
	keyboard := buildIssueTypeKeyboard(lang, stateData)
	msg := tgbotapi.NewMessage(chatID, locale.T(lang, "issuetypes.choose"))
	msg.ReplyMarkup = keyboard

	// Show current selection summary.
	var current []string
	for key, val := range stateData {
		if strings.HasPrefix(key, "sel:") && val == "1" {
			current = append(current, strings.TrimPrefix(key, "sel:"))
		}
	}
	if len(current) > 0 {
		msg.Text += fmt.Sprintf("\n\n_%s_", locale.T(lang, "sprint.filtered", strings.Join(current, ", ")))
	}

	msg.ParseMode = tgbotapi.ModeMarkdown
	h.sendMessage(msg)
}
