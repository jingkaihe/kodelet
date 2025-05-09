package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/state"
	"github.com/jingkaihe/kodelet/pkg/sysprompt"
	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/spf13/viper"
)

// AssistantClient handles the interaction with the LLM provider
type AssistantClient struct {
	provider llm.Provider
	state    state.State
	messages []llm.Message
}

// NewAssistantClient creates a new assistant client
func NewAssistantClient() *AssistantClient {
	// Get the configured LLM provider
	provider, err := llm.GetProviderFromConfig()
	if err != nil {
		// Fallback to default if there's an error
		// This should be improved with better error handling
		fmt.Printf("Error initializing LLM provider: %v, falling back to defaults\n", err)
	}

	return &AssistantClient{
		provider: provider,
		state:    state.NewBasicState(),
		messages: []llm.Message{},
	}
}

// SendMessage sends a message to the assistant and processes the response
func (a *AssistantClient) SendMessage(ctx context.Context, message string) ([]MessageEvent, error) {
	// Check if provider is initialized
	if a.provider == nil {
		return nil, fmt.Errorf("LLM provider not initialized")
	}

	// Add the user message to the history
	a.messages = append(a.messages, llm.Message{
		Role:    "user",
		Content: message,
	})

	// Get the model from config for system prompt
	modelName := viper.GetString("model")
	if modelName == "" {
		// Try provider-specific model
		providerName := viper.GetString("provider")
		modelName = viper.GetString(fmt.Sprintf("providers.%s.model", providerName))
	}

	// Initialize the response events
	var events []MessageEvent

	// Send the message to LLM
	for {

		// Send the message to the LLM provider
		resp, err := a.provider.SendMessage(
			ctx,
			a.messages,
			sysprompt.SystemPrompt(modelName),
			tools.Tools,
		)
		if err != nil {
			return nil, fmt.Errorf("error sending message to LLM: %w", err)
		}

		// Add the assistant message to history
		a.messages = append(a.messages, llm.Message{
			Role:    "assistant",
			Content: resp.Content,
		})

		// Add text content to events if available
		if resp.Content != "" {
			events = append(events, MessageEvent{
				Type:    EventTypeText,
				Content: resp.Content,
			})
		}

		// Process tool calls if any
		var toolResults []llm.ToolResult
		var toolEvents []MessageEvent

		for _, toolCall := range resp.ToolCalls {
			// Convert parameters to JSON for display
			paramsJSON, _ := json.Marshal(toolCall.Parameters)

			// Add the tool use event
			events = append(events, MessageEvent{
				Type:    EventTypeToolUse,
				Content: fmt.Sprintf("%s: %s", toolCall.Name, string(paramsJSON)),
			})

			// Run the tool
			output := tools.RunTool(ctx, a.state, toolCall.Name, string(paramsJSON))

			// Add the tool result event
			toolEvents = append(toolEvents, MessageEvent{
				Type:    EventTypeToolResult,
				Content: output.String(),
			})

			// Add to tool results
			toolResults = append(toolResults, llm.ToolResult{
				CallID:  toolCall.ID,
				Content: output.String(),
				Error:   output.Error != "",
			})
		}

		// Add all tool events after we've processed all blocks
		events = append(events, toolEvents...)

		// If no tool calls, we're done
		if len(toolResults) == 0 {
			break
		}

		// Add tool results to messages
		toolMessage := a.provider.AddToolResults(toolResults)
		a.messages = append(a.messages, toolMessage)
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
