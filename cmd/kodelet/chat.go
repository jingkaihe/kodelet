package main

import (
	"context"
	"io"
	stdlog "log"
	"net/http"
	"os"
	"strings"

	chatpkg "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/messagehistory"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	"github.com/jingkaihe/kodelet/pkg/tui"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type ChatConfig struct {
	ResumeConvID     string
	CWD              string
	Theme            string
	Follow           bool
	NoExtensions     bool
	NoTools          bool
	Runner           string
	RunnerProfile    string
	Server           string
	ServerConfigured bool
	AuthToken        string
	ConfigError      error
	Options          *llmtypes.ExecutionOptions
}

func NewChatConfig() *ChatConfig {
	return &ChatConfig{}
}

var chatCmd = &cobra.Command{
	Use:               "chat",
	Short:             "Start an interactive chat in your terminal",
	Long:              `Chat with Kodelet in your terminal. A local server starts automatically in the background when needed. Use --server to connect to an explicitly managed server. Conversations are saved automatically; exiting chat leaves the server running.`,
	Args:              cobra.NoArgs,
	PersistentPreRunE: func(cmd *cobra.Command, _ []string) error { return validateRemoteChatFlags(cmd) },
	RunE: func(cmd *cobra.Command, _ []string) error {
		theme, _ := cmd.Flags().GetString("theme")
		logger.SetLogOutput(io.Discard)
		stdlog.SetOutput(io.Discard)
		return errors.Wrap(tui.Run(cmd.Context(), tui.Config{
			Remote: true,
			Theme:  theme,
			Initialize: func(ctx context.Context) (tui.Config, error) {
				return prepareDaemonChat(ctx, cmd)
			},
		}), "could not start chat")
	},
}

func validateRemoteChatFlags(cmd *cobra.Command) error {
	if cmd.Flags().Changed("no-save") {
		return errors.New("--no-save is no longer supported; all conversations are saved automatically")
	}
	for _, flag := range []string{"sysprompt", "sysprompt-arg", "allowed-domains-file", "anthropic-api-access", "account", "tool-mode", "context-patterns", "compact-ratio", "enable-openai-search"} {
		if cmd.Flags().Changed(flag) {
			return errors.Errorf("--%s cannot be set with 'kodelet %s'; set it in the server or runner configuration", flag, cmd.Name())
		}
	}
	return nil
}

// configuredChatRunner keeps command-scoped options out of the TUI and promotes
// the shared transport's history, streams, cancellation and UI response APIs.
type configuredChatRunner struct {
	*chatpkg.Client
	options            *llmtypes.ExecutionOptions
	runnerID           string
	explicitRunnerID   string
	defaultCWD         string
	environmentProfile string
}

func (r *configuredChatRunner) discoveryTarget(ctx context.Context, target chatpkg.WorkspaceTarget) (chatpkg.WorkspaceTarget, error) {
	target.Options = r.options.Restrictions()
	if target.ConversationID == "" {
		if target.RunnerID == "" {
			target.RunnerID = r.runnerID
			if target.RunnerID == "" {
				settings, err := r.ChatSettings(ctx, "")
				if err != nil {
					return target, err
				}
				if !settings.DefaultRunnerReady || settings.DefaultRunnerID == "" {
					return target, errors.New("the default runner is unavailable; check 'kodelet runner list' and the server logs, or select another runner with --runner")
				}
				target.RunnerID = settings.DefaultRunnerID
				if target.CWD == "" && r.defaultCWD == "" {
					target.CWD, err = sameHostDefaultCWD(settings)
					if err != nil {
						return target, err
					}
				}
			}
		}
		if target.CWD == "" {
			target.CWD = r.defaultCWD
		}
		if target.EnvironmentProfile == "" {
			target.EnvironmentProfile = r.environmentProfile
		}
	}
	return target, nil
}

func (r *configuredChatRunner) DiscoverWorkspace(ctx context.Context, target chatpkg.WorkspaceTarget) (protocol.WorkspaceDiscoverResult, error) {
	target, err := r.discoveryTarget(ctx, target)
	if err != nil {
		return protocol.WorkspaceDiscoverResult{}, err
	}
	return r.Client.DiscoverWorkspace(ctx, target)
}

func (r *configuredChatRunner) ExecuteWorkspaceShortcut(ctx context.Context, request chatpkg.WorkspaceShortcutRequest) (runnerpayload.ShortcutExecuteResult, error) {
	target, err := r.discoveryTarget(ctx, request.Target)
	if err != nil {
		return runnerpayload.ShortcutExecuteResult{}, err
	}
	request.Target = target
	return r.Client.ExecuteWorkspaceShortcut(ctx, request)
}

func (r *configuredChatRunner) WorkspaceCWDSuggestions(ctx context.Context, target chatpkg.WorkspaceTarget, query string) (protocol.WorkspaceCWDHintsResult, error) {
	target, err := r.discoveryTarget(ctx, target)
	if err != nil {
		return protocol.WorkspaceCWDHintsResult{}, err
	}
	// Directory resolution does not execute extensions and accepts no run options.
	target.Options = nil
	return r.Client.WorkspaceCWDSuggestions(ctx, target, query)
}

func (r *configuredChatRunner) LoadMessageHistory(ctx context.Context, target chatpkg.WorkspaceTarget) (protocol.WorkspaceMessageHistoryResult, error) {
	target, err := r.discoveryTarget(ctx, target)
	if err != nil {
		return protocol.WorkspaceMessageHistoryResult{}, err
	}
	target.Options = nil
	return r.Client.LoadMessageHistory(ctx, target)
}

func (r *configuredChatRunner) AppendMessageHistory(ctx context.Context, target chatpkg.WorkspaceTarget, entry messagehistory.Entry) error {
	target, err := r.discoveryTarget(ctx, target)
	if err != nil {
		return err
	}
	target.Options = nil
	return r.Client.AppendMessageHistory(ctx, target, entry)
}

func (r *configuredChatRunner) Run(ctx context.Context, request chatpkg.ChatRequest, sink chatpkg.ChatEventSink) (string, error) {
	request.Options = r.options.Clone()
	var history chatpkg.ConversationHistory
	var err error
	if request.ConversationID != "" {
		history, err = r.LoadConversation(ctx, request.ConversationID)
		var responseErr *chatpkg.ControlPlaneHTTPError
		if err != nil && (!errors.As(err, &responseErr) || responseErr.StatusCode != http.StatusNotFound) {
			return request.ConversationID, err
		}
	}
	if history.ID != "" {
		if err := validateDaemonChatAffinity(history, request, r.explicitRunnerID); err != nil {
			return request.ConversationID, err
		}
		request.RunnerID, request.CWD, request.EnvironmentProfile = history.RunnerID, history.CWD, history.EnvironmentProfile
	} else {
		target, err := r.discoveryTarget(ctx, chatpkg.WorkspaceTarget{CWD: request.CWD, Profile: request.Profile, EnvironmentProfile: request.EnvironmentProfile})
		if err != nil {
			return request.ConversationID, err
		}
		discovery, err := r.Client.DiscoverWorkspace(ctx, target)
		if err != nil {
			return request.ConversationID, err
		}
		request.RunnerID, request.CWD, request.EnvironmentProfile = target.RunnerID, discovery.CWD, discovery.EnvironmentProfile
	}
	id, err := r.Client.Run(ctx, request, sink)
	if err != nil {
		return id, errors.Wrapf(err, "chat failed or the connection was interrupted; before sending the message again, check 'kodelet conversation turn %s %s'", request.ConversationID, request.TurnID)
	}
	return id, nil
}

func validateDaemonChatAffinity(history chatpkg.ConversationHistory, request chatpkg.ChatRequest, explicitRunnerID string) error {
	if history.ID != request.ConversationID || history.RunnerID == "" || history.CWD == "" {
		return errors.New("this conversation has no saved runner or working directory; use 'kodelet conversation adopt' before resuming")
	}
	if explicitRunnerID != "" && explicitRunnerID != history.RunnerID {
		return errors.New("this conversation uses a different runner; omit --runner to use its saved runner")
	}
	if request.CWD != "" && request.CWD != history.CWD {
		return errors.New("the working directory cannot be changed when resuming; start a new conversation to use another directory")
	}
	if request.Profile != "" && chatpkg.NormalizeRequestedProfile(request.Profile) != chatpkg.NormalizeRequestedProfile(history.Profile) {
		return errors.New("the model profile cannot be changed when resuming; start a new conversation to use another profile")
	}
	if request.EnvironmentProfile != "" && chatpkg.NormalizeEnvironmentProfile(request.EnvironmentProfile) != chatpkg.NormalizeEnvironmentProfile(history.EnvironmentProfile) {
		return errors.New("the runner profile cannot be changed when resuming; start a new conversation to use another profile")
	}
	return nil
}

func prepareDaemonChat(ctx context.Context, cmd *cobra.Command) (tui.Config, error) {
	var result tui.Config
	if err := validateRemoteChatFlags(cmd); err != nil {
		return result, err
	}
	config := getChatConfigFromFlags(cmd)
	if config.ConfigError != nil {
		return result, config.ConfigError
	}
	if err := tui.ValidateThemeName(config.Theme); err != nil {
		return result, err
	}
	if config.Follow && config.Runner == "" && config.CWD == "" {
		return result, errors.New("--follow requires --runner or --cwd to choose which conversation history to search")
	}
	server, token, err := prepareClientServer(ctx, cmd)
	if err != nil {
		return result, err
	}
	config.Server, config.AuthToken = server, token
	client, err := prepareServerChatRunner(config)
	if err != nil {
		return result, err
	}
	runner := &configuredChatRunner{Client: client, options: config.Options.Clone(), environmentProfile: config.RunnerProfile}
	if config.Runner != "" {
		runners, _, err := fetchRunners(ctx, config.Server, config.AuthToken)
		if err != nil {
			return result, err
		}
		selected, err := selectRunner(runners, config.Runner)
		if err != nil {
			return result, err
		}
		runner.runnerID, runner.explicitRunnerID = selected.ID, selected.ID
	}
	if config.Follow {
		var profile string
		if cmd.Flags().Changed("profile") {
			profile, _ = cmd.Flags().GetString("profile")
		}
		target, err := runner.WorkspaceCWDSuggestions(ctx, chatpkg.WorkspaceTarget{CWD: config.CWD, Profile: profile}, "")
		if err != nil {
			return result, err
		}
		source, err := chatpkg.NewClient(config.Server, config.AuthToken, runner.explicitRunnerID)
		if err != nil {
			return result, err
		}
		history, err := source.ListConversationsInCWD(ctx, 1, target.BaseDir)
		if err != nil {
			return result, err
		}
		if len(history) == 0 {
			return result, errors.New("no conversation found for the selected runner or directory; omit --follow to start one")
		}
		config.ResumeConvID = history[0].ID
	}
	result = tui.Config{Remote: true, Runner: runner, Theme: config.Theme, ConversationID: config.ResumeConvID, CWD: config.CWD, EnvironmentProfile: config.RunnerProfile, ReasoningEffortExplicit: cmd.Flags().Changed("reasoning-effort")}
	if cmd.Flags().Changed("profile") {
		result.Profile, _ = cmd.Flags().GetString("profile")
	}
	var target chatpkg.WorkspaceTarget
	if config.ResumeConvID != "" {
		history, err := client.LoadConversation(ctx, config.ResumeConvID)
		if err != nil {
			return result, err
		}
		if err := validateDaemonChatAffinity(history, chatpkg.ChatRequest{ConversationID: config.ResumeConvID, CWD: config.CWD, Profile: result.Profile, EnvironmentProfile: config.RunnerProfile}, runner.explicitRunnerID); err != nil {
			return result, err
		}
		if cmd.Flags().Changed("runner-profile") && chatpkg.NormalizeEnvironmentProfile(config.RunnerProfile) != chatpkg.NormalizeEnvironmentProfile(history.EnvironmentProfile) {
			return result, errors.New("the runner profile cannot be changed when resuming; start a new conversation to use another profile")
		}
		result.Profile, result.EnvironmentProfile, result.ReasoningEffort = history.Profile, history.EnvironmentProfile, history.ReasoningEffort
		result.CWD = history.CWD
		runner.runnerID = history.RunnerID
		result.ProfileOptions = []string{history.Profile}
		result.ReasoningEffortOptions = []string{history.ReasoningEffort}
		target.ConversationID = history.ID
	} else {
		result.Profile, result.ProfileOptions, result.ProfileSettings, _, err = prepareRemoteChatSettings(ctx, client, result.Profile)
		if err != nil {
			return result, err
		}
		settings, _ := remoteProfileSettings(result.ProfileSettings, result.Profile)
		result.ReasoningEffort, result.ReasoningEffortOptions = settings.ReasoningEffort, settings.ReasoningEffortOptions
		target = chatpkg.WorkspaceTarget{CWD: config.CWD, Profile: result.Profile, EnvironmentProfile: config.RunnerProfile}
	}
	if result.ReasoningEffortExplicit {
		result.ReasoningEffort, _ = cmd.Flags().GetString("reasoning-effort")
		if err := validateRemoteReasoningEffort(result.ReasoningEffort, result.ReasoningEffortOptions); err != nil {
			return result, err
		}
	}
	target, err = runner.discoveryTarget(ctx, target)
	if err != nil {
		return result, err
	}
	// Resolve runner-owned paths without initializing extensions. The TUI loads
	// commands asynchronously once the composer is available.
	target.Options = nil
	resolved, err := client.WorkspaceCWDSuggestions(ctx, target, "")
	if err != nil {
		return result, err
	}
	if resolved.BaseDir == "" {
		return result, errors.New("the runner did not return a working directory; check the directory and runner logs")
	}
	if config.ResumeConvID != "" && resolved.BaseDir != result.CWD {
		return result, errors.New("the runner returned a different directory than this conversation saved")
	}
	result.CWD, result.DefaultCWD = resolved.BaseDir, resolved.BaseDir
	runner.defaultCWD = resolved.BaseDir
	if config.ResumeConvID == "" {
		result.EnvironmentProfile = chatpkg.NormalizeEnvironmentProfile(target.EnvironmentProfile)
		runner.runnerID = target.RunnerID
	}
	return result, nil
}

func init() {
	defaults := NewChatConfig()
	chatCmd.Flags().StringP("resume", "r", defaults.ResumeConvID, "Resume a specific conversation")
	chatCmd.Flags().String("cwd", defaults.CWD, "Working directory on the runner (defaults to your current directory when using this machine's built-in runner)")
	chatCmd.Flags().String("theme", tui.AutoThemeName, "TUI theme (available: "+strings.Join(tui.AvailableThemeNames(), ", ")+")")
	chatCmd.Flags().BoolP("follow", "f", defaults.Follow, "Follow the most recent conversation")
	chatCmd.Flags().Bool("no-extensions", defaults.NoExtensions, "Disable extensions for this conversation")
	chatCmd.Flags().Bool("no-tools", defaults.NoTools, "Disable all tools (for simple query-response usage)")
	chatCmd.Flags().Bool("use-weak-model", false, "Use the configured weak model")
	chatCmd.Flags().Int("max-turns", 0, "Maximum AI turns per prompt (0 for no limit)")
	chatCmd.Flags().String("runner", defaults.Runner, "Workspace runner ID, ID prefix, or display name")
	chatCmd.Flags().String("runner-profile", defaults.RunnerProfile, "Runner environment profile for new conversations")
	chatCmd.Flags().String("server", defaultRunnerServer, "Server URL (or KODELET_SERVER)")
	chatCmd.Flags().String("auth-token", "", "API authentication token (or KODELET_AUTH_TOKEN)")
}

func getChatConfigFromFlags(cmd *cobra.Command) *ChatConfig {
	config := NewChatConfig()

	if resumeConvID, err := cmd.Flags().GetString("resume"); err == nil {
		config.ResumeConvID = strings.TrimSpace(resumeConvID)
	}
	if cwd, err := cmd.Flags().GetString("cwd"); err == nil {
		config.CWD = strings.TrimSpace(cwd)
	}
	if theme, err := cmd.Flags().GetString("theme"); err == nil {
		config.Theme = strings.TrimSpace(theme)
	}
	if follow, err := cmd.Flags().GetBool("follow"); err == nil {
		config.Follow = follow
	}
	if config.Follow {
		if config.ResumeConvID != "" {
			config.ConfigError = errors.New("--follow and --resume cannot be used together")
			return config
		}
	}
	if noExtensions, err := cmd.Flags().GetBool("no-extensions"); err == nil {
		config.NoExtensions = noExtensions
	}
	if noTools, err := cmd.Flags().GetBool("no-tools"); err == nil {
		config.NoTools = noTools
	}
	if runner, err := cmd.Flags().GetString("runner"); err == nil {
		config.Runner = strings.TrimSpace(runner)
	}
	if runnerProfile, err := cmd.Flags().GetString("runner-profile"); err == nil {
		config.RunnerProfile = strings.TrimSpace(runnerProfile)
	}
	config.Server, config.ServerConfigured = serverFlagOrConfig(cmd)
	config.Options, config.ConfigError = remoteRunExecutionOptions(cmd)
	if config.ConfigError == nil && (config.ServerConfigured || cmd.Flags().Changed("auth-token") || strings.TrimSpace(os.Getenv(controlPlaneAuthTokenEnv)) != "") {
		config.AuthToken, _, config.ConfigError = resolveControlPlaneAuthToken(cmd, config.Server)
	}

	return config
}

func prepareRemoteChatRunner(ctx context.Context, config *ChatConfig) (*chatpkg.Client, string, error) {
	if config == nil || strings.TrimSpace(config.Runner) == "" {
		return nil, "", errors.New("runner selector is required")
	}
	runners, server, err := fetchRunners(ctx, config.Server, config.AuthToken)
	if err != nil {
		return nil, "", err
	}
	selected, err := selectRunner(runners, config.Runner)
	if err != nil {
		return nil, "", err
	}
	if selected.Status == runnerregistry.RunnerStatusIncompatible {
		if selected.CompatibilityError != "" {
			return nil, "", errors.New(selected.CompatibilityError)
		}
		return nil, "", errors.New("runner is incompatible with this server")
	}
	if !selected.Connected {
		return nil, "", errors.New("runner is offline")
	}
	if selected.Status != runnerregistry.RunnerStatusIdle && selected.Status != runnerregistry.RunnerStatusBusy {
		return nil, "", errors.Errorf("runner is not available: %s", selected.Status)
	}
	if selected.Status == runnerregistry.RunnerStatusBusy && !selected.ConcurrentRuns {
		return nil, "", errors.New("runner does not support concurrent runs")
	}
	runner, err := chatpkg.NewClient(server, config.AuthToken, selected.ID)
	if err != nil {
		return nil, "", err
	}
	return runner, selected.Workspace.Path, nil
}

func prepareServerChatRunner(config *ChatConfig) (*chatpkg.Client, error) {
	if config == nil {
		return nil, errors.New("chat configuration is required")
	}
	return chatpkg.NewClient(config.Server, config.AuthToken, "")
}

func usesControlPlaneChat(config *ChatConfig) bool {
	return config != nil
}

func resolveFollowConversation(ctx context.Context, source chatpkg.ConversationSource) (string, error) {
	if source == nil {
		return "", errors.New("conversation history is unavailable")
	}
	if runner, ok := source.(*chatpkg.Client); ok && runner == nil {
		return "", errors.New("conversation history is unavailable")
	}
	summaries, err := source.ListConversations(ctx, 1)
	if err != nil {
		return "", err
	}
	if len(summaries) == 0 || strings.TrimSpace(summaries[0].ID) == "" {
		return "", errors.New("no conversations found")
	}
	return strings.TrimSpace(summaries[0].ID), nil
}

func prepareRemoteChatSettings(ctx context.Context, runner *chatpkg.Client, requestedProfile string) (string, []string, map[string]tui.ProfileSettings, string, error) {
	if runner == nil {
		return "", nil, nil, "", errors.New("chat runner is required")
	}
	selected, err := runner.ChatSettings(ctx, requestedProfile)
	if err != nil {
		return "", nil, nil, "", err
	}
	profile := strings.TrimSpace(selected.CurrentProfile)
	if profile == "" {
		profile = "default"
	}
	options := make([]string, 0, len(selected.Profiles)+1)
	settings := make(map[string]tui.ProfileSettings, len(selected.Profiles)+1)
	for _, option := range selected.Profiles {
		name := strings.TrimSpace(option.Name)
		if name == "" {
			continue
		}
		options = append(options, name)
		profileSettings := selected
		if !strings.EqualFold(name, profile) {
			profileSettings, err = runner.ChatSettings(ctx, name)
			if err != nil {
				return "", nil, nil, "", errors.Wrapf(err, "failed to load profile %s", name)
			}
		}
		settings[name] = tui.ProfileSettings{
			ReasoningEffort:        profileSettings.ReasoningEffort,
			ReasoningEffortOptions: append([]string(nil), profileSettings.ReasoningEffortOptions...),
		}
	}
	if _, ok := remoteProfileSettings(settings, profile); !ok {
		options = append(options, profile)
		settings[profile] = tui.ProfileSettings{
			ReasoningEffort:        selected.ReasoningEffort,
			ReasoningEffortOptions: append([]string(nil), selected.ReasoningEffortOptions...),
		}
	}
	return profile, options, settings, strings.TrimSpace(selected.DefaultCWD), nil
}

func remoteProfileSettings(settings map[string]tui.ProfileSettings, profile string) (tui.ProfileSettings, bool) {
	for name, value := range settings {
		if strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(profile)) {
			return value, true
		}
	}
	return tui.ProfileSettings{}, false
}

func validateRemoteReasoningEffort(requested string, options []string) error {
	requested = strings.TrimSpace(requested)
	for _, option := range options {
		if strings.EqualFold(strings.TrimSpace(option), requested) {
			return nil
		}
	}
	return errors.Errorf("reasoning effort %q is not allowed by the selected model profile", requested)
}
