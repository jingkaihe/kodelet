package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	collectormetric "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
)

func TestMetricsExportFromDaemonWithoutRunnerExporter(t *testing.T) {
	var mu sync.Mutex
	// Retain the latest cumulative sums, not the sum of periodic exports.
	exported := map[string]map[string]int64{}
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/metrics", r.URL.Path)
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		for _, private := range []string{"runner-file-evidence", "inspect the runner file", "daemon-only-key"} {
			assert.NotContains(t, string(body), private)
		}
		var request collectormetric.ExportMetricsServiceRequest
		if !assert.NoError(t, proto.Unmarshal(body, &request)) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		for _, resource := range request.ResourceMetrics {
			var instance string
			for _, attr := range resource.Resource.Attributes {
				if attr.Key == "service.instance.id" {
					instance = attr.Value.GetStringValue()
				}
			}
			assert.NotEmpty(t, instance)
			for _, scope := range resource.ScopeMetrics {
				for _, m := range scope.Metrics {
					if m.GetSum() == nil {
						continue
					}
					var total int64
					for _, point := range m.GetSum().DataPoints {
						total += point.GetAsInt()
					}
					if exported[m.Name] == nil {
						exported[m.Name] = map[string]int64{}
					}
					exported[m.Name][instance] = total
				}
			}
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	t.Cleanup(collector.Close)
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", collector.URL+"/v1/metrics")
	t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "25")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", "cumulative")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.instance.id=test-daemon")
	previous := otel.GetMeterProvider()
	t.Cleanup(func() { otel.SetMeterProvider(previous) })
	shutdown, err := telemetry.InitMetrics(t.Context(), telemetry.Config{
		Enabled:     true,
		ServiceName: "kodelet-test-daemon",
	})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, shutdown(context.Background())) })
	fixture := newDaemonRunFixture(t, "standalone",
		"KODELET_METRICS_ENABLED=false",
		"KODELET_TRACING_ENABLED=false",
		"OTEL_RESOURCE_ATTRIBUTES=service.instance.id=test-runner",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=http://127.0.0.1:1/v1/metrics",
	)
	testDaemonFileRun(t, fixture)
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return exported["kodelet.model.calls"]["test-daemon"] > 0 &&
			exported["kodelet.model.tokens"]["test-daemon"] > 0 &&
			exported["kodelet.tool.executions"]["test-daemon"] > 0
	}, 5*time.Second, 25*time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, exported["kodelet.tool.executions"], 1)
	assert.EqualValues(t, 1, exported["kodelet.tool.executions"]["test-daemon"],
		"only the daemon records the runner's tool completion")
	assert.NotContains(t, exported["kodelet.model.calls"], "test-runner")
}
