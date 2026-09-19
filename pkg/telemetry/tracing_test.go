package telemetry

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func preserveTracingPolicy(t *testing.T) {
	t.Helper()
	previousContent, previousInternalRPC := ContentEnabled(), InternalRPCSpansEnabled()
	t.Cleanup(func() {
		captureContent.Store(previousContent)
		internalRPCSpans.Store(previousInternalRPC)
	})
}

func TestInitTracerDisabled(t *testing.T) {
	preserveTracingPolicy(t)
	shutdown, err := InitTracer(context.Background(), Config{Enabled: false})
	require.NoError(t, err)
	require.NotNil(t, shutdown)
	assert.NoError(t, shutdown(context.Background()))
}

func TestContentCaptureIsExplicit(t *testing.T) {
	preserveTracingPolicy(t)
	for _, enabled := range []bool{false, true, false} {
		shutdown, err := InitTracer(t.Context(), Config{CaptureContent: enabled})
		require.NoError(t, err)
		assert.Equal(t, enabled, ContentEnabled())
		assert.NoError(t, shutdown(t.Context()))
	}
}

func TestInternalRPCSpansAreOptIn(t *testing.T) {
	preserveTracingPolicy(t)
	for _, enabled := range []bool{false, true, false} {
		shutdown, err := InitTracer(t.Context(), Config{InternalRPCSpans: enabled})
		require.NoError(t, err)
		assert.Equal(t, enabled, InternalRPCSpansEnabled())
		assert.NoError(t, shutdown(t.Context()))
	}
}

func TestTracerShutdownFlushesPendingSpans(t *testing.T) {
	preserveTracingPolicy(t)
	requests := make(chan []byte, 1)
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/traces", r.URL.Path)
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		requests <- body
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	defer collector.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", collector.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", collector.URL+"/v1/traces")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_HEADERS", "")
	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
	})
	shutdown, err := InitTracer(t.Context(), Config{Enabled: true, ServiceName: "tracing-test", SamplerType: "always"})
	require.NoError(t, err)
	_, span := Tracer("").Start(t.Context(), "pending-span")
	span.End()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	require.NoError(t, shutdown(ctx))
	select {
	case body := <-requests:
		assert.Contains(t, string(body), "pending-span")
	case <-ctx.Done():
		t.Fatal("shutdown did not export the pending span")
	}
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
	preserveTracingPolicy(t)
	captureContent.Store(false)
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
	assert.Equal(t, "*errors.errorString", span.Status().Description)
	assert.Empty(t, span.Events(), "error text may contain content and requires opt-in")

	spanRecorder.Reset()
	captureContent.Store(true)
	_, classified := Tracer("test").Start(ctx, "classified-error")
	RecordSpanErrorWithType(classified, wantErr, "rpc_error", oteltrace.WithAttributes(attribute.String("source", "test")))
	classified.End()
	ended = spanRecorder.Ended()
	require.Len(t, ended, 1)
	span = ended[0]
	assert.Contains(t, span.Attributes(), attribute.String("error.type", "rpc_error"))
	assert.Equal(t, codes.Error, span.Status().Code)
	assert.Equal(t, wantErr.Error(), span.Status().Description)
	require.Len(t, span.Events(), 1)
	assert.Contains(t, span.Events()[0].Attributes, attribute.String("exception.message", wantErr.Error()))
	assert.Contains(t, span.Events()[0].Attributes, attribute.String("source", "test"))

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

func TestErrorType(t *testing.T) {
	for _, test := range []struct {
		name     string
		err      error
		fallback string
		want     string
	}{
		{name: "no error"},
		{name: "status without error", fallback: "500", want: "500"},
		{name: "Go type", err: errors.New("private error"), want: "*errors.errorString"},
		{name: "protocol fallback", err: errors.New("private error"), fallback: "rpc_error", want: "rpc_error"},
		{name: "wrapped cancellation", err: errors.Join(errors.New("private error"), context.Canceled), fallback: "rpc_error", want: "cancelled"},
		{name: "wrapped timeout", err: errors.Join(errors.New("private error"), context.DeadlineExceeded), fallback: "500", want: "timeout"},
		{name: "cancellation takes precedence", err: errors.Join(context.DeadlineExceeded, context.Canceled), want: "cancelled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, ErrorType(test.err, test.fallback))
		})
	}
}
