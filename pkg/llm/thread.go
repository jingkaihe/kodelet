// Package llm provides a unified interface for Large Language Model providers.
// It abstracts different LLM providers (Anthropic Claude, OpenAI GPT) behind
// a common Thread interface for consistent interaction patterns.
package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/llm/anthropic"
	"github.com/jingkaihe/kodelet/pkg/llm/openai"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// NewThread creates a new thread based on the model specified in the config
func NewThread(config llmtypes.Config) (llmtypes.Thread, error) {
	// If a provider is explicitly specified, use that
	if config.Provider != "" {
		switch strings.ToLower(config.Provider) {
		case "openai":
			return openai.NewOpenAIThread(config), nil
		case "anthropic":
			return anthropic.NewAnthropicThread(config)
		default:
			// If unknown provider, fall back to model name detection
		}
	}

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

	// If the model starts with "gpt" or matches OpenAI's naming conventions, use OpenAI
	case openai.IsOpenAIModel(modelName):
		return openai.NewOpenAIThread(config), nil

	// Default to Anthropic for now
	default:
		return anthropic.NewAnthropicThread(config)
	}
}

// SendMessageAndGetTextWithUsage is a convenience method for one-shot queries that returns the response as a string and usage information
func SendMessageAndGetTextWithUsage(ctx context.Context, state tooltypes.State, query string, config llmtypes.Config, silent bool, opt llmtypes.MessageOpt) (string, llmtypes.Usage) {
	thread, err := NewThread(config)
	if err != nil {
		return fmt.Sprintf("Error creating thread: %v", err), llmtypes.Usage{}
	}
	thread.SetState(state)

	handler := &llmtypes.StringCollectorHandler{Silent: silent}
	_, err = thread.SendMessage(ctx, query, handler, opt)
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
func ExtractMessages(provider string, rawMessages []byte, toolResults map[string]tooltypes.StructuredToolResult) ([]llmtypes.Message, error) {
	switch provider {
	case "anthropic":
		return anthropic.ExtractMessages(rawMessages, toolResults)
	case "openai":
		return openai.ExtractMessages(rawMessages, toolResults)
	default:
		return nil, fmt.Errorf("unsupported provider: %s", provider)
	}
}
