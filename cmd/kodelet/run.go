package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/spf13/cobra"
)

// RunConfig holds configuration for the run command
type RunConfig struct {
	ResumeConvID       string
	Follow             bool
	NoSave             bool
	Images             []string // Image paths or URLs to include with the message
	MaxTurns           int      // Maximum number of turns within a single SendMessage call
	EnableBrowserTools bool     // Enable browser automation tools
	CompactRatio       float64  // Ratio of context window at which to trigger auto-compact (0.0-1.0)
	DisableAutoCompact bool     // Disable auto-compact functionality
}

// NewRunConfig creates a new RunConfig with default values
func NewRunConfig() *RunConfig {
	return &RunConfig{
		ResumeConvID:       "",
		Follow:             false,
		NoSave:             false,
		Images:             []string{},
		MaxTurns:           50, // Default to 50 turns
		EnableBrowserTools: false,
		CompactRatio:       0.8, // Default to 80% context window utilization
		DisableAutoCompact: false,
	}
}

var runCmd = &cobra.Command{
	Use:   "run [query]",
	Short: "Execute a one-shot query with Kodelet",
	Long:  `Execute a one-shot query with Kodelet and return the result.`,
	Args:  cobra.MinimumNArgs(0),
	Run: func(cmd *cobra.Command, args []string) {
		// Create a cancellable context that listens for signals
		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()

		// Get run config from flags
		config := getRunConfigFromFlags(ctx, cmd)

		// Set up signal handling
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-sigCh
			presenter.Warning("Cancellation requested, shutting down...")
			cancel()
		}()

		// Check if there's input from stdin (pipe)
		stat, _ := os.Stdin.Stat()
		isPipe := (stat.Mode() & os.ModeCharDevice) == 0

		var query string
		if isPipe {
			// Read from stdin
			stdinBytes, err := io.ReadAll(os.Stdin)
			if err != nil {
				presenter.Error(err, "Failed to read from stdin")
				return
			}

			stdinContent := string(stdinBytes)

			// If there are command line args, prepend them to the stdin content
			if len(args) > 0 {
				argsContent := strings.Join(args, " ")
				query = argsContent + "\n" + stdinContent
			} else {
				// If no args, just use stdin content
				query = stdinContent
			}
		} else {
			// No pipe, just use args
			if len(args) == 0 {
				presenter.Error(fmt.Errorf("no query provided"), "Please provide a query to execute")
				return
			}
			query = strings.Join(args, " ")
		}

		// Create the MCP manager from Viper configuration
		mcpManager, err := tools.CreateMCPManagerFromViper(ctx)
		if err != nil {
			presenter.Error(err, "Failed to create MCP manager")
			return
		}

		// Get LLM config
		llmConfig := llm.GetConfigFromViper()

		// Create state with appropriate tools based on browser support
		var stateOpts []tools.BasicStateOption
		stateOpts = append(stateOpts, tools.WithLLMConfig(llmConfig))
		stateOpts = append(stateOpts, tools.WithMCPTools(mcpManager))
		if config.EnableBrowserTools {
			stateOpts = append(stateOpts, tools.WithMainToolsAndBrowser())
		}
		appState := tools.NewBasicState(ctx, stateOpts...)

		// Print the user query
		fmt.Printf("\033[1;33m[user]: \033[0m%s\n", query)

		// Process the query using the Thread abstraction
		handler := &llmtypes.ConsoleMessageHandler{Silent: false}
		thread, err := llm.NewThread(llmConfig)
		if err != nil {
			presenter.Error(err, "Failed to create LLM thread")
			return
		}
		thread.SetState(appState)

		// Configure conversation persistence
		if config.ResumeConvID != "" {
			thread.SetConversationID(config.ResumeConvID)
			presenter.Info(fmt.Sprintf("Resuming conversation: %s", config.ResumeConvID))
		}

		thread.EnablePersistence(ctx, !config.NoSave)

		// Send the message and process the response
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

		// Display usage statistics
		usage := thread.GetUsage()
		usageStats := presenter.ConvertUsageStats(&usage)
		presenter.Stats(usageStats)

		// Display conversation ID if persistence was enabled
		if thread.IsPersisted() {
			presenter.Section("Conversation Information")
			presenter.Info(fmt.Sprintf("ID: %s", thread.GetConversationID()))
			presenter.Info(fmt.Sprintf("To resume this conversation: kodelet run --resume %s", thread.GetConversationID()))
			presenter.Info(fmt.Sprintf("To delete this conversation: kodelet conversation delete %s", thread.GetConversationID()))
		}
	},
}

func init() {
	defaults := NewRunConfig()
	runCmd.Flags().String("resume", defaults.ResumeConvID, "Resume a specific conversation")
	runCmd.Flags().BoolP("follow", "f", defaults.Follow, "Follow the most recent conversation")
	runCmd.Flags().Bool("no-save", defaults.NoSave, "Disable conversation persistence")
	runCmd.Flags().StringSliceP("image", "I", defaults.Images, "Add image input (can be used multiple times)")
	runCmd.Flags().Int("max-turns", defaults.MaxTurns, "Maximum number of turns within a single message exchange (0 for no limit)")
	runCmd.Flags().Bool("enable-browser-tools", defaults.EnableBrowserTools, "Enable browser automation tools (navigate, click, type, screenshot, etc.)")
	runCmd.Flags().Float64("compact-ratio", defaults.CompactRatio, "Context window utilization ratio to trigger auto-compact (0.0-1.0)")
	runCmd.Flags().Bool("disable-auto-compact", defaults.DisableAutoCompact, "Disable auto-compact functionality")
}

// getRunConfigFromFlags extracts run configuration from command flags
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
			presenter.Error(fmt.Errorf("conflicting flags"), "--follow and --resume cannot be used together")
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
	if enableBrowserTools, err := cmd.Flags().GetBool("enable-browser-tools"); err == nil {
		config.EnableBrowserTools = enableBrowserTools
	}
	if compactRatio, err := cmd.Flags().GetFloat64("compact-ratio"); err == nil {
		// Validate compact ratio is between 0.0 and 1.0
		if compactRatio < 0.0 || compactRatio > 1.0 {
			presenter.Error(fmt.Errorf("invalid compact-ratio"), "compact-ratio must be between 0.0 and 1.0")
			os.Exit(1)
		}
		config.CompactRatio = compactRatio
	}
	if disableAutoCompact, err := cmd.Flags().GetBool("disable-auto-compact"); err == nil {
		config.DisableAutoCompact = disableAutoCompact
	}

	return config
}
