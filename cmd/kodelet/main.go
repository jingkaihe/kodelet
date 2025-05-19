package main

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func init() {
	// Set default configuration values
	viper.SetDefault("max_tokens", 8192)
	viper.SetDefault("weak_model_max_tokens", 8192)
	viper.SetDefault("thinking_budget_tokens", 4048)
	viper.SetDefault("model", anthropic.ModelClaude3_7SonnetLatest)
	viper.SetDefault("weak_model", anthropic.ModelClaude3_5HaikuLatest)

	// Set default tracing configuration
	viper.SetDefault("tracing.enabled", false)
	viper.SetDefault("tracing.sampler", "ratio")
	viper.SetDefault("tracing.ratio", 1)

	// Environment variables
	viper.SetEnvPrefix("KODELET")
	viper.AutomaticEnv()

	// Support for nested keys in environment variables
	// e.g. KODELET_TRACING_ENABLED -> tracing.enabled
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// Config file support
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("$HOME/.kodelet")
	viper.AddConfigPath(".")

	// Load config file if it exists (ignore errors if it doesn't)
	if err := viper.ReadInConfig(); err == nil {
		logrus.WithField("config_file", viper.ConfigFileUsed()).Debug("Using config file")
	}
}

var rootCmd = &cobra.Command{
	Use:   "kodelet",
	Short: "Kodelet is a CLI tool for software engineering and production operations tasks",
	Long:  `Kodelet is a lightweight CLI tool that helps with software engineering and production operations tasks.`,
	// Default behavior is to show help if no arguments are provided
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) > 0 {
			// If arguments are provided but no subcommand, forward to run command
			runCmd.Run(cmd, args)
		} else {
			cmd.Help()
			os.Exit(1)
		}
	},
}

func main() {
	// Create a context
	ctx := context.Background()

	// Add global flags
	rootCmd.PersistentFlags().String("model", anthropic.ModelClaude3_7SonnetLatest, "Anthropic model to use (overrides config)")
	rootCmd.PersistentFlags().Int("max-tokens", 8192, "Maximum tokens for response (overrides config)")
	rootCmd.PersistentFlags().Int("thinking-budget-tokens", 4048, "Maximum tokens for thinking capability (overrides config)")
	rootCmd.PersistentFlags().String("weak-model", anthropic.ModelClaude3_5HaikuLatest, "Weak model to use (overrides config)")
	rootCmd.PersistentFlags().Int("weak-model-max-tokens", 8192, "Maximum tokens for weak model response (overrides config)")

	// Bind flags to viper
	viper.BindPFlag("model", rootCmd.PersistentFlags().Lookup("model"))
	viper.BindPFlag("max_tokens", rootCmd.PersistentFlags().Lookup("max-tokens"))
	viper.BindPFlag("thinking_budget_tokens", rootCmd.PersistentFlags().Lookup("thinking-budget-tokens"))
	viper.BindPFlag("weak_model", rootCmd.PersistentFlags().Lookup("weak-model"))
	viper.BindPFlag("weak_model_max_tokens", rootCmd.PersistentFlags().Lookup("weak-model-max-tokens"))

	// Add subcommands
	rootCmd.AddCommand(chatCmd)
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(commitCmd)
	rootCmd.AddCommand(watchCmd)
	rootCmd.AddCommand(updateCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(conversationCmd)

	// Initialize telemetry with tracing
	tracingShutdown, err := initTracing(ctx)
	if err != nil {
		logrus.WithField("error", err).Warn("Failed to initialize tracing")
	} else if tracingShutdown != nil {
		// Ensure tracing is properly shutdown
		defer func() {
			if viper.GetBool("tracing.enabled") {
				// best effort to ensure graceful shutdown
				time.Sleep(1 * time.Second)
				if err := tracingShutdown(ctx); err != nil {
					logrus.WithField("error", err).Warn("Failed to shutdown tracing")
				}
			}
		}()
	}

	// Apply tracing to all commands
	rootCmd = withTracing(rootCmd)
	runCmd = withTracing(runCmd)
	chatCmd = withTracing(chatCmd)
	versionCmd = withTracing(versionCmd)
	commitCmd = withTracing(commitCmd)
	watchCmd = withTracing(watchCmd)
	updateCmd = withTracing(updateCmd)
	initCmd = withTracing(initCmd)
	conversationCmd = withTracing(conversationCmd)

	// Set the root command context to include the tracing context
	rootCmd.SetContext(ctx)

	// Execute
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		logrus.WithField("error", err).Error("Failed to execute command")
		os.Exit(1)
	}
}
