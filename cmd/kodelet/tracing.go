package main

import (
	"context"
	"strings"

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
		Enabled:          viper.GetBool("tracing.enabled"),
		CaptureContent:   viper.GetBool("tracing.capture_content"),
		InternalRPCSpans: viper.GetBool("tracing.internal_rpc_spans"),
		ServiceName:      "kodelet",
		ServiceVersion:   getVersion(),
		SamplerType:      viper.GetString("tracing.sampler"),
		SamplerRatio:     viper.GetFloat64("tracing.ratio"),
	}

	shutdown, err := telemetry.InitTracer(ctx, config)
	if err != nil {
		return nil, err
	}

	return shutdown, nil
}

var tracer = telemetry.Tracer("kodelet.cli")

func withTracing(cmd *cobra.Command) *cobra.Command {
	originalRun := cmd.Run
	originalRunE := cmd.RunE
	if originalRun == nil && originalRunE == nil {
		return cmd
	}

	run := func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		attrs := []attribute.KeyValue{
			attribute.String("command.name", cmd.Name()),
			attribute.String("command.path", cmd.CommandPath()),
			attribute.Int("args.count", len(args)),
		}

		var flags []string
		cmd.Flags().Visit(func(flag *pflag.Flag) {
			if !isSensitiveFlagName(flag.Name) {
				flags = append(flags, flag.Name)
				if telemetry.ContentEnabled() {
					attrs = append(attrs, attribute.String("flag."+flag.Name, flag.Value.String()))
				}
			}
		})
		attrs = append(attrs, attribute.StringSlice("command.flags", flags))

		ctx, span := tracer.Start(
			ctx,
			"cli.command",
			trace.WithAttributes(attrs...),
		)
		defer span.End()

		cmd.SetContext(ctx)
		if originalRunE != nil {
			if err := originalRunE(cmd, args); err != nil {
				telemetry.RecordSpanError(span, err)
				return err
			}
		} else {
			originalRun(cmd, args)
		}
		span.SetStatus(codes.Ok, "")
		return nil
	}
	if originalRunE != nil {
		cmd.RunE = run
	} else {
		cmd.Run = func(cmd *cobra.Command, args []string) { _ = run(cmd, args) }
	}

	return cmd
}

func isSensitiveFlagName(name string) bool {
	lowerName := strings.ToLower(name)
	return strings.Contains(lowerName, "password") || strings.Contains(lowerName, "token") || strings.Contains(lowerName, "key")
}

func init() {
	rootCmd.PersistentFlags().Bool("tracing-enabled", false, "Enable OpenTelemetry tracing")
	rootCmd.PersistentFlags().Bool("tracing-capture-content", false, "Include prompts, messages, and tool payloads in traces")
	rootCmd.PersistentFlags().Bool("tracing-internal-rpc-spans", false, "Include routine lifecycle and housekeeping RPC spans in traces")
	rootCmd.PersistentFlags().String("tracing-sampler", "ratio", "Tracing sampler type (always, never, ratio)")
	rootCmd.PersistentFlags().Float64("tracing-ratio", 1, "Sampling ratio when using ratio sampler")

	viper.BindPFlag("tracing.enabled", rootCmd.PersistentFlags().Lookup("tracing-enabled"))
	viper.BindPFlag("tracing.capture_content", rootCmd.PersistentFlags().Lookup("tracing-capture-content"))
	viper.BindPFlag("tracing.internal_rpc_spans", rootCmd.PersistentFlags().Lookup("tracing-internal-rpc-spans"))
	viper.BindPFlag("tracing.sampler", rootCmd.PersistentFlags().Lookup("tracing-sampler"))
	viper.BindPFlag("tracing.ratio", rootCmd.PersistentFlags().Lookup("tracing-ratio"))
}
