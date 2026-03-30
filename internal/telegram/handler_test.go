package telegram

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"SleepJiraBot/internal/format"
	"SleepJiraBot/internal/jira"
	"SleepJiraBot/internal/locale"
)

func TestEscapeMarkdown(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty", "", ""},
		{"no special", "hello world", "hello world"},
		{"underscore", "snake_case", "snake\\_case"},
		{"asterisk", "*bold*", "\\*bold\\*"},
		{"backtick", "`code`", "\\`code\\`"},
		{"bracket", "[link]", "\\[link\\]"},
		{"all special", "_*`[]", "\\_\\*\\`\\[\\]"},
		{"mixed", "hello_world *test*", "hello\\_world \\*test\\*"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, format.EscapeMarkdown(tt.input))
		})
	}
}

func TestFormatIssue_Full(t *testing.T) {
	issue := &jira.Issue{
		Key: "TEST-42",
		Fields: jira.IssueFields{
			Summary:   "Test Summary",
			Status:    &jira.Status{Name: "In Progress"},
			Priority:  &jira.Priority{Name: "High"},
			Assignee:  &jira.JiraUser{DisplayName: "Alice"},
			Reporter:  &jira.JiraUser{DisplayName: "Bob"},
			IssueType: &jira.IssueType{Name: "Bug"},
			DueDate:   "2026-04-01",
			Labels:    []string{"backend", "urgent"},
			Description: &jira.ADFDocument{
				Type:    "doc",
				Version: 1,
				Content: []jira.ADFNode{
					{
						Type: "paragraph",
						Content: []jira.ADFNode{
							{Type: "text", Text: "This is the description."},
						},
					},
				},
			},
		},
	}

	result := formatIssue(locale.EN, issue, "https://mysite.atlassian.net")

	assert.Contains(t, result, "TEST-42")
	assert.Contains(t, result, "https://mysite.atlassian.net/browse/TEST-42")
	assert.Contains(t, result, "Test Summary")
	assert.Contains(t, result, "In Progress")
	assert.Contains(t, result, "High")
	assert.Contains(t, result, "Alice")
	assert.Contains(t, result, "Bob")
	assert.Contains(t, result, "Bug")
	assert.Contains(t, result, "2026-04-01")
	assert.Contains(t, result, "backend, urgent")
	assert.Contains(t, result, "This is the description.")
}

func TestFormatIssue_Minimal(t *testing.T) {
	issue := &jira.Issue{
		Key: "MIN-1",
		Fields: jira.IssueFields{
			Summary: "Minimal issue",
		},
	}

	result := formatIssue(locale.EN, issue, "https://site.atlassian.net")

	assert.Contains(t, result, "MIN-1")
	assert.Contains(t, result, "Minimal issue")
	assert.Contains(t, result, "—") // default status
	assert.Contains(t, result, "Unassigned")
	assert.NotContains(t, result, "Due:")
	assert.NotContains(t, result, "Labels:")
}

func TestFormatIssue_LongDescription(t *testing.T) {
	longText := ""
	for i := 0; i < 400; i++ {
		longText += "x"
	}

	issue := &jira.Issue{
		Key: "LONG-1",
		Fields: jira.IssueFields{
			Summary: "Long desc issue",
			Description: &jira.ADFDocument{
				Type:    "doc",
				Version: 1,
				Content: []jira.ADFNode{
					{
						Type: "paragraph",
						Content: []jira.ADFNode{
							{Type: "text", Text: longText},
						},
					},
				},
			},
		},
	}

	result := formatIssue(locale.EN, issue, "https://site.atlassian.net")
	assert.Contains(t, result, "...")
}

func TestFormatIssue_NilDescription(t *testing.T) {
	issue := &jira.Issue{
		Key: "NIL-1",
		Fields: jira.IssueFields{
			Summary:     "No description",
			Description: nil,
		},
	}

	result := formatIssue(locale.EN, issue, "https://site.atlassian.net")
	assert.NotContains(t, result, "_\n") // no italic description block
}

func TestFormatIssue_SpecialCharsEscaped(t *testing.T) {
	issue := &jira.Issue{
		Key: "ESC-1",
		Fields: jira.IssueFields{
			Summary:  "Fix *critical* _bug_",
			Status:   &jira.Status{Name: "To_Do"},
			Assignee: &jira.JiraUser{DisplayName: "User_Name"},
		},
	}

	result := formatIssue(locale.EN, issue, "https://site.atlassian.net")
	assert.Contains(t, result, "\\*critical\\*")
	assert.Contains(t, result, "\\_bug\\_")
	assert.Contains(t, result, "To\\_Do")
	assert.Contains(t, result, "User\\_Name")
}

func TestValidateIssueKey(t *testing.T) {
	assert.True(t, validateIssueKey("PROJ-123"))
	assert.True(t, validateIssueKey("AB-1"))
	assert.False(t, validateIssueKey("proj-123"))
	assert.False(t, validateIssueKey(""))
	assert.False(t, validateIssueKey("PROJ"))
	assert.False(t, validateIssueKey("PROJ-"))

	longKey := "ABCDEFGHIJKLMNOPQRSTUVWXYZ1234567-1"
	assert.False(t, validateIssueKey(longKey))
}

func TestValidateProjectKey(t *testing.T) {
	assert.True(t, validateProjectKey("PROJ"))
	assert.True(t, validateProjectKey("A"))
	assert.True(t, validateProjectKey("AB123"))
	assert.False(t, validateProjectKey(""))
	assert.False(t, validateProjectKey("proj"))
	assert.False(t, validateProjectKey("PR-OJ"))
	assert.False(t, validateProjectKey("ABCDEFGHIJKLMNOPQRSTUVWXYZ"))
}

func TestValidateCronExpression(t *testing.T) {
	assert.NoError(t, validateCronExpression("0 9 * * 1-5"))
	assert.NoError(t, validateCronExpression("0 18 * * 5"))

	assert.Error(t, validateCronExpression("invalid"))

	err := validateCronExpression("* * * * *")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "too frequent")
}

func TestGenerateState(t *testing.T) {
	state1, err := generateState()
	assert.NoError(t, err)
	assert.Len(t, state1, 32) // 16 bytes -> 32 hex chars

	state2, err := generateState()
	assert.NoError(t, err)
	assert.Len(t, state2, 32)

	assert.NotEqual(t, state1, state2)
}
