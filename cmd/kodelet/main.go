// Package main provides the entry point for the Kodelet CLI application.
// It initializes configuration, sets up command structure with Cobra,
// and manages application lifecycle including tracing and error handling.
package main

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func init() {
	// Set default configuration values
	viper.SetDefault("max_tokens", 8192)
	viper.SetDefault("weak_model_max_tokens", 8192)
	viper.SetDefault("thinking_budget_tokens", 4048)
	viper.SetDefault("model", anthropic.ModelClaudeSonnet4_20250514)
	viper.SetDefault("weak_model", anthropic.ModelClaude3_5Haiku20241022)
	viper.SetDefault("provider", "anthropic")
	viper.SetDefault("use_copilot", false)
	viper.SetDefault("reasoning_effort", "medium")
	viper.SetDefault("cache_every", 10)
	viper.SetDefault("allowed_commands", []string{})
	viper.SetDefault("allowed_domains_file", "~/.kodelet/allowed_domains.txt")
	viper.SetDefault("anthropic_api_access", "auto")

	// Commit coauthor configuration defaults
	viper.SetDefault("commit.coauthor.enabled", true)
	viper.SetDefault("commit.coauthor.name", "Kodelet")
	viper.SetDefault("commit.coauthor.email", "noreply@kodelet.com")

	// Set default MCP configuration
	viper.SetDefault("mcp", map[string]tools.MCPConfig{})

	// Set default tracing configuration
	viper.SetDefault("tracing.enabled", false)
	viper.SetDefault("tracing.sampler", "ratio")
	viper.SetDefault("tracing.ratio", 1)

	// Set default logging configuration
	viper.SetDefault("log_level", "info")
	viper.SetDefault("log_format", "fmt")

	// Environment variables
	viper.SetEnvPrefix("KODELET")
	viper.AutomaticEnv()

	// Support for nested keys in environment variables
	// e.g. KODELET_TRACING_ENABLED -> tracing.enabled
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// Config file support - layered approach (global first, then repo-level override)
	// First, try to load global config
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("$HOME/.kodelet")

	if err := viper.ReadInConfig(); err == nil {
		logger.G(context.TODO()).WithField("config_file", viper.ConfigFileUsed()).Debug("Using global config file")
	}

	// Then, try to merge repo-level config which will override global settings
	if _, err := os.Stat("kodelet-config.yaml"); err == nil {
		viper.SetConfigFile("kodelet-config.yaml")
		if err := viper.MergeInConfig(); err == nil {
			logger.G(context.TODO()).WithField("config_file", "kodelet-config.yaml").Debug("Merged repo-level config file")
		}
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

	// Initialize log level and format from configuration after CLI parsing
	cobra.OnInitialize(func() {
		if logLevel := viper.GetString("log_level"); logLevel != "" {
			if err := logger.SetLogLevel(logLevel); err != nil {
				logger.G(context.TODO()).WithField("error", err).WithField("log_level", logLevel).Warn("Invalid log level, using default")
			}
		}
		if logFormat := viper.GetString("log_format"); logFormat != "" {
			logger.SetLogFormat(logFormat)
		}
	})

	// Add global flags
	rootCmd.PersistentFlags().String("provider", "anthropic", "LLM provider to use (anthropic, openai)")
	rootCmd.PersistentFlags().Bool("use-copilot", false, "Use GitHub Copilot subscription for OpenAI requests (env: KODELET_USE_COPILOT)")
	rootCmd.PersistentFlags().String("model", string(anthropic.ModelClaudeSonnet4_20250514), "LLM model to use (overrides config)")
	rootCmd.PersistentFlags().Int("max-tokens", 8192, "Maximum tokens for response (overrides config)")
	rootCmd.PersistentFlags().Int("thinking-budget-tokens", 4048, "Maximum tokens for thinking capability (overrides config)")
	rootCmd.PersistentFlags().String("weak-model", string(anthropic.ModelClaude3_5Haiku20241022), "Weak model to use (overrides config)")
	rootCmd.PersistentFlags().Int("weak-model-max-tokens", 8192, "Maximum tokens for weak model response (overrides config)")
	rootCmd.PersistentFlags().String("reasoning-effort", "medium", "Reasoning effort for OpenAI models (low, medium, high)")
	rootCmd.PersistentFlags().Int("cache-every", 10, "Cache messages every N interactions (0 to disable, Anthropic only)")
	rootCmd.PersistentFlags().String("log-level", "info", "Log level (panic, fatal, error, warn, info, debug, trace)")
	rootCmd.PersistentFlags().String("log-format", "fmt", "Log format (json, text, fmt)")
	rootCmd.PersistentFlags().StringSlice("allowed-commands", []string{}, "Allowed command patterns for bash tool (e.g. 'yarn start,ls *')")
	rootCmd.PersistentFlags().String("allowed-domains-file", "~/.kodelet/allowed_domains.txt", "Path to file containing allowed domains for web_fetch and browser tools (one domain per line)")
	rootCmd.PersistentFlags().String("anthropic-api-access", "auto", "Anthropic API access mode (auto, subscription, api-key)")

	// Bind flags to viper
	viper.BindPFlag("provider", rootCmd.PersistentFlags().Lookup("provider"))
	viper.BindPFlag("use_copilot", rootCmd.PersistentFlags().Lookup("use-copilot"))
	viper.BindPFlag("model", rootCmd.PersistentFlags().Lookup("model"))
	viper.BindPFlag("max_tokens", rootCmd.PersistentFlags().Lookup("max-tokens"))
	viper.BindPFlag("thinking_budget_tokens", rootCmd.PersistentFlags().Lookup("thinking-budget-tokens"))
	viper.BindPFlag("weak_model", rootCmd.PersistentFlags().Lookup("weak-model"))
	viper.BindPFlag("weak_model_max_tokens", rootCmd.PersistentFlags().Lookup("weak-model-max-tokens"))
	viper.BindPFlag("reasoning_effort", rootCmd.PersistentFlags().Lookup("reasoning-effort"))
	viper.BindPFlag("weak_reasoning_effort", rootCmd.PersistentFlags().Lookup("weak-reasoning-effort"))
	viper.BindPFlag("cache_every", rootCmd.PersistentFlags().Lookup("cache-every"))
	viper.BindPFlag("log_level", rootCmd.PersistentFlags().Lookup("log-level"))
	viper.BindPFlag("log_format", rootCmd.PersistentFlags().Lookup("log-format"))
	viper.BindPFlag("allowed_commands", rootCmd.PersistentFlags().Lookup("allowed-commands"))
	viper.BindPFlag("allowed_domains_file", rootCmd.PersistentFlags().Lookup("allowed-domains-file"))
	viper.BindPFlag("anthropic_api_access", rootCmd.PersistentFlags().Lookup("anthropic-api-access"))

	// Add subcommands
	rootCmd.AddCommand(chatCmd)
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(commitCmd)
	rootCmd.AddCommand(watchCmd)
	rootCmd.AddCommand(updateCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(conversationCmd)
	rootCmd.AddCommand(usageCmd)
	rootCmd.AddCommand(prCmd)
	rootCmd.AddCommand(prRespondCmd)
	rootCmd.AddCommand(issueResolveCmd)
	rootCmd.AddCommand(resolveCmd)
	rootCmd.AddCommand(ghaAgentOnboardCmd)
	rootCmd.AddCommand(anthropicLoginCmd)
	rootCmd.AddCommand(anthropicLogoutCmd)
	rootCmd.AddCommand(copilotLoginCmd)
	rootCmd.AddCommand(copilotLogoutCmd)
	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(feedbackCmd)
	rootCmd.AddCommand(recipeCmd)

	// Initialize telemetry with tracing
	tracingShutdown, err := initTracing(ctx)
	if err != nil {
		logger.G(context.TODO()).WithField("error", err).Warn("Failed to initialize tracing")
	} else if tracingShutdown != nil {
		// Ensure tracing is properly shutdown
		defer func() {
			if viper.GetBool("tracing.enabled") {
				// best effort to ensure graceful shutdown
				time.Sleep(1 * time.Second)
				if err := tracingShutdown(ctx); err != nil {
					logger.G(context.TODO()).WithField("error", err).Warn("Failed to shutdown tracing")
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
	usageCmd = withTracing(usageCmd)
	prCmd = withTracing(prCmd)
	prRespondCmd = withTracing(prRespondCmd)
	issueResolveCmd = withTracing(issueResolveCmd)
	resolveCmd = withTracing(resolveCmd)
	ghaAgentOnboardCmd = withTracing(ghaAgentOnboardCmd)
	anthropicLoginCmd = withTracing(anthropicLoginCmd)
	anthropicLogoutCmd = withTracing(anthropicLogoutCmd)
	copilotLoginCmd = withTracing(copilotLoginCmd)
	copilotLogoutCmd = withTracing(copilotLogoutCmd)
	serveCmd = withTracing(serveCmd)
	feedbackCmd = withTracing(feedbackCmd)
	recipeCmd = withTracing(recipeCmd)

	// Set the root command context to include the tracing context
	rootCmd.SetContext(ctx)

	// Execute
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		logger.G(context.TODO()).WithField("error", err).Error("Failed to execute command")
		os.Exit(1)
	}
}
