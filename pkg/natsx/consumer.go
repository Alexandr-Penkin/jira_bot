package natsx

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Consumer-side metric instruments. Every JetStream puller in the fleet
// records against these, labelled by subject + outcome, so the Grafana
// "events pipeline" dashboard can show producer-vs-consumer throughput
// side by side. They live in pkg/natsx (not each cmd/*) so new
// consumers inherit the instrumentation without having to re-declare
// instruments.
var (
	consumeCount    metric.Int64Counter
	consumeDuration metric.Float64Histogram
)

// Outcome labels used with the consumer instruments. Callers pass one
// of these to ObserveConsume to classify each processed message.
const (
	OutcomeAck  = "ack"
	OutcomeNak  = "nak"
	OutcomeTerm = "term"
)

func init() {
	consumeCount, _ = meter.Int64Counter(
		"sjb.events.consumed",
		metric.WithDescription("Count of JetStream messages processed by a consumer, labelled by subject and outcome"),
	)
	consumeDuration, _ = meter.Float64Histogram(
		"sjb.events.consume.duration",
		metric.WithDescription("Duration of consumer message handling (extract → ack/nak/term)"),
		metric.WithUnit("ms"),
	)
}

// ObserveConsume records both the consume.duration histogram and the
// consumed counter in one call. Typical usage:
//
//	start := time.Now()
//	defer natsx.ObserveConsume(ctx, msg.Subject, &outcome, start)
//	outcome = natsx.OutcomeAck // or Nak / Term, set during handling
//
// Passing outcome as a pointer lets the defer capture whichever value
// the handler settles on without the caller having to duplicate the
// record call on every branch.
func ObserveConsume(ctx context.Context, subject string, outcome *string, start time.Time) {
	o := ""
	if outcome != nil {
		o = *outcome
	}
	attrs := metric.WithAttributes(
		attribute.String("subject", subject),
		attribute.String("outcome", o),
	)
	consumeDuration.Record(ctx, float64(time.Since(start).Microseconds())/1000.0,
		metric.WithAttributes(attribute.String("subject", subject)))
	consumeCount.Add(ctx, 1, attrs)
}
