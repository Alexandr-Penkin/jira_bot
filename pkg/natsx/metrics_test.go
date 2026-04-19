package natsx

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// TestPackageMetrics_InitializedByInit asserts the package-level metric
// instruments wired in init() are non-nil and safe to call with no
// configured MeterProvider (the global no-op meter must accept them).
func TestPackageMetrics_InitializedByInit(t *testing.T) {
	require.NotNil(t, publishCount)
	require.NotNil(t, publishDuration)
	require.NotNil(t, ackFailures)
	require.NotNil(t, ackDuration)

	ctx := context.Background()
	attrs := metric.WithAttributes(attribute.String("subject", "test.subject"))
	assert.NotPanics(t, func() {
		publishCount.Add(ctx, 1, attrs)
		publishDuration.Record(ctx, 0.5, attrs)
		ackFailures.Add(ctx, 1, attrs)
		ackDuration.Record(ctx, 5.0, attrs)
	})
}
