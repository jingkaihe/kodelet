package renderers

import (
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
)

func TestRendererRegistryRenderToolUseMarkdown(t *testing.T) {
	registry := NewRendererRegistry()

	t.Run("apply_patch uses tool-owned invocation renderer", func(t *testing.T) {
		rendered := registry.RenderToolUseMarkdown("apply_patch", `{"input":"*** Begin Patch\n*** Add File: hello.txt\n+hello\n*** End Patch"}`)

		assert.Contains(t, rendered, "- **Patch operations:** 1")
		assert.Contains(t, rendered, "- Add `hello.txt`")
		assert.Contains(t, rendered, "Original patch")
		assert.Contains(t, rendered, "```diff")
	})

	t.Run("file_edit uses tool-owned invocation renderer", func(t *testing.T) {
		rendered := registry.RenderToolUseMarkdown("file_edit", `{"file_path":"/tmp/test.go","old_text":"old()","new_text":"new()","replace_all":false}`)

		assert.Contains(t, rendered, "- **Path:** `/tmp/test.go`")
		assert.Contains(t, rendered, "- **Mode:** targeted edit")
		assert.Contains(t, rendered, "**Old text**")
		assert.Contains(t, rendered, "```text\nold()\n```")
		assert.Contains(t, rendered, "**New text**")
		assert.Contains(t, rendered, "```text\nnew()\n```")
	})

	t.Run("read_conversation uses tool-owned invocation renderer", func(t *testing.T) {
		rendered := registry.RenderToolUseMarkdown("read_conversation", `{"conversation_id":"conv-123","goal":"Extract the auth changes"}`)

		assert.Contains(t, rendered, "- **Conversation ID:** `conv-123`")
		assert.Contains(t, rendered, "- **Goal:** Extract the auth changes")
	})

	t.Run("unknown tools fall back to pretty json", func(t *testing.T) {
		rendered := registry.RenderToolUseMarkdown("unknown_tool", `{"alpha":1,"beta":"two"}`)

		assert.Contains(t, rendered, "```json")
		assert.Contains(t, rendered, "\"alpha\": 1")
		assert.Contains(t, rendered, "\"beta\": \"two\"")
	})

	t.Run("file_read uses tool-owned invocation renderer", func(t *testing.T) {
		rendered := registry.RenderToolUseMarkdown("file_read", `{"file_path":"/tmp/test.go","offset":3,"line_limit":10}`)

		assert.Contains(t, rendered, "- **Path:** `/tmp/test.go`")
		assert.Contains(t, rendered, "- **Offset:** 3")
		assert.Contains(t, rendered, "- **Line limit:** 10")
	})

	t.Run("file_write uses tool-owned invocation renderer", func(t *testing.T) {
		rendered := registry.RenderToolUseMarkdown("file_write", `{"file_path":"/tmp/test.go","text":"package main\n"}`)

		assert.Contains(t, rendered, "- **Path:** `/tmp/test.go`")
		assert.Contains(t, rendered, "Requested content")
		assert.Contains(t, rendered, "```text\npackage main\n```")
	})

	t.Run("bash uses tool-owned invocation renderer", func(t *testing.T) {
		rendered := registry.RenderToolUseMarkdown("bash", `{"command":"go test ./...","description":"Run tests","timeout":30}`)

		assert.Contains(t, rendered, "- **Description:** Run tests")
		assert.Contains(t, rendered, "- **Timeout:** 30 seconds")
		assert.Contains(t, rendered, "**Command**")
		assert.Contains(t, rendered, "```bash\ngo test ./...\n```")
	})

	t.Run("registered renderer invalid input falls back to raw json", func(t *testing.T) {
		rendered := registry.RenderToolUseMarkdown("file_read", `{"file_path":`)

		assert.Contains(t, rendered, "```json")
		assert.Contains(t, rendered, `{"file_path":`)
	})
}

func TestRendererRegistryRenderMergedMarkdown(t *testing.T) {
	registry := NewRendererRegistry()

	t.Run("bash merged renderer omits duplicated command block", func(t *testing.T) {
		rendered := registry.RenderMergedMarkdown(tools.StructuredToolResult{
			ToolName:  "bash",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.BashMetadata{
				Command:       "pwd",
				ExitCode:      0,
				WorkingDir:    "/tmp/project",
				ExecutionTime: 25 * time.Millisecond,
				Output:        "/tmp/project",
			},
		})

		assert.Contains(t, rendered, "- **Exit code:** 0")
		assert.Contains(t, rendered, "**Output**")
		assert.NotContains(t, rendered, "**Command**")
		assert.NotContains(t, rendered, "```bash\npwd\n```")
	})

	t.Run("file_read merged renderer omits duplicated path metadata", func(t *testing.T) {
		rendered := registry.RenderMergedMarkdown(tools.StructuredToolResult{
			ToolName:  "file_read",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.FileReadMetadata{
				FilePath: "/tmp/test.go",
				Offset:   10,
				Lines:    []string{"first", "second"},
				Language: "go",
			},
		})

		assert.Contains(t, rendered, "- **Lines:** 2")
		assert.Contains(t, rendered, "- **Language:** `go`")
		assert.Contains(t, rendered, "10: first")
		assert.NotContains(t, rendered, "- **Path:**")
		assert.NotContains(t, rendered, "- **Offset:**")
	})

	t.Run("read_conversation merged renderer keeps extracted content only", func(t *testing.T) {
		rendered := registry.RenderMergedMarkdown(tools.StructuredToolResult{
			ToolName:  "read_conversation",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.ReadConversationMetadata{
				ConversationID: "conv-123",
				Goal:           "Extract auth changes",
				Content:        "Relevant excerpt",
			},
		})

		assert.Contains(t, rendered, "Relevant excerpt")
		assert.NotContains(t, rendered, "Conversation ID")
		assert.NotContains(t, rendered, "Goal")
	})

	t.Run("unknown tools use merged fallback", func(t *testing.T) {
		rendered := registry.RenderMergedMarkdown(tools.StructuredToolResult{
			ToolName:  "future_tool",
			Success:   true,
			Timestamp: time.Date(2026, 5, 23, 1, 2, 3, 0, time.UTC),
		})

		assert.Contains(t, rendered, "Tool Result (future_tool):")
		assert.Contains(t, rendered, "Success: true")
		assert.NotContains(t, rendered, "- **Tool:**")
	})
}

func TestRendererRegistryRenderMarkdownFallbacks(t *testing.T) {
	registry := NewRendererRegistry()

	t.Run("unknown tool markdown fallback", func(t *testing.T) {
		rendered := registry.RenderMarkdown(tools.StructuredToolResult{
			ToolName:  "future_tool",
			Success:   true,
			Timestamp: time.Date(2026, 5, 23, 1, 2, 3, 0, time.UTC),
		})

		assert.Contains(t, rendered, "- **Status:** success")
		assert.Contains(t, rendered, "Tool Result (future_tool):")
	})

	t.Run("non-markdown renderer falls back to cli", func(t *testing.T) {
		registry.Register("plain_tool", &TestRenderer{message: "plain output"})

		rendered := registry.RenderMarkdown(tools.StructuredToolResult{ToolName: "plain_tool", Success: true})
		assert.Contains(t, rendered, "- **Status:** success")
		assert.Contains(t, rendered, "plain output")

		merged := registry.RenderMergedMarkdown(tools.StructuredToolResult{ToolName: "plain_tool", Success: true})
		assert.Equal(t, "- **Status:** success\n```text\nplain output\n```", merged)
	})
}
