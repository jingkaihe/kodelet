package telemetry

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Tracer returns a named tracer from the global provider
// If the name is empty, it uses "kodelet" as the default
func Tracer(name string) trace.Tracer {
	if name == "" {
		name = "kodelet"
	}
	return otel.GetTracerProvider().Tracer(name)
}

// WithSpan wraps a function with a span
// It automatically sets the status and records errors
func WithSpan(ctx context.Context, name string, f func(context.Context) error, attrs ...attribute.KeyValue) error {
	tracer := Tracer("kodelet")
	ctx, span := tracer.Start(ctx, name, trace.WithAttributes(attrs...))
	defer span.End()

	err := f(ctx)
	if err != nil {
		RecordSpanError(span, err)
	} else {
		span.SetStatus(codes.Ok, "")
	}

	return err
}

// WithSpanFunc is like WithSpan but for functions that don't return errors
func WithSpanFunc(ctx context.Context, name string, f func(context.Context), attrs ...attribute.KeyValue) {
	tracer := Tracer("kodelet")
	ctx, span := tracer.Start(ctx, name, trace.WithAttributes(attrs...))
	defer span.End()

	f(ctx)
	span.SetStatus(codes.Ok, "")
}

// AddEvent adds an event to the current span
func AddEvent(ctx context.Context, name string, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	span.AddEvent(name, trace.WithAttributes(attrs...))
}

// SetAttributes adds attributes to the current span
func SetAttributes(ctx context.Context, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(attrs...)
}

// RecordError records an error on the current span
func RecordError(ctx context.Context, err error, opts ...trace.EventOption) {
	span := trace.SpanFromContext(ctx)
	RecordSpanError(span, err, opts...)
}

// RecordSpanError records an error without exporting arbitrary provider responses
// or tool output unless content capture is explicitly enabled in this process.
func RecordSpanError(span trace.Span, err error, opts ...trace.EventOption) {
	RecordSpanErrorWithType(span, err, ErrorType(err, ""), opts...)
}

// ErrorType classifies cancellation and timeouts before using a caller-supplied
// fallback. Without a fallback, other errors are classified by their Go type.
func ErrorType(err error, fallback string) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case fallback != "":
		return fallback
	case err != nil:
		return fmt.Sprintf("%T", err)
	default:
		return ""
	}
}

// SetSpanError records only a bounded classification, never exception text,
// regardless of the content-capture policy. An empty classification is a no-op.
func SetSpanError(span trace.Span, errorType string) {
	if errorType == "" {
		return
	}
	span.SetAttributes(attribute.String("error.type", errorType))
	span.SetStatus(codes.Error, errorType)
}

// RecordSpanErrorWithType uses an already-classified error while retaining the
// same content-capture policy as RecordSpanError.
func RecordSpanErrorWithType(span trace.Span, err error, errorType string, opts ...trace.EventOption) {
	if err == nil {
		return
	}
	SetSpanError(span, errorType)
	if ContentEnabled() {
		span.RecordError(err, opts...)
		span.SetStatus(codes.Error, err.Error())
	}
}
