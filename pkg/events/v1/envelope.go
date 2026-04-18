package eventsv1

import (
	"context"
	"encoding/json"
	"time"
)

// Envelope wraps every published event with routing + tracing metadata.
// The payload is kept as a raw JSON message so downstream services can
// decode into a type they own without importing the producer's struct.
type Envelope struct {
	// ID is a stable identifier unique per logical event occurrence.
	// It doubles as the NATS Nats-Msg-Id for JetStream-side dedup.
	ID string `json:"id"`
	// Subject mirrors the NATS subject for convenience when consumers
	// process events from a buffered queue.
	Subject string `json:"subject"`
	// PublishedAt is the producer-side timestamp (unix milliseconds).
	PublishedAt int64 `json:"published_at"`
	// SchemaVersion disambiguates envelope-level changes.
	SchemaVersion int `json:"schema_version"`
	// TraceID propagates OpenTelemetry trace context when available.
	TraceID string          `json:"trace_id,omitempty"`
	Payload json.RawMessage `json:"payload"`
}

// Event is implemented by every v1 payload. IdempotencyKey produces the
// Nats-Msg-Id used by JetStream to dedupe retries of the same logical
// occurrence.
type Event interface {
	Subject() string
	IdempotencyKey() string
}

// Publisher abstracts event emission so producer packages do not depend
// on a specific broker client. Implementations live in pkg/natsx (real)
// and are replaced by no-ops when ENABLE_EVENT_PUBLISH=false.
type Publisher interface {
	Publish(ctx context.Context, event Event, traceID string) error
	Close() error
}

// NoopPublisher discards every event. Useful as a default when a Publisher
// is optional so call sites need not nil-check.
type NoopPublisher struct{}

func (NoopPublisher) Publish(context.Context, Event, string) error { return nil }
func (NoopPublisher) Close() error                                 { return nil }

// Marshal wraps payload into an Envelope and returns JSON-encoded bytes.
func Marshal(e Event, traceID string) ([]byte, error) {
	payload, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	env := Envelope{
		ID:            e.IdempotencyKey(),
		Subject:       e.Subject(),
		PublishedAt:   time.Now().UnixMilli(),
		SchemaVersion: 1,
		TraceID:       traceID,
		Payload:       payload,
	}
	return json.Marshal(env)
}
