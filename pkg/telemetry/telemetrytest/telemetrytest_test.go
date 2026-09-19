package telemetrytest

import (
	"strconv"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

func TestNewRecorderRestoresTracingGlobals(t *testing.T) {
	_, previousProvider := NewRecorder(t, true)
	_, err := telemetry.InitTracer(t.Context(), telemetry.Config{CaptureContent: true, InternalRPCSpans: true})
	require.NoError(t, err)
	previousPropagator := &propagation.TraceContext{}
	otel.SetTextMapPropagator(previousPropagator)

	for _, capture := range []bool{false, true} {
		t.Run(strconv.FormatBool(capture), func(t *testing.T) {
			recorder, provider := NewRecorder(t, capture)
			assert.Same(t, provider, otel.GetTracerProvider())
			assert.Equal(t, capture, telemetry.ContentEnabled())
			assert.False(t, telemetry.InternalRPCSpansEnabled())
			assert.Same(t, previousPropagator, otel.GetTextMapPropagator(), "setup must not replace the propagator")
			_, span := otel.Tracer("test").Start(t.Context(), "recorded")
			span.End()
			require.Len(t, recorder.Ended(), 1)
			otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())
		})
		assert.Same(t, previousProvider, otel.GetTracerProvider())
		assert.Same(t, previousPropagator, otel.GetTextMapPropagator())
		assert.True(t, telemetry.ContentEnabled())
		assert.True(t, telemetry.InternalRPCSpansEnabled())
	}
}
