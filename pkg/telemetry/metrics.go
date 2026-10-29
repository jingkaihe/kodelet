package telemetry

import (
	"context"
	"time"

	"github.com/pkg/errors"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// InitMetrics installs opt-in OTLP/HTTP metrics independently of tracing. The
// periodic reader aggregates in memory; recording never waits for the network.
// Standard OTEL_* variables configure the exporter and export interval.
func InitMetrics(ctx context.Context, cfg Config) (func(context.Context) error, error) {
	if !cfg.Enabled {
		return func(context.Context) error { return nil }, nil
	}
	res, err := telemetryResource(ctx, cfg)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create metrics resource")
	}
	exporter, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create metrics exporter")
	}
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter)),
		sdkmetric.WithCardinalityLimit(2000),
	)
	otel.SetMeterProvider(provider)
	// Shutdown performs a final collection/export, including short-lived runs.
	return provider.Shutdown, nil
}

// Instruments use constant, valid definitions. Look up the current provider on
// recording, like Tracer, so SDK embedders can install their own provider.

// RecordModelCall records one logical provider call, including streaming/retries.
func RecordModelCall(
	ctx context.Context,
	provider, model string,
	duration time.Duration,
	err error,
) {
	outcome := "success"
	if err != nil {
		outcome = ErrorType(err, "error")
	}
	attrs := metric.WithAttributes(
		attribute.String("gen_ai.provider.name", provider),
		attribute.String("gen_ai.request.model", model),
		attribute.String("outcome", outcome),
	)
	meter := otel.Meter("kodelet.llm")
	calls, _ := meter.Int64Counter("kodelet.model.calls",
		metric.WithUnit("{call}"),
		metric.WithDescription("Logical model calls, including streaming and retries."),
	)
	calls.Add(ctx, 1, attrs)
	latency, _ := meter.Float64Histogram("kodelet.model.duration", durationOptions()...)
	latency.Record(ctx, duration.Seconds(), attrs)
}

// RecordTokenUsage records disjoint input, output, cache_read, or cache_write
// usage increments, never cumulative thread snapshots or parent aggregates.
func RecordTokenUsage(
	ctx context.Context,
	provider, model, tokenType string,
	tokens int64,
) {
	if tokens <= 0 {
		return
	}
	tokenCount, _ := otel.Meter("kodelet.llm").Int64Counter("kodelet.model.tokens",
		metric.WithUnit("{token}"),
		metric.WithDescription("Token usage by disjoint billing category."),
	)
	tokenCount.Add(ctx, tokens, metric.WithAttributes(
		attribute.String("gen_ai.provider.name", provider),
		attribute.String("gen_ai.request.model", model),
		attribute.String("token.type", tokenType),
	))
}

// RecordCost records an estimated USD cost increment using Kodelet's pricing.
func RecordCost(ctx context.Context, provider, model string, cost float64) {
	if cost <= 0 {
		return
	}
	costs, _ := otel.Meter("kodelet.llm").Float64Counter("kodelet.model.cost",
		metric.WithUnit("USD"),
		metric.WithDescription("Estimated model usage cost, not a billing ledger."),
	)
	costs.Add(ctx, cost, metric.WithAttributes(
		attribute.String("gen_ai.provider.name", provider),
		attribute.String("gen_ai.request.model", model),
	))
}

// RecordToolExecution records a runner-measured completion in the control plane,
// including code-mode children. Runners and RPC spans do not record it again.
func RecordToolExecution(
	ctx context.Context,
	name string,
	nested bool,
	duration time.Duration,
	outcome string,
) {
	attrs := metric.WithAttributes(
		attribute.String("gen_ai.tool.name", name),
		attribute.Bool("kodelet.tool.nested", nested),
		attribute.String("outcome", outcome),
	)
	meter := otel.Meter("kodelet.tools")
	executions, _ := meter.Int64Counter("kodelet.tool.executions",
		metric.WithUnit("{execution}"),
		metric.WithDescription("Runner tool lifecycles, including code-mode children."),
	)
	executions.Add(ctx, 1, attrs)
	latency, _ := meter.Float64Histogram("kodelet.tool.duration", durationOptions()...)
	latency.Record(ctx, duration.Seconds(), attrs)
}

func durationOptions() []metric.Float64HistogramOption {
	return []metric.Float64HistogramOption{
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(.01, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120, 300, 600),
	}
}
