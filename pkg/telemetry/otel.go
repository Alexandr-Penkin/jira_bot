// Package telemetry wires the OpenTelemetry SDK for the SleepJiraBot
// microservices. The Init helper installs a TracerProvider, a
// MeterProvider, and the W3C TraceContext + Baggage propagator so
// downstream code can call `otel.Tracer("...")` / `otel.Meter("...")`
// unconditionally.
//
// Behaviour is gated entirely on OTEL_EXPORTER_OTLP_ENDPOINT. Empty or
// unset → no providers are installed (the global otel no-ops take over)
// and the returned shutdown is a no-op. Non-empty → OTLP/gRPC exporters
// are dialled for both traces (batching, 5s) and metrics (periodic reader,
// 30s), and the shutdown function flushes them in parallel. The
// zero-config path keeps the default monolith behaviour byte-identical.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"
	"google.golang.org/grpc/credentials"
)

// metricsExporterEnv is the standard OTel env var operators use to
// disable metrics export without touching the endpoint. Setting it to
// "none" keeps traces flowing (useful for debugging-only stacks)
// while skipping the metric OTLP exporter entirely.
const metricsExporterEnv = "OTEL_METRICS_EXPORTER"

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

	traceExporter, err := otlptracegrpc.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("telemetry: create OTLP trace exporter: %w", err)
	}

	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter,
			sdktrace.WithBatchTimeout(5*time.Second),
		),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tracerProvider)

	// Operators can opt out of metrics export (keeping traces) by
	// setting the standard OTel env var OTEL_METRICS_EXPORTER=none.
	// Useful for debug-only stacks (Jaeger/Tempo without Prometheus)
	// or cost-controlled rollouts where metric volume is a concern.
	if os.Getenv(metricsExporterEnv) == "none" {
		log.Info().
			Str("endpoint", cfg.Endpoint).
			Str("service", serviceName).
			Msg("telemetry: OTLP tracer installed; metrics disabled via OTEL_METRICS_EXPORTER=none")
		return func(shutdownCtx context.Context) error {
			return tracerProvider.Shutdown(shutdownCtx)
		}, nil
	}

	metricOpts := []otlpmetricgrpc.Option{
		otlpmetricgrpc.WithEndpoint(cfg.Endpoint),
	}
	if cfg.Insecure {
		metricOpts = append(metricOpts, otlpmetricgrpc.WithInsecure())
	} else {
		metricOpts = append(metricOpts, otlpmetricgrpc.WithTLSCredentials(credentials.NewTLS(nil)))
	}

	metricExporter, err := otlpmetricgrpc.New(ctx, metricOpts...)
	if err != nil {
		// Trace provider is already installed; best-effort shutdown so the
		// error path does not leak the exporter goroutine.
		_ = tracerProvider.Shutdown(ctx)
		return nil, fmt.Errorf("telemetry: create OTLP metric exporter: %w", err)
	}

	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter,
			sdkmetric.WithInterval(30*time.Second),
		)),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(meterProvider)

	log.Info().
		Str("endpoint", cfg.Endpoint).
		Str("service", serviceName).
		Bool("insecure", cfg.Insecure).
		Msg("telemetry: OTLP tracer + meter providers installed")

	return func(shutdownCtx context.Context) error {
		return errors.Join(
			tracerProvider.Shutdown(shutdownCtx),
			meterProvider.Shutdown(shutdownCtx),
		)
	}, nil
}
