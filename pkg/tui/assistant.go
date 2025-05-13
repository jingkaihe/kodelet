package tui

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/llm/anthropic"
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

// GetThreadMessages returns the messages from the thread
func (a *AssistantClient) GetThreadMessages() ([]Message, error) {
	// Get access to the underlying anthropic thread to extract messages
	if anthropicThread, ok := a.thread.(*anthropic.AnthropicThread); ok {
		msgParams := anthropicThread.GetMessages()
		var messages []Message

		for _, msgParam := range msgParams {
			for _, block := range msgParam.Content {
				blockType := *block.GetType()
				switch blockType {
				case "text":
					messages = append(messages, Message{
						Content: *block.GetText(),
						IsUser:  msgParam.Role == "user",
					})
				case "tool_use":
					inputJSON, _ := json.Marshal(block.OfRequestToolUseBlock.Input)
					messages = append(messages, Message{
						Content: fmt.Sprintf("🔧 Using tool: %s", string(inputJSON)),
						IsUser:  msgParam.Role == "user",
					})
				case "tool_result":
					if len(block.OfRequestToolResultBlock.Content) > 0 {
						result := block.OfRequestToolResultBlock.Content[0].OfRequestTextBlock.Text
						messages = append(messages, Message{
							Content: fmt.Sprintf("🔄 Tool result: %s", result),
							IsUser:  false,
						})
					}
				}
			}
		}

		return messages, nil
	}

	return nil, fmt.Errorf("unsupported thread type")
}

func (a *AssistantClient) AddUserMessage(message string) {
	a.thread.AddUserMessage(message)
}

func (a *AssistantClient) SaveConversation(ctx context.Context) error {
	return a.thread.SaveConversation(ctx, true)
}

// SendMessage sends a message to the assistant and processes the response
func (a *AssistantClient) SendMessage(ctx context.Context, message string, messageCh chan types.MessageEvent) error {
	// Create a handler for channel-based events
	handler := &types.ChannelMessageHandler{MessageCh: messageCh}

	// Send the message using the persistent thread
	err := a.thread.SendMessage(ctx, message, handler, types.MessageOpt{
		PromptCache: true,
	})

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
