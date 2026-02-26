// Package openai provides OpenAI API client implementations.
// This file contains the factory function for creating OpenAI threads.
package openai

import (
	"context"
	"encoding/json"

	"github.com/jingkaihe/kodelet/pkg/llm/openai/responses"
	"github.com/jingkaihe/kodelet/pkg/logger"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// NewThread creates a new OpenAI thread based on the configuration.
// It dispatches between the Chat Completions API and the Responses API
// based on api_mode with backward-compatible aliases.
func NewThread(config llmtypes.Config) (llmtypes.Thread, error) {
	log := logger.G(context.Background())
	apiMode := resolveAPIMode(config)

	log.WithField("api_mode", apiMode).
		WithField("platform", resolvePlatformName(config)).
		WithField("config_openai_set", config.OpenAI != nil).
		Debug("OpenAI factory dispatching to API implementation")

	if apiMode == llmtypes.OpenAIAPIModeResponses {
		log.Debug("using OpenAI Responses API")
		return responses.NewThread(config)
	}

	log.Debug("using OpenAI Chat Completions API")
	return NewOpenAIThread(config)
}

// shouldUseResponsesAPI determines whether to use the Responses API based on configuration.
func shouldUseResponsesAPI(config llmtypes.Config) bool {
	return resolveAPIMode(config) == llmtypes.OpenAIAPIModeResponses
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

// RecordUsesResponsesMode determines if a conversation should be interpreted using Responses API parsing.
func RecordUsesResponsesMode(metadata map[string]any, rawMessages []byte) bool {
	if modeRaw, ok := metadata["api_mode"]; ok {
		if mode, ok := modeRaw.(string); ok {
			if parsedMode, parsed := parseAPIMode(mode); parsed {
				return parsedMode == llmtypes.OpenAIAPIModeResponses
			}
		}
	}

	var generic []map[string]any
	if err := json.Unmarshal(rawMessages, &generic); err != nil {
		return false
	}
	if len(generic) == 0 {
		return false
	}
	if kind, ok := generic[0]["type"].(string); ok {
		switch kind {
		case "message", "reasoning", "function_call", "function_call_output", "compaction", "compaction_summary":
			return true
		}
	}

	return false
}
