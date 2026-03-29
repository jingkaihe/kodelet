package llm

import (
	"strings"
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

	markdown := renderConversationEntriesMarkdown(messages, toolResults, ConversationMarkdownOptions{})

	assert.Contains(t, markdown, "## Messages")
	assert.Contains(t, markdown, "### User")
	assert.Contains(t, markdown, "Can you show me the working directory?")
	assert.Contains(t, markdown, "### Assistant · Tool")
	assert.Contains(t, markdown, "- **Tool:** `bash`")
	assert.Contains(t, markdown, "**Command**")
	assert.Contains(t, markdown, "- **Exit code:** 0")
	assert.Contains(t, markdown, "/tmp/project")
}

func TestRenderConversationEntriesMarkdownTruncatesToolResults(t *testing.T) {
	longOutput := strings.Repeat("A", 300)
	messages := []conversations.StreamableMessage{
		{
			Kind:       "tool-use",
			Role:       "assistant",
			ToolName:   "bash",
			ToolCallID: "call_1",
			Input:      `{"command":"cat big.txt","description":"Show file contents"}`,
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
				Command:       "cat big.txt",
				ExitCode:      0,
				Output:        longOutput,
				ExecutionTime: time.Second,
			},
		},
	}

	markdown := renderConversationEntriesMarkdown(messages, toolResults, ConversationMarkdownOptions{
		TruncateToolResults: true,
		MaxToolResultChars:  120,
	})

	assert.Contains(t, markdown, strings.TrimPrefix(toolResultTruncationMarker, "\n"))
	assert.Contains(t, markdown, "```text")
	assert.Contains(t, markdown, "\n```")
	assert.NotContains(t, markdown, longOutput)
}

func TestRenderConversationEntriesMarkdownTruncatesAmpToolFieldsOnly(t *testing.T) {
	longOutput := strings.Repeat("A", 80)
	longContent := strings.Repeat("B", 80)
	messages := []conversations.StreamableMessage{
		{
			Kind:       "tool-result",
			Role:       "assistant",
			ToolName:   "custom_tool_demo",
			ToolCallID: "call_1",
			Content:    `{"output":"` + longOutput + `","content":"` + longContent + `"}`,
		},
	}

	markdown := renderConversationEntriesMarkdown(messages, nil, ConversationMarkdownOptions{
		TruncateToolResults: true,
		MaxToolResultChars:  20,
	})

	assert.Contains(t, markdown, strings.TrimPrefix(toolResultTruncationMarker, "\n"))
	assert.Contains(t, markdown, longContent)
	assert.NotContains(t, markdown, longOutput)
}

func TestRenderConversationEntriesMarkdownDropsImageItemsBeforeTruncation(t *testing.T) {
	messages := []conversations.StreamableMessage{
		{
			Kind:       "tool-result",
			Role:       "assistant",
			ToolName:   "mcp_tool_demo",
			ToolCallID: "call_1",
			Content: `[
				{"type":"image","data":"ignored"},
				{"type":"text","text":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}
			]`,
		},
	}

	markdown := renderConversationEntriesMarkdown(messages, nil, ConversationMarkdownOptions{
		TruncateToolResults: true,
		MaxToolResultChars:  12,
	})

	assert.NotContains(t, markdown, `"type": "image"`)
	assert.NotContains(t, markdown, "ignored")
	assert.Contains(t, markdown, strings.TrimPrefix(toolResultTruncationMarker, "\n"))
}

func TestRenderConversationEntriesMarkdownUsesHardCapPlaceholder(t *testing.T) {
	messages := []conversations.StreamableMessage{
		{
			Kind:       "tool-result",
			Role:       "assistant",
			ToolName:   "read_conversation",
			ToolCallID: "call_1",
			Content:    `{"content":"` + strings.Repeat("A", 400) + `"}`,
		},
	}

	markdown := renderConversationEntriesMarkdown(messages, nil, ConversationMarkdownOptions{
		MaxToolResultBytes: 100,
	})

	assert.Contains(t, markdown, "Tool result truncated:")
	assert.Contains(t, markdown, "Please refine the query.")
	assert.NotContains(t, markdown, strings.Repeat("A", 200))
}
