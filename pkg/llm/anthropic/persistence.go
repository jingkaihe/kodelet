package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/tools/renderers"
	"github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
)

// cleanupOrphanedMessages removes orphaned messages from the end of the message list.
// This includes:
// - Empty messages (messages with no content)
// - Messages containing tool use blocks that are not followed by tool result messages
func (t *AnthropicThread) cleanupOrphanedMessages() {
	for len(t.messages) > 0 {
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
		return errors.Wrap(err, "failed to marshal conversation messages")
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
		ToolResults:    t.GetStructuredToolResults(),
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
		return errors.Wrap(err, "failed to load conversation")
	}

	// Check if this is an Anthropic model conversation
	if record.ModelType != "" && record.ModelType != "anthropic" {
		return errors.Errorf("incompatible model type: %s", record.ModelType)
	}

	// Reset current messages
	messages, err := DeserializeMessages(record.RawMessages)
	if err != nil {
		return errors.Wrap(err, "failed to deserialize conversation messages")
	}
	t.messages = messages

	t.cleanupOrphanedMessages()
	// Restore usage statistics
	t.usage = &record.Usage
	t.summary = record.Summary
	t.state.SetFileLastAccess(record.FileLastAccess)
	// Restore structured tool results
	t.SetStructuredToolResults(record.ToolResults)
	return nil
}

func DeserializeMessages(b []byte) ([]anthropic.MessageParam, error) {
	var messages []anthropic.MessageParam
	if err := json.Unmarshal(b, &messages); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal conversation messages")
	}

	return messages, nil
}

// ExtractMessages parses the raw messages from a conversation record
func ExtractMessages(rawMessages json.RawMessage, toolResults map[string]tooltypes.StructuredToolResult) ([]llm.Message, error) {
	// Deserialize the raw messages using the existing DeserializeMessages function
	anthropicMessages, err := DeserializeMessages(rawMessages)
	if err != nil {
		return nil, errors.Wrap(err, "error deserializing messages")
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
						// Use CLI rendering if structured result is available
						if structuredResult, ok := toolResults[toolResultBlock.ToolUseID]; ok {
							registry := renderers.NewRendererRegistry()
							text = registry.Render(structuredResult)
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
					Content: fmt.Sprintf("💭 Thinking: %s", strings.TrimLeft(thinkingBlock.Thinking, "\n")),
				})
			}
		}
	}

	return messages, nil
}
