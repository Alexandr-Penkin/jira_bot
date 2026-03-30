package scheduler

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"SleepJiraBot/internal/format"
	"SleepJiraBot/internal/jira"
	"SleepJiraBot/internal/locale"
	"SleepJiraBot/internal/storage"
)

func TestEscapeMarkdown(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{"snake_case", "snake\\_case"},
		{"*bold*", "\\*bold\\*"},
		{"`code`", "\\`code\\`"},
		{"[link]", "\\[link\\]"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, format.EscapeMarkdown(tt.input))
		})
	}
}

func TestFormatReport_NoIssues(t *testing.T) {
	s := &Scheduler{}

	result := s.formatReport(locale.EN,
		&storage.ScheduledReport{ReportName: "Daily Report"},
		&jira.SearchResult{Total: 0, Issues: nil},
	)

	assert.Contains(t, result, "Daily Report")
	assert.Contains(t, result, "Found: 0 issues")
	assert.Contains(t, result, "No issues found")
}

func TestFormatReport_WithIssues(t *testing.T) {
	s := &Scheduler{}

	result := s.formatReport(locale.EN,
		&storage.ScheduledReport{ReportName: "Sprint Report"},
		&jira.SearchResult{
			Total: 2,
			Issues: []jira.Issue{
				{
					Key: "PROJ-1",
					Fields: jira.IssueFields{
						Summary:  "First issue",
						Status:   &jira.Status{Name: "Open"},
						Priority: &jira.Priority{Name: "High"},
					},
				},
				{
					Key: "PROJ-2",
					Fields: jira.IssueFields{
						Summary: "Second issue",
						Status:  &jira.Status{Name: "Done"},
					},
				},
			},
		},
	)

	assert.Contains(t, result, "Sprint Report")
	assert.Contains(t, result, "Found: 2 issues")
	assert.Contains(t, result, "PROJ-1")
	assert.Contains(t, result, "First issue")
	assert.Contains(t, result, "Open")
	assert.Contains(t, result, "High")
	assert.Contains(t, result, "PROJ-2")
	assert.Contains(t, result, "Second issue")
	assert.Contains(t, result, "Done")
}

func TestFormatReport_MoreIssuesThanShown(t *testing.T) {
	s := &Scheduler{}

	result := s.formatReport(locale.EN,
		&storage.ScheduledReport{ReportName: "Big Report"},
		&jira.SearchResult{
			Total: 50,
			Issues: []jira.Issue{
				{
					Key: "PROJ-1",
					Fields: jira.IssueFields{
						Summary: "Issue",
						Status:  &jira.Status{Name: "Open"},
					},
				},
			},
		},
	)

	assert.Contains(t, result, "Found: 50 issues")
	assert.Contains(t, result, "and 49 more")
}

func TestFormatReport_EmptyName(t *testing.T) {
	s := &Scheduler{}

	result := s.formatReport(locale.EN,
		&storage.ScheduledReport{},
		&jira.SearchResult{Total: 0, Issues: nil},
	)

	assert.Contains(t, result, "Scheduled Report")
}

func TestFormatReport_NilStatus(t *testing.T) {
	s := &Scheduler{}

	result := s.formatReport(locale.EN,
		&storage.ScheduledReport{ReportName: "Report"},
		&jira.SearchResult{
			Total: 1,
			Issues: []jira.Issue{
				{
					Key: "PROJ-1",
					Fields: jira.IssueFields{
						Summary: "No status",
					},
				},
			},
		},
	)

	assert.Contains(t, result, "?")
}
