package telegram

import (
	"context"
	"fmt"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"SleepJiraBot/internal/locale"
)

func (h *Handler) handleDailyJQLStart(ctx context.Context, chatID, userID int64) {
	lang := h.getLang(ctx, userID)

	user, err := h.requireAuth(ctx, userID)
	if err != nil {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected")))
		return
	}

	doneLabel := user.DailyDoneJQL
	if doneLabel == "" {
		doneLabel = locale.T(lang, "daily_jql.default")
	}
	doingLabel := user.DailyDoingJQL
	if doingLabel == "" {
		doingLabel = locale.T(lang, "daily_jql.default")
	}
	planLabel := user.DailyPlanJQL
	if planLabel == "" {
		planLabel = locale.T(lang, "daily_jql.default")
	}

	text := locale.T(lang, "daily_jql.title")
	text += fmt.Sprintf(locale.T(lang, "daily_jql.current"), doneLabel, doingLabel, planLabel)

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "daily_jql.btn_done"), "djql_done:edit"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "daily_jql.btn_doing"), "djql_doing:edit"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "daily_jql.btn_plan"), "djql_plan:edit"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "daily_jql.btn_reset"), "djql_reset:all"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.back"), "m:profile"),
		),
	)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.ReplyMarkup = keyboard
	h.sendMessage(msg)
}

func (h *Handler) handleDailyJQLEdit(ctx context.Context, cq *tgbotapi.CallbackQuery, state, promptKey string) {
	_, _ = h.api.Request(tgbotapi.NewCallback(cq.ID, ""))
	userID := cq.From.ID
	chatID := cq.Message.Chat.ID
	lang := h.getLang(ctx, userID)

	h.states.Set(userID, state, nil)
	h.sendPrompt(chatID, locale.T(lang, promptKey), lang)
}

func (h *Handler) handleDailyJQLSave(ctx context.Context, chatID, userID int64, lang locale.Lang, block, text string) {
	user, err := h.requireAuth(ctx, userID)
	if err != nil {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.not_connected")))
		return
	}

	if len(text) > maxJQLLen {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "list.jql_too_long", maxJQLLen)))
		return
	}

	doneJQL := user.DailyDoneJQL
	doingJQL := user.DailyDoingJQL
	planJQL := user.DailyPlanJQL

	jql := text
	if jql == "-" {
		jql = ""
	}

	switch block {
	case "done":
		doneJQL = jql
	case "doing":
		doingJQL = jql
	case "plan":
		planJQL = jql
	}

	if err := h.userRepo.SetDailyJQL(ctx, userID, doneJQL, doingJQL, planJQL); err != nil {
		h.log.Error().Err(err).Msg("failed to save daily JQL")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.generic")))
		return
	}

	h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "daily_jql.saved")))
}

func (h *Handler) handleDailyJQLReset(ctx context.Context, cq *tgbotapi.CallbackQuery) {
	_, _ = h.api.Request(tgbotapi.NewCallback(cq.ID, ""))
	userID := cq.From.ID
	chatID := cq.Message.Chat.ID
	lang := h.getLang(ctx, userID)

	if err := h.userRepo.SetDailyJQL(ctx, userID, "", "", ""); err != nil {
		h.log.Error().Err(err).Msg("failed to reset daily JQL")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.generic")))
		return
	}

	h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "daily_jql.reset")))
}
