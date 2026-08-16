package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/llm/base"
	"github.com/jingkaihe/kodelet/pkg/tools/renderers"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
)

// isEmptyContentBlock reports whether a block carries no usable content. A thinking
// block with a signature counts as content: the signature is needed to replay the turn.
func isEmptyContentBlock(contentBlock anthropic.ContentBlockParamUnion) bool {
	if textBlock := contentBlock.OfText; textBlock != nil {
		return strings.TrimSpace(textBlock.Text) == ""
	}
	if thinkingBlock := contentBlock.OfThinking; thinkingBlock != nil {
		return strings.TrimSpace(thinkingBlock.Thinking) == "" &&
			strings.TrimSpace(thinkingBlock.Signature) == ""
	}
	return false
}

func withoutEmptyContentBlocks(content []anthropic.ContentBlockParamUnion) []anthropic.ContentBlockParamUnion {
	kept := make([]anthropic.ContentBlockParamUnion, 0, len(content))
	for _, contentBlock := range content {
		if isEmptyContentBlock(contentBlock) {
			continue
		}
		kept = append(kept, contentBlock)
	}
	return kept
}

// cleanedAnthropicMessages drops orphaned tool use and empty blocks from the tail.
// Blocks are stripped individually so a message survives unless nothing is left.
func cleanedAnthropicMessages(messages []anthropic.MessageParam) []anthropic.MessageParam {
	cleaned := slices.Clone(messages)

	for len(cleaned) > 0 {
		lastMessage := cleaned[len(cleaned)-1]

		if isMessageToolUse(lastMessage) {
			cleaned = cleaned[:len(cleaned)-1]
			continue
		}

		content := withoutEmptyContentBlocks(lastMessage.Content)
		if len(content) == 0 {
			cleaned = cleaned[:len(cleaned)-1]
			continue
		}

		if len(content) != len(lastMessage.Content) {
			lastMessage.Content = content
			cleaned[len(cleaned)-1] = lastMessage
		}

		break
	}

	return cleaned
}

func cleanedAnthropicMessagesForFork(messages []anthropic.MessageParam) []anthropic.MessageParam {
	cleaned := slices.Clone(messages)
	for len(cleaned) > 0 {
		lastIndex := len(cleaned) - 1
		lastMessage := cleaned[lastIndex]
		content := make([]anthropic.ContentBlockParamUnion, 0, len(lastMessage.Content))
		for _, contentBlock := range lastMessage.Content {
			if lastMessage.Role == anthropic.MessageParamRoleAssistant && contentBlock.OfToolUse != nil {
				continue
			}
			if isEmptyContentBlock(contentBlock) {
				continue
			}
			content = append(content, contentBlock)
		}
		if len(content) == 0 {
			cleaned = cleaned[:lastIndex]
			continue
		}
		if len(content) != len(lastMessage.Content) {
			lastMessage.Content = content
			cleaned[lastIndex] = lastMessage
		}
		break
	}
	return cleaned
}

func (t *Thread) cleanupOrphanedMessages() {
	t.messages = cleanedAnthropicMessages(t.messages)
}

// SaveConversation saves the current thread to the conversation store
func (t *Thread) SaveConversation(ctx context.Context) error {
	t.ConversationMu.Lock()
	defer t.ConversationMu.Unlock()

	if !t.Persisted || t.Store == nil {
		return nil
	}
	record, err := t.buildConversationRecord(ctx, cleanedAnthropicMessages(t.messages), true)
	if err != nil {
		return err
	}
	return t.Store.Save(ctx, record)
}

// ForkConversation snapshots the live thread into a new persisted conversation.
func (t *Thread) ForkConversation(ctx context.Context) (string, error) {
	if t.ConversationForkBlocked() {
		return "", llm.ErrConversationForkUnavailable
	}
	t.ConversationMu.Lock()
	defer t.ConversationMu.Unlock()

	if !t.Persisted || t.Store == nil {
		return "", llm.ErrConversationForkUnavailable
	}
	record, err := t.buildConversationRecord(ctx, cleanedAnthropicMessagesForFork(t.messages), false)
	if err != nil {
		return "", err
	}
	forkOptions := convtypes.ConversationForkOptions{Mode: convtypes.ConversationForkModeLiveSnapshot}
	if initiator, ok := convtypes.ConversationForkInitiatorFromContext(ctx); ok {
		forkOptions.Initiator = &initiator
	}
	forked := convtypes.ForkConversationRecordWithOptions(record, forkOptions)
	if err := t.Store.Save(ctx, forked); err != nil {
		return "", errors.Wrap(err, "failed to save forked conversation")
	}
	return forked.ID, nil
}

func (t *Thread) buildConversationRecord(ctx context.Context, messagesToSave []anthropic.MessageParam, updateThreadMetadata bool) (convtypes.ConversationRecord, error) {
	rawMessages, err := json.Marshal(messagesToSave)
	if err != nil {
		return convtypes.ConversationRecord{}, errors.Wrap(err, "failed to marshal conversation messages")
	}

	toolResults := t.GetStructuredToolResults()
	messages, err := StreamMessages(rawMessages, toolResults)
	if err != nil {
		return convtypes.ConversationRecord{}, errors.Wrap(err, "failed to parse conversation messages for naming")
	}
	metadata := t.GetMetadata()
	metadata = conversations.PreserveStoredConversationName(ctx, t.Store, t.ConversationID, metadata)
	if explicitName := conversations.ExplicitConversationName(metadata); updateThreadMetadata && explicitName != "" {
		t.SetMetadataValue(conversations.ConversationNameMetadataKey, explicitName)
	}
	fallbackName := base.FirstUserMessageName(conversations.ApplyDisplayToStreamableMessages(conversationsFromAnthropic(messages), metadata))
	metadata, name := conversations.EnsureConversationName(metadata, fallbackName)
	if automaticName := conversations.AutomaticConversationName(metadata); updateThreadMetadata && automaticName != "" {
		t.SetMetadataValue(conversations.ConversationAutoNameMetadataKey, automaticName)
	}

	// Create a new conversation record
	if profile := strings.TrimSpace(t.Config.Profile); profile != "" {
		metadata["profile"] = profile
	}
	metadata["model"] = t.Config.Model
	snapshotConfig := t.Config
	if strings.TrimSpace(snapshotConfig.Provider) == "" {
		snapshotConfig.Provider = "anthropic"
	}
	metadata, err = conversations.AddConfigSnapshot(metadata, snapshotConfig)
	if err != nil {
		return convtypes.ConversationRecord{}, errors.Wrap(err, "failed to persist conversation config snapshot")
	}

	return convtypes.ConversationRecord{
		ID:          t.ConversationID,
		CWD:         t.Config.WorkingDirectory,
		RawMessages: rawMessages,
		Provider:    "anthropic",
		Usage:       t.GetUsage(),
		Metadata:    metadata,
		Summary:     name,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		ToolResults: toolResults,
	}, nil
}

// loadConversation loads a conversation from the store into the thread.
// This method is used as a callback for the base.Thread's EnablePersistence method.
// Note: The base thread's ConversationMu is already locked when this is called.
func (t *Thread) loadConversation(ctx context.Context) {
	if !t.Persisted || t.Store == nil {
		return
	}

	// Try to load the conversation
	record, err := t.Store.Load(ctx, t.ConversationID)
	if err != nil {
		// Log error but don't return - caller expects void return
		return
	}

	// Check if this is an Anthropic model conversation
	if record.Provider != "" && record.Provider != "anthropic" {
		return
	}

	// Reset current messages
	messages, err := DeserializeMessages(record.RawMessages)
	if err != nil {
		return
	}
	t.messages = messages

	t.cleanupOrphanedMessages()
	// Restore usage statistics
	t.Usage = &record.Usage
	t.SetMetadata(record.Metadata)
	// Restore structured tool results
	t.SetStructuredToolResults(record.ToolResults)
}

// DeserializeMessages deserializes a JSON byte array into Anthropic message parameters
func DeserializeMessages(b []byte) ([]anthropic.MessageParam, error) {
	var messages []anthropic.MessageParam
	if err := json.Unmarshal(b, &messages); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal conversation messages")
	}

	return messages, nil
}

// StreamableMessage contains parsed message data for streaming
type StreamableMessage struct {
	Kind       string // "text", "tool-use", "tool-result", "thinking"
	Role       string // "user", "assistant", "system"
	Content    string // Text content
	RawItem    json.RawMessage
	ToolName   string // For tool use/result
	ToolCallID string // For matching tool results
	Input      string // For tool use (JSON string)
	ToolOutput string // Display output retained alongside structured results
}

// StreamMessages parses raw messages into normalized persisted conversation entries.
func StreamMessages(rawMessages json.RawMessage, toolResults map[string]tooltypes.StructuredToolResult) ([]StreamableMessage, error) {
	messages, err := DeserializeMessages(rawMessages)
	if err != nil {
		return nil, errors.Wrap(err, "failed to deserialize anthropic messages")
	}

	var streamable []StreamableMessage

	for _, msg := range messages {
		for _, contentBlock := range msg.Content {
			if textBlock := contentBlock.OfText; textBlock != nil && textBlock.Text != "" {
				streamable = append(streamable, StreamableMessage{
					Kind:    "text",
					Role:    string(msg.Role),
					Content: textBlock.Text,
				})
			}

			if imageBlock := contentBlock.OfImage; imageBlock != nil {
				if imageText := anthropicImageDisplayString(imageBlock); imageText != "" {
					streamable = append(streamable, StreamableMessage{
						Kind:    "text",
						Role:    string(msg.Role),
						Content: imageText,
						RawItem: anthropicImageRawItem(string(msg.Role), imageBlock),
					})
				}
			}

			if toolUseBlock := contentBlock.OfToolUse; toolUseBlock != nil {
				inputJSON, _ := json.Marshal(toolUseBlock.Input)
				streamable = append(streamable, StreamableMessage{
					Kind:       "tool-use",
					Role:       string(msg.Role),
					ToolName:   toolUseBlock.Name,
					ToolCallID: toolUseBlock.ID,
					Input:      string(inputJSON),
				})
			}

			if toolResultBlock := contentBlock.OfToolResult; toolResultBlock != nil {
				var output strings.Builder
				for _, resultContent := range toolResultBlock.Content {
					if textBlock := resultContent.OfText; textBlock != nil {
						output.WriteString(textBlock.Text)
					}
				}
				result := output.String()
				toolName := ""

				if structuredResult, ok := toolResults[toolResultBlock.ToolUseID]; ok {
					toolName = structuredResult.ToolName
					if jsonData, err := structuredResult.MarshalJSON(); err == nil {
						result = string(jsonData)
					}
				}

				streamable = append(streamable, StreamableMessage{
					Kind:       "tool-result",
					Role:       string(msg.Role),
					ToolName:   toolName,
					ToolCallID: toolResultBlock.ToolUseID,
					Content:    result,
					ToolOutput: output.String(),
				})
			}

			if thinkingBlock := contentBlock.OfThinking; thinkingBlock != nil && thinkingBlock.Thinking != "" {
				streamable = append(streamable, StreamableMessage{
					Kind:    "thinking",
					Role:    string(msg.Role),
					Content: thinkingBlock.Thinking,
				})
			}
		}
	}

	return streamable, nil
}

// ExtractMessages parses the raw messages from a conversation record
func ExtractMessages(rawMessages json.RawMessage, toolResults map[string]tooltypes.StructuredToolResult) ([]llm.Message, error) {
	// Deserialize the raw messages using the existing DeserializeMessages function
	anthropicMessages, err := DeserializeMessages(rawMessages)
	if err != nil {
		return nil, errors.Wrap(err, "error deserializing messages")
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
			if imageBlock := contentBlock.OfImage; imageBlock != nil {
				if imageText := anthropicImageDisplayString(imageBlock); imageText != "" {
					messages = append(messages, llm.Message{
						Role:    string(msg.Role),
						Content: imageText,
					})
				}
			}
			// Handle tool use blocks
			if toolUseBlock := contentBlock.OfToolUse; toolUseBlock != nil {
				inputJSON, err := json.Marshal(toolUseBlock.Input)
				if err != nil {
					continue // Skip if marshaling fails
				}
				messages = append(messages, llm.Message{
					Role:    string(msg.Role),
					Content: fmt.Sprintf("🔧 Using tool: %s with input: %s", toolUseBlock.Name, string(inputJSON)),
				})
			}
			// Handle tool result blocks
			if toolResultBlock := contentBlock.OfToolResult; toolResultBlock != nil {
				for _, resultContent := range toolResultBlock.Content {
					if textBlock := resultContent.OfText; textBlock != nil {
						text := textBlock.Text
						// Use CLI rendering if structured result is available
						if structuredResult, ok := toolResults[toolResultBlock.ToolUseID]; ok {
							registry := renderers.NewRendererRegistry()
							text = registry.Render(structuredResult)
						}
						messages = append(messages, llm.Message{
							Role:    "assistant",
							Content: fmt.Sprintf("🔄 Tool result:\n%s", text),
						})
					}
				}
			}
			// Handle thinking blocks
			if thinkingBlock := contentBlock.OfThinking; thinkingBlock != nil {
				messages = append(messages, llm.Message{
					Role:    "assistant",
					Content: fmt.Sprintf("💭 Thinking: %s", strings.TrimLeft(thinkingBlock.Thinking, "\n")),
				})
			}
		}
	}

	return messages, nil
}

func anthropicImageDisplayString(imageBlock *anthropic.ImageBlockParam) string {
	if imageBlock == nil {
		return ""
	}

	if imageBlock.Source.OfBase64 != nil {
		mediaType := strings.TrimSpace(string(imageBlock.Source.OfBase64.MediaType))
		if mediaType != "" {
			return fmt.Sprintf("Inline image input (%s).", mediaType)
		}
		return "Inline image input."
	}

	if imageBlock.Source.OfURL != nil && strings.TrimSpace(imageBlock.Source.OfURL.URL) != "" {
		return fmt.Sprintf("Image input: %s", imageBlock.Source.OfURL.URL)
	}

	return ""
}

func anthropicImageRawItem(role string, imageBlock *anthropic.ImageBlockParam) json.RawMessage {
	if imageBlock == nil {
		return nil
	}
	imageURL := ""
	if imageBlock.Source.OfBase64 != nil {
		mediaType := strings.TrimSpace(string(imageBlock.Source.OfBase64.MediaType))
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}
		if imageBlock.Source.OfBase64.Data != "" {
			imageURL = "data:" + mediaType + ";base64," + imageBlock.Source.OfBase64.Data
		}
	} else if imageBlock.Source.OfURL != nil {
		imageURL = strings.TrimSpace(imageBlock.Source.OfURL.URL)
	}
	if imageURL == "" {
		return nil
	}
	payload, err := json.Marshal(map[string]any{
		"role": role,
		"content": []map[string]any{{
			"type":      "input_image",
			"image_url": imageURL,
		}},
	})
	if err != nil {
		return nil
	}
	return payload
}
