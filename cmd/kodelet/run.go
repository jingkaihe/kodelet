package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:   "run [query]",
	Short: "Execute a one-shot query with Kodelet",
	Long:  `Execute a one-shot query with Kodelet and return the result.`,
	Args:  cobra.MinimumNArgs(0),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()

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

		// Create a new state for this session
		appState := tools.NewBasicState()

		// Print the user query
		fmt.Printf("\033[1;33m[user]: \033[0m%s\n", query)

		// Process the query using the Thread abstraction
		handler := &llmtypes.ConsoleMessageHandler{Silent: false}
		thread := llm.NewThread(llm.GetConfigFromViper())
		thread.SetState(appState)

		// Send the message and process the response
		_, err := thread.SendMessage(ctx, query, handler, llmtypes.MessageOpt{
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
	},
}
