package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

func TestMalformedResourceAttributesKeepTelemetryEnabled(t *testing.T) {
	preserveTracingPolicy(t)
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	t.Cleanup(collector.Close)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", collector.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	t.Setenv("OTEL_SERVICE_NAME", "")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "malformed,service.instance.id=daemon-1")
	cfg := Config{Enabled: true, ServiceName: "kodelet-test", ServiceVersion: "test"}

	res, err := telemetryResource(t.Context(), cfg)
	require.NoError(t, err)
	attrs := map[attribute.Key]string{}
	for _, attr := range res.Attributes() {
		attrs[attr.Key] = attr.Value.String()
	}
	assert.Equal(t, "daemon-1", attrs["service.instance.id"])
	assert.Equal(t, "kodelet-test", attrs["service.name"])
	assert.Equal(t, "test", attrs["service.version"])
	assert.NotEmpty(t, attrs["host.name"])
	assert.NotContains(t, attrs, attribute.Key("malformed"))

	previousTracer := otel.GetTracerProvider()
	previousMeter := otel.GetMeterProvider()
	previousPropagator := otel.GetTextMapPropagator()
	t.Cleanup(func() {
		otel.SetTracerProvider(previousTracer)
		otel.SetMeterProvider(previousMeter)
		otel.SetTextMapPropagator(previousPropagator)
	})
	for name, initialize := range map[string]func(context.Context, Config) (func(context.Context) error, error){
		"tracing": InitTracer,
		"metrics": InitMetrics,
	} {
		shutdown, err := initialize(t.Context(), cfg)
		require.NoError(t, err, name)
		assert.NoError(t, shutdown(context.Background()), name)
	}
}
