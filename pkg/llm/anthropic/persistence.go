package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/types/llm"
)

// SaveConversation saves the current thread to the conversation store
func (t *AnthropicThread) SaveConversation(ctx context.Context, summarise bool) error {
	t.conversationMu.Lock()
	defer t.conversationMu.Unlock()

	if !t.isPersisted || t.store == nil {
		return nil
	}

	// Marshall the messages to JSON
	rawMessages, err := json.Marshal(t.messages)
	if err != nil {
		return fmt.Errorf("failed to marshal conversation messages: %w", err)
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
		return fmt.Errorf("failed to load conversation: %w", err)
	}

	// Check if this is an Anthropic model conversation
	if record.ModelType != "" && record.ModelType != "anthropic" {
		return fmt.Errorf("incompatible model type: %s", record.ModelType)
	}

	// Reset current messages
	messages, err := DeserializeMessages(record.RawMessages)
	if err != nil {
		return fmt.Errorf("failed to deserialize conversation messages: %w", err)
	}
	t.messages = messages

	// Restore usage statistics
	t.usage = &record.Usage
	t.summary = record.Summary
	t.state.SetFileLastAccess(record.FileLastAccess)
	return nil
}

type contentBlock map[string]interface{}
type messageParam struct {
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
}

func DeserializeMessages(b []byte) ([]anthropic.MessageParam, error) {
	messages := []anthropic.MessageParam{}
	var listRawMessages []json.RawMessage
	if err := json.Unmarshal(b, &listRawMessages); err != nil {
		return nil, fmt.Errorf("failed to unmarshal conversation messages: %w", err)
	}

	for _, rawMessage := range listRawMessages {
		var msg anthropic.MessageParam
		var shallowMessage messageParam
		if err := json.Unmarshal(rawMessage, &shallowMessage); err != nil {
			return nil, fmt.Errorf("failed to unmarshal conversation messages: %w", err)
		}

		msg.Role = anthropic.MessageParamRole(shallowMessage.Role)
		msg.Content = []anthropic.ContentBlockParamUnion{}
		for _, content := range shallowMessage.Content {
			switch content["type"].(string) {
			case "text":
				for _, field := range []string{"text"} {
					if _, ok := content[field]; !ok {
						return nil, fmt.Errorf("missing field: %s", field)
					}
				}
				msg.Content = append(msg.Content, anthropic.ContentBlockParamUnion{
					OfRequestTextBlock: &anthropic.TextBlockParam{
						Type: "text",
						Text: content["text"].(string),
					},
				})
			case "tool_use":
				for _, field := range []string{"id", "name", "input"} {
					if _, ok := content[field]; !ok {
						return nil, fmt.Errorf("missing field: %s", field)
					}
				}
				msg.Content = append(msg.Content, anthropic.ContentBlockParamUnion{
					OfRequestToolUseBlock: &anthropic.ToolUseBlockParam{
						Type:  "tool_use",
						ID:    content["id"].(string),
						Name:  content["name"].(string),
						Input: content["input"],
					},
				})
			case "thinking":
				for _, field := range []string{"thinking", "signature", "type"} {
					if _, ok := content[field]; !ok {
						return nil, fmt.Errorf("missing field: %s", field)
					}
				}
				msg.Content = append(msg.Content, anthropic.ContentBlockParamUnion{
					OfRequestThinkingBlock: &anthropic.ThinkingBlockParam{
						Type:      "thinking",
						Thinking:  content["thinking"].(string),
						Signature: content["signature"].(string),
					},
				})
			case "tool_result":
				for _, field := range []string{"tool_use_id", "content"} {
					if _, ok := content[field]; !ok {
						return nil, fmt.Errorf("missing field: %s", field)
					}
				}
				toolCallContentList, ok := content["content"].([]interface{})
				if !ok {
					return nil, fmt.Errorf("content is not a list")
				}
				if len(toolCallContentList) == 0 {
					return nil, fmt.Errorf("content is empty")
				}
				toolCallContent := toolCallContentList[0].(map[string]interface{})
				for _, field := range []string{"text"} {
					if _, ok := toolCallContent[field]; !ok {
						return nil, fmt.Errorf("missing field: %s", field)
					}
				}
				isError, ok := toolCallContent["is_error"].(bool)
				if !ok {
					isError = false
				}
				msg.Content = append(msg.Content, anthropic.ContentBlockParamUnion{
					OfRequestToolResultBlock: &anthropic.ToolResultBlockParam{
						Type:      "tool_result",
						ToolUseID: content["tool_use_id"].(string),
						IsError:   param.Opt[bool]{Value: isError},
						Content: []anthropic.ToolResultBlockParamContentUnion{
							{
								OfRequestTextBlock: &anthropic.TextBlockParam{
									Type: "text",
									Text: toolCallContent["text"].(string),
								},
							},
						},
					},
				})
			}
		}

		if len(msg.Content) != 0 {
			messages = append(messages, msg)
		}
	}

	return messages, nil
}

// ExtractMessages parses the raw messages from a conversation record
func ExtractMessages(rawMessages json.RawMessage) ([]llm.Message, error) {
	// Parse the raw JSON messages directly
	var rawMsgs []map[string]interface{}
	if err := json.Unmarshal(rawMessages, &rawMsgs); err != nil {
		return nil, fmt.Errorf("error parsing raw messages: %v", err)
	}

	var messages []llm.Message
	for _, msg := range rawMsgs {
		role, ok := msg["role"].(string)
		if !ok {
			continue // Skip if role is not a string or doesn't exist
		}

		content, ok := msg["content"].([]interface{})
		if !ok || len(content) == 0 {
			continue // Skip if content is not an array or is empty
		}

		// Process each content block in the message
		for _, block := range content {
			blockMap, ok := block.(map[string]interface{})
			if !ok {
				continue // Skip if block is not a map
			}

			// Extract block type
			blockType, ok := blockMap["type"].(string)
			if !ok {
				continue // Skip if type is not a string or doesn't exist
			}

			// Extract message content based on block type
			switch blockType {
			case "text":
				// Add text content
				text, ok := blockMap["text"].(string)
				if !ok {
					continue // Skip if text is not a string or doesn't exist
				}

				messages = append(messages, llm.Message{
					Role:    role,
					Content: text,
				})

			case "tool_use":
				// Add tool usage as content
				input, ok := blockMap["input"]
				if !ok {
					continue // Skip if input is not found
				}

				inputJSON, err := json.Marshal(input)
				if err != nil {
					continue // Skip if marshaling fails
				}

				messages = append(messages, llm.Message{
					Role:    role,
					Content: fmt.Sprintf("🔧 Using tool: %s", string(inputJSON)),
				})

			case "tool_result":
				// Add tool result as content
				resultContent, ok := blockMap["content"].([]interface{})
				if !ok || len(resultContent) == 0 {
					continue // Skip if content is not an array or is empty
				}

				resultBlock, ok := resultContent[0].(map[string]interface{})
				if !ok {
					continue // Skip if first element is not a map
				}

				if resultBlock["type"] == "text" {
					result, ok := resultBlock["text"].(string)
					if !ok {
						continue // Skip if text is not a string
					}

					messages = append(messages, llm.Message{
						Role:    "assistant",
						Content: fmt.Sprintf("🔄 Tool result: %s", result),
					})
				}

			case "thinking":
				// Add thinking content
				thinking, ok := blockMap["thinking"].(string)
				if !ok {
					continue // Skip if thinking is not a string
				}

				messages = append(messages, llm.Message{
					Role:    "assistant",
					Content: fmt.Sprintf("💭 Thinking: %s", thinking),
				})
			}
		}
	}

	return messages, nil
}
