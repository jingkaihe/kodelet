// Package responses implements storage types for the OpenAI Responses API.
// These types provide a stable serialization format that doesn't depend on
// the SDK's discriminated union types which don't roundtrip through JSON well.
package responses

import (
	"encoding/json"
	"strings"

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
	Status    string `json:"status,omitempty"`
	Action    string `json:"action,omitempty"`

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
		if item.Type == "message" && len(item.RawItem) > 0 {
			if inputItem, ok := messageInputItemFromRawItem(item.RawItem); ok {
				result = append(result, inputItem)
				continue
			}
		}

		if item.Type == "message" {
			if inputItem, ok := messageInputItemFromStoredItem(item); ok {
				result = append(result, inputItem)
				continue
			}
		}

		if len(item.RawItem) > 0 {
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

		case "web_search_call":
			result = append(result, responses.ResponseInputItemUnionParam{
				OfWebSearchCall: &responses.ResponseFunctionWebSearchParam{
					ID:     item.CallID,
					Status: responses.ResponseFunctionWebSearchStatus(item.Status),
					Action: webSearchActionParamFromStoredItem(item),
				},
			})

		case "compaction":
			result = append(result, responses.ResponseInputItemParamOfCompaction(item.EncryptedContent))
		}
	}

	return result
}

func messageInputItemFromStoredItem(item StoredInputItem) (responses.ResponseInputItemUnionParam, bool) {
	role := strings.ToLower(strings.TrimSpace(item.Role))
	switch role {
	case "assistant":
		var rawMessage struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Phase  string `json:"phase"`
		}
		if len(item.RawItem) > 0 {
			_ = json.Unmarshal(item.RawItem, &rawMessage)
		}
		// Codex resumes must replay assistant history as output-message items.
		// Rebuilding these as input_text content causes the backend to reject the request.
		content := []responses.ResponseOutputMessageContentUnionParam{{
			OfOutputText: &responses.ResponseOutputTextParam{Text: item.Content},
		}}
		status := responses.ResponseOutputMessageStatusCompleted
		switch strings.ToLower(strings.TrimSpace(rawMessage.Status)) {
		case string(responses.ResponseOutputMessageStatusInProgress):
			status = responses.ResponseOutputMessageStatusInProgress
		case string(responses.ResponseOutputMessageStatusIncomplete):
			status = responses.ResponseOutputMessageStatusIncomplete
		}
		outputItem := responses.ResponseInputItemParamOfOutputMessage(content, rawMessage.ID, status)
		if outputItem.OfOutputMessage != nil {
			outputItem.OfOutputMessage.ID = rawMessage.ID
			outputItem.OfOutputMessage.Status = status
			switch strings.ToLower(strings.TrimSpace(rawMessage.Phase)) {
			case string(responses.ResponseOutputMessagePhaseCommentary):
				outputItem.OfOutputMessage.Phase = responses.ResponseOutputMessagePhaseCommentary
			case string(responses.ResponseOutputMessagePhaseFinalAnswer):
				outputItem.OfOutputMessage.Phase = responses.ResponseOutputMessagePhaseFinalAnswer
			}
		}
		return outputItem, true
	case "user", "system", "developer":
		parsedRole, ok := parseStoredMessageRole(role)
		if !ok {
			return responses.ResponseInputItemUnionParam{}, false
		}
		return responses.ResponseInputItemParamOfMessage(item.Content, parsedRole), true
	default:
		return responses.ResponseInputItemUnionParam{}, false
	}
}

func messageInputItemFromRawItem(raw json.RawMessage) (responses.ResponseInputItemUnionParam, bool) {
	var rawMessage struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
		ID      string          `json:"id"`
		Status  string          `json:"status"`
		Phase   string          `json:"phase"`
	}

	if err := json.Unmarshal(raw, &rawMessage); err != nil {
		return responses.ResponseInputItemUnionParam{}, false
	}

	role := responses.EasyInputMessageRole(rawMessage.Role)
	if role == "" {
		return responses.ResponseInputItemUnionParam{}, false
	}

	var stringContent string
	if err := json.Unmarshal(rawMessage.Content, &stringContent); err == nil {
		return responses.ResponseInputItemUnionParam{
			OfMessage: &responses.EasyInputMessageParam{
				Role:    role,
				Content: responses.EasyInputMessageContentUnionParam{OfString: param.NewOpt(stringContent)},
			},
		}, true
	}

	var parts []struct {
		Type     string `json:"type"`
		Text     string `json:"text,omitempty"`
		ImageURL string `json:"image_url,omitempty"`
	}
	if err := json.Unmarshal(rawMessage.Content, &parts); err != nil {
		return responses.ResponseInputItemUnionParam{}, false
	}

	contentParts := make(responses.ResponseInputMessageContentListParam, 0, len(parts))
	outputParts := make([]responses.ResponseOutputMessageContentUnionParam, 0, len(parts))
	hasSupportedPart := false
	for _, part := range parts {
		switch part.Type {
		case "output_text":
			outputParts = append(outputParts, responses.ResponseOutputMessageContentUnionParam{
				OfOutputText: &responses.ResponseOutputTextParam{Text: part.Text},
			})
			hasSupportedPart = true
		case "input_text":
			// Keep input_text for user/developer/system messages, but do not reuse it
			// verbatim for assistant messages when rebuilding request history.
			contentParts = append(contentParts, responses.ResponseInputContentUnionParam{
				OfInputText: &responses.ResponseInputTextParam{Text: part.Text},
			})
			hasSupportedPart = true
		case "input_image":
			contentParts = append(contentParts, responses.ResponseInputContentUnionParam{
				OfInputImage: &responses.ResponseInputImageParam{ImageURL: param.NewOpt(part.ImageURL)},
			})
			hasSupportedPart = true
		}
	}
	if !hasSupportedPart {
		return responses.ResponseInputItemUnionParam{}, false
	}

	if role == responses.EasyInputMessageRoleAssistant {
		if len(outputParts) == 0 {
			// Older Codex transcripts may persist assistant text as input_text.
			// Accept that shape on load, but normalize it back to output_text on replay.
			for _, part := range parts {
				if part.Type == "input_text" {
					outputParts = append(outputParts, responses.ResponseOutputMessageContentUnionParam{
						OfOutputText: &responses.ResponseOutputTextParam{Text: part.Text},
					})
				}
			}
		}
		if len(outputParts) == 0 {
			return responses.ResponseInputItemUnionParam{}, false
		}
		status := responses.ResponseOutputMessageStatusCompleted
		switch strings.ToLower(strings.TrimSpace(rawMessage.Status)) {
		case string(responses.ResponseOutputMessageStatusInProgress):
			status = responses.ResponseOutputMessageStatusInProgress
		case string(responses.ResponseOutputMessageStatusIncomplete):
			status = responses.ResponseOutputMessageStatusIncomplete
		}
		outputItem := responses.ResponseInputItemParamOfOutputMessage(outputParts, rawMessage.ID, status)
		if outputItem.OfOutputMessage != nil {
			outputItem.OfOutputMessage.ID = rawMessage.ID
			outputItem.OfOutputMessage.Status = status
			switch strings.ToLower(strings.TrimSpace(rawMessage.Phase)) {
			case string(responses.ResponseOutputMessagePhaseCommentary):
				outputItem.OfOutputMessage.Phase = responses.ResponseOutputMessagePhaseCommentary
			case string(responses.ResponseOutputMessagePhaseFinalAnswer):
				outputItem.OfOutputMessage.Phase = responses.ResponseOutputMessagePhaseFinalAnswer
			}
		}
		return outputItem, true
	}

	return responses.ResponseInputItemUnionParam{
		OfMessage: &responses.EasyInputMessageParam{
			Role:    role,
			Content: responses.EasyInputMessageContentUnionParam{OfInputItemContentList: contentParts},
		},
	}, true
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

func webSearchActionParamFromStoredItem(item StoredInputItem) responses.ResponseFunctionWebSearchActionUnionParam {
	switch item.Action {
	case "open_page":
		return responses.ResponseFunctionWebSearchActionUnionParam{
			OfOpenPage: &responses.ResponseFunctionWebSearchActionOpenPageParam{URL: param.NewOpt(item.Content)},
		}
	case "find_in_page":
		return responses.ResponseFunctionWebSearchActionUnionParam{
			OfFind: &responses.ResponseFunctionWebSearchActionFindParam{
				URL:     item.Content,
				Pattern: item.Arguments,
			},
		}
	default:
		queries := []string{}
		if strings.TrimSpace(item.Content) != "" {
			queries = append(queries, item.Content)
		}
		return responses.ResponseFunctionWebSearchActionUnionParam{
			OfSearch: &responses.ResponseFunctionWebSearchActionSearchParam{Queries: queries},
		}
	}
}
