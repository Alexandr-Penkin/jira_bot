package calendar

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"

	ics "github.com/arran4/golang-ical"
)

// Event is the library-neutral representation of a VEVENT we care about.
// Recurring events carry a non-nil Recurrence; instance expansion is
// done downstream by Expander.
type Event struct {
	UID          string
	Summary      string
	Description  string
	Location     string
	Start        time.Time
	End          time.Time
	AllDay       bool
	LastModified time.Time
	Sequence     int

	// RecurrenceID, when non-zero, marks this event as an override of a
	// recurring master — the matching expanded instance should be
	// replaced with this override.
	RecurrenceID time.Time

	Recurrence *RecurrenceSpec
}

// RecurrenceSpec captures the master event's RRULE plus EXDATEs. We
// stash the raw RRULE string and pass it to rrule-go in the expander.
type RecurrenceSpec struct {
	RRule   string
	ExDates []time.Time
}

// Parse reads an ICS payload and returns the slice of VEVENTs (one
// entry per BEGIN:VEVENT block — recurring masters and overrides are
// returned as separate entries).
func Parse(raw []byte) ([]Event, error) {
	cal, err := ics.ParseCalendar(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("parse ics: %w", err)
	}

	vevents := cal.Events()
	result := make([]Event, 0, len(vevents))
	for _, ev := range vevents {
		parsed, ok := mapVEvent(ev)
		if !ok {
			continue
		}
		result = append(result, parsed)
	}
	return result, nil
}

func mapVEvent(ev *ics.VEvent) (Event, bool) {
	uid := ""
	if p := ev.GetProperty(ics.ComponentPropertyUniqueId); p != nil {
		uid = strings.TrimSpace(p.Value)
	}
	if uid == "" {
		return Event{}, false
	}

	start, allDay, ok := readEventTime(ev, true)
	if !ok {
		return Event{}, false
	}
	end, _, _ := readEventTime(ev, false)

	out := Event{
		UID:     uid,
		Start:   start,
		End:     end,
		AllDay:  allDay,
		Summary: trimProp(ev, ics.ComponentPropertySummary),
	}

	if v := trimProp(ev, ics.ComponentPropertyDescription); v != "" {
		out.Description = v
	}
	if v := trimProp(ev, ics.ComponentPropertyLocation); v != "" {
		out.Location = v
	}
	if lm, err := ev.GetLastModifiedAt(); err == nil {
		out.LastModified = lm
	} else if c, err := ev.GetDtStampTime(); err == nil {
		out.LastModified = c
	}
	if seqProp := ev.GetProperty(ics.ComponentPropertySequence); seqProp != nil {
		if n, err := strconv.Atoi(strings.TrimSpace(seqProp.Value)); err == nil {
			out.Sequence = n
		}
	}
	if rid, err := ev.GetRecurrenceID(); err == nil && !rid.IsZero() {
		out.RecurrenceID = rid
	}

	rrules := ev.GetProperties(ics.ComponentPropertyRrule)
	if len(rrules) > 0 {
		rrule := strings.TrimSpace(rrules[0].Value)
		if rrule != "" {
			spec := &RecurrenceSpec{RRule: rrule}
			if exdates, err := ev.GetExDates(); err == nil && len(exdates) > 0 {
				spec.ExDates = exdates
			}
			out.Recurrence = spec
		}
	}

	return out, true
}

func trimProp(ev *ics.VEvent, prop ics.ComponentProperty) string {
	p := ev.GetProperty(prop)
	if p == nil {
		return ""
	}
	return strings.TrimSpace(p.Value)
}

// readEventTime returns the DTSTART (when start=true) or DTEND/DUE
// (when start=false). The second return value is true for all-day
// events. The third return is false when the property is absent or
// unparseable.
func readEventTime(ev *ics.VEvent, start bool) (time.Time, bool, bool) {
	var prop ics.ComponentProperty
	if start {
		prop = ics.ComponentPropertyDtStart
	} else {
		prop = ics.ComponentPropertyDtEnd
	}

	p := ev.GetProperty(prop)
	if p == nil {
		return time.Time{}, false, false
	}

	allDay := false
	if vt, ok := p.ICalParameters["VALUE"]; ok {
		for _, v := range vt {
			if strings.EqualFold(v, "DATE") {
				allDay = true
				break
			}
		}
	}

	if start {
		if allDay {
			t, err := ev.GetAllDayStartAt()
			if err == nil {
				return t, true, true
			}
		}
		t, err := ev.GetStartAt()
		if err == nil {
			return t, allDay, true
		}
		return time.Time{}, allDay, false
	}

	if allDay {
		t, err := ev.GetAllDayEndAt()
		if err == nil {
			return t, true, true
		}
	}
	t, err := ev.GetEndAt()
	if err == nil {
		return t, allDay, true
	}
	return time.Time{}, allDay, false
}
