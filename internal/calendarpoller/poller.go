// Package calendarpoller delivers ICS-based event notifications.
// It fetches each user's ICS feed on a fixed interval, expands
// recurring events inside a lookahead window, and emits two kinds of
// Telegram notifications:
//
//   - "New event" / "Event changed" when an ICS UID first appears (or
//     when DTSTART/SUMMARY/SEQUENCE move on a known UID),
//   - "Starts in N min" reminders when an instance crosses into
//     [now+remindMins, now+remindMins+tickInterval).
//
// State lives in the `calendar_events` Mongo collection so reminders
// and "new" suppression survive process restarts.
package calendarpoller

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"SleepJiraBot/internal/calendar"
	"SleepJiraBot/internal/locale"
	"SleepJiraBot/internal/notifydedup"
	"SleepJiraBot/internal/storage"
	"SleepJiraBot/pkg/notifier"
)

const (
	defaultInterval       = 5 * time.Minute
	defaultLookahead      = 24 * time.Hour
	defaultReminderMins   = 15
	defaultNewHorizon     = 1 * time.Hour
	fetchTimeout          = 30 * time.Second
	staleInstanceLookback = 7 * 24 * time.Hour
)

// UserRepoIface is the narrow surface the poller needs from
// storage.UserRepo. Kept private so tests can drop in a stub without
// implementing the entire user repo.
type UserRepoIface interface {
	ListUsersWithCalendar(ctx context.Context) ([]storage.User, error)
	MarkCalendarFetched(ctx context.Context, telegramUserID int64, errMsg string) error
}

// Parser parses an ICS body to events. Aliased so tests can supply a
// canned slice without a real ICS payload.
type Parser interface {
	Parse(raw []byte) ([]calendar.Event, error)
}

// Expander expands events with RRULE into concrete instances inside
// the [from, to] window.
type Expander interface {
	Expand(ev calendar.Event, from, to time.Time) ([]calendar.Instance, error)
	ApplyOverrides(instances []calendar.Instance, overrides []calendar.Event) []calendar.Instance
}

// Fetcher pulls the ICS body. Decoupled into an interface so tests
// can return a fixture without spinning up an httptest.Server.
type Fetcher interface {
	Fetch(ctx context.Context, url string) ([]byte, error)
}

// Poller runs the calendar feedback loop.
type Poller struct {
	userRepo            UserRepoIface
	eventRepo           storage.CalendarEventRepo
	fetcher             Fetcher
	parser              Parser
	expander            Expander
	notif               notifier.Notifier
	dedup               notifydedup.Allower
	log                 zerolog.Logger
	interval            time.Duration
	lookahead           time.Duration
	defaultReminderMins int
	newHorizon          time.Duration
	mu                  sync.Mutex
	lastPollAt          time.Time
}

// New constructs a Poller. Pass nil for dedup to disable the in-window
// guard — the durable state flags still prevent re-fires.
func New(
	userRepo UserRepoIface,
	eventRepo storage.CalendarEventRepo,
	fetcher Fetcher,
	parser Parser,
	expander Expander,
	notif notifier.Notifier,
	dedup notifydedup.Allower,
	log zerolog.Logger,
	interval, lookahead, newHorizon time.Duration,
	defaultReminderMin int,
) *Poller {
	if interval <= 0 {
		interval = defaultInterval
	}
	if lookahead <= 0 {
		lookahead = defaultLookahead
	}
	if defaultReminderMin <= 0 {
		defaultReminderMin = defaultReminderMins
	}
	if newHorizon <= 0 {
		newHorizon = defaultNewHorizon
	}
	return &Poller{
		userRepo:            userRepo,
		eventRepo:           eventRepo,
		fetcher:             fetcher,
		parser:              parser,
		expander:            expander,
		notif:               notif,
		dedup:               dedup,
		log:                 log,
		interval:            interval,
		lookahead:           lookahead,
		defaultReminderMins: defaultReminderMin,
		newHorizon:          newHorizon,
	}
}

// Start runs the poll loop until ctx is cancelled.
func (p *Poller) Start(ctx context.Context) {
	p.log.Info().
		Dur("interval", p.interval).
		Dur("lookahead", p.lookahead).
		Int("default_reminder_min", p.defaultReminderMins).
		Msg("calendar poller started")

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	// Initial tick so a restart doesn't wait p.interval for first work.
	p.pollAll(ctx)
	for {
		select {
		case <-ctx.Done():
			p.log.Info().Msg("calendar poller stopped")
			return
		case <-ticker.C:
			p.pollAll(ctx)
		}
	}
}

func (p *Poller) pollAll(ctx context.Context) {
	p.mu.Lock()
	p.lastPollAt = time.Now()
	p.mu.Unlock()

	users, err := p.userRepo.ListUsersWithCalendar(ctx)
	if err != nil {
		p.log.Error().Err(err).Msg("calendar poller: list users failed")
		return
	}
	if len(users) == 0 {
		return
	}
	p.log.Info().Int("users", len(users)).Msg("calendar poller: cycle start")

	for i := range users {
		if ctx.Err() != nil {
			return
		}
		p.pollUser(ctx, &users[i])
	}
}

// pollUser is the per-user happy path: fetch, parse, expand, diff,
// notify. Errors are logged + recorded via MarkCalendarFetched so the
// user sees them in the profile menu rather than silently disappearing.
func (p *Poller) pollUser(ctx context.Context, user *storage.User) {
	if user.CalendarURL == "" {
		return
	}
	fetchCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
	body, err := p.fetcher.Fetch(fetchCtx, user.CalendarURL)
	cancel()
	if err != nil {
		p.log.Warn().Err(err).Int64("user_id", user.TelegramUserID).Msg("calendar fetch failed")
		_ = p.userRepo.MarkCalendarFetched(ctx, user.TelegramUserID, "fetch: "+err.Error())
		return
	}
	events, err := p.parser.Parse(body)
	if err != nil {
		p.log.Warn().Err(err).Int64("user_id", user.TelegramUserID).Msg("calendar parse failed")
		_ = p.userRepo.MarkCalendarFetched(ctx, user.TelegramUserID, "parse: "+err.Error())
		return
	}

	now := time.Now()
	windowEnd := now.Add(p.lookahead)

	// Partition: masters vs overrides. Overrides modify expanded
	// instances of their master AFTER expansion.
	masters := make([]calendar.Event, 0, len(events))
	overrides := make([]calendar.Event, 0)
	for _, ev := range events {
		if !ev.RecurrenceID.IsZero() {
			overrides = append(overrides, ev)
		} else {
			masters = append(masters, ev)
		}
	}

	var instances []calendar.Instance
	for _, ev := range masters {
		expanded, err := p.expander.Expand(ev, now, windowEnd)
		if err != nil {
			p.log.Warn().Err(err).
				Str("event_uid", ev.UID).
				Int64("user_id", user.TelegramUserID).
				Msg("calendar expand failed")
			continue
		}
		instances = append(instances, expanded...)
	}
	instances = p.expander.ApplyOverrides(instances, overrides)

	reminderMins := user.CalendarReminderMinutes
	if reminderMins <= 0 {
		reminderMins = p.defaultReminderMins
	}
	reminderWindow := time.Duration(reminderMins) * time.Minute

	// First-fetch suppression: avoid spamming "new event" for every
	// existing event when the user first connects their calendar.
	// CalendarLastFetchedAt is zero until MarkCalendarFetched is
	// called the first time below.
	firstFetch := user.CalendarLastFetchedAt == 0

	lang := locale.FromString(user.Language)

	for i := range instances {
		inst := &instances[i]

		state := &storage.CalendarEventState{
			TelegramUserID: user.TelegramUserID,
			UID:            inst.UID,
			InstanceTime:   inst.Start.Unix(),
			Summary:        inst.Summary,
			Start:          inst.Start.Unix(),
			Location:       inst.Location,
			Sequence:       inst.Sequence,
		}
		if !inst.End.IsZero() {
			state.End = inst.End.Unix()
		}
		if !inst.LastModified.IsZero() {
			state.LastModified = inst.LastModified.Unix()
		}
		// On the very first fetch, mark every state as already-notified
		// so we don't dump the whole upcoming window into Telegram in
		// one go. Reminders still fire normally because that decision
		// is based on the time window, not on NotifiedNew.
		if firstFetch {
			state.NotifiedNew = true
		}

		change, err := p.eventRepo.UpsertState(ctx, state)
		if err != nil {
			p.log.Error().Err(err).
				Int64("user_id", user.TelegramUserID).
				Str("event_uid", inst.UID).
				Msg("calendar poller: upsert state failed")
			continue
		}

		// "New event" notification. Only fire when the event is still
		// in the future AND within the configured `newHorizon` — users
		// reported being pinged for every meeting that landed in the
		// next 24h of their feed, which spams them about tomorrow's
		// stand-ups they already know about. Events further out are
		// still recorded in state, so the regular "starts in N min"
		// reminder kicks in when the time comes.
		inWindow := inst.Start.After(now) && inst.Start.Sub(now) <= p.newHorizon
		if !firstFetch && change.Created && inWindow {
			if p.allow(user.TelegramUserID, "new", inst) {
				if err := p.sendNew(ctx, user, lang, inst); err != nil {
					p.log.Warn().Err(err).Msg("calendar poller: send new failed")
				} else {
					_ = p.eventRepo.MarkNotifiedNew(ctx, user.TelegramUserID, inst.UID, inst.Start.Unix())
				}
			}
		} else if !firstFetch && change.Created {
			// Beyond the new-event horizon we silently record the
			// event so we don't re-evaluate it on every subsequent
			// tick — but never resurrect it as "new" once we cross
			// into the horizon either: the reminder is the right
			// signal at that point.
			_ = p.eventRepo.MarkNotifiedNew(ctx, user.TelegramUserID, inst.UID, inst.Start.Unix())
		}

		// "Event changed" notification.
		if change.Changed && change.Prev != nil {
			if p.allow(user.TelegramUserID, "changed", inst) {
				if err := p.sendChanged(ctx, user, lang, inst, change.Prev); err != nil {
					p.log.Warn().Err(err).Msg("calendar poller: send changed failed")
				}
			}
		}

		// Reminder window: notify if the instance is starting within
		// (reminderMins, reminderMins+interval] from now. Skip when
		// the durable flag says we already did it.
		reminderAt := inst.Start.Add(-reminderWindow)
		if now.After(reminderAt) || now.Equal(reminderAt) {
			if inst.Start.After(now) && now.Sub(reminderAt) <= p.interval {
				prevSent := change.Prev != nil && change.Prev.ReminderSent
				if !prevSent && p.allow(user.TelegramUserID, "reminder", inst) {
					if err := p.sendReminder(ctx, user, lang, inst, reminderMins); err != nil {
						p.log.Warn().Err(err).Msg("calendar poller: send reminder failed")
					} else {
						_ = p.eventRepo.MarkReminderSent(ctx, user.TelegramUserID, inst.UID, inst.Start.Unix())
					}
				}
			}
		}
	}

	// House-keeping: drop stale rows so the user's slice of
	// calendar_events stays small. TTL on seen_at is the longer
	// safety net; this is the eager cleanup that keeps "fresh" users
	// from accumulating week-old instances.
	cutoff := now.Add(-staleInstanceLookback).Unix()
	if err := p.eventRepo.DeleteStaleByUser(ctx, user.TelegramUserID, cutoff); err != nil {
		p.log.Debug().Err(err).Int64("user_id", user.TelegramUserID).Msg("calendar poller: stale cleanup")
	}

	_ = p.userRepo.MarkCalendarFetched(ctx, user.TelegramUserID, "")
}

func (p *Poller) allow(telegramID int64, kind string, inst *calendar.Instance) bool {
	if p.dedup == nil {
		return true
	}
	key := fmt.Sprintf("cal:%s:%s:%d", kind, inst.UID, inst.Start.Unix())
	return p.dedup.Allow(telegramID, key)
}

func (p *Poller) sendNew(ctx context.Context, user *storage.User, lang locale.Lang, inst *calendar.Instance) error {
	when := formatInstanceWhen(inst, lang)
	text := locale.T(lang, "calendar.notif.new", inst.Summary, when)
	if inst.Location != "" {
		text += "\n" + locale.T(lang, "calendar.field.where") + ": " + inst.Location
	}
	text += renderDescription(lang, inst.Description)
	return p.notif.Send(ctx, notifier.Request{
		ChatID:                user.TelegramUserID,
		TelegramID:            user.TelegramUserID,
		Text:                  text,
		ParseMode:             "Markdown",
		DisableWebPagePreview: true,
		DedupKey:              fmt.Sprintf("cal:new:%d:%s:%d", user.TelegramUserID, inst.UID, inst.Start.Unix()),
		Reason:                "calendar:new",
	})
}

func (p *Poller) sendChanged(ctx context.Context, user *storage.User, lang locale.Lang, inst *calendar.Instance, prev *storage.CalendarEventState) error {
	var diff strings.Builder
	if prev.Summary != inst.Summary {
		fmt.Fprintf(&diff, "\n%s: %s → %s", locale.T(lang, "calendar.field.summary"), prev.Summary, inst.Summary)
	}
	if prev.Start != inst.Start.Unix() {
		prevWhen := time.Unix(prev.Start, 0).Format("2006-01-02 15:04")
		newWhen := inst.Start.Format("2006-01-02 15:04")
		fmt.Fprintf(&diff, "\n%s: %s → %s", locale.T(lang, "calendar.field.when"), prevWhen, newWhen)
	}
	if prev.Location != inst.Location {
		fmt.Fprintf(&diff, "\n%s: %q → %q", locale.T(lang, "calendar.field.where"), prev.Location, inst.Location)
	}
	when := formatInstanceWhen(inst, lang)
	text := locale.T(lang, "calendar.notif.changed", inst.Summary, when, diff.String())
	text += renderDescription(lang, inst.Description)
	return p.notif.Send(ctx, notifier.Request{
		ChatID:                user.TelegramUserID,
		TelegramID:            user.TelegramUserID,
		Text:                  text,
		ParseMode:             "Markdown",
		DisableWebPagePreview: true,
		DedupKey:              fmt.Sprintf("cal:changed:%d:%s:%d", user.TelegramUserID, inst.UID, inst.Start.Unix()),
		Reason:                "calendar:changed",
	})
}

func (p *Poller) sendReminder(ctx context.Context, user *storage.User, lang locale.Lang, inst *calendar.Instance, minsBefore int) error {
	loc := ""
	if inst.Location != "" {
		loc = "\n" + locale.T(lang, "calendar.field.where") + ": " + inst.Location
	}
	text := locale.T(lang, "calendar.notif.reminder", inst.Summary, minsBefore, loc)
	text += renderDescription(lang, inst.Description)
	return p.notif.Send(ctx, notifier.Request{
		ChatID:                user.TelegramUserID,
		TelegramID:            user.TelegramUserID,
		Text:                  text,
		ParseMode:             "Markdown",
		DisableWebPagePreview: true,
		DedupKey:              fmt.Sprintf("cal:reminder:%d:%s:%d", user.TelegramUserID, inst.UID, inst.Start.Unix()),
		Reason:                "calendar:reminder",
	})
}

// formatInstanceWhen returns the local-time representation of when the
// instance starts. All-day events drop the clock segment.
func formatInstanceWhen(inst *calendar.Instance, _ locale.Lang) string {
	if inst.AllDay {
		return inst.Start.Format("2006-01-02")
	}
	return inst.Start.Format("2006-01-02 15:04 MST")
}

// descriptionMaxRunes caps the rendered description per notification.
// Telegram messages are limited to 4096 chars and we already spend a
// few hundred on summary/when/location; 500 keeps the body readable
// without hitting the cap on calendars that paste long agendas.
const descriptionMaxRunes = 500

// renderDescription returns the localised description block to append
// to a notification body. Returns empty when the description is
// blank. ICS DESCRIPTION values commonly carry escaped "\n" / "\," —
// unescape them so the user sees a real newline instead of "\n".
func renderDescription(lang locale.Lang, desc string) string {
	desc = strings.TrimSpace(desc)
	if desc == "" {
		return ""
	}
	desc = strings.ReplaceAll(desc, `\n`, "\n")
	desc = strings.ReplaceAll(desc, `\,`, ",")
	desc = strings.ReplaceAll(desc, `\;`, ";")
	desc = strings.ReplaceAll(desc, `\\`, `\`)

	if runes := []rune(desc); len(runes) > descriptionMaxRunes {
		desc = string(runes[:descriptionMaxRunes]) + "…"
	}
	return "\n" + locale.T(lang, "calendar.field.description") + ": " + desc
}
