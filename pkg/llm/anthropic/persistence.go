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
					OfText: &anthropic.TextBlockParam{
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
					OfToolUse: &anthropic.ToolUseBlockParam{
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
					OfThinking: &anthropic.ThinkingBlockParam{
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
					OfToolResult: &anthropic.ToolResultBlockParam{
						Type:      "tool_result",
						ToolUseID: content["tool_use_id"].(string),
						IsError:   param.Opt[bool]{Value: isError},
						Content: []anthropic.ToolResultBlockParamContentUnion{
							{
								OfText: &anthropic.TextBlockParam{
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
	// Deserialize the raw messages using the existing DeserializeMessages function
	anthropicMessages, err := DeserializeMessages(rawMessages)
	if err != nil {
		return nil, fmt.Errorf("error deserializing messages: %w", err)
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
					Content: fmt.Sprintf("🔧 Using tool: %s", string(inputJSON)),
				})
			}
			// Handle tool result blocks
			if toolResultBlock := contentBlock.OfToolResult; toolResultBlock != nil {
				for _, resultContent := range toolResultBlock.Content {
					if textBlock := resultContent.OfText; textBlock != nil {
						messages = append(messages, llm.Message{
							Role:    "assistant",
							Content: fmt.Sprintf("🔄 Tool result: %s", textBlock.Text),
						})
					}
				}
			}
			// Handle thinking blocks
			if thinkingBlock := contentBlock.OfThinking; thinkingBlock != nil {
				messages = append(messages, llm.Message{
					Role:    "assistant",
					Content: fmt.Sprintf("💭 Thinking: %s", thinkingBlock.Thinking),
				})
			}
		}
	}

	return messages, nil
}
