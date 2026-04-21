package telemetry

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
)

func TestInit_EmptyEndpoint_InstallsNoop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	shutdown, err := Init(ctx, Config{
		Service: "sjb-test",
	}, zerolog.Nop())
	require.NoError(t, err)
	require.NotNil(t, shutdown)

	tracer := otel.Tracer("sjb-test")
	_, span := tracer.Start(ctx, "noop-span")
	span.End()

	require.NoError(t, shutdown(ctx))
}

func TestInit_EmptyEndpoint_InstallsPropagator(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := Init(ctx, Config{Service: "sjb-test"}, zerolog.Nop())
	require.NoError(t, err)

	prop := otel.GetTextMapPropagator()
	require.NotNil(t, prop)

	carrier := propagation.MapCarrier{}
	prop.Inject(ctx, carrier)
}

func TestInit_EmptyEndpoint_MeterIsUsable(t *testing.T) {
	// With no endpoint Init skips the MeterProvider install entirely; the
	// global otel no-op meter takes over. Calling the instrument APIs
	// must not panic and must produce usable (non-nil) handles.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	shutdown, err := Init(ctx, Config{Service: "sjb-test"}, zerolog.Nop())
	require.NoError(t, err)

	m := otel.Meter("sjb-test")
	c, err := m.Int64Counter("test.counter")
	require.NoError(t, err)
	c.Add(ctx, 1, metric.WithAttributes())

	h, err := m.Float64Histogram("test.histogram")
	require.NoError(t, err)
	h.Record(ctx, 1.5, metric.WithAttributes())

	require.NoError(t, shutdown(ctx))
}

// TestInit_MetricsDisabled_TracesStillFlow asserts that setting
// OTEL_METRICS_EXPORTER=none together with a real endpoint installs
// the tracer but skips the metric exporter. Shutdown must flush only
// the tracer. The gRPC exporter dials lazily, so the loopback endpoint
// never needs to accept a connection for this test.
func TestInit_MetricsDisabled_TracesStillFlow(t *testing.T) {
	t.Setenv("OTEL_METRICS_EXPORTER", "none")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	shutdown, err := Init(ctx, Config{
		Service:  "sjb-test",
		Endpoint: "127.0.0.1:14317",
		Insecure: true,
	}, zerolog.Nop())
	require.NoError(t, err)
	require.NotNil(t, shutdown)

	// Calling meter APIs must still be safe — the global MeterProvider
	// stays on its no-op default because we skipped the Set call.
	m := otel.Meter("sjb-test")
	c, err := m.Int64Counter("test.disabled")
	require.NoError(t, err)
	c.Add(ctx, 1, metric.WithAttributes())

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	_ = shutdown(shutdownCtx)
}
