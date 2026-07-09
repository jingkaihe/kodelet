package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/fragments"
	"github.com/jingkaihe/kodelet/pkg/goals"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/jingkaihe/kodelet/pkg/slashcommands"
	"github.com/jingkaihe/kodelet/pkg/tools"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type RunConfig struct {
	ResumeConvID        string
	CWD                 string
	Follow              bool
	NoSave              bool
	Headless            bool              // Use structured JSON output instead of console formatting
	StreamDeltas        bool              // Stream partial text deltas in headless mode
	Images              []string          // Image paths or URLs to include with the message
	MaxTurns            int               // Maximum number of turns within a single SendMessage call
	FragmentName        string            // Name of fragment to use
	FragmentArgs        map[string]string // Arguments to pass to fragment
	FragmentDirs        []string          // Additional fragment directories
	IncludeHistory      bool              // Include historical conversation data in headless streaming
	NoSkills            bool              // Disable agentic skills
	NoExtensions        bool              // Disable extension runtime
	NoTools             bool              // Disable all tools (for simple query-response usage)
	EnableFSSearchTools bool              // Enable filesystem search tools (glob_tool and grep_tool)
	MessageDisplay      string            // User-facing compact text for persisted display
	Sysprompt           string            // Path to custom system prompt template file
	SyspromptArgs       map[string]string // Arguments passed to custom system prompt template
	ResultOnly          bool              // Only print the final agent message, no intermediate output or usage stats
	UseWeakModel        bool              // Use weak model for SendMessage
	Account             string            // Anthropic subscription account alias to use
}

func NewRunConfig() *RunConfig {
	return &RunConfig{
		ResumeConvID:        "",
		CWD:                 "",
		Follow:              false,
		NoSave:              false,
		Headless:            false,
		Images:              []string{},
		MaxTurns:            0,
		FragmentName:        "",
		FragmentArgs:        make(map[string]string),
		FragmentDirs:        []string{},
		IncludeHistory:      false,
		NoSkills:            false,
		NoExtensions:        false,
		NoTools:             false,
		EnableFSSearchTools: false,
		Sysprompt:           "",
		SyspromptArgs:       make(map[string]string),
		ResultOnly:          false,
		UseWeakModel:        false,
		Account:             "",
	}
}

type processedFragment struct {
	Query     string
	Display   string
	Metadata  *fragments.Metadata
	Response  string
	Responded bool
}

func processFragment(ctx context.Context, config *RunConfig, args []string, extensionRuntime *extensions.Runtime, callContext extensions.ExtensionCallContext) (*processedFragment, error) {
	var validDirs []string
	for _, dir := range config.FragmentDirs {
		trimmed := strings.TrimSpace(dir)
		if trimmed != "" {
			validDirs = append(validDirs, trimmed)
		}
	}

	fragmentProcessor, err := fragments.NewFragmentProcessor(fragments.WithAdditionalDirs(validDirs...))
	if err != nil {
		return nil, errors.Wrap(err, "failed to create fragment processor")
	}

	fragmentConfig := &fragments.Config{
		FragmentName: config.FragmentName,
		Arguments:    config.FragmentArgs,
	}

	display := strings.TrimSpace(config.MessageDisplay)
	if display == "" {
		display = "/" + strings.TrimSpace(config.FragmentName)
		if argDisplay := formatFragmentDisplayArgs(config.FragmentArgs); argDisplay != "" {
			display += " " + argDisplay
		}
		if argsContent := strings.TrimSpace(strings.Join(args, " ")); argsContent != "" {
			display += " " + argsContent
		}
	}

	commandArgs := strings.Join(args, " ")
	if argDisplay := formatFragmentDisplayArgs(config.FragmentArgs); argDisplay != "" {
		parts := []string{argDisplay}
		if argsContent := strings.TrimSpace(commandArgs); argsContent != "" {
			parts = append(parts, argsContent)
		}
		commandArgs = strings.Join(parts, " ")
	}

	if extensionRuntime != nil {
		if callContext.InvokedBy == "" {
			callContext.InvokedBy = "main"
		}
		callContext.RecipeName = config.FragmentName
		if commandResult, err := extensionRuntime.TryCommand(
			ctx,
			display,
			config.FragmentName,
			commandArgs,
			callContext,
		); err != nil {
			return nil, errors.Wrapf(err, "failed to execute extension recipe %s", config.FragmentName)
		} else if commandResult != nil && commandResult.Matched {
			switch commandResult.Action {
			case extensions.CommandActionRunAgent:
				metadata := &fragments.Metadata{
					Name:        commandResult.Registration.Name,
					Description: commandResult.Registration.Description,
				}
				return &processedFragment{Query: commandResult.Prompt, Display: display, Metadata: metadata}, nil
			case extensions.CommandActionRespond:
				metadata := &fragments.Metadata{
					Name:        commandResult.Registration.Name,
					Description: commandResult.Registration.Description,
				}
				return &processedFragment{Display: display, Metadata: metadata, Response: commandResult.Response, Responded: true}, nil
			}
		}
	}

	fragment, err := fragmentProcessor.LoadFragment(ctx, fragmentConfig)
	if err != nil {
		return nil, errors.Wrap(err, "failed to load fragment")
	}

	var query string
	if len(args) > 0 {
		argsContent := strings.Join(args, " ")
		query = fragment.Content + "\n" + argsContent
	} else {
		query = fragment.Content
	}

	return &processedFragment{Query: query, Display: display, Metadata: &fragment.Metadata}, nil
}

func formatFragmentDisplayArgs(args map[string]string) string {
	if len(args) == 0 {
		return ""
	}

	keys := make([]string, 0, len(args))
	for key := range args {
		if strings.TrimSpace(key) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := args[key]
		if strings.ContainsAny(value, " \t\n\r\"") {
			value = fmt.Sprintf("%q", value)
		}
		parts = append(parts, fmt.Sprintf("%s=%s", key, value))
	}
	return strings.Join(parts, " ")
}

func addRunMessageDisplay(thread llmtypes.Thread, query string, config *RunConfig) {
	display := strings.TrimSpace(config.MessageDisplay)
	if display == "" || strings.TrimSpace(query) == "" {
		return
	}

	metadata := conversations.AddSlashCommandDisplay(thread.GetMetadata(), query, display, config.FragmentName)
	for key, value := range metadata {
		thread.SetMetadataValue(key, value)
	}
}

func addRunGoalDisplay(thread llmtypes.Thread, update *goals.CommandUpdate) {
	if thread == nil || update == nil {
		return
	}

	thread.SetMetadataValue(goals.MetadataKey, update.Goal)
	metadata := conversations.AddMessageDisplay(thread.GetMetadata(), update.ModelPrompt, update.Display, conversations.MessageDisplayKindGoal, goals.SlashCommandName)
	for key, value := range metadata {
		thread.SetMetadataValue(key, value)
	}
}

func getQueryFromStdinOrArgs(args []string) (string, error) {
	stat, _ := os.Stdin.Stat()
	isPipe := (stat.Mode() & os.ModeCharDevice) == 0

	if isPipe {
		stdinBytes, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", errors.Wrap(err, "failed to read from stdin")
		}

		stdinContent := string(stdinBytes)

		if len(args) > 0 {
			argsContent := strings.Join(args, " ")
			return argsContent + "\n" + stdinContent, nil
		}
		return stdinContent, nil
	}

	if len(args) == 0 {
		return "", errors.New("no query provided")
	}
	return strings.Join(args, " "), nil
}

func applyFragmentRestrictions(llmConfig *llmtypes.Config, fragmentMetadata *fragments.Metadata) {
	if fragmentMetadata == nil {
		return
	}

	if len(fragmentMetadata.AllowedTools) > 0 {
		if err := tools.ValidateTools(fragmentMetadata.AllowedTools); err != nil {
			presenter.Warning(fmt.Sprintf("Invalid tools in fragment metadata, ignoring: %v", err))
		} else {
			llmConfig.AllowedTools = fragmentMetadata.AllowedTools
		}
	}

	if len(fragmentMetadata.AllowedCommands) > 0 {
		llmConfig.AllowedCommands = fragmentMetadata.AllowedCommands
	}
}

func applyRunToolRestrictions(llmConfig *llmtypes.Config, fragmentMetadata *fragments.Metadata, noTools bool) {
	applyFragmentRestrictions(llmConfig, fragmentMetadata)
	if noTools {
		llmConfig.AllowedTools = []string{tools.NoToolsMarker}
	}
}

func createRunToolManagers(ctx context.Context, config *RunConfig, workingDir string) (*extensions.Runtime, error) {
	var extensionRuntime *extensions.Runtime
	if !config.NoTools && !config.NoExtensions {
		var err error
		extensionRuntime, err = extensions.NewRuntimeFromViper(ctx, workingDir)
		if err != nil {
			return nil, err
		}
	}

	return extensionRuntime, nil
}

func normalizeConversationProfile(profile string) string {
	profile = strings.TrimSpace(profile)
	if profile == "" || strings.EqualFold(profile, "default") {
		return ""
	}
	return profile
}

func loadResumeConversationConfig(ctx context.Context, cmd *cobra.Command, conversationID string, requestedCWD string) (llmtypes.Config, string, error) {
	defaultCWD, err := conversations.CurrentWorkingDirectory()
	if err != nil {
		return llmtypes.Config{}, "", err
	}

	if strings.TrimSpace(conversationID) == "" {
		config, err := llm.GetConfigFromViperWithCmd(cmd)
		if err != nil {
			return llmtypes.Config{}, "", err
		}

		resolution, err := conversations.ResolveCWD(ctx, nil, "", requestedCWD, defaultCWD, false)
		if err != nil {
			return llmtypes.Config{}, "", err
		}
		config.WorkingDirectory = resolution.CWD
		return config, resolution.CWD, nil
	}

	store, err := conversations.GetConversationStore(ctx)
	if err != nil {
		return llmtypes.Config{}, "", errors.Wrap(err, "failed to open conversation store")
	}
	defer func() {
		_ = store.Close()
	}()

	resolution, err := conversations.ResolveCWD(ctx, store, conversationID, requestedCWD, defaultCWD, true)
	if err != nil {
		return llmtypes.Config{}, "", errors.Wrap(err, "failed to resolve conversation cwd")
	}

	record, err := store.Load(ctx, conversationID)
	if err != nil {
		return llmtypes.Config{}, "", errors.Wrap(err, "failed to load conversation")
	}

	profileName := ""
	hasStoredProfile := false
	if record.Metadata != nil {
		if rawProfile, ok := record.Metadata["profile"].(string); ok {
			hasStoredProfile = true
			profileName = normalizeConversationProfile(rawProfile)
		}
	}

	var config llmtypes.Config
	if hasStoredProfile {
		config, err = llm.GetConfigFromViperWithProfileAndCmd(profileName, cmd)
	} else {
		config, err = llm.GetConfigFromViperWithCmd(cmd)
	}
	if err != nil {
		return llmtypes.Config{}, "", err
	}

	if strings.TrimSpace(record.Provider) != "" {
		config.Provider = strings.TrimSpace(record.Provider)
	}

	if record.Metadata != nil {
		if model, ok := record.Metadata["model"].(string); ok && strings.TrimSpace(model) != "" {
			config.Model = strings.TrimSpace(model)
		}

		if strings.EqualFold(config.Provider, "openai") {
			if config.OpenAI == nil {
				config.OpenAI = &llmtypes.OpenAIConfig{}
			}
			if platform, ok := record.Metadata["platform"].(string); ok && strings.TrimSpace(platform) != "" {
				config.OpenAI.Platform = strings.TrimSpace(platform)
			}
			if apiMode, ok := record.Metadata["api_mode"].(string); ok && strings.TrimSpace(apiMode) != "" {
				config.OpenAI.APIMode = llmtypes.OpenAIAPIMode(strings.TrimSpace(apiMode))
			}
			if serviceTier, ok := record.Metadata["service_tier"].(string); ok && strings.TrimSpace(serviceTier) != "" {
				config.OpenAI.ServiceTier = llmtypes.OpenAIServiceTier(strings.TrimSpace(serviceTier))
			}
		}
	}

	if hasStoredProfile && profileName == "" {
		config.Profile = "default"
	} else {
		config.Profile = profileName
	}
	config.WorkingDirectory = resolution.CWD
	return config, resolution.CWD, nil
}

var runCmd = &cobra.Command{
	Use:   "run [query]",
	Short: "Execute a one-shot query with Kodelet",
	Long:  `Execute a one-shot query with Kodelet and return the result.`,
	Args:  cobra.MinimumNArgs(0),
	Run: func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()

		config := getRunConfigFromFlags(ctx, cmd)

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-sigCh
			presenter.Warning("Cancellation requested, shutting down...")
			cancel()
		}()

		var query string
		var fragmentMetadata *fragments.Metadata
		var goalUpdate *goals.CommandUpdate
		var llmConfig llmtypes.Config
		var resolvedCWD string
		var err error

		if config.FragmentName == "" {
			query, err = getQueryFromStdinOrArgs(args)
			if err != nil {
				presenter.Error(err, "Please provide a query to execute")
				return
			}

			if command, commandArgs, found := slashcommands.Parse(query); found {
				update, handled, err := goals.ParseSlashCommand(command, commandArgs, time.Now())
				if handled {
					if err != nil {
						presenter.Error(err, "Failed to process goal")
						return
					}
					query = update.ModelPrompt
					goalUpdate = &update
				}
			}
		}

		llmConfig, resolvedCWD, err = loadResumeConversationConfig(ctx, cmd, config.ResumeConvID, config.CWD)
		if err != nil {
			presenter.Error(err, "Failed to load configuration")
			os.Exit(1)
		}
		llmConfig.WorkingDirectory = resolvedCWD

		if !config.Headless && !config.ResultOnly {
			ctx = extensions.ContextWithUIInputBroker(ctx, extensions.NewTerminalUIInputBroker(os.Stdin, os.Stderr))
		}

		extensionRuntime, err := createRunToolManagers(ctx, config, resolvedCWD)
		if err != nil {
			presenter.Error(err, "Failed to initialize tools")
			return
		}
		if extensionRuntime != nil {
			defer func() {
				_ = extensionRuntime.Close()
			}()
		}

		if config.FragmentName != "" {
			processed, err := processFragment(ctx, config, args, extensionRuntime, extensions.ExtensionCallContext{
				ConversationID: config.ResumeConvID,
				CWD:            resolvedCWD,
				Provider:       llmConfig.Provider,
				Model:          llmConfig.Model,
				Profile:        llmConfig.Profile,
				InvokedBy:      "main",
			})
			if err != nil {
				presenter.Error(err, "Failed to process fragment")
				return
			}
			if processed.Responded {
				presenter.Info(processed.Response)
				return
			}
			query = processed.Query
			config.MessageDisplay = processed.Display
			fragmentMetadata = processed.Metadata
		}

		if config.FragmentName == "" && goalUpdate == nil && extensionRuntime != nil {
			if command, commandArgs, found := slashcommands.Parse(query); found {
				commandResult, err := extensionRuntime.TryCommand(ctx, query, command, commandArgs, extensions.ExtensionCallContext{
					ConversationID: config.ResumeConvID,
					CWD:            resolvedCWD,
					Provider:       llmConfig.Provider,
					Model:          llmConfig.Model,
					Profile:        llmConfig.Profile,
					InvokedBy:      "main",
				})
				if err != nil {
					presenter.Error(err, "Failed to execute extension command")
					return
				}
				if commandResult != nil && commandResult.Matched {
					switch commandResult.Action {
					case extensions.CommandActionRespond:
						presenter.Info(commandResult.Response)
						return
					case extensions.CommandActionRunAgent:
						query = commandResult.Prompt
						config.MessageDisplay = commandResult.Display
						if commandResult.RecipeName != "" {
							config.FragmentName = commandResult.RecipeName
						}
					default:
						presenter.Warning(fmt.Sprintf("Extension command %s returned unknown action %q", commandResult.CommandName, commandResult.Action))
					}
				}
			}
		}

		if cmd.Flags().Changed("enable-fs-search-tools") {
			llmConfig.EnableFSSearchTools = config.EnableFSSearchTools
		}
		if strings.TrimSpace(config.Sysprompt) != "" {
			llmConfig.Sysprompt = strings.TrimSpace(config.Sysprompt)
		}
		if len(config.SyspromptArgs) > 0 {
			llmConfig.SyspromptArgs = config.SyspromptArgs
		}
		llmConfig.RecipeName = config.FragmentName
		llmConfig.Extensions = extensionRuntime

		// Set Anthropic account if specified
		if config.Account != "" {
			// Validate the account exists
			if _, err := auth.GetAnthropicCredentialsByAlias(config.Account); err != nil {
				presenter.Error(err, fmt.Sprintf("Account '%s' not found. Run 'kodelet accounts list' to see available accounts", config.Account))
				return
			}
			llmConfig.AnthropicAccount = config.Account
		}

		applyRunToolRestrictions(&llmConfig, fragmentMetadata, config.NoTools)

		var stateOpts []tools.BasicStateOption
		stateOpts = append(stateOpts, tools.WithWorkingDirectory(llmConfig.WorkingDirectory))
		stateOpts = append(stateOpts, tools.WithLLMConfig(llmConfig))
		if !config.NoTools {
			if extensionRuntime != nil {
				stateOpts = append(stateOpts, tools.WithExtensionTools(extensionRuntime.Tools()))
			}

			stateOpts = append(stateOpts, tools.WithMainTools())

			// Initialize skills (discovery happens inside WithSkillTool)
			stateOpts = append(stateOpts, tools.WithSkillTool())

		}

		// Generate session ID (use resume ID if available, otherwise new ID)
		sessionID := config.ResumeConvID
		if sessionID == "" {
			sessionID = convtypes.GenerateID()
		}

		appState := tools.NewBasicState(ctx, stateOpts...)

		if config.Headless {
			presenter.SetQuiet(true)

			// Configure logging for headless mode to avoid contaminating JSON output
			logger.SetLogFormat("json")
			logger.SetLogLevel("error")

			thread, err := llm.NewThread(llmConfig)
			if err != nil {
				presenter.Error(err, "Failed to create LLM thread")
				return
			}
			thread.SetState(appState)
			thread.SetConversationID(sessionID)
			thread.EnablePersistence(ctx, !config.NoSave)
			if goalUpdate != nil {
				addRunGoalDisplay(thread, goalUpdate)
			} else {
				addRunMessageDisplay(thread, query, config)
			}

			streamer, closeFunc, err := llm.NewConversationStreamer(ctx)
			if err != nil {
				presenter.Error(err, "Failed to create conversation streamer")
				return
			}
			defer closeFunc()

			conversationID := thread.GetConversationID()
			done := make(chan error, 1)

			go func() {
				var handler llmtypes.MessageHandler
				if config.StreamDeltas {
					handler = llmtypes.NewHeadlessStreamHandler(conversationID)
				} else {
					handler = &llmtypes.ConsoleMessageHandler{Silent: true}
				}
				_, err := thread.SendMessage(ctx, query, handler, llmtypes.MessageOpt{
					PromptCache:  true,
					Images:       config.Images,
					MaxTurns:     config.MaxTurns,
					CompactRatio: llmConfig.CompactRatio,
					UseWeakModel: config.UseWeakModel,
				})
				done <- err
			}()

			streamCtx, cancel := context.WithCancel(ctx)
			defer cancel()

			liveUpdateInterval := 200 * time.Millisecond
			streamOpts := conversations.StreamOpts{
				Interval:       liveUpdateInterval,
				IncludeHistory: config.IncludeHistory,
				New:            config.ResumeConvID == "",
			}
			streamDone := make(chan error, 1)
			go func() {
				streamDone <- streamer.StreamLiveUpdates(streamCtx, conversationID, streamOpts)
			}()

			select {
			case err := <-done:
				if err != nil {
					logger.G(ctx).WithError(err).Error("Error processing query")
				}
				// Give streaming time to catch final messages (2 polling cycles)
				time.Sleep(2 * liveUpdateInterval)
				cancel()
				<-streamDone
			case err := <-streamDone:
				if err != nil && err != context.Canceled {
					logger.G(ctx).WithError(err).Error("Error streaming updates")
				}
			}
		} else {
			if config.ResultOnly {
				presenter.SetQuiet(true)
				logger.SetLogLevel("error")
			}

			handler := &llmtypes.ConsoleMessageHandler{Silent: config.ResultOnly}
			thread, err := llm.NewThread(llmConfig)
			if err != nil {
				presenter.Error(err, "Failed to create LLM thread")
				return
			}
			thread.SetState(appState)
			thread.SetConversationID(sessionID)

			if config.ResumeConvID != "" && !config.ResultOnly {
				presenter.Info(fmt.Sprintf("Resuming conversation: %s", config.ResumeConvID))
			}

			thread.EnablePersistence(ctx, !config.NoSave)
			if goalUpdate != nil {
				addRunGoalDisplay(thread, goalUpdate)
			} else {
				addRunMessageDisplay(thread, query, config)
			}

			finalOutput, err := thread.SendMessage(ctx, query, handler, llmtypes.MessageOpt{
				PromptCache:  true,
				Images:       config.Images,
				MaxTurns:     config.MaxTurns,
				CompactRatio: llmConfig.CompactRatio,
				UseWeakModel: config.UseWeakModel,
			})
			if err != nil {
				presenter.Error(err, "Failed to process query")
				return
			}

			if config.ResultOnly {
				fmt.Println(finalOutput)
				return
			}

			usage := thread.GetUsage()
			usageStats := presenter.ConvertUsageStats(&usage)
			presenter.Stats(usageStats)

			if thread.IsPersisted() {
				presenter.Section("Conversation Information")
				presenter.Info(fmt.Sprintf("ID: %s", thread.GetConversationID()))
				presenter.Info(fmt.Sprintf("To resume this conversation: kodelet run --resume %s", thread.GetConversationID()))
				presenter.Info(fmt.Sprintf("To delete this conversation: kodelet conversation delete %s", thread.GetConversationID()))
			}
		}
	},
}

func init() {
	defaults := NewRunConfig()
	runCmd.Flags().String("resume", defaults.ResumeConvID, "Resume a specific conversation")
	runCmd.Flags().String("cwd", defaults.CWD, "Working directory to execute in (defaults to current shell directory for new runs)")
	runCmd.Flags().BoolP("follow", "f", defaults.Follow, "Follow the most recent conversation")
	runCmd.Flags().Bool("no-save", defaults.NoSave, "Disable conversation persistence")
	runCmd.Flags().Bool("headless", defaults.Headless, "Output structured JSON instead of console formatting")
	runCmd.Flags().Bool("stream-deltas", defaults.StreamDeltas, "Stream partial text deltas in headless mode (requires --headless)")
	runCmd.Flags().StringSliceP("image", "I", defaults.Images, "Add image input (can be used multiple times)")
	runCmd.Flags().Int("max-turns", defaults.MaxTurns, "Maximum number of agentic turns (0 for no limit)")
	runCmd.Flags().StringP("recipe", "r", defaults.FragmentName, "Use a fragment/recipe template")
	runCmd.Flags().StringToString("arg", defaults.FragmentArgs, "Arguments to pass to fragment (e.g., --arg name=John --arg occupation=Engineer)")
	runCmd.Flags().StringSlice("fragment-dirs", defaults.FragmentDirs, "Additional fragment directories (e.g., --fragment-dirs ./project-fragments --fragment-dirs ./team-fragments)")
	runCmd.Flags().Bool("include-history", defaults.IncludeHistory, "Include historical conversation data in headless streaming")
	runCmd.Flags().Bool("no-extensions", defaults.NoExtensions, "Disable extension runtime")
	runCmd.Flags().Bool("no-tools", defaults.NoTools, "Disable all tools (for simple query-response usage)")
	runCmd.Flags().Bool("enable-fs-search-tools", defaults.EnableFSSearchTools, "Enable filesystem search tools (glob_tool and grep_tool)")
	runCmd.Flags().Bool("result-only", defaults.ResultOnly, "Only print the final agent message, suppressing all intermediate output and usage statistics")
	runCmd.Flags().Bool("use-weak-model", defaults.UseWeakModel, "Use weak model for processing")
	runCmd.Flags().String("account", defaults.Account, "Anthropic subscription account alias to use (see 'kodelet accounts list')")
}

func getRunConfigFromFlags(ctx context.Context, cmd *cobra.Command) *RunConfig {
	config := NewRunConfig()

	if resumeConvID, err := cmd.Flags().GetString("resume"); err == nil {
		config.ResumeConvID = resumeConvID
	}
	if cwd, err := cmd.Flags().GetString("cwd"); err == nil {
		config.CWD = strings.TrimSpace(cwd)
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

	if noSave, err := cmd.Flags().GetBool("no-save"); err == nil {
		config.NoSave = noSave
	}
	if headless, err := cmd.Flags().GetBool("headless"); err == nil {
		config.Headless = headless
	}

	if config.NoSave && config.Headless {
		presenter.Error(errors.New("conflicting flags"), "--no-save and --headless cannot be used together (headless mode requires conversation storage)")
		os.Exit(1)
	}
	if streamDeltas, err := cmd.Flags().GetBool("stream-deltas"); err == nil {
		config.StreamDeltas = streamDeltas
	}
	if config.StreamDeltas && !config.Headless {
		presenter.Error(errors.New("invalid flags"), "--stream-deltas requires --headless mode")
		os.Exit(1)
	}
	if images, err := cmd.Flags().GetStringSlice("image"); err == nil {
		config.Images = images
	}
	if maxTurns, err := cmd.Flags().GetInt("max-turns"); err == nil {
		config.MaxTurns = max(maxTurns, 0)
	}
	if fragmentName, err := cmd.Flags().GetString("recipe"); err == nil {
		config.FragmentName = fragmentName
	}
	if fragmentArgs, err := cmd.Flags().GetStringToString("arg"); err == nil {
		config.FragmentArgs = fragmentArgs
	}
	if fragmentDirs, err := cmd.Flags().GetStringSlice("fragment-dirs"); err == nil {
		config.FragmentDirs = fragmentDirs
	}
	if includeHistory, err := cmd.Flags().GetBool("include-history"); err == nil {
		config.IncludeHistory = includeHistory
	}

	if noExtensions, err := cmd.Flags().GetBool("no-extensions"); err == nil {
		config.NoExtensions = noExtensions
	}

	if noTools, err := cmd.Flags().GetBool("no-tools"); err == nil {
		config.NoTools = noTools
	}

	if enableFSSearchTools, err := cmd.Flags().GetBool("enable-fs-search-tools"); err == nil {
		config.EnableFSSearchTools = enableFSSearchTools
	}

	if sysprompt, err := cmd.Flags().GetString("sysprompt"); err == nil {
		config.Sysprompt = sysprompt
	}

	if syspromptArgs, err := cmd.Flags().GetStringToString("sysprompt-arg"); err == nil {
		config.SyspromptArgs = syspromptArgs
	}

	if resultOnly, err := cmd.Flags().GetBool("result-only"); err == nil {
		config.ResultOnly = resultOnly
	}

	if useWeakModel, err := cmd.Flags().GetBool("use-weak-model"); err == nil {
		config.UseWeakModel = useWeakModel
	}

	if account, err := cmd.Flags().GetString("account"); err == nil {
		config.Account = account
	}

	return config
}
