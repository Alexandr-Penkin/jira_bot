package calendarpoller

import (
	"time"

	"SleepJiraBot/internal/calendar"
)

// PackageParser adapts the package-level calendar.Parse to the
// Parser interface so the poller's seam stays test-friendly without
// turning the parser into an instance type.
type PackageParser struct{}

// Parse delegates to calendar.Parse.
func (PackageParser) Parse(raw []byte) ([]calendar.Event, error) {
	return calendar.Parse(raw)
}

// PackageExpander adapts the package-level Expand/ApplyOverrides.
type PackageExpander struct{}

// Expand delegates to calendar.Expand.
func (PackageExpander) Expand(ev calendar.Event, from, to time.Time) ([]calendar.Instance, error) {
	return calendar.Expand(ev, from, to)
}

// ApplyOverrides delegates to calendar.ApplyOverrides.
func (PackageExpander) ApplyOverrides(instances []calendar.Instance, overrides []calendar.Event) []calendar.Instance {
	return calendar.ApplyOverrides(instances, overrides)
}
