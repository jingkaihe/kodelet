package main

import (
	"context"
	"strings"

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
		println(color("[user]: ") + query)

		// Process the query
		ask(context.Background(), appState, query, false)
	},
}
