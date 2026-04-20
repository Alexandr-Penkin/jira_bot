package telemetry

import (
	"context"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/event"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"
	"go.opentelemetry.io/otel/trace"
)

// Mongo command instruments. Initialised once and reused for every
// monitor handed out by MongoCommandMonitor — the driver may call the
// monitor on many concurrent goroutines, so the counters/histograms
// must be shared state, not per-monitor.
var (
	mongoCmdMeter    = otel.Meter("SleepJiraBot/pkg/telemetry/mongo")
	mongoCmdCount    metric.Int64Counter
	mongoCmdDuration metric.Float64Histogram
)

func init() {
	mongoCmdCount, _ = mongoCmdMeter.Int64Counter(
		"sjb.mongo.command.count",
		metric.WithDescription("Count of MongoDB driver commands observed, labelled by command and outcome"),
	)
	mongoCmdDuration, _ = mongoCmdMeter.Float64Histogram(
		"sjb.mongo.command.duration",
		metric.WithDescription("Duration of MongoDB driver commands (Started → Succeeded/Failed)"),
		metric.WithUnit("ms"),
	)
}

// mongoCmdState carries the span + start timestamp across the async
// Started → Succeeded/Failed callbacks. RequestID is the correlation
// key; the driver guarantees uniqueness within the lifetime of a
// command.
type mongoCmdState struct {
	span  trace.Span
	start time.Time
}

// MongoCommandMonitor returns a driver CommandMonitor that emits a client
// span + metrics per MongoDB command. The span name is "mongo.<command>",
// span kind is Client, attributes carry db.system=mongodb + db.name +
// db.operation. Metrics: sjb.mongo.command.count (command, outcome),
// sjb.mongo.command.duration (command). Returning this from telemetry
// keeps the SDK choice in one place — when OTel is disabled the spans
// are non-recording and the instruments record into the global no-op
// meter at effectively zero cost.
func MongoCommandMonitor() *event.CommandMonitor {
	tr := otel.Tracer("SleepJiraBot/pkg/telemetry/mongo")
	var states sync.Map // map[int64]*mongoCmdState

	return &event.CommandMonitor{
		Started: func(ctx context.Context, e *event.CommandStartedEvent) {
			_, span := tr.Start(ctx, "mongo."+e.CommandName,
				trace.WithSpanKind(trace.SpanKindClient),
				trace.WithAttributes(
					semconv.DBSystemKey.String("mongodb"),
					semconv.DBNamespace(e.DatabaseName),
					semconv.DBOperationName(e.CommandName),
				),
			)
			states.Store(e.RequestID, &mongoCmdState{span: span, start: time.Now()})
		},
		Succeeded: func(ctx context.Context, e *event.CommandSucceededEvent) {
			finish(ctx, &states, e.RequestID, e.CommandName, "ok", nil)
		},
		Failed: func(ctx context.Context, e *event.CommandFailedEvent) {
			finish(ctx, &states, e.RequestID, e.CommandName, "error", e.Failure)
		},
	}
}

func finish(ctx context.Context, states *sync.Map, requestID int64, command, outcome string, failure error) {
	v, ok := states.LoadAndDelete(requestID)
	if !ok {
		return
	}
	st := v.(*mongoCmdState)
	if outcome == "error" {
		msg := ""
		if failure != nil {
			msg = failure.Error()
		}
		st.span.SetStatus(codes.Error, msg)
	}
	st.span.End()

	cmdAttr := attribute.String("command", command)
	mongoCmdDuration.Record(ctx, float64(time.Since(st.start).Microseconds())/1000.0,
		metric.WithAttributes(cmdAttr))
	mongoCmdCount.Add(ctx, 1, metric.WithAttributes(
		cmdAttr,
		attribute.String("outcome", outcome),
	))
}
