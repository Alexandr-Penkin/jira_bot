package telegram

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"SleepJiraBot/internal/jira"
	"SleepJiraBot/internal/locale"
)

func ts(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, s)
	assert.NoError(t, err)
	return parsed
}

// statusChange builds a single-item changelog history entry for a status transition.
func statusChange(created, from, to string) jira.ChangeHistory {
	return jira.ChangeHistory{
		Created: created,
		Items: []jira.ChangeItem{
			{Field: "status", FromString: from, ToString: to},
		},
	}
}

func makeKanbanIssue(key, typeName, statusName, statusCat, created string, histories ...jira.ChangeHistory) jira.Issue {
	issue := makeIssue(key, typeName, statusName, statusCat, nil)
	issue.Fields.Created = created
	if len(histories) > 0 {
		issue.Changelog = &jira.Changelog{Histories: histories}
	}
	return issue
}

func TestComputeKanbanMetrics_CycleAndLeadTime(t *testing.T) {
	now := ts(t, "2026-06-10T00:00:00Z")
	periodStart := now.AddDate(0, 0, -30)

	done := []jira.Issue{
		makeKanbanIssue("T-1", "Story", "Done", "done", "2026-05-20T00:00:00Z",
			statusChange("2026-05-22T00:00:00Z", "To Do", "In Progress"),
			statusChange("2026-05-24T00:00:00Z", "In Progress", "Done"),
		),
	}

	m := computeKanbanMetrics(done, nil, periodStart, now, nil, nil, nil, false)

	assert.Equal(t, 1, m.throughput)
	assert.Len(t, m.cycleHours, 1)
	assert.InDelta(t, 48, m.cycleHours[0], 0.01) // 2 days in progress
	assert.Len(t, m.leadHours, 1)
	assert.InDelta(t, 96, m.leadHours[0], 0.01) // 4 days from created to done
}

func TestComputeKanbanMetrics_DoneOutsidePeriodSkipped(t *testing.T) {
	now := ts(t, "2026-06-10T00:00:00Z")
	periodStart := now.AddDate(0, 0, -7)

	done := []jira.Issue{
		// Completed before the period, updated recently (e.g. comment added).
		makeKanbanIssue("T-1", "Story", "Done", "done", "2026-04-01T00:00:00Z",
			statusChange("2026-04-10T00:00:00Z", "In Progress", "Done"),
		),
		makeKanbanIssue("T-2", "Story", "Done", "done", "2026-06-01T00:00:00Z",
			statusChange("2026-06-05T00:00:00Z", "In Progress", "Done"),
		),
	}

	m := computeKanbanMetrics(done, nil, periodStart, now, nil, nil, nil, false)

	assert.Equal(t, 1, m.throughput)
}

func TestComputeKanbanMetrics_DoneTimeFallbackToUpdated(t *testing.T) {
	now := ts(t, "2026-06-10T00:00:00Z")
	periodStart := now.AddDate(0, 0, -30)

	issue := makeKanbanIssue("T-1", "Story", "Done", "done", "2026-05-20T00:00:00Z")
	issue.Fields.Updated = "2026-06-01T00:00:00Z"

	m := computeKanbanMetrics([]jira.Issue{issue}, nil, periodStart, now, nil, nil, nil, false)

	assert.Equal(t, 1, m.throughput)
	assert.Empty(t, m.cycleHours) // no in-progress transition recorded
}

func TestComputeKanbanMetrics_WeeklyThroughputBuckets(t *testing.T) {
	now := ts(t, "2026-06-29T00:00:00Z")
	periodStart := now.AddDate(0, 0, -14)

	done := []jira.Issue{
		makeKanbanIssue("T-1", "Story", "Done", "done", "2026-06-01T00:00:00Z",
			statusChange("2026-06-16T00:00:00Z", "In Progress", "Done"), // week 0
		),
		makeKanbanIssue("T-2", "Story", "Done", "done", "2026-06-01T00:00:00Z",
			statusChange("2026-06-25T00:00:00Z", "In Progress", "Done"), // week 1
		),
		makeKanbanIssue("T-3", "Story", "Done", "done", "2026-06-01T00:00:00Z",
			statusChange("2026-06-26T00:00:00Z", "In Progress", "Done"), // week 1
		),
	}

	m := computeKanbanMetrics(done, nil, periodStart, now, nil, nil, nil, false)

	assert.Equal(t, 3, m.throughput)
	assert.Equal(t, []int{1, 2}, m.weekly)
}

func TestComputeKanbanMetrics_TypeFilter(t *testing.T) {
	now := ts(t, "2026-06-10T00:00:00Z")
	periodStart := now.AddDate(0, 0, -30)

	done := []jira.Issue{
		makeKanbanIssue("T-1", "Story", "Done", "done", "2026-05-20T00:00:00Z",
			statusChange("2026-06-01T00:00:00Z", "In Progress", "Done"),
		),
		makeKanbanIssue("T-2", "Sub-task", "Done", "done", "2026-05-20T00:00:00Z",
			statusChange("2026-06-01T00:00:00Z", "In Progress", "Done"),
		),
	}

	m := computeKanbanMetrics(done, nil, periodStart, now, map[string]bool{"Story": true}, nil, nil, false)

	assert.Equal(t, 1, m.throughput)
	assert.Contains(t, m.byType, "Story")
	assert.NotContains(t, m.byType, "Sub-task")
}

func TestComputeKanbanMetrics_WIPAndItemAge(t *testing.T) {
	now := ts(t, "2026-06-10T00:00:00Z")
	periodStart := now.AddDate(0, 0, -30)

	wip := []jira.Issue{
		makeKanbanIssue("T-1", "Story", "In Progress", "indeterminate", "2026-06-01T00:00:00Z",
			statusChange("2026-06-04T00:00:00Z", "To Do", "In Progress"),
		),
		// No changelog: age falls back to created.
		makeKanbanIssue("T-2", "Story", "Review", "indeterminate", "2026-06-08T00:00:00Z"),
	}

	m := computeKanbanMetrics(nil, wip, periodStart, now, nil, nil, nil, false)

	assert.Equal(t, 2, m.wipTotal)
	assert.Equal(t, 1, m.wipByStatus["In Progress"])
	assert.Equal(t, 1, m.wipByStatus["Review"])
	assert.Len(t, m.ageHours, 2)
	assert.Len(t, m.oldest, 2)
	assert.Equal(t, "T-1", m.oldest[0].key) // 6 days in progress > 2 days since created
	assert.InDelta(t, 144, m.oldest[0].ageH, 0.01)
	assert.InDelta(t, 48, m.oldest[1].ageH, 0.01)
}

func TestComputeKanbanMetrics_BlockedTimeAndFlowEfficiency(t *testing.T) {
	now := ts(t, "2026-06-10T00:00:00Z")
	periodStart := now.AddDate(0, 0, -30)

	done := []jira.Issue{
		makeKanbanIssue("T-1", "Story", "Done", "done", "2026-05-20T00:00:00Z",
			statusChange("2026-05-22T00:00:00Z", "To Do", "In Progress"),
			statusChange("2026-05-23T00:00:00Z", "In Progress", "Blocked"),
			statusChange("2026-05-24T00:00:00Z", "Blocked", "In Progress"),
			statusChange("2026-05-26T00:00:00Z", "In Progress", "Done"),
		),
	}

	m := computeKanbanMetrics(done, nil, periodStart, now, nil, nil, nil, false)

	assert.Equal(t, 1, m.blockedCount)
	assert.InDelta(t, 24, m.totalBlockedH, 0.01)
	// cycle = 96h, blocked = 24h -> flow efficiency 75%.
	assert.Equal(t, 75, m.flowEffPct)
}

func TestComputeKanbanMetrics_FlowEfficiencyUnknown(t *testing.T) {
	now := ts(t, "2026-06-10T00:00:00Z")
	periodStart := now.AddDate(0, 0, -30)

	m := computeKanbanMetrics(nil, nil, periodStart, now, nil, nil, nil, false)

	assert.Equal(t, -1, m.flowEffPct)
}

func TestPercentile(t *testing.T) {
	assert.Equal(t, float64(0), percentile(nil, 50))
	assert.Equal(t, float64(3), percentile([]float64{5, 1, 3}, 50))
	assert.Equal(t, float64(2), percentile([]float64{4, 3, 2, 1}, 50))
	assert.Equal(t, float64(9), percentile([]float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, 85))
	assert.Equal(t, float64(10), percentile([]float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, 100))
}

func TestKanbanDoneJQL(t *testing.T) {
	assert.Equal(t,
		"project = PROJ AND statusCategory = Done AND updated >= -30d ORDER BY updated DESC",
		kanbanDoneJQL("PROJ", 30, nil))

	assert.Equal(t,
		`project = PROJ AND status in ("Done", "Released \"v2\"") AND updated >= -14d ORDER BY updated DESC`,
		kanbanDoneJQL("PROJ", 14, []string{"Done", `Released "v2"`}))
}

func TestKanbanWIPJQL(t *testing.T) {
	assert.Equal(t,
		"project = PROJ AND statusCategory = indeterminate ORDER BY created ASC",
		kanbanWIPJQL("PROJ", nil))

	assert.Equal(t,
		`project = PROJ AND statusCategory = indeterminate AND status not in ("Done") ORDER BY created ASC`,
		kanbanWIPJQL("PROJ", []string{"Done"}))
}

func TestFormatKanbanReport_Basic(t *testing.T) {
	now := ts(t, "2026-06-10T00:00:00Z")
	periodStart := now.AddDate(0, 0, -14)

	done := []jira.Issue{
		makeKanbanIssue("T-1", "Story", "Done", "done", "2026-06-01T00:00:00Z",
			statusChange("2026-06-02T00:00:00Z", "To Do", "In Progress"),
			statusChange("2026-06-04T00:00:00Z", "In Progress", "Done"),
		),
	}
	done[0].Fields.Assignee = &jira.JiraUser{DisplayName: "Alice"}
	wip := []jira.Issue{
		makeKanbanIssue("T-2", "Bug", "In Progress", "indeterminate", "2026-06-08T00:00:00Z"),
	}

	m := computeKanbanMetrics(done, wip, periodStart, now, nil, nil, nil, false)
	result := formatKanbanReport(locale.EN, "PROJ", 14, nil, m)

	assert.Contains(t, result, "Kanban Report")
	assert.Contains(t, result, "PROJ")
	assert.Contains(t, result, "last 14 days")
	assert.Contains(t, result, "Throughput")
	assert.Contains(t, result, "1 issues")
	assert.Contains(t, result, "Cycle Time")
	assert.Contains(t, result, "Lead Time")
	assert.Contains(t, result, "Work in Progress")
	assert.Contains(t, result, "In Progress: 1")
	assert.Contains(t, result, "Work Item Age")
	assert.Contains(t, result, "By Issue Type")
	assert.Contains(t, result, "Story: 1")
	assert.Contains(t, result, "By Assignee")
	assert.Contains(t, result, "Alice: 1")
}

func TestFormatKanbanReport_Unassigned(t *testing.T) {
	now := ts(t, "2026-06-10T00:00:00Z")
	periodStart := now.AddDate(0, 0, -7)

	done := []jira.Issue{
		makeKanbanIssue("T-1", "Story", "Done", "done", "2026-06-05T00:00:00Z",
			statusChange("2026-06-08T00:00:00Z", "In Progress", "Done"),
		),
	}

	m := computeKanbanMetrics(done, nil, periodStart, now, nil, nil, nil, false)
	result := formatKanbanReport(locale.EN, "PROJ", 7, nil, m)

	assert.Contains(t, result, "Unassigned: 1")
}
