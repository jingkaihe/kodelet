// Package telemetry provides OpenTelemetry tracing and metrics for Kodelet.
package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
)

// Config configures a telemetry signal and its shared service resource.
type Config struct {
	// Enabled determines if the signal being initialized is enabled.
	Enabled bool
	// ServiceName and ServiceVersion identify the service in traces and metrics.
	ServiceName    string
	ServiceVersion string

	// The remaining fields apply only to tracing.
	// CaptureContent opts in to recording prompts, messages, and tool payloads.
	CaptureContent bool
	// InternalRPCSpans includes routine lifecycle and housekeeping transport spans.
	InternalRPCSpans bool
	// SamplerType is the type of sampler to use (always, never, ratio).
	SamplerType string
	// SamplerRatio is the sampling ratio when using the ratio sampler.
	SamplerRatio float64
}

func telemetryResource(ctx context.Context, cfg Config) (*resource.Resource, error) {
	return resource.New(ctx,
		resource.WithHost(),
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
		),
		resource.WithFromEnv(),
	)
}
