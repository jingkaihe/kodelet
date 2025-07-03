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

// cleanupOrphanedMessages removes orphaned messages from the end of the message list.
// This includes:
// - Empty messages (messages with no content)
// - Messages containing tool use blocks that are not followed by tool result messages
func (t *AnthropicThread) cleanupOrphanedMessages() {
	for {
		if len(t.messages) == 0 {
			break
		}
		lastMessage := t.messages[len(t.messages)-1]
		// remove the last message if it is empty
		if len(lastMessage.Content) == 0 {
			t.messages = t.messages[:len(t.messages)-1]
			continue
		}
		// remove the last message if it is an empty message
		if len(lastMessage.Content) == 1 && lastMessage.Content[0].OfText != nil && lastMessage.Content[0].OfText.Text == "" {
			t.messages = t.messages[:len(t.messages)-1]
			continue
		}
		// remove the last message if it has any tool use message, as it must be followed by a tool result message
		hasToolUse := false
		for _, contentBlock := range lastMessage.Content {
			if contentBlock.OfToolUse != nil {
				hasToolUse = true
				break
			}
		}

		if hasToolUse {
			t.messages = t.messages[:len(t.messages)-1]
			continue
		}
		break
	}
}

// SaveConversation saves the current thread to the conversation store
func (t *AnthropicThread) SaveConversation(ctx context.Context, summarise bool) error {
	t.conversationMu.Lock()
	defer t.conversationMu.Unlock()

	if !t.isPersisted || t.store == nil {
		return nil
	}

	// Clean up orphaned messages before saving
	t.cleanupOrphanedMessages()

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
		ID:                    t.conversationID,
		RawMessages:           rawMessages,
		ModelType:             "anthropic",
		Usage:                 *t.usage,
		Metadata:              map[string]interface{}{"model": t.config.Model},
		Summary:               t.summary,
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
		FileLastAccess:        t.state.FileLastAccess(),
		UserFacingToolResults: t.GetUserFacingToolResults(),
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

	t.cleanupOrphanedMessages()
	// Restore usage statistics
	t.usage = &record.Usage
	t.summary = record.Summary
	t.state.SetFileLastAccess(record.FileLastAccess)
	// Restore user-facing tool results
	t.SetUserFacingToolResults(record.UserFacingToolResults)
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
func ExtractMessages(rawMessages json.RawMessage, userFacingToolResults map[string]string) ([]llm.Message, error) {
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
					Content: fmt.Sprintf("🔧 Using tool: %s with input: %s", toolUseBlock.Name, string(inputJSON)),
				})
			}
			// Handle tool result blocks
			if toolResultBlock := contentBlock.OfToolResult; toolResultBlock != nil {
				for _, resultContent := range toolResultBlock.Content {
					if textBlock := resultContent.OfText; textBlock != nil {
						text := textBlock.Text
						if userFacingToolResults, ok := userFacingToolResults[toolResultBlock.ToolUseID]; ok {
							text = userFacingToolResults
						}
						messages = append(messages, llm.Message{
							Role:    "assistant",
							Content: fmt.Sprintf("🔄 Tool result:\n%s", text),
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
