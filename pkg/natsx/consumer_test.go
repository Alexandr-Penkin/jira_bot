package natsx

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConsumerMetrics_InitializedByInit(t *testing.T) {
	require.NotNil(t, consumeCount)
	require.NotNil(t, consumeDuration)
}

// TestObserveConsume verifies the helper is safe to call under the
// global no-op meter (tests don't configure an exporter) and handles a
// nil outcome pointer without panicking.
func TestObserveConsume(t *testing.T) {
	ctx := context.Background()
	start := time.Now().Add(-5 * time.Millisecond)

	assert.NotPanics(t, func() {
		outcome := OutcomeAck
		ObserveConsume(ctx, "sjb.notify.requested.v1", &outcome, start)
	})
	assert.NotPanics(t, func() {
		ObserveConsume(ctx, "sjb.notify.requested.v1", nil, start)
	})
}

func TestOutcomeLabels_Stable(t *testing.T) {
	// The Grafana dashboard filters on these exact labels; catch
	// accidental renames.
	assert.Equal(t, "ack", OutcomeAck)
	assert.Equal(t, "nak", OutcomeNak)
	assert.Equal(t, "term", OutcomeTerm)
}
