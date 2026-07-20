package tui

import (
	"fmt"
	"strings"

	chat "github.com/jingkaihe/kodelet/pkg/chat"
)

func (m *model) applyChatEvent(event chat.ChatEvent) {
	if event.ConversationID != "" {
		m.conversationID = event.ConversationID
	}

	switch event.Kind {
	case "conversation":
		return
	case "user-message":
		content := userMessageContentText(event.Content)
		if content == "" {
			return
		}
		m.removeQueuedSteering(content)
		if m.hasLastUserEntry(content) {
			return
		}
		m.entries = append(m.entries, chatEntry{kind: entryUser, content: content})
		return
	case "text", "text-delta":
		idx := m.ensureAssistantEntry()
		text := event.Delta
		if text == "" {
			text, _ = event.Content.(string)
		}
		appendTextBlock(&m.entries[idx], text)
	case "thinking-start":
		idx := m.ensureAssistantEntry()
		blockIdx := appendThoughtBlock(&m.entries[idx])
		m.entries[idx].blocks[blockIdx].thoughts = append(m.entries[idx].blocks[blockIdx].thoughts, thoughtBlock{})
	case "thinking", "thinking-delta":
		idx := m.ensureAssistantEntry()
		text := event.Delta
		if text == "" {
			text, _ = event.Content.(string)
		}
		blockIdx := appendThoughtBlock(&m.entries[idx])
		if len(m.entries[idx].blocks[blockIdx].thoughts) == 0 {
			m.entries[idx].blocks[blockIdx].thoughts = append(m.entries[idx].blocks[blockIdx].thoughts, thoughtBlock{})
		}
		last := len(m.entries[idx].blocks[blockIdx].thoughts) - 1
		m.entries[idx].blocks[blockIdx].thoughts[last].text += text
	case "thinking-end":
		idx := m.ensureAssistantEntry()
		for blockIdx := len(m.entries[idx].blocks) - 1; blockIdx >= 0; blockIdx-- {
			block := &m.entries[idx].blocks[blockIdx]
			if block.kind != blockThoughts || len(block.thoughts) == 0 {
				continue
			}
			block.thoughts[len(block.thoughts)-1].done = true
			break
		}
	case "tool-use":
		idx := m.ensureAssistantEntry()
		blockIdx := appendToolBlock(&m.entries[idx])
		m.entries[idx].blocks[blockIdx].tools = append(m.entries[idx].blocks[blockIdx].tools, toolCall{
			id:    event.ToolCallID,
			name:  event.ToolName,
			input: event.Input,
		})
	case "tool-update", "tool-result":
		complete := event.Kind == "tool-result"
		idx := m.ensureAssistantEntry()
		resultText := structuredToolResultText(event.ToolResult)
		if blockIdx, toolIdx := findToolLocation(m.entries[idx], event.ToolCallID); blockIdx >= 0 && toolIdx >= 0 {
			tool := &m.entries[idx].blocks[blockIdx].tools[toolIdx]
			tool.result = resultText
			tool.done = complete
			if event.ToolResult != nil {
				tool.failed = complete && !event.ToolResult.Success
				tool.structured = event.ToolResult
				if tool.name == "" {
					tool.name = event.ToolResult.ToolName
				}
			}
			return
		}
		for blockIdx := len(m.entries[idx].blocks) - 1; blockIdx >= 0; blockIdx-- {
			if m.entries[idx].blocks[blockIdx].kind != blockTools {
				continue
			}
			for toolIdx := range m.entries[idx].blocks[blockIdx].tools {
				if m.entries[idx].blocks[blockIdx].tools[toolIdx].id == event.ToolCallID {
					tool := &m.entries[idx].blocks[blockIdx].tools[toolIdx]
					tool.result = resultText
					tool.done = complete
					if event.ToolResult != nil {
						tool.failed = complete && !event.ToolResult.Success
						tool.structured = event.ToolResult
						if tool.name == "" {
							tool.name = event.ToolResult.ToolName
						}
					}
					return
				}
			}
		}
		failed := false
		if complete && event.ToolResult != nil {
			failed = !event.ToolResult.Success
		}
		toolName := event.ToolName
		if event.ToolResult != nil && strings.TrimSpace(toolName) == "" {
			toolName = event.ToolResult.ToolName
		}
		blockIdx := appendToolBlock(&m.entries[idx])
		m.entries[idx].blocks[blockIdx].tools = append(m.entries[idx].blocks[blockIdx].tools, toolCall{
			id:         event.ToolCallID,
			name:       toolName,
			result:     resultText,
			done:       complete,
			failed:     failed,
			structured: event.ToolResult,
		})
	case "usage":
		if event.Usage != nil {
			m.usage = *event.Usage
		}
	case "error":
		idx := m.ensureAssistantEntry()
		appendTextBlock(&m.entries[idx], event.Error)
	}
}

func (m *model) ensureAssistantEntry() int {
	if len(m.entries) == 0 || m.entries[len(m.entries)-1].kind != entryAssistant {
		m.entries = append(m.entries, chatEntry{kind: entryAssistant})
	}
	return len(m.entries) - 1
}

func userMessageContentText(content any) string {
	switch content := content.(type) {
	case string:
		return strings.TrimSpace(content)
	case []chat.ChatContentBlock:
		return strings.TrimSpace(textFromWebContentBlocks(content))
	case []any:
		return strings.TrimSpace(textFromAnyContentBlocks(content))
	default:
		return ""
	}
}

func textFromWebContentBlocks(blocks []chat.ChatContentBlock) string {
	parts := make([]string, 0, len(blocks))
	imageCount := 0
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if text := strings.TrimSpace(block.Text); text != "" {
				parts = append(parts, text)
			}
		case "image":
			imageCount++
		}
	}
	if imageCount > 0 {
		parts = append(parts, fmt.Sprintf("[%d image%s]", imageCount, pluralSuffix(imageCount)))
	}
	return strings.Join(parts, "\n")
}

func textFromAnyContentBlocks(blocks []any) string {
	parts := make([]string, 0, len(blocks))
	imageCount := 0
	for _, rawBlock := range blocks {
		block, ok := rawBlock.(map[string]any)
		if !ok {
			continue
		}
		typeValue, _ := block["type"].(string)
		switch typeValue {
		case "text":
			if text, _ := block["text"].(string); strings.TrimSpace(text) != "" {
				parts = append(parts, strings.TrimSpace(text))
			}
		case "image":
			imageCount++
		}
	}
	if imageCount > 0 {
		parts = append(parts, fmt.Sprintf("[%d image%s]", imageCount, pluralSuffix(imageCount)))
	}
	return strings.Join(parts, "\n")
}

func pluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func (m model) hasLastUserEntry(content string) bool {
	content = strings.TrimSpace(content)
	if content == "" || len(m.entries) == 0 {
		return false
	}
	last := m.entries[len(m.entries)-1]
	return last.kind == entryUser && strings.TrimSpace(last.content) == content
}

func (m *model) removeQueuedSteering(content string) {
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}
	for i, queued := range m.queuedSteering {
		if strings.TrimSpace(queued) != content {
			continue
		}
		m.queuedSteering = append(m.queuedSteering[:i], m.queuedSteering[i+1:]...)
		return
	}
}
