package llm

import (
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
)

func TestRenderConversationEntriesMarkdown(t *testing.T) {
	messages := []conversations.StreamableMessage{
		{
			Kind:    "text",
			Role:    "user",
			Content: "Can you show me the working directory?",
		},
		{
			Kind:       "tool-use",
			Role:       "assistant",
			ToolName:   "bash",
			ToolCallID: "call_1",
			Input:      `{"command":"pwd","description":"Print the working directory"}`,
		},
		{
			Kind:       "tool-result",
			Role:       "assistant",
			ToolCallID: "call_1",
		},
	}

	toolResults := map[string]tooltypes.StructuredToolResult{
		"call_1": {
			ToolName:  "bash",
			Success:   true,
			Timestamp: time.Unix(0, 0),
			Metadata: &tooltypes.BashMetadata{
				Command:       "pwd",
				ExitCode:      0,
				Output:        "/tmp/project",
				ExecutionTime: time.Second,
				WorkingDir:    "/tmp/project",
			},
		},
	}

	markdown := renderConversationEntriesMarkdown(messages, toolResults)

	assert.Contains(t, markdown, "## Messages")
	assert.Contains(t, markdown, "### User")
	assert.Contains(t, markdown, "Can you show me the working directory?")
	assert.Contains(t, markdown, "### Assistant · Tool")
	assert.Contains(t, markdown, "- **Tool:** `bash`")
	assert.Contains(t, markdown, "**Command**")
	assert.Contains(t, markdown, "- **Exit code:** 0")
	assert.Contains(t, markdown, "/tmp/project")
}
