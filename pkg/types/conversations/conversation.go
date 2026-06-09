// Package conversations defines types and interfaces for conversation
// data structures, query options, and conversation records used
// throughout kodelet's conversation management system.
package conversations

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// QueryOptions provides filtering and sorting options for conversation queries
type QueryOptions struct {
	StartDate  *time.Time // Filter by start date
	EndDate    *time.Time // Filter by end date
	SearchTerm string     // Text to search for in messages
	Provider   string     // Filter by LLM provider (e.g., "anthropic", "openai")
	CWD        string     // Filter by canonical working directory
	Limit      int        // Maximum number of results
	Offset     int        // Offset for pagination
	SortBy     string     // Field to sort by
	SortOrder  string     // "asc" or "desc"
}

// ConversationRecord represents a persisted conversation with its messages and metadata
type ConversationRecord struct {
	ID          string                                `json:"id"`
	CWD         string                                `json:"cwd,omitempty"`
	RawMessages json.RawMessage                       `json:"rawMessages"` // Raw LLM provider messages
	Provider    string                                `json:"provider"`    // e.g., "anthropic"
	Usage       llmtypes.Usage                        `json:"usage"`
	Summary     string                                `json:"summary,omitempty"`
	CreatedAt   time.Time                             `json:"createdAt"`
	UpdatedAt   time.Time                             `json:"updatedAt"`
	Metadata    map[string]any                        `json:"metadata,omitempty"`
	ToolResults map[string]tools.StructuredToolResult `json:"toolResults,omitempty"` // Maps tool_call_id to structured result
}

// ConversationSummary provides a brief overview of a conversation
type ConversationSummary struct {
	ID           string         `json:"id"`
	CWD          string         `json:"cwd,omitempty"`
	MessageCount int            `json:"messageCount"`
	FirstMessage string         `json:"firstMessage"`
	Summary      string         `json:"summary,omitempty"`
	Provider     string         `json:"provider"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	Usage        llmtypes.Usage `json:"usage"`
	CreatedAt    time.Time      `json:"createdAt"`
	UpdatedAt    time.Time      `json:"updatedAt"`
}

// QueryResult represents the result of a query operation
type QueryResult struct {
	ConversationSummaries []ConversationSummary `json:"conversationSummaries"`
	Total                 int                   `json:"total"` // Represents the total number of the entries that match the query without pagination
	QueryOptions
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
		Metadata:    make(map[string]any),
		ToolResults: make(map[string]tools.StructuredToolResult),
	}
}

// ToSummary converts a ConversationRecord to a ConversationSummary
func (cr *ConversationRecord) ToSummary() ConversationSummary {
	// Extract first message by parsing the raw messages
	firstMessage := ""
	if len(cr.RawMessages) > 0 {
		displays := parseConversationDisplayMetadata(cr.Metadata)
		var messages []map[string]any
		if err := json.Unmarshal(cr.RawMessages, &messages); err == nil && len(messages) > 0 {
			// Find first user message
			for _, msg := range messages {
				if role, ok := msg["role"].(string); ok && role == "user" {
					if content, ok := msg["content"].([]any); ok && len(content) > 0 {
						if block, ok := content[0].(map[string]any); ok {
							if text, ok := block["text"].(string); ok {
								firstMessage = applyConversationDisplay(text, displays)
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
		CWD:          cr.CWD,
		MessageCount: messageCount,
		FirstMessage: firstMessage,
		Summary:      cr.Summary,
		Provider:     cr.Provider,
		Metadata:     cr.Metadata,
		Usage:        cr.Usage,
		CreatedAt:    cr.CreatedAt,
		UpdatedAt:    cr.UpdatedAt,
	}
}

func parseConversationDisplayMetadata(metadata map[string]any) map[string]string {
	if len(metadata) == 0 {
		return nil
	}

	rawRoot, ok := metadata["message_display"]
	if !ok {
		rawRoot, ok = metadata["message_display_overrides"]
	}
	if !ok {
		return nil
	}

	root, ok := rawRoot.(map[string]any)
	if !ok {
		return nil
	}

	rawVersion, ok := root["v1"]
	if !ok {
		return nil
	}

	versionMap, ok := rawVersion.(map[string]any)
	if !ok {
		return nil
	}

	displays := make(map[string]string, len(versionMap))
	for key, rawDisplay := range versionMap {
		display := conversationDisplayValue(rawDisplay)
		if strings.TrimSpace(key) == "" || strings.TrimSpace(display) == "" {
			continue
		}
		displays[key] = display
	}
	return displays
}

func conversationDisplayValue(raw any) string {
	switch value := raw.(type) {
	case map[string]any:
		if display, ok := value["text"]; ok && display != nil {
			return fmt.Sprint(display)
		}
		if display, ok := value["display"]; ok && display != nil {
			return fmt.Sprint(display)
		}
		return ""
	case map[string]string:
		if value["text"] != "" {
			return value["text"]
		}
		return value["display"]
	default:
		return ""
	}
}

func applyConversationDisplay(text string, displays map[string]string) string {
	if len(displays) == 0 || strings.TrimSpace(text) == "" {
		return text
	}

	sum := sha256.Sum256([]byte(strings.TrimSpace(text)))
	key := fmt.Sprintf("sha256:%x", sum[:])
	if display, ok := displays[key]; ok && strings.TrimSpace(display) != "" {
		return display
	}
	return text
}

// GetID returns the conversation ID for usage.ConversationSummary compatibility
func (cs ConversationSummary) GetID() string {
	return cs.ID
}

// GetCreatedAt returns the creation timestamp of the conversation
func (cs ConversationSummary) GetCreatedAt() time.Time {
	return cs.CreatedAt
}

// GetUpdatedAt returns the last update timestamp of the conversation
func (cs ConversationSummary) GetUpdatedAt() time.Time {
	return cs.UpdatedAt
}

// GetMessageCount returns the number of messages in the conversation
func (cs ConversationSummary) GetMessageCount() int {
	return cs.MessageCount
}

// GetUsage returns the LLM usage statistics for the conversation
func (cs ConversationSummary) GetUsage() llmtypes.Usage {
	return cs.Usage
}

// GetProvider returns the LLM provider name used for the conversation
func (cs ConversationSummary) GetProvider() string {
	return cs.Provider
}
