package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/fragments"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
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

	fragmentConfig := &fragments.Config{
		FragmentName: config.FragmentName,
		Arguments:    config.FragmentArgs,
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

		mcpManager, err := tools.CreateMCPManagerFromViper(ctx)
		if err != nil {
			presenter.Error(err, "Failed to create MCP manager")
			return
		}

		customManager, err := tools.CreateCustomToolManagerFromViper(ctx)
		if err != nil {
			presenter.Error(err, "Failed to create custom tool manager")
			return
		}

		llmConfig, err := llm.GetConfigFromViper()
		if err != nil {
			presenter.Error(err, "Failed to load configuration")
			return
		}

		applyFragmentRestrictions(&llmConfig, fragmentMetadata)

		var stateOpts []tools.BasicStateOption

		stateOpts = append(stateOpts, tools.WithLLMConfig(llmConfig))
		stateOpts = append(stateOpts, tools.WithMCPTools(mcpManager))
		stateOpts = append(stateOpts, tools.WithCustomTools(customManager))
		stateOpts = append(stateOpts, tools.WithMainTools())
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

			if config.ResumeConvID != "" {
				thread.SetConversationID(config.ResumeConvID)
			}

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
			presenter.Info(fmt.Sprintf("[User]: %s", query))

			handler := &llmtypes.ConsoleMessageHandler{Silent: false}
			thread, err := llm.NewThread(llmConfig)
			if err != nil {
				presenter.Error(err, "Failed to create LLM thread")
				return
			}
			thread.SetState(appState)

			if config.ResumeConvID != "" {
				thread.SetConversationID(config.ResumeConvID)
				presenter.Info(fmt.Sprintf("Resuming conversation: %s", config.ResumeConvID))
			}

			thread.EnablePersistence(ctx, !config.NoSave)

			_, err = thread.SendMessage(ctx, query, handler, llmtypes.MessageOpt{
				PromptCache:        true,
				Images:             config.Images,
				MaxTurns:           config.MaxTurns,
				CompactRatio:       config.CompactRatio,
				DisableAutoCompact: config.DisableAutoCompact,
			})
			if err != nil {
				presenter.Error(err, "Failed to process query")
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

	return config
}
