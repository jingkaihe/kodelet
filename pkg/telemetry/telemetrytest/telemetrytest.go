// Package telemetrytest provides tracing fixtures and assertions for tests.
package telemetrytest

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// NewRecorder installs an in-memory provider and content policy without an exporter.
// It restores all tracing globals at cleanup; callers must not run in parallel.
func NewRecorder(t *testing.T, captureContent bool) (*tracetest.SpanRecorder, *sdktrace.TracerProvider) {
	t.Helper()
	previousPolicy := telemetry.Config{
		CaptureContent:   telemetry.ContentEnabled(),
		InternalRPCSpans: telemetry.InternalRPCSpansEnabled(),
	}
	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() {
		shutdownErr := provider.Shutdown(context.Background())
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
		_, policyErr := telemetry.InitTracer(context.Background(), previousPolicy)
		assert.NoError(t, shutdownErr)
		assert.NoError(t, policyErr)
	})
	_, err := telemetry.InitTracer(t.Context(), telemetry.Config{CaptureContent: captureContent})
	require.NoError(t, err)
	otel.SetTracerProvider(provider)
	return recorder, provider
}

// AssertToolTracePrivacy checks that lifecycle events stay metadata-only and tool
// attributes contain input and output only when content capture is enabled.
func AssertToolTracePrivacy(t *testing.T, spans []sdktrace.ReadOnlySpan, toolName, input, output string, capture bool) {
	t.Helper()
	var sawStart, sawComplete, sawTool bool
	for _, span := range spans {
		if span.Name() == "invoke_agent kodelet" {
			encoded, err := json.Marshal(span.Events())
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), input)
			assert.NotContains(t, string(encoded), output)
			for _, event := range span.Events() {
				sawStart = sawStart || event.Name == "tool_execution_start"
				sawComplete = sawComplete || event.Name == "tool_execution_complete"
			}
		}
		if span.Name() == "execute_tool "+toolName {
			sawTool = true
			attributes, err := json.Marshal(span.Attributes())
			require.NoError(t, err)
			if capture {
				assert.Contains(t, string(attributes), input)
				assert.Contains(t, string(attributes), output)
			} else {
				assert.NotContains(t, string(attributes), input)
				assert.NotContains(t, string(attributes), output)
			}
		}
	}
	assert.True(t, sawStart, "missing tool_execution_start event")
	assert.True(t, sawComplete, "missing tool_execution_complete event")
	assert.True(t, sawTool, "missing execute_tool span for %s", toolName)
}
