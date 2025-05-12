package anthropic

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/jingkaihe/kodelet/pkg/conversations"
)

// saveConversation saves the current thread to the conversation store
func (t *AnthropicThread) saveConversation() error {
	if !t.isPersisted || t.store == nil {
		return nil
	}

	// Marshall the messages to JSON
	rawMessages, err := json.Marshal(t.messages)
	if err != nil {
		return fmt.Errorf("failed to marshal conversation messages: %w", err)
	}

	// Extract first user message for display purposes
	firstUserPrompt := ""
	for _, msg := range t.messages {
		if msg.Role == "user" {
			for _, block := range msg.Content {
				if text := block.GetText(); text != nil {
					firstUserPrompt = *text
					break
				}
			}
			if firstUserPrompt != "" {
				break
			}
		}
	}

	// Create a new conversation record
	record := conversations.ConversationRecord{
		ID:              t.conversationID,
		RawMessages:     rawMessages,
		ModelType:       "anthropic",
		Usage:           t.usage,
		Metadata:        map[string]interface{}{"model": t.config.Model},
		FirstUserPrompt: firstUserPrompt,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	// Save the record
	return t.store.Save(record)
}

// loadConversation loads a conversation from the store into the thread
func (t *AnthropicThread) loadConversation() error {
	if !t.isPersisted || t.store == nil || t.conversationID == "" {
		return nil
	}

	// Try to load the conversation
	record, err := t.store.Load(t.conversationID)
	if err != nil {
		return fmt.Errorf("failed to load conversation: %w", err)
	}

	// Check if this is an Anthropic model conversation
	if record.ModelType != "" && record.ModelType != "anthropic" {
		return fmt.Errorf("incompatible model type: %s", record.ModelType)
	}

	// Reset current messages
	t.messages = []anthropic.MessageParam{}
	listRawMessages := []json.RawMessage{}
	if err := json.Unmarshal(record.RawMessages, &listRawMessages); err != nil {
		return fmt.Errorf("failed to unmarshal conversation messages: %w", err)
	}

	for _, rawMessage := range listRawMessages {
		var msg anthropic.Message
		json.Unmarshal(rawMessage, &msg)
		t.messages = append(t.messages, msg.ToParam())
	}

	// Restore usage statistics
	t.usage = record.Usage

	return nil
}
