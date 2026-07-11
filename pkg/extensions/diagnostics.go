package extensions

import "context"

// DiagnosticLevel is the severity of an extension diagnostic that may be
// surfaced by an interactive host.
type DiagnosticLevel string

const (
	DiagnosticLevelWarning DiagnosticLevel = "warning"
	DiagnosticLevelError   DiagnosticLevel = "error"
)

// Diagnostic is a structured warning or error emitted by an extension.
type Diagnostic struct {
	Level     DiagnosticLevel
	Extension string
	Message   string
	Fields    map[string]any
}

// DiagnosticSink receives structured extension diagnostics. Implementations
// should return promptly so extension stderr processing cannot be blocked by UI
// rendering.
type DiagnosticSink interface {
	ReportDiagnostic(ctx context.Context, diagnostic Diagnostic)
}

type diagnosticSinkKey struct{}

// ContextWithDiagnosticSink attaches a diagnostic sink to an extension runtime
// context.
func ContextWithDiagnosticSink(ctx context.Context, sink DiagnosticSink) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if sink == nil {
		return ctx
	}
	return context.WithValue(ctx, diagnosticSinkKey{}, sink)
}

// DiagnosticSinkFromContext returns the run-scoped diagnostic sink, if one is
// available.
func DiagnosticSinkFromContext(ctx context.Context) (DiagnosticSink, bool) {
	if ctx == nil {
		return nil, false
	}
	sink, ok := ctx.Value(diagnosticSinkKey{}).(DiagnosticSink)
	return sink, ok && sink != nil
}
