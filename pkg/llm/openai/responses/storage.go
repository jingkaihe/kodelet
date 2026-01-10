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
	Type string `json:"type"` // "message", "function_call", "function_call_output"

	// Message fields (when Type == "message")
	Role    string `json:"role,omitempty"`    // "user", "assistant", "system", "developer"
	Content string `json:"content,omitempty"` // Text content

	// Function call fields (when Type == "function_call")
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`

	// Function call output fields (when Type == "function_call_output")
	Output string `json:"output,omitempty"`
}

// toStoredItems converts SDK input items to storage format.
func toStoredItems(items []responses.ResponseInputItemUnionParam) []StoredInputItem {
	result := make([]StoredInputItem, 0, len(items))

	for _, item := range items {
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

	return result
}

// fromStoredItems converts storage format back to SDK input items.
func fromStoredItems(items []StoredInputItem) []responses.ResponseInputItemUnionParam {
	result := make([]responses.ResponseInputItemUnionParam, 0, len(items))

	for _, item := range items {
		switch item.Type {
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
		}
	}

	return result
}
