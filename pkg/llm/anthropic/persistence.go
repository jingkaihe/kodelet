package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/jingkaihe/kodelet/pkg/llm/base"
	"github.com/jingkaihe/kodelet/pkg/tools/renderers"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
)

func hasAnyEmptyBlock(message *anthropic.MessageParam) bool {
	for _, contentBlock := range message.Content {
		if textBlock := contentBlock.OfText; textBlock != nil && strings.TrimSpace(textBlock.Text) == "" {
			return true
		}
		if thinkingBlock := contentBlock.OfThinking; thinkingBlock != nil && strings.TrimSpace(thinkingBlock.Thinking) == "" {
			return true
		}
	}
	return false
}

// cleanupOrphanedMessages removes orphaned messages from the end of the message list.
// This includes:
// - Empty messages (messages with no content)
// - Messages containing tool use blocks that are not followed by tool result messages
func (t *Thread) cleanupOrphanedMessages() {
	for len(t.messages) > 0 {
		lastMessage := t.messages[len(t.messages)-1]
		// remove the last message if it is empty
		if len(lastMessage.Content) == 0 {
			t.messages = t.messages[:len(t.messages)-1]
			continue
		}
		// remove the last message if it is an empty message
		if hasAnyEmptyBlock(&lastMessage) {
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
func (t *Thread) SaveConversation(ctx context.Context, summarise bool) error {
	t.ConversationMu.Lock()
	defer t.ConversationMu.Unlock()

	if !t.Persisted || t.Store == nil {
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
	record := convtypes.ConversationRecord{
		ID:                  t.ConversationID,
		RawMessages:         rawMessages,
		Provider:            "anthropic",
		Usage:               *t.Usage,
		Metadata:            map[string]any{"model": t.Config.Model},
		Summary:             t.summary,
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
		FileLastAccess:      t.State.FileLastAccess(),
		ToolResults:         t.GetStructuredToolResults(),
		BackgroundProcesses: t.State.GetBackgroundProcesses(),
	}

	// Save the record
	return t.Store.Save(ctx, record)
}

// loadConversation loads a conversation from the store into the thread.
// This method is used as a callback for the base.Thread's EnablePersistence method.
// Note: The base thread's ConversationMu is already locked when this is called.
func (t *Thread) loadConversation(ctx context.Context) {
	if !t.Persisted || t.Store == nil {
		return
	}

	// Try to load the conversation
	record, err := t.Store.Load(ctx, t.ConversationID)
	if err != nil {
		// Log error but don't return - caller expects void return
		return
	}

	// Check if this is an Anthropic model conversation
	if record.Provider != "" && record.Provider != "anthropic" {
		return
	}

	// Reset current messages
	messages, err := DeserializeMessages(record.RawMessages)
	if err != nil {
		return
	}
	t.messages = messages

	t.cleanupOrphanedMessages()
	// Restore usage statistics
	t.Usage = &record.Usage
	t.summary = record.Summary
	t.State.SetFileLastAccess(record.FileLastAccess)
	// Restore structured tool results
	t.SetStructuredToolResults(record.ToolResults)
	// Restore background processes
	base.RestoreBackgroundProcesses(t.State, record.BackgroundProcesses)
}

// DeserializeMessages deserializes a JSON byte array into Anthropic message parameters
func DeserializeMessages(b []byte) ([]anthropic.MessageParam, error) {
	var messages []anthropic.MessageParam
	if err := json.Unmarshal(b, &messages); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal conversation messages")
	}

	return messages, nil
}

// StreamableMessage contains parsed message data for streaming
type StreamableMessage struct {
	Kind       string // "text", "tool-use", "tool-result", "thinking"
	Role       string // "user", "assistant", "system"
	Content    string // Text content
	ToolName   string // For tool use/result
	ToolCallID string // For matching tool results
	Input      string // For tool use (JSON string)
}

// StreamMessages parses raw messages into streamable format for conversation streaming
func StreamMessages(rawMessages json.RawMessage, toolResults map[string]tooltypes.StructuredToolResult) ([]StreamableMessage, error) {
	messages, err := DeserializeMessages(rawMessages)
	if err != nil {
		return nil, errors.Wrap(err, "failed to deserialize anthropic messages")
	}

	var streamable []StreamableMessage

	for _, msg := range messages {
		for _, contentBlock := range msg.Content {
			if textBlock := contentBlock.OfText; textBlock != nil && textBlock.Text != "" {
				streamable = append(streamable, StreamableMessage{
					Kind:    "text",
					Role:    string(msg.Role),
					Content: textBlock.Text,
				})
			}

			if toolUseBlock := contentBlock.OfToolUse; toolUseBlock != nil {
				inputJSON, _ := json.Marshal(toolUseBlock.Input)
				streamable = append(streamable, StreamableMessage{
					Kind:       "tool-use",
					Role:       string(msg.Role),
					ToolName:   toolUseBlock.Name,
					ToolCallID: toolUseBlock.ID,
					Input:      string(inputJSON),
				})
			}

			if toolResultBlock := contentBlock.OfToolResult; toolResultBlock != nil {
				result := ""
				toolName := ""

				if structuredResult, ok := toolResults[toolResultBlock.ToolUseID]; ok {
					toolName = structuredResult.ToolName
					if jsonData, err := structuredResult.MarshalJSON(); err == nil {
						result = string(jsonData)
					}
				} else {
					// Fallback: extract raw text from tool result
					for _, resultContent := range toolResultBlock.Content {
						if textBlock := resultContent.OfText; textBlock != nil {
							result += textBlock.Text
						}
					}
				}

				streamable = append(streamable, StreamableMessage{
					Kind:       "tool-result",
					Role:       string(msg.Role),
					ToolName:   toolName,
					ToolCallID: toolResultBlock.ToolUseID,
					Content:    result,
				})
			}

			if thinkingBlock := contentBlock.OfThinking; thinkingBlock != nil && thinkingBlock.Thinking != "" {
				streamable = append(streamable, StreamableMessage{
					Kind:    "thinking",
					Role:    string(msg.Role),
					Content: thinkingBlock.Thinking,
				})
			}
		}
	}

	return streamable, nil
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
