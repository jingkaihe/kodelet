package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/state"
)

// legacyChatUI implements the original CLI interface
func legacyChatUI() {
	fmt.Println("Kodelet Chat Mode (Legacy UI) - Type 'exit' or 'quit' to end the session")
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
		ask(context.Background(), state, input, false)
	}
}
