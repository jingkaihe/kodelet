package renderers

import (
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
)

func TestReadConversationRenderer(t *testing.T) {
	renderer := &ReadConversationRenderer{}

	t.Run("cli with content", func(t *testing.T) {
		output := renderer.RenderCLI(tools.StructuredToolResult{
			ToolName:  "read_conversation",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.ReadConversationMetadata{
				ConversationID: "conv-123",
				Goal:           "Extract decisions",
				Content:        "Decision: ship it",
			},
		})

		assert.Equal(t, "Read conversation: conv-123\nGoal: Extract decisions\n\nDecision: ship it", output)
	})

	t.Run("cli without content", func(t *testing.T) {
		output := renderer.RenderCLI(tools.StructuredToolResult{
			ToolName: "read_conversation",
			Success:  true,
			Metadata: &tools.ReadConversationMetadata{ConversationID: "conv-123", Goal: "Extract decisions"},
		})

		assert.Equal(t, "Read conversation: conv-123\nGoal: Extract decisions", output)
	})

	t.Run("cli error and invalid metadata", func(t *testing.T) {
		assert.Equal(t, "Error: missing conversation", renderer.RenderCLI(tools.StructuredToolResult{
			ToolName: "read_conversation",
			Success:  false,
			Error:    "missing conversation",
		}))

		assert.Equal(t, "Error: Invalid metadata type for read_conversation", renderer.RenderCLI(tools.StructuredToolResult{
			ToolName: "read_conversation",
			Success:  true,
			Metadata: &tools.FileReadMetadata{},
		}))
	})

	t.Run("markdown variants", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName: "read_conversation",
			Success:  true,
			Metadata: &tools.ReadConversationMetadata{
				ConversationID: "conv-123",
				Goal:           "Extract *decisions*",
				Content:        "Decision: ship it",
			},
		}

		markdown := renderer.RenderMarkdown(result)
		assert.Contains(t, markdown, "- **Conversation ID:** `conv-123`")
		assert.Contains(t, markdown, "- **Goal:** Extract *decisions*")
		assert.Contains(t, markdown, "Decision: ship it")

		merged := renderer.RenderMergedMarkdown(result)
		assert.Equal(t, "Decision: ship it", merged)

		noContent := renderer.RenderMergedMarkdown(tools.StructuredToolResult{
			ToolName: "read_conversation",
			Success:  true,
			Metadata: &tools.ReadConversationMetadata{ConversationID: "conv-123", Goal: "Extract decisions"},
		})
		assert.Empty(t, noContent)
	})

	t.Run("tool use markdown", func(t *testing.T) {
		rendered := renderer.RenderToolUseMarkdown(`{"conversation_id":"conv-123","goal":"Extract **decisions**"}`)
		assert.Contains(t, rendered, "- **Conversation ID:** `conv-123`")
		assert.Contains(t, rendered, "- **Goal:** Extract **decisions**")
		assert.Empty(t, renderer.RenderToolUseMarkdown(`{"conversation_id":`))
	})
}
