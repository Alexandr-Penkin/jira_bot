package telemetry

import (
	"context"
	"sync"

	"go.mongodb.org/mongo-driver/v2/event"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"
	"go.opentelemetry.io/otel/trace"
)

// MongoCommandMonitor returns a driver CommandMonitor that emits a client
// span per MongoDB command. The span name is "mongo.<command>", span kind
// is Client, and attributes carry db.system=mongodb + db.name +
// db.operation. Returning this from telemetry keeps the SDK choice in one
// place — when OTel is disabled the created spans are non-recording and
// have zero cost beyond the tracer lookup.
func MongoCommandMonitor() *event.CommandMonitor {
	tr := otel.Tracer("SleepJiraBot/pkg/telemetry/mongo")
	var spans sync.Map // map[int64]trace.Span

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
			spans.Store(e.RequestID, span)
		},
		Succeeded: func(_ context.Context, e *event.CommandSucceededEvent) {
			v, ok := spans.LoadAndDelete(e.RequestID)
			if !ok {
				return
			}
			v.(trace.Span).End()
		},
		Failed: func(_ context.Context, e *event.CommandFailedEvent) {
			v, ok := spans.LoadAndDelete(e.RequestID)
			if !ok {
				return
			}
			sp := v.(trace.Span)
			msg := ""
			if e.Failure != nil {
				msg = e.Failure.Error()
			}
			sp.SetStatus(codes.Error, msg)
			sp.End()
		},
	}
}
