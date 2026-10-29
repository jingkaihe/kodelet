package main

import (
	"context"

	"github.com/jingkaihe/kodelet/pkg/telemetry"
	"github.com/spf13/viper"
)

func initMetrics(ctx context.Context) (func(context.Context) error, error) {
	return telemetry.InitMetrics(ctx, telemetry.Config{
		Enabled:        viper.GetBool("metrics.enabled"),
		ServiceName:    "kodelet",
		ServiceVersion: getVersion(),
	})
}

func init() {
	rootCmd.PersistentFlags().Bool("metrics-enabled", false,
		"Enable OpenTelemetry metrics over OTLP/HTTP")
	viper.BindPFlag("metrics.enabled", rootCmd.PersistentFlags().Lookup("metrics-enabled"))
}
