package webhook

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeEventType(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{EventIssueCreated, "issue_created"},
		{EventIssueUpdated, "issue_updated"},
		{EventIssueDeleted, "issue_deleted"},
		{EventCommentCreated, "comment_created"},
		{EventCommentUpdated, "comment_updated"},
		{"unknown_event", "unknown_event"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, NormalizeEventType(tt.input))
		})
	}
}

func TestAllEventTypes(t *testing.T) {
	types := AllEventTypes()
	assert.Len(t, types, 5)
	assert.Contains(t, types, "issue_created")
	assert.Contains(t, types, "issue_updated")
	assert.Contains(t, types, "issue_deleted")
	assert.Contains(t, types, "comment_created")
	assert.Contains(t, types, "comment_updated")
}

func TestEventConstants(t *testing.T) {
	assert.Equal(t, "jira:issue_created", EventIssueCreated)
	assert.Equal(t, "jira:issue_updated", EventIssueUpdated)
	assert.Equal(t, "jira:issue_deleted", EventIssueDeleted)
	assert.Equal(t, "comment_created", EventCommentCreated)
	assert.Equal(t, "comment_updated", EventCommentUpdated)
}
