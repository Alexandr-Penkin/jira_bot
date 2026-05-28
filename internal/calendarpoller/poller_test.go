package calendarpoller

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"SleepJiraBot/internal/calendar"
	"SleepJiraBot/internal/storage"
	"SleepJiraBot/pkg/notifier"
)

// fakeUserRepo lets the test drive ListUsersWithCalendar /
// MarkCalendarFetched without standing up a real Mongo.
type fakeUserRepo struct {
	mu             sync.Mutex
	users          []storage.User
	markedErrs     []string
	markedSuccess  int
	lastFetchedAt0 bool
}

func (f *fakeUserRepo) ListUsersWithCalendar(_ context.Context) ([]storage.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]storage.User, len(f.users))
	copy(out, f.users)
	return out, nil
}

func (f *fakeUserRepo) MarkCalendarFetched(_ context.Context, _ int64, errMsg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if errMsg == "" {
		f.markedSuccess++
	} else {
		f.markedErrs = append(f.markedErrs, errMsg)
	}
	// Once we mark "fetched" with no error, the user has a non-zero
	// CalendarLastFetchedAt for the next tick.
	for i := range f.users {
		f.users[i].CalendarLastFetchedAt = time.Now().Unix()
	}
	return nil
}

// memEventRepo is an in-memory storage.CalendarEventRepo.
type memEventRepo struct {
	mu       sync.Mutex
	states   map[string]*storage.CalendarEventState
	upserted int
}

func newMemEventRepo() *memEventRepo {
	return &memEventRepo{states: map[string]*storage.CalendarEventState{}}
}

func keyOf(uid int64, eventUID string, ts int64) string {
	return strKey(uid, eventUID, ts)
}

func strKey(uid int64, eventUID string, ts int64) string {
	return eventUID + ":" + intToStr(uid) + ":" + intToStr(ts)
}

func intToStr(n int64) string {
	return time.Unix(n, 0).Format("20060102T150405")
}

func (m *memEventRepo) UpsertState(_ context.Context, s *storage.CalendarEventState) (storage.CalendarEventStateChange, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := keyOf(s.TelegramUserID, s.UID, s.InstanceTime)
	prev := m.states[k]
	change := storage.CalendarEventStateChange{Prev: prev}
	if prev == nil {
		change.Created = true
	} else {
		change.Changed = prev.Summary != s.Summary || prev.Start != s.Start
	}
	cp := *s
	cp.SeenAt = time.Now().Unix()
	m.states[k] = &cp
	m.upserted++
	return change, nil
}

func (m *memEventRepo) GetByUserAndInstance(_ context.Context, uid int64, eventUID string, instanceTS int64) (*storage.CalendarEventState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.states[keyOf(uid, eventUID, instanceTS)], nil
}

func (m *memEventRepo) MarkReminderSent(_ context.Context, uid int64, eventUID string, instanceTS int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.states[keyOf(uid, eventUID, instanceTS)]; ok {
		s.ReminderSent = true
	}
	return nil
}

func (m *memEventRepo) MarkNotifiedNew(_ context.Context, uid int64, eventUID string, instanceTS int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.states[keyOf(uid, eventUID, instanceTS)]; ok {
		s.NotifiedNew = true
	}
	return nil
}

func (m *memEventRepo) DeleteStaleByUser(_ context.Context, _ int64, _ int64) error { return nil }
func (m *memEventRepo) DeleteAllByUser(_ context.Context, _ int64) error            { return nil }
func (m *memEventRepo) EnsureIndexes(_ context.Context) error                       { return nil }

// recordingNotifier records every Send call.
type recordingNotifier struct {
	mu       sync.Mutex
	requests []notifier.Request
}

func (r *recordingNotifier) Send(_ context.Context, req notifier.Request) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, req)
	return nil
}

func (r *recordingNotifier) reasons() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.requests))
	for _, req := range r.requests {
		out = append(out, req.Reason)
	}
	sort.Strings(out)
	return out
}

// stubFetcher returns a fixed body.
type stubFetcher struct {
	body []byte
	err  error
}

func (s stubFetcher) Fetch(_ context.Context, _ string) ([]byte, error) {
	return s.body, s.err
}

// stubParser yields a canned slice without touching golang-ical.
type stubParser struct{ events []calendar.Event }

func (s stubParser) Parse(_ []byte) ([]calendar.Event, error) { return s.events, nil }

// stubExpander mirrors the package implementation but keeps the
// test independent of rrule library specifics.
type stubExpander struct{}

func (stubExpander) Expand(ev calendar.Event, from, to time.Time) ([]calendar.Instance, error) {
	if ev.Start.Before(from) || ev.Start.After(to) {
		return nil, nil
	}
	return []calendar.Instance{{
		UID:     ev.UID,
		Summary: ev.Summary,
		Start:   ev.Start,
		End:     ev.End,
	}}, nil
}

func (stubExpander) ApplyOverrides(in []calendar.Instance, _ []calendar.Event) []calendar.Instance {
	return in
}

func newTestPoller(t *testing.T, ur *fakeUserRepo, er storage.CalendarEventRepo, fetcher Fetcher, parser Parser, notif notifier.Notifier) *Poller {
	t.Helper()
	// newHorizon=time.Hour matches the default — every test event in this
	// suite is scheduled within an hour of "now".
	return New(ur, er, fetcher, parser, stubExpander{}, notif, nil, zerolog.Nop(), time.Minute, time.Hour, time.Hour, 15)
}

func TestPoller_FirstFetchSuppressesNewNotifications(t *testing.T) {
	user := storage.User{
		TelegramUserID:        42,
		CalendarURL:           "https://x.test/cal.ics",
		CalendarNotifyEnabled: true,
		CalendarLastFetchedAt: 0, // never fetched
	}
	ur := &fakeUserRepo{users: []storage.User{user}}
	er := newMemEventRepo()
	notif := &recordingNotifier{}

	events := []calendar.Event{
		{UID: "e1", Summary: "Future", Start: time.Now().Add(30 * time.Minute)},
		{UID: "e2", Summary: "Future 2", Start: time.Now().Add(45 * time.Minute)},
	}
	p := newTestPoller(t, ur, er, stubFetcher{body: []byte("BEGIN:VCALENDAR")}, stubParser{events: events}, notif)
	p.pollAll(context.Background())

	require.Empty(t, notif.requests, "first-poll backfill must not send 'new event' notifications")
	require.Equal(t, 1, ur.markedSuccess)
}

func TestPoller_NewEventBeyondHorizonSuppressed(t *testing.T) {
	user := storage.User{
		TelegramUserID:        42,
		CalendarURL:           "https://x.test/cal.ics",
		CalendarNotifyEnabled: true,
		CalendarLastFetchedAt: time.Now().Unix() - 600,
	}
	ur := &fakeUserRepo{users: []storage.User{user}}
	er := newMemEventRepo()
	notif := &recordingNotifier{}

	// Event starts in 3 hours — comfortably past the default 1h
	// horizon used by newTestPoller; "🆕" must NOT fire.
	events := []calendar.Event{
		{UID: "far", Summary: "Tomorrow's meeting", Start: time.Now().Add(3 * time.Hour)},
	}
	p := newTestPoller(t, ur, er, stubFetcher{body: []byte("ok")}, stubParser{events: events}, notif)
	// Widen lookahead so the stub expander surfaces the event; the
	// horizon guard, not the lookahead, is what should suppress "new".
	p.lookahead = 24 * time.Hour
	p.pollAll(context.Background())

	require.Empty(t, notif.reasons(), "events outside newHorizon must not trigger 🆕")
	// And the state must be marked so a follow-up tick doesn't
	// surface it as new either.
	state, _ := er.GetByUserAndInstance(context.Background(), 42, "far", events[0].Start.Unix())
	require.NotNil(t, state)
	require.True(t, state.NotifiedNew)
}

func TestPoller_NewEventOnSecondTick(t *testing.T) {
	user := storage.User{
		TelegramUserID:        42,
		CalendarURL:           "https://x.test/cal.ics",
		CalendarNotifyEnabled: true,
		CalendarLastFetchedAt: time.Now().Unix() - 600, // already fetched
	}
	ur := &fakeUserRepo{users: []storage.User{user}}
	er := newMemEventRepo()
	notif := &recordingNotifier{}

	events := []calendar.Event{
		{UID: "fresh", Summary: "Brand new", Start: time.Now().Add(45 * time.Minute)},
	}
	p := newTestPoller(t, ur, er, stubFetcher{body: []byte("ok")}, stubParser{events: events}, notif)
	p.pollAll(context.Background())

	require.Len(t, notif.requests, 1)
	require.Equal(t, "calendar:new", notif.requests[0].Reason)
}

func TestPoller_ChangedEventEmitsChange(t *testing.T) {
	now := time.Now()
	user := storage.User{
		TelegramUserID:        42,
		CalendarURL:           "https://x.test/cal.ics",
		CalendarNotifyEnabled: true,
		CalendarLastFetchedAt: now.Unix() - 600,
	}
	ur := &fakeUserRepo{users: []storage.User{user}}
	er := newMemEventRepo()

	// Pre-seed an existing state with a different summary.
	_, _ = er.UpsertState(context.Background(), &storage.CalendarEventState{
		TelegramUserID: 42,
		UID:            "rec",
		InstanceTime:   now.Add(60 * time.Minute).Unix(),
		Summary:        "Old",
		Start:          now.Add(60 * time.Minute).Unix(),
		NotifiedNew:    true,
	})

	notif := &recordingNotifier{}
	events := []calendar.Event{
		{UID: "rec", Summary: "New", Start: now.Add(60 * time.Minute)},
	}
	p := newTestPoller(t, ur, er, stubFetcher{body: []byte("ok")}, stubParser{events: events}, notif)
	p.pollAll(context.Background())

	reasons := notif.reasons()
	require.Contains(t, reasons, "calendar:changed")
}

func TestPoller_ReminderFiresInWindow(t *testing.T) {
	now := time.Now()
	user := storage.User{
		TelegramUserID:          42,
		CalendarURL:             "https://x.test/cal.ics",
		CalendarNotifyEnabled:   true,
		CalendarReminderMinutes: 15,
		CalendarLastFetchedAt:   now.Unix() - 600,
	}
	ur := &fakeUserRepo{users: []storage.User{user}}
	er := newMemEventRepo()
	notif := &recordingNotifier{}

	// Instance starts in 10 minutes; with reminderMins=15, reminderAt
	// is 5 minutes ago, which falls within [now-interval, now].
	events := []calendar.Event{
		{UID: "soon", Summary: "Stand-up", Start: now.Add(10 * time.Minute)},
	}
	p := newTestPoller(t, ur, er, stubFetcher{body: []byte("ok")}, stubParser{events: events}, notif)
	// Make the interval wide enough so the reminder window is hit.
	p.interval = 10 * time.Minute
	p.pollAll(context.Background())

	require.Contains(t, notif.reasons(), "calendar:reminder")

	// Second tick: ReminderSent should now be true, no fresh notification.
	notif.mu.Lock()
	notif.requests = nil
	notif.mu.Unlock()
	p.pollAll(context.Background())
	require.NotContains(t, notif.reasons(), "calendar:reminder")
}

func TestPoller_ReminderFiresEvenWhenLagExceedsInterval(t *testing.T) {
	// Regression: the reminder gate used to require
	// `now.Sub(reminderAt) <= p.interval`, so when the poll cadence was
	// shorter than the time already elapsed since reminderAt (e.g. a
	// 60s poll catching a reminder whose reminderAt was 5 minutes ago,
	// or any cycle drift past the interval), the reminder was silently
	// dropped and no later cycle ever picked it up. The ReminderSent
	// flag is the only idempotency guard now — any cycle inside the
	// lead window is allowed to fire.
	now := time.Now()
	user := storage.User{
		TelegramUserID:          42,
		CalendarURL:             "https://x.test/cal.ics",
		CalendarNotifyEnabled:   true,
		CalendarReminderMinutes: 15,
		CalendarLastFetchedAt:   now.Unix() - 600,
	}
	ur := &fakeUserRepo{users: []storage.User{user}}
	er := newMemEventRepo()
	notif := &recordingNotifier{}

	// Event starts in 10 minutes; reminderAt = now-5m. With interval=1m
	// the previous gate (5m <= 1m) was false and the reminder was lost.
	events := []calendar.Event{
		{UID: "soon", Summary: "Stand-up", Start: now.Add(10 * time.Minute)},
	}
	p := newTestPoller(t, ur, er, stubFetcher{body: []byte("ok")}, stubParser{events: events}, notif)
	p.interval = 1 * time.Minute
	p.pollAll(context.Background())

	require.Contains(t, notif.reasons(), "calendar:reminder")
}

func TestPoller_DisabledUserSkipped(t *testing.T) {
	user := storage.User{
		TelegramUserID:        42,
		CalendarURL:           "https://x.test/cal.ics",
		CalendarNotifyEnabled: false,
	}
	ur := &fakeUserRepo{users: nil} // ListUsersWithCalendar already filters by enabled flag
	er := newMemEventRepo()
	notif := &recordingNotifier{}

	_ = user
	p := newTestPoller(t, ur, er, stubFetcher{}, stubParser{}, notif)
	p.pollAll(context.Background())
	require.Empty(t, notif.requests)
}

func TestPoller_FetchErrorMarksAndSkips(t *testing.T) {
	user := storage.User{
		TelegramUserID:        42,
		CalendarURL:           "https://x.test/cal.ics",
		CalendarNotifyEnabled: true,
	}
	ur := &fakeUserRepo{users: []storage.User{user}}
	er := newMemEventRepo()
	notif := &recordingNotifier{}

	p := newTestPoller(t, ur, er, stubFetcher{err: errFakeFetch}, stubParser{}, notif)
	p.pollAll(context.Background())
	require.Empty(t, notif.requests)
	require.NotEmpty(t, ur.markedErrs)
}

type fakeFetchErr struct{}

func (fakeFetchErr) Error() string { return "fetch boom" }

var errFakeFetch = fakeFetchErr{}
