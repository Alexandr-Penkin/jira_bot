package calendar

import (
	"fmt"
	"sort"
	"time"

	"github.com/teambition/rrule-go"
)

// Instance is one concrete occurrence of an Event. For non-recurring
// events the Instance carries the same Start/End as Event; for
// recurring events Instance.Start is one of the expanded times.
type Instance struct {
	UID          string
	Summary      string
	Description  string
	Location     string
	Start        time.Time
	End          time.Time
	AllDay       bool
	LastModified time.Time
	Sequence     int
	IsRecurring  bool
}

// Expand returns concrete instances of ev that start within [from, to].
// When the event has no RRULE, returns at most one instance (the
// event itself if its Start is in the window). When the event has an
// RRULE, returns every occurrence in the window minus EXDATEs.
//
// Overrides (events with RecurrenceID set) are NOT expanded — the
// caller handles those separately via ApplyOverrides.
func Expand(ev Event, from, to time.Time) ([]Instance, error) {
	if !ev.RecurrenceID.IsZero() {
		// Overrides are applied via ApplyOverrides; do not expand them
		// as a master would otherwise produce a spurious instance at
		// the master's DTSTART.
		return nil, nil
	}
	if ev.Recurrence == nil || ev.Recurrence.RRule == "" {
		if ev.Start.Before(from) || ev.Start.After(to) {
			return nil, nil
		}
		return []Instance{instanceFromMaster(ev, ev.Start, false)}, nil
	}

	opt, err := rrule.StrToROption("RRULE:" + ev.Recurrence.RRule)
	if err != nil {
		return nil, fmt.Errorf("parse rrule: %w", err)
	}
	if opt.Dtstart.IsZero() {
		opt.Dtstart = ev.Start
	}
	r, err := rrule.NewRRule(*opt)
	if err != nil {
		return nil, fmt.Errorf("build rrule: %w", err)
	}

	occurrences := r.Between(from, to, true)
	if len(occurrences) == 0 {
		return nil, nil
	}

	excluded := make(map[int64]struct{}, len(ev.Recurrence.ExDates))
	for _, ex := range ev.Recurrence.ExDates {
		excluded[ex.Unix()] = struct{}{}
	}

	out := make([]Instance, 0, len(occurrences))
	for _, occ := range occurrences {
		if _, skip := excluded[occ.Unix()]; skip {
			continue
		}
		out = append(out, instanceFromMaster(ev, occ, true))
	}
	return out, nil
}

func instanceFromMaster(ev Event, start time.Time, recurring bool) Instance {
	var end time.Time
	if !ev.End.IsZero() && !ev.Start.IsZero() {
		end = start.Add(ev.End.Sub(ev.Start))
	}
	return Instance{
		UID:          ev.UID,
		Summary:      ev.Summary,
		Description:  ev.Description,
		Location:     ev.Location,
		Start:        start,
		End:          end,
		AllDay:       ev.AllDay,
		LastModified: ev.LastModified,
		Sequence:     ev.Sequence,
		IsRecurring:  recurring,
	}
}

// ApplyOverrides replaces instances whose Start equals an override's
// RecurrenceID with the override's data. Removes the original instance
// when STATUS=CANCELLED — but we treat any override as a replacement
// rather than honouring CANCELLED specifically; that is a follow-up.
func ApplyOverrides(instances []Instance, overrides []Event) []Instance {
	if len(overrides) == 0 {
		return instances
	}
	// Build (uid, recurrenceTS) → override.
	type key struct {
		uid string
		ts  int64
	}
	idx := make(map[key]Event, len(overrides))
	for _, ov := range overrides {
		if ov.RecurrenceID.IsZero() || ov.UID == "" {
			continue
		}
		idx[key{ov.UID, ov.RecurrenceID.Unix()}] = ov
	}

	out := make([]Instance, 0, len(instances))
	for _, inst := range instances {
		ov, ok := idx[key{inst.UID, inst.Start.Unix()}]
		if !ok {
			out = append(out, inst)
			continue
		}
		out = append(out, Instance{
			UID:          ov.UID,
			Summary:      ov.Summary,
			Description:  ov.Description,
			Location:     ov.Location,
			Start:        ov.Start,
			End:          ov.End,
			AllDay:       ov.AllDay,
			LastModified: ov.LastModified,
			Sequence:     ov.Sequence,
			IsRecurring:  true,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}
