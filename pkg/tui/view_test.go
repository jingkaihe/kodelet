package tui

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
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
	plainLines := strings.Split(view, "\n")

	assert.Contains(t, view, "draft")
	assert.Contains(t, view, "1.5K/3.0K (50%)")
	assert.Contains(t, view, "work")
	assert.Equal(t, 3, m.textarea.Height())
	assert.True(t, strings.HasPrefix(plainLines[0], strings.Repeat(" ", tuiLeftMargin)))
	assert.Equal(t, m.width-tuiRightMargin, tuiLeftMargin+m.inputOuterWidth())
	assert.Equal(t, m.contentWidth(), m.viewport.Width)
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

func TestRunningIndicatorRendersInComposerBottomBorder(t *testing.T) {
	m := newModel(context.Background(), Config{CWD: "/tmp/kodelet-workspace"})
	t.Cleanup(m.cancel)
	m.width = 72
	m.height = 12
	m.resize()
	m.running = true
	m.entries = []chatEntry{{kind: entryUser, content: "hello"}}
	m.refreshViewport(true)

	transcript := xansi.Strip(m.viewport.View())
	rawView := m.View()
	view := xansi.Strip(rawView)
	lines := strings.Split(view, "\n")
	bottomBorder := lines[len(lines)-1]
	rawLines := strings.Split(rawView, "\n")
	rawBottomBorder := rawLines[len(rawLines)-1]

	assert.NotContains(t, transcript, "Following the thread…")
	assert.NotContains(t, transcript, "working…")
	assert.Contains(t, bottomBorder, "~ Following the thread…")
	assert.Contains(t, bottomBorder, "Following the thread…")
	assert.NotContains(t, bottomBorder, "working…")
	assert.Contains(t, bottomBorder, displayCWD(m.cwd))
	assert.Equal(t, 1, lipgloss.Width(m.flowingWaterFrame()))
	assert.Equal(t, 1, utf8.RuneCountInString(m.flowingWaterFrame()))
	flowStart, _ := styleSequences(composerFlowStyle)
	labelStart, _ := styleSequences(composerLabelStyle)
	assert.Contains(t, rawBottomBorder, flowStart+"~")
	assert.Contains(t, rawBottomBorder, labelStart+" Following the thread…")
	assert.Contains(t, rawBottomBorder, labelStart+" "+displayCWD(m.cwd))

	m.workingFrame = 8
	view = xansi.Strip(m.View())
	lines = strings.Split(view, "\n")
	bottomBorder = lines[len(lines)-1]

	assert.Contains(t, bottomBorder, "≈ Following the thread…")
	assert.Equal(t, 1, lipgloss.Width(m.flowingWaterFrame()))
	assert.Equal(t, 1, utf8.RuneCountInString(m.flowingWaterFrame()))

	m.workingFrame = 16
	view = xansi.Strip(m.View())
	lines = strings.Split(view, "\n")
	bottomBorder = lines[len(lines)-1]

	assert.Contains(t, bottomBorder, "≋ Following the thread…")
	assert.Equal(t, 1, lipgloss.Width(m.flowingWaterFrame()))
	assert.Equal(t, 1, utf8.RuneCountInString(m.flowingWaterFrame()))

	m.workingFrame = 36
	view = xansi.Strip(m.View())
	lines = strings.Split(view, "\n")
	bottomBorder = lines[len(lines)-1]

	assert.Contains(t, bottomBorder, "Gathering the next clue…")
	assert.Contains(t, bottomBorder, displayCWD(m.cwd))

	m.running = false
	m.refreshViewport(true)
	view = xansi.Strip(m.View())
	lines = strings.Split(view, "\n")
	bottomBorder = lines[len(lines)-1]

	assert.NotContains(t, bottomBorder, "Following the thread…")
	assert.NotContains(t, bottomBorder, "Gathering the next clue…")
	assert.NotContains(t, bottomBorder, "working…")
	assert.Contains(t, bottomBorder, displayCWD(m.cwd))
}

func TestNewModelDefaultsToCatppuccinMochaTheme(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)

	assert.Equal(t, DefaultThemeName, m.theme.Name)
	assert.Equal(t, "#cdd6f4", themes[DefaultThemeName].Assistant)
}

func TestComposerLabelThemeColors(t *testing.T) {
	for _, theme := range themes {
		assert.Equal(t, theme.ThoughtBody, theme.ComposerLabel)
		assert.NotEmpty(t, theme.ComposerFlow)
	}
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
