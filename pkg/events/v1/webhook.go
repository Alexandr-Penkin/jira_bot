package eventsv1

import (
	"encoding/json"
	"strconv"
)

// WebhookReceived fires after HMAC verification (or verification skip when
// JIRA_WEBHOOK_SECRET is empty) and initial parse. Payload is the raw JSON
// Jira delivered so consumers are not bound to the current parsing logic.
type WebhookReceived struct {
	Source            string          `json:"source"`
	EventType         string          `json:"event_type"`
	JiraEventID       string          `json:"jira_event_id,omitempty"`
	ReceivedAt        int64           `json:"received_at"`
	SignatureVerified bool            `json:"signature_verified"`
	Payload           json.RawMessage `json:"payload"`
}

func (WebhookReceived) Subject() string { return SubjectWebhookReceived }

func (e WebhookReceived) IdempotencyKey() string {
	if e.JiraEventID != "" {
		return "webhook.received:" + e.JiraEventID
	}
	return "webhook.received:" + strconv.FormatInt(e.ReceivedAt, 10) + ":" + e.EventType
}

// WebhookAffected identifies one subscriber that should be notified about a
// normalized webhook event. The Telegram-side consumer formats and delivers
// the message; webhook-svc only determines who cares.
type WebhookAffected struct {
	TelegramID       int64  `json:"telegram_id"`
	ChatID           int64  `json:"chat_id"`
	SubscriptionID   string `json:"subscription_id"`
	SubscriptionType string `json:"subscription_type"`
}

// WebhookNormalized fires after webhook-svc has classified a Jira payload
// and resolved the list of affected subscribers. Payload carries the raw
// Jira JSON so downstream formatters (Telegram gateway) can extract fields
// without re-fetching from Jira.
type WebhookNormalized struct {
	EventType   string            `json:"event_type"`
	IssueKey    string            `json:"issue_key,omitempty"`
	ProjectKey  string            `json:"project_key,omitempty"`
	ChangeType  string            `json:"change_type"`
	Actor       string            `json:"actor,omitempty"`
	At          int64             `json:"at"`
	JiraEventID string            `json:"jira_event_id,omitempty"`
	Affected    []WebhookAffected `json:"affected"`
	Payload     json.RawMessage   `json:"payload,omitempty"`
}

func (*WebhookNormalized) Subject() string { return SubjectWebhookNormalized }

func (e *WebhookNormalized) IdempotencyKey() string {
	if e.JiraEventID != "" {
		return "webhook.normalized:" + e.JiraEventID
	}
	// Minute-bucket the timestamp so retries within a minute dedup without
	// suppressing legitimate repeated change types on the same issue.
	bucket := e.At - (e.At % 60)
	return "webhook.normalized:" + e.IssueKey + ":" + e.ChangeType + ":" + strconv.FormatInt(bucket, 10)
}
