package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/state"
	"github.com/spf13/cobra"
)

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Start an interactive chat session with Kodelet",
	Long:  `Start an interactive chat session with Kodelet through stdin.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Kodelet Chat Mode - Type 'exit' or 'quit' to end the session")
		fmt.Println("----------------------------------------------------------")

		state := state.NewBasicState()
		reader := bufio.NewReader(os.Stdin)

		for {
			fmt.Print(color("[user]: "))
			input, err := reader.ReadString('\n')
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error reading input: %s\n", err)
				continue
			}

			// Trim whitespace and newlines
			input = strings.TrimSpace(input)

			// Check for exit commands
			if input == "exit" || input == "quit" {
				fmt.Println("Exiting chat mode. Goodbye!")
				return
			}

			// Skip empty inputs
			if input == "" {
				continue
			}

			// Process the query
			ask(context.Background(), state, input)
		}
	},
}
