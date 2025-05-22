package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/sashabaranov/go-openai"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// SaveConversation saves the current thread to the conversation store
func (t *OpenAIThread) SaveConversation(ctx context.Context, summarize bool) error {
	t.conversationMu.Lock()
	defer t.conversationMu.Unlock()

	if !t.isPersisted || t.store == nil {
		return nil
	}

	// Generate a new summary if requested
	if summarize {
		t.summary = t.ShortSummary(ctx)
	}

	// Serialize the thread state
	messagesJSON, err := json.Marshal(t.messages)
	if err != nil {
		return fmt.Errorf("error marshaling messages: %w", err)
	}

	// Build the conversation record
	record := conversations.ConversationRecord{
		ID:             t.conversationID,
		RawMessages:    messagesJSON,
		ModelType:      "openai",
		Usage:          *t.usage,
		Metadata:       map[string]interface{}{"model": t.config.Model},
		Summary:        t.summary,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		FileLastAccess: t.state.FileLastAccess(),
	}

	// Save to the store
	return t.store.Save(record)
}

// loadConversation loads a conversation from the store
func (t *OpenAIThread) loadConversation() error {
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

	// Check if this is an OpenAI model conversation
	if record.ModelType != "" && record.ModelType != "openai" {
		return fmt.Errorf("incompatible model type: %s", record.ModelType)
	}

	// Deserialize the messages
	var messages []openai.ChatCompletionMessage
	if err := json.Unmarshal(record.RawMessages, &messages); err != nil {
		return fmt.Errorf("error unmarshaling messages: %w", err)
	}

	t.messages = messages
	t.usage = &record.Usage
	t.summary = record.Summary
	t.state.SetFileLastAccess(record.FileLastAccess)
	
	return nil
}

// ExtractMessages converts the internal message format to the common format
func ExtractMessages(data []byte) ([]llmtypes.Message, error) {
	var messages []openai.ChatCompletionMessage
	if err := json.Unmarshal(data, &messages); err != nil {
		return nil, fmt.Errorf("error unmarshaling messages: %w", err)
	}

	result := make([]llmtypes.Message, 0, len(messages))
	for _, msg := range messages {
		// Skip system messages as they are implementation details
		if msg.Role == openai.ChatMessageRoleSystem {
			continue
		}

		content := msg.Content

		// Handle tool calls by serializing them to JSON
		if msg.ToolCalls != nil && len(msg.ToolCalls) > 0 {
			toolCallsJSON, _ := json.Marshal(msg.ToolCalls)
			content = string(toolCallsJSON)
		}

		result = append(result, llmtypes.Message{
			Role:    string(msg.Role),
			Content: content,
		})
	}

	return result, nil
}