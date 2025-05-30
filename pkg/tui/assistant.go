package tui

import (
	"context"
	"fmt"

	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// AssistantClient handles the interaction with the LLM thread
type AssistantClient struct {
	thread     llmtypes.Thread
	mcpManager *tools.MCPManager
	maxTurns   int
}

// NewAssistantClient creates a new assistant client
func NewAssistantClient(ctx context.Context, conversationID string, enablePersistence bool, mcpManager *tools.MCPManager, maxTurns int) *AssistantClient {
	// Create a persistent thread with config from viper
	thread := llm.NewThread(llm.GetConfigFromViper())

	state := tools.NewBasicState(ctx, tools.WithMCPTools(mcpManager))
	thread.SetState(state)

	// Configure conversation persistence
	if conversationID != "" {
		thread.SetConversationID(conversationID)
	}

	thread.EnablePersistence(enablePersistence)

	return &AssistantClient{
		thread:     thread,
		mcpManager: mcpManager,
		maxTurns:   maxTurns,
	}
}

// GetThreadMessages returns the messages from the thread
func (a *AssistantClient) GetThreadMessages() ([]llmtypes.Message, error) {
	return a.thread.GetMessages()
}

// func (a *AssistantClient) AddUserMessage(message string, imagePaths ...string) {
// 	a.thread.AddUserMessage(message, imagePaths...)
// }

func (a *AssistantClient) SaveConversation(ctx context.Context) error {
	return a.thread.SaveConversation(ctx, true)
}

// SendMessage sends a message to the assistant and processes the response
func (a *AssistantClient) SendMessage(ctx context.Context, message string, messageCh chan llmtypes.MessageEvent, imagePaths ...string) error {
	// Create a handler for channel-based events
	handler := &llmtypes.ChannelMessageHandler{MessageCh: messageCh}

	// Send the message using the persistent thread
	_, err := a.thread.SendMessage(ctx, message, handler, llmtypes.MessageOpt{
		PromptCache: true,
		Images:      imagePaths,
		MaxTurns:    a.maxTurns,
	})

	return err
}

// GetUsage returns the current token usage
func (a *AssistantClient) GetUsage() llmtypes.Usage {
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

// Close performs cleanup operations for the assistant client
func (a *AssistantClient) Close(ctx context.Context) error {
	if a.mcpManager != nil {
		return a.mcpManager.Close(ctx)
	}
	return nil
}

// ProcessAssistantEvent processes the events from the assistant
// and returns a formatted message
func ProcessAssistantEvent(event llmtypes.MessageEvent) string {
	switch event.Type {
	case llmtypes.EventTypeText:
		return event.Content
	case llmtypes.EventTypeToolUse:
		return fmt.Sprintf("🔧 Using tool: %s", event.Content)
	case llmtypes.EventTypeToolResult:
		return fmt.Sprintf("🔄 Tool result: %s", event.Content)
	case llmtypes.EventTypeThinking:
		return fmt.Sprintf("💭 Thinking: %s", event.Content)
	}

	return ""
}
