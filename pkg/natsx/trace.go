package natsx

import (
	"context"

	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"
	"go.opentelemetry.io/otel/trace"
)

// HeaderCarrier adapts nats.Header to the W3C TextMapCarrier interface so
// the global TextMapPropagator can inject/extract traceparent and
// tracestate transparently on either side of a JetStream publish.
type HeaderCarrier nats.Header

// Get implements propagation.TextMapCarrier.
func (c HeaderCarrier) Get(key string) string {
	v := nats.Header(c).Values(key)
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

// Set implements propagation.TextMapCarrier.
func (c HeaderCarrier) Set(key, value string) {
	nats.Header(c).Set(key, value)
}

// Keys implements propagation.TextMapCarrier.
func (c HeaderCarrier) Keys() []string {
	out := make([]string, 0, len(c))
	for k := range c {
		out = append(out, k)
	}
	return out
}

var _ propagation.TextMapCarrier = HeaderCarrier{}

// ExtractContext returns a child context that carries the span context
// propagated by the upstream publisher via traceparent/tracestate on the
// message headers. Safe to call on messages without headers — the
// returned context is the input unchanged.
func ExtractContext(parent context.Context, msg *nats.Msg) context.Context {
	if msg == nil || msg.Header == nil {
		return parent
	}
	return otel.GetTextMapPropagator().Extract(parent, HeaderCarrier(msg.Header))
}

// ConsumerAttrs returns a fixed set of semconv-aligned attributes for
// spans started around NATS message consumption. Callers add
// per-message specifics (message id) separately.
func ConsumerAttrs(subject string) []trace.SpanStartOption {
	return []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			semconv.MessagingSystemKey.String("nats"),
			semconv.MessagingDestinationName(subject),
			semconv.MessagingOperationTypeReceive,
		),
	}
}
