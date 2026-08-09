package main

import (
	"context"
	"io"
	stdlog "log"
	"os"
	"strings"

	chatpkg "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	"github.com/jingkaihe/kodelet/pkg/tui"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type ChatConfig struct {
	ResumeConvID  string
	CWD           string
	Theme         string
	Follow        bool
	NoExtensions  bool
	NoTools       bool
	Runner        string
	RunnerProfile string
	Server        string
	AuthToken     string
}

func NewChatConfig() *ChatConfig {
	return &ChatConfig{}
}

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Start an interactive Kodelet chat TUI",
	Long:  `Start an interactive terminal UI for chatting with Kodelet.`,
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		config := getChatConfigFromFlags(ctx, cmd)
		var chatRunner chatpkg.ChatRunner
		var remoteRunner *chatpkg.ControlPlaneChatRunner
		remote := strings.TrimSpace(config.Runner) != ""
		if remote {
			selectedRunner, workspace, remoteErr := prepareRemoteChatRunner(ctx, config)
			if remoteErr != nil {
				presenter.Error(remoteErr, "Failed to select remote runner")
				os.Exit(1)
			}
			remoteRunner = selectedRunner
			chatRunner = remoteRunner
			config.CWD = workspace
		} else if strings.TrimSpace(config.RunnerProfile) != "" {
			presenter.Error(errors.New("--runner-profile requires --runner"), "Invalid remote runner configuration")
			os.Exit(1)
		}

		if !remote {
			applyChatRuntimeRestrictions(config)
		}
		if err := tui.ValidateThemeName(config.Theme); err != nil {
			presenter.Error(err, "Invalid TUI theme")
			os.Exit(1)
		}
		reasoningEffort := ""
		reasoningEffortExplicit := cmd.Flags().Changed("reasoning-effort")
		if reasoningEffortExplicit {
			reasoningEffort, _ = cmd.Flags().GetString("reasoning-effort")
		}
		if err := validateChatResumeConversation(ctx, config.ResumeConvID, reasoningEffort); err != nil {
			presenter.Error(err, "Failed to resume conversation")
			os.Exit(1)
		}
		logger.SetLogOutput(io.Discard)
		stdlog.SetOutput(io.Discard)

		profile, _ := cmd.Flags().GetString("profile")
		var profileOptions []string
		var profileSettings map[string]tui.ProfileSettings
		var reasoningEffortOptions []string
		if remote {
			requestedProfile := ""
			if cmd.Flags().Changed("profile") {
				requestedProfile = profile
			}
			remoteProfile, options, settings, settingsErr := prepareRemoteChatSettings(ctx, remoteRunner, requestedProfile)
			if settingsErr != nil {
				presenter.Error(settingsErr, "Failed to load control-plane chat settings")
				os.Exit(1)
			}
			profile = remoteProfile
			profileOptions = options
			profileSettings = settings
			if selected, ok := remoteProfileSettings(settings, profile); ok {
				reasoningEffortOptions = selected.ReasoningEffortOptions
				if !reasoningEffortExplicit {
					reasoningEffort = selected.ReasoningEffort
				}
			}
			if reasoningEffortExplicit {
				if err := validateRemoteReasoningEffort(reasoningEffort, reasoningEffortOptions); err != nil {
					presenter.Error(err, "Invalid control-plane reasoning effort")
					os.Exit(1)
				}
			}
		} else if strings.TrimSpace(profile) == "" {
			profile = viper.GetString("profile")
		}

		if err := tui.Run(ctx, tui.Config{
			ConversationID:          config.ResumeConvID,
			Profile:                 profile,
			ProfileOptions:          profileOptions,
			ProfileSettings:         profileSettings,
			EnvironmentProfile:      config.RunnerProfile,
			ReasoningEffort:         reasoningEffort,
			ReasoningEffortOptions:  reasoningEffortOptions,
			ReasoningEffortExplicit: reasoningEffortExplicit,
			CWD:                     config.CWD,
			Theme:                   config.Theme,
			Runner:                  chatRunner,
			Remote:                  remote,
		}); err != nil {
			presenter.Error(err, "Chat failed")
			os.Exit(1)
		}
	},
}

func applyChatRuntimeRestrictions(config *ChatConfig) {
	if config.NoExtensions || config.NoTools {
		viper.Set("extensions.enabled", false)
	}
	if config.NoTools {
		viper.Set("allowed_tools", []string{"none"})
	}
}

func init() {
	defaults := NewChatConfig()
	chatCmd.Flags().StringP("resume", "r", defaults.ResumeConvID, "Resume a specific conversation")
	chatCmd.Flags().String("cwd", defaults.CWD, "Working directory to execute in (defaults to current shell directory for new chats)")
	chatCmd.Flags().String("theme", tui.AutoThemeName, "TUI theme (available: "+strings.Join(tui.AvailableThemeNames(), ", ")+")")
	chatCmd.Flags().BoolP("follow", "f", defaults.Follow, "Follow the most recent conversation")
	chatCmd.Flags().Bool("no-extensions", defaults.NoExtensions, "Disable extension runtime")
	chatCmd.Flags().Bool("no-tools", defaults.NoTools, "Disable all tools (for simple query-response usage)")
	chatCmd.Flags().String("runner", defaults.Runner, "Use a remote runner by ID, ID prefix, or display name")
	chatCmd.Flags().String("runner-profile", defaults.RunnerProfile, "Runner-local environment profile used with --runner (blank uses the runner base configuration)")
	chatCmd.Flags().String("server", defaultRunnerServer, "Control-plane URL used with --runner")
	chatCmd.Flags().String("auth-token", "", "Control-plane API authentication token used with --runner (or KODELET_AUTH_TOKEN)")
}

func getChatConfigFromFlags(ctx context.Context, cmd *cobra.Command) *ChatConfig {
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
			presenter.Error(errors.New("conflicting flags"), "--follow and --resume cannot be used together")
			os.Exit(1)
		}
		var err error
		config.ResumeConvID, err = conversations.GetMostRecentConversationID(ctx)
		if err != nil {
			presenter.Warning("No conversations found, starting a new conversation")
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
	if server, err := cmd.Flags().GetString("server"); err == nil {
		config.Server = strings.TrimSpace(server)
	}
	config.AuthToken = authTokenFlagOrEnvironment(cmd, controlPlaneAuthTokenEnv)

	return config
}

func prepareRemoteChatRunner(ctx context.Context, config *ChatConfig) (*chatpkg.ControlPlaneChatRunner, string, error) {
	if config == nil || strings.TrimSpace(config.Runner) == "" {
		return nil, "", errors.New("runner selector is required")
	}
	if config.ResumeConvID != "" || config.Follow {
		return nil, "", errors.New("remote TUI resume is not enabled yet; start a new conversation with --runner")
	}
	if strings.TrimSpace(config.CWD) != "" {
		return nil, "", errors.New("--cwd cannot be used with --runner because the runner owns its workspace")
	}
	if config.NoExtensions || config.NoTools {
		return nil, "", errors.New("--no-extensions and --no-tools are local-only options and cannot be used with --runner")
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
		return nil, "", errors.New("runner is incompatible with this control plane")
	}
	if !selected.Connected {
		return nil, "", errors.New("runner is offline")
	}
	if selected.Status != runnerregistry.RunnerStatusIdle {
		return nil, "", errors.Errorf("runner is not idle: %s", selected.Status)
	}
	runner, err := chatpkg.NewControlPlaneChatRunner(server, config.AuthToken, selected.ID)
	if err != nil {
		return nil, "", err
	}
	return runner, selected.Workspace.Path, nil
}

func prepareRemoteChatSettings(ctx context.Context, runner *chatpkg.ControlPlaneChatRunner, requestedProfile string) (string, []string, map[string]tui.ProfileSettings, error) {
	if runner == nil {
		return "", nil, nil, errors.New("control-plane chat runner is required")
	}
	selected, err := runner.ChatSettings(ctx, requestedProfile)
	if err != nil {
		return "", nil, nil, err
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
				return "", nil, nil, errors.Wrapf(err, "failed to load profile %s", name)
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
	return profile, options, settings, nil
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
	return errors.Errorf("reasoning effort %q is not allowed by the selected control-plane profile", requested)
}

func validateChatResumeConversation(ctx context.Context, conversationID string, requestedReasoningEfforts ...string) error {
	requestedReasoningEffort := ""
	if len(requestedReasoningEfforts) > 0 {
		requestedReasoningEffort = requestedReasoningEfforts[0]
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return nil
	}

	service, err := conversations.GetDefaultConversationService(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to open conversation store")
	}
	defer func() {
		_ = service.Close()
	}()

	record, err := service.GetConversation(ctx, conversationID)
	if err != nil {
		return errors.Wrapf(err, "conversation not found: %s", conversationID)
	}
	_, err = chatpkg.ResolveConfigForExistingConversation(record, requestedReasoningEffort)
	return err
}
