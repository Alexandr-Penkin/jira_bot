package eventsv1

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNoopPublisher_DoesNothing(t *testing.T) {
	var p Publisher = NoopPublisher{}
	assert.NoError(t, p.Publish(context.Background(), UserAuthenticated{TelegramID: 1}, ""))
	assert.NoError(t, p.Close())
}

func TestSubjects_AllIdentitySubjects(t *testing.T) {
	assert.Equal(t, SubjectUserAuthenticated, UserAuthenticated{}.Subject())
	assert.Equal(t, SubjectTokensRefreshed, TokensRefreshed{}.Subject())
	assert.Equal(t, SubjectUserDeauthorized, UserDeauthorized{}.Subject())
}

func TestSubjects_AllSubscriptionSubjects(t *testing.T) {
	assert.Equal(t, SubjectSubscriptionCreated, (&SubscriptionCreated{}).Subject())
	assert.Equal(t, SubjectSubscriptionDeleted, SubscriptionDeleted{}.Subject())
	assert.Equal(t, SubjectChangeDetected, ChangeDetected{}.Subject())
}

func TestSubjects_WebhookAndSchedule(t *testing.T) {
	assert.Equal(t, SubjectWebhookReceived, WebhookReceived{}.Subject())
	assert.Equal(t, SubjectWebhookNormalized, (&WebhookNormalized{}).Subject())
	assert.Equal(t, SubjectScheduleDue, ScheduleDue{}.Subject())
}

func TestTokensRefreshed_IdempotencyKey(t *testing.T) {
	a := TokensRefreshed{TelegramID: 7, RefreshedAt: 100}
	b := TokensRefreshed{TelegramID: 7, RefreshedAt: 100}
	c := TokensRefreshed{TelegramID: 7, RefreshedAt: 101}

	assert.Equal(t, a.IdempotencyKey(), b.IdempotencyKey())
	assert.NotEqual(t, a.IdempotencyKey(), c.IdempotencyKey())
	assert.Contains(t, a.IdempotencyKey(), "identity.tokens.refreshed:")
}

func TestUserDeauthorized_IdempotencyKey(t *testing.T) {
	a := UserDeauthorized{TelegramID: 7, At: 100, Reason: "revoked"}
	assert.Contains(t, a.IdempotencyKey(), "identity.user.deauthorized:")
}

func TestSubscriptionCreated_IdempotencyKey_UsesID(t *testing.T) {
	// Subscription IDs are already unique per row (Mongo ObjectID) so the
	// timestamp is intentionally omitted — re-published events for the same
	// ID must dedupe at the broker.
	a := SubscriptionCreated{SubscriptionID: "s1", At: 10}
	b := SubscriptionCreated{SubscriptionID: "s1", At: 999}
	assert.Equal(t, a.IdempotencyKey(), b.IdempotencyKey())
}

func TestSubscriptionDeleted_IdempotencyKey_IncludesTimestamp(t *testing.T) {
	// Deletions are tied to a timestamp so a future re-delete (same ID) still
	// flows through — unlike create, which is unique per row.
	a := SubscriptionDeleted{SubscriptionID: "s1", At: 10}
	b := SubscriptionDeleted{SubscriptionID: "s1", At: 11}
	assert.NotEqual(t, a.IdempotencyKey(), b.IdempotencyKey())
}

func TestScheduleDue_IdempotencyKey(t *testing.T) {
	a := ScheduleDue{ReportID: "r1", FiredAt: 100}
	b := ScheduleDue{ReportID: "r1", FiredAt: 200}
	assert.Equal(t, a.IdempotencyKey(), ScheduleDue{ReportID: "r1", FiredAt: 100}.IdempotencyKey())
	assert.NotEqual(t, a.IdempotencyKey(), b.IdempotencyKey())
}

func TestWebhookReceived_IdempotencyKey_PrefersJiraEventID(t *testing.T) {
	withID := WebhookReceived{JiraEventID: "evt-1", ReceivedAt: 100, EventType: "issue_created"}
	assert.Equal(t, "webhook.received:evt-1", withID.IdempotencyKey())

	withoutID := WebhookReceived{ReceivedAt: 100, EventType: "issue_created"}
	assert.Equal(t, "webhook.received:100:issue_created", withoutID.IdempotencyKey())
}

func TestWebhookNormalized_IdempotencyKey_BucketsByMinute(t *testing.T) {
	// Two events in the same minute bucket must dedupe.
	a := WebhookNormalized{IssueKey: "JIRA-1", ChangeType: "updated", At: 60}
	b := WebhookNormalized{IssueKey: "JIRA-1", ChangeType: "updated", At: 119}
	assert.Equal(t, a.IdempotencyKey(), b.IdempotencyKey())

	// Two events that straddle a minute boundary must be distinct.
	c := WebhookNormalized{IssueKey: "JIRA-1", ChangeType: "updated", At: 59}
	d := WebhookNormalized{IssueKey: "JIRA-1", ChangeType: "updated", At: 60}
	assert.NotEqual(t, c.IdempotencyKey(), d.IdempotencyKey())
}

func TestWebhookNormalized_IdempotencyKey_PrefersJiraEventID(t *testing.T) {
	a := WebhookNormalized{JiraEventID: "evt-x", IssueKey: "JIRA-1", At: 100}
	assert.Equal(t, "webhook.normalized:evt-x", a.IdempotencyKey())
}

func TestMarshal_RoundtripPreservesFields(t *testing.T) {
	original := SubscriptionCreated{
		SubscriptionID:   "sub-1",
		TelegramID:       42,
		ChatID:           -100,
		SubscriptionType: "my_mentions",
		ProjectKey:       "PROJ",
		At:               1_700_000_000,
	}

	raw, err := Marshal(&original, "")
	require.NoError(t, err)

	var env Envelope
	require.NoError(t, json.Unmarshal(raw, &env))
	assert.Equal(t, SubjectSubscriptionCreated, env.Subject)
	assert.Empty(t, env.TraceID)

	var back SubscriptionCreated
	require.NoError(t, json.Unmarshal(env.Payload, &back))
	assert.Equal(t, original, back)
}
