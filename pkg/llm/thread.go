package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/llm/anthropic"
	"github.com/jingkaihe/kodelet/pkg/llm/types"
	"github.com/jingkaihe/kodelet/pkg/state"
)

// NewThread creates a new thread based on the model specified in the config
func NewThread(config types.Config) types.Thread {
	// Determine which provider to use based on the model name
	modelName := config.Model

	// Default to Anthropic Claude if no model specified
	if modelName == "" {
		return anthropic.NewAnthropicThread(config)
	}

	// Check model name patterns to determine provider
	switch {
	// If the model starts with "claude" or matches Anthropic's constants, use Anthropic
	case strings.HasPrefix(strings.ToLower(modelName), "claude"):
		return anthropic.NewAnthropicThread(config)

	// Add cases for other providers here in the future
	// Example:
	// case strings.HasPrefix(strings.ToLower(modelName), "gpt"):
	//     return NewOpenAIThread(config)

	// Default to Anthropic for now
	default:
		return anthropic.NewAnthropicThread(config)
	}
}

// SendMessageAndGetTextWithUsage is a convenience method for one-shot queries that returns the response as a string and usage information
func SendMessageAndGetTextWithUsage(ctx context.Context, state state.State, query string, config types.Config, silent bool, opt types.MessageOpt) (string, types.Usage) {
	thread := NewThread(config)
	thread.SetState(state)

	handler := &types.StringCollectorHandler{Silent: silent}
	err := thread.SendMessage(ctx, query, handler, opt)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), types.Usage{}
	}
	return handler.CollectedText(), thread.GetUsage()
}

// SendMessageAndGetText is a convenience method for one-shot queries that returns the response as a string
func SendMessageAndGetText(ctx context.Context, state state.State, query string, config types.Config, silent bool, opt types.MessageOpt) string {
	text, _ := SendMessageAndGetTextWithUsage(ctx, state, query, config, silent, opt)
	return text
}
