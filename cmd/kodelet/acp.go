package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jingkaihe/kodelet/pkg/acp"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/logger"
	runnerclient "github.com/jingkaihe/kodelet/pkg/runner/client"
	"github.com/jingkaihe/kodelet/pkg/runner/localstate"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type acpServerLifecycle interface {
	Run() error
	Shutdown()
}

var acpCmd = &cobra.Command{
	Use:   "acp",
	Short: "Run kodelet as an ACP agent",
	Long: `Run kodelet as an Agent Client Protocol (ACP) agent.

This mode allows kodelet to be embedded in ACP-compatible clients like
Zed, JetBrains IDEs, or any other ACP client. Communication happens
over stdio using JSON-RPC 2.0.

Example:
  # Launch as subprocess from an IDE
  kodelet acp

  # With custom model
  kodelet acp --model claude-sonnet-4-6

	# Disable skills
	kodelet acp --no-skills

	# Disable extensions
	kodelet acp --no-extensions

	# Keep workspace tools, skills, and extensions local while the control plane owns the agent loop
	kodelet acp --server https://kodelet.example`,
	RunE: runACP,
}

func init() {
	rootCmd.AddCommand(acpCmd)

	defaults := NewRunConfig()
	acpCmd.Flags().String("model", "", "LLM model to use")
	acpCmd.Flags().String("provider", "", "LLM provider (anthropic, openai)")
	acpCmd.Flags().Int("max-tokens", 0, "Maximum tokens for LLM responses")
	acpCmd.Flags().Bool("no-skills", defaults.NoSkills, "Disable agentic skills")
	acpCmd.Flags().Bool("no-extensions", defaults.NoExtensions, "Disable extension runtime")
	acpCmd.Flags().Bool("enable-fs-search-tools", defaults.EnableFSSearchTools, "Enable filesystem search tools (glob_tool and grep_tool)")
	acpCmd.Flags().Int("max-turns", defaults.MaxTurns, "Maximum number of agentic turns (0 for no limit)")
	acpCmd.Flags().String("server", defaultRunnerServer, "Run the agentic loop on a control plane while keeping the workspace environment local")
	acpCmd.Flags().String("auth-token", "", "Control-plane API authentication token (or KODELET_AUTH_TOKEN)")
	acpCmd.Flags().String("runner-auth-token", "", "Runner-only authentication token (or KODELET_RUNNER_AUTH_TOKEN)")
	acpCmd.Flags().String("runner-profile", "", "Local environment profile for the selected workspace runner")
}

func runACP(cmd *cobra.Command, _ []string) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.SetLogOutput(os.Stderr)
	logger.SetLogLevel(viper.GetString("log_level"))
	if serverURL, configured := serverFlagOrConfig(cmd); configured {
		return runRemoteACP(ctx, cmd, serverURL)
	}

	config, err := buildACPServerConfig(cmd)
	if err != nil {
		return err
	}

	server := acp.NewServer(
		acp.WithConfig(config),
		acp.WithContext(ctx),
	)

	return runACPServer(ctx, server)
}

func runRemoteACP(ctx context.Context, cmd *cobra.Command, rawServerURL string) error {
	if err := validateRemoteACPFlags(cmd); err != nil {
		return err
	}
	workspace, err := conversations.CurrentWorkingDirectory()
	if err != nil {
		return err
	}
	serverURL, err := normalizeRunnerAPIBaseURL(rawServerURL)
	if err != nil {
		return err
	}
	if err := os.Setenv(controlPlaneServerEnv, serverURL); err != nil {
		return errors.Wrap(err, "failed to expose the control-plane server to ACP subprocesses")
	}
	apiAuthToken, runnerAuthToken, err := resolveRemoteACPAuthTokens(cmd, serverURL)
	if err != nil {
		return err
	}
	runnerProfile, _ := cmd.Flags().GetString("runner-profile")

	store, err := localstate.NewStore()
	if err != nil {
		return errors.Wrap(err, "failed to initialize runner state")
	}
	provider := newEmbeddedACPRemoteProvider(serverURL, apiAuthToken, workspace, store)
	runner, err := runnerclient.NewRunner(ctx, runnerclient.RunnerConfig{
		Server:         serverURL,
		AuthToken:      runnerAuthToken,
		Workspace:      workspace,
		Store:          store,
		ServiceOptions: remoteACPServiceOptions(cmd),
		OnRegistered:   provider.registeredRunner,
		OnRetry: func(connectionErr error, delay time.Duration) {
			provider.unavailable()
			logger.G(ctx).WithError(connectionErr).WithField("retry_in", delay).Warn("Embedded ACP runner connection lost")
		},
	})
	if err != nil {
		return errors.Wrap(err, "failed to initialize embedded ACP runner")
	}
	defer func() {
		if closeErr := runner.Close(); closeErr != nil {
			logger.G(ctx).WithError(closeErr).Warn("Failed to close ACP workspace runner")
		}
	}()
	ownsWorkspaceLock, existingRunnerID, err := acquireOrReuseACPRunner(ctx, runner, serverURL)
	if err != nil {
		return errors.Wrap(err, "failed to acquire or reuse ACP workspace runner")
	}
	if !ownsWorkspaceLock {
		if err := validateReusedACPFlags(cmd); err != nil {
			return err
		}
		provider.reuseRunner(existingRunnerID)
		if existingRunnerID == "" {
			logger.G(ctx).Info("Waiting for the existing workspace runner to advertise its control-plane registration")
		} else {
			logger.G(ctx).WithField("runner_id", existingRunnerID).Info("Reusing existing workspace runner for server-backed ACP")
		}
	}

	profile := ""
	if cmd.Flags().Changed("profile") {
		profile, _ = cmd.Flags().GetString("profile")
	}
	reasoningEffort := ""
	if cmd.Flags().Changed("reasoning-effort") {
		reasoningEffort, _ = cmd.Flags().GetString("reasoning-effort")
	}
	server := acp.NewServer(
		acp.WithContext(ctx),
		acp.WithRemoteSessions(acp.RemoteSessionConfig{
			Provider:                   provider,
			CommandSource:              remoteACPCommandSource(runner, ownsWorkspaceLock),
			Workspace:                  workspace,
			Profile:                    profile,
			ReasoningEffort:            reasoningEffort,
			EnvironmentProfile:         runnerProfile,
			EnvironmentProfileExplicit: cmd.Flags().Changed("runner-profile"),
		}),
	)
	if ownsWorkspaceLock {
		return runACPServerWithEmbeddedRunner(ctx, server, runner, provider)
	}
	return runACPServer(ctx, server)
}

func remoteACPCommandSource(runner *runnerclient.Runner, ownsWorkspaceLock bool) acp.RemoteCommandSource {
	if !ownsWorkspaceLock {
		return nil
	}
	return runner
}

func resolveRemoteACPAuthTokens(cmd *cobra.Command, server string) (string, string, error) {
	apiAuthToken, _, err := resolveControlPlaneAuthToken(cmd, server)
	if err != nil {
		return "", "", err
	}
	return apiAuthToken, stringFlagOrEnvironment(cmd, "runner-auth-token", runnerAuthTokenEnv), nil
}

func validateRemoteACPFlags(cmd *cobra.Command) error {
	for _, name := range []string{
		"provider",
		"model",
		"max-tokens",
		"max-turns",
		"thinking-budget-tokens",
		"weak-model",
		"weak-model-max-tokens",
		"weak-reasoning-effort",
		"compact-ratio",
		"anthropic-api-access",
		"enable-openai-search",
	} {
		if cmd.Flags().Changed(name) {
			return errors.Errorf("--%s configures the local agentic loop and cannot be used with --server", name)
		}
	}
	return nil
}

func validateReusedACPFlags(cmd *cobra.Command) error {
	for _, name := range []string{"no-skills", "no-extensions", "enable-fs-search-tools"} {
		if cmd.Flags().Changed(name) {
			return errors.Errorf("--%s cannot override an already-running workspace runner; configure its runner profile or stop it so ACP can start the embedded runner", name)
		}
	}
	return nil
}

func remoteACPServiceOptions(cmd *cobra.Command) runnerclient.ServiceOptions {
	noSkills, _ := cmd.Flags().GetBool("no-skills")
	noExtensions, _ := cmd.Flags().GetBool("no-extensions")
	enableFSSearchTools, _ := cmd.Flags().GetBool("enable-fs-search-tools")
	return runnerclient.ServiceOptions{
		ConfigLoader: func(environmentProfile string) (llmtypes.Config, error) {
			config, err := llm.GetConfigFromViperWithEnvironmentProfile(environmentProfile)
			if err != nil {
				return config, err
			}
			if noSkills {
				config.Skills = &llmtypes.SkillsConfig{Enabled: false}
			}
			if noExtensions {
				if config.ExtensionSettings == nil {
					config.ExtensionSettings = make(map[string]any)
				}
				config.ExtensionSettings["enabled"] = false
			}
			if enableFSSearchTools {
				config.EnableFSSearchTools = true
			}
			return config, nil
		},
	}
}

func runACPServer(ctx context.Context, server acpServerLifecycle) error {
	runErr := make(chan error, 1)
	go func() {
		runErr <- server.Run()
	}()

	select {
	case err := <-runErr:
		server.Shutdown()
		return err
	case <-ctx.Done():
		server.Shutdown()
		return nil
	}
}

func buildACPServerConfig(cmd *cobra.Command) (*acp.ServerConfig, error) {
	llmConfig, err := llm.GetConfigFromViperWithCmd(cmd)
	if err != nil {
		return nil, err
	}

	provider, _ := cmd.Flags().GetString("provider")
	model, _ := cmd.Flags().GetString("model")
	maxTokens, _ := cmd.Flags().GetInt("max-tokens")
	noSkills, _ := cmd.Flags().GetBool("no-skills")
	noExtensions, _ := cmd.Flags().GetBool("no-extensions")
	enableFSSearchTools, _ := cmd.Flags().GetBool("enable-fs-search-tools")
	maxTurns, _ := cmd.Flags().GetInt("max-turns")
	maxTurns = max(maxTurns, 0)

	config := &acp.ServerConfig{
		Provider:            provider,
		Model:               model,
		MaxTokens:           maxTokens,
		NoSkills:            noSkills,
		NoExtensions:        noExtensions,
		EnableFSSearchTools: enableFSSearchTools || llmConfig.EnableFSSearchTools,
		MaxTurns:            maxTurns,
		CompactRatio:        llmConfig.CompactRatio,
	}

	return config, nil
}
