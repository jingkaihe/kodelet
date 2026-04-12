package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/llm/base"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/tools/renderers"
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
func (t *Thread) cleanupOrphanedMessages() {
	t.messages = cleanedOpenAIMessages(t.messages)
}

func cleanedOpenAIMessages(messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	cleaned := slices.Clone(messages)
	for len(cleaned) > 0 {
		lastMessage := cleaned[len(cleaned)-1]

		// Remove the last message if it is empty (no content and no tool calls)
		if lastMessage.Content == "" && len(lastMessage.ToolCalls) == 0 && lastMessage.Role != openai.ChatMessageRoleTool && !hasOpenAIMultiContent(lastMessage.MultiContent) {
			cleaned = cleaned[:len(cleaned)-1]
			continue
		}

		// Remove the synthetic multimodal follow-up injected after tool results.
		if isOpenAIInternalFollowupImageMessage(cleaned) {
			cleaned = cleaned[:len(cleaned)-1]
			continue
		}

		// Remove the last message if it's an assistant message with tool calls,
		// as it must be followed by tool result messages
		if lastMessage.Role == openai.ChatMessageRoleAssistant && len(lastMessage.ToolCalls) > 0 {
			cleaned = cleaned[:len(cleaned)-1]
			continue
		}

		break
	}

	return cleaned
}

func isOpenAIInternalFollowupImageMessage(messages []openai.ChatCompletionMessage) bool {
	if len(messages) < 3 {
		return false
	}

	lastMessage := messages[len(messages)-1]
	if lastMessage.Role != openai.ChatMessageRoleUser || strings.TrimSpace(lastMessage.Content) != "" || len(lastMessage.ToolCalls) > 0 {
		return false
	}
	if !isImageOnlyOpenAIMultiContent(lastMessage.MultiContent) {
		return false
	}

	toolResultStart := len(messages) - 1
	for toolResultStart > 0 && messages[toolResultStart-1].Role == openai.ChatMessageRoleTool {
		toolResultStart--
	}
	if toolResultStart == len(messages)-1 || toolResultStart == 0 {
		return false
	}

	assistantMessage := messages[toolResultStart-1]
	if assistantMessage.Role != openai.ChatMessageRoleAssistant || len(assistantMessage.ToolCalls) == 0 {
		return false
	}

	return true
}

func isImageOnlyOpenAIMultiContent(parts []openai.ChatMessagePart) bool {
	if len(parts) == 0 {
		return false
	}
	hasImage := false
	for _, part := range parts {
		switch part.Type {
		case openai.ChatMessagePartTypeImageURL:
			if part.ImageURL == nil || strings.TrimSpace(part.ImageURL.URL) == "" {
				return false
			}
			hasImage = true
		case openai.ChatMessagePartTypeText:
			if strings.TrimSpace(part.Text) != "" {
				return false
			}
		default:
			return false
		}
	}
	return hasImage
}

// SaveConversation saves the current thread to the conversation store
func (t *Thread) SaveConversation(ctx context.Context, summarize bool) error {
	t.ConversationMu.Lock()
	defer t.ConversationMu.Unlock()

	if !t.Persisted || t.Store == nil {
		return nil
	}

	// Clean up orphaned messages before saving
	messagesToSave := cleanedOpenAIMessages(t.messages)
	summary := base.FirstUserMessageFallback(conversationsFromOpenAI(streamMessagesForSummary(messagesToSave, t.GetStructuredToolResults())))

	// Generate a new summary if requested and enabled; otherwise keep the first user message.
	if summarize {
		if t.Config.ConversationSummaryMode.UsesLLM() {
			generatedSummary, err := t.ShortSummary(ctx)
			if err != nil {
				logger.G(ctx).WithError(err).Error("failed to generate summary")
			} else if generatedSummary != "" {
				summary = generatedSummary
			}
		}
	}
	t.summary = summary

	// Serialize the thread state
	messagesJSON, err := json.Marshal(messagesToSave)
	if err != nil {
		return errors.Wrap(err, "error marshaling messages")
	}

	metadata := map[string]any{
		"model":    t.Config.Model,
		"api_mode": "chat_completions",
		"platform": resolvePlatformName(t.Config),
	}
	if profile := strings.TrimSpace(t.Config.Profile); profile != "" {
		metadata["profile"] = profile
	}

	// Build the conversation record
	record := convtypes.ConversationRecord{
		ID:             t.ConversationID,
		CWD:            t.Config.WorkingDirectory,
		RawMessages:    messagesJSON,
		Provider:       "openai",
		Usage:          *t.Usage,
		Metadata:       metadata,
		Summary:        t.summary,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		FileLastAccess: t.State.FileLastAccess(),
		ToolResults:    t.GetStructuredToolResults(),
	}

	// Save to the store
	return t.Store.Save(ctx, record)
}

func streamMessagesForSummary(messages []openai.ChatCompletionMessage, toolResults map[string]tooltypes.StructuredToolResult) []StreamableMessage {
	rawMessages, err := json.Marshal(messages)
	if err != nil {
		return nil
	}

	streamable, err := StreamMessages(rawMessages, toolResults)
	if err != nil {
		return nil
	}

	return streamable
}

// loadConversation loads a conversation from the store.
// This method is called by the base.Thread.EnablePersistence via the LoadConversation callback.
// NOTE: This function expects the caller to hold ConversationMu lock.
func (t *Thread) loadConversation(ctx context.Context) {
	if !t.Persisted || t.Store == nil {
		return
	}

	// Try to load the conversation
	record, err := t.Store.Load(ctx, t.ConversationID)
	if err != nil {
		return
	}

	// Check if this is an OpenAI model conversation
	if record.Provider != "" && record.Provider != "openai" {
		return
	}

	// Deserialize the messages
	var messages []openai.ChatCompletionMessage
	if err := json.Unmarshal(record.RawMessages, &messages); err != nil {
		return
	}

	t.messages = cleanedOpenAIMessages(messages)
	t.Usage = &record.Usage
	t.summary = record.Summary
	t.State.SetFileLastAccess(record.FileLastAccess)
	// Restore structured tool results
	t.SetStructuredToolResults(record.ToolResults)
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
}

// StreamMessages parses raw messages into streamable format for conversation streaming
func StreamMessages(rawMessages json.RawMessage, toolResults map[string]tooltypes.StructuredToolResult) ([]StreamableMessage, error) {
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
				RawItem:    mustMarshalOpenAIMultiContent(msg.Role, msg.MultiContent),
			})
			continue
		}

		if msg.ReasoningContent != "" {
			streamable = append(streamable, StreamableMessage{
				Kind:    "thinking",
				Role:    msg.Role,
				Content: strings.TrimLeft(msg.ReasoningContent, "\n"),
			})
		}

		// Handle plain text content stored directly on the message.
		if msg.Content != "" && len(msg.MultiContent) == 0 && len(msg.ToolCalls) == 0 {
			streamable = append(streamable, StreamableMessage{
				Kind:    "text",
				Role:    msg.Role,
				Content: msg.Content,
			})
		}

		if rawItem := mustMarshalOpenAIMultiContent(msg.Role, msg.MultiContent); len(rawItem) > 0 {
			streamable = append(streamable, StreamableMessage{
				Kind:    "text",
				Role:    msg.Role,
				Content: openAIMultiContentText(msg.MultiContent),
				RawItem: rawItem,
			})
		}

		if len(msg.ToolCalls) > 0 {
			for _, toolCall := range msg.ToolCalls {
				inputJSON, _ := json.Marshal(toolCall.Function.Arguments)
				streamable = append(streamable, StreamableMessage{
					Kind:       "tool-use",
					Role:       msg.Role,
					ToolName:   toolCall.Function.Name,
					ToolCallID: toolCall.ID,
					Input:      string(inputJSON),
				})
			}
		}
	}

	return streamable, nil
}

func mustMarshalOpenAIMultiContent(role string, parts []openai.ChatMessagePart) json.RawMessage {
	payload := openAIMultiContentPayload(parts)
	if len(payload) == 0 {
		return nil
	}
	b, err := json.Marshal(map[string]any{
		"role":    role,
		"content": payload,
	})
	if err != nil {
		return nil
	}
	return b
}

func openAIMultiContentPayload(parts []openai.ChatMessagePart) []map[string]any {
	if len(parts) == 0 {
		return nil
	}
	payload := make([]map[string]any, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case openai.ChatMessagePartTypeText:
			if strings.TrimSpace(part.Text) == "" {
				continue
			}
			payload = append(payload, map[string]any{"type": "input_text", "text": part.Text})
		case openai.ChatMessagePartTypeImageURL:
			if part.ImageURL == nil || strings.TrimSpace(part.ImageURL.URL) == "" {
				continue
			}
			payload = append(payload, map[string]any{"type": "input_image", "image_url": part.ImageURL.URL})
		}
	}
	return payload
}

func hasOpenAIMultiContent(parts []openai.ChatMessagePart) bool {
	return len(openAIMultiContentPayload(parts)) > 0
}

func openAIMultiContentText(parts []openai.ChatMessagePart) string {
	textParts := make([]string, 0, len(parts))
	for _, part := range parts {
		if part.Type != openai.ChatMessagePartTypeText || strings.TrimSpace(part.Text) == "" {
			continue
		}
		textParts = append(textParts, part.Text)
	}
	return strings.Join(textParts, "\n\n")
}

func openAIMultiContentDisplay(parts []openai.ChatMessagePart) string {
	blocks := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case openai.ChatMessagePartTypeText:
			if strings.TrimSpace(part.Text) != "" {
				blocks = append(blocks, part.Text)
			}
		case openai.ChatMessagePartTypeImageURL:
			if part.ImageURL == nil {
				continue
			}
			if imageText := openAIImageDisplayString(part.ImageURL.URL); imageText != "" {
				blocks = append(blocks, imageText)
			}
		}
	}
	return strings.Join(blocks, "\n\n")
}

func openAIImageDisplayString(imageURL string) string {
	if strings.TrimSpace(imageURL) == "" {
		return ""
	}
	if mediaType := openAIDataURLMediaType(imageURL); mediaType != "" {
		return fmt.Sprintf("Inline image input (%s).", mediaType)
	}
	return fmt.Sprintf("Image input: %s", imageURL)
}

func openAIDataURLMediaType(dataURL string) string {
	if !strings.HasPrefix(dataURL, "data:") {
		return ""
	}

	metadata, hasPayload := strings.CutPrefix(dataURL, "data:")
	if !hasPayload {
		return ""
	}
	header, _, found := strings.Cut(metadata, ",")
	if !found {
		return ""
	}
	mediaType, _, _ := strings.Cut(header, ";")
	return mediaType
}

// ExtractMessages converts the internal message format to the common format
func ExtractMessages(data []byte, toolResults map[string]tooltypes.StructuredToolResult) ([]llmtypes.Message, error) {
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

		if msg.ReasoningContent != "" {
			result = append(result, llmtypes.Message{
				Role:    "assistant",
				Content: fmt.Sprintf("💭 Thinking: %s", strings.TrimLeft(msg.ReasoningContent, "\n")),
			})
		}

		// Handle plain text content stored directly on the message.
		if msg.Content != "" && len(msg.MultiContent) == 0 && len(msg.ToolCalls) == 0 {
			result = append(result, llmtypes.Message{
				Role:    msg.Role,
				Content: msg.Content,
			})
		}

		if content := openAIMultiContentDisplay(msg.MultiContent); strings.TrimSpace(content) != "" {
			result = append(result, llmtypes.Message{
				Role:    msg.Role,
				Content: content,
			})
		}

		// Handle tool calls
		if len(msg.ToolCalls) > 0 {
			for _, toolCall := range msg.ToolCalls {
				inputJSON, err := json.Marshal(toolCall)
				if err != nil {
					continue
				}
				result = append(result, llmtypes.Message{
					Role:    msg.Role,
					Content: fmt.Sprintf("🔧 Using tool: %s", string(inputJSON)),
				})
			}
		}
	}

	return result, nil
}
