package llm

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/sashabaranov/go-openai"
)

// OpenAIProvider implements the Provider interface for OpenAI
type OpenAIProvider struct {
	client    *openai.Client
	model     string
	maxTokens int
}

// NewOpenAIProvider creates a new OpenAI provider
func NewOpenAIProvider(options ProviderOptions) (Provider, error) {
	if options.APIKey == "" {
		return nil, errors.New("OpenAI API key is required")
	}

	client := openai.NewClient(options.APIKey)
	model := options.Model
	if model == "" {
		model = openai.GPT4o
	}

	maxTokens := options.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096 // Default max tokens
	}

	return &OpenAIProvider{
		client:    client,
		model:     model,
		maxTokens: maxTokens,
	}, nil
}

// SendMessage sends a message to OpenAI and returns the response
func (p *OpenAIProvider) SendMessage(ctx context.Context, messages []Message, systemPrompt string, tools []tools.Tool) (MessageResponse, error) {
	// Convert messages to OpenAI format
	openaiMessages := make([]openai.ChatCompletionMessage, 0, len(messages)+1)

	// Add system message if provided
	if systemPrompt != "" {
		openaiMessages = append(openaiMessages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: systemPrompt,
		})
	}

	// Add rest of the messages
	for _, msg := range messages {
		openaiMsg := openai.ChatCompletionMessage{
			Content: msg.Content,
		}

		// Map roles to OpenAI roles
		switch msg.Role {
		case "user":
			openaiMsg.Role = openai.ChatMessageRoleUser
		case "assistant":
			openaiMsg.Role = openai.ChatMessageRoleAssistant
		case "system":
			openaiMsg.Role = openai.ChatMessageRoleSystem
		case "tool":
			openaiMsg.Role = openai.ChatMessageRoleTool
			openaiMsg.ToolCallID = msg.ToolCallID
		}

		openaiMessages = append(openaiMessages, openaiMsg)
	}

	// Convert tools to OpenAI format
	var openaiTools []openai.Tool
	if len(tools) > 0 {
		openaiTools = p.ConvertTools(tools).([]openai.Tool)
	}

	// Build request params
	req := openai.ChatCompletionRequest{
		Model:     p.model,
		Messages:  openaiMessages,
		MaxTokens: p.maxTokens,
	}

	if len(openaiTools) > 0 {
		req.Tools = openaiTools
	}

	// Send request to OpenAI
	resp, err := p.client.CreateChatCompletion(ctx, req)
	if err != nil {
		return MessageResponse{}, err
	}

	// Convert response to generic format
	messageResp := MessageResponse{}

	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		messageResp.Content = choice.Message.Content
		messageResp.StopReason = string(choice.FinishReason)

		// Extract tool calls if any
		if len(choice.Message.ToolCalls) > 0 {
			for _, tc := range choice.Message.ToolCalls {
				toolCall := ToolCall{
					ID:         tc.ID,
					Name:       tc.Function.Name,
					Parameters: make(map[string]interface{}),
				}

				// Parse parameters as map
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &toolCall.Parameters); err != nil {
					// If parsing fails, store the raw arguments
					toolCall.Parameters["raw"] = tc.Function.Arguments
				}

				messageResp.ToolCalls = append(messageResp.ToolCalls, toolCall)
			}
		}
	}

	return messageResp, nil
}

// AddToolResults creates a tool response message
func (p *OpenAIProvider) AddToolResults(toolResults []ToolResult) Message {
	if len(toolResults) == 0 {
		return Message{}
	}

	// Currently returning just one tool result per message
	result := toolResults[0]
	return Message{
		Role:       "tool",
		Content:    result.Content,
		ToolCallID: result.CallID,
	}
}

var (
	openaiChatModels = []string{
		openai.GPT4Dot1,
		openai.GPT4Dot1Mini,
		openai.GPT4Dot1Nano,
		openai.GPT4o,
		openai.GPT4oMini,
	}
	openaiReasoningModels = []string{
		openai.O4Mini,
		openai.O3Mini,
		openai.O3,
	}
)

// GetAvailableModels returns the list of available OpenAI models
func (p *OpenAIProvider) GetAvailableModels() []string {
	models := make([]string, 0, len(openaiChatModels)+len(openaiReasoningModels))
	models = append(models, openaiChatModels...)
	models = append(models, openaiReasoningModels...)
	return models
}

func isReasoningModel(model string) bool {
	return slices.Contains(openaiReasoningModels, model)
}

// ConvertTools converts the standard tools to OpenAI format
func (p *OpenAIProvider) ConvertTools(tools []tools.Tool) interface{} {
	openaiTools := make([]openai.Tool, 0, len(tools))

	for _, tool := range tools {
		name := tool.Name()
		description := tool.Description()
		parameters := tool.GenerateSchema()

		// Create function definition
		functionDef := openai.FunctionDefinition{
			Name:        name,
			Description: description,
			Parameters:  parameters,
		}

		openaiTools = append(openaiTools, openai.Tool{
			Type:     openai.ToolTypeFunction,
			Function: &functionDef,
		})
	}

	return openaiTools
}
