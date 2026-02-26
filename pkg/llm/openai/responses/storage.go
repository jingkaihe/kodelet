// Package responses implements storage types for the OpenAI Responses API.
// These types provide a stable serialization format that doesn't depend on
// the SDK's discriminated union types which don't roundtrip through JSON well.
package responses

import (
	"encoding/json"

	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
)

// StoredInputItem represents a conversation item in a format suitable for JSON storage.
// This is used instead of the SDK's ResponseInputItemUnionParam which uses discriminated
// unions that don't serialize/deserialize reliably.
//
// Items are stored in order as they occur during the conversation:
// - User messages
// - Assistant reasoning (thinking)
// - Function calls
// - Function call outputs
// - Assistant messages
//
// This mirrors Anthropic's approach where thinking blocks are stored inline with messages.
type StoredInputItem struct {
	Type string `json:"type"` // "message", "function_call", "function_call_output", "reasoning", "compaction"

	// Message fields (when Type == "message")
	Role    string `json:"role,omitempty"`    // "user", "assistant", "system", "developer"
	Content string `json:"content,omitempty"` // Text content

	// Function call fields (when Type == "function_call")
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`

	// Function call output fields (when Type == "function_call_output")
	Output string `json:"output,omitempty"`

	// Compaction fields (when Type == "compaction")
	EncryptedContent string `json:"encrypted_content,omitempty"`

	// Reasoning fields (when Type == "reasoning")
	// Reasoning string is stored in Content field with Role == "assistant"

	// RawItem stores the original Responses API item payload when available.
	// This lets us preserve compact output variants without lossy field mapping.
	RawItem json.RawMessage `json:"raw_item,omitempty"`
}

// fromStoredItems converts storage format back to SDK input items for API calls.
// Reasoning items are skipped as they're only for display, not sent to the API.
func fromStoredItems(items []StoredInputItem) []responses.ResponseInputItemUnionParam {
	result := make([]responses.ResponseInputItemUnionParam, 0, len(items))

	for _, item := range items {
		if item.Type != "message" && len(item.RawItem) > 0 {
			if inputItem, ok := inputItemFromRawItem(item.RawItem); ok {
				result = append(result, inputItem)
				continue
			}
		}

		switch item.Type {
		case "reasoning":
			// Reasoning is for display only, skip for API calls
			continue

		case "message":
			role := responses.EasyInputMessageRole(item.Role)
			result = append(result, responses.ResponseInputItemUnionParam{
				OfMessage: &responses.EasyInputMessageParam{
					Role:    role,
					Content: responses.EasyInputMessageContentUnionParam{OfString: param.NewOpt(item.Content)},
				},
			})

		case "function_call":
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

		case "compaction":
			result = append(result, responses.ResponseInputItemParamOfCompaction(item.EncryptedContent))
		}
	}

	return result
}

func inputItemFromRawItem(raw json.RawMessage) (responses.ResponseInputItemUnionParam, bool) {
	var inputItem responses.ResponseInputItemUnionParam
	if err := json.Unmarshal(raw, &inputItem); err == nil {
		return inputItem, true
	}

	var compactVariant struct {
		Type             string `json:"type"`
		EncryptedContent string `json:"encrypted_content"`
	}
	if err := json.Unmarshal(raw, &compactVariant); err == nil && compactVariant.Type == "compaction_summary" && compactVariant.EncryptedContent != "" {
		return responses.ResponseInputItemParamOfCompaction(compactVariant.EncryptedContent), true
	}

	return responses.ResponseInputItemUnionParam{}, false
}
