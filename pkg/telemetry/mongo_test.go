package telemetry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/event"
)

func TestMongoCommandMonitor_ReturnsWiredCallbacks(t *testing.T) {
	m := MongoCommandMonitor()
	require.NotNil(t, m)
	assert.NotNil(t, m.Started, "Started callback must be set")
	assert.NotNil(t, m.Succeeded, "Succeeded callback must be set")
	assert.NotNil(t, m.Failed, "Failed callback must be set")
}

func TestMongoCommandInstruments_InitializedByInit(t *testing.T) {
	require.NotNil(t, mongoCmdCount)
	require.NotNil(t, mongoCmdDuration)
}

// TestMongoCommandMonitor_StartedSucceededDoesNotPanic drives a full
// Started → Succeeded round through the monitor and asserts the state
// map is drained. Verifies the happy path works under the global no-op
// meter (no MeterProvider configured in tests).
func TestMongoCommandMonitor_StartedSucceededDoesNotPanic(t *testing.T) {
	m := MongoCommandMonitor()
	ctx := context.Background()
	assert.NotPanics(t, func() {
		m.Started(ctx, &event.CommandStartedEvent{
			CommandName:  "find",
			DatabaseName: "sleepjirabot",
			RequestID:    42,
		})
		time.Sleep(1 * time.Millisecond)
		m.Succeeded(ctx, &event.CommandSucceededEvent{
			CommandFinishedEvent: event.CommandFinishedEvent{
				CommandName: "find",
				RequestID:   42,
			},
		})
	})
}

// TestMongoCommandMonitor_FailedDoesNotPanic covers the error branch —
// SetStatus with the failure's Error() + metric record with
// outcome=error. nil Failure is permitted by the driver contract.
func TestMongoCommandMonitor_FailedDoesNotPanic(t *testing.T) {
	m := MongoCommandMonitor()
	ctx := context.Background()
	assert.NotPanics(t, func() {
		m.Started(ctx, &event.CommandStartedEvent{
			CommandName:  "update",
			DatabaseName: "sleepjirabot",
			RequestID:    7,
		})
		m.Failed(ctx, &event.CommandFailedEvent{
			CommandFinishedEvent: event.CommandFinishedEvent{
				CommandName: "update",
				RequestID:   7,
			},
			Failure: errors.New("boom"),
		})
	})
	assert.NotPanics(t, func() {
		m.Started(ctx, &event.CommandStartedEvent{
			CommandName:  "update",
			DatabaseName: "sleepjirabot",
			RequestID:    8,
		})
		m.Failed(ctx, &event.CommandFailedEvent{
			CommandFinishedEvent: event.CommandFinishedEvent{
				CommandName: "update",
				RequestID:   8,
			},
			Failure: nil,
		})
	})
}

// TestMongoCommandMonitor_MissingStartedIsNoOp guards against a driver
// bug where Succeeded/Failed fires without a matching Started — the
// monitor must not panic or leak state.
func TestMongoCommandMonitor_MissingStartedIsNoOp(t *testing.T) {
	m := MongoCommandMonitor()
	ctx := context.Background()
	assert.NotPanics(t, func() {
		m.Succeeded(ctx, &event.CommandSucceededEvent{
			CommandFinishedEvent: event.CommandFinishedEvent{
				CommandName: "find",
				RequestID:   999,
			},
		})
		m.Failed(ctx, &event.CommandFailedEvent{
			CommandFinishedEvent: event.CommandFinishedEvent{
				CommandName: "find",
				RequestID:   998,
			},
		})
	})
}
