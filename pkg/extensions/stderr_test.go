package extensions

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtensionStderrWriterCapturesStructuredDiagnostics(t *testing.T) {
	sink := newRecordingDiagnosticSink()
	ctx := ContextWithDiagnosticSink(context.Background(), sink)
	var output bytes.Buffer
	writer := newExtensionStderrWriter(ctx, "fallback", &output)

	first := `{"level":"warn","extension":"mcp","message":"failed to initialize MCP server","server":"playwright",`
	second := `"error":"spawn npxx ENOENT"}` + "\nplain stderr\n"
	_, err := writer.Write([]byte(first))
	require.NoError(t, err)
	_, err = writer.Write([]byte(second))
	require.NoError(t, err)

	diagnostic := receiveDiagnostic(t, sink.ch)
	assert.Equal(t, DiagnosticLevelWarning, diagnostic.Level)
	assert.Equal(t, "mcp", diagnostic.Extension)
	assert.Equal(t, "failed to initialize MCP server", diagnostic.Message)
	assert.Equal(t, "playwright", diagnostic.Fields["server"])
	assert.Equal(t, "spawn npxx ENOENT", diagnostic.Fields["error"])
	assert.Equal(t, first+second, output.String())
	select {
	case unexpected := <-sink.ch:
		t.Fatalf("unexpected diagnostic: %#v", unexpected)
	default:
	}
}

func TestParseExtensionDiagnostic(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		fallback  string
		want      Diagnostic
		wantFound bool
	}{
		{
			name:     "warning uses fallback extension",
			line:     `{"level":"warning","message":"check configuration"}`,
			fallback: "weather",
			want: Diagnostic{
				Level:     DiagnosticLevelWarning,
				Extension: "weather",
				Message:   "check configuration",
				Fields:    map[string]any{},
			},
			wantFound: true,
		},
		{
			name:     "fatal is presented as an error",
			line:     `{"level":"fatal","extension":"mcp","message":"server stopped","code":12}`,
			fallback: "fallback",
			want: Diagnostic{
				Level:     DiagnosticLevelError,
				Extension: "mcp",
				Message:   "server stopped",
				Fields:    map[string]any{"code": float64(12)},
			},
			wantFound: true,
		},
		{name: "info is not surfaced", line: `{"level":"info","message":"ready"}`},
		{name: "unstructured stderr is not surfaced", line: "npm warning"},
		{name: "message is required", line: `{"level":"error"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := parseExtensionDiagnostic([]byte(tt.line), tt.fallback)
			assert.Equal(t, tt.wantFound, found)
			assert.Equal(t, tt.want, got)
		})
	}
}

type recordingDiagnosticSink struct {
	ch chan Diagnostic
}

func newRecordingDiagnosticSink() *recordingDiagnosticSink {
	return &recordingDiagnosticSink{ch: make(chan Diagnostic, 8)}
}

func (s *recordingDiagnosticSink) ReportDiagnostic(_ context.Context, diagnostic Diagnostic) {
	select {
	case s.ch <- diagnostic:
	default:
	}
}

func receiveDiagnostic(t *testing.T, ch <-chan Diagnostic) Diagnostic {
	t.Helper()
	select {
	case diagnostic := <-ch:
		return diagnostic
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for extension diagnostic")
		return Diagnostic{}
	}
}
