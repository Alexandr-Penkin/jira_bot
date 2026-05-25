package calendar

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return b
}

func TestParse_SingleEvent(t *testing.T) {
	events, err := Parse(loadFixture(t, "single_event.ics"))
	require.NoError(t, err)
	require.Len(t, events, 1)

	ev := events[0]
	require.Equal(t, "single-event-1@test", ev.UID)
	require.Equal(t, "Standup", ev.Summary)
	require.Equal(t, "HQ", ev.Location)
	require.False(t, ev.AllDay)
	require.Equal(t, time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC), ev.Start.UTC())
	require.Equal(t, time.Date(2026, 6, 1, 11, 0, 0, 0, time.UTC), ev.End.UTC())
	require.Nil(t, ev.Recurrence)
	require.Equal(t, 0, ev.Sequence)
}

func TestParse_WeeklyWithExdate(t *testing.T) {
	events, err := Parse(loadFixture(t, "weekly_with_exdate.ics"))
	require.NoError(t, err)
	require.Len(t, events, 1)

	ev := events[0]
	require.NotNil(t, ev.Recurrence)
	require.Contains(t, ev.Recurrence.RRule, "FREQ=WEEKLY")
	require.Len(t, ev.Recurrence.ExDates, 1)
	require.Equal(t, time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC), ev.Recurrence.ExDates[0].UTC())
	require.Equal(t, 1, ev.Sequence)
}

func TestParse_AllDay(t *testing.T) {
	events, err := Parse(loadFixture(t, "all_day.ics"))
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.True(t, events[0].AllDay)
	require.Equal(t, 2026, events[0].Start.Year())
}
