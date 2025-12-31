package bridge

import (
	"strings"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
)

type mockSender struct {
	updates []any
}

func (m *mockSender) SendUpdate(_ acptypes.SessionID, update any) error {
	m.updates = append(m.updates, update)
	return nil
}

// mockTitleGenerator is a simple mock for testing
type mockTitleGenerator struct{}

func (m *mockTitleGenerator) GenerateTitle(toolName string, _ string) string {
	return toolName + "_title"
}

func TestACPMessageHandler_HandleText(t *testing.T) {
	sender := &mockSender{}
	handler := NewACPMessageHandler(sender, "test-session")

	handler.HandleText("Hello, world!")

	assert.Len(t, sender.updates, 1)
	update := sender.updates[0].(map[string]any)
	assert.Equal(t, acptypes.UpdateAgentMessageChunk, update["sessionUpdate"])

	content := update["content"].(map[string]any)
	assert.Equal(t, acptypes.ContentTypeText, content["type"])
	assert.Equal(t, "Hello, world!", content["text"])
}

func TestACPMessageHandler_HandleTextDelta(t *testing.T) {
	sender := &mockSender{}
	handler := NewACPMessageHandler(sender, "test-session")

	handler.HandleTextDelta("chunk1")
	handler.HandleTextDelta("chunk2")

	assert.Len(t, sender.updates, 2)

	update1 := sender.updates[0].(map[string]any)
	content1 := update1["content"].(map[string]any)
	assert.Equal(t, "chunk1", content1["text"])

	update2 := sender.updates[1].(map[string]any)
	content2 := update2["content"].(map[string]any)
	assert.Equal(t, "chunk2", content2["text"])
}

func TestACPMessageHandler_HandleToolUse(t *testing.T) {
	sender := &mockSender{}
	handler := NewACPMessageHandler(sender, "test-session", WithTitleGenerator(&mockTitleGenerator{}))

	handler.HandleToolUse("call_1", "file_read", `{"file_path": "/test.txt"}`)

	assert.Len(t, sender.updates, 2)

	toolCall := sender.updates[0].(map[string]any)
	assert.Equal(t, acptypes.UpdateToolCall, toolCall["sessionUpdate"])
	assert.Equal(t, "file_read_title", toolCall["title"])
	assert.Equal(t, acptypes.ToolKindRead, toolCall["kind"])
	assert.Equal(t, acptypes.ToolStatusPending, toolCall["status"])
	assert.Equal(t, "call_1", toolCall["toolCallId"])

	toolUpdate := sender.updates[1].(map[string]any)
	assert.Equal(t, acptypes.UpdateToolCallUpdate, toolUpdate["sessionUpdate"])
	assert.Equal(t, "call_1", toolUpdate["toolCallId"])
	assert.Equal(t, acptypes.ToolStatusInProgress, toolUpdate["status"])
}

func TestACPMessageHandler_HandleToolResult(t *testing.T) {
	sender := &mockSender{}
	handler := NewACPMessageHandler(sender, "test-session", WithTitleGenerator(&mockTitleGenerator{}))

	handler.HandleToolUse("call_1", "file_read", `{}`)
	handler.HandleToolResult("call_1", "file_read", tooltypes.BaseToolResult{Result: "file contents here"})

	assert.Len(t, sender.updates, 3)

	result := sender.updates[2].(map[string]any)
	assert.Equal(t, acptypes.UpdateToolCallUpdate, result["sessionUpdate"])
	assert.Equal(t, acptypes.ToolStatusCompleted, result["status"])
	assert.NotNil(t, result["content"])
}

func TestACPMessageHandler_HandleToolResult_Error(t *testing.T) {
	sender := &mockSender{}
	handler := NewACPMessageHandler(sender, "test-session", WithTitleGenerator(&mockTitleGenerator{}))

	handler.HandleToolUse("call_1", "bash", `{}`)
	handler.HandleToolResult("call_1", "bash", tooltypes.BaseToolResult{Error: "command not found"})

	result := sender.updates[2].(map[string]any)
	assert.Equal(t, acptypes.ToolStatusFailed, result["status"])
}

func TestACPMessageHandler_HandleThinking(t *testing.T) {
	sender := &mockSender{}
	handler := NewACPMessageHandler(sender, "test-session")

	handler.HandleThinking("I'm thinking about this...")

	assert.Len(t, sender.updates, 1)
	update := sender.updates[0].(map[string]any)
	assert.Equal(t, acptypes.UpdateThoughtChunk, update["sessionUpdate"])

	content := update["content"].(map[string]any)
	assert.Equal(t, "I'm thinking about this...", content["text"])
}

func TestACPMessageHandler_HandleThinkingDelta(t *testing.T) {
	sender := &mockSender{}
	handler := NewACPMessageHandler(sender, "test-session")

	handler.HandleThinkingDelta("thinking...")

	assert.Len(t, sender.updates, 1)
	update := sender.updates[0].(map[string]any)
	assert.Equal(t, acptypes.UpdateThoughtChunk, update["sessionUpdate"])
}

func TestToACPToolKind(t *testing.T) {
	tests := []struct {
		toolName string
		expected acptypes.ToolKind
	}{
		{"file_read", acptypes.ToolKindRead},
		{"grep_tool", acptypes.ToolKindRead},
		{"glob_tool", acptypes.ToolKindRead},
		{"file_write", acptypes.ToolKindEdit},
		{"file_edit", acptypes.ToolKindEdit},
		{"bash", acptypes.ToolKindExecute},
		{"code_execution", acptypes.ToolKindExecute},
		{"web_fetch", acptypes.ToolKindFetch},
		{"thinking", acptypes.ToolKindThink},
		{"subagent", acptypes.ToolKindSearch},
		{"unknown_tool", acptypes.ToolKindOther},
	}

	for _, tt := range tests {
		t.Run(tt.toolName, func(t *testing.T) {
			result := ToACPToolKind(tt.toolName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestContentBlocksToMessage(t *testing.T) {
	blocks := []acptypes.ContentBlock{
		{
			Type: acptypes.ContentTypeText,
			Text: "Hello",
		},
		{
			Type: acptypes.ContentTypeText,
			Text: "World",
		},
	}

	message, images := ContentBlocksToMessage(blocks)
	assert.Equal(t, "Hello\n\nWorld", message)
	assert.Empty(t, images)
}

func TestContentBlocksToMessage_WithImage(t *testing.T) {
	blocks := []acptypes.ContentBlock{
		{
			Type: acptypes.ContentTypeText,
			Text: "Check this image:",
		},
		{
			Type:     acptypes.ContentTypeImage,
			Data:     "base64data",
			MimeType: "image/png",
		},
	}

	message, images := ContentBlocksToMessage(blocks)
	assert.Equal(t, "Check this image:", message)
	assert.Len(t, images, 1)
	assert.Equal(t, "data:image/png;base64,base64data", images[0])
}

func TestContentBlocksToMessage_WithImageURI(t *testing.T) {
	blocks := []acptypes.ContentBlock{
		{
			Type: acptypes.ContentTypeImage,
			URI:  "https://example.com/image.png",
		},
	}

	message, images := ContentBlocksToMessage(blocks)
	assert.Empty(t, message)
	assert.Len(t, images, 1)
	assert.Equal(t, "https://example.com/image.png", images[0])
}

func TestContentBlocksToMessage_WithResource(t *testing.T) {
	blocks := []acptypes.ContentBlock{
		{
			Type: acptypes.ContentTypeResource,
			Resource: &acptypes.EmbeddedResource{
				URI:  "file:///test.txt",
				Text: "file content",
			},
		},
	}

	message, images := ContentBlocksToMessage(blocks)
	assert.Contains(t, message, "--- file:///test.txt ---")
	assert.Contains(t, message, "file content")
	assert.Empty(t, images)
}

func TestContentBlocksToMessage_WithResourceLink(t *testing.T) {
	blocks := []acptypes.ContentBlock{
		{
			Type: acptypes.ContentTypeResourceLink,
			URI:  "file:///test.txt",
		},
	}

	message, images := ContentBlocksToMessage(blocks)
	assert.Contains(t, message, "[Resource: file:///test.txt]")
	assert.Empty(t, images)
}

func TestContentBlocksToMessage_Empty(t *testing.T) {
	blocks := []acptypes.ContentBlock{}

	message, images := ContentBlocksToMessage(blocks)
	assert.Empty(t, message)
	assert.Empty(t, images)
}

func TestDefaultTitleGenerator_EmptyInput(t *testing.T) {
	gen := &DefaultTitleGenerator{}
	title := gen.GenerateTitle("file_read", "")
	assert.Equal(t, "file_read", title)
}

func TestDefaultTitleGenerator_FileRead(t *testing.T) {
	gen := &DefaultTitleGenerator{}
	title := gen.GenerateTitle("file_read", `{"file_path": "/path/to/test.txt"}`)
	assert.Equal(t, "file_read: test.txt", title)
}

func TestDefaultTitleGenerator_Bash(t *testing.T) {
	gen := &DefaultTitleGenerator{}
	title := gen.GenerateTitle("bash", `{"command": "ls -la"}`)
	assert.Equal(t, "bash: ls -la", title)
}

func TestDefaultTitleGenerator_BashLongCommand(t *testing.T) {
	gen := &DefaultTitleGenerator{}
	longCmd := strings.Repeat("a", 100)
	title := gen.GenerateTitle("bash", `{"command": "`+longCmd+`"}`)
	assert.Contains(t, title, "bash: ")
	assert.Contains(t, title, "...")
	assert.LessOrEqual(t, len(title), 80)
}

func TestDefaultTitleGenerator_Grep(t *testing.T) {
	gen := &DefaultTitleGenerator{}
	title := gen.GenerateTitle("grep_tool", `{"pattern": "func main"}`)
	assert.Equal(t, "grep: func main", title)
}

func TestDefaultTitleGenerator_InvalidJSON(t *testing.T) {
	gen := &DefaultTitleGenerator{}
	title := gen.GenerateTitle("file_read", "not json")
	assert.Equal(t, "file_read", title)
}

func TestDefaultTitleGenerator_UnknownTool(t *testing.T) {
	gen := &DefaultTitleGenerator{}
	title := gen.GenerateTitle("unknown_tool", `{"some": "param"}`)
	assert.Equal(t, "unknown_tool", title)
}
