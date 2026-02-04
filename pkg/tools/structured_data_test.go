package tools

import (
	"encoding/json"
	"testing"
	"time"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileReadToolResult_StructuredData(t *testing.T) {
	result := &FileReadToolResult{
		filename: "test.go",
		lines:    []string{"package main", "func main() {", "}"},
		offset:   1,
	}

	structured := result.StructuredData()

	assert.Equal(t, "file_read", structured.ToolName)
	assert.True(t, structured.Success)

	meta, ok := structured.Metadata.(*tooltypes.FileReadMetadata)
	require.True(t, ok, "Expected FileReadMetadata, got %T", structured.Metadata)

	assert.Equal(t, "test.go", meta.FilePath)
	assert.Equal(t, 1, meta.Offset)
	assert.Len(t, meta.Lines, 3)
	assert.Equal(t, "go", meta.Language)
}

func TestFileEditToolResult_StructuredData(t *testing.T) {
	result := &FileEditToolResult{
		filename: "main.py",
		oldText:  "print('old')",
		newText:  "print('new')",
	}

	structured := result.StructuredData()

	assert.Equal(t, "file_edit", structured.ToolName)

	meta, ok := structured.Metadata.(*tooltypes.FileEditMetadata)
	require.True(t, ok, "Expected FileEditMetadata, got %T", structured.Metadata)

	assert.Equal(t, "main.py", meta.FilePath)
	assert.Equal(t, "python", meta.Language)
	assert.Len(t, meta.Edits, 1)

	edit := meta.Edits[0]
	assert.Equal(t, "print('old')", edit.OldContent)
	assert.Equal(t, "print('new')", edit.NewContent)
}

func TestFileWriteToolResult_StructuredData(t *testing.T) {
	result := &FileWriteToolResult{
		filename: "config.json",
		text:     `{"setting": "value"}`,
	}

	structured := result.StructuredData()

	assert.Equal(t, "file_write", structured.ToolName)

	meta, ok := structured.Metadata.(*tooltypes.FileWriteMetadata)
	require.True(t, ok, "Expected FileWriteMetadata, got %T", structured.Metadata)

	assert.Equal(t, "config.json", meta.FilePath)
	assert.Equal(t, "json", meta.Language)
	assert.Equal(t, int64(len(`{"setting": "value"}`)), meta.Size)
	assert.Equal(t, `{"setting": "value"}`, meta.Content)
}

func TestGlobToolResult_StructuredData(t *testing.T) {
	result := &GlobToolResult{
		pattern: "*.go",
		path:    "/test",
		files:   []string{"/test/main.go", "/test/util.go"},
	}

	structured := result.StructuredData()

	assert.Equal(t, "glob_tool", structured.ToolName)

	meta, ok := structured.Metadata.(*tooltypes.GlobMetadata)
	require.True(t, ok, "Expected GlobMetadata, got %T", structured.Metadata)

	assert.Equal(t, "*.go", meta.Pattern)
	assert.Equal(t, "/test", meta.Path)
	assert.Len(t, meta.Files, 2)
}

func TestBashToolResult_StructuredData(t *testing.T) {
	result := &BashToolResult{
		command:        "ls -la",
		combinedOutput: "total 8\ndrwxr-xr-x 2 user user 4096 Jan 1 10:00 .\n",
	}

	structured := result.StructuredData()

	assert.Equal(t, "bash", structured.ToolName)

	meta, ok := structured.Metadata.(*tooltypes.BashMetadata)
	require.True(t, ok, "Expected BashMetadata, got %T", structured.Metadata)

	assert.Equal(t, "ls -la", meta.Command)
	assert.Contains(t, meta.Output, "total 8")
}

func TestErrorToolResult_StructuredData(t *testing.T) {
	result := &FileReadToolResult{
		filename: "nonexistent.txt",
		err:      "file not found",
	}

	structured := result.StructuredData()

	assert.False(t, structured.Success)
	assert.Equal(t, "file not found", structured.Error)
	assert.Equal(t, "file_read", structured.ToolName)
}

func TestStructuredToolResult_JSONSerialization(t *testing.T) {
	original := tooltypes.StructuredToolResult{
		ToolName:  "test_tool",
		Success:   true,
		Error:     "",
		Timestamp: time.Now().Round(time.Second), // Round to avoid precision issues
	}

	// Test JSON serialization of basic fields (metadata is complex due to any)
	data, err := json.Marshal(original)
	require.NoError(t, err, "Failed to marshal StructuredToolResult")

	// Test that we can at least serialize and get JSON
	assert.NotEmpty(t, data)

	// Verify the JSON contains expected fields
	jsonStr := string(data)
	assert.Contains(t, jsonStr, "test_tool")
	assert.Contains(t, jsonStr, "true")
}

func TestTodoToolResult_StructuredData(t *testing.T) {
	result := &TodoToolResult{
		filePath: "/test/todos.json",
		todos: []Todo{
			{Content: "Test task", Status: "pending", Priority: "high"},
			{Content: "Done task", Status: "completed", Priority: "low"},
		},
		isWrite: false,
	}

	structured := result.StructuredData()

	assert.Equal(t, "todo_read", structured.ToolName)

	meta, ok := structured.Metadata.(*tooltypes.TodoMetadata)
	require.True(t, ok, "Expected TodoMetadata, got %T", structured.Metadata)

	assert.Equal(t, "read", meta.Action)
	assert.Len(t, meta.TodoList, 2)
	assert.Equal(t, 2, meta.Statistics.Total)
	assert.Equal(t, 1, meta.Statistics.Pending)
	assert.Equal(t, 1, meta.Statistics.Completed)
}
