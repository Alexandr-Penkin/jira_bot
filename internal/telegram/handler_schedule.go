package telegram

import (
	"context"
	"fmt"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"SleepJiraBot/internal/format"
	"SleepJiraBot/internal/locale"
	"SleepJiraBot/internal/storage"
)

func (h *Handler) handleSchedule(ctx context.Context, chatID, userID int64, input string) tgbotapi.MessageConfig {
	lang := h.getLang(ctx, userID)

	parts := strings.SplitN(input, "|", 3)
	if len(parts) < 3 {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "schedule.invalid_format"))
	}

	cronExpr := strings.TrimSpace(parts[0])
	reportName := strings.TrimSpace(parts[1])
	jql := strings.TrimSpace(parts[2])

	if cronExpr == "" || reportName == "" || jql == "" {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "schedule.fields_required"))
	}

	if len(reportName) > maxReportNameLen {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "schedule.name_too_long", maxReportNameLen))
	}

	if len(jql) > maxJQLLen {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "list.jql_too_long", maxJQLLen))
	}

	if err := validateCronExpression(cronExpr); err != nil {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "schedule.invalid_cron", err.Error()))
	}

	report := &storage.ScheduledReport{
		TelegramChatID: chatID,
		TelegramUserID: userID,
		CronExpression: cronExpr,
		JQL:            jql,
		ReportName:     reportName,
	}

	if err := h.scheduleRepo.Create(ctx, report); err != nil {
		h.log.Error().Err(err).Msg("failed to create schedule")
		return tgbotapi.NewMessage(chatID, locale.T(lang, "schedule.failed"))
	}

	if h.onScheduleChange != nil {
		h.onScheduleChange()
	}

	text := locale.T(lang, "schedule.created",
		format.EscapeMarkdown(reportName),
		cronExpr,
		format.EscapeMarkdown(jql),
	)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	return msg
}

func (h *Handler) handleUnschedule(ctx context.Context, chatID, userID int64) tgbotapi.MessageConfig {
	lang := h.getLang(ctx, userID)
	if err := h.scheduleRepo.DeleteByChat(ctx, chatID); err != nil {
		h.log.Error().Err(err).Msg("failed to delete schedules")
		return tgbotapi.NewMessage(chatID, locale.T(lang, "unschedule.failed"))
	}

	if h.onScheduleChange != nil {
		h.onScheduleChange()
	}

	return tgbotapi.NewMessage(chatID, locale.T(lang, "unschedule.success"))
}

func (h *Handler) handleSchedules(ctx context.Context, chatID, userID int64) tgbotapi.MessageConfig {
	lang := h.getLang(ctx, userID)
	reports, err := h.scheduleRepo.GetByChat(ctx, chatID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to get schedules")
		return tgbotapi.NewMessage(chatID, locale.T(lang, "schedules.failed"))
	}

	if len(reports) == 0 {
		return tgbotapi.NewMessage(chatID, locale.T(lang, "schedules.none"))
	}

	text := locale.T(lang, "schedules.title")
	for i, r := range reports {
		text += fmt.Sprintf("%d. *%s*\n   Cron: `%s`\n   JQL: `%s`\n\n",
			i+1,
			format.EscapeMarkdown(r.ReportName),
			r.CronExpression,
			format.EscapeMarkdown(r.JQL),
		)
	}

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	return msg
}
