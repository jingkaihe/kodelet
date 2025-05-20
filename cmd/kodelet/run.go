package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/spf13/cobra"
)

// RunOptions contains all options for the run command
type RunOptions struct {
	resumeConvID string
	noSave       bool
}

var runOptions = &RunOptions{}

var runCmd = &cobra.Command{
	Use:   "run [query]",
	Short: "Execute a one-shot query with Kodelet",
	Long:  `Execute a one-shot query with Kodelet and return the result.`,
	Args:  cobra.MinimumNArgs(0),
	Run: func(cmd *cobra.Command, args []string) {
		// Create a cancellable context that listens for signals
		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()

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

		appState := tools.NewBasicState(ctx, tools.WithMCPTools(mcpManager))

		// Print the user query
		fmt.Printf("\033[1;33m[user]: \033[0m%s\n", query)

		// Process the query using the Thread abstraction
		handler := &llmtypes.ConsoleMessageHandler{Silent: false}
		thread := llm.NewThread(llm.GetConfigFromViper())
		thread.SetState(appState)

		// Configure conversation persistence
		if runOptions.resumeConvID != "" {
			thread.SetConversationID(runOptions.resumeConvID)
			fmt.Printf("Resuming conversation: %s\n", runOptions.resumeConvID)
		}

		thread.EnablePersistence(!runOptions.noSave)

		// Send the message and process the response
		_, err = thread.SendMessage(ctx, query, handler, llmtypes.MessageOpt{
			PromptCache: true,
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
	runCmd.Flags().StringVar(&runOptions.resumeConvID, "resume", "", "Resume a specific conversation")
	runCmd.Flags().BoolVar(&runOptions.noSave, "no-save", false, "Disable conversation persistence")
}
