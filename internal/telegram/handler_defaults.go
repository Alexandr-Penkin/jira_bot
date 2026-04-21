package telegram

import (
	"context"
	"fmt"
	"strconv"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"SleepJiraBot/internal/jira"
	"SleepJiraBot/internal/locale"
	"SleepJiraBot/internal/storage"
)

// maxDefaultIssueTypeButtons caps how many issue-type buttons we render so a
// project with many types still fits in a single inline keyboard message.
const maxDefaultIssueTypeButtons = 15

// handleDefaultsProject fetches issue types for the chosen project and shows
// the picker. The board-selection step is deferred until the issue type is
// chosen (or skipped), keeping /defaults as a guided two-step flow.
func (h *Handler) handleDefaultsProject(ctx context.Context, chatID, userID int64, projectKey string) tgbotapi.MessageConfig {
	lang := h.getLang(ctx, userID)

	user, err := h.requireAuth(ctx, userID)
	if err != nil {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected"))
	}

	// Persist the project immediately so /createfast has the minimum it
	// needs even if the user abandons the wizard mid-way.
	if saveErr := h.prefs.SetDefaults(ctx, userID, projectKey, 0); saveErr != nil {
		h.log.Error().Err(saveErr).Msg("failed to save default project")
	}

	issueTypes, itErr := h.jiraAPI.GetCreateMetaIssueTypes(ctx, user, projectKey)
	if itErr != nil || len(issueTypes) == 0 {
		if itErr != nil {
			h.log.Warn().Err(itErr).Str("project", projectKey).Msg("defaults: failed to get issue types, skipping picker")
		}
		return h.defaultsShowBoards(ctx, chatID, userID, user, projectKey, lang)
	}

	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(issueTypes)+1)
	count := 0
	for _, it := range issueTypes {
		if it.Subtask {
			continue
		}
		if count >= maxDefaultIssueTypeButtons {
			break
		}
		data := fmt.Sprintf("def_it:%s:%s", it.ID, it.Name)
		if len(data) > 60 {
			continue
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(it.Name, data),
		))
		count++
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.skip"), "def_it:skip"),
	))

	h.states.Set(userID, "defaults_issue_type", map[string]string{"project": projectKey})

	msg := tgbotapi.NewMessage(chatID, locale.T(lang, "defaults.choose_issue_type", projectKey))
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	msg.ParseMode = tgbotapi.ModeMarkdown
	return msg
}

// defaultsShowBoards renders the board-selection step once project (and
// optionally issue type) have been captured.
func (h *Handler) defaultsShowBoards(ctx context.Context, chatID, userID int64, user *storage.User, projectKey string, lang locale.Lang) tgbotapi.MessageConfig {
	boards, err := h.jiraAPI.GetBoards(ctx, user, projectKey)
	if err != nil {
		h.log.Error().Err(err).Str("project", projectKey).Msg("defaults: failed to get boards")
		return tgbotapi.NewMessage(chatID, locale.T(lang, "defaults.boards_failed"))
	}

	if len(boards) == 0 {
		msg := tgbotapi.NewMessage(chatID, locale.T(lang, "defaults.project_saved", projectKey))
		msg.ParseMode = tgbotapi.ModeMarkdown
		return msg
	}

	if len(boards) == 1 {
		if saveErr := h.prefs.SetDefaults(ctx, userID, projectKey, boards[0].ID); saveErr != nil {
			h.log.Error().Err(saveErr).Msg("failed to save defaults")
		}
		msg := tgbotapi.NewMessage(chatID, locale.T(lang, "defaults.saved", projectKey, boards[0].Name))
		msg.ParseMode = tgbotapi.ModeMarkdown
		return msg
	}

	h.states.Set(userID, "defaults_board", map[string]string{"project": projectKey})

	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(boards))
	for _, b := range boards {
		data := fmt.Sprintf("defaults_board:%s:%d", projectKey, b.ID)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.Name, data),
		))
	}

	msg := tgbotapi.NewMessage(chatID, locale.T(lang, "defaults.choose_board"))
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	return msg
}

// handleDefaultsIssueTypeCallback persists the chosen default issue type (or
// skips it), then continues to the board-selection step.
func (h *Handler) handleDefaultsIssueTypeCallback(ctx context.Context, cq *tgbotapi.CallbackQuery, parts []string) {
	_, _ = h.api.Request(tgbotapi.NewCallback(cq.ID, ""))

	chatID := cq.Message.Chat.ID
	userID := cq.From.ID
	lang := h.getLang(ctx, userID)

	step, data := h.states.Get(userID)
	if step != "defaults_issue_type" {
		return
	}
	projectKey := data["project"]
	if projectKey == "" {
		return
	}

	switch {
	case len(parts) >= 2 && parts[1] == "skip":
		// Keep whatever DefaultIssueType the user already had; just move on.
	case len(parts) >= 3:
		typeID := parts[1]
		typeName := parts[2]
		if err := h.prefs.SetDefaultIssueType(ctx, userID, typeID, typeName); err != nil {
			h.log.Error().Err(err).Msg("failed to save default issue type")
		} else {
			h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "defaults.issue_type_saved", typeName)))
		}
	default:
		return
	}

	user, authErr := h.requireAuth(ctx, userID)
	if authErr != nil {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected")))
		return
	}
	h.sendMessage(h.defaultsShowBoards(ctx, chatID, userID, user, projectKey, lang))
}

// handleDefaultsBoard matches board by name/ID and saves defaults.
func (h *Handler) handleDefaultsBoard(ctx context.Context, chatID, userID int64, projectKey, boardHint string) tgbotapi.MessageConfig {
	lang := h.getLang(ctx, userID)

	user, err := h.requireAuth(ctx, userID)
	if err != nil {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected"))
	}

	boards, err := h.jiraAPI.GetBoards(ctx, user, projectKey)
	if err != nil {
		h.log.Error().Err(err).Msg("defaults: failed to get boards")
		return tgbotapi.NewMessage(chatID, locale.T(lang, "defaults.boards_failed"))
	}

	boardID, found := matchBoard(boards, boardHint)
	if !found {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "sprint.board_not_found", boardHint))
	}

	if boardID == -1 {
		h.states.Set(userID, "defaults_board", map[string]string{"project": projectKey})

		matched := filterBoards(boards, boardHint)
		rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(matched))
		for _, b := range matched {
			data := fmt.Sprintf("defaults_board:%s:%d", projectKey, b.ID)
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(b.Name, data),
			))
		}
		msg := tgbotapi.NewMessage(chatID, locale.T(lang, "defaults.choose_board"))
		msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
		return msg
	}

	boardName := findBoardName(boards, boardID)
	if saveErr := h.prefs.SetDefaults(ctx, userID, projectKey, boardID); saveErr != nil {
		h.log.Error().Err(saveErr).Msg("failed to save defaults")
	}
	msg := tgbotapi.NewMessage(chatID, locale.T(lang, "defaults.saved", projectKey, boardName))
	msg.ParseMode = tgbotapi.ModeMarkdown
	return msg
}

// handleDefaultsBoardCallback handles inline button selection for default board.
func (h *Handler) handleDefaultsBoardCallback(ctx context.Context, cq *tgbotapi.CallbackQuery, parts []string) {
	_, _ = h.api.Request(tgbotapi.NewCallback(cq.ID, ""))

	chatID := cq.Message.Chat.ID
	userID := cq.From.ID
	lang := h.getLang(ctx, userID)

	h.states.Clear(userID)

	// Format: defaults_board:PROJECT:BOARD_ID
	if len(parts) < 3 {
		return
	}

	projectKey := parts[1]
	boardID, err := strconv.Atoi(parts[2])
	if err != nil {
		return
	}

	user, authErr := h.requireAuth(ctx, userID)
	if authErr != nil {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected")))
		return
	}

	boardName := ""
	boards, apiErr := h.jiraAPI.GetBoards(ctx, user, projectKey)
	if apiErr == nil {
		boardName = findBoardName(boards, boardID)
	}
	if boardName == "" {
		boardName = strconv.Itoa(boardID)
	}

	if saveErr := h.prefs.SetDefaults(ctx, userID, projectKey, boardID); saveErr != nil {
		h.log.Error().Err(saveErr).Msg("failed to save defaults")
	}

	msg := tgbotapi.NewMessage(chatID, locale.T(lang, "defaults.saved", projectKey, boardName))
	msg.ParseMode = tgbotapi.ModeMarkdown
	h.sendMessage(msg)
}

func findBoardName(boards []jira.Board, boardID int) string {
	for _, b := range boards {
		if b.ID == boardID {
			return b.Name
		}
	}
	return ""
}
