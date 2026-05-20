// Package llm provides a unified interface for Large Language Model providers.
// It abstracts different LLM providers (Anthropic Claude, OpenAI GPT) behind
// a common Thread interface for consistent interaction patterns.
package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/pkg/errors"

	"github.com/jingkaihe/kodelet/pkg/llm/anthropic"
	"github.com/jingkaihe/kodelet/pkg/llm/openai"
	"github.com/jingkaihe/kodelet/pkg/logger"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// resolveModelAlias resolves a model name through the configured aliases
// If the model name exists as an alias, returns the mapped full name
// Otherwise returns the original model name unchanged
func resolveModelAlias(modelName string, aliases map[string]string) string {
	logger.G(context.TODO()).
		WithField("modelName", modelName).
		WithField("aliases", aliases).Debug("Resolving model alias")

	if aliases == nil {
		return modelName
	}

	if resolvedName, exists := aliases[modelName]; exists {
		return resolvedName
	}

	return modelName
}

// NewThread creates a new thread based on the model specified in the config
func NewThread(config llmtypes.Config) (llmtypes.Thread, error) {
	config.Model = resolveModelAlias(config.Model, config.Aliases)

	// Create thread based on provider
	switch strings.ToLower(config.Provider) {
	case "openai":
		return openai.NewThread(config)
	case "anthropic":
		return anthropic.NewAnthropicThread(config)
	default:
		return nil, errors.Errorf("unsupported provider: %s", config.Provider)
	}
}

// SendMessageAndGetTextWithUsage is a convenience method for one-shot queries that returns the response as a string and usage information
func SendMessageAndGetTextWithUsage(ctx context.Context, state tooltypes.State, query string, config llmtypes.Config, silent bool, opt llmtypes.MessageOpt) (string, llmtypes.Usage) {
	thread, err := NewThread(config)
	if err != nil {
		return fmt.Sprintf("Error creating thread: %v", err), llmtypes.Usage{}
	}
	thread.SetState(state)
	thread.EnablePersistence(ctx, !opt.NoSaveConversation)

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
func ExtractMessages(provider string, rawMessages []byte, metadata map[string]any, toolResults map[string]tooltypes.StructuredToolResult) ([]llmtypes.Message, error) {
	var messages []llmtypes.Message
	var err error

	switch provider {
	case "anthropic":
		messages, err = anthropic.ExtractMessages(rawMessages, toolResults)
	case "openai":
		if openai.RecordUsesResponsesMode(metadata, rawMessages) {
			messages, err = openai.ExtractResponsesMessages(rawMessages, toolResults)
		} else {
			messages, err = openai.ExtractMessages(rawMessages, toolResults)
		}
	default:
		return nil, errors.Errorf("unsupported provider: %s", provider)
	}
	if err != nil {
		return nil, err
	}
	return conversations.ApplyDisplayToLLMMessages(messages, metadata), nil
}

// ExtractConversationEntries parses the raw messages from a conversation record into structured conversation entries.
func ExtractConversationEntries(provider string, rawMessages []byte, metadata map[string]any, toolResults map[string]tooltypes.StructuredToolResult) ([]conversations.StreamableMessage, error) {
	var messages []conversations.StreamableMessage
	var err error

	switch provider {
	case "anthropic":
		msgs, err := anthropic.StreamMessages(rawMessages, toolResults)
		if err != nil {
			return nil, err
		}
		messages = convertAnthropicStreamableMessages(msgs)
	case "openai":
		if openai.RecordUsesResponsesMode(metadata, rawMessages) {
			msgs, err := openai.StreamResponsesMessages(rawMessages, toolResults)
			if err != nil {
				return nil, err
			}
			messages = convertResponsesStreamableMessages(msgs)
		} else {
			msgs, err := openai.StreamMessages(rawMessages, toolResults)
			if err != nil {
				return nil, err
			}
			messages = convertOpenAIStreamableMessages(msgs)
		}
	default:
		return nil, errors.Errorf("unsupported provider: %s", provider)
	}
	if err != nil {
		return nil, err
	}
	return conversations.ApplyDisplayToStreamableMessages(messages, metadata), nil
}
