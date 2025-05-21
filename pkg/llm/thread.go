package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/llm/anthropic"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// NewThread creates a new thread based on the model specified in the config
func NewThread(config llmtypes.Config) llmtypes.Thread {
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
func SendMessageAndGetTextWithUsage(ctx context.Context, state tooltypes.State, query string, config llmtypes.Config, silent bool, opt llmtypes.MessageOpt) (string, llmtypes.Usage) {
	thread := NewThread(config)
	thread.SetState(state)

	handler := &llmtypes.StringCollectorHandler{Silent: silent}
	_, err := thread.SendMessage(ctx, query, handler, opt)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), llmtypes.Usage{}
	}
	return handler.CollectedText(), thread.GetUsage()
}

// SendMessageAndGetText is a convenience method for one-shot queries that returns the response as a string
func SendMessageAndGetText(ctx context.Context, state tooltypes.State, query string, config llmtypes.Config, silent bool, opt llmtypes.MessageOpt) string {
	text, _ := SendMessageAndGetTextWithUsage(ctx, state, query, config, silent, opt)
	return text
}

// ExtractMessages parses the raw messages from a conversation record
func ExtractMessages(provider string, rawMessages []byte) ([]llmtypes.Message, error) {
	if provider == "anthropic" {
		return anthropic.ExtractMessages(rawMessages)
	}

	return nil, fmt.Errorf("unsupported model type: %s", provider)
}
