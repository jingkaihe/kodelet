package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// MessageFormatter handles formatting of messages for display
type MessageFormatter struct {
	width          int
	userStyle      lipgloss.Style
	assistantStyle lipgloss.Style
	systemStyle    lipgloss.Style
}

// NewMessageFormatter creates a new message formatter (Tokyo Night)
func NewMessageFormatter(width int) *MessageFormatter {
	return &MessageFormatter{
		width:          width,
		userStyle:      lipgloss.NewStyle().Foreground(lipgloss.Color("#7dcfff")).Bold(true), // Cyan
		assistantStyle: lipgloss.NewStyle().Foreground(lipgloss.Color("#bb9af7")).Bold(true), // Purple
		systemStyle:    lipgloss.NewStyle().Foreground(lipgloss.Color("#9ece6a")).Bold(true), // Green
	}
}

// SetWidth updates the width for message formatting
func (f *MessageFormatter) SetWidth(width int) {
	f.width = width
}

// FormatMessage formats a single message for display
func (f *MessageFormatter) FormatMessage(msg llmtypes.Message) string {
	switch msg.Role {
	case "":
		// No prefix for system messages
		return msg.Content
	case "user":
		// Create a styled user message
		userPrefix := f.userStyle.Render("You")
		messageText := lipgloss.NewStyle().
			PaddingLeft(1).
			Width(f.width - 15). // Ensure text wraps within viewport width
			Render(msg.Content)
		return userPrefix + " → " + messageText
	default:
		// Create a styled assistant message
		assistantPrefix := f.assistantStyle.Render("Assistant")
		messageText := lipgloss.NewStyle().
			PaddingLeft(1).
			Width(f.width - 15). // Ensure text wraps within viewport width
			Render(msg.Content)
		return assistantPrefix + " → " + messageText
	}
}

// FormatMessages formats multiple messages for display
func (f *MessageFormatter) FormatMessages(messages []llmtypes.Message) string {
	var content string

	// Format and render each message
	for i, msg := range messages {
		renderedMsg := f.FormatMessage(msg)

		// Add padding between messages
		if i > 0 {
			content += "\n\n"
		}

		content += renderedMsg
	}

	return content
}

// FormatAssistantEvent formats an assistant event for display
func FormatAssistantEvent(event llmtypes.MessageEvent) string {
	switch event.Type {
	case llmtypes.EventTypeText:
		return event.Content
	case llmtypes.EventTypeToolUse:
		return fmt.Sprintf("🔧 Using tool: %s", event.Content)
	case llmtypes.EventTypeToolResult:
		return fmt.Sprintf("🔄 Tool result: %s", event.Content)
	case llmtypes.EventTypeThinking:
		return fmt.Sprintf("💭 Thinking: %s", event.Content)
	}

	return ""
}

// FormatBashOutput formats the output of a bash command
func FormatBashOutput(command, output string) string {
	var builder strings.Builder
	builder.WriteString("\n## command\n")
	builder.WriteString(command)
	builder.WriteString("\n\n## output\n")
	builder.WriteString(output)
	builder.WriteString("\n")
	return builder.String()
}
