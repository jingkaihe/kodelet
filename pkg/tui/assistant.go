package tui

import (
	"context"
	"fmt"

	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/state"
)

// AssistantClient handles the interaction with the LLM thread
type AssistantClient struct {
	thread llm.Thread
}

// NewAssistantClient creates a new assistant client
func NewAssistantClient() *AssistantClient {
	// Create a persistent thread with config from viper
	thread := llm.NewThread(llm.GetConfigFromViper())

	// Set default state
	thread.SetState(state.NewBasicState())

	return &AssistantClient{
		thread: thread,
	}
}

func (a *AssistantClient) AddUserMessage(message string) {
	a.thread.AddUserMessage(message)
}

// SendMessage sends a message to the assistant and processes the response
func (a *AssistantClient) SendMessage(ctx context.Context, message string, messageCh chan llm.MessageEvent) error {
	// Create a handler for channel-based events
	handler := &llm.ChannelMessageHandler{MessageCh: messageCh}

	// Send the message using the persistent thread
	return a.thread.SendMessage(ctx, message, handler)
}

// ProcessAssistantEvent processes the events from the assistant
// and returns a formatted message
func ProcessAssistantEvent(event llm.MessageEvent) string {
	switch event.Type {
	case llm.EventTypeText:
		return event.Content
	case llm.EventTypeToolUse:
		return fmt.Sprintf("🔧 Using tool: %s", event.Content)
	case llm.EventTypeToolResult:
		return fmt.Sprintf("🔄 Tool result: %s", event.Content)
	}

	return ""
}
