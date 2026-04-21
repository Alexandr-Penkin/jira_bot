package scheduler

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog"

	"SleepJiraBot/internal/daily"
	"SleepJiraBot/internal/format"
	"SleepJiraBot/internal/jira"
	"SleepJiraBot/internal/locale"
	"SleepJiraBot/internal/storage"
	eventsv1 "SleepJiraBot/pkg/events/v1"
	"SleepJiraBot/pkg/notifier"
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
	notifier     notifier.Notifier
	log          zerolog.Logger
	mu           sync.Mutex
	cancelCtx    context.Context
	cancelFunc   context.CancelFunc
	pub          eventsv1.Publisher
}

func New(scheduleRepo *storage.ScheduleRepo, userRepo *storage.UserRepo, jiraClient *jira.Client, n notifier.Notifier, log zerolog.Logger) *Scheduler {
	ctx, cancel := context.WithCancel(context.Background())
	return &Scheduler{
		cron:         cron.New(),
		scheduleRepo: scheduleRepo,
		userRepo:     userRepo,
		jiraClient:   jiraClient,
		notifier:     n,
		log:          log,
		cancelCtx:    ctx,
		cancelFunc:   cancel,
		pub:          eventsv1.NoopPublisher{},
	}
}

// SetEventPublisher installs a domain event publisher. ScheduleDue is
// emitted on each cron fire before the JQL executes.
func (s *Scheduler) SetEventPublisher(p eventsv1.Publisher) {
	if p == nil {
		s.pub = eventsv1.NoopPublisher{}
		return
	}
	s.pub = p
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

	_ = s.pub.Publish(ctx, eventsv1.ScheduleDue{
		ReportID:   report.ID.Hex(),
		TelegramID: report.TelegramUserID,
		ChatID:     report.TelegramChatID,
		JQL:        report.JQL,
		ReportName: report.ReportName,
		FiredAt:    time.Now().Unix(),
	}, "")

	lang := s.getLang(ctx, report.TelegramUserID)

	user, err := s.userRepo.GetByTelegramID(ctx, report.TelegramUserID)
	if err != nil || user == nil || user.AccessToken == "" {
		s.log.Error().
			Int64("user_id", report.TelegramUserID).
			Str("report", report.ReportName).
			Msg("skipping report: user not connected")
		_ = s.notifier.Send(ctx, notifier.Request{
			ChatID:     report.TelegramChatID,
			TelegramID: report.TelegramUserID,
			Text:       locale.T(lang, "report.not_connected", format.EscapeMarkdown(report.ReportName)),
			ParseMode:  "Markdown",
			DedupKey:   fmt.Sprintf("scheduler:not_connected:%s:%d", report.ID.Hex(), time.Now().Unix()),
			Reason:     "scheduler:not_connected",
		})
		return
	}

	var text string
	switch report.Kind {
	case storage.ScheduleKindDaily:
		built, buildErr := daily.Build(ctx, s.jiraClient, user, lang, "")
		if buildErr != nil {
			s.log.Error().Err(buildErr).Str("report", report.ReportName).Msg("failed to build daily report")
			if sendErr := s.notifier.Send(ctx, notifier.Request{
				ChatID:     report.TelegramChatID,
				TelegramID: report.TelegramUserID,
				Text: locale.T(lang, "report.failed",
					format.EscapeMarkdown(report.ReportName),
					"Jira query failed",
				),
				ParseMode: "Markdown",
				DedupKey:  fmt.Sprintf("scheduler:failed:%s:%d", report.ID.Hex(), time.Now().Unix()),
				Reason:    "scheduler:daily_failed",
			}); sendErr != nil {
				s.log.Error().Err(sendErr).Msg("failed to send daily error notification")
			}
			return
		}
		text = built
	default:
		result, err := s.jiraClient.SearchIssues(ctx, user, report.JQL, reportMaxResults)
		if err != nil {
			s.log.Error().Err(err).Str("report", report.ReportName).Msg("failed to execute report JQL")
			if sendErr := s.notifier.Send(ctx, notifier.Request{
				ChatID:     report.TelegramChatID,
				TelegramID: report.TelegramUserID,
				Text: locale.T(lang, "report.failed",
					format.EscapeMarkdown(report.ReportName),
					"Jira query failed",
				),
				ParseMode: "Markdown",
				DedupKey:  fmt.Sprintf("scheduler:failed:%s:%d", report.ID.Hex(), time.Now().Unix()),
				Reason:    "scheduler:query_failed",
			}); sendErr != nil {
				s.log.Error().Err(sendErr).Msg("failed to send report error notification")
			}
			return
		}
		text = s.formatReport(lang, &report, result)
	}

	if err := s.notifier.Send(ctx, notifier.Request{
		ChatID:     report.TelegramChatID,
		TelegramID: report.TelegramUserID,
		Text:       text,
		ParseMode:  "Markdown",
		DedupKey:   fmt.Sprintf("scheduler:report:%s:%d", report.ID.Hex(), time.Now().Unix()),
		Reason:     "scheduler:report",
	}); err != nil {
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
