package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// plainChatUI implements the plain CLI interface
func plainChatUI(ctx context.Context, options *ChatOptions) {
	presenter.Section("Kodelet Chat Mode (Plain UI)")
	presenter.Info("Type 'exit' or 'quit' to end the session")
	presenter.Separator()

	// Create a persistent thread with state
	config := llm.GetConfigFromViper()
	thread := llm.NewThread(config)

	// Create the MCP manager from Viper configuration
	mcpManager, err := tools.CreateMCPManagerFromViper(ctx)
	if err != nil {
		presenter.Error(err, "Failed to create MCP manager")
		return
	}

	// Create state with appropriate tools based on browser support
	var stateOpts []tools.BasicStateOption
	stateOpts = append(stateOpts, tools.WithLLMConfig(config))
	stateOpts = append(stateOpts, tools.WithMCPTools(mcpManager))
	if options.enableBrowserTools {
		stateOpts = append(stateOpts, tools.WithMainToolsAndBrowser())
	}
	thread.SetState(tools.NewBasicState(ctx, stateOpts...))

	// Configure conversation persistence
	if options.resumeConvID != "" {
		thread.SetConversationID(options.resumeConvID)
		presenter.Info(fmt.Sprintf("Resuming conversation: %s", options.resumeConvID))
	}

	thread.EnablePersistence(!options.noSave)

	if !options.noSave {
		presenter.Info("Conversation persistence is enabled")
	} else {
		presenter.Info("Conversation persistence is disabled (--no-save)")
	}

	// Create a console handler
	handler := &llmtypes.ConsoleMessageHandler{Silent: false}

	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Print("\033[1;33m[user]: \033[0m")
		input, err := reader.ReadString('\n')
		if err != nil {
			presenter.Error(err, "Error reading input")
			continue
		}

		// Trim whitespace and newlines
		input = strings.TrimSpace(input)

		// Check for exit commands
		if input == "exit" || input == "quit" {
			// Display final usage statistics before exiting
			usage := thread.GetUsage()
			presenter.Separator()

			// Convert and display usage statistics using presenter
			usageStats := presenter.ConvertUsageStats(&usage)
			presenter.Stats(usageStats)

			// Display conversation ID if persistence was enabled
			if thread.IsPersisted() {
				presenter.Section("Conversation Information")
				presenter.Info(fmt.Sprintf("Conversation ID: %s", thread.GetConversationID()))
				presenter.Info(fmt.Sprintf("To resume this conversation: kodelet chat --resume %s", thread.GetConversationID()))
				presenter.Info(fmt.Sprintf("To delete this conversation: kodelet conversation delete %s", thread.GetConversationID()))
			}

			presenter.Success("Exiting chat mode. Goodbye!")
			return
		}

		// Skip empty inputs
		if input == "" {
			continue
		}

		// Process the query using the persistent thread
		_, err = thread.SendMessage(ctx, input, handler, llmtypes.MessageOpt{
			PromptCache: true,
			MaxTurns:    options.maxTurns,
		})
		if err != nil {
			presenter.Error(err, "Failed to process message")
		}
	}
}
