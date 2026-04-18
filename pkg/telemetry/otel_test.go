package telemetry

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
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
