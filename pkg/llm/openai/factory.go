// Package openai provides OpenAI API client implementations.
// This file contains the factory function for creating OpenAI threads.
package openai

import (
	"context"
	"os"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/logger"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"

	"github.com/jingkaihe/kodelet/pkg/llm/openai/responses"
)

// NewThread creates a new OpenAI thread based on the configuration.
// It dispatches between the Chat Completions API and the Responses API
// based on the UseResponsesAPI configuration setting.
//
// The API selection follows this priority:
// 1. KODELET_OPENAI_USE_RESPONSES_API environment variable (if set)
// 2. config.OpenAI.UseResponsesAPI configuration setting
// 3. Default: Chat Completions API
func NewThread(config llmtypes.Config, subagentContextFactory llmtypes.SubagentContextFactory) (llmtypes.Thread, error) {
	log := logger.G(context.Background())

	// Check if we should use the Responses API
	useResponsesAPI := shouldUseResponsesAPI(config)

	log.WithField("use_responses_api", useResponsesAPI).
		WithField("config_openai_set", config.OpenAI != nil).
		Debug("OpenAI factory dispatching to API implementation")

	if useResponsesAPI {
		log.Debug("using OpenAI Responses API")
		return responses.NewThread(config, subagentContextFactory)
	}

	// Default to Chat Completions API
	log.Debug("using OpenAI Chat Completions API")
	return NewOpenAIThread(config, subagentContextFactory)
}

// shouldUseResponsesAPI determines whether to use the Responses API based on configuration.
func shouldUseResponsesAPI(config llmtypes.Config) bool {
	// Environment variable takes precedence
	if envValue := os.Getenv("KODELET_OPENAI_USE_RESPONSES_API"); envValue != "" {
		return strings.EqualFold(envValue, "true") || envValue == "1"
	}

	// Check config setting
	if config.OpenAI != nil {
		return config.OpenAI.UseResponsesAPI
	}

	return false
}

// ExtractResponsesMessages extracts messages from Responses API conversation data.
// This is a wrapper around the responses package's ExtractMessages function.
func ExtractResponsesMessages(rawMessages []byte, toolResults map[string]tooltypes.StructuredToolResult) ([]llmtypes.Message, error) {
	return responses.ExtractMessages(rawMessages, toolResults)
}

// StreamResponsesMessages parses raw Responses API messages into streamable format.
// This is a wrapper around the responses package's StreamMessages function.
func StreamResponsesMessages(rawMessages []byte, toolResults map[string]tooltypes.StructuredToolResult) ([]responses.StreamableMessage, error) {
	return responses.StreamMessages(rawMessages, toolResults)
}
