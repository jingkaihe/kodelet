package google

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pkg/errors"
	"google.golang.org/genai"

	"github.com/jingkaihe/kodelet/pkg/logger"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// StreamableMessage contains parsed message data for streaming.
type StreamableMessage struct {
	Kind       string // "text", "tool-use", "tool-result", "thinking"
	Role       string // "user", "assistant", "system"
	Content    string // Text content
	ToolName   string // For tool use/result
	ToolCallID string // For matching tool results
	Input      string // For tool use (JSON string)
}

// SaveConversation persists the current conversation state to the conversation store
func (t *Thread) SaveConversation(ctx context.Context, summarise bool) error {
	t.ConversationMu.Lock()
	defer t.ConversationMu.Unlock()

	if !t.Persisted || t.Store == nil {
		return nil
	}

	rawMessages, err := json.Marshal(t.messages)
	if err != nil {
		return errors.Wrap(err, "failed to marshal conversation messages")
	}

	summary := ""
	if summarise {
		summary = t.ShortSummary(ctx)
	}

	var fileLastAccess map[string]time.Time

	if t.State != nil {
		fileLastAccess = t.State.FileLastAccess()
	}

	metadata := map[string]any{"model": t.Config.Model, "backend": t.backend}
	if profile := strings.TrimSpace(t.Config.Profile); profile != "" {
		metadata["profile"] = profile
	}

	record := convtypes.ConversationRecord{
		ID:             t.ConversationID,
		CWD:            t.Config.WorkingDirectory,
		RawMessages:    rawMessages,
		Provider:       "google",
		Usage:          *t.Usage,
		Metadata:       metadata,
		Summary:        summary,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		FileLastAccess: fileLastAccess,
		ToolResults:    t.GetStructuredToolResults(),
	}

	return t.Store.Save(ctx, record)
}

// LoadConversationByID loads a conversation from the conversation store by ID.
// This is different from the loadConversation callback which loads the current conversation.
func (t *Thread) LoadConversationByID(ctx context.Context, conversationID string) error {
	t.ConversationMu.Lock()
	defer t.ConversationMu.Unlock()

	if t.Store == nil {
		return errors.New("conversation store not initialized")
	}

	record, err := t.Store.Load(ctx, conversationID)
	if err != nil {
		return errors.Wrap(err, "failed to load conversation")
	}

	if err := json.Unmarshal(record.RawMessages, &t.messages); err != nil {
		return errors.Wrap(err, "failed to deserialize messages")
	}
	t.ConversationID = record.ID
	t.Usage = &record.Usage
	t.SetStructuredToolResults(record.ToolResults)

	if t.State != nil {
		t.State.SetFileLastAccess(record.FileLastAccess)
	}

	logger.G(ctx).WithField("conversation_id", conversationID).Info("Loaded conversation")
	return nil
}

// StreamMessages parses raw Google messages into streamable format for conversation streaming.
func StreamMessages(rawMessages []byte, toolResults map[string]tooltypes.StructuredToolResult) ([]StreamableMessage, error) {
	var googleMessages []*genai.Content
	if err := json.Unmarshal(rawMessages, &googleMessages); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal Google messages")
	}

	var messages []StreamableMessage
	pendingToolUsesByName := make(map[string][]int)

	for _, content := range googleMessages {
		for _, part := range content.Parts {
			switch {
			case part.Text != "":
				if part.Thought {
					messages = append(messages, StreamableMessage{
						Kind:    "thinking",
						Role:    "assistant",
						Content: part.Text,
					})
					continue
				}

				role := "assistant"
				if content.Role == genai.RoleUser {
					role = "user"
				}

				messages = append(messages, StreamableMessage{
					Kind:    "text",
					Role:    role,
					Content: part.Text,
				})

			case part.FunctionCall != nil:
				argsJSON, _ := json.Marshal(part.FunctionCall.Args)
				messages = append(messages, StreamableMessage{
					Kind:       "tool-use",
					Role:       "assistant",
					ToolName:   part.FunctionCall.Name,
					ToolCallID: part.FunctionCall.ID,
					Input:      string(argsJSON),
				})
				if part.FunctionCall.ID == "" {
					pendingToolUsesByName[part.FunctionCall.Name] = append(
						pendingToolUsesByName[part.FunctionCall.Name],
						len(messages)-1,
					)
				}

			case part.FunctionResponse != nil:
				result := ""
				toolName := part.FunctionResponse.Name
				callID := extractToolCallID(part.FunctionResponse.Response)
				if callID != "" {
					if pending := pendingToolUsesByName[toolName]; len(pending) > 0 {
						messages[pending[0]].ToolCallID = callID
						pendingToolUsesByName[toolName] = pending[1:]
					}
				}

				structuredResult, ok := toolResults[callID]
				if !ok {
					structuredResult, ok = toolResults[toolName]
				}
				if ok {
					toolName = structuredResult.ToolName
					if jsonData, err := structuredResult.MarshalJSON(); err == nil {
						result = string(jsonData)
					}
				}
				if result == "" {
					if responseJSON, err := json.Marshal(part.FunctionResponse.Response); err == nil {
						result = string(responseJSON)
					}
				}

				messages = append(messages, StreamableMessage{
					Kind:       "tool-result",
					Role:       "assistant",
					ToolName:   toolName,
					ToolCallID: callID,
					Content:    result,
				})
			}
		}
	}

	return messages, nil
}

// ExtractMessages converts raw Google GenAI message bytes to standard message format
func ExtractMessages(rawMessages []byte, toolResults map[string]tooltypes.StructuredToolResult) ([]llmtypes.Message, error) {
	var googleMessages []*genai.Content
	if err := json.Unmarshal(rawMessages, &googleMessages); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal Google messages")
	}

	var messages []llmtypes.Message

	for _, content := range googleMessages {
		for _, part := range content.Parts {
			switch {
			case part.Text != "":
				role := "assistant"
				if content.Role == genai.RoleUser {
					role = "user"
				}

				if part.Thought {
					continue
				}

				messages = append(messages, llmtypes.Message{
					Role:    role,
					Content: part.Text,
				})

			case part.FunctionCall != nil:
				argsJSON, _ := json.Marshal(part.FunctionCall.Args)
				messages = append(messages, llmtypes.Message{
					Role:    "assistant",
					Content: fmt.Sprintf("🔧 Using tool: %s with input: %s", part.FunctionCall.Name, string(argsJSON)),
				})

			case part.FunctionResponse != nil:
				result := ""
				toolName := part.FunctionResponse.Name
				callID := extractToolCallID(part.FunctionResponse.Response)

				// Structured results are keyed by tool call ID at execution time.
				// Keep a tool-name fallback for older persisted records.
				structuredResult, ok := toolResults[callID]
				if !ok {
					structuredResult, ok = toolResults[toolName]
				}
				if ok && (structuredResult.Metadata != nil || structuredResult.Error != "") {
					if jsonData, err := structuredResult.MarshalJSON(); err == nil {
						result = string(jsonData)
					}
				}
				if result == "" {
					if responseJSON, err := json.Marshal(part.FunctionResponse.Response); err == nil {
						result = string(responseJSON)
					}
				}

				messages = append(messages, llmtypes.Message{
					Role:    "user",
					Content: fmt.Sprintf("🔄 Tool result:\n%s", result),
				})
			}
		}
	}

	return messages, nil
}

func extractToolCallID(response map[string]any) string {
	if response == nil {
		return ""
	}

	if rawCallID, ok := response["call_id"]; ok {
		if callID, ok := rawCallID.(string); ok {
			return callID
		}
	}

	return ""
}

// DeserializeMessages deserializes raw message bytes into Google GenAI Content objects
func DeserializeMessages(rawMessages []byte) ([]*genai.Content, error) {
	var messages []*genai.Content
	if err := json.Unmarshal(rawMessages, &messages); err != nil {
		return nil, errors.Wrap(err, "failed to deserialize Google messages")
	}
	return messages, nil
}
