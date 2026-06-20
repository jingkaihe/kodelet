package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/muesli/termenv"
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

func TestRenderInputBoxUsesCatppuccinComposerStyles(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(previous)
	})

	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 12
	m.resize()
	m.textarea.SetValue("draft")

	box := m.renderInputBox()

	assert.Contains(t, box, "\x1b[38;2;205;214;243m")   // Catppuccin text border
	assert.Contains(t, box, "\x1b[38;2;205;214;243m")   // Catppuccin text labels
	assert.NotContains(t, box, "\x1b[1;")               // labels are not bolded
	assert.Contains(t, box, "\x1b[38;2;245;224;220m")   // rosewater text
	assert.Contains(t, box, "\x1b[7;38;2;250;179;135m") // peach cursor
	assert.NotContains(t, box, "48;2;")                 // no composer background fill
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
