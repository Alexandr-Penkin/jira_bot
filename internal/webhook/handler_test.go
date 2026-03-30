package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"

	"SleepJiraBot/internal/format"
)

func TestEscapeMarkdown(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{"hello_world", "hello\\_world"},
		{"*bold*", "\\*bold\\*"},
		{"`code`", "\\`code\\`"},
		{"[link]", "\\[link\\]"},
		{"_*`[]", "\\_\\*\\`\\[\\]"},
		{"", ""},
		{"no special chars", "no special chars"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, format.EscapeMarkdown(tt.input))
		})
	}
}

func TestFormatNotification_NilIssue(t *testing.T) {
	h := &Handler{log: zerolog.Nop()}
	event := Event{WebhookEvent: EventIssueCreated}
	result := h.formatNotification(event, "issue_created")
	assert.Equal(t, "Jira event: issue_created", result)
}

func TestFormatNotification_BasicFormat(t *testing.T) {
	h := &Handler{log: zerolog.Nop()}
	event := Event{
		WebhookEvent: EventIssueCreated,
		Issue: &Issue{
			Key: "PROJ-123",
			Fields: IssueFields{
				Summary: "Test issue",
			},
		},
	}

	result := h.formatNotification(event, "issue_created")
	assert.Contains(t, result, "PROJ-123")
	assert.Contains(t, result, "Test issue")
	assert.Contains(t, result, "🆕")
}

func TestFormatNotification_DetailedFormat(t *testing.T) {
	h := &Handler{log: zerolog.Nop()}
	event := Event{
		WebhookEvent: EventIssueUpdated,
		Issue: &Issue{
			Key: "PROJ-456",
			Fields: IssueFields{
				Summary:  "Updated issue",
				Status:   &NameObj{Name: "In Progress"},
				Assignee: &User{DisplayName: "John Doe"},
			},
		},
		User: &User{DisplayName: "Jane Smith"},
		Changelog: &Changelog{
			Items: []ChangelogItem{
				{Field: "status", FromString: "Open", ToString: "In Progress"},
			},
		},
	}

	result := h.formatNotification(event, "issue_updated")
	assert.Contains(t, result, "PROJ-456")
	assert.Contains(t, result, "Updated issue")
	assert.Contains(t, result, "✏️")
	assert.Contains(t, result, "Jane Smith")
	assert.Contains(t, result, "In Progress")
	assert.Contains(t, result, "John Doe")
	assert.Contains(t, result, "status")
	assert.Contains(t, result, "Open")
}

func TestFormatNotification_CommentEvent(t *testing.T) {
	h := &Handler{log: zerolog.Nop()}
	event := Event{
		WebhookEvent: EventCommentCreated,
		Issue: &Issue{
			Key: "PROJ-789",
			Fields: IssueFields{
				Summary: "Commented issue",
			},
		},
		Comment: &Comment{
			Body:   "A comment",
			Author: &User{DisplayName: "Commenter"},
		},
	}

	result := h.formatNotification(event, "comment_created")
	assert.Contains(t, result, "💬")
	assert.Contains(t, result, "Commenter")
}

func TestFormatNotification_DeletedEvent(t *testing.T) {
	h := &Handler{log: zerolog.Nop()}
	event := Event{
		WebhookEvent: EventIssueDeleted,
		Issue: &Issue{
			Key:    "PROJ-999",
			Fields: IssueFields{Summary: "Deleted"},
		},
	}

	result := h.formatNotification(event, "issue_deleted")
	assert.Contains(t, result, "🗑")
}

func TestFormatNotification_UnknownEventEmoji(t *testing.T) {
	h := &Handler{log: zerolog.Nop()}
	event := Event{
		Issue: &Issue{
			Key:    "PROJ-1",
			Fields: IssueFields{Summary: "Something"},
		},
	}

	result := h.formatNotification(event, "unknown_type")
	assert.Contains(t, result, "📋")
}

const testWebhookSecret = "test-secret"

func newTestHandler() *Handler {
	return &Handler{
		webhookSecret: testWebhookSecret,
		log:           zerolog.Nop(),
		sem:           make(chan struct{}, maxConcurrentJobs),
		eventQueue:    make(chan Event, eventQueueSize),
	}
}

func signBody(body []byte) string {
	mac := hmac.New(sha256.New, []byte(testWebhookSecret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestServeHTTP_MethodNotAllowed(t *testing.T) {
	h := newTestHandler()

	req := httptest.NewRequest(http.MethodGet, "/webhook/jira", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestServeHTTP_InvalidJSON(t *testing.T) {
	h := newTestHandler()

	body := []byte("not json")
	req := httptest.NewRequest(http.MethodPost, "/webhook/jira", strings.NewReader(string(body)))
	req.Header.Set("X-Hub-Signature", signBody(body))
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestServeHTTP_EmptyBody(t *testing.T) {
	h := newTestHandler()

	body := []byte("")
	req := httptest.NewRequest(http.MethodPost, "/webhook/jira", strings.NewReader(string(body)))
	req.Header.Set("X-Hub-Signature", signBody(body))
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestServeHTTP_ReadError(t *testing.T) {
	h := newTestHandler()

	req := httptest.NewRequest(http.MethodPost, "/webhook/jira", &errorReader{})
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

type errorReader struct{}

func (e *errorReader) Read(_ []byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}
