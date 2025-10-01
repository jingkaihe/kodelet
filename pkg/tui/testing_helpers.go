package tui

import (
	"context"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// MockAssistant is a mock implementation of AssistantClient for testing
type MockAssistant struct {
	messages         []llmtypes.Message
	usage            llmtypes.Usage
	conversationID   string
	persisted        bool
	sendMessageError error
}

// NewMockAssistant creates a new mock assistant for testing
func NewMockAssistant() *MockAssistant {
	return &MockAssistant{
		messages:       []llmtypes.Message{},
		conversationID: "test-conversation-id",
		persisted:      true,
		usage: llmtypes.Usage{
			InputTokens:  100,
			OutputTokens: 50,
			MaxContextWindow: 200000,
			CurrentContextWindow: 150,
		},
	}
}

// GetThreadMessages returns the mock messages
func (m *MockAssistant) GetThreadMessages() ([]llmtypes.Message, error) {
	return m.messages, nil
}

// SendMessage simulates sending a message
func (m *MockAssistant) SendMessage(ctx context.Context, message string, messageCh chan llmtypes.MessageEvent, imagePaths ...string) error {
	if m.sendMessageError != nil {
		return m.sendMessageError
	}
	
	// Simulate assistant response
	go func() {
		messageCh <- llmtypes.MessageEvent{
			Type:    llmtypes.EventTypeText,
			Content: "Mock response",
			Done:    false,
		}
		messageCh <- llmtypes.MessageEvent{
			Done: true,
		}
	}()
	
	return nil
}

// GetUsage returns mock usage stats
func (m *MockAssistant) GetUsage() llmtypes.Usage {
	return m.usage
}

// GetConversationID returns the mock conversation ID
func (m *MockAssistant) GetConversationID() string {
	return m.conversationID
}

// IsPersisted returns the mock persistence status
func (m *MockAssistant) IsPersisted() bool {
	return m.persisted
}

// Close performs cleanup (no-op for mock)
func (m *MockAssistant) Close(ctx context.Context) error {
	return nil
}

// SetSendMessageError sets an error to be returned by SendMessage
func (m *MockAssistant) SetSendMessageError(err error) {
	m.sendMessageError = err
}

// AddMessage adds a message to the mock assistant's message history
func (m *MockAssistant) AddMessage(content, role string) {
	m.messages = append(m.messages, llmtypes.Message{
		Content: content,
		Role:    role,
	})
}
