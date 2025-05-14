package main

import (
	"context"

	"github.com/jingkaihe/kodelet/pkg/telemetry"
	"github.com/jingkaihe/kodelet/pkg/version"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// getVersion returns the current version of the application
func getVersion() string {
	return version.Get().Version
}

// initTracing initializes the OpenTelemetry tracing system
func initTracing(ctx context.Context) (func(context.Context) error, error) {
	// Create a configuration for telemetry
	config := telemetry.Config{
		Enabled:        viper.GetBool("tracing.enabled"),
		ServiceName:    "kodelet",
		ServiceVersion: getVersion(),
		SamplerType:    viper.GetString("tracing.sampler"),
		SamplerRatio:   viper.GetFloat64("tracing.ratio"),
	}

	// Initialize the tracer
	shutdown, err := telemetry.InitTracer(ctx, config)
	if err != nil {
		return nil, err
	}

	return shutdown, nil
}

var (
	tracer = telemetry.Tracer("kodelet.cli")
)

// withTracing wraps a Cobra command with tracing
func withTracing(cmd *cobra.Command) *cobra.Command {
	// Save the original run function
	originalRun := cmd.Run

	// Replace with a traced version
	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()

		// Add command attributes
		attrs := []attribute.KeyValue{
			attribute.String("command.name", cmd.Name()),
			attribute.String("command.path", cmd.CommandPath()),
			attribute.Int("args.count", len(args)),
		}

		// Add flags as attributes
		cmd.Flags().Visit(func(flag *pflag.Flag) {
			// Skip sensitive flags that might contain secrets
			if flag.Name != "password" && flag.Name != "token" && flag.Name != "key" {
				attrs = append(attrs, attribute.String("flag."+flag.Name, flag.Value.String()))
			}
		})

		ctx, span := tracer.Start(
			ctx,
			"cli.command",
			trace.WithAttributes(attrs...),
		)
		defer span.End()

		// Set the context for the command
		cmd.SetContext(ctx)

		// Run the original command with the updated context
		originalRun(cmd, args)

		// Mark the span as successful
		span.SetStatus(codes.Ok, "")
	}

	return cmd
}

// Initialize global flags for tracing
func init() {
	// Add tracing flags
	rootCmd.PersistentFlags().Bool("tracing-enabled", false, "Enable OpenTelemetry tracing")
	rootCmd.PersistentFlags().String("tracing-sampler", "ratio", "Tracing sampler type (always, never, ratio)")
	rootCmd.PersistentFlags().Float64("tracing-ratio", 1, "Sampling ratio when using ratio sampler")

	// Bind flags to viper
	viper.BindPFlag("tracing.enabled", rootCmd.PersistentFlags().Lookup("tracing-enabled"))
	viper.BindPFlag("tracing.sampler", rootCmd.PersistentFlags().Lookup("tracing-sampler"))
	viper.BindPFlag("tracing.ratio", rootCmd.PersistentFlags().Lookup("tracing-ratio"))
}
