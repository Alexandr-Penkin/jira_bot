package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
)

func TestVerifySignature_Valid(t *testing.T) {
	secret := "my-secret"
	h := &Handler{webhookSecret: secret, log: zerolog.Nop()}

	body := []byte(`{"test": true}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	assert.True(t, h.verifySignature(body, signature))
}

func TestVerifySignature_Invalid(t *testing.T) {
	h := &Handler{webhookSecret: "my-secret", log: zerolog.Nop()}

	body := []byte(`{"test": true}`)
	assert.False(t, h.verifySignature(body, "sha256=invalid"))
}

func TestVerifySignature_EmptySignature(t *testing.T) {
	h := &Handler{webhookSecret: "my-secret", log: zerolog.Nop()}
	assert.False(t, h.verifySignature([]byte("body"), ""))
}

func TestVerifySignature_WithoutPrefix_Rejected(t *testing.T) {
	secret := "my-secret"
	h := &Handler{webhookSecret: secret, log: zerolog.Nop()}

	body := []byte(`test`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	signature := hex.EncodeToString(mac.Sum(nil))

	// Signatures without the "sha256=" prefix must be rejected.
	assert.False(t, h.verifySignature(body, signature))
}

func TestServeHTTP_SignatureVerification_Rejected(t *testing.T) {
	h := &Handler{
		webhookSecret: "secret",
		log:           zerolog.Nop(),
		sem:           make(chan struct{}, maxConcurrentJobs),
		eventQueue:    make(chan Event, eventQueueSize),
	}

	body := `{"webhookEvent": "jira:issue_created"}`
	req := httptest.NewRequest(http.MethodPost, "/webhook/jira", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature", "sha256=wrong")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestServeHTTP_NoSignature_Rejected(t *testing.T) {
	secret := "test-secret"
	h := &Handler{
		webhookSecret: secret,
		log:           zerolog.Nop(),
		sem:           make(chan struct{}, maxConcurrentJobs),
		eventQueue:    make(chan Event, eventQueueSize),
	}

	event := Event{WebhookEvent: EventIssueCreated}
	body, _ := json.Marshal(event)
	req := httptest.NewRequest(http.MethodPost, "/webhook/jira", strings.NewReader(string(body)))
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestServeHTTP_ValidJSON_Accepted(t *testing.T) {
	secret := "test-secret"
	h := &Handler{
		webhookSecret: secret,
		log:           zerolog.Nop(),
		sem:           make(chan struct{}, maxConcurrentJobs),
		eventQueue:    make(chan Event, eventQueueSize),
	}

	event := Event{
		WebhookEvent: EventIssueCreated,
		Issue: &Issue{
			Key:    "PROJ-1",
			Fields: IssueFields{Summary: "Test"},
		},
	}
	body, _ := json.Marshal(event)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, "/webhook/jira", strings.NewReader(string(body)))
	req.Header.Set("X-Hub-Signature", signature)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}
