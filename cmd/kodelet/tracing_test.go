package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/telemetry"
	"github.com/jingkaihe/kodelet/pkg/telemetry/telemetrytest"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

func TestInitTracingDisabled(t *testing.T) {
	originalSettings := viper.AllSettings()
	defer func() {
		viper.Reset()
		for key, value := range originalSettings {
			viper.Set(key, value)
		}
	}()

	viper.Reset()
	viper.Set("tracing.enabled", false)
	viper.Set("tracing.sampler", "never")
	viper.Set("tracing.ratio", 0.25)
	viper.Set("tracing.internal_rpc_spans", true)
	previousPolicy := telemetry.Config{
		CaptureContent:   telemetry.ContentEnabled(),
		InternalRPCSpans: telemetry.InternalRPCSpansEnabled(),
	}
	t.Cleanup(func() {
		_, err := telemetry.InitTracer(context.Background(), previousPolicy)
		assert.NoError(t, err)
	})

	shutdown, err := initTracing(context.Background())
	require.NoError(t, err)
	require.NotNil(t, shutdown)
	assert.True(t, telemetry.InternalRPCSpansEnabled())
	assert.NoError(t, shutdown(context.Background()))
}

func TestWithTracingWrapsCommandAndCapturesNonSensitiveFlags(t *testing.T) {
	spanRecorder, provider := telemetrytest.NewRecorder(t, false)
	previousTracer := tracer
	tracer = provider.Tracer("kodelet.cli.test")
	t.Cleanup(func() { tracer = previousTracer })

	var ran bool
	var sawSpanContext bool
	cmd := &cobra.Command{
		Use: "demo",
		Run: func(cmd *cobra.Command, args []string) {
			ran = true
			sawSpanContext = trace.SpanFromContext(cmd.Context()).SpanContext().IsValid()
			assert.Equal(t, []string{"arg1", "arg2"}, args)
		},
	}
	cmd.Flags().String("target", "main", "")
	cmd.Flags().String("api-key", "secret", "")
	require.NoError(t, cmd.Flags().Set("target", "develop"))
	require.NoError(t, cmd.Flags().Set("api-key", "super-secret"))

	wrapped := withTracing(cmd)
	wrapped.SetContext(context.Background())
	wrapped.Run(wrapped, []string{"arg1", "arg2"})

	assert.True(t, ran)
	assert.True(t, sawSpanContext)
	ended := spanRecorder.Ended()
	require.Len(t, ended, 1)
	span := ended[0]
	assert.Equal(t, "cli.command", span.Name())
	assert.Equal(t, codes.Ok, span.Status().Code)
	assert.Contains(t, span.Attributes(), attribute.String("command.name", "demo"))
	assert.Contains(t, span.Attributes(), attribute.String("command.path", "demo"))
	assert.Contains(t, span.Attributes(), attribute.Int("args.count", 2))
	assert.Contains(t, span.Attributes(), attribute.StringSlice("command.flags", []string{"target"}))
	assert.NotContains(t, span.Attributes(), attribute.String("flag.target", "develop"))
	assert.NotContains(t, span.Attributes(), attribute.String("flag.api-key", "super-secret"))
}

func TestWithTracingRunERecordsErrorAndOptionalContent(t *testing.T) {
	recorder, provider := telemetrytest.NewRecorder(t, true)
	previousTracer := tracer
	tracer = provider.Tracer("kodelet.cli.test")
	t.Cleanup(func() { tracer = previousTracer })
	cmd := &cobra.Command{
		Use: "run",
		RunE: func(cmd *cobra.Command, _ []string) error {
			assert.True(t, trace.SpanFromContext(cmd.Context()).SpanContext().IsValid())
			return assert.AnError
		},
	}
	cmd.Flags().String("arg", "", "")
	require.NoError(t, cmd.Flags().Set("arg", "captured only by opt-in"))
	cmd.SetContext(t.Context())
	withTracing(cmd)
	require.ErrorIs(t, cmd.RunE(cmd, nil), assert.AnError)
	require.Len(t, recorder.Ended(), 1)
	span := recorder.Ended()[0]
	assert.Equal(t, codes.Error, span.Status().Code)
	assert.Contains(t, span.Attributes(), attribute.String("flag.arg", "captured only by opt-in"))
	assert.NotEmpty(t, span.Events())
}

func TestWithTracingLeavesCommandGroupsUnchanged(t *testing.T) {
	cmd := &cobra.Command{Use: "group"}
	assert.Same(t, cmd, withTracing(cmd))
	assert.Nil(t, cmd.Run)
	assert.Nil(t, cmd.RunE)
}

func TestTracingFlagsInitializeBeforeCommandAndFlushOnExit(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "error"}[failed], func(t *testing.T) {
			requests := make(chan []byte, 4)
			collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				requests <- body
				w.Header().Set("Content-Type", "application/x-protobuf")
			}))
			defer collector.Close()
			root := t.TempDir()
			config := filepath.Join(root, "config.yaml")
			require.NoError(t, os.WriteFile(config, []byte(`tracing:
  enabled: false
`), 0o600))
			environment := []string{
				"PATH=" + os.Getenv("PATH"),
				"HOME=" + root,
				"KODELET_TEST_CLI_PROCESS=1",
				"KODELET_CONFIG_FILE=" + config,
				"KODELET_CONFIG_FILE_MODE=" + configFileModeIsolate,
				"OTEL_EXPORTER_OTLP_ENDPOINT=" + collector.URL,
			}
			args := []string{"version", "--tracing-enabled", "--tracing-sampler=always"}
			if failed {
				// Fail in RunE, before any daemon or provider is contacted.
				args = []string{"run", "--server=not-a-url", "--tracing-enabled", "--tracing-sampler=always", "test"}
			}
			process := daemonCLIProcess(t.Context(), t, root, environment, args...)
			output, err := process.CombinedOutput()
			if failed {
				require.Error(t, err, "%s", output)
			} else {
				require.NoError(t, err, "%s", output)
			}
			select {
			case body := <-requests:
				assert.Contains(t, string(body), "cli.command")
			default:
				t.Fatalf("CLI exited without exporting its span: %s", output)
			}
		})
	}
}

func TestDistributedTraceFromCLIThroughDaemonAndRunner(t *testing.T) {
	var mu sync.Mutex
	var exported []*tracev1.Span
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		var request collectortrace.ExportTraceServiceRequest
		if err := proto.Unmarshal(body, &request); !assert.NoError(t, err) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		for _, resource := range request.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				exported = append(exported, scope.Spans...)
			}
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	t.Cleanup(collector.Close)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", collector.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", collector.URL+"/v1/traces")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_HEADERS", "")
	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	previousPolicy := telemetry.Config{
		CaptureContent:   telemetry.ContentEnabled(),
		InternalRPCSpans: telemetry.InternalRPCSpansEnabled(),
	}
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
		_, err := telemetry.InitTracer(context.Background(), previousPolicy)
		assert.NoError(t, err)
	})
	shutdown, err := telemetry.InitTracer(t.Context(), telemetry.Config{Enabled: true, ServiceName: "kodelet-test-daemon", SamplerType: "always"})
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, shutdown(context.Background()))
	})
	fixture := newDaemonRunFixture(t, "standalone",
		"KODELET_TRACING_ENABLED=true",
		"OTEL_EXPORTER_OTLP_ENDPOINT="+collector.URL,
	)
	testDaemonFileRun(t, fixture)

	// Wait for the runner's and daemon's batch processors, then assert actual
	// exported ancestry across the CLI process, HTTP, and runner process.
	var spans []*tracev1.Span
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, span := range exported {
			if span.Name == "tools.run_tool.file_read" {
				counts := map[string]int{}
				for _, candidate := range exported {
					if string(candidate.TraceId) == string(span.TraceId) {
						counts[candidate.Name]++
					}
				}
				for name, count := range map[string]int{
					"cli.command":             1,
					"POST /api/chat":          2,
					"invoke_agent kodelet":    1,
					"chat gpt-4o":             2,
					"execute_tool file_read":  1,
					"runner.rpc tool.execute": 2,
				} {
					if counts[name] < count {
						return false
					}
				}
				spans = append([]*tracev1.Span(nil), exported...)
				return true
			}
		}
		return false
	}, 10*time.Second, 50*time.Millisecond)
	byID := map[string]*tracev1.Span{}
	var tool *tracev1.Span
	for _, span := range spans {
		byID[string(span.SpanId)] = span
		assert.NotEqual(t, "runner.rpc lifecycle.dispatch", span.Name, "routine lifecycle spans are opt-in")
		assert.NotEqual(t, "runner.rpc ui.extension.cleanup", span.Name, "successful cleanup must not create traces")
		if span.Name == "tools.run_tool.file_read" {
			tool = span
		}
		payload, err := proto.Marshal(span)
		require.NoError(t, err)
		for _, content := range []string{"inspect the runner file", "runner-file-evidence", "daemon-only-key", "client-secret"} {
			assert.NotContains(t, string(payload), content, "default trace %s must not export content", span.Name)
		}
	}
	require.NotNil(t, tool)
	var ancestry []string
	for current := tool; current != nil; current = byID[string(current.ParentSpanId)] {
		assert.Equal(t, tool.TraceId, current.TraceId)
		ancestry = append(ancestry, current.Name)
		require.Less(t, len(ancestry), 20, "trace ancestry must not contain cycles")
	}
	assert.Contains(t, ancestry, "execute_tool file_read")
	assert.Contains(t, ancestry, "invoke_agent kodelet")
	assert.Equal(t, "cli.command", ancestry[len(ancestry)-1])
	for _, name := range []string{"POST /api/chat", "runner.rpc tool.execute"} {
		count := 0
		for _, ancestor := range ancestry {
			if ancestor == name {
				count++
			}
		}
		assert.Equal(t, 2, count, "%s client and server must be in the tool ancestry", name)
	}
	var invocation *tracev1.Span
	for _, span := range spans {
		if span.Name == "invoke_agent kodelet" && string(span.TraceId) == string(tool.TraceId) {
			invocation = span
		}
	}
	require.NotNil(t, invocation)
	modelCalls := 0
	for _, span := range spans {
		if span.Name == "chat gpt-4o" && string(span.TraceId) == string(tool.TraceId) {
			modelCalls++
			assert.Equal(t, invocation.SpanId, span.ParentSpanId, "model calls and logical tools must be siblings")
		}
	}
	assert.Equal(t, 2, modelCalls, "the file tool should sit between two model calls")
}

func TestGetVersionReturnsPackageVersion(t *testing.T) {
	assert.NotEmpty(t, getVersion())
}
