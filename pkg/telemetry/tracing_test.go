package telemetry

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestInitTracerDisabled(t *testing.T) {
	shutdown, err := InitTracer(context.Background(), Config{Enabled: false})
	require.NoError(t, err)
	require.NotNil(t, shutdown)
	assert.NoError(t, shutdown(context.Background()))
}

func TestGetSampler(t *testing.T) {
	tests := []struct {
		name         string
		cfg          Config
		wantDesc     string
		wantDecision sdktrace.SamplingDecision
	}{
		{
			name:         "always",
			cfg:          Config{SamplerType: "always"},
			wantDesc:     "AlwaysOnSampler",
			wantDecision: sdktrace.RecordAndSample,
		},
		{
			name:         "never",
			cfg:          Config{SamplerType: "never"},
			wantDesc:     "AlwaysOffSampler",
			wantDecision: sdktrace.Drop,
		},
		{
			name:         "ratio",
			cfg:          Config{SamplerType: "ratio", SamplerRatio: 1},
			wantDesc:     "ParentBased{root:TraceIDRatioBased{1}",
			wantDecision: sdktrace.RecordAndSample,
		},
		{
			name:         "default",
			cfg:          Config{SamplerType: "unknown"},
			wantDesc:     "AlwaysOnSampler",
			wantDecision: sdktrace.RecordAndSample,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sampler := getSampler(tt.cfg)
			assert.True(t, strings.HasPrefix(sampler.Description(), tt.wantDesc), sampler.Description())
			result := sampler.ShouldSample(sdktrace.SamplingParameters{ParentContext: context.Background()})
			assert.Equal(t, tt.wantDecision, result.Decision)
		})
	}
}

func TestSpanHelpers(t *testing.T) {
	spanRecorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	previousProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(previousProvider)
	})

	ctx := context.Background()
	err := WithSpan(ctx, "operation", func(ctx context.Context) error {
		assert.True(t, oteltrace.SpanFromContext(ctx).SpanContext().IsValid())
		AddEvent(ctx, "started", attribute.String("phase", "begin"))
		SetAttributes(ctx, attribute.String("component", "test"))
		return nil
	}, attribute.String("request", "abc"))
	require.NoError(t, err)

	ended := spanRecorder.Ended()
	require.Len(t, ended, 1)
	span := ended[0]
	assert.Equal(t, "operation", span.Name())
	assert.Equal(t, codes.Ok, span.Status().Code)
	assert.Contains(t, span.Attributes(), attribute.String("request", "abc"))
	assert.Contains(t, span.Attributes(), attribute.String("component", "test"))
	require.Len(t, span.Events(), 1)
	assert.Equal(t, "started", span.Events()[0].Name)

	spanRecorder.Reset()
	wantErr := errors.New("boom")
	err = WithSpan(ctx, "failing-operation", func(ctx context.Context) error {
		RecordError(ctx, wantErr)
		return wantErr
	})
	require.ErrorIs(t, err, wantErr)

	ended = spanRecorder.Ended()
	require.Len(t, ended, 1)
	span = ended[0]
	assert.Equal(t, "failing-operation", span.Name())
	assert.Equal(t, codes.Error, span.Status().Code)
	assert.Equal(t, wantErr.Error(), span.Status().Description)
	assert.NotEmpty(t, span.Events())

	spanRecorder.Reset()
	WithSpanFunc(ctx, "func-operation", func(ctx context.Context) {
		SetAttributes(ctx, attribute.Bool("called", true))
	})

	ended = spanRecorder.Ended()
	require.Len(t, ended, 1)
	span = ended[0]
	assert.Equal(t, "func-operation", span.Name())
	assert.Equal(t, codes.Ok, span.Status().Code)
	assert.Contains(t, span.Attributes(), attribute.Bool("called", true))
}

func TestTracerDefaultName(t *testing.T) {
	spanRecorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	previousProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(previousProvider)
	})

	_, span := Tracer("").Start(context.Background(), "default-name")
	span.End()

	ended := spanRecorder.Ended()
	require.Len(t, ended, 1)
	assert.Equal(t, "kodelet", ended[0].InstrumentationScope().Name)
}
