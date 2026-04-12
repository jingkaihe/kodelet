package base

import (
	"context"
	"strings"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateShortSummary(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		called := false
		messages := []conversations.StreamableMessage{{Kind: "text", Role: "user", Content: "Fallback title"}}
		summary := GenerateShortSummary(
			ctx,
			messages,
			false,
			"summary prompt",
			func(_ context.Context, prompt string, useWeakModel bool) (string, error) {
				assert.Contains(t, prompt, "Conversation to summarize:")
				assert.Contains(t, prompt, "summary prompt")
				assert.True(t, useWeakModel)
				return "generated summary.", nil
			},
			func(error) {
				called = true
			},
		)

		assert.Equal(t, "generated summary", summary)
		assert.False(t, called)
	})

	t.Run("preserves ellipsis", func(t *testing.T) {
		messages := []conversations.StreamableMessage{{Kind: "text", Role: "user", Content: "Fallback title"}}
		summary := GenerateShortSummary(
			ctx,
			messages,
			false,
			"summary prompt",
			func(_ context.Context, prompt string, useWeakModel bool) (string, error) {
				assert.Contains(t, prompt, "Conversation to summarize:")
				assert.True(t, useWeakModel)
				return "generated summary...", nil
			},
			nil,
		)

		assert.Equal(t, "generated summary...", summary)
	})

	t.Run("error with callback", func(t *testing.T) {
		var gotErr error
		messages := []conversations.StreamableMessage{{Kind: "text", Role: "user", Content: "Fallback title"}}
		summary := GenerateShortSummary(
			ctx,
			messages,
			false,
			"summary prompt",
			func(context.Context, string, bool) (string, error) {
				return "", errors.New("generation failed")
			},
			func(err error) {
				gotErr = err
			},
		)

		assert.Equal(t, "Fallback title", summary)
		require.Error(t, gotErr)
		assert.Contains(t, gotErr.Error(), "generation failed")
	})

	t.Run("error without callback", func(t *testing.T) {
		messages := []conversations.StreamableMessage{{Kind: "text", Role: "user", Content: "Fallback title"}}
		summary := GenerateShortSummary(
			ctx,
			messages,
			false,
			"summary prompt",
			func(context.Context, string, bool) (string, error) {
				return "", errors.New("generation failed")
			},
			nil,
		)

		assert.Equal(t, "Fallback title", summary)
	})

	t.Run("disabled llm summary uses fallback", func(t *testing.T) {
		messages := []conversations.StreamableMessage{{Kind: "text", Role: "user", Content: "Use first user message instead"}}
		called := false

		summary := GenerateShortSummary(
			ctx,
			messages,
			true,
			"summary prompt",
			func(context.Context, string, bool) (string, error) {
				called = true
				return "generated summary", nil
			},
			nil,
		)

		assert.Equal(t, "Use first user message instead", summary)
		assert.False(t, called)
	})

	t.Run("empty model summary falls back to first user message", func(t *testing.T) {
		messages := []conversations.StreamableMessage{{Kind: "text", Role: "user", Content: "Fallback title"}}

		summary := GenerateShortSummary(
			ctx,
			messages,
			false,
			"summary prompt",
			func(context.Context, string, bool) (string, error) {
				return "   ", nil
			},
			nil,
		)

		assert.Equal(t, "Fallback title", summary)
	})
}

func TestFirstUserMessageFallback(t *testing.T) {
	t.Run("prefers first user text message", func(t *testing.T) {
		messages := []conversations.StreamableMessage{
			{Kind: "text", Role: "assistant", Content: "Ignore"},
			{Kind: "text", Role: "user", Content: "  First user message  "},
			{Kind: "text", Role: "user", Content: "Second user message"},
		}

		assert.Equal(t, "First user message", FirstUserMessageFallback(messages))
	})

	t.Run("uses raw item text when content is empty", func(t *testing.T) {
		messages := []conversations.StreamableMessage{
			{
				Kind:    "text",
				Role:    "user",
				RawItem: []byte(`{"content":[{"type":"input_text","text":"Message from raw item"}]}`),
			},
		}

		assert.Equal(t, "Message from raw item", FirstUserMessageFallback(messages))
	})

	t.Run("truncates long fallback to 100 chars", func(t *testing.T) {
		long := "This is a very long user message that should be truncated when used as the fallback conversation summary text."
		messages := []conversations.StreamableMessage{{Kind: "text", Role: "user", Content: long}}

		fallback := FirstUserMessageFallback(messages)
		assert.Len(t, fallback, 100)
		assert.True(t, strings.HasSuffix(fallback, "..."))
	})
}

func TestRenderMarkdownForSummaryExcludesThinking(t *testing.T) {
	messages := []conversations.StreamableMessage{
		{Kind: "text", Role: "user", Content: "Summarize this"},
		{Kind: "thinking", Role: "assistant", Content: "Internal reasoning"},
		{Kind: "text", Role: "assistant", Content: "Here is the summary."},
	}

	markdown := RenderMarkdownForSummary(messages, nil)

	assert.Contains(t, markdown, "Summarize this")
	assert.Contains(t, markdown, "Here is the summary.")
	assert.NotContains(t, markdown, "### Assistant · Thinking")
	assert.NotContains(t, markdown, "Internal reasoning")
}
