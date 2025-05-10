package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/llm"
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

		// Initialize LLM client from viper config
		client := llm.NewClient(llm.GetConfigFromViper())

		// Process the query
		client.Ask(context.Background(), appState, query, false)
	},
}
