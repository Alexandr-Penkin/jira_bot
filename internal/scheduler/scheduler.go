package scheduler

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog"

	"SleepJiraBot/internal/format"
	"SleepJiraBot/internal/jira"
	"SleepJiraBot/internal/locale"
	"SleepJiraBot/internal/storage"
)

const (
	reportMaxResults   = 20
	reportQueryTimeout = 30 * time.Second
)

type Scheduler struct {
	cron         *cron.Cron
	scheduleRepo *storage.ScheduleRepo
	userRepo     *storage.UserRepo
	jiraClient   *jira.Client
	tgAPI        *tgbotapi.BotAPI
	log          zerolog.Logger
	mu           sync.Mutex
	cancelCtx    context.Context
	cancelFunc   context.CancelFunc
}

func New(scheduleRepo *storage.ScheduleRepo, userRepo *storage.UserRepo, jiraClient *jira.Client, tgAPI *tgbotapi.BotAPI, log zerolog.Logger) *Scheduler {
	return &Scheduler{
		cron:         cron.New(),
		scheduleRepo: scheduleRepo,
		userRepo:     userRepo,
		jiraClient:   jiraClient,
		tgAPI:        tgAPI,
		log:          log,
	}
}

func (s *Scheduler) Start(ctx context.Context) error {
	s.cancelCtx, s.cancelFunc = context.WithCancel(ctx)

	s.mu.Lock()
	err := s.loadSchedules(ctx)
	s.mu.Unlock()
	if err != nil {
		s.cancelFunc()
		return fmt.Errorf("load schedules: %w", err)
	}

	s.cron.Start()
	s.log.Info().Msg("scheduler started")

	<-ctx.Done()
	s.cancelFunc()
	s.log.Info().Msg("stopping scheduler")
	stopCtx := s.cron.Stop()
	<-stopCtx.Done()

	return nil
}

func (s *Scheduler) Reload(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Stop old cron, wait for in-flight jobs, then create a new one.
	stopCtx := s.cron.Stop()
	<-stopCtx.Done()

	s.cron = cron.New()
	if err := s.loadSchedules(ctx); err != nil {
		return err
	}

	s.cron.Start()
	return nil
}

func (s *Scheduler) loadSchedules(ctx context.Context) error {
	reports, err := s.scheduleRepo.GetAllActive(ctx)
	if err != nil {
		return err
	}

	for _, report := range reports {
		if err := s.addJob(report); err != nil {
			s.log.Error().
				Err(err).
				Str("report", report.ReportName).
				Str("cron", report.CronExpression).
				Msg("failed to add scheduled job")
		}
	}

	s.log.Info().Int("count", len(reports)).Msg("loaded scheduled reports")
	return nil
}

func (s *Scheduler) addJob(report storage.ScheduledReport) error {
	r := report
	_, err := s.cron.AddFunc(r.CronExpression, func() {
		s.executeReport(r)
	})
	return err
}

func (s *Scheduler) getLang(ctx context.Context, userID int64) locale.Lang {
	user, err := s.userRepo.GetByTelegramID(ctx, userID)
	if err != nil || user == nil {
		return locale.Default
	}
	return locale.FromString(user.Language)
}

func (s *Scheduler) executeReport(report storage.ScheduledReport) {
	ctx, cancel := context.WithTimeout(s.cancelCtx, reportQueryTimeout)
	defer cancel()

	lang := s.getLang(ctx, report.TelegramUserID)

	user, err := s.userRepo.GetByTelegramID(ctx, report.TelegramUserID)
	if err != nil || user == nil || user.AccessToken == "" {
		s.log.Error().
			Int64("user_id", report.TelegramUserID).
			Str("report", report.ReportName).
			Msg("skipping report: user not connected")
		errMsg := tgbotapi.NewMessage(report.TelegramChatID,
			locale.T(lang, "report.not_connected", format.EscapeMarkdown(report.ReportName)))
		errMsg.ParseMode = tgbotapi.ModeMarkdown
		_, _ = s.tgAPI.Send(errMsg)
		return
	}

	result, err := s.jiraClient.SearchIssues(ctx, user, report.JQL, reportMaxResults)
	if err != nil {
		s.log.Error().Err(err).Str("report", report.ReportName).Msg("failed to execute report JQL")
		errMsg := tgbotapi.NewMessage(report.TelegramChatID,
			locale.T(lang, "report.failed",
				format.EscapeMarkdown(report.ReportName),
				format.EscapeMarkdown(err.Error()),
			))
		errMsg.ParseMode = tgbotapi.ModeMarkdown
		if _, sendErr := s.tgAPI.Send(errMsg); sendErr != nil {
			s.log.Error().Err(sendErr).Msg("failed to send report error notification")
		}
		return
	}

	text := s.formatReport(lang, &report, result)
	msg := tgbotapi.NewMessage(report.TelegramChatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown

	if _, err := s.tgAPI.Send(msg); err != nil {
		s.log.Error().Err(err).Int64("chat_id", report.TelegramChatID).Msg("failed to send report")
	}
}

func (s *Scheduler) formatReport(lang locale.Lang, report *storage.ScheduledReport, result *jira.SearchResult) string {
	var sb strings.Builder

	name := report.ReportName
	if name == "" {
		name = locale.T(lang, "report.default_name")
	}

	fmt.Fprintf(&sb, "📊 *%s*\n", format.EscapeMarkdown(name))
	fmt.Fprintf(&sb, "%s\n\n", locale.T(lang, "report.found", result.Total))

	if len(result.Issues) == 0 {
		sb.WriteString(locale.T(lang, "report.no_issues"))
		return sb.String()
	}

	for i := range result.Issues {
		issue := &result.Issues[i]
		status := "?"
		if issue.Fields.Status != nil {
			status = issue.Fields.Status.Name
		}
		priority := ""
		if issue.Fields.Priority != nil {
			priority = issue.Fields.Priority.Name + " "
		}
		fmt.Fprintf(&sb, "%d. `%s` \\[%s] %s%s\n",
			i+1,
			issue.Key,
			format.EscapeMarkdown(status),
			format.EscapeMarkdown(priority),
			format.EscapeMarkdown(issue.Fields.Summary),
		)
	}

	if result.Total > len(result.Issues) {
		sb.WriteString(locale.T(lang, "report.more", result.Total-len(result.Issues)))
	}

	return sb.String()
}
