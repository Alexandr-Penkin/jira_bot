package telegram

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"SleepJiraBot/internal/jira"
	"SleepJiraBot/internal/locale"
)

func sp(v float64) *float64 { return &v }

func makeIssue(key, typeName, statusName, statusCat string, storyPoints *float64) jira.Issue {
	issue := jira.Issue{Key: key}
	issue.Fields.Summary = key + " summary"
	if typeName != "" {
		issue.Fields.IssueType = &jira.IssueType{Name: typeName}
	}
	if statusName != "" {
		var sc *jira.StatusCategory
		if statusCat != "" {
			sc = &jira.StatusCategory{Key: statusCat}
		}
		issue.Fields.Status = &jira.Status{Name: statusName, StatusCategory: sc}
	}
	issue.Fields.StoryPoints = storyPoints
	return issue
}

func TestFormatSprintReport_Basic(t *testing.T) {
	issues := []jira.Issue{
		makeIssue("T-1", "Story", "Done", "done", sp(3)),
		makeIssue("T-2", "Story", "In Progress", "indeterminate", sp(5)),
		makeIssue("T-3", "Task", "To Do", "new", sp(2)),
	}

	result := formatSprintReport(locale.EN, "Sprint 1", "", issues, nil, false, nil, nil, nil, nil, nil)

	assert.Contains(t, result, "Sprint Report")
	assert.Contains(t, result, "Sprint 1")
	assert.Contains(t, result, "Total issues: *3*")
	assert.Contains(t, result, "Done: *1*")
	assert.Contains(t, result, "In Progress: *1*")
	assert.Contains(t, result, "To Do: *1*")
	assert.Contains(t, result, "Story Points:")
	assert.Contains(t, result, "3 / 10")
}

func TestFormatSprintReport_WithFilter(t *testing.T) {
	issues := []jira.Issue{
		makeIssue("T-1", "Story", "Done", "done", sp(3)),
		makeIssue("T-2", "Bug", "Done", "done", sp(1)),
		makeIssue("T-3", "Sub-task", "To Do", "new", sp(1)),
	}

	result := formatSprintReport(locale.EN, "S1", "", issues, []string{"Story", "Bug"}, false, nil, nil, nil, nil, nil)

	assert.Contains(t, result, "Filter: Story, Bug")
	assert.Contains(t, result, "Total issues: *2*")
	assert.Contains(t, result, "Done: *2*")
	assert.NotContains(t, result, "Sub-task")
}

func TestFormatSprintReport_SprintGoal(t *testing.T) {
	issues := []jira.Issue{
		makeIssue("T-1", "Story", "Done", "done", sp(1)),
	}

	result := formatSprintReport(locale.EN, "S1", "Ship the *new* feature", issues, nil, false, nil, nil, nil, nil, nil)
	assert.Contains(t, result, "\\*new\\*") // escaped markdown
}

func TestFormatSprintReport_BugRatio(t *testing.T) {
	issues := []jira.Issue{
		makeIssue("T-1", "Story", "Done", "done", sp(3)),
		makeIssue("T-2", "Bug", "Done", "done", sp(1)),
		makeIssue("T-3", "Bug", "To Do", "new", sp(1)),
	}

	result := formatSprintReport(locale.EN, "S1", "", issues, nil, false, nil, nil, nil, nil, nil)
	assert.Contains(t, result, "Bug ratio: *2*/3")
	assert.Contains(t, result, "(66%)")
}

func TestFormatSprintReport_Unestimated(t *testing.T) {
	issues := []jira.Issue{
		makeIssue("T-1", "Story", "Done", "done", sp(3)),
		makeIssue("T-2", "Story", "To Do", "new", nil),
		makeIssue("T-3", "Task", "To Do", "new", nil),
	}

	result := formatSprintReport(locale.EN, "S1", "", issues, nil, false, nil, nil, nil, nil, nil)
	assert.Contains(t, result, "Unestimated Issues:")
	assert.Contains(t, result, "T-2")
	assert.Contains(t, result, "T-3")
}

func TestFormatSprintReport_TimeSpentVsSP(t *testing.T) {
	issues := []jira.Issue{
		makeIssue("T-1", "Story", "Done", "done", sp(5)),
		makeIssue("T-2", "Story", "Done", "done", sp(3)),
		makeIssue("T-3", "Story", "In Progress", "indeterminate", sp(2)),
	}
	issues[0].Fields.TimeSpent = 4 * 3600  // 4h
	issues[1].Fields.TimeSpent = 12 * 3600 // 12h
	issues[2].Fields.TimeSpent = 2 * 3600  // 2h
	// Aggregate should win when larger.
	issues[0].Fields.AggregateTimeSpent = 6 * 3600 // 6h

	result := formatSprintReport(locale.EN, "S1", "", issues, nil, false, nil, nil, nil, nil, nil)

	// Total logged = 6 + 12 + 2 = 20h
	assert.Contains(t, result, "Logged:")
	assert.Contains(t, result, "20.0h")
	// Done SP = 8, done logged = 18h → 2.25h/SP ≈ 2.3h
	assert.Contains(t, result, "/ SP")
}

func TestFormatSprintReport_Overdue(t *testing.T) {
	today := time.Now().Truncate(24 * time.Hour)
	yesterday := today.AddDate(0, 0, -1).Format("2006-01-02")
	tomorrow := today.AddDate(0, 0, 1).Format("2006-01-02")

	notDone := makeIssue("T-1", "Story", "To Do", "new", sp(1))
	notDone.Fields.DueDate = yesterday

	doneOverdue := makeIssue("T-2", "Story", "Done", "done", sp(1))
	doneOverdue.Fields.DueDate = yesterday

	notDueSoon := makeIssue("T-3", "Story", "To Do", "new", sp(1))
	notDueSoon.Fields.DueDate = tomorrow

	issues := []jira.Issue{notDone, doneOverdue, notDueSoon}
	result := formatSprintReport(locale.EN, "S1", "", issues, nil, false, nil, nil, nil, nil, nil)

	assert.Contains(t, result, "Overdue Issues:")
	assert.Contains(t, result, "T-1")
	assert.NotContains(t, result, "T-2") // done, not overdue
}

func TestFormatSprintReport_PrioritySort(t *testing.T) {
	issues := []jira.Issue{
		makeIssue("T-1", "Story", "Done", "done", sp(1)),
		makeIssue("T-2", "Story", "Done", "done", sp(1)),
		makeIssue("T-3", "Story", "Done", "done", sp(1)),
	}
	issues[0].Fields.Priority = &jira.Priority{Name: "Low"}
	issues[1].Fields.Priority = &jira.Priority{Name: "High"}
	issues[2].Fields.Priority = &jira.Priority{Name: "Critical"}

	result := formatSprintReport(locale.EN, "S1", "", issues, nil, false, nil, nil, nil, nil, nil)

	critIdx := strings.Index(result, "Critical")
	highIdx := strings.Index(result, "High")
	lowIdx := strings.Index(result, "Low")

	assert.Greater(t, highIdx, critIdx, "Critical should appear before High")
	assert.Greater(t, lowIdx, highIdx, "High should appear before Low")
}

func TestFormatSprintReport_AssigneeSort(t *testing.T) {
	issues := []jira.Issue{
		makeIssue("T-1", "Story", "Done", "done", sp(1)),
		makeIssue("T-2", "Story", "Done", "done", sp(1)),
		makeIssue("T-3", "Story", "Done", "done", sp(1)),
		makeIssue("T-4", "Story", "To Do", "new", sp(1)),
	}
	issues[0].Fields.Assignee = &jira.JiraUser{DisplayName: "Alice"}
	issues[1].Fields.Assignee = &jira.JiraUser{DisplayName: "Bob"}
	issues[2].Fields.Assignee = &jira.JiraUser{DisplayName: "Bob"}
	issues[3].Fields.Assignee = &jira.JiraUser{DisplayName: "Alice"}

	result := formatSprintReport(locale.EN, "S1", "", issues, nil, false, nil, nil, nil, nil, nil)

	// Bob has 2 issues, Alice has 2 — tied, so alphabetical: Alice before Bob.
	aliceIdx := strings.Index(result, "Alice")
	bobIdx := strings.Index(result, "Bob")
	assert.Greater(t, bobIdx, aliceIdx)
}

func TestFormatSprintReport_ChangelogMetrics(t *testing.T) {
	issues := []jira.Issue{
		makeIssue("T-1", "Story", "Done", "done", sp(5)),
	}

	clm := &changelogMetrics{
		scopeCreepKeys: []string{"T-10", "T-11"},
		scopeCreepSP:   8,
		carryOverKeys:  []string{"T-20"},
		carryOverSP:    3,
		committedSP:    20,
		completedSP:    15,
		avgCycleHours:  48,
		cycleCount:     3,
		totalBlockedH:  24,
		avgBlockedH:    8,
		blockedCount:   3,
	}

	result := formatSprintReport(locale.EN, "S1", "", issues, nil, false, clm, nil, nil, nil, nil)

	assert.Contains(t, result, "Scope Creep")
	assert.Contains(t, result, "T-10")
	assert.Contains(t, result, "(8 SP)")
	assert.Contains(t, result, "Carry-over")
	assert.Contains(t, result, "T-20")
	assert.Contains(t, result, "Commitment")
	assert.Contains(t, result, "20 SP")
	assert.Contains(t, result, "15 SP")
	assert.Contains(t, result, "Cycle Time")
	assert.Contains(t, result, "2.0d")
	assert.Contains(t, result, "Blocked Time")
}

func TestFormatSprintReport_Velocity(t *testing.T) {
	issues := []jira.Issue{
		makeIssue("T-1", "Story", "Done", "done", sp(10)),
	}

	vel := &velocityData{
		currentSP: 34,
		history:   []float64{28, 30, 26},
		avgSP:     28,
		trend:     21,
	}

	result := formatSprintReport(locale.EN, "S1", "", issues, nil, false, nil, vel, nil, nil, nil)

	assert.Contains(t, result, "Velocity")
	assert.Contains(t, result, "34 SP")
	assert.Contains(t, result, "avg(3): 28 SP")
	assert.Contains(t, result, "+21%")
}

func TestFormatSprintReport_Forecast(t *testing.T) {
	issues := []jira.Issue{
		makeIssue("T-1", "Story", "Done", "done", sp(10)),
		makeIssue("T-2", "Story", "To Do", "new", sp(10)),
	}

	now := time.Now()
	fc := &forecastData{
		start: now.AddDate(0, 0, -7),
		end:   now.AddDate(0, 0, 7),
	}

	result := formatSprintReport(locale.EN, "S1", "", issues, nil, false, nil, nil, fc, nil, nil)

	assert.Contains(t, result, "Forecast")
	assert.Contains(t, result, "days left")
}

func TestComputeChangelogMetrics_ScopeCreep(t *testing.T) {
	sprintStart := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	issue := makeIssue("T-1", "Story", "To Do", "new", sp(5))
	issue.Changelog = &jira.Changelog{
		Histories: []jira.ChangeHistory{
			{
				Created: "2026-03-05T10:00:00.000+0000", // after sprint start
				Items: []jira.ChangeItem{
					{Field: "Sprint", FromString: "", ToString: "Sprint 10"},
				},
			},
		},
	}

	m := computeChangelogMetrics([]jira.Issue{issue}, "Sprint 10", sprintStart, nil, nil, nil)

	assert.Equal(t, []string{"T-1"}, m.scopeCreepKeys)
	assert.Equal(t, float64(5), m.scopeCreepSP)
}

func TestComputeChangelogMetrics_CarryOver(t *testing.T) {
	sprintStart := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	issue := makeIssue("T-1", "Story", "To Do", "new", sp(3))
	issue.Changelog = &jira.Changelog{
		Histories: []jira.ChangeHistory{
			{
				Created: "2026-02-28T10:00:00.000+0000", // before sprint start
				Items: []jira.ChangeItem{
					{Field: "Sprint", FromString: "Sprint 9", ToString: "Sprint 9, Sprint 10"},
				},
			},
		},
	}

	m := computeChangelogMetrics([]jira.Issue{issue}, "Sprint 10", sprintStart, nil, nil, nil)

	assert.Equal(t, []string{"T-1"}, m.carryOverKeys)
	assert.Equal(t, float64(3), m.carryOverSP)
}

func TestComputeChangelogMetrics_CycleTime(t *testing.T) {
	sprintStart := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	issue := makeIssue("T-1", "Story", "Done", "done", sp(5))
	issue.Changelog = &jira.Changelog{
		Histories: []jira.ChangeHistory{
			{
				Created: "2026-03-02T10:00:00.000+0000",
				Items:   []jira.ChangeItem{{Field: "status", FromString: "To Do", ToString: "In Progress"}},
			},
			{
				Created: "2026-03-04T10:00:00.000+0000",
				Items:   []jira.ChangeItem{{Field: "status", FromString: "In Progress", ToString: "Done"}},
			},
		},
	}

	m := computeChangelogMetrics([]jira.Issue{issue}, "Sprint 10", sprintStart, nil, nil, nil)

	assert.Equal(t, 1, m.cycleCount)
	assert.InDelta(t, 48, m.avgCycleHours, 1) // 2 days = 48 hours
}

func TestComputeChangelogMetrics_BlockedTime(t *testing.T) {
	sprintStart := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	issue := makeIssue("T-1", "Story", "In Progress", "indeterminate", sp(5))
	issue.Changelog = &jira.Changelog{
		Histories: []jira.ChangeHistory{
			{
				Created: "2026-03-02T10:00:00.000+0000",
				Items:   []jira.ChangeItem{{Field: "status", FromString: "In Progress", ToString: "Blocked"}},
			},
			{
				Created: "2026-03-03T10:00:00.000+0000",
				Items:   []jira.ChangeItem{{Field: "status", FromString: "Blocked", ToString: "In Progress"}},
			},
		},
	}

	m := computeChangelogMetrics([]jira.Issue{issue}, "Sprint 10", sprintStart, nil, nil, nil)

	assert.Equal(t, 1, m.blockedCount)
	assert.InDelta(t, 24, m.totalBlockedH, 1)
}

func TestSplitMessage_Short(t *testing.T) {
	parts := splitMessage("short text", 4000)
	assert.Len(t, parts, 1)
	assert.Equal(t, "short text", parts[0])
}

func TestSplitMessage_Long(t *testing.T) {
	section1 := strings.Repeat("a", 2000)
	section2 := strings.Repeat("b", 2000)
	section3 := strings.Repeat("c", 2000)
	text := section1 + "\n\n" + section2 + "\n\n" + section3

	parts := splitMessage(text, 4000)
	assert.Greater(t, len(parts), 1)
	for _, p := range parts {
		assert.LessOrEqual(t, len(p), 4002) // section boundary may add \n\n
	}
}

func TestParseJiraTime(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"with offset", "2026-03-15T10:30:00.000+0000"},
		{"with Z", "2026-03-15T10:30:00.000Z"},
		{"RFC3339", "2026-03-15T10:30:00Z"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseJiraTime(tt.input)
			assert.NoError(t, err)
			assert.Equal(t, 2026, result.Year())
			assert.Equal(t, time.March, result.Month())
			assert.Equal(t, 15, result.Day())
		})
	}

	_, err := parseJiraTime("invalid")
	assert.Error(t, err)
}

func TestFormatDuration(t *testing.T) {
	assert.Equal(t, "30m", formatDuration(0.5))
	assert.Equal(t, "2.0h", formatDuration(2))
	assert.Equal(t, "12.5h", formatDuration(12.5))
	assert.Equal(t, "2.0d", formatDuration(48))
	assert.Equal(t, "3.5d", formatDuration(84))
}

func TestPriorityOrder(t *testing.T) {
	assert.Less(t, priorityOrder("Critical"), priorityOrder("High"))
	assert.Less(t, priorityOrder("High"), priorityOrder("Medium"))
	assert.Less(t, priorityOrder("Medium"), priorityOrder("Low"))
	assert.Less(t, priorityOrder("Low"), priorityOrder("None"))
}
