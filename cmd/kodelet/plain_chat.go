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

func plainChatUI(ctx context.Context, options *ChatOptions) {
	presenter.Section("Kodelet Chat Mode (Plain UI)")
	presenter.Info("Type 'exit' or 'quit' to end the session")
	presenter.Separator()

	config, err := llm.GetConfigFromViper()
	if err != nil {
		presenter.Error(err, "Failed to load configuration")
		return
	}
	thread, err := llm.NewThread(config)
	if err != nil {
		presenter.Error(err, "Failed to create LLM thread")
		return
	}

	mcpManager, err := tools.CreateMCPManagerFromViper(ctx)
	if err != nil {
		presenter.Error(err, "Failed to create MCP manager")
		return
	}

	customManager, err := tools.CreateCustomToolManagerFromViper(ctx)
	if err != nil {
		presenter.Error(err, "Failed to create custom tool manager")
		return
	}

	var stateOpts []tools.BasicStateOption
	stateOpts = append(stateOpts, tools.WithLLMConfig(config))
	stateOpts = append(stateOpts, tools.WithMCPTools(mcpManager))
	stateOpts = append(stateOpts, tools.WithCustomTools(customManager))
	stateOpts = append(stateOpts, tools.WithMainTools())
	thread.SetState(tools.NewBasicState(ctx, stateOpts...))

	if options.resumeConvID != "" {
		thread.SetConversationID(options.resumeConvID)
		presenter.Info(fmt.Sprintf("Resuming conversation: %s", options.resumeConvID))
	}

	thread.EnablePersistence(ctx, !options.noSave)

	if !options.noSave {
		presenter.Info("Conversation persistence is enabled")
	} else {
		presenter.Info("Conversation persistence is disabled (--no-save)")
	}

	handler := &llmtypes.ConsoleMessageHandler{Silent: false}

	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Print("\033[1;33m[user]: \033[0m")
		input, err := reader.ReadString('\n')
		if err != nil {
			presenter.Error(err, "Error reading input")
			continue
		}

		input = strings.TrimSpace(input)

		if input == "exit" || input == "quit" {
			usage := thread.GetUsage()
			presenter.Separator()

			usageStats := presenter.ConvertUsageStats(&usage)
			presenter.Stats(usageStats)

			if thread.IsPersisted() {
				presenter.Section("Conversation Information")
				presenter.Info(fmt.Sprintf("Conversation ID: %s", thread.GetConversationID()))
				presenter.Info(fmt.Sprintf("To resume this conversation: kodelet chat --resume %s", thread.GetConversationID()))
				presenter.Info(fmt.Sprintf("To delete this conversation: kodelet conversation delete %s", thread.GetConversationID()))
			}

			presenter.Success("Exiting chat mode. Goodbye!")
			return
		}

		if input == "" {
			continue
		}

		_, err = thread.SendMessage(ctx, input, handler, llmtypes.MessageOpt{
			PromptCache:        true,
			MaxTurns:           options.maxTurns,
			CompactRatio:       options.compactRatio,
			DisableAutoCompact: options.disableAutoCompact,
		})
		if err != nil {
			presenter.Error(err, "Failed to process message")
		}
	}
}
