package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"SleepJiraBot/internal/format"
	"SleepJiraBot/internal/locale"
	"SleepJiraBot/internal/notiflog"
	"SleepJiraBot/internal/poller"
	"SleepJiraBot/internal/storage"
)

const adminUsersPageSize = 10

func (h *Handler) isAdmin(userID int64) bool {
	return h.adminID != 0 && h.adminID == userID
}

func (h *Handler) handleAdminCommand(chatID int64, lang locale.Lang) tgbotapi.MessageConfig {
	msg := tgbotapi.NewMessage(chatID, locale.T(lang, "admin.menu"))
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.ReplyMarkup = adminMenuKeyboard(lang)
	return msg
}

func (h *Handler) handleAdminCallback(ctx context.Context, cq *tgbotapi.CallbackQuery, action string) {
	_, _ = h.api.Request(tgbotapi.NewCallback(cq.ID, ""))

	chatID := cq.Message.Chat.ID
	userID := cq.From.ID
	lang := h.getLang(ctx, userID)

	if !h.isAdmin(userID) {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "admin.not_authorized")))
		return
	}

	switch action {
	case "stats":
		h.handleAdminStats(ctx, chatID, lang)
	case "users":
		h.handleAdminUsers(ctx, chatID, 0, lang)
	case "broadcast":
		h.states.Set(userID, "admin_broadcast", nil)
		h.sendPrompt(chatID, locale.T(lang, "admin.broadcast_enter"), lang)
	case "poller":
		h.handleAdminPoller(chatID, lang)
	case "notiflog":
		h.handleAdminNotifLog(chatID, lang)
	}
}

func (h *Handler) handleAdminNotifLog(chatID int64, lang locale.Lang) {
	entries := h.notifLog.Recent()

	var sb strings.Builder
	sb.WriteString(locale.T(lang, "admin.notiflog_title"))
	sb.WriteString("\n\n")

	if len(entries) == 0 {
		sb.WriteString(locale.T(lang, "admin.notiflog_empty"))
	} else {
		for i := range entries {
			e := &entries[i]
			ts := e.At.Format("2006-01-02 15:04:05")
			merged := "—"
			if e.Merged {
				merged = "✅"
			}
			link := format.EscapeMarkdown(e.IssueKey)
			if e.IssueURL != "" {
				link = fmt.Sprintf("[%s](%s)", format.EscapeMarkdown(e.IssueKey), e.IssueURL)
			}
			changes := e.Changes
			// Keep each entry short enough to fit a 4096-char Telegram
			// message even when 10 entries are concatenated.
			const maxChangesLen = 200
			if len(changes) > maxChangesLen {
				changes = changes[:maxChangesLen] + "…"
			}
			fmt.Fprintf(&sb, "`%s` *%s* %s %s\n%s\n\n",
				ts,
				sourceLabel(e.Source),
				link,
				merged,
				format.EscapeMarkdown(changes),
			)
		}
	}

	msg := tgbotapi.NewMessage(chatID, sb.String())
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.DisableWebPagePreview = true
	msg.ReplyMarkup = adminMenuKeyboard(lang)
	h.sendMessage(msg)
}

// sourceLabel returns the display source name for a notiflog entry.
func sourceLabel(s notiflog.Source) string {
	return string(s)
}

func (h *Handler) handleAdminStats(ctx context.Context, chatID int64, lang locale.Lang) {
	totalUsers, _ := h.userRepo.CountAll(ctx)
	connectedUsers, _ := h.userRepo.CountConnected(ctx)
	activeSubs, _ := h.subRepo.CountActive(ctx)
	activeSchedules, _ := h.scheduleRepo.CountActive(ctx)

	stats := h.notifLog.Snapshot()
	since := stats.StartedAt.Format("2006-01-02 15:04:05")

	text := locale.T(lang, "admin.stats",
		totalUsers, connectedUsers, activeSubs, activeSchedules,
		since,
		stats.ReceivedPoller, stats.ReceivedWebhook,
		stats.SentPoller, stats.SentWebhook,
		stats.Merged)
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.ReplyMarkup = adminMenuKeyboard(lang)
	h.sendMessage(msg)
}

func (h *Handler) handleAdminUsers(ctx context.Context, chatID int64, page int, lang locale.Lang) {
	skip := int64(page) * adminUsersPageSize
	users, err := h.userRepo.ListAll(ctx, skip, adminUsersPageSize+1)
	if err != nil {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.generic")))
		return
	}

	if len(users) == 0 && page == 0 {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "admin.users_empty")))
		return
	}

	hasNext := len(users) > adminUsersPageSize
	if hasNext {
		users = users[:adminUsersPageSize]
	}

	var sb strings.Builder
	sb.WriteString(locale.T(lang, "admin.users_title", page+1))

	for i := range users {
		u := &users[i]
		num := int(skip) + i + 1
		created := time.Unix(u.CreatedTS, 0).Format("2006-01-02")
		if u.AccessToken != "" || u.JiraSiteURL != "" {
			site := u.JiraSiteURL
			if site == "" {
				site = "—"
			}
			fmt.Fprintf(&sb, locale.T(lang, "admin.user_entry"),
				num, u.TelegramUserID,
				format.EscapeMarkdown(site),
				format.EscapeMarkdown(site),
				created)
		} else {
			fmt.Fprintf(&sb, locale.T(lang, "admin.user_disconnected"),
				num, u.TelegramUserID, created)
		}
	}

	var rows [][]tgbotapi.InlineKeyboardButton

	// User action buttons
	for i := range users {
		u := &users[i]
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("👤 %d", u.TelegramUserID),
				fmt.Sprintf("adm_user:%d", u.TelegramUserID)),
		))
	}

	// Pagination
	var navButtons []tgbotapi.InlineKeyboardButton
	if page > 0 {
		navButtons = append(navButtons,
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.admin_prev"),
				fmt.Sprintf("adm_page:%d", page-1)))
	}
	if hasNext {
		navButtons = append(navButtons,
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.admin_next"),
				fmt.Sprintf("adm_page:%d", page+1)))
	}
	if len(navButtons) > 0 {
		rows = append(rows, navButtons)
	}

	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.admin_back"), "m:admin"),
	))

	msg := tgbotapi.NewMessage(chatID, sb.String())
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	h.sendMessage(msg)
}

func (h *Handler) handleAdminUserCallback(ctx context.Context, cq *tgbotapi.CallbackQuery, parts []string) {
	_, _ = h.api.Request(tgbotapi.NewCallback(cq.ID, ""))

	chatID := cq.Message.Chat.ID
	lang := h.getLang(ctx, cq.From.ID)

	if len(parts) < 2 {
		return
	}

	targetID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return
	}

	user, err := h.userRepo.GetByTelegramID(ctx, targetID)
	if err != nil || user == nil {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "admin.user_not_found")))
		return
	}

	site := user.JiraSiteURL
	if site == "" {
		site = "—"
	}
	connected := "no"
	if user.AccessToken != "" {
		connected = "yes"
	}

	text := locale.T(lang, "admin.user_actions", targetID,
		format.EscapeMarkdown(site), connected)

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.admin_disconnect"),
				fmt.Sprintf("adm_disc:%d", targetID)),
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.admin_delete"),
				fmt.Sprintf("adm_del:%d", targetID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.admin_back"), "adm:users"),
		),
	)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.ReplyMarkup = keyboard
	h.sendMessage(msg)
}

func (h *Handler) handleAdminDisconnect(ctx context.Context, cq *tgbotapi.CallbackQuery, parts []string) {
	_, _ = h.api.Request(tgbotapi.NewCallback(cq.ID, ""))

	chatID := cq.Message.Chat.ID
	lang := h.getLang(ctx, cq.From.ID)

	if len(parts) < 2 {
		return
	}

	targetID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return
	}

	if err := h.userRepo.UpdateTokens(ctx, targetID, "", "", time.Time{}); err != nil {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.generic")))
		return
	}

	msg := tgbotapi.NewMessage(chatID, locale.T(lang, "admin.user_disconnected_ok", targetID))
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.ReplyMarkup = adminMenuKeyboard(lang)
	h.sendMessage(msg)
}

func (h *Handler) handleAdminDelete(ctx context.Context, cq *tgbotapi.CallbackQuery, parts []string) {
	_, _ = h.api.Request(tgbotapi.NewCallback(cq.ID, ""))

	chatID := cq.Message.Chat.ID
	lang := h.getLang(ctx, cq.From.ID)

	if len(parts) < 2 {
		return
	}

	targetID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return
	}

	var errs []string
	if h.webhookMgr != nil {
		h.webhookMgr.DeleteAllForUser(ctx, targetID)
	}
	if err := h.subRepo.DeleteByUserID(ctx, targetID); err != nil {
		errs = append(errs, "subscriptions: "+err.Error())
	}
	if err := h.scheduleRepo.DeleteByUserID(ctx, targetID); err != nil {
		errs = append(errs, "schedules: "+err.Error())
	}
	if err := h.userRepo.DeleteByTelegramID(ctx, targetID); err != nil {
		errs = append(errs, "user: "+err.Error())
	}
	if len(errs) > 0 {
		h.log.Error().Strs("errors", errs).Int64("target_id", targetID).Msg("admin: partial delete failure")
	}

	msg := tgbotapi.NewMessage(chatID, locale.T(lang, "admin.user_deleted", targetID))
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.ReplyMarkup = adminMenuKeyboard(lang)
	h.sendMessage(msg)
}

func (h *Handler) handleAdminBroadcast(ctx context.Context, chatID, userID int64, text string) {
	lang := h.getLang(ctx, userID)

	users, err := h.userRepo.ListAll(ctx, 0, 10000)
	if err != nil {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.generic")))
		return
	}

	// Acknowledge immediately so the admin's update handler isn't blocked
	// for the minutes it can take to walk ~10k recipients at a 50ms-each
	// rate-limit-respecting pace.
	h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "admin.broadcast_started")))

	go func(recipients []storage.User) {
		var sent, failed int
		for i := range recipients {
			u := &recipients[i]
			if u.AccessToken == "" {
				continue
			}
			msg := tgbotapi.NewMessage(u.TelegramUserID, text)
			msg.ParseMode = tgbotapi.ModeMarkdown
			if _, err := h.api.Send(msg); err != nil {
				failed++
			} else {
				sent++
			}
			// Telegram rate limit: ~30 messages/second.
			time.Sleep(50 * time.Millisecond)
		}

		if sent == 0 && failed == 0 {
			h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "admin.broadcast_empty")))
			return
		}

		result := tgbotapi.NewMessage(chatID, locale.T(lang, "admin.broadcast_done", sent, failed))
		result.ReplyMarkup = adminMenuKeyboard(lang)
		h.sendMessage(result)
	}(users)
}

func (h *Handler) handleAdminPoller(chatID int64, lang locale.Lang) {
	if h.pollerRef == nil {
		h.sendMessage(tgbotapi.NewMessage(chatID, locale.T(lang, "error.generic")))
		return
	}

	status := h.pollerRef.Status()
	lastPoll := locale.T(lang, "admin.poller_never")
	if !status.LastPollAt.IsZero() {
		lastPoll = status.LastPollAt.Format("2006-01-02 15:04:05")
	}

	text := locale.T(lang, "admin.poller_status",
		status.Interval.String(),
		status.BatchWindow.String(),
		status.PendingCount,
		lastPoll)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.ReplyMarkup = adminMenuKeyboard(lang)
	h.sendMessage(msg)
}

func (h *Handler) handleAdminPageCallback(ctx context.Context, cq *tgbotapi.CallbackQuery, parts []string) {
	_, _ = h.api.Request(tgbotapi.NewCallback(cq.ID, ""))

	if len(parts) < 2 {
		return
	}

	page, err := strconv.Atoi(parts[1])
	if err != nil || page < 0 {
		return
	}

	lang := h.getLang(ctx, cq.From.ID)
	h.handleAdminUsers(ctx, cq.Message.Chat.ID, page, lang)
}

// SetPollerRef sets the poller reference for admin status reporting.
func (h *Handler) SetPollerRef(p *poller.Poller) {
	h.pollerRef = p
}
