package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/jingkaihe/kodelet/pkg/state"
	"github.com/jingkaihe/kodelet/pkg/sysprompt"
	"github.com/jingkaihe/kodelet/pkg/tools"
)

// AnthropicThread implements the Thread interface using Anthropic's Claude API
type AnthropicThread struct {
	client   anthropic.Client
	config   Config
	state    state.State
	messages []anthropic.MessageParam
	usage    Usage
}

// NewAnthropicThread creates a new thread with Anthropic's Claude API
func NewAnthropicThread(config Config) *AnthropicThread {
	// Apply defaults if not provided
	if config.Model == "" {
		config.Model = anthropic.ModelClaude3_7SonnetLatest
	}
	if config.MaxTokens == 0 {
		config.MaxTokens = 8192
	}

	return &AnthropicThread{
		client: anthropic.NewClient(),
		config: config,
	}
}

// SetState sets the state for the thread
func (t *AnthropicThread) SetState(s state.State) {
	t.state = s
}

// GetState returns the current state of the thread
func (t *AnthropicThread) GetState() state.State {
	return t.state
}

// AddUserMessage adds a user message to the thread
func (t *AnthropicThread) AddUserMessage(message string) {
	t.messages = append(t.messages, anthropic.NewUserMessage(anthropic.NewTextBlock(message)))
}

// SendMessage sends a message to the LLM and processes the response
func (t *AnthropicThread) SendMessage(
	ctx context.Context,
	message string,
	handler MessageHandler,
	modelOverride ...string,
) error {
	// Add the user message to history
	t.AddUserMessage(message)

	// Main interaction loop for handling tool calls
	for {
		// Determine which model to use
		model := t.config.Model
		if len(modelOverride) > 0 && modelOverride[0] != "" {
			model = modelOverride[0]
		}

		// Send request to Anthropic API
		response, err := t.client.Messages.New(ctx, anthropic.MessageNewParams{
			MaxTokens: int64(t.config.MaxTokens),
			System: []anthropic.TextBlockParam{
				{
					Text: sysprompt.SystemPrompt(model),
					CacheControl: anthropic.CacheControlEphemeralParam{
						Type: "ephemeral",
					},
				},
			},
			Messages: t.messages,
			Model:    model,
			Tools:    tools.ToAnthropicTools(tools.Tools),
		})
		if err != nil {
			return fmt.Errorf("error sending message to Anthropic: %w", err)
		}

		// Add the assistant response to history
		t.messages = append(t.messages, response.ToParam())

		// Track usage statistics
		t.usage.InputTokens += int(response.Usage.InputTokens)
		t.usage.OutputTokens += int(response.Usage.OutputTokens)

		t.usage.CacheCreationInputTokens += int(response.Usage.CacheCreationInputTokens)
		t.usage.CacheReadInputTokens += int(response.Usage.CacheReadInputTokens)

		t.usage.TotalTokens += t.usage.InputTokens + t.usage.OutputTokens + t.usage.CacheCreationInputTokens + t.usage.CacheReadInputTokens

		// Process the response content blocks
		toolUseCount := 0
		for _, block := range response.Content {
			switch variant := block.AsAny().(type) {
			case anthropic.TextBlock:
				handler.HandleText(variant.Text)
			case anthropic.ToolUseBlock:
				toolUseCount++
				inputJSON, _ := json.Marshal(variant.JSON.Input.Raw())
				handler.HandleToolUse(block.Name, string(inputJSON))

				// Run the tool
				output := tools.RunTool(ctx, t.state, block.Name, string(variant.JSON.Input.Raw()))
				handler.HandleToolResult(block.Name, output.String())

				// Add tool result to messages for next API call
				t.messages = append(t.messages, anthropic.NewUserMessage(
					anthropic.NewToolResultBlock(block.ID, output.String(), false),
				))
			}
		}

		// If no tools were used, we're done
		if toolUseCount == 0 {
			break
		}
	}

	handler.HandleDone()
	return nil
}

// GetUsage returns the current token usage for the thread
func (t *AnthropicThread) GetUsage() Usage {
	return t.usage
}
