package conversations

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
