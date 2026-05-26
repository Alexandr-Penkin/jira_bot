package telegram

import (
	"context"
	"fmt"
	"strconv"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"SleepJiraBot/internal/calendar"
	"SleepJiraBot/internal/format"
	"SleepJiraBot/internal/locale"
	"SleepJiraBot/internal/storage"
)

// FSM stateNames for the calendar subflow. Defined as constants so the
// state strings stay in lockstep with handleTextInput.
const (
	calendarStateAwaitURL      = "calendar_await_url"
	calendarStateAwaitReminder = "calendar_await_reminder"
)

// handleCalendarMenu renders the calendar submenu — current state plus
// the action keyboard. Called both from the profile menu (`a:calendar`)
// and from sub-callbacks that want to refresh the view after a mutation.
func (h *Handler) handleCalendarMenu(ctx context.Context, chatID, userID int64, msgID int) {
	lang := h.getLang(ctx, userID)
	user, _ := h.userRepo.GetByTelegramID(ctx, userID)

	text := locale.T(lang, "calendar.menu.title", calendarStatusBlock(user, lang, h.calendarDefaultMins))
	keyboard := calendarMenuKeyboard(user, lang, h.calendarDefaultMins)

	if msgID != 0 {
		edit := tgbotapi.NewEditMessageTextAndMarkup(chatID, msgID, text, keyboard)
		edit.ParseMode = tgbotapi.ModeMarkdown
		if _, err := h.api.Send(edit); err == nil {
			return
		}
	}
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.ReplyMarkup = keyboard
	h.sendMessage(msg)
}

func calendarStatusBlock(user *storage.User, lang locale.Lang, defaultMins int) string {
	if user == nil || user.CalendarURL == "" {
		return locale.T(lang, "calendar.status.unset")
	}
	statusToggle := locale.T(lang, "calendar.status.enabled")
	if !user.CalendarNotifyEnabled {
		statusToggle = locale.T(lang, "calendar.status.disabled")
	}
	mins := effectiveReminderMins(user, defaultMins)
	out := locale.T(lang, "calendar.status.set", statusToggle, mins)
	if user.CalendarLastError != "" {
		out += locale.T(lang, "calendar.status.last_error", format.EscapeMarkdown(user.CalendarLastError))
	}
	return out
}

func calendarMenuKeyboard(user *storage.User, lang locale.Lang, _ int) tgbotapi.InlineKeyboardMarkup {
	hasURL := user != nil && user.CalendarURL != ""
	toggleLabel := locale.T(lang, "calendar.btn.toggle_on")
	if user != nil && user.CalendarNotifyEnabled {
		toggleLabel = locale.T(lang, "calendar.btn.toggle_off")
	}

	rows := [][]tgbotapi.InlineKeyboardButton{
		{
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "calendar.btn.set_url"), "cal:seturl"),
		},
	}
	if hasURL {
		rows = append(rows,
			[]tgbotapi.InlineKeyboardButton{
				tgbotapi.NewInlineKeyboardButtonData(toggleLabel, "cal:toggle"),
				tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "calendar.btn.remove"), "cal:remove"),
			},
			[]tgbotapi.InlineKeyboardButton{
				tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "calendar.btn.rem_5"), "cal:rem:5"),
				tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "calendar.btn.rem_15"), "cal:rem:15"),
				tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "calendar.btn.rem_30"), "cal:rem:30"),
				tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "calendar.btn.rem_60"), "cal:rem:60"),
			},
			[]tgbotapi.InlineKeyboardButton{
				tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "calendar.btn.rem_default"), "cal:rem:0"),
				tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "calendar.btn.rem_custom"), "cal:reminput"),
			},
		)
	}
	rows = append(rows, []tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.back"), "m:profile"),
	})
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// effectiveReminderMins returns the lead time the bot will actually
// use for this user: an explicit override when set, otherwise the env
// default. Centralised so the UI and the summary line agree.
func effectiveReminderMins(user *storage.User, defaultMins int) int {
	if user != nil && user.CalendarReminderMinutes > 0 {
		return user.CalendarReminderMinutes
	}
	if defaultMins > 0 {
		return defaultMins
	}
	return storage.DefaultCalendarReminderMinutes
}

// handleCalendarSubCallback dispatches "cal:*" callbacks: URL prompt,
// toggle, remove, fixed-value reminders, custom-reminder prompt.
func (h *Handler) handleCalendarSubCallback(ctx context.Context, cq *tgbotapi.CallbackQuery, parts []string) {
	_, _ = h.api.Request(tgbotapi.NewCallback(cq.ID, ""))

	chatID := cq.Message.Chat.ID
	userID := cq.From.ID
	lang := h.getLang(ctx, userID)

	if len(parts) < 2 {
		return
	}

	switch parts[1] {
	case "seturl":
		h.states.Set(chatID, userID, calendarStateAwaitURL, nil)
		h.sendPrompt(chatID, locale.T(lang, "calendar.prompt.url"), lang)
	case "reminput":
		h.states.Set(chatID, userID, calendarStateAwaitReminder, nil)
		h.sendPrompt(chatID, locale.T(lang, "calendar.prompt.reminder"), lang)
	case "toggle":
		h.toggleCalendarNotify(ctx, chatID, userID, cq.Message.MessageID)
	case "remove":
		h.handleCalendarRemove(ctx, chatID, userID, lang, cq.Message.MessageID)
	case "rem":
		if len(parts) < 3 {
			return
		}
		mins, err := strconv.Atoi(parts[2])
		if err != nil {
			return
		}
		// `0` means "clear override, use env default". Stored as 0 in
		// Mongo so the poller's fallback to defaultReminderMins kicks in.
		if mins < 0 {
			return
		}
		if err := h.userRepo.SetCalendarReminderMinutes(ctx, userID, mins); err != nil {
			h.log.Error().Err(err).Msg("calendar: set reminder")
		}
		h.handleCalendarMenu(ctx, chatID, userID, cq.Message.MessageID)
	}
}

func (h *Handler) toggleCalendarNotify(ctx context.Context, chatID, userID int64, msgID int) {
	user, err := h.userRepo.GetByTelegramID(ctx, userID)
	if err != nil || user == nil {
		return
	}
	if err := h.userRepo.SetCalendarNotifyEnabled(ctx, userID, !user.CalendarNotifyEnabled); err != nil {
		h.log.Error().Err(err).Msg("calendar: toggle")
		return
	}
	h.handleCalendarMenu(ctx, chatID, userID, msgID)
}

// handleCalendarURLInput is the text-state handler for the "Set URL"
// flow. Normalises the input, validates by doing one synchronous fetch
// + parse so the user gets immediate feedback on a bad URL, then
// persists.
func (h *Handler) handleCalendarURLInput(ctx context.Context, chatID, userID int64, lang locale.Lang, text string) {
	if h.calendarFetcher == nil {
		// Calendar feature disabled at startup; do nothing.
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.generic")))
		return
	}

	normalized, err := calendar.Normalize(text, nil)
	if err != nil {
		h.sendMessage(withMenuButton(tgbotapi.NewMessage(chatID,
			locale.T(lang, "calendar.invalid", err.Error())), lang))
		return
	}

	body, fetchErr := h.calendarFetcher.Fetch(ctx, normalized)
	if fetchErr != nil {
		h.sendMessage(withMenuButton(tgbotapi.NewMessage(chatID,
			locale.T(lang, "calendar.invalid", fetchErr.Error())), lang))
		return
	}
	events, parseErr := calendar.Parse(body)
	if parseErr != nil || len(events) == 0 {
		reason := "no VEVENT"
		if parseErr != nil {
			reason = parseErr.Error()
		}
		h.sendMessage(withMenuButton(tgbotapi.NewMessage(chatID,
			locale.T(lang, "calendar.invalid", reason)), lang))
		return
	}

	if err := h.userRepo.SetCalendarURL(ctx, userID, normalized); err != nil {
		h.log.Error().Err(err).Msg("calendar: set url")
		h.sendMessage(withMenuButton(tgbotapi.NewMessage(chatID,
			locale.T(lang, "error.generic")), lang))
		return
	}
	h.sendMessage(withMenuButton(tgbotapi.NewMessage(chatID,
		locale.T(lang, "calendar.saved")), lang))
}

// handleCalendarReminderInput parses the custom reminder integer and
// persists it.
func (h *Handler) handleCalendarReminderInput(ctx context.Context, chatID, userID int64, lang locale.Lang, text string) {
	n, err := strconv.Atoi(text)
	if err != nil || n < 1 || n > 1440 {
		h.sendMessage(withMenuButton(tgbotapi.NewMessage(chatID,
			locale.T(lang, "calendar.invalid_minutes")), lang))
		return
	}
	if err := h.userRepo.SetCalendarReminderMinutes(ctx, userID, n); err != nil {
		h.log.Error().Err(err).Msg("calendar: set reminder")
		return
	}
	h.handleCalendarMenu(ctx, chatID, userID, 0)
}

// handleCalendarRemove clears the URL and wipes the user's slice of
// the events collection so a re-set later starts from a clean slate.
func (h *Handler) handleCalendarRemove(ctx context.Context, chatID, userID int64, lang locale.Lang, msgID int) {
	if err := h.userRepo.SetCalendarURL(ctx, userID, ""); err != nil {
		h.log.Error().Err(err).Msg("calendar: remove url")
		return
	}
	if h.calendarEvents != nil {
		if err := h.calendarEvents.DeleteAllByUser(ctx, userID); err != nil {
			h.log.Warn().Err(err).Msg("calendar: delete events")
		}
	}
	notice := tgbotapi.NewMessage(chatID, locale.T(lang, "calendar.removed"))
	h.sendMessage(notice)
	h.handleCalendarMenu(ctx, chatID, userID, msgID)
}

// calendarSummaryLine appends a one-line "📅 Calendar: ..." entry to
// the /me profile output. Empty when the user has no calendar.
func calendarSummaryLine(user *storage.User, lang locale.Lang, defaultMins int) string {
	if user == nil || user.CalendarURL == "" {
		return ""
	}
	mins := effectiveReminderMins(user, defaultMins)
	return fmt.Sprintf(locale.T(lang, "me.calendar.connected"), mins)
}
