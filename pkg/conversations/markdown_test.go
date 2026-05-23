package conversations

import (
	"strings"
	"testing"
	"time"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderMarkdownIncludesThinkingByDefault(t *testing.T) {
	messages := []StreamableMessage{
		{Kind: "text", Role: "user", Content: "Summarize this"},
		{Kind: "thinking", Role: "assistant", Content: "Internal reasoning"},
		{Kind: "text", Role: "assistant", Content: "Here is the summary."},
	}

	markdown := RenderMarkdown(messages, nil, MarkdownOptions{})

	assert.Contains(t, markdown, "### Assistant · Thinking")
	assert.Contains(t, markdown, "Internal reasoning")
	assert.Contains(t, markdown, "Here is the summary.")
}

func TestRenderMarkdownCanExcludeThinking(t *testing.T) {
	messages := []StreamableMessage{
		{Kind: "text", Role: "user", Content: "Summarize this"},
		{Kind: "thinking", Role: "assistant", Content: "Internal reasoning"},
		{Kind: "text", Role: "assistant", Content: "Here is the summary."},
	}

	markdown := RenderMarkdown(messages, nil, MarkdownOptions{ExcludeThinking: true})

	assert.NotContains(t, markdown, "### Assistant · Thinking")
	assert.NotContains(t, markdown, "Internal reasoning")
	assert.Contains(t, markdown, "### User")
	assert.Contains(t, markdown, "### Assistant")
	assert.Contains(t, markdown, "Here is the summary.")
}

func TestRenderMarkdownShowsNoMessagesWhenOnlyThinkingIsExcluded(t *testing.T) {
	messages := []StreamableMessage{{Kind: "thinking", Role: "assistant", Content: "Internal reasoning"}}

	markdown := RenderMarkdown(messages, nil, MarkdownOptions{ExcludeThinking: true})

	assert.Contains(t, markdown, "_No messages._")
	assert.NotContains(t, markdown, "Internal reasoning")
}

func TestRenderMarkdownRendersOpenAIRawTextAndImages(t *testing.T) {
	messages := []StreamableMessage{
		{
			Kind: "text",
			Role: "user",
			RawItem: []byte(`{
				"content": [
					{"type":"input_text","text":"Describe this"},
					{"type":"input_image","image_url":"data:image/png;base64,abc"},
					{"type":"input_image","image_url":"https://example.com/cat.png"}
				]
			}`),
		},
		{
			Kind:    "text",
			Role:    "assistant",
			RawItem: []byte(`{"content":"raw assistant text"}`),
		},
	}

	markdown := RenderMarkdown(messages, nil, MarkdownOptions{})

	assert.Contains(t, markdown, "Describe this")
	assert.Contains(t, markdown, "_Inline image input (image/png)._")
	assert.Contains(t, markdown, "Image input: <https://example.com/cat.png>")
	assert.Contains(t, markdown, "raw assistant text")
	assert.NotContains(t, markdown, "_Empty message._")
}

func TestRenderMarkdownToolUseMergesMatchingStructuredResult(t *testing.T) {
	messages := []StreamableMessage{
		{Kind: "tool-use", Role: "assistant", ToolName: "bash", ToolCallID: "call-1", Input: `{"command":"echo hi","description":"say hi","timeout":12}`},
		{Kind: "text", Role: "assistant", Content: "interleaved"},
		{Kind: "tool-result", Role: "user", ToolCallID: "call-1", Content: `{"fallback":"unused"}`},
		{Kind: "tool-result", Role: "user", ToolName: "bash", Content: "standalone"},
	}
	toolResults := map[string]tooltypes.StructuredToolResult{
		"call-1": {
			ToolName:  "bash",
			Success:   true,
			Timestamp: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC),
			Metadata: tooltypes.BashMetadata{
				Command:       "echo hi",
				ExitCode:      0,
				Output:        "hi\n",
				ExecutionTime: time.Second,
				WorkingDir:    "/tmp/project",
			},
		},
	}

	markdown := RenderMarkdown(messages, toolResults, MarkdownOptions{})

	assert.Contains(t, markdown, "### Assistant · Tool")
	assert.Contains(t, markdown, "- **Tool:** `bash`")
	assert.Contains(t, markdown, "- **Call ID:** `call-1`")
	assert.Contains(t, markdown, "**Command**")
	assert.Contains(t, markdown, "echo hi")
	assert.Contains(t, markdown, "**Result**")
	assert.Contains(t, markdown, "- **Working directory:** `/tmp/project`")
	assert.Contains(t, markdown, "hi")
	assert.Contains(t, markdown, "### Assistant")
	assert.Contains(t, markdown, "interleaved")
	assert.NotContains(t, markdown, "fallback")
	assert.Equal(t, 1, strings.Count(markdown, "### User · Tool Result"), "matched result should be consumed, standalone result should remain")
}

func TestRenderMarkdownToolUseMatchesByToolNameWhenCallIDMissing(t *testing.T) {
	messages := []StreamableMessage{
		{Kind: "tool-use", Role: "assistant", ToolName: "unknown_tool", Input: `{"alpha":1}`},
		{Kind: "tool-result", Role: "user", ToolName: "unknown_tool", Content: `{"ok":true}`},
	}

	markdown := RenderMarkdown(messages, nil, MarkdownOptions{})

	assert.Contains(t, markdown, "### Assistant · Tool")
	assert.Contains(t, markdown, "**Result**")
	assert.Contains(t, markdown, `"ok": true`)
	assert.NotContains(t, markdown, "### User · Tool Result")
}

func TestRenderMarkdownRawToolPayloadTruncationAndImageFiltering(t *testing.T) {
	messages := []StreamableMessage{
		{
			Kind:    "tool-result",
			Role:    "user",
			Content: `[{"type":"image","data":"hidden"},{"output":"0123456789abcdef"}]`,
		},
		{
			Kind:    "tool-result",
			Role:    "user",
			Content: `abcdefghijklmnopqrstuvwxyz`,
		},
	}

	markdown := RenderMarkdown(messages, nil, MarkdownOptions{
		TruncateToolResults: true,
		MaxToolResultChars:  20,
		MaxToolResultBytes:  4096,
	})

	assert.NotContains(t, markdown, "hidden")
	assert.Contains(t, markdown, "abcdefghijklmnopqrst")
	assert.Contains(t, markdown, "omitted remaining lines")
}

func TestRenderMarkdownAppliesByteLimitPlaceholder(t *testing.T) {
	messages := []StreamableMessage{{Kind: "tool-result", Role: "user", Content: `{"text":"abcdefghijklmnopqrstuvwxyz"}`}}

	markdown := RenderMarkdown(messages, nil, MarkdownOptions{MaxToolResultBytes: 10})

	assert.Contains(t, markdown, "Tool result truncated")
	assert.Contains(t, markdown, "exceeds limit")
}

func TestRenderMarkdownStructuredResultHardCapsLargeMetadata(t *testing.T) {
	messages := []StreamableMessage{{Kind: "tool-result", Role: "user", ToolCallID: "call-1"}}
	toolResults := map[string]tooltypes.StructuredToolResult{
		"call-1": {
			ToolName:  "bash",
			Success:   false,
			Error:     strings.Repeat("e", 20),
			Timestamp: time.Now(),
			Metadata:  tooltypes.BashMetadata{Command: "cmd", ExitCode: 1, Output: strings.Repeat("x", 200), ExecutionTime: time.Second},
		},
	}

	markdown := RenderMarkdown(messages, toolResults, MarkdownOptions{
		TruncateToolResults: true,
		MaxToolResultChars:  5,
		MaxToolResultBytes:  30,
	})

	assert.Contains(t, markdown, "Tool result truncated")
	assert.NotContains(t, markdown, strings.Repeat("x", 50))
}

func TestRenderMarkdownHelpersCoverEdgeCases(t *testing.T) {
	assert.Equal(t, "", renderInputImageMarkdown(""))
	assert.Equal(t, "_Inline image input._", renderInputImageMarkdown("data:image-without-comma"))
	assert.Equal(t, "", mediaTypeFromDataURL("https://example.com/image.png"))
	assert.Equal(t, "alpha", cutBefore("alpha,beta", ","))
	assert.Equal(t, "alpha", cutBefore("alpha", ","))
	assert.Equal(t, "``a`b``", inlineMarkdownCode("a`b"))

	structured := tooltypes.StructuredToolResult{ToolName: "bash", Success: true, Timestamp: time.Now()}
	raw, err := structuredToolResultToMap(structured)
	require.NoError(t, err)
	assert.Equal(t, "bash", raw["toolName"])

	roundTrip, err := structuredToolResultFromMap(raw)
	require.NoError(t, err)
	assert.Equal(t, "bash", roundTrip.ToolName)
}

func TestMarkdownRoleHeadingsAndRawFallbacks(t *testing.T) {
	markdown := RenderMarkdown([]StreamableMessage{
		{Kind: "text", Role: "system", Content: "system prompt"},
		{Kind: "unknown", Role: "critic", Content: "custom role"},
		{Kind: "tool-use", Role: "assistant", ToolName: "custom", Input: `not-json`},
		{Kind: "tool-result", Role: "user", ToolName: "custom", Content: `plain result`},
	}, nil, MarkdownOptions{})

	assert.Contains(t, markdown, "### System")
	assert.Contains(t, markdown, "### Critic")
	assert.Contains(t, markdown, "custom role")
	assert.Contains(t, markdown, "plain result")
	assert.NotContains(t, markdown, "### User · Tool Result")

	assert.Equal(t, "Message · Tool Call", markdownRoleHeading("", "tool-use"))
	assert.Equal(t, "Assistant · Tool Result", markdownRoleHeading("assistant", "tool-result"))
}

func TestMarkdownInvalidOpenAIRawItemFallsBackToContent(t *testing.T) {
	markdown := RenderMarkdown([]StreamableMessage{
		{Kind: "text", Role: "user", Content: "fallback", RawItem: []byte(`{"content": 123}`)},
		{Kind: "text", Role: "assistant", RawItem: []byte(`not-json`)},
	}, nil, MarkdownOptions{})

	assert.Contains(t, markdown, "fallback")
	assert.Contains(t, markdown, "_Empty message._")
}

func TestMarkdownToolPayloadHelpersCoverInvalidAndScalarValues(t *testing.T) {
	prepared, hardCapped := prepareToolPayloadForMarkdown("plain", MarkdownOptions{MaxToolResultBytes: 1024})
	assert.Equal(t, "plain", prepared)
	assert.False(t, hardCapped)
	assert.Equal(t, "plain", truncateToolPayloadValue("plain", 100))
	assert.Equal(t, 42.0, truncateToolPayloadValue(42.0, 100))
	assert.Equal(t, []any{1.0, "two"}, truncateToolPayloadValue([]any{1.0, "two"}, 100))
	assert.Equal(t, map[string]any{"text": "short"}, truncateToolPayloadValue(map[string]any{"text": "short"}, 100))
	assert.False(t, isToolPayloadImage("not-an-object"))
	assert.False(t, isToolPayloadImage(map[string]any{"type": "text", "data": "visible"}))

	assert.Contains(t, string(toolPayloadJSON(func() {})), "0x")

	placeholder := toolPayloadByteLimitPlaceholder("payload", 2048, 1024)
	require.IsType(t, "", placeholder)
	assert.Contains(t, placeholder, "2KB")
	assert.Contains(t, placeholder, "1KB")
	arrayPlaceholder := toolPayloadByteLimitPlaceholder([]any{"payload"}, 2048, 1024)
	require.IsType(t, []any{}, arrayPlaceholder)
	assert.Len(t, arrayPlaceholder, 1)

	result, err := structuredToolResultFromMap(map[string]any{"toolName": func() {}})
	assert.Zero(t, result)
	require.Error(t, err)

	_, ok := lookupStructuredToolResult(StreamableMessage{ToolCallID: "missing"}, map[string]tooltypes.StructuredToolResult{})
	assert.False(t, ok)
}

func TestMarkdownStructuredResultFallsBackWhenMetadataCannotMarshal(t *testing.T) {
	badResult := tooltypes.StructuredToolResult{
		ToolName:  "bad_tool",
		Success:   true,
		Timestamp: time.Now(),
		Metadata:  badToolMetadata{},
	}

	prepared, hardCappedPayload, hardCapped := prepareStructuredToolResultForMarkdown(badResult, MarkdownOptions{MaxToolResultBytes: 1024})
	assert.Equal(t, badResult, prepared)
	assert.Nil(t, hardCappedPayload)
	assert.False(t, hardCapped)

	badRaw := renderToolPayloadValueMarkdown(func() {})
	assert.Contains(t, badRaw, "```text")
	assert.Contains(t, badRaw, "0x")
}

type badToolMetadata struct{}

func (badToolMetadata) ToolType() string { return "bad_tool" }

func (badToolMetadata) MarshalJSON() ([]byte, error) {
	return nil, assert.AnError
}
