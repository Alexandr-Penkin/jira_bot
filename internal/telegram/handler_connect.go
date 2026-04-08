package telegram

import (
	"context"
	"strconv"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"SleepJiraBot/internal/format"
	"SleepJiraBot/internal/locale"
)

func (h *Handler) handleConnect(ctx context.Context, chatID, userID int64) tgbotapi.MessageConfig {
	lang := h.getLang(ctx, userID)

	existing, err := h.userRepo.GetByTelegramID(ctx, userID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to check user")
		return tgbotapi.NewMessage(chatID, locale.T(lang, "error.generic"))
	}

	if existing != nil && existing.AccessToken != "" {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "connect.already"))
	}

	state, err := generateState()
	if err != nil {
		h.log.Error().Err(err).Msg("failed to generate state")
		return tgbotapi.NewMessage(chatID, locale.T(lang, "error.generic"))
	}

	authURL := h.oauth.GenerateAuthURL(state, userID)

	msg := tgbotapi.NewMessage(chatID, locale.T(lang, "connect.click"))
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonURL(locale.T(lang, "connect.btn"), authURL),
		),
	)
	return msg
}

func (h *Handler) handleDisconnect(ctx context.Context, chatID, userID int64) tgbotapi.MessageConfig {
	lang := h.getLang(ctx, userID)

	existing, err := h.userRepo.GetByTelegramID(ctx, userID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to check user")
		return tgbotapi.NewMessage(chatID, locale.T(lang, "error.generic"))
	}

	if existing == nil || existing.AccessToken == "" {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "disconnect.not_linked"))
	}

	// Delete Jira-side webhooks BEFORE wiping the user record, since
	// the manager needs the access token to call the Jira API.
	if h.webhookMgr != nil {
		h.webhookMgr.DeleteAllForUser(ctx, userID)
	}

	if err = h.subRepo.DeleteByUserID(ctx, userID); err != nil {
		h.log.Error().Err(err).Int64("user_id", userID).Msg("failed to delete user subscriptions")
	}

	if err = h.scheduleRepo.DeleteByUserID(ctx, userID); err != nil {
		h.log.Error().Err(err).Int64("user_id", userID).Msg("failed to delete user schedules")
	}

	if h.onScheduleChange != nil {
		h.onScheduleChange()
	}

	// Clear only the Jira-side credentials — keep language, default
	// project/board, sprint issue types, assignee/story points field
	// ids, and daily JQLs so reconnecting does not force the user to
	// reconfigure everything.
	if err = h.userRepo.ClearJiraCredentials(ctx, userID); err != nil {
		h.log.Error().Err(err).Msg("failed to clear jira credentials")
		return tgbotapi.NewMessage(chatID, locale.T(lang, "disconnect.failed"))
	}

	return tgbotapi.NewMessage(chatID, locale.T(lang, "disconnect.success"))
}

func (h *Handler) handleSiteSelectCallback(ctx context.Context, cq *tgbotapi.CallbackQuery, parts []string) {
	_, _ = h.api.Request(tgbotapi.NewCallback(cq.ID, ""))

	userID := cq.From.ID
	lang := h.getLang(ctx, userID)

	if h.callbackServer == nil || len(parts) < 2 {
		msg := tgbotapi.NewMessage(cq.Message.Chat.ID, locale.T(lang, "error.generic"))
		_, _ = h.api.Send(msg)
		return
	}

	idx, err := strconv.Atoi(parts[1])
	if err != nil {
		msg := tgbotapi.NewMessage(cq.Message.Chat.ID, locale.T(lang, "error.generic"))
		_, _ = h.api.Send(msg)
		return
	}

	pending := h.callbackServer.ConsumePendingSite(userID)
	if pending == nil {
		msg := tgbotapi.NewMessage(cq.Message.Chat.ID, locale.T(lang, "connect.site_expired"))
		_, _ = h.api.Send(msg)
		return
	}

	if idx < 0 || idx >= len(pending.Resources) {
		msg := tgbotapi.NewMessage(cq.Message.Chat.ID, locale.T(lang, "error.generic"))
		_, _ = h.api.Send(msg)
		return
	}

	resource := pending.Resources[idx]
	if err = h.callbackServer.FinalizeSiteConnection(ctx, userID, pending.TokenResponse, resource); err != nil {
		h.log.Error().Err(err).Msg("failed to finalize site connection")
		msg := tgbotapi.NewMessage(cq.Message.Chat.ID, locale.T(lang, "error.generic"))
		_, _ = h.api.Send(msg)
	}
}

func (h *Handler) handleMe(ctx context.Context, chatID, userID int64) tgbotapi.MessageConfig {
	lang := h.getLang(ctx, userID)

	user, err := h.requireAuth(ctx, userID)
	if err != nil {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected"))
	}

	jiraUser, err := h.jiraAPI.GetMyself(ctx, user)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to get jira user")
		return tgbotapi.NewMessage(chatID, locale.T(lang, "me.failed"))
	}

	text := locale.T(lang, "me.title",
		format.EscapeMarkdown(jiraUser.DisplayName),
		format.EscapeMarkdown(jiraUser.Email),
		format.EscapeMarkdown(user.JiraSiteURL),
	)

	if user.DefaultProject != "" {
		boardName := strconv.Itoa(user.DefaultBoardID)
		if user.DefaultBoardID != 0 {
			boards, apiErr := h.jiraAPI.GetBoards(ctx, user, user.DefaultProject)
			if apiErr == nil {
				if name := findBoardName(boards, user.DefaultBoardID); name != "" {
					boardName = name
				}
			}
		} else {
			boardName = "-"
		}
		text += locale.T(lang, "defaults.current",
			format.EscapeMarkdown(user.DefaultProject),
			format.EscapeMarkdown(boardName),
		)
	}

	typesLabel := formatIssueTypesList(user.SprintIssueTypes, lang)
	text += locale.T(lang, "issuetypes.current", format.EscapeMarkdown(typesLabel))

	assigneeFieldLabel := locale.T(lang, "assigneefield.default")
	if user.AssigneeFieldID != "" {
		assigneeFieldLabel = user.AssigneeFieldID
	}
	text += locale.T(lang, "assigneefield.current", format.EscapeMarkdown(assigneeFieldLabel))

	spFieldLabel := locale.T(lang, "spfield.default")
	if user.StoryPointsFieldID != "" {
		spFieldLabel = user.StoryPointsFieldID
	}
	text += locale.T(lang, "spfield.current", format.EscapeMarkdown(spFieldLabel))

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	return msg
}
