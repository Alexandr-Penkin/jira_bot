// Package natsx is a thin wrapper around nats.go + JetStream tailored for
// SleepJiraBot's event-bus contract.
//
// Phase 0 of the DDD microservices split uses this only to publish domain
// events alongside the existing monolith. Consumers are introduced in
// later phases.
package natsx

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"
	"go.opentelemetry.io/otel/trace"

	eventsv1 "SleepJiraBot/pkg/events/v1"
)

var (
	tracer = otel.Tracer("SleepJiraBot/pkg/natsx")
	meter  = otel.Meter("SleepJiraBot/pkg/natsx")

	publishCount    metric.Int64Counter
	publishDuration metric.Float64Histogram
	ackFailures     metric.Int64Counter
	ackDuration     metric.Float64Histogram
)

func init() {
	publishCount, _ = meter.Int64Counter(
		"sjb.events.published",
		metric.WithDescription("Count of JetStream publish calls, labelled by subject and outcome"),
	)
	publishDuration, _ = meter.Float64Histogram(
		"sjb.events.publish.duration",
		metric.WithDescription("Duration of JetStream publish calls (marshal + PublishMsgAsync)"),
		metric.WithUnit("ms"),
	)
	ackFailures, _ = meter.Int64Counter(
		"sjb.events.ack_failed",
		metric.WithDescription("Count of async publish ack failures observed by the producer"),
	)
	ackDuration, _ = meter.Float64Histogram(
		"sjb.events.ack.duration",
		metric.WithDescription("Time from PublishMsgAsync dispatch to JetStream ack (ok or err)"),
		metric.WithUnit("ms"),
	)
}

// JetStreamPublisher publishes envelope-wrapped events with the event's
// idempotency key as the Nats-Msg-Id header, letting JetStream dedupe
// retries of the same logical occurrence. It implements eventsv1.Publisher.
type JetStreamPublisher struct {
	nc  *nats.Conn
	js  nats.JetStreamContext
	log zerolog.Logger
}

// StreamConfig describes a JetStream stream to ensure on startup.
type StreamConfig struct {
	Name     string
	Subjects []string
	MaxAge   time.Duration
	Storage  nats.StorageType
}

// DefaultStreams returns the set of streams the monolith ensures in
// Phase 0. Retention is left at Limits so events remain replayable while
// downstream services are being built.
func DefaultStreams() []StreamConfig {
	const week = 7 * 24 * time.Hour
	return []StreamConfig{
		{Name: eventsv1.StreamIdentity, Subjects: []string{eventsv1.SubjectsIdentity}, MaxAge: week, Storage: nats.FileStorage},
		{Name: eventsv1.StreamPreferences, Subjects: []string{eventsv1.SubjectsPreferences}, MaxAge: week, Storage: nats.FileStorage},
		{Name: eventsv1.StreamSubscription, Subjects: []string{eventsv1.SubjectsSubscription}, MaxAge: week, Storage: nats.FileStorage},
		{Name: eventsv1.StreamWebhook, Subjects: []string{eventsv1.SubjectsWebhook}, MaxAge: week, Storage: nats.FileStorage},
		{Name: eventsv1.StreamSchedule, Subjects: []string{eventsv1.SubjectsSchedule}, MaxAge: week, Storage: nats.FileStorage},
		{Name: eventsv1.StreamNotify, Subjects: []string{eventsv1.SubjectsNotify}, MaxAge: week, Storage: nats.FileStorage},
	}
}

// Connect dials NATS and returns a ready-to-use publisher. Callers that
// want the no-op behavior should construct NoopPublisher directly rather
// than plumbing a disabled flag through.
func Connect(ctx context.Context, url string, log zerolog.Logger) (*JetStreamPublisher, error) {
	if url == "" {
		return nil, errors.New("natsx: empty url")
	}
	nc, err := nats.Connect(url,
		nats.Name("sleepjirabot-monolith"),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
		nats.Timeout(5*time.Second),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			log.Warn().Err(err).Msg("nats disconnected")
		}),
		nats.ReconnectHandler(func(c *nats.Conn) {
			log.Info().Str("url", c.ConnectedUrl()).Msg("nats reconnected")
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("natsx: connect: %w", err)
	}

	js, err := nc.JetStream(nats.PublishAsyncMaxPending(256))
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("natsx: jetstream: %w", err)
	}

	p := &JetStreamPublisher{nc: nc, js: js, log: log}

	// Context is accepted for symmetry with future async-ensure code
	// paths, though the current JetStream AddStream call is blocking.
	_ = ctx

	return p, nil
}

// EnsureStreams creates the given streams if they do not exist, or
// updates subjects/retention when they do. Idempotent across restarts.
func (p *JetStreamPublisher) EnsureStreams(streams []StreamConfig) error {
	for _, s := range streams {
		cfg := &nats.StreamConfig{
			Name:       s.Name,
			Subjects:   s.Subjects,
			Retention:  nats.LimitsPolicy,
			MaxAge:     s.MaxAge,
			Storage:    s.Storage,
			Duplicates: 2 * time.Minute,
		}
		info, err := p.js.StreamInfo(s.Name)
		if err != nil && !errors.Is(err, nats.ErrStreamNotFound) {
			return fmt.Errorf("natsx: stream info %s: %w", s.Name, err)
		}
		if info == nil {
			if _, err := p.js.AddStream(cfg); err != nil {
				return fmt.Errorf("natsx: add stream %s: %w", s.Name, err)
			}
			p.log.Info().Str("stream", s.Name).Strs("subjects", s.Subjects).Msg("jetstream stream created")
			continue
		}
		if _, err := p.js.UpdateStream(cfg); err != nil {
			return fmt.Errorf("natsx: update stream %s: %w", s.Name, err)
		}
	}
	return nil
}

// Publish marshals the event into an Envelope and publishes it with its
// idempotency key as the Nats-Msg-Id header. A producer span is started
// around the publish call and the active W3C trace context is injected
// into the outgoing NATS headers so consumers downstream (telegram-svc
// et al.) can continue the trace via ExtractContext.
func (p *JetStreamPublisher) Publish(ctx context.Context, event eventsv1.Event, traceID string) error {
	start := time.Now()
	subject := event.Subject()
	subjectAttr := attribute.String("subject", subject)

	ctx, span := tracer.Start(ctx, "natsx.publish "+subject,
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			semconv.MessagingSystemKey.String("nats"),
			semconv.MessagingDestinationName(subject),
			semconv.MessagingOperationTypePublish,
			semconv.MessagingMessageID(event.IdempotencyKey()),
		),
	)
	defer func() {
		publishDuration.Record(ctx, float64(time.Since(start).Microseconds())/1000.0,
			metric.WithAttributes(subjectAttr))
		span.End()
	}()

	data, err := eventsv1.Marshal(event, traceID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "marshal failed")
		publishCount.Add(ctx, 1, metric.WithAttributes(subjectAttr, attribute.String("outcome", "marshal_error")))
		return fmt.Errorf("natsx: marshal %s: %w", subject, err)
	}
	msg := &nats.Msg{
		Subject: subject,
		Data:    data,
		Header:  nats.Header{},
	}
	msg.Header.Set(nats.MsgIdHdr, event.IdempotencyKey())
	// Inject the active span context as W3C traceparent/tracestate.
	// When OTel is disabled the global propagator is still installed
	// (TextMapPropagator is always non-nil after telemetry.Init) but
	// emits nothing for a non-recording span — safe no-op.
	otel.GetTextMapPropagator().Inject(ctx, HeaderCarrier(msg.Header))

	// We use the async publisher so slow NATS latency cannot block
	// primary request paths. The ack is observed in the background.
	fut, err := p.js.PublishMsgAsync(msg)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "publish failed")
		publishCount.Add(ctx, 1, metric.WithAttributes(subjectAttr, attribute.String("outcome", "publish_error")))
		return fmt.Errorf("natsx: publish %s: %w", subject, err)
	}
	publishCount.Add(ctx, 1, metric.WithAttributes(subjectAttr, attribute.String("outcome", "queued")))
	// Detach from the caller's ctx: the goroutine outlives the request
	// context in the common case (caller returns, ack arrives later). We
	// still want the metric increment on ack failure to land.
	ackCtx := context.WithoutCancel(ctx)
	ackStart := time.Now()
	go func() {
		recordAck := func(outcome string) {
			ackDuration.Record(ackCtx, float64(time.Since(ackStart).Microseconds())/1000.0,
				metric.WithAttributes(subjectAttr, attribute.String("outcome", outcome)))
		}
		select {
		case <-fut.Ok():
			recordAck("ok")
		case err := <-fut.Err():
			recordAck("error")
			ackFailures.Add(ackCtx, 1, metric.WithAttributes(subjectAttr))
			p.log.Warn().Err(err).Str("subject", subject).Msg("jetstream publish ack failed")
		case <-ackCtx.Done():
		}
	}()
	return nil
}

// Close drains the NATS connection.
func (p *JetStreamPublisher) Close() error {
	if p.nc == nil {
		return nil
	}
	return p.nc.Drain()
}

// PullSubscribe creates (or reuses) a durable pull subscription on the
// given stream and subject. The durable name survives restarts and
// coordinates delivery across multiple consumer replicas — JetStream
// hands each message to exactly one subscriber sharing the durable.
//
// Consumers should call Fetch in a loop, ack/nak each message based on
// processing outcome, and Drain on shutdown.
func (p *JetStreamPublisher) PullSubscribe(subject, durable string) (*nats.Subscription, error) {
	return p.js.PullSubscribe(subject, durable,
		nats.ManualAck(),
		nats.AckWait(30*time.Second),
		nats.MaxDeliver(5),
	)
}
