package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jingkaihe/kodelet/pkg/tools/renderers"
	"github.com/jingkaihe/kodelet/pkg/utils"
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

	// Generate a new summary if requested
	if summarize {
		t.summary = t.ShortSummary(ctx)
	}

	var messagesJSON []byte
	var err error
	var metadata map[string]interface{}

	if t.useResponsesAPI {
		// For Response API, serialize the unified conversation history
		conversationData := map[string]interface{}{
			"api_type":             "responses", // Discriminator for Response API
			"previous_response_id": t.previousResponseID,
			"conversation_items":   t.conversationItems, // Unified history for visualization
		}
		messagesJSON, err = json.Marshal(conversationData)
		if err != nil {
			return errors.Wrap(err, "error marshaling response items")
		}
		metadata = map[string]interface{}{
			"model":    t.config.Model,
			"api_type": "responses",
		}
	} else {
		// For Chat Completion API, use existing logic
		t.cleanupOrphanedMessages()
		messagesJSON, err = json.Marshal(t.messages)
		if err != nil {
			return errors.Wrap(err, "error marshaling messages")
		}
		metadata = map[string]interface{}{
			"model":    t.config.Model,
			"api_type": "chat_completion",
		}
	}

	// Build the conversation record (same structure for both APIs)
	record := convtypes.ConversationRecord{
		ID:                  t.conversationID,
		RawMessages:         messagesJSON,
		Provider:            "openai",
		Usage:               *t.usage,
		Metadata:            metadata,
		Summary:             t.summary,
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
		FileLastAccess:      t.state.FileLastAccess(),
		ToolResults:         t.GetStructuredToolResults(),
		BackgroundProcesses: t.state.GetBackgroundProcesses(),
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

	// Check which API type was used
	apiType := "chat_completion" // default
	if metadata, ok := record.Metadata["api_type"].(string); ok {
		apiType = metadata
	}

	if apiType == "responses" && t.useResponsesAPI {
		// Load Response API conversation
		var conversationData struct {
			APIType            string             `json:"api_type"`
			PreviousResponseID string             `json:"previous_response_id"`
			ConversationItems  []ConversationItem `json:"conversation_items"`
		}

		if err := json.Unmarshal(record.RawMessages, &conversationData); err != nil {
			return errors.Wrap(err, "error unmarshaling response items")
		}

		t.previousResponseID = conversationData.PreviousResponseID
		t.conversationItems = conversationData.ConversationItems
	} else if apiType == "chat_completion" && !t.useResponsesAPI {
		// Load Chat Completion API conversation (existing logic)
		var messages []openai.ChatCompletionMessage
		if err := json.Unmarshal(record.RawMessages, &messages); err != nil {
			return errors.Wrap(err, "error unmarshaling messages")
		}

		t.cleanupOrphanedMessages()
		t.messages = messages
	} else {
		return errors.Errorf("API type mismatch: conversation uses %s but thread is configured for %s",
			apiType, map[bool]string{true: "responses", false: "chat_completion"}[t.useResponsesAPI])
	}

	// Common restoration for both APIs
	t.usage = &record.Usage
	t.summary = record.Summary
	t.state.SetFileLastAccess(record.FileLastAccess)
	// Restore structured tool results
	t.SetStructuredToolResults(record.ToolResults)
	// Restore background processes
	t.restoreBackgroundProcesses(record.BackgroundProcesses)

	return nil
}

// restoreBackgroundProcesses restores background processes from the conversation record
func (t *OpenAIThread) restoreBackgroundProcesses(processes []tooltypes.BackgroundProcess) {
	for _, process := range processes {
		// Check if process is still alive
		if utils.IsProcessAlive(process.PID) {
			// Reattach to the process
			if restoredProcess, err := utils.ReattachProcess(process); err == nil {
				t.state.AddBackgroundProcess(restoredProcess)
			}
		}
	}
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
	// Try to determine the API type
	var typeCheck struct {
		APIType string `json:"api_type"`
	}
	if err := json.Unmarshal(rawMessages, &typeCheck); err == nil && typeCheck.APIType == "responses" {
		return streamResponseAPIMessages(rawMessages, toolResults)
	}

	// Fall back to Chat Completion API format (existing implementation)
	return streamChatCompletionMessages(rawMessages, toolResults)
}

// streamChatCompletionMessages handles Chat Completion API format
func streamChatCompletionMessages(rawMessages json.RawMessage, toolResults map[string]tooltypes.StructuredToolResult) ([]StreamableMessage, error) {
	var messages []openai.ChatCompletionMessage
	if err := json.Unmarshal(rawMessages, &messages); err != nil {
		return nil, errors.Wrap(err, "error unmarshaling messages")
	}

	var streamable []StreamableMessage

	for _, msg := range messages {
		// Skip system messages as they are implementation details
		if msg.Role == openai.ChatMessageRoleSystem {
			continue
		}

		if msg.Role == openai.ChatMessageRoleTool {
			result := msg.Content
			toolName := ""
			if structuredResult, ok := toolResults[msg.ToolCallID]; ok {
				toolName = structuredResult.ToolName
				if jsonData, err := structuredResult.MarshalJSON(); err == nil {
					result = string(jsonData)
				}
			}
			streamable = append(streamable, StreamableMessage{
				Kind:       "tool-result",
				Role:       "assistant", // Tool results are shown as assistant messages
				ToolName:   toolName,
				ToolCallID: msg.ToolCallID,
				Content:    result,
			})
			continue
		}

		// Handle plain content (legacy format)
		if msg.Content != "" && len(msg.MultiContent) == 0 && len(msg.ToolCalls) == 0 {
			streamable = append(streamable, StreamableMessage{
				Kind:    "text",
				Role:    string(msg.Role),
				Content: msg.Content,
			})
		}

		for _, contentBlock := range msg.MultiContent {
			if contentBlock.Text != "" {
				streamable = append(streamable, StreamableMessage{
					Kind:    "text",
					Role:    string(msg.Role),
					Content: contentBlock.Text,
				})
			}
		}

		if len(msg.ToolCalls) > 0 {
			for _, toolCall := range msg.ToolCalls {
				inputJSON, _ := json.Marshal(toolCall.Function.Arguments)
				streamable = append(streamable, StreamableMessage{
					Kind:       "tool-use",
					Role:       string(msg.Role),
					ToolName:   toolCall.Function.Name,
					ToolCallID: toolCall.ID,
					Input:      string(inputJSON),
				})
			}
		}
	}

	return streamable, nil
}

// streamResponseAPIMessages handles Response API format
func streamResponseAPIMessages(rawMessages json.RawMessage, toolResults map[string]tooltypes.StructuredToolResult) ([]StreamableMessage, error) {
	var conversationData struct {
		ConversationItems []ConversationItem `json:"conversation_items"`
	}

	if err := json.Unmarshal(rawMessages, &conversationData); err != nil {
		return nil, errors.Wrap(err, "error unmarshaling response API messages")
	}

	streamable := make([]StreamableMessage, 0)

	// Process conversation items in chronological order
	for _, convItem := range conversationData.ConversationItems {
		if convItem.Type == "input" {
			// Parse input item to determine its type
			var itemType struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(convItem.Item, &itemType); err != nil {
				continue
			}

			switch itemType.Type {
			case "message":
				// User message
				var inputMsg struct {
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
					Role string `json:"role"`
				}
				if err := json.Unmarshal(convItem.Item, &inputMsg); err != nil {
					continue
				}

				// Extract text content
				for _, content := range inputMsg.Content {
					if content.Type == "input_text" && content.Text != "" {
						streamable = append(streamable, StreamableMessage{
							Kind:    "text",
							Role:    inputMsg.Role,
							Content: content.Text,
						})
					}
				}

			case "function_call_output":
				// Tool result
				var toolResult struct {
					CallID string `json:"call_id"`
					Output string `json:"output"`
				}
				if err := json.Unmarshal(convItem.Item, &toolResult); err != nil {
					continue
				}

				result := toolResult.Output
				toolName := ""
				if structuredResult, ok := toolResults[toolResult.CallID]; ok {
					toolName = structuredResult.ToolName
					if jsonData, err := structuredResult.MarshalJSON(); err == nil {
						result = string(jsonData)
					}
				}

				streamable = append(streamable, StreamableMessage{
					Kind:       "tool-result",
					Role:       "assistant",
					ToolName:   toolName,
					ToolCallID: toolResult.CallID,
					Content:    result,
				})
			}

		} else if convItem.Type == "output" {
			// Parse output item
			var itemType struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(convItem.Item, &itemType); err != nil {
				continue
			}

			switch itemType.Type {
			case "message":
				// Assistant message
				var outputMsg struct {
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
				}
				if err := json.Unmarshal(convItem.Item, &outputMsg); err != nil {
					continue
				}

				for _, content := range outputMsg.Content {
					if content.Type == "output_text" && content.Text != "" {
						streamable = append(streamable, StreamableMessage{
							Kind:    "text",
							Role:    "assistant",
							Content: content.Text,
						})
					}
				}

			case "function_call":
				// Tool use
				var functionCall struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
					CallID    string `json:"call_id"`
				}
				if err := json.Unmarshal(convItem.Item, &functionCall); err != nil {
					continue
				}

				streamable = append(streamable, StreamableMessage{
					Kind:       "tool-use",
					Role:       "assistant",
					ToolName:   functionCall.Name,
					ToolCallID: functionCall.CallID,
					Input:      functionCall.Arguments,
				})

			case "reasoning":
				// Reasoning content (o-series models)
				var reasoning struct {
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
				}
				if err := json.Unmarshal(convItem.Item, &reasoning); err != nil {
					continue
				}

				for _, content := range reasoning.Content {
					if content.Text != "" {
						streamable = append(streamable, StreamableMessage{
							Kind:    "thinking",
							Role:    "assistant",
							Content: content.Text,
						})
					}
				}
			}
		}
	}

	return streamable, nil
}

// ExtractMessages converts the internal message format to the common format
func ExtractMessages(data []byte, toolResults map[string]tooltypes.StructuredToolResult) ([]llmtypes.Message, error) {
	// Try to determine the API type
	var typeCheck struct {
		APIType string `json:"api_type"`
	}
	if err := json.Unmarshal(data, &typeCheck); err == nil && typeCheck.APIType == "responses" {
		// Response API format
		return extractResponseAPIMessages(data, toolResults)
	}

	// Chat Completion API format (existing implementation)
	return extractChatCompletionMessages(data, toolResults)
}

// extractChatCompletionMessages handles Chat Completion API format
func extractChatCompletionMessages(data []byte, toolResults map[string]tooltypes.StructuredToolResult) ([]llmtypes.Message, error) {
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

// extractResponseAPIMessages handles Response API format
func extractResponseAPIMessages(data []byte, toolResults map[string]tooltypes.StructuredToolResult) ([]llmtypes.Message, error) {
	var conversationData struct {
		ConversationItems []ConversationItem `json:"conversation_items"`
	}

	if err := json.Unmarshal(data, &conversationData); err != nil {
		return nil, errors.Wrap(err, "error unmarshaling response API messages")
	}

	result := make([]llmtypes.Message, 0)

	// Process conversation items in chronological order
	for _, convItem := range conversationData.ConversationItems {
		if convItem.Type == "input" {
			// Try to parse as user message first
			var inputMsg struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
				Role string `json:"role"`
			}
			if err := json.Unmarshal(convItem.Item, &inputMsg); err == nil && inputMsg.Role != "" {
				// Extract text content
				for _, content := range inputMsg.Content {
					if content.Type == "input_text" && content.Text != "" {
						result = append(result, llmtypes.Message{
							Role:    inputMsg.Role,
							Content: content.Text,
						})
					}
				}
				continue
			}

			// Try to parse as function call output (tool result)
			var toolResult struct {
				CallID string `json:"call_id"`
				Output string `json:"output"`
			}
			if err := json.Unmarshal(convItem.Item, &toolResult); err == nil && toolResult.CallID != "" {
				text := toolResult.Output
				// Use CLI rendering if structured result is available
				if structuredResult, ok := toolResults[toolResult.CallID]; ok {
					registry := renderers.NewRendererRegistry()
					text = registry.Render(structuredResult)
				}

				result = append(result, llmtypes.Message{
					Role:    "assistant",
					Content: fmt.Sprintf("🔄 Tool result:\n%s", text),
				})
			}

		} else if convItem.Type == "output" {
			// Parse output item
			var itemType struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(convItem.Item, &itemType); err != nil {
				continue
			}

			switch itemType.Type {
			case "message":
				// Assistant message
				var outputMsg struct {
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
					Role string `json:"role"`
				}
				if err := json.Unmarshal(convItem.Item, &outputMsg); err != nil {
					continue
				}

				// Extract text content
				for _, content := range outputMsg.Content {
					if content.Type == "output_text" && content.Text != "" {
						result = append(result, llmtypes.Message{
							Role:    outputMsg.Role,
							Content: content.Text,
						})
					}
				}

			case "function_call":
				// Tool call
				var toolCall struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}
				if err := json.Unmarshal(convItem.Item, &toolCall); err != nil {
					continue
				}

				result = append(result, llmtypes.Message{
					Role:    "assistant",
					Content: fmt.Sprintf("🔧 Using tool: %s: %s", toolCall.Name, toolCall.Arguments),
				})
			}
		}
	}

	return result, nil
}
