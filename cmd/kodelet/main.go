// Package main provides the entry point for the Kodelet CLI application.
package main

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/jingkaihe/kodelet/pkg/binaries"
	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func init() {
	viper.SetDefault("max_tokens", 8192)
	viper.SetDefault("weak_model_max_tokens", 8192)
	viper.SetDefault("thinking_budget_tokens", 4048)
	viper.SetDefault("model", "claude-sonnet-4-6")
	viper.SetDefault("weak_model", anthropic.ModelClaudeHaiku4_5_20251001)
	viper.SetDefault("provider", "anthropic")
	viper.SetDefault("use_copilot", false)
	viper.SetDefault("reasoning_effort", "medium")
	viper.SetDefault("allowed_commands", []string{})
	viper.SetDefault("allowed_domains_file", "~/.kodelet/allowed_domains.txt")
	viper.SetDefault("anthropic_api_access", "auto")

	viper.SetDefault("commit.coauthor.enabled", true)
	viper.SetDefault("commit.coauthor.name", "Kodelet")
	viper.SetDefault("commit.coauthor.email", "noreply@kodelet.com")

	viper.SetDefault("mcp", map[string]tools.MCPConfig{})

	viper.SetDefault("tracing.enabled", false)
	viper.SetDefault("tracing.sampler", "ratio")
	viper.SetDefault("tracing.ratio", 1)

	viper.SetDefault("log_level", "info")
	viper.SetDefault("log_format", "fmt")

	viper.SetEnvPrefix("KODELET")
	viper.AutomaticEnv()

	// Support for nested keys in environment variables
	// e.g. KODELET_TRACING_ENABLED -> tracing.enabled
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// Layered config: global first, then repo-level override
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
	ctx := context.Background()

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

	rootCmd.PersistentFlags().String("provider", "anthropic", "LLM provider to use (anthropic, openai)")
	rootCmd.PersistentFlags().Bool("use-copilot", false, "Use GitHub Copilot subscription for OpenAI requests (env: KODELET_USE_COPILOT)")
	rootCmd.PersistentFlags().String("model", "claude-sonnet-4-6", "LLM model to use (overrides config)")
	rootCmd.PersistentFlags().Int("max-tokens", 8192, "Maximum tokens for response (overrides config)")
	rootCmd.PersistentFlags().Int("thinking-budget-tokens", 4048, "Maximum tokens for thinking capability (overrides config)")
	rootCmd.PersistentFlags().String("weak-model", string(anthropic.ModelClaudeHaiku4_5_20251001), "Weak model to use (overrides config)")
	rootCmd.PersistentFlags().Int("weak-model-max-tokens", 8192, "Maximum tokens for weak model response (overrides config)")
	rootCmd.PersistentFlags().String("reasoning-effort", "medium", "Reasoning effort for OpenAI models (low, medium, high)")
	rootCmd.PersistentFlags().String("log-level", "info", "Log level (panic, fatal, error, warn, info, debug, trace)")
	rootCmd.PersistentFlags().String("log-format", "fmt", "Log format (json, text, fmt)")
	rootCmd.PersistentFlags().StringSlice("allowed-commands", []string{}, "Allowed command patterns for bash tool (e.g. 'yarn start,ls *')")
	rootCmd.PersistentFlags().String("allowed-domains-file", "~/.kodelet/allowed_domains.txt", "Path to file containing allowed domains for web_fetch tool (one domain per line)")
	rootCmd.PersistentFlags().StringSlice("allowed-tools", []string{}, "Comma-separated list of allowed tools for main agent (e.g. 'bash,file_read,grep_tool')")
	rootCmd.PersistentFlags().String("anthropic-api-access", "auto", "Anthropic API access mode (auto, subscription, api-key)")
	rootCmd.PersistentFlags().String("profile", "", "Configuration profile to use (overrides config file)")
	rootCmd.PersistentFlags().Bool("no-skills", false, "Disable agentic skills")
	rootCmd.PersistentFlags().Bool("no-workflows", false, "Disable subagent workflows")
	rootCmd.PersistentFlags().Bool("disable-subagent", false, "Disable the subagent tool and remove subagent-related system prompt context")
	rootCmd.PersistentFlags().StringSlice("context-patterns", []string{"AGENTS.md"}, "Context file patterns to load (e.g. 'AGENTS.md,README.md')")

	viper.BindPFlag("provider", rootCmd.PersistentFlags().Lookup("provider"))
	viper.BindPFlag("use_copilot", rootCmd.PersistentFlags().Lookup("use-copilot"))
	viper.BindPFlag("model", rootCmd.PersistentFlags().Lookup("model"))
	viper.BindPFlag("max_tokens", rootCmd.PersistentFlags().Lookup("max-tokens"))
	viper.BindPFlag("thinking_budget_tokens", rootCmd.PersistentFlags().Lookup("thinking-budget-tokens"))
	viper.BindPFlag("weak_model", rootCmd.PersistentFlags().Lookup("weak-model"))
	viper.BindPFlag("weak_model_max_tokens", rootCmd.PersistentFlags().Lookup("weak-model-max-tokens"))
	viper.BindPFlag("reasoning_effort", rootCmd.PersistentFlags().Lookup("reasoning-effort"))
	viper.BindPFlag("weak_reasoning_effort", rootCmd.PersistentFlags().Lookup("weak-reasoning-effort"))
	viper.BindPFlag("log_level", rootCmd.PersistentFlags().Lookup("log-level"))
	viper.BindPFlag("log_format", rootCmd.PersistentFlags().Lookup("log-format"))
	viper.BindPFlag("allowed_commands", rootCmd.PersistentFlags().Lookup("allowed-commands"))
	viper.BindPFlag("allowed_domains_file", rootCmd.PersistentFlags().Lookup("allowed-domains-file"))
	viper.BindPFlag("allowed_tools", rootCmd.PersistentFlags().Lookup("allowed-tools"))
	viper.BindPFlag("anthropic_api_access", rootCmd.PersistentFlags().Lookup("anthropic-api-access"))
	viper.BindPFlag("profile", rootCmd.PersistentFlags().Lookup("profile"))
	viper.BindPFlag("no_skills", rootCmd.PersistentFlags().Lookup("no-skills"))
	viper.BindPFlag("no_workflows", rootCmd.PersistentFlags().Lookup("no-workflows"))
	viper.BindPFlag("disable_subagent", rootCmd.PersistentFlags().Lookup("disable-subagent"))
	viper.BindPFlag("context.patterns", rootCmd.PersistentFlags().Lookup("context-patterns"))

	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(commitCmd)
	rootCmd.AddCommand(updateCmd)
	rootCmd.AddCommand(setupCmd)
	rootCmd.AddCommand(conversationCmd)
	rootCmd.AddCommand(usageCmd)
	rootCmd.AddCommand(prCmd)
	rootCmd.AddCommand(prRespondCmd)
	rootCmd.AddCommand(issueResolveCmd)
	rootCmd.AddCommand(resolveCmd)
	rootCmd.AddCommand(ghaAgentOnboardCmd)
	rootCmd.AddCommand(anthropicCmd)
	rootCmd.AddCommand(copilotLoginCmd)
	rootCmd.AddCommand(copilotLogoutCmd)
	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(steerCmd)
	rootCmd.AddCommand(recipeCmd)
	rootCmd.AddCommand(profileCmd)
	rootCmd.AddCommand(dbCmd)

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

	// Ensure required external binaries are installed
	binaries.EnsureDepsInstalled(ctx)

	// Run database migrations once at startup (skip for db commands to allow manual control)
	skipMigrations := len(os.Args) > 1 && os.Args[1] == "db"
	if !skipMigrations {
		if err := db.RunMigrations(ctx, migrations.All()); err != nil {
			logger.G(ctx).WithError(err).Fatal("Failed to run database migrations")
		}
	}

	rootCmd = withTracing(rootCmd)
	runCmd = withTracing(runCmd)
	versionCmd = withTracing(versionCmd)
	commitCmd = withTracing(commitCmd)
	updateCmd = withTracing(updateCmd)
	setupCmd = withTracing(setupCmd)
	conversationCmd = withTracing(conversationCmd)
	usageCmd = withTracing(usageCmd)
	prCmd = withTracing(prCmd)
	prRespondCmd = withTracing(prRespondCmd)
	issueResolveCmd = withTracing(issueResolveCmd)
	resolveCmd = withTracing(resolveCmd)
	ghaAgentOnboardCmd = withTracing(ghaAgentOnboardCmd)
	anthropicCmd = withTracing(anthropicCmd)
	copilotLoginCmd = withTracing(copilotLoginCmd)
	copilotLogoutCmd = withTracing(copilotLogoutCmd)
	serveCmd = withTracing(serveCmd)
	steerCmd = withTracing(steerCmd)
	recipeCmd = withTracing(recipeCmd)

	// Set the root command context to include the tracing context
	rootCmd.SetContext(ctx)

	// Execute
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		logger.G(context.TODO()).WithField("error", err).Error("Failed to execute command")
		os.Exit(1)
	}
}
