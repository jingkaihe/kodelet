# ADR 007: Implementing OpenTelemetry Tracing in Go

## Status

Proposed

## Context

As Kodelet's CLI functionality grows, understanding the execution flow and performance of its operations becomes increasingly important. Tracing provides visibility into the execution path of commands and interactions with various tools and the LLM service. OpenTelemetry (OTel) is an open-source observability framework that provides vendor-neutral APIs, libraries, and agents for collecting distributed traces.

To improve our ability to monitor CLI performance, diagnose issues with LLM interactions, and understand user workflow behavior, we need to implement distributed tracing. This ADR outlines how to implement OpenTelemetry tracing in Kodelet's Go-based CLI application and integrate with Grafana Cloud.

## Decision

We will adopt OpenTelemetry for tracing in our Go application with the following implementation steps:

### 1. Install Required Dependencies

```bash
go get go.opentelemetry.io/otel \
      go.opentelemetry.io/otel/trace \
      go.opentelemetry.io/otel/sdk \
      go.opentelemetry.io/otel/exporters/otlp/otlptrace \
      go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp \
      go.opentelemetry.io/otel/propagation
```

### 2. Set Up Tracer Provider

Create a dedicated package for tracing in `pkg/telemetry` with initialization code:

```go
// pkg/telemetry/tracing.go
package telemetry

import (
    "context"
    "errors"
    "fmt"
    "time"

    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace"
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
    "go.opentelemetry.io/otel/propagation"
    "go.opentelemetry.io/otel/sdk/resource"
    "go.opentelemetry.io/otel/sdk/trace"
    semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
)

// InitTracer initializes the OpenTelemetry tracer provider
// Returns a shutdown function to be called before application termination
func InitTracer(ctx context.Context, serviceName, serviceVersion string) (shutdown func(context.Context) error, err error) {
    var shutdownFuncs []func(context.Context) error

    // Configure resource with service information
    res, err := resource.New(ctx,
        resource.WithAttributes(
            semconv.ServiceName(serviceName),
            semconv.ServiceVersion(serviceVersion),
        ),
    )
    if err != nil {
        return nil, fmt.Errorf("failed to create resource: %w", err)
    }

    // Configure OTLP exporter for Grafana Cloud
    // Uses environment variables:
    // - OTEL_EXPORTER_OTLP_ENDPOINT
    // - OTEL_EXPORTER_OTLP_HEADERS for auth
    traceExporter, err := otlptracehttp.New(ctx)
    if err != nil {
        return nil, fmt.Errorf("failed to create trace exporter: %w", err)
    }
    shutdownFuncs = append(shutdownFuncs, traceExporter.Shutdown)

    // Configure trace provider with batch export for better performance
    batchSpanProcessor := trace.NewBatchSpanProcessor(
        traceExporter,
        trace.WithMaxExportBatchSize(512),
        trace.WithBatchTimeout(5 * time.Second),
    )

    tracerProvider := trace.NewTracerProvider(
        trace.WithResource(res),
        trace.WithSpanProcessor(batchSpanProcessor),
        trace.WithSampler(trace.ParentBased(trace.TraceIDRatioBased(0.1))), // Sample 10% of traces in production
    )
    shutdownFuncs = append(shutdownFuncs, tracerProvider.Shutdown)

    // Set the global tracer provider
    otel.SetTracerProvider(tracerProvider)

    // Set global propagator for context propagation
    otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
        propagation.TraceContext{},
        propagation.Baggage{},
    ))

    // Return a shutdown function that calls all the shutdown functions
    return func(ctx context.Context) error {
        var err error
        for _, fn := range shutdownFuncs {
            err = errors.Join(err, fn(ctx))
        }
        return err
    }, nil
}
```

### 3. Initialize Tracing in Application Entry Point

Initialize the tracer in the main function:

```go
// cmd/kodelet/main.go
package main

import (
    "context"
    "log"
    "os"
    "os/signal"

    "github.com/jingkaihe/kodelet/pkg/telemetry"
)

func main() {
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
    defer stop()

    // Initialize OpenTelemetry
    shutdown, err := telemetry.InitTracer(ctx, "kodelet", getAppVersion())
    if err != nil {
        log.Fatalf("Failed to initialize OpenTelemetry: %v", err)
    }

    // Ensure clean shutdown of the OpenTelemetry SDK
    defer func() {
        if err := shutdown(ctx); err != nil {
            log.Printf("Failed to shut down OpenTelemetry: %v", err)
        }
    }()

    // ... rest of main function
}

func getAppVersion() string {
    // Get version from pkg/version or other source
    return "1.0.0" // Replace with actual version retrieval
}
```

### 4. Create Helper for Creating Traced Functions

Create a helper function to simplify creating traced functions:

```go
// pkg/telemetry/trace_helpers.go
package telemetry

import (
    "context"

    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/attribute"
    "go.opentelemetry.io/otel/codes"
    "go.opentelemetry.io/otel/trace"
)

// Tracer returns a named tracer from the global provider
func Tracer(name string) trace.Tracer {
    return otel.GetTracerProvider().Tracer(name)
}

// WithSpan wraps a function with a span
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
```

### 5. Instrument CLI Commands

Instrument CLI commands using the tracing helpers:

```go
// pkg/commands/run.go
package commands

import (
    "context"

    "github.com/jingkaihe/kodelet/pkg/telemetry"
    "go.opentelemetry.io/otel/attribute"
)

func ExecuteRunCommand(ctx context.Context, query string) error {
    // Create a span for the run command
    return telemetry.WithSpan(ctx, "kodelet.run", func(ctx context.Context) error {
        // Set attributes for the command
        span := trace.SpanFromContext(ctx)
        span.SetAttributes(attribute.String("query", query))

        // Execute the command
        return processQuery(ctx, query)
    })
}

// processQuery handles the query execution
func processQuery(ctx context.Context, query string) error {
    // Use the tracing helper again for a sub-operation
    return telemetry.WithSpan(ctx, "kodelet.query.process", func(ctx context.Context) error {
        // Query processing logic here
        return nil
    })
}
```

### 6. Instrument Core Components

Add tracing to core Kodelet components:

```go
// pkg/llm/thread.go - Add tracing to the LLM interaction
package llm

import (
    "context"

    "github.com/jingkaihe/kodelet/pkg/telemetry"
    "go.opentelemetry.io/otel/attribute"
    "go.opentelemetry.io/otel/trace"
)

func (t *Thread) SendMessage(ctx context.Context, message string) error {
    return telemetry.WithSpan(ctx, "llm.send_message", func(ctx context.Context) error {
        span := trace.SpanFromContext(ctx)
        span.SetAttributes(
            attribute.Int("message.length", len(message)),
            attribute.String("model", t.config.Model),
        )

        // Existing LLM message sending logic
        return t.sendMessageToLLM(ctx, message)
    })
}

// pkg/tools/bash_tool.go - Add tracing to tools execution
package tools

import (
    "context"

    "github.com/jingkaihe/kodelet/pkg/telemetry"
    "go.opentelemetry.io/otel/attribute"
)

func (t *BashTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
    command := params["command"].(string)

    var result interface{}
    err := telemetry.WithSpan(ctx, "tools.bash.execute", func(ctx context.Context) error {
        // Add attributes about the command
        span := trace.SpanFromContext(ctx)
        span.SetAttributes(
            attribute.String("command", command),
            attribute.Int("timeout", t.timeout),
        )

        // Execute the bash command
        var err error
        result, err = t.executeCommand(command)
        return err
    }, attribute.String("tool", "bash"))

    return result, err
}
```

### 7. Instrument Cobra Commands

Implement tracing for the Cobra CLI commands to track command usage patterns:

```go
// cmd/kodelet/chat.go
package main

import (
    "context"

    "github.com/jingkaihe/kodelet/pkg/telemetry"
    "github.com/spf13/cobra"
    "go.opentelemetry.io/otel/attribute"
    "go.opentelemetry.io/otel/trace"
)

func newChatCommand() *cobra.Command {
    var plainMode bool

    cmd := &cobra.Command{
        Use:   "chat",
        Short: "Start interactive chat session",
        RunE: func(cmd *cobra.Command, args []string) error {
            // Create a context with tracing
            ctx := cmd.Context()

            // Create a span for the chat command execution
            return telemetry.WithSpan(ctx, "cmd.chat", func(ctx context.Context) error {
                // Add command-specific attributes
                span := trace.SpanFromContext(ctx)
                span.SetAttributes(
                    attribute.Bool("plain_mode", plainMode),
                    attribute.Int("args_count", len(args)),
                )

                // Execute the chat functionality
                return executeChat(ctx, plainMode, args)
            })
        },
    }

    // Add flags
    cmd.Flags().BoolVar(&plainMode, "plain", false, "Use plain terminal interface")

    return cmd
}
```

### 8. Configure Environment for Grafana Cloud

To send traces to Grafana Cloud, configure the following environment variables:

```bash
# Required for Grafana Cloud
export OTEL_EXPORTER_OTLP_ENDPOINT="https://otlp-gateway-<stack-name>.grafana.net/otlp"
export OTEL_EXPORTER_OTLP_HEADERS="Authorization=Basic <base64-encoded-username:apikey>"

# Optional configuration
export OTEL_SERVICE_NAME="kodelet"
export OTEL_RESOURCE_ATTRIBUTES="deployment.environment=production"
```

### 9. Add Configuration Options

Add tracing configuration to the application config:

```go
// pkg/config/config.go
package config

type TracingConfig struct {
    Enabled bool   `mapstructure:"enabled"`
    Sampler string `mapstructure:"sampler"` // Options: always, never, ratio
    Ratio   float64 `mapstructure:"ratio"`  // Used with ratio sampler
}

type Config struct {
    // Other config
    Tracing TracingConfig `mapstructure:"tracing"`
}
```

## Consequences

### Positive

1. **Improved Observability**: We will gain deeper insights into CLI command execution and LLM interactions.
2. **User Behavior Analytics**: Tracing will help understand how users interact with the Kodelet CLI.
3. **Performance Analysis**: Identify slow operations, LLM response times, and tool execution overhead.
4. **Vendor Neutrality**: OpenTelemetry provides a vendor-neutral solution that can export to different backends.
5. **Better Support**: Tracing data will help reproduce and diagnose user-reported issues.
6. **Standardization**: Using industry-standard observability framework.

### Negative

1. **Increased Complexity**: Adds complexity to the codebase with additional instrumentation.
2. **Runtime Overhead**: Tracing adds some overhead to command execution.
3. **Cost Considerations**: More telemetry data means higher costs for hosted services like Grafana Cloud.
4. **Privacy Concerns**: Need to ensure sensitive user data isn't included in traces.

### Neutral

1. **Configuration Management**: Need to manage configuration for different environments.
2. **Sampling Strategy**: Need to determine appropriate sampling strategy based on traffic volume.

## Implementation Notes

1. Start with basic tracing on critical components:
   - LLM interactions via the Thread abstraction
   - CLI command execution
   - Tool invocations (especially file operations and bash commands)
2. Use sampling in production to control costs (10% sampling is a good starting point).
3. Add the ability to enable/disable tracing via config file and environment variables.
4. Implement a local trace exporter for development (e.g., Jaeger or stdout exporter).
5. Add tracing context to logs to correlate logs with traces.
6. Ensure traces capture:
   - Command execution time
   - LLM response time
   - Token usage
   - Tool execution metrics
7. Be mindful of privacy - avoid capturing sensitive user data in trace attributes.

## References

1. [Grafana Cloud Documentation](https://grafana.com/docs/grafana-cloud/monitor-applications/application-observability/instrument/go/)
2. [OpenTelemetry Go Documentation](https://opentelemetry.io/docs/instrumentation/go/)
3. [OpenTelemetry Best Practices](https://opentelemetry.io/docs/concepts/sdk-configuration/general-sdk-configuration/)
