// Package main provides the entry point for the Kodelet CLI application.
package main

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/binaries"
	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/logger"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

const (
	configFileEnv            = llm.ConfigFileEnv
	configFileModeEnv        = llm.ConfigFileModeEnv
	controlPlaneServerEnv    = "KODELET_SERVER"
	controlPlaneAuthTokenEnv = "KODELET_AUTH_TOKEN"
	runnerAuthTokenEnv       = "KODELET_RUNNER_AUTH_TOKEN"
	configFileModeMerge      = llm.ConfigFileModeMerge
	configFileModeIsolate    = llm.ConfigFileModeIsolated
)

var configFileLoadError error

func authTokenFlagOrEnvironment(cmd *cobra.Command, environmentName string) string {
	return stringFlagOrEnvironment(cmd, "auth-token", environmentName)
}

func stringFlagOrEnvironment(cmd *cobra.Command, flagName, environmentName string) string {
	value, err := cmd.Flags().GetString(flagName)
	if err != nil {
		return ""
	}
	if cmd.Flags().Changed(flagName) {
		return strings.TrimSpace(value)
	}
	return strings.TrimSpace(os.Getenv(environmentName))
}

func serverFlagOrConfig(cmd *cobra.Command) (string, bool) {
	value, err := cmd.Flags().GetString("server")
	if err != nil {
		return "", false
	}
	if cmd.Flags().Changed("server") {
		return strings.TrimSpace(value), true
	}
	if environment := strings.TrimSpace(os.Getenv(controlPlaneServerEnv)); environment != "" {
		return environment, true
	}
	if configured := strings.TrimSpace(viper.GetString("server")); configured != "" {
		return configured, true
	}
	return strings.TrimSpace(value), false
}

func init() {
	viper.SetDefault("max_tokens", 8192)
	viper.SetDefault("weak_model_max_tokens", 8192)
	viper.SetDefault("thinking_budget_tokens", 4048)
	viper.SetDefault("model", "gpt-5.5")
	viper.SetDefault("weak_model", "gpt-5.4-mini")
	viper.SetDefault("provider", "openai")
	viper.SetDefault("openai.api_mode", "responses")
	// Keep the configured value empty so request construction can distinguish an
	// explicit opt-in from the upstream API default.
	viper.SetDefault("openai.text_verbosity", "")
	viper.SetDefault("openai.enable_search", true)
	viper.SetDefault("openai.websocket_mode", true)
	viper.SetDefault("reasoning_effort", "medium")
	viper.SetDefault("allowed_reasoning_efforts", []string{})
	viper.SetDefault("allowed_commands", []string{})
	viper.SetDefault("bash.timeout", "120s")
	viper.SetDefault("allowed_domains_file", "~/.kodelet/allowed_domains.txt")
	viper.SetDefault("sysprompt", "")
	viper.SetDefault("sysprompt_args", map[string]string{})
	viper.SetDefault("tool_mode", "patch")
	viper.SetDefault("enable_fs_search_tools", false)
	viper.SetDefault("anthropic_api_access", "auto")
	viper.SetDefault("compact_ratio", llmtypes.DefaultCompactRatio)

	viper.SetDefault("extensions.enabled", true)
	viper.SetDefault("extensions.global_dir", "~/.kodelet/extensions")
	viper.SetDefault("extensions.local_dir", "./.kodelet/extensions")
	viper.SetDefault("extensions.max_output_size", 102400)

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

	configFileLoadError = loadConfigFiles()
}

func loadConfigFiles() error {
	overrideConfigFile := strings.TrimSpace(os.Getenv(configFileEnv))
	mode, err := configFileMode()
	if err != nil {
		return err
	}
	if overrideConfigFile != "" && mode == configFileModeIsolate {
		return readConfigFile(overrideConfigFile, "isolated override")
	}

	// Layered config: global first, then repo-level override
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("$HOME/.kodelet")

	if err := viper.ReadInConfig(); err == nil {
		if err := validateTrustedConfigPermissions(viper.ConfigFileUsed()); err != nil {
			return err
		}
		logger.G(context.TODO()).WithField("config_file", viper.ConfigFileUsed()).Debug("Using global config file")
	}

	// Then, try to merge repo-level config which will override global settings
	if _, err := os.Stat("kodelet-config.yaml"); err == nil {
		mergeRepositoryConfigFile("kodelet-config.yaml")
	}

	if overrideConfigFile != "" {
		return mergeConfigFile(overrideConfigFile, "override")
	}
	return nil
}

func configFileMode() (string, error) {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv(configFileModeEnv)))
	switch mode {
	case "", configFileModeMerge:
		return configFileModeMerge, nil
	case configFileModeIsolate:
		return configFileModeIsolate, nil
	default:
		return "", errors.Errorf("invalid %s value %q: must be %q or %q", configFileModeEnv, mode, configFileModeMerge, configFileModeIsolate)
	}
}

func readConfigFile(configFile, label string) error {
	viper.SetConfigFile(configFile)
	if err := viper.ReadInConfig(); err == nil {
		if err := validateTrustedConfigPermissions(configFile); err != nil {
			return err
		}
		logger.G(context.TODO()).WithField("config_file", configFile).Debugf("Read %s config file", label)
	} else {
		return errors.Wrapf(err, "failed to read %s config file %q", label, configFile)
	}
	return nil
}

func mergeConfigFile(configFile, label string) error {
	viper.SetConfigFile(configFile)
	if err := viper.MergeInConfig(); err == nil {
		if err := validateTrustedConfigPermissions(configFile); err != nil {
			return err
		}
		logger.G(context.TODO()).WithField("config_file", configFile).Debugf("Merged %s config file", label)
	} else {
		return errors.Wrapf(err, "failed to merge %s config file %q", label, configFile)
	}
	return nil
}

func validateTrustedConfigPermissions(configFile string) error {
	contents, err := os.ReadFile(configFile)
	if err != nil {
		return errors.Wrapf(err, "failed to inspect trusted config file %q", configFile)
	}
	var settings map[string]any
	if err := yaml.Unmarshal(contents, &settings); err != nil {
		return errors.Wrapf(err, "failed to inspect trusted config file %q", configFile)
	}
	containsControlPlaneSettings := trustedConfigContainsControlPlaneSettings(settings)
	containsStaticTokens := trustedConfigContainsStaticTokens(settings)
	if !containsControlPlaneSettings {
		return nil
	}
	info, err := os.Stat(configFile)
	if err != nil {
		return errors.Wrapf(err, "failed to inspect trusted config file %q", configFile)
	}
	if !info.Mode().IsRegular() {
		return errors.Errorf("trusted control-plane config file %q must be a regular file", configFile)
	}
	if containsStaticTokens && info.Mode().Perm()&0o077 != 0 {
		return errors.Errorf("trusted config file %q containing static authentication tokens must not be accessible by group or other users", configFile)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return errors.Errorf("trusted control-plane config file %q must not be writable by group or other users", configFile)
	}
	return nil
}

func trustedConfigContainsControlPlaneSettings(settings map[string]any) bool {
	for key := range settings {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if normalized == "server" || normalized == "serve" || strings.HasPrefix(normalized, "serve.") {
			return true
		}
	}
	return false
}

func trustedConfigContainsStaticTokens(settings map[string]any) bool {
	for key, value := range settings {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "serve.auth_token", "serve.runner_auth_token":
			if configSecretPresent(value) {
				return true
			}
		case "serve":
			nested, ok := value.(map[string]any)
			if !ok {
				continue
			}
			for nestedKey, nestedValue := range nested {
				switch strings.ToLower(strings.TrimSpace(nestedKey)) {
				case "auth_token", "runner_auth_token":
					if configSecretPresent(nestedValue) {
						return true
					}
				}
			}
		}
	}
	return false
}

func configSecretPresent(value any) bool {
	secret, ok := value.(string)
	return ok && strings.TrimSpace(secret) != ""
}

func mergeRepositoryConfigFile(configFile string) {
	data, err := os.ReadFile(configFile)
	if err != nil {
		logger.G(context.TODO()).WithField("config_file", configFile).WithError(err).Warn("Failed to read repo-level config file")
		return
	}

	var settings map[string]any
	if err := yaml.Unmarshal(data, &settings); err != nil {
		logger.G(context.TODO()).WithField("config_file", configFile).WithError(err).Warn("Failed to parse repo-level config file")
		return
	}
	for key := range settings {
		normalizedKey := strings.ToLower(strings.TrimSpace(key))
		if normalizedKey == "server" || normalizedKey == "serve" || strings.HasPrefix(normalizedKey, "serve.") {
			delete(settings, key)
		}
	}
	if err := viper.MergeConfigMap(settings); err != nil {
		logger.G(context.TODO()).WithField("config_file", configFile).WithError(err).Warn("Failed to merge repo-level config file")
		return
	}
	logger.G(context.TODO()).WithField("config_file", configFile).Debug("Merged repo-level config file")
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

func executeCLICommand(ctx context.Context, root *cobra.Command) error {
	// Cobra adds these commands lazily during Execute. Initialize them before
	// wrapping the tree so their execution errors follow the same usage policy.
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()

	var restore []func()
	wrapExecutionHook := func(hook func(*cobra.Command, []string) error) func(*cobra.Command, []string) error {
		if hook == nil {
			return nil
		}
		return func(cmd *cobra.Command, args []string) error {
			err := hook(cmd, args)
			if err != nil {
				// Usage helps with command discovery, flags, and argument shape. Once
				// execution has begun, it obscures the actionable failure instead.
				cmd.SilenceUsage = true
			}
			return err
		}
	}
	var configure func(*cobra.Command)
	configure = func(command *cobra.Command) {
		originalPersistentPreRunE := command.PersistentPreRunE
		originalPreRunE := command.PreRunE
		originalRunE := command.RunE
		originalPostRunE := command.PostRunE
		originalPersistentPostRunE := command.PersistentPostRunE
		originalSilenceUsage := command.SilenceUsage
		command.PersistentPreRunE = wrapExecutionHook(originalPersistentPreRunE)
		command.PreRunE = wrapExecutionHook(originalPreRunE)
		command.RunE = wrapExecutionHook(originalRunE)
		command.PostRunE = wrapExecutionHook(originalPostRunE)
		command.PersistentPostRunE = wrapExecutionHook(originalPersistentPostRunE)
		restore = append(restore, func() {
			command.PersistentPreRunE = originalPersistentPreRunE
			command.PreRunE = originalPreRunE
			command.RunE = originalRunE
			command.PostRunE = originalPostRunE
			command.PersistentPostRunE = originalPersistentPostRunE
			command.SilenceUsage = originalSilenceUsage
		})
		for _, child := range command.Commands() {
			configure(child)
		}
	}
	configure(root)
	defer func() {
		for i := len(restore) - 1; i >= 0; i-- {
			restore[i]()
		}
	}()
	return root.ExecuteContext(ctx)
}

func main() {
	ctx := context.Background()
	if configFileLoadError != nil {
		logger.G(ctx).WithError(configFileLoadError).Fatal("Failed to load trusted configuration")
	}

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

	rootCmd.PersistentFlags().String("provider", "openai", "LLM provider to use (anthropic, openai)")
	rootCmd.PersistentFlags().String("model", "gpt-5.5", "LLM model to use (overrides config)")
	rootCmd.PersistentFlags().Int("max-tokens", 8192, "Maximum tokens for response (overrides config)")
	rootCmd.PersistentFlags().Int("thinking-budget-tokens", 4048, "Thinking budget for non-adaptive Claude models; adaptive Claude models ignore this and use reasoning-effort instead (overrides config)")
	rootCmd.PersistentFlags().String("weak-model", "gpt-5.4-mini", "Weak model to use (overrides config)")
	rootCmd.PersistentFlags().Int("weak-model-max-tokens", 8192, "Maximum tokens for weak model response (overrides config)")
	rootCmd.PersistentFlags().String("reasoning-effort", "medium", "Reasoning effort for supported models (provider-specific; e.g. OpenAI none|minimal|low|medium|high|xhigh|max; Anthropic adaptive none|low|medium|high|xhigh|max)")
	rootCmd.PersistentFlags().String("log-level", "info", "Log level (panic, fatal, error, warn, info, debug, trace)")
	rootCmd.PersistentFlags().String("log-format", "fmt", "Log format (json, text, fmt)")
	rootCmd.PersistentFlags().StringSlice("allowed-commands", []string{}, "Allowed command patterns for bash tool (e.g. 'yarn start,ls *')")
	rootCmd.PersistentFlags().String("allowed-domains-file", "~/.kodelet/allowed_domains.txt", "Path to file containing allowed domains for web_fetch tool (one domain per line)")
	rootCmd.PersistentFlags().Bool("enable-openai-search", true, "Enable native OpenAI Responses web_search tool when supported")
	rootCmd.PersistentFlags().String("sysprompt", "", "Path to custom system prompt template file")
	rootCmd.PersistentFlags().StringToString("sysprompt-arg", map[string]string{}, "Arguments passed to custom system prompt template (e.g. --sysprompt-arg project=kodelet)")
	rootCmd.PersistentFlags().StringSlice("allowed-tools", []string{}, "Comma-separated list of allowed tools for main agent (e.g. 'bash,file_read,grep_tool')")
	rootCmd.PersistentFlags().String("tool-mode", "full", "Tool interaction mode (full, patch)")
	rootCmd.PersistentFlags().String("anthropic-api-access", "auto", "Anthropic API access mode (auto, subscription, api-key)")
	rootCmd.PersistentFlags().String("profile", "", "Configuration profile to use (overrides config file)")
	rootCmd.PersistentFlags().Bool("no-skills", false, "Disable agentic skills")
	rootCmd.PersistentFlags().Bool("enable-fs-search-tools", false, "Enable filesystem search tools (glob_tool and grep_tool)")
	rootCmd.PersistentFlags().StringSlice("context-patterns", []string{"AGENTS.md"}, "Context file patterns to load (e.g. 'AGENTS.md,README.md')")
	rootCmd.PersistentFlags().Float64("compact-ratio", llmtypes.DefaultCompactRatio, "Context window utilization ratio to trigger auto-compact (>0.0-1.0)")

	viper.BindPFlag("provider", rootCmd.PersistentFlags().Lookup("provider"))
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
	viper.BindPFlag("openai.enable_search", rootCmd.PersistentFlags().Lookup("enable-openai-search"))
	viper.BindPFlag("sysprompt", rootCmd.PersistentFlags().Lookup("sysprompt"))
	viper.BindPFlag("sysprompt_args", rootCmd.PersistentFlags().Lookup("sysprompt-arg"))
	viper.BindPFlag("allowed_tools", rootCmd.PersistentFlags().Lookup("allowed-tools"))
	viper.BindPFlag("tool_mode", rootCmd.PersistentFlags().Lookup("tool-mode"))
	viper.BindPFlag("anthropic_api_access", rootCmd.PersistentFlags().Lookup("anthropic-api-access"))
	viper.BindPFlag("profile", rootCmd.PersistentFlags().Lookup("profile"))
	viper.BindPFlag("no_skills", rootCmd.PersistentFlags().Lookup("no-skills"))
	viper.BindPFlag("enable_fs_search_tools", rootCmd.PersistentFlags().Lookup("enable-fs-search-tools"))
	viper.BindPFlag("context.patterns", rootCmd.PersistentFlags().Lookup("context-patterns"))
	viper.BindPFlag("compact_ratio", rootCmd.PersistentFlags().Lookup("compact-ratio"))

	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(chatCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(commitCmd)
	rootCmd.AddCommand(setupCmd)
	rootCmd.AddCommand(conversationCmd)
	rootCmd.AddCommand(usageCmd)
	rootCmd.AddCommand(prCmd)
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
	chatCmd = withTracing(chatCmd)
	versionCmd = withTracing(versionCmd)
	commitCmd = withTracing(commitCmd)
	setupCmd = withTracing(setupCmd)
	conversationCmd = withTracing(conversationCmd)
	usageCmd = withTracing(usageCmd)
	prCmd = withTracing(prCmd)
	anthropicCmd = withTracing(anthropicCmd)
	copilotLoginCmd = withTracing(copilotLoginCmd)
	copilotLogoutCmd = withTracing(copilotLogoutCmd)
	serveCmd = withTracing(serveCmd)
	steerCmd = withTracing(steerCmd)
	recipeCmd = withTracing(recipeCmd)

	// Set the root command context to include the tracing context
	rootCmd.SetContext(ctx)

	// Execute
	if err := executeCLICommand(ctx, rootCmd); err != nil {
		os.Exit(1)
	}
}
