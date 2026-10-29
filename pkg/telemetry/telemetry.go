// Package telemetry provides OpenTelemetry tracing and metrics for Kodelet.
package telemetry

import (
	"context"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/pkg/errors"
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
	res, err := resource.New(ctx,
		resource.WithHost(),
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
		),
		resource.WithFromEnv(),
	)
	if errors.Is(err, resource.ErrPartialResource) {
		// The SDK keeps valid attributes and drops malformed environment entries;
		// a bad OTEL_RESOURCE_ATTRIBUTES entry must not disable telemetry.
		logger.G(ctx).WithError(err).Warn("Ignoring invalid OpenTelemetry resource attributes")
		return res, nil
	}
	return res, err
}
