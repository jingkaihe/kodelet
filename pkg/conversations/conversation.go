package conversations

import (
	"encoding/json"
	"strings"
	"time"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// ConversationRecord represents a persisted conversation with its messages and metadata
type ConversationRecord struct {
	ID             string                                `json:"id"`
	RawMessages    json.RawMessage                       `json:"rawMessages"` // Raw LLM provider messages
	ModelType      string                                `json:"modelType"`   // e.g., "anthropic"
	FileLastAccess map[string]time.Time                  `json:"fileLastAccess"`
	Usage          llmtypes.Usage                        `json:"usage"`
	Summary        string                                `json:"summary,omitempty"`
	CreatedAt      time.Time                             `json:"createdAt"`
	UpdatedAt      time.Time                             `json:"updatedAt"`
	Metadata       map[string]interface{}                `json:"metadata,omitempty"`
	ToolResults    map[string]tools.StructuredToolResult `json:"toolResults,omitempty"` // Maps tool_call_id to structured result
}

// ConversationSummary provides a brief overview of a conversation
type ConversationSummary struct {
	ID           string    `json:"id"`
	MessageCount int       `json:"messageCount"`
	FirstMessage string    `json:"firstMessage"`
	Summary      string    `json:"summary,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// NewConversationRecord creates a new conversation record with a unique ID
func NewConversationRecord(id string) ConversationRecord {
	now := time.Now()

	// If no ID is provided, generate one
	if id == "" {
		id = GenerateID()
	}

	return ConversationRecord{
		ID:             id,
		RawMessages:    json.RawMessage("[]"),
		CreatedAt:      now,
		UpdatedAt:      now,
		Metadata:       make(map[string]interface{}),
		FileLastAccess: make(map[string]time.Time),
		ToolResults:    make(map[string]tools.StructuredToolResult),
	}
}

// ToSummary converts a ConversationRecord to a ConversationSummary
func (cr *ConversationRecord) ToSummary() ConversationSummary {
	// Extract first message by parsing the raw messages
	firstMessage := ""
	if len(cr.RawMessages) > 0 {
		var messages []map[string]interface{}
		if err := json.Unmarshal(cr.RawMessages, &messages); err == nil && len(messages) > 0 {
			// Find first user message
			for _, msg := range messages {
				if role, ok := msg["role"].(string); ok && role == "user" {
					if content, ok := msg["content"].([]interface{}); ok && len(content) > 0 {
						if block, ok := content[0].(map[string]interface{}); ok {
							if text, ok := block["text"].(string); ok {
								firstMessage = text
								// Truncate if too long
								if len(firstMessage) > 100 {
									firstMessage = firstMessage[:97] + "..."
								}
								break
							}
						}
					}
				}
			}
		}
	}

	// Estimate message count from the raw JSON (this is just an approximation)
	messageCount := 0
	if len(cr.RawMessages) > 0 {
		// Count the occurrences of Role fields as a rough estimate
		messageCount = strings.Count(string(cr.RawMessages), `"role"`)
	}

	return ConversationSummary{
		ID:           cr.ID,
		MessageCount: messageCount,
		FirstMessage: firstMessage,
		Summary:      cr.Summary,
		CreatedAt:    cr.CreatedAt,
		UpdatedAt:    cr.UpdatedAt,
	}
}
