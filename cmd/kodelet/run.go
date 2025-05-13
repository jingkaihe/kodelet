package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/llm/types"
	"github.com/jingkaihe/kodelet/pkg/state"
	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:   "run [query]",
	Short: "Execute a one-shot query with Kodelet",
	Long:  `Execute a one-shot query with Kodelet and return the result.`,
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		// Join all arguments as a single query
		query := strings.Join(args, " ")

		// Create a new state for this session
		appState := state.NewBasicState()

		// Print the user query
		fmt.Printf("\033[1;33m[user]: \033[0m%s\n", query)

		// Process the query using the Thread abstraction
		handler := &types.ConsoleMessageHandler{Silent: false}
		thread := llm.NewThread(llm.GetConfigFromViper())
		thread.SetState(appState)

		// Send the message and process the response
		err := thread.SendMessage(context.Background(), query, handler, types.MessageOpt{
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
