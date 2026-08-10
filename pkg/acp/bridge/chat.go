package bridge

import (
	"encoding/json"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/tools/renderers"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// ACPChatEventSink translates streamed chat events into ACP session updates.
type ACPChatEventSink struct {
	handler   *ACPMessageHandler
	sender    UpdateSender
	sessionID acptypes.SessionID
}

// NewACPChatEventSink creates a chat event sink for one ACP session.
func NewACPChatEventSink(sender UpdateSender, sessionID acptypes.SessionID) *ACPChatEventSink {
	return &ACPChatEventSink{
		handler:   NewACPMessageHandler(sender, sessionID),
		sender:    sender,
		sessionID: sessionID,
	}
}

// Send implements chat.ChatEventSink.
func (s *ACPChatEventSink) Send(event chat.ChatEvent) error {
	if s == nil || s.handler == nil {
		return nil
	}
	switch event.Kind {
	case "text":
		if text := chatEventText(event); text != "" {
			s.handler.HandleText(text)
		}
	case "text-delta":
		s.handler.HandleTextDelta(event.Delta)
	case "thinking":
		if text := chatEventText(event); text != "" {
			s.handler.HandleThinking(text)
		}
	case "thinking-start":
		s.handler.HandleThinkingStart()
	case "thinking-delta":
		s.handler.HandleThinkingDelta(event.Delta)
	case "thinking-end":
		s.handler.HandleThinkingBlockEnd()
	case "content-end":
		s.handler.HandleContentBlockEnd()
	case "tool-use":
		s.handler.HandleToolUse(event.ToolCallID, event.ToolName, event.Input)
	case "tool-update":
		if event.ToolResult != nil {
			s.handler.HandleToolUpdate(event.ToolCallID, event.ToolName, chatEventToolResult(event))
		}
	case "tool-result":
		if event.ToolResult != nil {
			s.handler.HandleToolResult(event.ToolCallID, event.ToolName, chatEventToolResult(event))
		}
	case "user-message":
		text := chatEventText(event)
		if text == "" || s.sender == nil {
			return nil
		}
		return s.sender.SendUpdate(s.sessionID, userMessageUpdate(text))
	}
	return nil
}

// ReplayConversationHistory converts normalized persisted history into ACP updates.
func ReplayConversationHistory(sender UpdateSender, sessionID acptypes.SessionID, messages []conversations.StreamableMessage) error {
	sink := NewACPChatEventSink(sender, sessionID)
	for _, message := range messages {
		switch message.Kind {
		case "text":
			if message.Role == "user" {
				if err := replayUserMessage(sender, sessionID, message); err != nil {
					return err
				}
				continue
			}
			if err := sink.Send(chat.ChatEvent{Kind: "text", Content: message.Content}); err != nil {
				return err
			}
		case "thinking":
			if err := sink.Send(chat.ChatEvent{Kind: "thinking", Content: message.Content}); err != nil {
				return err
			}
		case "tool-use":
			if err := sink.Send(chat.ChatEvent{
				Kind:       "tool-use",
				ToolCallID: message.ToolCallID,
				ToolName:   message.ToolName,
				Input:      message.Input,
			}); err != nil {
				return err
			}
		case "tool-result":
			structured := tooltypes.StructuredToolResult{ToolName: message.ToolName, Success: true}
			output := message.ToolOutput
			if output == "" {
				output = message.Content
			}
			if err := json.Unmarshal([]byte(message.Content), &structured); err == nil {
				if message.ToolOutput == "" {
					output = renderers.NewRendererRegistry().Render(structured)
				}
			}
			if structured.ToolName == "" {
				structured.ToolName = message.ToolName
			}
			if err := sink.Send(chat.ChatEvent{
				Kind:       "tool-result",
				ToolCallID: message.ToolCallID,
				ToolName:   message.ToolName,
				ToolOutput: output,
				ToolResult: &structured,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func replayUserMessage(sender UpdateSender, sessionID acptypes.SessionID, message conversations.StreamableMessage) error {
	images := imageBlocksFromRawItem(message.RawItem)
	content := strings.TrimSpace(message.Content)
	if len(images) > 0 && (strings.HasPrefix(content, "Inline image input") || strings.HasPrefix(content, "Image input:")) {
		content = ""
	}
	if content != "" {
		if err := sender.SendUpdate(sessionID, userMessageUpdate(message.Content)); err != nil {
			return err
		}
	}
	for _, block := range images {
		if err := sender.SendUpdate(sessionID, map[string]any{
			"sessionUpdate": acptypes.UpdateUserMessageChunk,
			"content":       block,
		}); err != nil {
			return err
		}
	}
	return nil
}

func imageBlocksFromRawItem(rawItem json.RawMessage) []acptypes.ContentBlock {
	if len(rawItem) == 0 {
		return nil
	}
	var item struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(rawItem, &item); err != nil || len(item.Content) == 0 {
		return nil
	}
	var parts []struct {
		Type     string `json:"type"`
		ImageURL string `json:"image_url"`
	}
	if err := json.Unmarshal(item.Content, &parts); err != nil {
		return nil
	}
	blocks := make([]acptypes.ContentBlock, 0, len(parts))
	for _, part := range parts {
		if part.Type != "input_image" && part.Type != "image_url" && part.Type != "image" {
			continue
		}
		mimeType, data, ok := parseImageDataURL(part.ImageURL)
		if ok {
			blocks = append(blocks, acptypes.ContentBlock{Type: acptypes.ContentTypeImage, MimeType: mimeType, Data: data})
			continue
		}
		if uri := strings.TrimSpace(part.ImageURL); uri != "" {
			blocks = append(blocks, acptypes.ContentBlock{Type: acptypes.ContentTypeImage, URI: uri})
		}
	}
	return blocks
}

func parseImageDataURL(value string) (string, string, bool) {
	metadata, data, found := strings.Cut(strings.TrimPrefix(value, "data:"), ",")
	if !strings.HasPrefix(value, "data:") || !found || !strings.Contains(metadata, ";base64") || data == "" {
		return "", "", false
	}
	mimeType, _, _ := strings.Cut(metadata, ";")
	if strings.TrimSpace(mimeType) == "" {
		mimeType = "application/octet-stream"
	}
	return mimeType, data, true
}

func chatEventText(event chat.ChatEvent) string {
	if event.Delta != "" {
		return event.Delta
	}
	if text, ok := event.Content.(string); ok {
		return text
	}
	blocks, ok := event.Content.([]chat.ChatContentBlock)
	if ok {
		parts := make([]string, 0, len(blocks))
		for _, block := range blocks {
			if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
				parts = append(parts, block.Text)
			}
		}
		return strings.Join(parts, "\n\n")
	}
	values, ok := event.Content.([]any)
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		block, ok := value.(map[string]any)
		if !ok || block["type"] != "text" {
			continue
		}
		if text, ok := block["text"].(string); ok && strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func userMessageUpdate(text string) map[string]any {
	return map[string]any{
		"sessionUpdate": acptypes.UpdateUserMessageChunk,
		"content": acptypes.ContentBlock{
			Type: acptypes.ContentTypeText,
			Text: text,
		},
	}
}

func chatEventToolResult(event chat.ChatEvent) tooltypes.ToolResult {
	structured := tooltypes.StructuredToolResult{}
	if event.ToolResult != nil {
		structured = *event.ToolResult
	}
	if structured.ToolName == "" {
		structured.ToolName = event.ToolName
	}
	return structuredChatToolResult{output: event.ToolOutput, structured: structured}
}

type structuredChatToolResult struct {
	output     string
	structured tooltypes.StructuredToolResult
}

func (r structuredChatToolResult) AssistantFacing() string { return r.output }
func (r structuredChatToolResult) IsError() bool {
	return !r.structured.Success || strings.TrimSpace(r.structured.Error) != ""
}
func (r structuredChatToolResult) GetError() string  { return r.structured.Error }
func (r structuredChatToolResult) GetResult() string { return r.output }
func (r structuredChatToolResult) StructuredData() tooltypes.StructuredToolResult {
	return r.structured
}
