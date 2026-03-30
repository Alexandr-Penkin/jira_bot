package telegram

import (
	"context"
	"fmt"
	"strconv"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"SleepJiraBot/internal/jira"
	"SleepJiraBot/internal/locale"
)

// handleDefaultsProject fetches boards for the chosen project and shows selection.
func (h *Handler) handleDefaultsProject(ctx context.Context, chatID, userID int64, projectKey string) tgbotapi.MessageConfig {
	lang := h.getLang(ctx, userID)

	user, err := h.requireAuth(ctx, userID)
	if err != nil {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected"))
	}

	boards, err := h.jiraAPI.GetBoards(ctx, user, projectKey)
	if err != nil {
		h.log.Error().Err(err).Str("project", projectKey).Msg("defaults: failed to get boards")
		if saveErr := h.userRepo.SetDefaults(ctx, userID, projectKey, 0); saveErr != nil {
			h.log.Error().Err(saveErr).Msg("failed to save defaults")
		}
		return tgbotapi.NewMessage(chatID, locale.T(lang, "defaults.boards_failed"))
	}

	if len(boards) == 0 {
		if saveErr := h.userRepo.SetDefaults(ctx, userID, projectKey, 0); saveErr != nil {
			h.log.Error().Err(saveErr).Msg("failed to save defaults")
		}
		msg := tgbotapi.NewMessage(chatID, locale.T(lang, "defaults.project_saved", projectKey))
		msg.ParseMode = tgbotapi.ModeMarkdown
		return msg
	}

	if len(boards) == 1 {
		if saveErr := h.userRepo.SetDefaults(ctx, userID, projectKey, boards[0].ID); saveErr != nil {
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
	if saveErr := h.userRepo.SetDefaults(ctx, userID, projectKey, boardID); saveErr != nil {
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

	if saveErr := h.userRepo.SetDefaults(ctx, userID, projectKey, boardID); saveErr != nil {
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
