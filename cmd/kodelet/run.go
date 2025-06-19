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
		config := getRunConfigFromFlags(cmd)

		// Set up signal handling
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-sigCh
			fmt.Println("\n\033[1;33m[kodelet]: Cancellation requested, shutting down...\033[0m")
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
				fmt.Printf("\n\033[1;31mError reading from stdin: %v\033[0m\n", err)
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
				fmt.Printf("\n\033[1;31mError: No query provided\033[0m\n")
				return
			}
			query = strings.Join(args, " ")
		}

		// Create the MCP manager from Viper configuration
		mcpManager, err := tools.CreateMCPManagerFromViper(ctx)
		if err != nil {
			fmt.Printf("\n\033[1;31mError creating MCP manager: %v\033[0m\n", err)
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
		thread := llm.NewThread(llmConfig)
		thread.SetState(appState)

		// Configure conversation persistence
		if config.ResumeConvID != "" {
			thread.SetConversationID(config.ResumeConvID)
			fmt.Printf("Resuming conversation: %s\n", config.ResumeConvID)
		}

		thread.EnablePersistence(!config.NoSave)

		// Send the message and process the response
		_, err = thread.SendMessage(ctx, query, handler, llmtypes.MessageOpt{
			PromptCache: true,
			Images:      config.Images,
			MaxTurns:    config.MaxTurns,
		})
		if err != nil {
			fmt.Printf("\n\033[1;31mError: %v\033[0m\n", err)
			return
		}

		// Display usage statistics
		usage := thread.GetUsage()
		fmt.Printf("\n\033[1;36m[Usage Stats] Input tokens: %d | Output tokens: %d | Cache write: %d | Cache read: %d | Total: %d\033[0m\n",
			usage.InputTokens, usage.OutputTokens, usage.CacheCreationInputTokens, usage.CacheReadInputTokens, usage.TotalTokens())

		// Display cost information
		fmt.Printf("\033[1;36m[Cost Stats] Input: $%.4f | Output: $%.4f | Cache write: $%.4f | Cache read: $%.4f | Total: $%.4f\033[0m\n",
			usage.InputCost, usage.OutputCost, usage.CacheCreationCost, usage.CacheReadCost, usage.TotalCost())

		// Display conversation ID if persistence was enabled
		if thread.IsPersisted() {
			fmt.Printf("\033[1;36m[Conversation] ID: %s\033[0m\n", thread.GetConversationID())
			fmt.Printf("To resume this conversation: kodelet run --resume %s\n", thread.GetConversationID())
			fmt.Printf("To delete this conversation: kodelet conversation delete %s\n", thread.GetConversationID())
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
}

// getRunConfigFromFlags extracts run configuration from command flags
func getRunConfigFromFlags(cmd *cobra.Command) *RunConfig {
	config := NewRunConfig()

	if resumeConvID, err := cmd.Flags().GetString("resume"); err == nil {
		config.ResumeConvID = resumeConvID
	}
	if follow, err := cmd.Flags().GetBool("follow"); err == nil {
		config.Follow = follow
	}
	if config.Follow {
		if config.ResumeConvID != "" {
			fmt.Printf("Error: --auto-resume and --resume cannot be used together\n")
			os.Exit(1)
		}
		var err error
		config.ResumeConvID, err = conversations.GetMostRecentConversationID()
		if err != nil {
			fmt.Println("Warning: no conversations found, starting a new conversation")
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

	return config
}
