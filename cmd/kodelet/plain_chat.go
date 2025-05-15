package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// plainChatUI implements the plain CLI interface
func plainChatUI(ctx context.Context, options *ChatOptions) {
	fmt.Println("Kodelet Chat Mode (Plain UI) - Type 'exit' or 'quit' to end the session")
	fmt.Println("----------------------------------------------------------")

	// Create a persistent thread with state
	thread := llm.NewThread(llm.GetConfigFromViper())
	thread.SetState(tools.NewBasicState())

	// Configure conversation persistence
	if options.resumeConvID != "" {
		thread.SetConversationID(options.resumeConvID)
		fmt.Printf("Resuming conversation: %s\n", options.resumeConvID)
	}

	thread.EnablePersistence(!options.noSave)

	if !options.noSave {
		fmt.Println("Conversation persistence is enabled.")
	} else {
		fmt.Println("Conversation persistence is disabled (--no-save).")
	}

	// Create a console handler
	handler := &llmtypes.ConsoleMessageHandler{Silent: false}

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
				usage.InputTokens, usage.OutputTokens, usage.CacheCreationInputTokens, usage.CacheReadInputTokens, usage.TotalTokens())

			// Display cost information
			fmt.Printf("\033[1;36m[Cost Stats] Input: $%.4f | Output: $%.4f | Cache write: $%.4f | Cache read: $%.4f | Total: $%.4f\033[0m\n",
				usage.InputCost, usage.OutputCost, usage.CacheCreationCost, usage.CacheReadCost, usage.TotalCost())

			// Display conversation ID if persistence was enabled
			if thread.IsPersisted() {
				fmt.Printf("\033[1;36m[Conversation] ID: %s\033[0m\n", thread.GetConversationID())
				fmt.Printf("To resume this conversation: kodelet chat --resume %s\n", thread.GetConversationID())
				fmt.Printf("To delete this conversation: kodelet chat delete %s\n", thread.GetConversationID())
			}

			fmt.Println("Exiting chat mode. Goodbye!")
			return
		}

		// Skip empty inputs
		if input == "" {
			continue
		}

		// Process the query using the persistent thread
		_, err = thread.SendMessage(ctx, input, handler, llmtypes.MessageOpt{
			PromptCache: true,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
	}
}
