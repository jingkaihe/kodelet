package llm

import (
	"context"
	"encoding/json"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/llm/anthropic"
	"github.com/jingkaihe/kodelet/pkg/llm/openai"
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

	// Register Anthropic message parser
	streamer.RegisterMessageParser("anthropic", func(rawMessages json.RawMessage, toolResults map[string]tooltypes.StructuredToolResult) ([]conversations.StreamableMessage, error) {
		msgs, err := anthropic.StreamMessages(rawMessages, toolResults)
		if err != nil {
			return nil, err
		}
		return convertAnthropicStreamableMessages(msgs), nil
	})

	// Register OpenAI message parser  
	streamer.RegisterMessageParser("openai", func(rawMessages json.RawMessage, toolResults map[string]tooltypes.StructuredToolResult) ([]conversations.StreamableMessage, error) {
		msgs, err := openai.StreamMessages(rawMessages, toolResults)
		if err != nil {
			return nil, err
		}
		return convertOpenAIStreamableMessages(msgs), nil
	})

	return streamer, service.Close, nil
}

// Convert anthropic StreamableMessage to conversations StreamableMessage
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

// Convert openai StreamableMessage to conversations StreamableMessage  
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