package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/state"
)

// legacyChatUI implements the original CLI interface
func legacyChatUI() {
	fmt.Println("Kodelet Chat Mode (Legacy UI) - Type 'exit' or 'quit' to end the session")
	fmt.Println("----------------------------------------------------------")

	// Create a persistent thread with state
	thread := llm.NewThread(llm.GetConfigFromViper())
	thread.SetState(state.NewBasicState())

	// Create a console handler
	handler := &llm.ConsoleMessageHandler{Silent: false}

	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Print("\033[1;33m[user]: \033[0m")
		input, err := reader.ReadString('\n')
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading input: %s\n", err)
			continue
		}

		// Trim whitespace and newlines
		input = strings.TrimSpace(input)

		// Check for exit commands
		if input == "exit" || input == "quit" {
			// Display final usage statistics before exiting
			usage := thread.GetUsage()
			fmt.Printf("\n\033[1;36m[Usage Stats] Input tokens: %d | Output tokens: %d | Cache write: %d | Cache read: %d | Total: %d\033[0m\n",
				usage.InputTokens, usage.OutputTokens, usage.CacheCreationInputTokens, usage.CacheReadInputTokens, usage.TotalTokens)

			// Display cost information
			fmt.Printf("\033[1;36m[Cost Stats] Input: $%.4f | Output: $%.4f | Cache write: $%.4f | Cache read: $%.4f | Total: $%.4f\033[0m\n",
				usage.InputCost, usage.OutputCost, usage.CacheCreationCost, usage.CacheReadCost, usage.TotalCost)

			fmt.Println("Exiting chat mode. Goodbye!")
			return
		}

		// Skip empty inputs
		if input == "" {
			continue
		}

		// Process the query using the persistent thread
		err = thread.SendMessage(context.Background(), input, handler)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
	}
}
