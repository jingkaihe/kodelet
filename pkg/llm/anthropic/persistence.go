package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/types/llm"
)

// SaveConversation saves the current thread to the conversation store
func (t *AnthropicThread) SaveConversation(ctx context.Context, summarise bool) error {
	t.conversationMu.Lock()
	defer t.conversationMu.Unlock()

	if !t.isPersisted || t.store == nil {
		return nil
	}

	// Marshall the messages to JSON
	rawMessages, err := json.Marshal(t.messages)
	if err != nil {
		return fmt.Errorf("failed to marshal conversation messages: %w", err)
	}

	if summarise {
		// Generate summary for the conversation
		t.summary = t.ShortSummary(ctx)
	}

	// Create a new conversation record
	record := conversations.ConversationRecord{
		ID:             t.conversationID,
		RawMessages:    rawMessages,
		ModelType:      "anthropic",
		Usage:          *t.usage,
		Metadata:       map[string]interface{}{"model": t.config.Model},
		Summary:        t.summary,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		FileLastAccess: t.state.FileLastAccess(),
	}

	// Save the record
	return t.store.Save(record)
}

// loadConversation loads a conversation from the store into the thread
func (t *AnthropicThread) loadConversation() error {
	t.conversationMu.Lock()
	defer t.conversationMu.Unlock()

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
	messages, err := DeserializeMessages(record.RawMessages)
	if err != nil {
		return fmt.Errorf("failed to deserialize conversation messages: %w", err)
	}
	t.messages = messages

	// Restore usage statistics
	t.usage = &record.Usage
	t.summary = record.Summary
	t.state.SetFileLastAccess(record.FileLastAccess)
	return nil
}

type contentBlock map[string]interface{}
type messageParam struct {
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
}

func DeserializeMessages(b []byte) ([]anthropic.MessageParam, error) {
	var messages []anthropic.MessageParam
	if err := json.Unmarshal(b, &messages); err != nil {
		return nil, fmt.Errorf("failed to unmarshal conversation messages: %w", err)
	}

	return messages, nil
}

// ExtractMessages parses the raw messages from a conversation record
func ExtractMessages(rawMessages json.RawMessage) ([]llm.Message, error) {
	// Deserialize the raw messages using the existing DeserializeMessages function
	anthropicMessages, err := DeserializeMessages(rawMessages)
	if err != nil {
		return nil, fmt.Errorf("error deserializing messages: %w", err)
	}

	var messages []llm.Message
	// Convert Anthropic message format to LLM message format
	for _, msg := range anthropicMessages {
		for _, contentBlock := range msg.Content {
			// Handle text blocks
			if textBlock := contentBlock.OfText; textBlock != nil {
				messages = append(messages, llm.Message{
					Role:    string(msg.Role),
					Content: textBlock.Text,
				})
			}
			// Handle tool use blocks
			if toolUseBlock := contentBlock.OfToolUse; toolUseBlock != nil {
				inputJSON, err := json.Marshal(toolUseBlock.Input)
				if err != nil {
					continue // Skip if marshaling fails
				}
				messages = append(messages, llm.Message{
					Role:    string(msg.Role),
					Content: fmt.Sprintf("🔧 Using tool: %s", string(inputJSON)),
				})
			}
			// Handle tool result blocks
			if toolResultBlock := contentBlock.OfToolResult; toolResultBlock != nil {
				for _, resultContent := range toolResultBlock.Content {
					if textBlock := resultContent.OfText; textBlock != nil {
						messages = append(messages, llm.Message{
							Role:    "assistant",
							Content: fmt.Sprintf("🔄 Tool result: %s", textBlock.Text),
						})
					}
				}
			}
			// Handle thinking blocks
			if thinkingBlock := contentBlock.OfThinking; thinkingBlock != nil {
				messages = append(messages, llm.Message{
					Role:    "assistant",
					Content: fmt.Sprintf("💭 Thinking: %s", thinkingBlock.Thinking),
				})
			}
		}
	}

	return messages, nil
}
