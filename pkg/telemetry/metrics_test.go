package telemetry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	collectormetric "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

func TestInitMetricsDisabled(t *testing.T) {
	previous := otel.GetMeterProvider()
	shutdown, err := InitMetrics(t.Context(), Config{})
	require.NoError(t, err)
	assert.Same(t, previous, otel.GetMeterProvider())
	assert.NoError(t, shutdown(t.Context()))
}

func TestMetricsShutdownExportsPendingMeasurements(t *testing.T) {
	requests := make(chan *collectormetric.ExportMetricsServiceRequest, 1)
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/metrics", r.URL.Path)
		assert.Equal(t, "application/x-protobuf", r.Header.Get("Content-Type"))
		assert.Equal(t, "Bearer test", r.Header.Get("Authorization"))
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		request := new(collectormetric.ExportMetricsServiceRequest)
		assert.NoError(t, proto.Unmarshal(body, request))
		requests <- request
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	t.Cleanup(collector.Close)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", collector.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "Authorization=Bearer test")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_HEADERS", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", "")
	t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "3600000")
	t.Setenv("OTEL_SERVICE_NAME", "")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
	previous := otel.GetMeterProvider()
	t.Cleanup(func() { otel.SetMeterProvider(previous) })
	shutdown, err := InitMetrics(t.Context(), Config{
		Enabled:        true,
		ServiceName:    "kodelet-test",
		ServiceVersion: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = shutdown(context.Background()) })
	RecordModelCall(t.Context(), "openai", "model", 2*time.Second, nil)
	RecordTokenUsage(t.Context(), "openai", "model", "input", 10)
	RecordCost(t.Context(), "openai", "model", .25)
	require.NoError(t, shutdown(t.Context()))

	var request *collectormetric.ExportMetricsServiceRequest
	select {
	case request = <-requests:
	default:
		t.Fatal("shutdown did not flush metrics")
	}
	require.Len(t, request.ResourceMetrics, 1)
	resource := request.ResourceMetrics[0]
	attrs := map[string]string{}
	for _, attr := range resource.Resource.Attributes {
		attrs[attr.Key] = attr.Value.GetStringValue()
	}
	host, err := os.Hostname()
	require.NoError(t, err)
	assert.Equal(t, host, attrs["host.name"])
	assert.Equal(t, "kodelet-test", attrs["service.name"])
	assert.NotContains(t, attrs, "service.instance.id")

	require.Len(t, resource.ScopeMetrics, 1)
	metrics := map[string]*metricpb.Metric{}
	for _, metric := range resource.ScopeMetrics[0].Metrics {
		metrics[metric.Name] = metric
	}
	require.Len(t, metrics, 4)
	calls := metrics["kodelet.model.calls"].GetSum()
	require.NotNil(t, calls)
	assert.Equal(t, metricpb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE,
		calls.AggregationTemporality)
	assert.EqualValues(t, 1, calls.DataPoints[0].GetAsInt())
	assert.EqualValues(t, 10, metrics["kodelet.model.tokens"].GetSum().DataPoints[0].GetAsInt())
	assert.Equal(t, .25, metrics["kodelet.model.cost"].GetSum().DataPoints[0].GetAsDouble())
	assert.Equal(t, 2.0, metrics["kodelet.model.duration"].GetHistogram().DataPoints[0].GetSum())
}

func TestMetricsShutdownRespectsDeadline(t *testing.T) {
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer collector.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", collector.URL+"/v1/metrics")
	t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "3600000")
	previous := otel.GetMeterProvider()
	defer otel.SetMeterProvider(previous)
	shutdown, err := InitMetrics(t.Context(), Config{Enabled: true, ServiceName: "test"})
	require.NoError(t, err)
	RecordModelCall(t.Context(), "openai", "model", time.Second, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	require.Error(t, shutdown(ctx))
	assert.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
}
