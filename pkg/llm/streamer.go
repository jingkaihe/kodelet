package llm

import (
	"context"
	"encoding/json"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/llm/anthropic"
	"github.com/jingkaihe/kodelet/pkg/llm/openai"
	"github.com/jingkaihe/kodelet/pkg/llm/openai/responses"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// NewConversationStreamer creates a fully configured conversation streamer
// with all provider message parsers pre-registered
func NewConversationStreamer(ctx context.Context) (streamer *conversations.ConversationStreamer, closer func() error, err error) {
	service, err := conversations.GetDefaultConversationService(ctx)
	if err != nil {
		return nil, nil, err
	}

	streamer = conversations.NewConversationStreamer(service)

	streamer.RegisterMessageParser("anthropic", func(rawMessages json.RawMessage, _ map[string]any, toolResults map[string]tooltypes.StructuredToolResult) ([]conversations.StreamableMessage, error) {
		msgs, err := anthropic.StreamMessages(rawMessages, toolResults)
		if err != nil {
			return nil, err
		}
		return convertAnthropicStreamableMessages(msgs), nil
	})

	streamer.RegisterMessageParser("openai", func(rawMessages json.RawMessage, metadata map[string]any, toolResults map[string]tooltypes.StructuredToolResult) ([]conversations.StreamableMessage, error) {
		if openai.RecordUsesResponsesMode(metadata, rawMessages) {
			msgs, err := openai.StreamResponsesMessages(rawMessages, toolResults)
			if err != nil {
				return nil, err
			}
			return convertResponsesStreamableMessages(msgs), nil
		}

		msgs, err := openai.StreamMessages(rawMessages, toolResults)
		if err != nil {
			return nil, err
		}
		return convertOpenAIStreamableMessages(msgs), nil
	})

	streamer.RegisterMessageParser("openai-responses", func(rawMessages json.RawMessage, _ map[string]any, toolResults map[string]tooltypes.StructuredToolResult) ([]conversations.StreamableMessage, error) {
		msgs, err := openai.StreamResponsesMessages(rawMessages, toolResults)
		if err != nil {
			return nil, err
		}
		return convertResponsesStreamableMessages(msgs), nil
	})

	return streamer, service.Close, nil
}

func convertAnthropicStreamableMessages(msgs []anthropic.StreamableMessage) []conversations.StreamableMessage {
	result := make([]conversations.StreamableMessage, len(msgs))
	for i, msg := range msgs {
		result[i] = conversations.StreamableMessage{
			Kind:       msg.Kind,
			Role:       msg.Role,
			Content:    msg.Content,
			ToolName:   msg.ToolName,
			ToolCallID: msg.ToolCallID,
			Input:      msg.Input,
		}
	}
	return result
}

func convertOpenAIStreamableMessages(msgs []openai.StreamableMessage) []conversations.StreamableMessage {
	result := make([]conversations.StreamableMessage, len(msgs))
	for i, msg := range msgs {
		result[i] = conversations.StreamableMessage{
			Kind:       msg.Kind,
			Role:       msg.Role,
			Content:    msg.Content,
			ToolName:   msg.ToolName,
			ToolCallID: msg.ToolCallID,
			Input:      msg.Input,
		}
	}
	return result
}

func convertResponsesStreamableMessages(msgs []responses.StreamableMessage) []conversations.StreamableMessage {
	result := make([]conversations.StreamableMessage, len(msgs))
	for i, msg := range msgs {
		result[i] = conversations.StreamableMessage{
			Kind:       msg.Kind,
			Role:       msg.Role,
			Content:    msg.Content,
			ToolName:   msg.ToolName,
			ToolCallID: msg.ToolCallID,
			Input:      msg.Input,
		}
	}
	return result
}
