// Package responses implements storage types for the OpenAI Responses API.
// These types provide a stable serialization format that doesn't depend on
// the SDK's discriminated union types which don't roundtrip through JSON well.
package responses

import (
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
)

// StoredInputItem represents a conversation item in a format suitable for JSON storage.
// This is used instead of the SDK's ResponseInputItemUnionParam which uses discriminated
// unions that don't serialize/deserialize reliably.
type StoredInputItem struct {
	Type string `json:"type"` // "message", "function_call", "function_call_output", "reasoning"

	// Message fields (when Type == "message")
	Role    string `json:"role,omitempty"`    // "user", "assistant", "system", "developer"
	Content string `json:"content,omitempty"` // Text content

	// Function call fields (when Type == "function_call")
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`

	// Function call output fields (when Type == "function_call_output")
	Output string `json:"output,omitempty"`

	// Reasoning fields (when Type == "reasoning")
	// Reasoning string is stored in Content field
}

// toStoredItems converts SDK input items to storage format.
// The reasoningItems slice contains reasoning items with their insertion indices.
func toStoredItems(items []responses.ResponseInputItemUnionParam, reasoningItems []ReasoningItem) []StoredInputItem {
	result := make([]StoredInputItem, 0, len(items)+len(reasoningItems))

	// Build a map of reasoning items by index for efficient lookup
	reasoningByIndex := make(map[int]string)
	for _, r := range reasoningItems {
		reasoningByIndex[r.BeforeIndex] = r.Content
	}

	for i, item := range items {
		// Insert reasoning item before this item if it exists
		if reasoning, ok := reasoningByIndex[i]; ok {
			result = append(result, StoredInputItem{
				Type:    "reasoning",
				Role:    "assistant",
				Content: reasoning,
			})
		}

		if item.OfMessage != nil {
			msg := item.OfMessage
			content := ""
			if msg.Content.OfString.Valid() {
				content = msg.Content.OfString.Value
			} else if len(msg.Content.OfInputItemContentList) > 0 {
				for _, part := range msg.Content.OfInputItemContentList {
					if part.OfInputText != nil {
						content += part.OfInputText.Text
					}
				}
			}
			result = append(result, StoredInputItem{
				Type:    "message",
				Role:    string(msg.Role),
				Content: content,
			})
		}

		if item.OfFunctionCall != nil {
			call := item.OfFunctionCall
			result = append(result, StoredInputItem{
				Type:      "function_call",
				CallID:    call.CallID,
				Name:      call.Name,
				Arguments: call.Arguments,
			})
		}

		if item.OfFunctionCallOutput != nil {
			output := item.OfFunctionCallOutput
			outputStr := ""
			if output.Output.OfString.Valid() {
				outputStr = output.Output.OfString.Value
			}
			result = append(result, StoredInputItem{
				Type:   "function_call_output",
				CallID: output.CallID,
				Output: outputStr,
			})
		}
	}

	// Handle reasoning at the end (for the final message)
	if reasoning, ok := reasoningByIndex[len(items)]; ok {
		result = append(result, StoredInputItem{
			Type:    "reasoning",
			Role:    "assistant",
			Content: reasoning,
		})
	}

	return result
}

// ReasoningItem represents a reasoning block and its position in the conversation.
type ReasoningItem struct {
	BeforeIndex int    // Index in inputItems where this reasoning should appear before
	Content     string // The reasoning text
}

// fromStoredItems converts storage format back to SDK input items.
// Returns the input items and reasoning items with their positions.
func fromStoredItems(items []StoredInputItem) ([]responses.ResponseInputItemUnionParam, []ReasoningItem) {
	result := make([]responses.ResponseInputItemUnionParam, 0, len(items))
	reasoningItems := make([]ReasoningItem, 0)
	var pendingReasoning string

	for _, item := range items {
		switch item.Type {
		case "reasoning":
			// Store reasoning to be associated with the next item
			pendingReasoning = item.Content

		case "message":
			// If there was pending reasoning, associate it with this item
			if pendingReasoning != "" {
				reasoningItems = append(reasoningItems, ReasoningItem{
					BeforeIndex: len(result),
					Content:     pendingReasoning,
				})
				pendingReasoning = ""
			}

			role := responses.EasyInputMessageRole(item.Role)
			result = append(result, responses.ResponseInputItemUnionParam{
				OfMessage: &responses.EasyInputMessageParam{
					Role:    role,
					Content: responses.EasyInputMessageContentUnionParam{OfString: param.NewOpt(item.Content)},
				},
			})

		case "function_call":
			// If there was pending reasoning, associate it with this item
			if pendingReasoning != "" {
				reasoningItems = append(reasoningItems, ReasoningItem{
					BeforeIndex: len(result),
					Content:     pendingReasoning,
				})
				pendingReasoning = ""
			}

			result = append(result, responses.ResponseInputItemUnionParam{
				OfFunctionCall: &responses.ResponseFunctionToolCallParam{
					CallID:    item.CallID,
					Name:      item.Name,
					Arguments: item.Arguments,
				},
			})

		case "function_call_output":
			result = append(result, responses.ResponseInputItemUnionParam{
				OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
					CallID: item.CallID,
					Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
						OfString: param.NewOpt(item.Output),
					},
				},
			})
		}
	}

	// Handle any trailing reasoning
	if pendingReasoning != "" {
		reasoningItems = append(reasoningItems, ReasoningItem{
			BeforeIndex: len(result),
			Content:     pendingReasoning,
		})
	}

	return result, reasoningItems
}
