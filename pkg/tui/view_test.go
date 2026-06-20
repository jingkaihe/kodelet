package tui

import (
	"context"
	"strings"
	"testing"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
)

func TestViewAndFormattingHelpers(t *testing.T) {
	m := newModel(context.Background(), Config{Profile: " work ", CWD: ""})
	t.Cleanup(m.cancel)

	assert.Empty(t, m.View())
	m.width = 48
	m.height = 12
	m.resize()
	m.usage = llmtypes.Usage{
		CurrentContextWindow: 1500,
		MaxContextWindow:     3000,
		InputCost:            0.125,
		OutputCost:           0.125,
	}
	m.textarea.SetValue("draft")
	view := m.View()

	assert.Contains(t, view, "draft")
	assert.Contains(t, view, "1.5K/3.0K (50%)")
	assert.Contains(t, view, "work")
	assert.Equal(t, "default", displayProfile(""))
	assert.Equal(t, "", profileForRequest("default"))
	assert.Equal(t, "work", profileForRequest(" work "))
	assert.Equal(t, "abcdefgh", shortID("abcdefghi123"))
	assert.Equal(t, "…", fitVisible("abcdef", 1))
	assert.Equal(t, "abcdef", fitVisible("abcdef", 20))
	assert.Equal(t, "a   ", padVisible("a", 4))
	chunk, rest := splitVisible("abcdef", 3)
	assert.Equal(t, "abc", chunk)
	assert.Equal(t, "def", rest)
	assert.Equal(t, "plain", compactJSON(" plain "))
	assert.Equal(t, `{"a":1}`, compactJSON("{\n  \"a\": 1\n}"))
	assert.Equal(t, "  one\n  \n  two", indentText("one\n\ntwo", "  "))
	assert.Equal(t, 2, lineCount("one\ntwo"))
	assert.True(t, strings.HasPrefix(rightLabeledBorder("╭", "╮", 12, "label"), "╭"))
}

func TestNewModelDefaultsToCatppuccinMochaTheme(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)

	assert.Equal(t, DefaultThemeName, m.theme.Name)
	assert.Equal(t, "#cdd6f4", themes[DefaultThemeName].Assistant)
}

func TestNewModelUsesConfiguredTheme(t *testing.T) {
	m := newModel(context.Background(), Config{Theme: " tokyo-night "})
	t.Cleanup(m.cancel)

	assert.Equal(t, "tokyo-night", m.theme.Name)
}

func TestValidateThemeName(t *testing.T) {
	assert.NoError(t, ValidateThemeName(DefaultThemeName))
	assert.NoError(t, ValidateThemeName(""))
	assert.ErrorContains(t, ValidateThemeName("missing-theme"), "unknown TUI theme")
}

func TestRenderExitSummary(t *testing.T) {
	summary := renderExitSummary(" conversation-123 ", llmtypes.Usage{
		InputTokens:              1200,
		OutputTokens:             300,
		CacheCreationInputTokens: 40,
		CacheReadInputTokens:     60,
		InputCost:                0.01,
		OutputCost:               0.02,
		CacheCreationCost:        0.003,
		CacheReadCost:            0.001,
		CurrentContextWindow:     1600,
		MaxContextWindow:         3200,
	})

	assert.Contains(t, summary, "Conversation ID: conversation-123")
	assert.Contains(t, summary, "Token usage: 1.2K input · 300 output · 40 cache write · 60 cache read · 1.6K total")
	assert.Contains(t, summary, "Context window: 1.6K/3.2K (50%)")
	assert.Contains(t, summary, "Cost: $0.0340")
	assert.Contains(t, summary, "Resume: kodelet chat -r conversation-123")
	assert.Empty(t, renderExitSummary(" ", llmtypes.Usage{}))
}
