package extensions

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
)

const maxExtensionDiagnosticBuffer = 64 * 1024

// extensionStderrWriter deliberately hides an underlying *os.File from
// os/exec. This makes os/exec give the child a pipe while still forwarding
// extension diagnostics to the configured writer. In particular, extensions
// must not inherit the TUI's controlling terminal and later restore a raw
// terminal state captured while the TUI was running.
type extensionStderrWriter struct {
	output      io.Writer
	ctx         context.Context
	extensionID string

	mu      sync.Mutex
	pending []byte
}

func newExtensionStderrWriter(ctx context.Context, extensionID string, output io.Writer) *extensionStderrWriter {
	if ctx == nil {
		ctx = context.Background()
	}
	if output == nil {
		output = io.Discard
	}
	return &extensionStderrWriter{
		output:      output,
		ctx:         ctx,
		extensionID: extensionID,
	}
}

func (w *extensionStderrWriter) Write(payload []byte) (int, error) {
	w.captureDiagnostics(payload)
	return w.output.Write(payload)
}

func (w *extensionStderrWriter) captureDiagnostics(payload []byte) {
	w.mu.Lock()
	w.pending = append(w.pending, payload...)

	lines := make([][]byte, 0, bytes.Count(w.pending, []byte{'\n'}))
	for {
		newline := bytes.IndexByte(w.pending, '\n')
		if newline < 0 {
			break
		}
		line := bytes.TrimSpace(w.pending[:newline])
		if len(line) > 0 {
			lines = append(lines, bytes.Clone(line))
		}
		w.pending = w.pending[newline+1:]
	}
	if len(w.pending) > maxExtensionDiagnosticBuffer {
		w.pending = nil
	}
	w.mu.Unlock()

	sink, ok := DiagnosticSinkFromContext(w.ctx)
	if !ok {
		return
	}
	for _, line := range lines {
		if diagnostic, ok := parseExtensionDiagnostic(line, w.extensionID); ok {
			sink.ReportDiagnostic(w.ctx, diagnostic)
		}
	}
}

func parseExtensionDiagnostic(line []byte, fallbackExtensionID string) (Diagnostic, bool) {
	var payload map[string]any
	if err := json.Unmarshal(line, &payload); err != nil {
		return Diagnostic{}, false
	}

	var level DiagnosticLevel
	switch strings.ToLower(strings.TrimSpace(stringField(payload, "level"))) {
	case "warn", "warning":
		level = DiagnosticLevelWarning
	case "error", "fatal", "panic":
		level = DiagnosticLevelError
	default:
		return Diagnostic{}, false
	}

	message := strings.TrimSpace(stringField(payload, "message"))
	if message == "" {
		return Diagnostic{}, false
	}
	extensionID := strings.TrimSpace(stringField(payload, "extension"))
	if extensionID == "" {
		extensionID = strings.TrimSpace(fallbackExtensionID)
	}

	delete(payload, "level")
	delete(payload, "extension")
	delete(payload, "message")
	return Diagnostic{
		Level:     level,
		Extension: extensionID,
		Message:   message,
		Fields:    payload,
	}, true
}

func stringField(fields map[string]any, name string) string {
	value, ok := fields[name].(string)
	if !ok {
		return ""
	}
	return value
}
