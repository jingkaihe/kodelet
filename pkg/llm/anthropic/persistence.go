package anthropic

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/jingkaihe/kodelet/pkg/conversations"
)

// saveConversation saves the current thread to the conversation store
func (t *AnthropicThread) saveConversation() error {
	if !t.isPersisted || t.store == nil {
		return nil
	}

	// Marshall the messages to JSON
	rawMessages, err := json.Marshal(t.messages)
	if err != nil {
		return fmt.Errorf("failed to marshal conversation messages: %w", err)
	}

	// Extract first user message for display purposes
	firstUserPrompt := ""
	for _, msg := range t.messages {
		if msg.Role == "user" {
			for _, block := range msg.Content {
				if text := block.GetText(); text != nil {
					firstUserPrompt = *text
					break
				}
			}
			if firstUserPrompt != "" {
				break
			}
		}
	}

	// Create a new conversation record
	record := conversations.ConversationRecord{
		ID:              t.conversationID,
		RawMessages:     rawMessages,
		ModelType:       "anthropic",
		Usage:           t.usage,
		Metadata:        map[string]interface{}{"model": t.config.Model},
		FirstUserPrompt: firstUserPrompt,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	// Save the record
	return t.store.Save(record)
}

// loadConversation loads a conversation from the store into the thread
func (t *AnthropicThread) loadConversation() error {
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
	if _, err := t.DeserializeMessages(record.RawMessages); err != nil {
		return fmt.Errorf("failed to deserialize conversation messages: %w", err)
	}

	// Restore usage statistics
	t.usage = record.Usage

	return nil
}

// type contentUnion struct {
// 	anthropic.TextBlockParam             `json:",omitzero,inline"`
// 	anthropic.ImageBlockParam            `json:",omitzero,inline"`
// 	anthropic.ToolUseBlockParam          `json:",omitzero,inline"`
// 	anthropic.ToolResultBlockParam       `json:",omitzero,inline"`
// 	anthropic.DocumentBlockParam         `json:",omitzero,inline"`
// 	anthropic.ThinkingBlockParam         `json:",omitzero,inline"`
// 	anthropic.RedactedThinkingBlockParam `json:",omitzero,inline"`
// }

type contentBlock map[string]interface{}
type messageParam struct {
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
}

func (t *AnthropicThread) DeserializeMessages(b []byte) ([]anthropic.MessageParam, error) {
	t.messages = []anthropic.MessageParam{}
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
			t.messages = append(t.messages, msg)
		}
	}

	return t.messages, nil
}
