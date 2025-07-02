package conversations

import (
	"encoding/json"
	"strings"
	"time"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// ToolExecution represents a single tool execution with its user-facing result
type ToolExecution struct {
	ToolName     string    `json:"toolName"`
	Input        string    `json:"input"`
	UserFacing   string    `json:"userFacing"`
	Timestamp    time.Time `json:"timestamp"`
	MessageIndex int       `json:"messageIndex"` // Index of the message this tool execution belongs to
}

// ConversationRecord represents a persisted conversation with its messages and metadata
type ConversationRecord struct {
	ID             string                 `json:"id"`
	RawMessages    json.RawMessage        `json:"rawMessages"` // Raw LLM provider messages
	ModelType      string                 `json:"modelType"`   // e.g., "anthropic"
	FileLastAccess map[string]time.Time   `json:"fileLastAccess"`
	Usage          llmtypes.Usage         `json:"usage"`
	Summary        string                 `json:"summary,omitempty"`
	CreatedAt      time.Time              `json:"createdAt"`
	UpdatedAt      time.Time              `json:"updatedAt"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
	ToolExecutions []ToolExecution        `json:"toolExecutions,omitempty"` // User-facing tool results
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
		ToolExecutions: make([]ToolExecution, 0),
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

// AddToolExecution adds a tool execution result to the conversation
func (cr *ConversationRecord) AddToolExecution(toolName, input, userFacing string, messageIndex int) {
	if cr.ToolExecutions == nil {
		cr.ToolExecutions = make([]ToolExecution, 0)
	}
	
	execution := ToolExecution{
		ToolName:     toolName,
		Input:        input,
		UserFacing:   userFacing,
		Timestamp:    time.Now(),
		MessageIndex: messageIndex,
	}
	
	cr.ToolExecutions = append(cr.ToolExecutions, execution)
	cr.UpdatedAt = time.Now()
}

// GetToolExecutionsForMessage returns all tool executions for a specific message index
func (cr *ConversationRecord) GetToolExecutionsForMessage(messageIndex int) []ToolExecution {
	var executions []ToolExecution
	for _, exec := range cr.ToolExecutions {
		if exec.MessageIndex == messageIndex {
			executions = append(executions, exec)
		}
	}
	return executions
}
