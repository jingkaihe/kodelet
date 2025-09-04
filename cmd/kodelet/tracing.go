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

func getVersion() string {
	return version.Get().Version
}

func initTracing(ctx context.Context) (func(context.Context) error, error) {
	config := telemetry.Config{
		Enabled:        viper.GetBool("tracing.enabled"),
		ServiceName:    "kodelet",
		ServiceVersion: getVersion(),
		SamplerType:    viper.GetString("tracing.sampler"),
		SamplerRatio:   viper.GetFloat64("tracing.ratio"),
	}

	shutdown, err := telemetry.InitTracer(ctx, config)
	if err != nil {
		return nil, err
	}

	return shutdown, nil
}

var (
	tracer = telemetry.Tracer("kodelet.cli")
)

func withTracing(cmd *cobra.Command) *cobra.Command {
	originalRun := cmd.Run

	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()

		attrs := []attribute.KeyValue{
			attribute.String("command.name", cmd.Name()),
			attribute.String("command.path", cmd.CommandPath()),
			attribute.Int("args.count", len(args)),
		}

		cmd.Flags().Visit(func(flag *pflag.Flag) {
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

		cmd.SetContext(ctx)
		originalRun(cmd, args)
		span.SetStatus(codes.Ok, "")
	}

	return cmd
}

func init() {
	rootCmd.PersistentFlags().Bool("tracing-enabled", false, "Enable OpenTelemetry tracing")
	rootCmd.PersistentFlags().String("tracing-sampler", "ratio", "Tracing sampler type (always, never, ratio)")
	rootCmd.PersistentFlags().Float64("tracing-ratio", 1, "Sampling ratio when using ratio sampler")

	viper.BindPFlag("tracing.enabled", rootCmd.PersistentFlags().Lookup("tracing-enabled"))
	viper.BindPFlag("tracing.sampler", rootCmd.PersistentFlags().Lookup("tracing-sampler"))
	viper.BindPFlag("tracing.ratio", rootCmd.PersistentFlags().Lookup("tracing-ratio"))
}
