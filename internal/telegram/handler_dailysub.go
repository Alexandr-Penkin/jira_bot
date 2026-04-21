package telegram

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"SleepJiraBot/internal/locale"
	"SleepJiraBot/internal/storage"
)

const (
	dailySubDefaultTZ   = "Europe/Moscow"
	dailySubMaxTZLen    = 64
	dailySubStateTZ     = "dailysub_tz"
	dailySubStateCustom = "dailysub_time"
)

var dailySubTimeRe = regexp.MustCompile(`^([01]?\d|2[0-3]):([0-5]?\d)$`)

// dailySubPresetTimes is the inline quick-pick grid. Stick to a single
// row of common standup slots so the keyboard stays compact on mobile.
var dailySubPresetTimes = []string{"09:00", "09:30", "10:00", "10:30", "11:00"}

func (h *Handler) handleDailySubMenu(ctx context.Context, chatID, userID int64) {
	lang := h.getLang(ctx, userID)

	existing, err := h.scheduleRepo.GetDaily(ctx, chatID, userID)
	if err != nil {
		h.log.Error().Err(err).Msg("dailysub: failed to load current subscription")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "daily_sub.save_failed")))
		return
	}

	text := locale.T(lang, "daily_sub.title")
	tz := dailySubDefaultTZ
	if existing != nil {
		if existing.Timezone != "" {
			tz = existing.Timezone
		}
		if hhmm := dailyCronHHMM(existing.CronExpression); hhmm != "" {
			text += fmt.Sprintf(locale.T(lang, "daily_sub.current"), hhmm, tz)
		} else {
			text += fmt.Sprintf(locale.T(lang, "daily_sub.current"), existing.CronExpression, tz)
		}
	} else {
		text += locale.T(lang, "daily_sub.none")
	}
	text += "\n\n" + locale.T(lang, "daily_sub.choose_time")

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.ReplyMarkup = dailySubKeyboard(lang, tz, existing != nil)
	h.sendMessage(msg)
}

func dailySubKeyboard(lang locale.Lang, tz string, hasExisting bool) tgbotapi.InlineKeyboardMarkup {
	presetRow := make([]tgbotapi.InlineKeyboardButton, 0, len(dailySubPresetTimes))
	for _, t := range dailySubPresetTimes {
		presetRow = append(presetRow, tgbotapi.NewInlineKeyboardButtonData(t, "dailysub_set:"+t))
	}

	rows := [][]tgbotapi.InlineKeyboardButton{
		presetRow,
		{
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "daily_sub.btn_custom"), "dailysub_custom"),
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf(locale.T(lang, "daily_sub.btn_timezone"), tz), "dailysub_tz"),
		},
	}
	if hasExisting {
		rows = append(rows, []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "daily_sub.btn_unsubscribe"), "dailysub_off"),
		})
	}
	rows = append(rows, []tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.back"), "m:reports"),
	})
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// handleDailySubCallback dispatches dailysub_* callback buttons.
func (h *Handler) handleDailySubCallback(ctx context.Context, cq *tgbotapi.CallbackQuery, parts []string) {
	_, _ = h.api.Request(tgbotapi.NewCallback(cq.ID, ""))

	chatID := cq.Message.Chat.ID
	userID := cq.From.ID
	lang := h.getLang(ctx, userID)

	switch parts[0] {
	case "dailysub_set":
		// cq.Data is "dailysub_set:HH:MM"; the router splits on ":" so
		// rejoin the tail to get the full HH:MM back.
		if _, tail, ok := strings.Cut(cq.Data, ":"); ok {
			h.dailySubSave(ctx, chatID, userID, lang, tail)
		}
	case "dailysub_custom":
		h.states.Set(userID, dailySubStateCustom, nil)
		h.sendPrompt(chatID, locale.T(lang, "daily_sub.enter_time"), lang)
	case "dailysub_tz":
		h.states.Set(userID, dailySubStateTZ, nil)
		h.sendPrompt(chatID, locale.T(lang, "daily_sub.enter_timezone"), lang)
	case "dailysub_off":
		if err := h.scheduleRepo.DeleteDaily(ctx, chatID, userID); err != nil {
			h.log.Error().Err(err).Msg("dailysub: failed to delete subscription")
			h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "daily_sub.save_failed")))
			return
		}
		if h.onScheduleChange != nil {
			h.onScheduleChange()
		}
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "daily_sub.removed")))
	}
}

// handleDailySubTimeInput consumes the HH:MM the user typed after tapping
// "Custom time".
func (h *Handler) handleDailySubTimeInput(ctx context.Context, chatID, userID int64, lang locale.Lang, text string) {
	h.states.Clear(userID)
	h.dailySubSave(ctx, chatID, userID, lang, text)
}

// handleDailySubTZInput consumes the IANA zone the user typed. On
// success it saves the zone for an existing subscription (so the next
// fire uses it) or, if none exists yet, just records the preference and
// re-opens the menu so the user can pick a time.
func (h *Handler) handleDailySubTZInput(ctx context.Context, chatID, userID int64, lang locale.Lang, text string) {
	h.states.Clear(userID)

	tz := strings.TrimSpace(text)
	if tz == "" || len(tz) > dailySubMaxTZLen {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "daily_sub.invalid_tz")))
		return
	}
	if _, err := time.LoadLocation(tz); err != nil {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "daily_sub.invalid_tz")))
		return
	}

	existing, err := h.scheduleRepo.GetDaily(ctx, chatID, userID)
	if err != nil {
		h.log.Error().Err(err).Msg("dailysub: failed to load current subscription")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "daily_sub.save_failed")))
		return
	}

	if existing == nil {
		// No subscription yet — stash the chosen TZ so the next save picks
		// it up, by creating a placeholder only when the user taps a time
		// preset or sends a custom time. Simplest option: re-open the menu
		// with the TZ applied. We encode the chosen TZ in FSM data so the
		// next time save reads it back.
		h.states.Set(userID, "dailysub_tz_pending", map[string]string{"tz": tz})
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "daily_sub.tz_saved")))
		h.handleDailySubMenu(ctx, chatID, userID)
		return
	}

	existing.Timezone = tz
	existing.CronExpression = rebuildDailyCronWithTZ(existing.CronExpression, tz)
	if err := h.scheduleRepo.UpsertDaily(ctx, existing); err != nil {
		h.log.Error().Err(err).Msg("dailysub: failed to update timezone")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "daily_sub.save_failed")))
		return
	}
	if h.onScheduleChange != nil {
		h.onScheduleChange()
	}
	hhmm := dailyCronHHMM(existing.CronExpression)
	h.sendMessage(tgbotapi.NewMessage(chatID, fmt.Sprintf(locale.T(lang, "daily_sub.updated"), hhmm, tz)))
}

func (h *Handler) dailySubSave(ctx context.Context, chatID, userID int64, lang locale.Lang, timeInput string) {
	hhmm, ok := parseDailySubTime(timeInput)
	if !ok {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "daily_sub.invalid_time")))
		return
	}

	tz := dailySubDefaultTZ
	existing, err := h.scheduleRepo.GetDaily(ctx, chatID, userID)
	if err != nil {
		h.log.Error().Err(err).Msg("dailysub: failed to load current subscription")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "daily_sub.save_failed")))
		return
	}
	if existing != nil && existing.Timezone != "" {
		tz = existing.Timezone
	}

	if step, data := h.states.Get(userID); step == "dailysub_tz_pending" {
		if pending := data["tz"]; pending != "" {
			tz = pending
		}
		h.states.Clear(userID)
	}

	hh, mm := hhmm[0], hhmm[1]
	cronExpr := fmt.Sprintf("CRON_TZ=%s %d %d * * *", tz, mm, hh)
	if err := validateCronExpression(cronExpr); err != nil {
		h.log.Error().Err(err).Str("cron", cronExpr).Msg("dailysub: generated cron failed validation")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "daily_sub.save_failed")))
		return
	}

	report := &storage.ScheduledReport{
		TelegramChatID: chatID,
		TelegramUserID: userID,
		CronExpression: cronExpr,
		ReportName:     locale.T(lang, "daily_sub.report_name"),
		Kind:           storage.ScheduleKindDaily,
		Timezone:       tz,
	}
	if err := h.scheduleRepo.UpsertDaily(ctx, report); err != nil {
		h.log.Error().Err(err).Msg("dailysub: failed to upsert subscription")
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "daily_sub.save_failed")))
		return
	}
	if h.onScheduleChange != nil {
		h.onScheduleChange()
	}

	pretty := fmt.Sprintf("%02d:%02d", hh, mm)
	key := "daily_sub.created"
	if existing != nil {
		key = "daily_sub.updated"
	}
	h.sendMessage(tgbotapi.NewMessage(chatID, fmt.Sprintf(locale.T(lang, key), pretty, tz)))
}

// parseDailySubTime accepts "HH:MM" or "H:MM" (minutes required) and
// returns [hour, minute] as ints.
func parseDailySubTime(s string) ([2]int, bool) {
	s = strings.TrimSpace(s)
	m := dailySubTimeRe.FindStringSubmatch(s)
	if m == nil {
		return [2]int{}, false
	}
	var hh, mm int
	fmt.Sscanf(m[1], "%d", &hh)
	fmt.Sscanf(m[2], "%d", &mm)
	return [2]int{hh, mm}, true
}

// dailyCronHHMM extracts the "HH:MM" display from a cron expression we
// generated (minute, hour in fields [0], [1] after an optional CRON_TZ=
// prefix). Returns "" if the expression is not in the shape we produce.
func dailyCronHHMM(expr string) string {
	expr = strings.TrimSpace(expr)
	if strings.HasPrefix(expr, "CRON_TZ=") || strings.HasPrefix(expr, "TZ=") {
		if i := strings.Index(expr, " "); i > 0 {
			expr = strings.TrimSpace(expr[i+1:])
		}
	}
	fields := strings.Fields(expr)
	if len(fields) < 2 {
		return ""
	}
	var mm, hh int
	if _, err := fmt.Sscanf(fields[0], "%d", &mm); err != nil {
		return ""
	}
	if _, err := fmt.Sscanf(fields[1], "%d", &hh); err != nil {
		return ""
	}
	return fmt.Sprintf("%02d:%02d", hh, mm)
}

// rebuildDailyCronWithTZ swaps the CRON_TZ prefix on a daily cron
// expression we produced. If the body isn't a daily-shaped expression
// we return it unchanged so we don't silently rewrite user-authored
// schedules.
func rebuildDailyCronWithTZ(expr, tz string) string {
	body := strings.TrimSpace(expr)
	if strings.HasPrefix(body, "CRON_TZ=") || strings.HasPrefix(body, "TZ=") {
		if i := strings.Index(body, " "); i > 0 {
			body = strings.TrimSpace(body[i+1:])
		}
	}
	if dailyCronHHMM(body) == "" {
		return expr
	}
	return "CRON_TZ=" + tz + " " + body
}
