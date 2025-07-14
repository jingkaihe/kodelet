package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jingkaihe/kodelet/pkg/tools/renderers"
	"github.com/pkg/errors"
	"github.com/sashabaranov/go-openai"

	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// cleanupOrphanedMessages removes orphaned messages from the end of the message list.
// This includes:
// - Empty messages (messages with no content and no tool calls)
// - Assistant messages containing tool calls that are not followed by tool result messages
func (t *OpenAIThread) cleanupOrphanedMessages() {
	for len(t.messages) > 0 {
		lastMessage := t.messages[len(t.messages)-1]

		// Remove the last message if it is empty (no content and no tool calls)
		if lastMessage.Content == "" && len(lastMessage.ToolCalls) == 0 && lastMessage.Role != openai.ChatMessageRoleTool {
			t.messages = t.messages[:len(t.messages)-1]
			continue
		}

		// Remove the last message if it's an assistant message with tool calls,
		// as it must be followed by tool result messages
		if lastMessage.Role == openai.ChatMessageRoleAssistant && len(lastMessage.ToolCalls) > 0 {
			t.messages = t.messages[:len(t.messages)-1]
			continue
		}

		break
	}
}

// SaveConversation saves the current thread to the conversation store
func (t *OpenAIThread) SaveConversation(ctx context.Context, summarize bool) error {
	t.conversationMu.Lock()
	defer t.conversationMu.Unlock()

	if !t.isPersisted || t.store == nil {
		return nil
	}

	// Clean up orphaned messages before saving
	t.cleanupOrphanedMessages()

	// Generate a new summary if requested
	if summarize {
		t.summary = t.ShortSummary(ctx)
	}

	// Serialize the thread state
	messagesJSON, err := json.Marshal(t.messages)
	if err != nil {
		return errors.Wrap(err, "error marshaling messages")
	}

	// Build the conversation record
	record := convtypes.ConversationRecord{
		ID:             t.conversationID,
		RawMessages:    messagesJSON,
		Provider:       "openai",
		Usage:          *t.usage,
		Metadata:       map[string]interface{}{"model": t.config.Model},
		Summary:        t.summary,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		FileLastAccess: t.state.FileLastAccess(),
		ToolResults:    t.GetStructuredToolResults(),
	}

	// Save to the store
	return t.store.Save(ctx, record)
}

// loadConversation loads a conversation from the store
func (t *OpenAIThread) loadConversation(ctx context.Context) error {
	t.conversationMu.Lock()
	defer t.conversationMu.Unlock()

	if !t.isPersisted || t.store == nil || t.conversationID == "" {
		return nil
	}

	// Try to load the conversation
	record, err := t.store.Load(ctx, t.conversationID)
	if err != nil {
		return errors.Wrap(err, "failed to load conversation")
	}

	// Check if this is an OpenAI model conversation
	if record.Provider != "" && record.Provider != "openai" {
		return errors.Errorf("incompatible model type: %s", record.Provider)
	}

	// Deserialize the messages
	var messages []openai.ChatCompletionMessage
	if err := json.Unmarshal(record.RawMessages, &messages); err != nil {
		return errors.Wrap(err, "error unmarshaling messages")
	}

	t.cleanupOrphanedMessages()

	t.messages = messages
	t.usage = &record.Usage
	t.summary = record.Summary
	t.state.SetFileLastAccess(record.FileLastAccess)
	// Restore structured tool results
	t.SetStructuredToolResults(record.ToolResults)

	return nil
}

// ExtractMessages converts the internal message format to the common format
func ExtractMessages(data []byte, toolResults map[string]tooltypes.StructuredToolResult) ([]llmtypes.Message, error) {
	var messages []openai.ChatCompletionMessage
	if err := json.Unmarshal(data, &messages); err != nil {
		return nil, errors.Wrap(err, "error unmarshaling messages")
	}

	result := make([]llmtypes.Message, 0, len(messages))
	for _, msg := range messages {
		// Skip system messages as they are implementation details
		if msg.Role == openai.ChatMessageRoleSystem {
			continue
		}

		// Handle tool results first (before plain content)
		if msg.Role == openai.ChatMessageRoleTool {
			text := msg.Content
			// Use CLI rendering if structured result is available
			if structuredResult, ok := toolResults[msg.ToolCallID]; ok {
				registry := renderers.NewRendererRegistry()
				text = registry.Render(structuredResult)
			}
			result = append(result, llmtypes.Message{
				Role:    "assistant",
				Content: fmt.Sprintf("🔄 Tool result:\n%s", text),
			})
			continue
		}

		// Handle plain content (legacy format)
		if msg.Content != "" && len(msg.MultiContent) == 0 && len(msg.ToolCalls) == 0 {
			result = append(result, llmtypes.Message{
				Role:    string(msg.Role),
				Content: msg.Content,
			})
		}

		// Handle text blocks in MultiContent
		for _, contentBlock := range msg.MultiContent {
			if contentBlock.Text != "" {
				result = append(result, llmtypes.Message{
					Role:    string(msg.Role),
					Content: contentBlock.Text,
				})
			}
		}

		// Handle tool calls
		if len(msg.ToolCalls) > 0 {
			for _, toolCall := range msg.ToolCalls {
				inputJSON, err := json.Marshal(toolCall)
				if err != nil {
					continue
				}
				result = append(result, llmtypes.Message{
					Role:    string(msg.Role),
					Content: fmt.Sprintf("🔧 Using tool: %s", string(inputJSON)),
				})
			}
		}
	}

	return result, nil
}
