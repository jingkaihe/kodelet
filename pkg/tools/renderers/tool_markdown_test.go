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
}
