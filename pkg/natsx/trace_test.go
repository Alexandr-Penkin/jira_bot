package natsx

import (
	"context"
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestHeaderCarrier_GetSetKeys(t *testing.T) {
	h := nats.Header{}
	c := HeaderCarrier(h)

	// nats.Header is case-sensitive — OTel's W3C propagators always use
	// the lowercase spec spellings for both Inject and Extract, so
	// HeaderCarrier doesn't canonicalize either side.
	c.Set("traceparent", "00-abc-def-01")
	require.Equal(t, "00-abc-def-01", c.Get("traceparent"))
	require.Equal(t, "", c.Get("nonexistent"))

	c.Set("tracestate", "vendor=value")
	keys := c.Keys()
	require.ElementsMatch(t, []string{"traceparent", "tracestate"}, keys)
}

func TestExtractContext_PropagatesTraceparent(t *testing.T) {
	otel.SetTextMapPropagator(propagation.TraceContext{})
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	defer func() { _ = provider.Shutdown(context.Background()) }()

	tr := provider.Tracer("test")
	producerCtx, producerSpan := tr.Start(context.Background(), "producer")
	defer producerSpan.End()

	// Inject producer span context into NATS headers.
	headers := nats.Header{}
	otel.GetTextMapPropagator().Inject(producerCtx, HeaderCarrier(headers))
	require.NotEmpty(t, headers.Get("traceparent"))

	// Consumer-side extraction.
	msg := &nats.Msg{Subject: "sjb.test", Header: headers}
	extracted := ExtractContext(context.Background(), msg)
	sc := trace.SpanContextFromContext(extracted)
	require.True(t, sc.IsValid())
	require.Equal(t, producerSpan.SpanContext().TraceID(), sc.TraceID())
}

func TestExtractContext_NilSafe(t *testing.T) {
	ctx := context.Background()
	require.Equal(t, ctx, ExtractContext(ctx, nil))
	require.Equal(t, ctx, ExtractContext(ctx, &nats.Msg{}))
}

func TestConsumerAttrs_ReturnsConsumerKind(t *testing.T) {
	opts := ConsumerAttrs("sjb.notify.requested.v1")
	require.NotEmpty(t, opts)
}
