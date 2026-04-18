package notifier

import (
	"context"
	"errors"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	eventsv1 "SleepJiraBot/pkg/events/v1"
)

type capturePub struct {
	got []eventsv1.Event
}

func (c *capturePub) Publish(_ context.Context, e eventsv1.Event, _ string) error {
	c.got = append(c.got, e)
	return nil
}
func (c *capturePub) Close() error { return nil }

type errPub struct{}

func (errPub) Publish(context.Context, eventsv1.Event, string) error {
	return errors.New("boom")
}
func (errPub) Close() error { return nil }

func TestEventNotifier_PublishesNotifyRequested(t *testing.T) {
	pub := &capturePub{}
	n := NewEvent(pub, zerolog.Nop())

	err := n.Send(context.Background(), Request{
		ChatID:                42,
		TelegramID:            42,
		Text:                  "hello",
		ParseMode:             "MarkdownV2",
		DisableWebPagePreview: true,
		DedupKey:              "poller:42:ISSUE-1:created",
		Reason:                "poller:my_new_issues",
	})
	require.NoError(t, err)
	require.Len(t, pub.got, 1)

	evt, ok := pub.got[0].(eventsv1.NotifyRequested)
	require.True(t, ok)
	assert.Equal(t, int64(42), evt.ChatID)
	assert.Equal(t, "hello", evt.Text)
	assert.Equal(t, "MarkdownV2", evt.ParseMode)
	assert.True(t, evt.DisableWebPagePreview)
	assert.Equal(t, "poller:42:ISSUE-1:created", evt.DedupKey)
	assert.NotZero(t, evt.RequestedAt)
	// IdempotencyKey must be deterministic for the same DedupKey.
	assert.Equal(t, evt.IdempotencyKey(), evt.IdempotencyKey())
}

func TestEventNotifier_PropagatesPublishError(t *testing.T) {
	n := NewEvent(errPub{}, zerolog.Nop())
	err := n.Send(context.Background(), Request{ChatID: 1, Text: "x"})
	require.Error(t, err)
}

func TestDirectNotifier_NilAPI(t *testing.T) {
	n := NewDirect(nil, zerolog.Nop())
	err := n.Send(context.Background(), Request{ChatID: 1, Text: "x"})
	require.Error(t, err)
}

func TestNotifyRequested_IdempotencyKey_Fallback(t *testing.T) {
	e := eventsv1.NotifyRequested{ChatID: 1, RequestedAt: 1234, DedupKey: ""}
	assert.Equal(t, "notify.requested:1:1234", e.IdempotencyKey())

	e2 := eventsv1.NotifyRequested{ChatID: 1, DedupKey: "k"}
	assert.NotEqual(t, e.IdempotencyKey(), e2.IdempotencyKey())
}
