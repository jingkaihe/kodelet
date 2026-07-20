package bridge

import (
	"strings"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockSender struct {
	updates          []any
	transientUpdates []any
}

func (m *mockSender) SendUpdate(_ acptypes.SessionID, update any) error {
	m.updates = append(m.updates, update)
	return nil
}

func (m *mockSender) SendTransientUpdate(_ acptypes.SessionID, update any) error {
	m.transientUpdates = append(m.transientUpdates, update)
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
	assert.Equal(t, "file_read", toolCall["toolName"])
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

func TestACPMessageHandler_HandleToolUpdate(t *testing.T) {
	sender := &mockSender{}
	handler := NewACPMessageHandler(sender, "test-session")
	result := &mockToolResult{
		result: "partial output",
		structuredData: tooltypes.StructuredToolResult{
			ToolName: "bash",
			Success:  true,
			Metadata: &tooltypes.BashMetadata{Command: "echo hi", Output: "partial output"},
		},
	}

	handler.HandleToolUpdate("call_1", "bash", result)

	require.Len(t, sender.transientUpdates, 1)
	assert.Empty(t, sender.updates)
	update := sender.transientUpdates[0].(map[string]any)
	assert.Equal(t, acptypes.UpdateToolCallUpdate, update["sessionUpdate"])
	assert.Equal(t, acptypes.ToolStatusInProgress, update["status"])
	assert.Equal(t, "call_1", update["toolCallId"])
	assert.NotEmpty(t, update["content"])
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

	content := update["content"].(map[string]any)
	assert.Equal(t, acptypes.ContentTypeText, content["type"])
	assert.Equal(t, "thinking...", content["text"])
}

func TestACPMessageHandler_LifecycleCallbacksAreNoOps(t *testing.T) {
	sender := &mockSender{}
	handler := NewACPMessageHandler(sender, "test-session")

	assert.NotPanics(t, func() {
		handler.HandleThinkingStart()
		handler.HandleThinkingBlockEnd()
		handler.HandleContentBlockEnd()
		handler.HandleDone()
	})
	assert.Empty(t, sender.updates)
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
		{"apply_patch", acptypes.ToolKindEdit},
		{"bash", acptypes.ToolKindOther}, // Currently mapped to other
		{"web_fetch", acptypes.ToolKindFetch},
		{"view_image", acptypes.ToolKindRead},
		{"thinking", acptypes.ToolKindThink},
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

func TestContentBlocksToMessage_EdgeCases(t *testing.T) {
	blocks := []acptypes.ContentBlock{
		{Type: acptypes.ContentTypeText, Text: ""},
		{Type: acptypes.ContentTypeText, Text: "First"},
		{
			Type:     acptypes.ContentTypeImage,
			Data:     "base64data",
			MimeType: "image/jpeg",
			URI:      "file:///ignored.jpg",
		},
		{Type: acptypes.ContentTypeImage, URI: "file:///image.png"},
		{Type: acptypes.ContentTypeResource},
		{Type: acptypes.ContentTypeResource, Resource: &acptypes.EmbeddedResource{URI: "file:///empty.txt"}},
		{Type: acptypes.ContentTypeResource, Resource: &acptypes.EmbeddedResource{URI: "file:///doc.md", Text: "Doc"}},
		{Type: acptypes.ContentTypeResourceLink},
		{Type: acptypes.ContentTypeAudio, Data: "ignored"},
		{Type: "unknown", Text: "ignored"},
	}

	message, images := ContentBlocksToMessage(blocks)

	assert.Equal(t, "First\n\n--- file:///doc.md ---\nDoc", message)
	assert.Equal(t, []string{"data:image/jpeg;base64,base64data", "file:///image.png"}, images)
}

func TestContentBlocksToMessage_ImageDataWithoutMimeType(t *testing.T) {
	blocks := []acptypes.ContentBlock{{Type: acptypes.ContentTypeImage, Data: "abc123"}}

	message, images := ContentBlocksToMessage(blocks)

	assert.Empty(t, message)
	assert.Equal(t, []string{"data:;base64,abc123"}, images)
}

func TestDefaultTitleGenerator_EmptyInput(t *testing.T) {
	gen := &DefaultTitleGenerator{}
	title := gen.GenerateTitle("file_read", "")
	assert.Equal(t, "file_read", title)
}

func TestDefaultTitleGenerator_FileRead(t *testing.T) {
	gen := &DefaultTitleGenerator{}
	title := gen.GenerateTitle("file_read", `{"file_path": "/path/to/test.txt"}`)
	assert.Equal(t, "Read: /path/to/test.txt", title)
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
	assert.True(t, strings.HasSuffix(title, "`"))
	assert.Equal(t, "`"+longCmd+"`", title)
}

func TestDefaultTitleGenerator_Grep(t *testing.T) {
	gen := &DefaultTitleGenerator{}
	title := gen.GenerateTitle("grep_tool", `{"pattern": "func main"}`)
	assert.Equal(t, "Grep: func main", title)
}

func TestDefaultTitleGenerator_ApplyPatch(t *testing.T) {
	gen := &DefaultTitleGenerator{}
	title := gen.GenerateTitle("apply_patch", `{"input":"*** Begin Patch\n*** Update File: /tmp/foo.txt\n@@\n-old\n+new\n*** End Patch"}`)
	assert.Equal(t, "Patch: /tmp/foo.txt", title)
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

func TestDefaultTitleGenerator_AdditionalTools(t *testing.T) {
	gen := &DefaultTitleGenerator{}

	tests := []struct {
		name     string
		toolName string
		input    string
		expected string
	}{
		{name: "file_write", toolName: "file_write", input: `{"file_path":"/tmp/out.txt"}`, expected: "Write: /tmp/out.txt"},
		{name: "file_edit", toolName: "file_edit", input: `{"file_path":"/tmp/edit.txt"}`, expected: "Edit: /tmp/edit.txt"},
		{name: "glob_tool", toolName: "glob_tool", input: `{"pattern":"**/*.go"}`, expected: "Glob: **/*.go"},
		{name: "web_fetch", toolName: "web_fetch", input: `{"url":"https://example.com"}`, expected: "Fetch: https://example.com"},
		{name: "view_image", toolName: "view_image", input: `{"path":"/tmp/img.png"}`, expected: "Image: /tmp/img.png"},
		{name: "apply_patch no file", toolName: "apply_patch", input: `{"input":"*** Begin Patch\n*** End Patch"}`, expected: "Apply patch"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, gen.GenerateTitle(tt.toolName, tt.input))
		})
	}
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
		{
			name:         "apply_patch with patch input",
			toolName:     "apply_patch",
			input:        `{"input":"*** Begin Patch\n*** Update File: /home/user/edit.go\n@@\n-old\n+new\n*** End Patch"}`,
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
			name:         "apply_patch",
			toolName:     "apply_patch",
			input:        `{"input":"*** Begin Patch\n*** Add File: /path/to/new.txt\n+hello\n*** End Patch"}`,
			expectedPath: "/path/to/new.txt",
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

func TestACPMessageHandler_HandleToolResult_FollowTheAgentLocations(t *testing.T) {
	tests := []struct {
		name         string
		result       tooltypes.StructuredToolResult
		expectedPath string
		expectedLine int
	}{
		{
			name: "file_read offset",
			result: tooltypes.StructuredToolResult{
				ToolName: "file_read",
				Success:  true,
				Metadata: &tooltypes.FileReadMetadata{FilePath: "/repo/main.go", Offset: 12, Lines: []string{"package main"}},
			},
			expectedPath: "/repo/main.go",
			expectedLine: 12,
		},
		{
			name: "file_write",
			result: tooltypes.StructuredToolResult{
				ToolName: "file_write",
				Success:  true,
				Metadata: &tooltypes.FileWriteMetadata{FilePath: "/repo/new.go", Content: "package main"},
			},
			expectedPath: "/repo/new.go",
		},
		{
			name: "file_edit start line",
			result: tooltypes.StructuredToolResult{
				ToolName: "file_edit",
				Success:  true,
				Metadata: &tooltypes.FileEditMetadata{
					FilePath: "/repo/edit.go",
					Edits:    []tooltypes.Edit{{StartLine: 7, OldContent: "old", NewContent: "new"}},
				},
			},
			expectedPath: "/repo/edit.go",
			expectedLine: 7,
		},
		{
			name: "apply_patch move path deduplicated",
			result: tooltypes.StructuredToolResult{
				ToolName: "apply_patch",
				Success:  true,
				Metadata: &tooltypes.ApplyPatchMetadata{Changes: []tooltypes.ApplyPatchChange{
					{Path: "", Operation: tooltypes.ApplyPatchOperationAdd},
					{Path: "/repo/old.go", MovePath: "/repo/new.go", Operation: tooltypes.ApplyPatchOperationUpdate},
					{Path: "/repo/new.go", Operation: tooltypes.ApplyPatchOperationUpdate},
				}},
			},
			expectedPath: "/repo/new.go",
		},
		{
			name: "bash working directory",
			result: tooltypes.StructuredToolResult{
				ToolName: "bash",
				Success:  true,
				Metadata: &tooltypes.BashMetadata{WorkingDir: "/repo", Output: "ok"},
			},
			expectedPath: "/repo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender := &mockSender{}
			handler := NewACPMessageHandler(sender, "test-session")
			result := &mockToolResult{structuredData: tt.result}

			handler.HandleToolResult("call_1", tt.result.ToolName, result)

			require.Len(t, sender.updates, 1)
			update := sender.updates[0].(map[string]any)
			locations := update["locations"].([]ToolCallLocation)
			require.Len(t, locations, 1)
			assert.Equal(t, tt.expectedPath, locations[0].Path)
			assert.Equal(t, tt.expectedLine, locations[0].Line)
		})
	}
}

func TestExtractLocations_EdgeCases(t *testing.T) {
	handler := NewACPMessageHandler(&mockSender{}, "test-session")

	tests := []struct {
		name   string
		result tooltypes.StructuredToolResult
	}{
		{name: "unknown tool", result: tooltypes.StructuredToolResult{ToolName: "unknown_tool", Success: true}},
		{name: "file_read no metadata", result: tooltypes.StructuredToolResult{ToolName: "file_read", Success: true}},
		{name: "file_edit no edits still returns path", result: tooltypes.StructuredToolResult{ToolName: "file_edit", Success: true, Metadata: &tooltypes.FileEditMetadata{FilePath: "/repo/file.go"}}},
		{name: "apply_patch no paths", result: tooltypes.StructuredToolResult{ToolName: "apply_patch", Success: true, Metadata: &tooltypes.ApplyPatchMetadata{Changes: []tooltypes.ApplyPatchChange{{Operation: tooltypes.ApplyPatchOperationUpdate}}}}},
		{name: "bash no working directory", result: tooltypes.StructuredToolResult{ToolName: "bash", Success: true, Metadata: &tooltypes.BashMetadata{Output: "ok"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			locations := handler.extractLocations(&mockToolResult{structuredData: tt.result})
			if tt.name == "file_edit no edits still returns path" {
				require.Len(t, locations, 1)
				assert.Equal(t, "/repo/file.go", locations[0].Path)
				assert.Zero(t, locations[0].Line)
				return
			}
			assert.Nil(t, locations)
		})
	}
}

func TestExtractFirstApplyPatchPath(t *testing.T) {
	tests := []struct {
		name     string
		params   map[string]any
		expected string
	}{
		{name: "add", params: map[string]any{"input": "*** Begin Patch\n*** Add File: new.txt\n+hello\n*** End Patch"}, expected: "new.txt"},
		{name: "delete", params: map[string]any{"input": "*** Begin Patch\n*** Delete File: old.txt\n*** End Patch"}, expected: "old.txt"},
		{name: "update", params: map[string]any{"input": "*** Begin Patch\n*** Update File: edit.txt\n@@\n-old\n+new\n*** End Patch"}, expected: "edit.txt"},
		{name: "missing input", params: map[string]any{}, expected: ""},
		{name: "non-string input", params: map[string]any{"input": 42}, expected: ""},
		{name: "no file operation", params: map[string]any{"input": "*** Begin Patch\n*** End Patch"}, expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, extractFirstApplyPatchPath(tt.params))
		})
	}
}

func TestExtractLocations_ApplyPatchPrefersMovePath(t *testing.T) {
	handler := NewACPMessageHandler(&mockSender{}, "test-session")

	result := &mockApplyPatchToolResult{
		changes: []tooltypes.ApplyPatchChange{
			{
				Path:      "/repo/old.go",
				MovePath:  "/repo/new.go",
				Operation: tooltypes.ApplyPatchOperationUpdate,
			},
			{
				Path:      "/repo/old.go",
				MovePath:  "/repo/new.go",
				Operation: tooltypes.ApplyPatchOperationUpdate,
			},
		},
	}

	locations := handler.extractLocations(result)
	assert.Len(t, locations, 1)
	assert.Equal(t, "/repo/new.go", locations[0].Path)
}

type mockApplyPatchToolResult struct {
	changes []tooltypes.ApplyPatchChange
}

func (m *mockApplyPatchToolResult) AssistantFacing() string { return "" }
func (m *mockApplyPatchToolResult) IsError() bool           { return false }
func (m *mockApplyPatchToolResult) GetError() string        { return "" }
func (m *mockApplyPatchToolResult) GetResult() string       { return "" }
func (m *mockApplyPatchToolResult) StructuredData() tooltypes.StructuredToolResult {
	return tooltypes.StructuredToolResult{
		ToolName: "apply_patch",
		Success:  true,
		Metadata: &tooltypes.ApplyPatchMetadata{
			Changes: m.changes,
		},
	}
}
