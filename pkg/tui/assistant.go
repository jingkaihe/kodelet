package tui

import (
	"context"
	"fmt"

	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/llm/types"
	"github.com/jingkaihe/kodelet/pkg/state"
)

// AssistantClient handles the interaction with the LLM thread
type AssistantClient struct {
	thread types.Thread
}

// NewAssistantClient creates a new assistant client
func NewAssistantClient(conversationID string, enablePersistence bool) *AssistantClient {
	// Create a persistent thread with config from viper
	thread := llm.NewThread(llm.GetConfigFromViper())

	// Set default state
	thread.SetState(state.NewBasicState())

	// Configure conversation persistence
	if conversationID != "" {
		thread.SetConversationID(conversationID)
	}

	thread.EnablePersistence(enablePersistence)

	return &AssistantClient{
		thread: thread,
	}
}

func (a *AssistantClient) AddUserMessage(message string) {
	a.thread.AddUserMessage(message)
}

// SendMessage sends a message to the assistant and processes the response
func (a *AssistantClient) SendMessage(ctx context.Context, message string, messageCh chan types.MessageEvent) error {
	// Create a handler for channel-based events
	handler := &llm.ChannelMessageHandler{MessageCh: messageCh}

	// Send the message using the persistent thread
	err := a.thread.SendMessage(ctx, message, handler)

	return err
}

// GetUsage returns the current token usage
func (a *AssistantClient) GetUsage() types.Usage {
	return a.thread.GetUsage()
}

// GetConversationID returns the current conversation ID
func (a *AssistantClient) GetConversationID() string {
	return a.thread.GetConversationID()
}

// IsPersisted returns whether this thread is being persisted
func (a *AssistantClient) IsPersisted() bool {
	return a.thread.IsPersisted()
}

// ProcessAssistantEvent processes the events from the assistant
// and returns a formatted message
func ProcessAssistantEvent(event types.MessageEvent) string {
	switch event.Type {
	case types.EventTypeText:
		return event.Content
	case types.EventTypeToolUse:
		return fmt.Sprintf("🔧 Using tool: %s", event.Content)
	case types.EventTypeToolResult:
		return fmt.Sprintf("🔄 Tool result: %s", event.Content)
	}

	return ""
}
