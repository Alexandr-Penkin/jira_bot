// Package telemetry wires the OpenTelemetry SDK for the SleepJiraBot
// microservices. Phase 7a ships only the bootstrap — no spans are
// created yet; the goal is that every service has a configured
// TracerProvider and TextMapPropagator so downstream phases can sprinkle
// `otel.Tracer("sjb/<component>").Start(...)` without another round of
// plumbing.
//
// Behaviour is gated entirely on OTEL_EXPORTER_OTLP_ENDPOINT. Empty or
// unset → a no-op TracerProvider is installed and the returned shutdown
// is a no-op. Non-empty → an OTLP/gRPC exporter is dialled, a batching
// SpanProcessor is attached, and the shutdown function flushes it. The
// zero-config path keeps the default monolith behaviour byte-identical.
package telemetry

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"
	"google.golang.org/grpc/credentials"
)

// ShutdownFunc flushes and closes the active tracer provider. Always
// safe to call, even when telemetry is disabled.
type ShutdownFunc func(context.Context) error

// Config captures the knobs consumed by Init. Service is the
// service.name resource attribute; callers typically hardcode their
// logical name (e.g. "telegram-svc") and let Endpoint/Insecure come from
// env. Override is for operators who want a bespoke service.name.
type Config struct {
	Service  string
	Override string
	Endpoint string
	Insecure bool
}

// Init installs a global TracerProvider and W3C TraceContext + Baggage
// propagator. Returns a shutdown that flushes exporters — call it with
// a bounded context during service shutdown.
//
// When cfg.Endpoint is empty Init installs a no-op provider; the
// returned shutdown is a no-op as well. This lets downstream code call
// `otel.Tracer(...)` unconditionally.
func Init(ctx context.Context, cfg Config, log zerolog.Logger) (ShutdownFunc, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	if cfg.Endpoint == "" {
		log.Debug().Msg("telemetry: OTLP endpoint unset, using no-op tracer provider")
		return func(context.Context) error { return nil }, nil
	}

	serviceName := cfg.Service
	if cfg.Override != "" {
		serviceName = cfg.Override
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
		),
		resource.WithFromEnv(),
		resource.WithProcess(),
		resource.WithOS(),
		resource.WithHost(),
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry: build resource: %w", err)
	}

	opts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(cfg.Endpoint),
	}
	if cfg.Insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	} else {
		opts = append(opts, otlptracegrpc.WithTLSCredentials(credentials.NewTLS(nil)))
	}

	exporter, err := otlptracegrpc.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("telemetry: create OTLP exporter: %w", err)
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter,
			sdktrace.WithBatchTimeout(5*time.Second),
		),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(provider)

	log.Info().
		Str("endpoint", cfg.Endpoint).
		Str("service", serviceName).
		Bool("insecure", cfg.Insecure).
		Msg("telemetry: OTLP tracer provider installed")

	return func(shutdownCtx context.Context) error {
		return provider.Shutdown(shutdownCtx)
	}, nil
}
