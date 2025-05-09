package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
func (a *AssistantClient) SendMessage(ctx context.Context, message string) ([]MessageEvent, error) {
	// Add the user message to the history
	a.messages = append(a.messages, anthropic.NewUserMessage(anthropic.NewTextBlock(message)))

	// Get the model from config
	model := viper.GetString("model")

	// Initialize the response events
	var events []MessageEvent

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
			return nil, fmt.Errorf("error sending message to Claude: %w", err)
		}

		// Add the assistant message to history
		a.messages = append(a.messages, claudeResponse.ToParam())

		// Process the response content
		toolEvents := []MessageEvent{}
		for _, block := range claudeResponse.Content {
			switch variant := block.AsAny().(type) {
			case anthropic.TextBlock:
				events = append(events, MessageEvent{
					Type:    EventTypeText,
					Content: variant.Text,
				})
			case anthropic.ToolUseBlock:
				toolName := block.Name
				inputJSON, _ := json.Marshal(variant.JSON.Input.Raw())
				
				// Add the tool use event
				events = append(events, MessageEvent{
					Type:    EventTypeToolUse,
					Content: fmt.Sprintf("%s: %s", toolName, string(inputJSON)),
				})
				
				// Run the tool
				output := tools.RunTool(ctx, a.state, toolName, string(variant.JSON.Input.Raw()))
				
				// Add the tool result event
				toolEvents = append(toolEvents, MessageEvent{
					Type:    EventTypeToolResult,
					Content: output.String(),
				})
				
				// Add the tool result to the messages for Claude
				a.messages = append(a.messages, anthropic.NewUserMessage(
					anthropic.NewToolResultBlock(block.ID, output.String(), false),
				))
			}
		}
		
		// Add all tool events after we've processed all blocks
		events = append(events, toolEvents...)
		
		// If no tool was used, we're done
		if len(toolEvents) == 0 {
			break
		}
	}

	return events, nil
}

// MessageEvent represents an event from processing a message
type MessageEvent struct {
	Type    string
	Content string
}

// Event types
const (
	EventTypeText       = "text"
	EventTypeToolUse    = "tool_use"
	EventTypeToolResult = "tool_result"
)

// ProcessAssistantEvents processes the events from the assistant
// and returns a formatted message
func ProcessAssistantEvents(events []MessageEvent) string {
	var parts []string
	
	for _, event := range events {
		switch event.Type {
		case EventTypeText:
			parts = append(parts, event.Content)
		case EventTypeToolUse:
			parts = append(parts, fmt.Sprintf("🔧 Using tool: %s", event.Content))
		case EventTypeToolResult:
			parts = append(parts, fmt.Sprintf("🔄 Tool result: %s", event.Content))
		}
	}
	
	return strings.Join(parts, "\n\n")
}