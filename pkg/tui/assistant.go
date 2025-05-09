package tui

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/jingkaihe/kodelet/pkg/state"
	"github.com/jingkaihe/kodelet/pkg/sysprompt"
	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/spf13/viper"
)

// AssistantClient handles the interaction with the Claude API
type AssistantClient struct {
	client   anthropic.Client
	state    state.State
	messages []anthropic.MessageParam
}

// NewAssistantClient creates a new assistant client
func NewAssistantClient() *AssistantClient {
	return &AssistantClient{
		client:   anthropic.NewClient(),
		state:    state.NewBasicState(),
		messages: []anthropic.MessageParam{},
	}
}

// SendMessage sends a message to the assistant and processes the response
func (a *AssistantClient) SendMessage(ctx context.Context, message string, messageCh chan MessageEvent) error {
	// Add the user message to the history
	a.messages = append(a.messages, anthropic.NewUserMessage(anthropic.NewTextBlock(message)))

	// Get the model from config
	model := viper.GetString("model")

	// Send the message to Claude
	for {
		claudeResponse, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
			MaxTokens: int64(viper.GetInt("max_tokens")),
			System: []anthropic.TextBlockParam{
				{
					Text:         sysprompt.SystemPrompt(model),
					CacheControl: anthropic.CacheControlEphemeralParam{},
				},
			},
			Messages: a.messages,
			Model:    model,
			Tools:    tools.ToAnthropicTools(tools.Tools),
		})
		if err != nil {
			return fmt.Errorf("error sending message to Claude: %w", err)
		}

		// Add the assistant message to history
		a.messages = append(a.messages, claudeResponse.ToParam())

		toolEventCnt := 0
		// Process the response content
		for _, block := range claudeResponse.Content {
			switch variant := block.AsAny().(type) {
			case anthropic.TextBlock:
				messageCh <- MessageEvent{
					Type:    EventTypeText,
					Content: variant.Text,
				}
			case anthropic.ToolUseBlock:
				toolName := block.Name
				inputJSON, _ := json.Marshal(variant.JSON.Input.Raw())
				toolEventCnt++
				// Add the tool use event
				messageCh <- MessageEvent{
					Type:    EventTypeToolUse,
					Content: fmt.Sprintf("%s: %s", toolName, string(inputJSON)),
				}

				// Run the tool
				output := tools.RunTool(ctx, a.state, toolName, string(variant.JSON.Input.Raw()))

				// Add the tool result event
				messageCh <- MessageEvent{
					Type:    EventTypeToolResult,
					Content: output.String(),
				}

				// Add the tool result to the messages for Claude
				a.messages = append(a.messages, anthropic.NewUserMessage(
					anthropic.NewToolResultBlock(block.ID, output.String(), false),
				))
			}
		}

		// If no tool was used, we're done
		if toolEventCnt == 0 {
			break
		}
	}
	messageCh <- MessageEvent{
		Type:    EventTypeText,
		Content: "Done",
		Done:    true,
	}

	return nil
}

// MessageEvent represents an event from processing a message
type MessageEvent struct {
	Type    string
	Content string
	Done    bool
}

// Event types
const (
	EventTypeText       = "text"
	EventTypeToolUse    = "tool_use"
	EventTypeToolResult = "tool_result"
)

// ProcessAssistantEvent processes the events from the assistant
// and returns a formatted message
func ProcessAssistantEvent(event MessageEvent) string {
	switch event.Type {
	case EventTypeText:
		return event.Content
	case EventTypeToolUse:
		return fmt.Sprintf("🔧 Using tool: %s", event.Content)
	case EventTypeToolResult:
		return fmt.Sprintf("🔄 Tool result: %s", event.Content)
	}

	return ""
}
