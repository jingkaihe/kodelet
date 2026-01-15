package main

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/fragments"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/mcp"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/jingkaihe/kodelet/pkg/skills"
	"github.com/jingkaihe/kodelet/pkg/tools"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type RunConfig struct {
	ResumeConvID       string
	Follow             bool
	NoSave             bool
	Headless           bool              // Use structured JSON output instead of console formatting
	Images             []string          // Image paths or URLs to include with the message
	MaxTurns           int               // Maximum number of turns within a single SendMessage call
	CompactRatio       float64           // Ratio of context window at which to trigger auto-compact (0.0-1.0)
	DisableAutoCompact bool              // Disable auto-compact functionality
	FragmentName       string            // Name of fragment to use
	FragmentArgs       map[string]string // Arguments to pass to fragment
	FragmentDirs       []string          // Additional fragment directories
	IncludeHistory     bool              // Include historical conversation data in headless streaming
	NoSkills           bool              // Disable agentic skills
	NoHooks            bool              // Disable agent lifecycle hooks
	NoMCP              bool              // Disable MCP tools
	ResultOnly         bool              // Only print the final agent message, no intermediate output or usage stats
	UseWeakModel       bool              // Use weak model for SendMessage
	Account            string            // Anthropic subscription account alias to use
}

func NewRunConfig() *RunConfig {
	return &RunConfig{
		ResumeConvID:       "",
		Follow:             false,
		NoSave:             false,
		Headless:           false,
		Images:             []string{},
		MaxTurns:           50,
		CompactRatio:       0.8,
		DisableAutoCompact: false,
		FragmentName:       "",
		FragmentArgs:       make(map[string]string),
		FragmentDirs:       []string{},
		IncludeHistory:     false,
		NoSkills:           false,
		NoHooks:            false,
		NoMCP:              false,
		ResultOnly:         false,
		UseWeakModel:       false,
		Account:            "",
	}
}

func processFragment(ctx context.Context, config *RunConfig, args []string) (string, *fragments.Metadata, error) {
	var validDirs []string
	for _, dir := range config.FragmentDirs {
		trimmed := strings.TrimSpace(dir)
		if trimmed != "" {
			validDirs = append(validDirs, trimmed)
		}
	}

	fragmentProcessor, err := fragments.NewFragmentProcessor(fragments.WithAdditionalDirs(validDirs...))
	if err != nil {
		return "", nil, errors.Wrap(err, "failed to create fragment processor")
	}

	fragmentArgs := make(map[string]string)
	maps.Copy(fragmentArgs, config.FragmentArgs)

	customToolsConfig := tools.LoadCustomToolConfig()
	fragmentArgs["custom_tools_local_dir"] = customToolsConfig.LocalDir
	fragmentArgs["custom_tools_global_dir"] = customToolsConfig.GlobalDir

	fragmentConfig := &fragments.Config{
		FragmentName: config.FragmentName,
		Arguments:    fragmentArgs,
	}

	fragment, err := fragmentProcessor.LoadFragment(ctx, fragmentConfig)
	if err != nil {
		return "", nil, errors.Wrap(err, "failed to load fragment")
	}

	var query string
	if len(args) > 0 {
		argsContent := strings.Join(args, " ")
		query = fragment.Content + "\n" + argsContent
	} else {
		query = fragment.Content
	}

	return query, &fragment.Metadata, nil
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
		var err error

		if config.FragmentName != "" {
			query, fragmentMetadata, err = processFragment(ctx, config, args)
			if err != nil {
				presenter.Error(err, "Failed to process fragment")
				return
			}
		} else {
			query, err = getQueryFromStdinOrArgs(args)
			if err != nil {
				presenter.Error(err, "Please provide a query to execute")
				return
			}
		}

		var mcpManager *tools.MCPManager
		if !config.NoMCP {
			mcpManager, err = tools.CreateMCPManagerFromViper(ctx)
			if err != nil && !errors.Is(err, tools.ErrMCPDisabled) {
				presenter.Error(err, "Failed to create MCP manager")
				return
			}
		}

		customManager, err := tools.CreateCustomToolManagerFromViper(ctx)
		if err != nil {
			presenter.Error(err, "Failed to create custom tool manager")
			return
		}

		llmConfig, err := llm.GetConfigFromViperWithCmd(cmd)
		if err != nil {
			presenter.Error(err, "Failed to load configuration")
			return
		}

		llmConfig.NoHooks = config.NoHooks

		// Set Anthropic account if specified
		if config.Account != "" {
			// Validate the account exists
			if _, err := auth.GetAnthropicCredentialsByAlias(config.Account); err != nil {
				presenter.Error(err, fmt.Sprintf("Account '%s' not found. Run 'kodelet accounts list' to see available accounts", config.Account))
				return
			}
			llmConfig.AnthropicAccount = config.Account
		}

		applyFragmentRestrictions(&llmConfig, fragmentMetadata)

		// Set InvokedRecipe if a fragment/recipe was used
		if config.FragmentName != "" {
			llmConfig.InvokedRecipe = config.FragmentName
		}

		// Check if MCP code execution mode is enabled
		executionMode := viper.GetString("mcp.execution_mode")
		workspaceDir := viper.GetString("mcp.code_execution.workspace_dir")
		if workspaceDir == "" {
			workspaceDir = ".kodelet/mcp"
		}

		// Set MCP configuration in llmConfig for system prompt
		llmConfig.MCPExecutionMode = executionMode
		llmConfig.MCPWorkspaceDir = workspaceDir

		var stateOpts []tools.BasicStateOption
		stateOpts = append(stateOpts, tools.WithLLMConfig(llmConfig))
		stateOpts = append(stateOpts, tools.WithCustomTools(customManager))
		stateOpts = append(stateOpts, tools.WithMainTools())

		// Initialize skills
		discoveredSkills, skillsEnabled := skills.Initialize(ctx, llmConfig)
		stateOpts = append(stateOpts, tools.WithSkillTool(discoveredSkills, skillsEnabled))

		// Generate session ID for MCP socket (use resume ID if available, otherwise new ID)
		sessionID := config.ResumeConvID
		if sessionID == "" {
			sessionID = convtypes.GenerateID()
		}

		// Set up MCP execution mode
		if mcpManager != nil {
			mcpSetup, err := mcp.SetupExecutionMode(ctx, mcpManager, sessionID)
			if err != nil && !errors.Is(err, mcp.ErrDirectMode) {
				presenter.Error(err, "Failed to set up MCP execution mode")
				return
			}

			if err == nil && mcpSetup != nil {
				// Code execution mode
				stateOpts = append(stateOpts, mcpSetup.StateOpts...)
			} else {
				// Direct mode - add MCP tools directly
				stateOpts = append(stateOpts, tools.WithMCPTools(mcpManager))
			}
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

			streamer, closeFunc, err := llm.NewConversationStreamer(ctx)
			if err != nil {
				presenter.Error(err, "Failed to create conversation streamer")
				return
			}
			defer closeFunc()

			conversationID := thread.GetConversationID()
			done := make(chan error, 1)

			go func() {
				handler := &llmtypes.ConsoleMessageHandler{Silent: true}
				_, err := thread.SendMessage(ctx, query, handler, llmtypes.MessageOpt{
					PromptCache:        true,
					Images:             config.Images,
					MaxTurns:           config.MaxTurns,
					CompactRatio:       config.CompactRatio,
					DisableAutoCompact: config.DisableAutoCompact,
					UseWeakModel:       config.UseWeakModel,
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

			finalOutput, err := thread.SendMessage(ctx, query, handler, llmtypes.MessageOpt{
				PromptCache:        true,
				Images:             config.Images,
				MaxTurns:           config.MaxTurns,
				CompactRatio:       config.CompactRatio,
				DisableAutoCompact: config.DisableAutoCompact,
				UseWeakModel:       config.UseWeakModel,
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
	runCmd.Flags().BoolP("follow", "f", defaults.Follow, "Follow the most recent conversation")
	runCmd.Flags().Bool("no-save", defaults.NoSave, "Disable conversation persistence")
	runCmd.Flags().Bool("headless", defaults.Headless, "Output structured JSON instead of console formatting")
	runCmd.Flags().StringSliceP("image", "I", defaults.Images, "Add image input (can be used multiple times)")
	runCmd.Flags().Int("max-turns", defaults.MaxTurns, "Maximum number of turns within a single message exchange (0 for no limit)")
	runCmd.Flags().Float64("compact-ratio", defaults.CompactRatio, "Context window utilization ratio to trigger auto-compact (0.0-1.0)")
	runCmd.Flags().Bool("disable-auto-compact", defaults.DisableAutoCompact, "Disable auto-compact functionality")
	runCmd.Flags().StringP("recipe", "r", defaults.FragmentName, "Use a fragment/recipe template")
	runCmd.Flags().StringToString("arg", defaults.FragmentArgs, "Arguments to pass to fragment (e.g., --arg name=John --arg occupation=Engineer)")
	runCmd.Flags().StringSlice("fragment-dirs", defaults.FragmentDirs, "Additional fragment directories (e.g., --fragment-dirs ./project-fragments --fragment-dirs ./team-fragments)")
	runCmd.Flags().Bool("include-history", defaults.IncludeHistory, "Include historical conversation data in headless streaming")
	runCmd.Flags().Bool("no-hooks", defaults.NoHooks, "Disable agent lifecycle hooks")
	runCmd.Flags().Bool("no-mcp", defaults.NoMCP, "Disable MCP tools")
	runCmd.Flags().Bool("result-only", defaults.ResultOnly, "Only print the final agent message, suppressing all intermediate output and usage statistics")
	runCmd.Flags().Bool("use-weak-model", defaults.UseWeakModel, "Use weak model for processing")
	runCmd.Flags().String("account", defaults.Account, "Anthropic subscription account alias to use (see 'kodelet accounts list')")
}

func getRunConfigFromFlags(ctx context.Context, cmd *cobra.Command) *RunConfig {
	config := NewRunConfig()

	if resumeConvID, err := cmd.Flags().GetString("resume"); err == nil {
		config.ResumeConvID = resumeConvID
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
	if images, err := cmd.Flags().GetStringSlice("image"); err == nil {
		config.Images = images
	}
	if maxTurns, err := cmd.Flags().GetInt("max-turns"); err == nil {
		// Ensure non-negative values (treat negative as 0/no limit)
		if maxTurns < 0 {
			maxTurns = 0
		}
		config.MaxTurns = maxTurns
	}
	if compactRatio, err := cmd.Flags().GetFloat64("compact-ratio"); err == nil {
		// Validate compact ratio is between 0.0 and 1.0
		if compactRatio < 0.0 || compactRatio > 1.0 {
			presenter.Error(errors.New("invalid compact-ratio"), "compact-ratio must be between 0.0 and 1.0")
			os.Exit(1)
		}
		config.CompactRatio = compactRatio
	}
	if disableAutoCompact, err := cmd.Flags().GetBool("disable-auto-compact"); err == nil {
		config.DisableAutoCompact = disableAutoCompact
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

	if noHooks, err := cmd.Flags().GetBool("no-hooks"); err == nil {
		config.NoHooks = noHooks
	}

	if noMCP, err := cmd.Flags().GetBool("no-mcp"); err == nil {
		config.NoMCP = noMCP
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
