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
		{"bash", acptypes.ToolKindOther},           // Currently mapped to other
		{"code_execution", acptypes.ToolKindOther}, // Currently mapped to other
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
	assert.Equal(t, "`ls -la`", title)
}

func TestDefaultTitleGenerator_BashWithBackticks(t *testing.T) {
	gen := &DefaultTitleGenerator{}
	title := gen.GenerateTitle("bash", `{"command": "echo \u0060hello\u0060"}`)
	// Backticks in command should be escaped
	assert.Equal(t, "`echo \\`hello\\``", title)
}

func TestDefaultTitleGenerator_BashLongCommand(t *testing.T) {
	gen := &DefaultTitleGenerator{}
	longCmd := strings.Repeat("a", 100)
	title := gen.GenerateTitle("bash", `{"command": "`+longCmd+`"}`)
	assert.True(t, strings.HasPrefix(title, "`"))
	assert.Contains(t, title, "...")
	assert.LessOrEqual(t, len(title), 80)
}

func TestDefaultTitleGenerator_CodeExecution(t *testing.T) {
	gen := &DefaultTitleGenerator{}
	title := gen.GenerateTitle("code_execution", `{"code_path": "scripts/analyze.ts"}`)
	assert.Equal(t, "Execute: analyze.ts", title)
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

func TestACPMessageHandler_HandleToolUse_FollowTheAgent(t *testing.T) {
	tests := []struct {
		name         string
		toolName     string
		input        string
		expectedPath string
		expectedLine int
	}{
		{
			name:         "file_read with path and offset",
			toolName:     "file_read",
			input:        `{"file_path": "/home/user/main.go", "offset": 42}`,
			expectedPath: "/home/user/main.go",
			expectedLine: 42,
		},
		{
			name:         "file_read with path only",
			toolName:     "file_read",
			input:        `{"file_path": "/home/user/main.go"}`,
			expectedPath: "/home/user/main.go",
			expectedLine: 0,
		},
		{
			name:         "file_write with path",
			toolName:     "file_write",
			input:        `{"file_path": "/home/user/new.txt", "text": "content"}`,
			expectedPath: "/home/user/new.txt",
			expectedLine: 0,
		},
		{
			name:         "file_edit with path",
			toolName:     "file_edit",
			input:        `{"file_path": "/home/user/edit.go", "old_text": "old", "new_text": "new"}`,
			expectedPath: "/home/user/edit.go",
			expectedLine: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender := &mockSender{}
			handler := NewACPMessageHandler(sender, "test-session")

			handler.HandleToolUse("call_1", tt.toolName, tt.input)

			// First update is tool_call (pending)
			toolCall := sender.updates[0].(map[string]any)
			assert.Equal(t, acptypes.UpdateToolCall, toolCall["sessionUpdate"])
			locations := toolCall["locations"].([]ToolCallLocation)
			assert.Len(t, locations, 1)
			assert.Equal(t, tt.expectedPath, locations[0].Path)
			if tt.expectedLine > 0 {
				assert.Equal(t, tt.expectedLine, locations[0].Line)
			}

			// Second update is tool_call_update (in_progress)
			toolCallUpdate := sender.updates[1].(map[string]any)
			assert.Equal(t, acptypes.UpdateToolCallUpdate, toolCallUpdate["sessionUpdate"])
			locationsUpdate := toolCallUpdate["locations"].([]ToolCallLocation)
			assert.Len(t, locationsUpdate, 1)
			assert.Equal(t, tt.expectedPath, locationsUpdate[0].Path)
			if tt.expectedLine > 0 {
				assert.Equal(t, tt.expectedLine, locationsUpdate[0].Line)
			}
		})
	}
}

func TestExtractLocationsFromInput(t *testing.T) {
	handler := NewACPMessageHandler(&mockSender{}, "test-session")

	tests := []struct {
		name         string
		toolName     string
		input        string
		expectedPath string
		expectedLine int
		expectNil    bool
	}{
		{
			name:         "file_read with offset",
			toolName:     "file_read",
			input:        `{"file_path": "/path/to/file.go", "offset": 100}`,
			expectedPath: "/path/to/file.go",
			expectedLine: 100,
		},
		{
			name:         "file_read without offset",
			toolName:     "file_read",
			input:        `{"file_path": "/path/to/file.go"}`,
			expectedPath: "/path/to/file.go",
			expectedLine: 0,
		},
		{
			name:         "file_write",
			toolName:     "file_write",
			input:        `{"file_path": "/path/to/new.txt", "text": "hello"}`,
			expectedPath: "/path/to/new.txt",
			expectedLine: 0,
		},
		{
			name:         "file_edit",
			toolName:     "file_edit",
			input:        `{"file_path": "/path/to/edit.go", "old_text": "a", "new_text": "b"}`,
			expectedPath: "/path/to/edit.go",
			expectedLine: 0,
		},
		{
			name:      "unknown tool",
			toolName:  "bash",
			input:     `{"command": "ls"}`,
			expectNil: true,
		},
		{
			name:      "empty input",
			toolName:  "file_read",
			input:     "",
			expectNil: true,
		},
		{
			name:      "invalid JSON",
			toolName:  "file_read",
			input:     "not json",
			expectNil: true,
		},
		{
			name:      "missing file_path",
			toolName:  "file_read",
			input:     `{"offset": 10}`,
			expectNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			locations := handler.extractLocationsFromInput(tt.toolName, tt.input)

			if tt.expectNil {
				assert.Nil(t, locations)
				return
			}

			assert.Len(t, locations, 1)
			assert.Equal(t, tt.expectedPath, locations[0].Path)
			if tt.expectedLine > 0 {
				assert.Equal(t, tt.expectedLine, locations[0].Line)
			} else {
				assert.Equal(t, 0, locations[0].Line)
			}
		})
	}
}

func TestACPMessageHandler_MaybeSendPlanUpdate(t *testing.T) {
	t.Run("sends plan update for todo_write", func(t *testing.T) {
		sender := &mockSender{}
		handler := NewACPMessageHandler(sender, "test-session")

		result := &mockTodoToolResult{
			toolName: "todo_write",
			todoItems: []tooltypes.TodoItem{
				{ID: "1", Content: "First task", Status: "pending", Priority: "high"},
				{ID: "2", Content: "Second task", Status: "in_progress", Priority: "medium"},
				{ID: "3", Content: "Completed task", Status: "completed", Priority: "low"},
			},
		}

		handler.maybeSendPlanUpdate(result)

		assert.Len(t, sender.updates, 1)
		update := sender.updates[0].(acptypes.PlanUpdate)
		assert.Equal(t, acptypes.UpdatePlan, update.SessionUpdate)
		assert.Len(t, update.Entries, 3)

		assert.Equal(t, "First task", update.Entries[0].Content)
		assert.Equal(t, acptypes.PlanPriorityHigh, update.Entries[0].Priority)
		assert.Equal(t, acptypes.PlanStatusPending, update.Entries[0].Status)

		assert.Equal(t, "Second task", update.Entries[1].Content)
		assert.Equal(t, acptypes.PlanPriorityMedium, update.Entries[1].Priority)
		assert.Equal(t, acptypes.PlanStatusInProgress, update.Entries[1].Status)

		assert.Equal(t, "Completed task", update.Entries[2].Content)
		assert.Equal(t, acptypes.PlanPriorityLow, update.Entries[2].Priority)
		assert.Equal(t, acptypes.PlanStatusCompleted, update.Entries[2].Status)
	})

	t.Run("sends plan update for todo_read", func(t *testing.T) {
		sender := &mockSender{}
		handler := NewACPMessageHandler(sender, "test-session")

		result := &mockTodoToolResult{
			toolName: "todo_read",
			todoItems: []tooltypes.TodoItem{
				{ID: "1", Content: "Read task", Status: "pending", Priority: "medium"},
			},
		}

		handler.maybeSendPlanUpdate(result)

		assert.Len(t, sender.updates, 1)
		update := sender.updates[0].(acptypes.PlanUpdate)
		assert.Equal(t, acptypes.UpdatePlan, update.SessionUpdate)
		assert.Len(t, update.Entries, 1)
		assert.Equal(t, "Read task", update.Entries[0].Content)
	})

	t.Run("does not send plan update for other tools", func(t *testing.T) {
		sender := &mockSender{}
		handler := NewACPMessageHandler(sender, "test-session")

		result := &mockNonTodoToolResult{
			toolName: "file_read",
		}

		handler.maybeSendPlanUpdate(result)

		assert.Len(t, sender.updates, 0)
	})

	t.Run("handles empty todo list", func(t *testing.T) {
		sender := &mockSender{}
		handler := NewACPMessageHandler(sender, "test-session")

		result := &mockTodoToolResult{
			toolName:  "todo_write",
			todoItems: []tooltypes.TodoItem{},
		}

		handler.maybeSendPlanUpdate(result)

		assert.Len(t, sender.updates, 1)
		update := sender.updates[0].(acptypes.PlanUpdate)
		assert.Len(t, update.Entries, 0)
	})
}

// mockTodoToolResult is a mock tool result for todo tools
type mockTodoToolResult struct {
	toolName  string
	todoItems []tooltypes.TodoItem
}

func (m *mockTodoToolResult) AssistantFacing() string { return "" }
func (m *mockTodoToolResult) IsError() bool           { return false }
func (m *mockTodoToolResult) GetError() string        { return "" }
func (m *mockTodoToolResult) GetResult() string       { return "" }
func (m *mockTodoToolResult) StructuredData() tooltypes.StructuredToolResult {
	return tooltypes.StructuredToolResult{
		ToolName: m.toolName,
		Success:  true,
		Metadata: &tooltypes.TodoMetadata{
			Action:   "write",
			TodoList: m.todoItems,
		},
	}
}

// mockNonTodoToolResult is a mock tool result for non-todo tools
type mockNonTodoToolResult struct {
	toolName string
}

func (m *mockNonTodoToolResult) AssistantFacing() string { return "" }
func (m *mockNonTodoToolResult) IsError() bool           { return false }
func (m *mockNonTodoToolResult) GetError() string        { return "" }
func (m *mockNonTodoToolResult) GetResult() string       { return "" }
func (m *mockNonTodoToolResult) StructuredData() tooltypes.StructuredToolResult {
	return tooltypes.StructuredToolResult{
		ToolName: m.toolName,
		Success:  true,
	}
}
