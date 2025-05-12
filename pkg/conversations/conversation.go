package conversations

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/llm/types"
)

// ConversationRecord represents a persisted conversation with its messages and metadata
type ConversationRecord struct {
	ID              string                 `json:"id"`
	RawMessages     json.RawMessage        `json:"rawMessages"` // Raw LLM provider messages
	ModelType       string                 `json:"modelType"`   // e.g., "anthropic"
	Usage           types.Usage            `json:"usage"`
	Summary         string                 `json:"summary,omitempty"`
	CreatedAt       time.Time              `json:"createdAt"`
	UpdatedAt       time.Time              `json:"updatedAt"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
	FirstUserPrompt string                 `json:"firstUserPrompt"` // For display in listings
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
		ID:          id,
		RawMessages: json.RawMessage("[]"),
		CreatedAt:   now,
		UpdatedAt:   now,
		Metadata:    make(map[string]interface{}),
	}
}

// ToSummary converts a ConversationRecord to a ConversationSummary
func (cr *ConversationRecord) ToSummary() ConversationSummary {
	// Use FirstUserPrompt for display in the summary
	firstMessage := cr.FirstUserPrompt
	if firstMessage != "" {
		// Truncate if too long
		if len(firstMessage) > 100 {
			firstMessage = firstMessage[:97] + "..."
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
