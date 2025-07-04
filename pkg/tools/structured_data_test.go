package tools

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

func TestFileReadToolResult_StructuredData(t *testing.T) {
	result := &FileReadToolResult{
		filename: "test.go",
		lines:    []string{"package main", "func main() {", "}"},
		offset:   1,
	}

	structured := result.StructuredData()

	if structured.ToolName != "file_read" {
		t.Errorf("Expected tool name 'file_read', got %s", structured.ToolName)
	}

	if !structured.Success {
		t.Errorf("Expected success to be true for non-error result")
	}

	meta, ok := structured.Metadata.(*tooltypes.FileReadMetadata)
	if !ok {
		t.Fatalf("Expected FileReadMetadata, got %T", structured.Metadata)
	}

	if meta.FilePath != "test.go" {
		t.Errorf("Expected file path 'test.go', got %s", meta.FilePath)
	}

	if meta.Offset != 1 {
		t.Errorf("Expected offset 1, got %d", meta.Offset)
	}

	if len(meta.Lines) != 3 {
		t.Errorf("Expected 3 lines, got %d", len(meta.Lines))
	}

	if meta.Language != "go" {
		t.Errorf("Expected language 'go', got %s", meta.Language)
	}
}

func TestFileEditToolResult_StructuredData(t *testing.T) {
	result := &FileEditToolResult{
		filename: "main.py",
		oldText:  "print('old')",
		newText:  "print('new')",
	}

	structured := result.StructuredData()

	if structured.ToolName != "file_edit" {
		t.Errorf("Expected tool name 'file_edit', got %s", structured.ToolName)
	}

	meta, ok := structured.Metadata.(*tooltypes.FileEditMetadata)
	if !ok {
		t.Fatalf("Expected FileEditMetadata, got %T", structured.Metadata)
	}

	if meta.FilePath != "main.py" {
		t.Errorf("Expected file path 'main.py', got %s", meta.FilePath)
	}

	if meta.Language != "python" {
		t.Errorf("Expected language 'python', got %s", meta.Language)
	}

	if len(meta.Edits) != 1 {
		t.Errorf("Expected 1 edit, got %d", len(meta.Edits))
	}

	edit := meta.Edits[0]
	if edit.OldContent != "print('old')" {
		t.Errorf("Expected old content 'print('old')', got %s", edit.OldContent)
	}

	if edit.NewContent != "print('new')" {
		t.Errorf("Expected new content 'print('new')', got %s", edit.NewContent)
	}
}

func TestFileWriteToolResult_StructuredData(t *testing.T) {
	result := &FileWriteToolResult{
		filename: "config.json",
		text:     `{"setting": "value"}`,
	}

	structured := result.StructuredData()

	if structured.ToolName != "file_write" {
		t.Errorf("Expected tool name 'file_write', got %s", structured.ToolName)
	}

	meta, ok := structured.Metadata.(*tooltypes.FileWriteMetadata)
	if !ok {
		t.Fatalf("Expected FileWriteMetadata, got %T", structured.Metadata)
	}

	if meta.FilePath != "config.json" {
		t.Errorf("Expected file path 'config.json', got %s", meta.FilePath)
	}

	if meta.Language != "json" {
		t.Errorf("Expected language 'json', got %s", meta.Language)
	}

	if meta.Size != int64(len(`{"setting": "value"}`)) {
		t.Errorf("Expected size %d, got %d", len(`{"setting": "value"}`), meta.Size)
	}

	if meta.Content != `{"setting": "value"}` {
		t.Errorf("Expected content '%s', got %s", `{"setting": "value"}`, meta.Content)
	}
}

func TestGlobToolResult_StructuredData(t *testing.T) {
	result := &GlobToolResult{
		pattern: "*.go",
		path:    "/test",
		files:   []string{"/test/main.go", "/test/util.go"},
	}

	structured := result.StructuredData()

	if structured.ToolName != "glob_tool" {
		t.Errorf("Expected tool name 'glob_tool', got %s", structured.ToolName)
	}

	meta, ok := structured.Metadata.(*tooltypes.GlobMetadata)
	if !ok {
		t.Fatalf("Expected GlobMetadata, got %T", structured.Metadata)
	}

	if meta.Pattern != "*.go" {
		t.Errorf("Expected pattern '*.go', got %s", meta.Pattern)
	}

	if meta.Path != "/test" {
		t.Errorf("Expected path '/test', got %s", meta.Path)
	}

	if len(meta.Files) != 2 {
		t.Errorf("Expected 2 files, got %d", len(meta.Files))
	}
}

func TestBashToolResult_StructuredData(t *testing.T) {
	result := &BashToolResult{
		command:        "ls -la",
		combinedOutput: "total 8\ndrwxr-xr-x 2 user user 4096 Jan 1 10:00 .\n",
	}

	structured := result.StructuredData()

	if structured.ToolName != "bash" {
		t.Errorf("Expected tool name 'bash', got %s", structured.ToolName)
	}

	meta, ok := structured.Metadata.(*tooltypes.BashMetadata)
	if !ok {
		t.Fatalf("Expected BashMetadata, got %T", structured.Metadata)
	}

	if meta.Command != "ls -la" {
		t.Errorf("Expected command 'ls -la', got %s", meta.Command)
	}

	if !strings.Contains(meta.Output, "total 8") {
		t.Errorf("Expected output to contain 'total 8', got %s", meta.Output)
	}
}

func TestErrorToolResult_StructuredData(t *testing.T) {
	result := &FileReadToolResult{
		filename: "nonexistent.txt",
		err:      "file not found",
	}

	structured := result.StructuredData()

	if structured.Success {
		t.Errorf("Expected success to be false for error result")
	}

	if structured.Error != "file not found" {
		t.Errorf("Expected error 'file not found', got %s", structured.Error)
	}

	if structured.ToolName != "file_read" {
		t.Errorf("Expected tool name 'file_read', got %s", structured.ToolName)
	}
}

func TestStructuredToolResult_JSONSerialization(t *testing.T) {
	original := tooltypes.StructuredToolResult{
		ToolName:  "test_tool",
		Success:   true,
		Error:     "",
		Timestamp: time.Now().Round(time.Second), // Round to avoid precision issues
	}

	// Test JSON serialization of basic fields (metadata is complex due to interface{})
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Failed to marshal StructuredToolResult: %v", err)
	}

	// Test that we can at least serialize and get JSON
	if len(data) == 0 {
		t.Errorf("Expected non-empty JSON data")
	}

	// Verify the JSON contains expected fields
	jsonStr := string(data)
	if !strings.Contains(jsonStr, "test_tool") {
		t.Errorf("Expected JSON to contain tool name, got: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, "true") {
		t.Errorf("Expected JSON to contain success field, got: %s", jsonStr)
	}
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

	if structured.ToolName != "todo_read" {
		t.Errorf("Expected tool name 'todo_read', got %s", structured.ToolName)
	}

	meta, ok := structured.Metadata.(*tooltypes.TodoMetadata)
	if !ok {
		t.Fatalf("Expected TodoMetadata, got %T", structured.Metadata)
	}

	if meta.Action != "read" {
		t.Errorf("Expected action 'read', got %s", meta.Action)
	}

	if len(meta.TodoList) != 2 {
		t.Errorf("Expected 2 todo items, got %d", len(meta.TodoList))
	}

	if meta.Statistics.Total != 2 {
		t.Errorf("Expected total 2, got %d", meta.Statistics.Total)
	}

	if meta.Statistics.Pending != 1 {
		t.Errorf("Expected pending 1, got %d", meta.Statistics.Pending)
	}

	if meta.Statistics.Completed != 1 {
		t.Errorf("Expected completed 1, got %d", meta.Statistics.Completed)
	}
}

func TestBatchToolResult_StructuredData(t *testing.T) {
	// Create a sub-result for testing
	subResult := &FileReadToolResult{
		filename: "test.txt",
		lines:    []string{"content"},
	}

	result := &BatchToolResult{
		description: "Test batch",
		toolResults: []tooltypes.ToolResult{subResult},
	}

	structured := result.StructuredData()

	if structured.ToolName != "batch" {
		t.Errorf("Expected tool name 'batch', got %s", structured.ToolName)
	}

	meta, ok := structured.Metadata.(*tooltypes.BatchMetadata)
	if !ok {
		t.Fatalf("Expected BatchMetadata, got %T", structured.Metadata)
	}

	if meta.Description != "Test batch" {
		t.Errorf("Expected description 'Test batch', got %s", meta.Description)
	}

	if meta.SuccessCount != 1 {
		t.Errorf("Expected success count 1, got %d", meta.SuccessCount)
	}

	if meta.FailureCount != 0 {
		t.Errorf("Expected failure count 0, got %d", meta.FailureCount)
	}

	if len(meta.SubResults) != 1 {
		t.Errorf("Expected 1 sub-result, got %d", len(meta.SubResults))
	}

	if meta.SubResults[0].ToolName != "file_read" {
		t.Errorf("Expected sub-result tool name 'file_read', got %s", meta.SubResults[0].ToolName)
	}
}
