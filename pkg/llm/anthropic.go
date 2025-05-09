package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/jingkaihe/kodelet/pkg/tools"
)

// AnthropicProvider implements the Provider interface for Anthropic Claude
type AnthropicProvider struct {
	client    anthropic.Client
	model     string
	maxTokens int
}

// NewAnthropicProvider creates a new Anthropic provider
func NewAnthropicProvider(options ProviderOptions) (Provider, error) {
	if options.APIKey == "" {
		return nil, errors.New("anthropic API key is required")
	}

	client := anthropic.NewClient()
	model := options.Model
	if model == "" {
		model = anthropic.ModelClaude3_7SonnetLatest
	}

	maxTokens := options.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096 // Default max tokens
	}

	return &AnthropicProvider{
		client:    client,
		model:     model,
		maxTokens: maxTokens,
	}, nil
}

// SendMessage sends a message to Anthropic Claude and returns the response
func (p *AnthropicProvider) SendMessage(ctx context.Context, messages []Message, systemPrompt string, tools []tools.Tool) (MessageResponse, error) {
	// Convert messages to Anthropic format
	var anthropicMessages []anthropic.MessageParam

	for _, msg := range messages {
		// Handle different message types
		switch msg.Role {
		case "user":
			anthropicMessages = append(anthropicMessages, anthropic.NewUserMessage(anthropic.NewTextBlock(msg.Content)))
		case "assistant":
			anthropicMessages = append(anthropicMessages, anthropic.NewAssistantMessage(anthropic.NewTextBlock(msg.Content)))
		case "tool":
			// For tool response messages
			if msg.ToolCallID != "" {
				// Since we can't directly create a tool result, use a workaround with JSON
				toolResultJSON := fmt.Sprintf(`{
					"type": "tool_result",
					"tool_call_id": "%s",
					"content": %q
				}`, msg.ToolCallID, msg.Content)

				// Parse the JSON into an interface{} and use it as content
				var toolResultContent interface{}
				json.Unmarshal([]byte(toolResultJSON), &toolResultContent)

				// Create user message with this content
				userContent := fmt.Sprintf("Tool result: %s", msg.Content)
				anthropicMessages = append(anthropicMessages, anthropic.NewUserMessage(
					anthropic.NewTextBlock(userContent),
				))
			}
		}
	}

	// Convert tools to Anthropic format
	var anthropicTools []anthropic.ToolUnionParam
	if len(tools) > 0 {
		anthropicTools = p.ConvertTools(tools).([]anthropic.ToolUnionParam)
	}

	// Build message params
	params := anthropic.MessageNewParams{
		Model:     p.model,
		MaxTokens: int64(p.maxTokens),
		Messages:  anthropicMessages,
		System: []anthropic.TextBlockParam{
			{
				Text: systemPrompt,
			},
		},
	}

	if len(anthropicTools) > 0 {
		params.Tools = anthropicTools
	}

	// Send request to Anthropic
	resp, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return MessageResponse{}, err
	}

	// Convert response to generic format
	messageResp := MessageResponse{
		StopReason: string(resp.StopReason),
	}

	// Extract content and tool calls
	for _, block := range resp.Content {
		switch variant := block.AsAny().(type) {
		case anthropic.TextBlock:
			messageResp.Content = variant.Text
		case anthropic.ToolUseBlock:
			toolCall := ToolCall{
				ID:   variant.ID,
				Name: variant.Name,
			}

			// Convert parameters from input JSON
			var params map[string]interface{}
			if err := json.Unmarshal(variant.Input, &params); err == nil {
				toolCall.Parameters = params
			} else {
				// If parsing fails, store as a string
				toolCall.Parameters = map[string]interface{}{
					"raw": string(variant.Input),
				}
			}

			messageResp.ToolCalls = append(messageResp.ToolCalls, toolCall)
		}
	}

	return messageResp, nil
}

// AddToolResults creates a tool response message
func (p *AnthropicProvider) AddToolResults(toolResults []ToolResult) Message {
	// For Anthropic, we'll use the standard format they expect for tool results
	if len(toolResults) == 0 {
		return Message{}
	}

	// Currently returning just one tool result per message, as that's how Anthropic structures it
	result := toolResults[0]
	return Message{
		Role:       "tool",
		Content:    result.Content,
		ToolCallID: result.CallID,
	}
}

// GetAvailableModels returns the list of available Claude models
func (p *AnthropicProvider) GetAvailableModels() []string {
	return []string{
		anthropic.ModelClaude3_7SonnetLatest,
		anthropic.ModelClaude3_5SonnetLatest,
		anthropic.ModelClaude3OpusLatest,
		anthropic.ModelClaude3_5HaikuLatest,
	}
}

// ConvertTools converts the standard tools to Anthropic format
func (p *AnthropicProvider) ConvertTools(tools []tools.Tool) interface{} {
	// This is a simplified implementation that assumes tools are already in a format
	// that can be easily converted to Anthropic Tool structs
	anthropicTools := make([]anthropic.ToolUnionParam, 0, len(tools))

	for _, tool := range tools {
		name := tool.Name()
		description := tool.Description()
		parameters := tool.GenerateSchema().Properties

		// Create a tool param
		toolParam := anthropic.ToolParam{
			Name:        name,
			Description: anthropic.String(description),
		}

		// Set input schema if params exist
		if parameters != nil {
			toolParam.InputSchema = anthropic.ToolInputSchemaParam{
				Properties: parameters,
			}
		}

		anthropicTools = append(anthropicTools, anthropic.ToolUnionParam{
			OfTool: &toolParam,
		})
	}

	return anthropicTools
}
