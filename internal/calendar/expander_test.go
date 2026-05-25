package calendar

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExpand_NonRecurring_InWindow(t *testing.T) {
	ev := Event{
		UID:     "single@x",
		Summary: "One-off",
		Start:   time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC),
		End:     time.Date(2026, 6, 1, 11, 0, 0, 0, time.UTC),
	}
	from := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(48 * time.Hour)
	instances, err := Expand(ev, from, to)
	require.NoError(t, err)
	require.Len(t, instances, 1)
	require.Equal(t, ev.Start, instances[0].Start)
}

func TestExpand_NonRecurring_OutOfWindow(t *testing.T) {
	ev := Event{
		UID:   "single@x",
		Start: time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC),
	}
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(48 * time.Hour)
	instances, err := Expand(ev, from, to)
	require.NoError(t, err)
	require.Empty(t, instances)
}

func TestExpand_WeeklyWithExdate(t *testing.T) {
	events, err := Parse(loadFixture(t, "weekly_with_exdate.ics"))
	require.NoError(t, err)
	require.Len(t, events, 1)

	from := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)
	instances, err := Expand(events[0], from, to)
	require.NoError(t, err)
	// COUNT=5 minus one EXDATE (2026-06-15).
	require.Len(t, instances, 4)

	for _, inst := range instances {
		require.NotEqual(t, time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC).Unix(), inst.Start.Unix())
		require.True(t, inst.IsRecurring)
	}
}

func TestApplyOverrides_Replaces(t *testing.T) {
	instances := []Instance{
		{UID: "u", Start: time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC), Summary: "Original"},
		{UID: "u", Start: time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC), Summary: "Original"},
	}
	overrides := []Event{
		{
			UID:          "u",
			RecurrenceID: time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC),
			Start:        time.Date(2026, 6, 8, 11, 0, 0, 0, time.UTC),
			Summary:      "Rescheduled",
		},
	}
	out := ApplyOverrides(instances, overrides)
	require.Len(t, out, 2)
	require.Equal(t, "Original", out[0].Summary)
	require.Equal(t, "Rescheduled", out[1].Summary)
	require.Equal(t, 11, out[1].Start.UTC().Hour())
}
