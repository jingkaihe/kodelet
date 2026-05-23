package main

import (
	"context"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
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

	shutdown, err := initTracing(context.Background())
	require.NoError(t, err)
	require.NotNil(t, shutdown)
	assert.NoError(t, shutdown(context.Background()))
}

func TestWithTracingWrapsCommandAndCapturesNonSensitiveFlags(t *testing.T) {
	spanRecorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	previousProvider := otel.GetTracerProvider()
	previousTracer := tracer
	otel.SetTracerProvider(provider)
	tracer = provider.Tracer("kodelet.cli.test")
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(previousProvider)
		tracer = previousTracer
	})

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
	assert.Contains(t, span.Attributes(), attribute.String("flag.target", "develop"))
	assert.NotContains(t, span.Attributes(), attribute.String("flag.api-key", "super-secret"))
}

func TestGetVersionReturnsPackageVersion(t *testing.T) {
	assert.NotEmpty(t, getVersion())
}
