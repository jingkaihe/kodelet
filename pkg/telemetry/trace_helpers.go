package telemetry

import (
	"context"

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
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
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
	span.RecordError(err, opts...)
	span.SetStatus(codes.Error, err.Error())
}
