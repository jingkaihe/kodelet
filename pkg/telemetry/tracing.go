// Package telemetry provides OpenTelemetry tracing for Kodelet
package telemetry

import (
	"context"
	"errors"
	"time"

	pkgerrors "github.com/pkg/errors"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
)

// Config represents the configuration for the telemetry system
type Config struct {
	// Enabled determines if tracing is enabled
	Enabled bool
	// ServiceName is the name of the service in traces
	ServiceName string
	// ServiceVersion is the version of the service in traces
	ServiceVersion string
	// SamplerType is the type of sampler to use (always, never, ratio)
	SamplerType string
	// SamplerRatio is the sampling ratio when using ratio sampler
	SamplerRatio float64
}

// InitTracer initializes the OpenTelemetry tracer provider
// Returns a shutdown function to be called before application termination
func InitTracer(ctx context.Context, cfg Config) (shutdown func(context.Context) error, err error) {
	if !cfg.Enabled {
		// Return a no-op shutdown function if tracing is disabled
		return func(context.Context) error { return nil }, nil
	}

	var shutdownFuncs []func(context.Context) error

	// Configure resource with service information
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
		),
	)
	if err != nil {
		return nil, pkgerrors.Wrap(err, "failed to create resource")
	}

	// Configure OTLP exporter for Grafana Cloud or other backends
	// Uses environment variables:
	// - OTEL_EXPORTER_OTLP_ENDPOINT
	// - OTEL_EXPORTER_OTLP_HEADERS for auth
	traceExporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, pkgerrors.Wrap(err, "failed to create trace exporter")
	}
	shutdownFuncs = append(shutdownFuncs, traceExporter.Shutdown)

	// Configure trace provider with batch export for better performance
	batchSpanProcessor := trace.NewBatchSpanProcessor(
		traceExporter,
		trace.WithMaxExportBatchSize(512),
		trace.WithBatchTimeout(1*time.Second),
	)

	// Get the appropriate sampler based on configuration
	sampler := getSampler(cfg)

	tracerProvider := trace.NewTracerProvider(
		trace.WithResource(res),
		trace.WithSpanProcessor(batchSpanProcessor),
		trace.WithSampler(sampler),
	)
	shutdownFuncs = append(shutdownFuncs, tracerProvider.Shutdown)

	// Set the global tracer provider
	otel.SetTracerProvider(tracerProvider)

	// Set global propagator for context propagation
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// Return a shutdown function that calls all the shutdown functions
	return func(ctx context.Context) error {
		var err error
		for _, fn := range shutdownFuncs {
			err = errors.Join(err, fn(ctx))
		}
		return err
	}, nil
}

// getSampler returns a sampler based on the provided configuration
func getSampler(cfg Config) trace.Sampler {
	switch cfg.SamplerType {
	case "always":
		return trace.AlwaysSample()
	case "never":
		return trace.NeverSample()
	case "ratio":
		return trace.ParentBased(trace.TraceIDRatioBased(cfg.SamplerRatio))
	default:
		return trace.AlwaysSample()
	}
}
