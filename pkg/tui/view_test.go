package tui

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/slashcommands"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	assert.Equal(t, "default", displayProfile(" DEFAULT "))
	assert.Equal(t, "default", profileForRequest("default"))
	assert.Equal(t, "work", profileForRequest(" work "))
	assert.Equal(t, []string{"default", "work", "prod"}, normalizeProfileOptions([]string{"default", "work", "work"}, "prod"))
	assert.Equal(t, 1, profileOptionIndex([]string{"default", "work"}, " WORK "))
	assert.Equal(t, "work", profileFromMetadata(map[string]any{"profile": " work "}))
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

func TestInitialMessageRendersCenteredWithShortcutHint(t *testing.T) {
	withANSI256ColorProfile(t)

	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()
	m.refreshViewport(true)

	view := xansi.Strip(m.View())
	lines := strings.Split(view, "\n")
	messageLine := ""
	hintLine := ""
	messageIndex := -1
	hintIndex := -1
	for i, line := range lines {
		if strings.Contains(line, "Hello! What would you like me to work on?") {
			messageLine = line
			messageIndex = i
		}
		if strings.Contains(line, "? for shortcuts") {
			hintLine = line
			hintIndex = i
		}
	}

	assert.NotEmpty(t, messageLine)
	assert.NotEmpty(t, hintLine)
	messageStart := strings.Index(messageLine, "Hello!")
	hintStart := strings.Index(hintLine, "? for shortcuts")
	assert.Greater(t, messageStart, 10)
	assert.Equal(t, messageStart, hintStart)
	assert.Equal(t, messageIndex+3, hintIndex)

	rawView := m.View()
	assistantStart, _ := styleSequences(assistantStyle)
	assert.Contains(t, rawView, assistantStart+"?")
}

func TestShortcutsDialogRendersWithThemeColors(t *testing.T) {
	withANSI256ColorProfile(t)

	for _, themeName := range []string{DefaultThemeName, "tokyo-night"} {
		t.Run(themeName, func(t *testing.T) {
			m := newModel(context.Background(), Config{Theme: themeName})
			t.Cleanup(m.cancel)
			m.width = 96
			m.height = 24
			m.resize()
			m.openShortcutsDialog()

			rawView := m.View()
			view := xansi.Strip(rawView)

			assert.Contains(t, view, "Shortcuts")
			assert.Contains(t, view, "Ctrl+G")
			assert.Contains(t, view, "Edit draft in $EDITOR")
			assert.Contains(t, view, "Press Esc, Enter, ?, or q to close.")
			dialogLines := strings.Split(xansi.Strip(m.renderShortcutsDialog()), "\n")
			require.GreaterOrEqual(t, len(dialogLines), 3)
			blankLine := strings.TrimSuffix(strings.TrimPrefix(dialogLines[2], "│"), "│")
			assert.Empty(t, strings.TrimSpace(blankLine))
			borderStart, _ := styleSequences(uiDialogBorderStyle)
			buttonStart, _ := styleSequences(uiDialogButtonStyle)
			assert.Contains(t, rawView, borderStart+"╭")
			assert.Contains(t, rawView, uiDialogTitleStyle.Render("Shortcuts"))
			assert.Contains(t, rawView, buttonStart+"Ctrl+G")
		})
	}
}

func TestNotificationSeverityUsesThemeColors(t *testing.T) {
	withANSI256ColorProfile(t)

	for _, themeName := range []string{DefaultThemeName, "tokyo-night"} {
		t.Run(themeName, func(t *testing.T) {
			m := newModel(context.Background(), Config{Theme: themeName})
			t.Cleanup(m.cancel)
			m.width = 96
			m.height = 24
			m.resize()

			info := m.renderUINotification(uiNotification{level: uiNotificationInfo, title: "Info", message: "Done"})
			warning := m.renderUINotification(uiNotification{level: uiNotificationWarning, title: "Warning", message: "Check config"})
			errorBox := m.renderUINotification(uiNotification{level: uiNotificationError, title: "Error", message: "Launch failed"})

			infoBorderStart, _ := styleSequences(uiNotificationBorderStyle)
			infoTitleStart, _ := styleSequences(uiNotificationTitleStyle)
			warningBorderStart, _ := styleSequences(uiNotificationWarningBorderStyle)
			warningTitleStart, _ := styleSequences(uiNotificationWarningTitleStyle)
			errorBorderStart, _ := styleSequences(uiNotificationErrorBorderStyle)
			errorTitleStart, _ := styleSequences(uiNotificationErrorTitleStyle)

			assert.Contains(t, info, infoBorderStart+"╭")
			assert.Contains(t, info, infoTitleStart+"Info")
			assert.Contains(t, warning, warningBorderStart+"╭")
			assert.Contains(t, warning, warningTitleStart+"Warning")
			assert.Contains(t, errorBox, errorBorderStart+"╭")
			assert.Contains(t, errorBox, errorTitleStart+"Error")
		})
	}
}

func TestProfilePickerRendersAboveComposerWithThemeColors(t *testing.T) {
	withANSI256ColorProfile(t)

	m := newModel(context.Background(), Config{Profile: "work", ProfileOptions: []string{"default", "work", "prod"}, Theme: "tokyo-night"})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()
	m.openProfilePicker()
	m.profilePickerIndex = 2
	m.resize()
	m.refreshViewport(true)

	rawView := m.View()
	view := xansi.Strip(rawView)
	lines := strings.Split(view, "\n")
	pickerLine := lines[m.viewport.Height+2]
	composerTop := lines[m.viewport.Height+m.profilePickerHeight()]

	assert.Contains(t, pickerLine, "prod")
	assert.Contains(t, composerTop, "work")
	assert.Contains(t, rawView, "\x1b[38;5;151mwork")
	assert.Contains(t, rawView, "48;5;")
	assert.NotContains(t, view, "→")
	assert.NotContains(t, view, "ACTIVE")
	assert.NotContains(t, view, "repo")
}

func TestReasoningPickerRendersBesideProfile(t *testing.T) {
	withANSI256ColorProfile(t)

	m := newModel(context.Background(), Config{
		Profile:                "work",
		ProfileOptions:         []string{"default", "work"},
		ReasoningEffort:        "medium",
		ReasoningEffortOptions: []string{"low", "medium", "high"},
		Theme:                  "tokyo-night",
	})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 24
	m.resize()
	m.openReasoningPicker()
	m.reasoningPickerIndex = 2
	m.resize()
	m.refreshViewport(true)

	view := xansi.Strip(m.View())
	lines := strings.Split(view, "\n")
	pickerLine := lines[m.viewport.Height+2]
	composerTop := lines[m.viewport.Height+m.reasoningPickerHeight()]

	assert.Contains(t, pickerLine, "high")
	assert.Contains(t, composerTop, "work")
	assert.Contains(t, composerTop, "effort:medium")
}

func TestSlashCommandSuggestionsRenderAboveComposerWithThemeColors(t *testing.T) {
	withANSI256ColorProfile(t)

	m := newModel(context.Background(), Config{Theme: "tokyo-night"})
	t.Cleanup(m.cancel)
	m.width = 160
	m.height = 24
	m.resize()
	m.slashCommands = []slashcommands.Command{
		{Name: "goal", Description: "Set the active goal", Hint: "objective"},
		{Name: "review", Description: "Review changes", Hint: "target", Placeholder: "/review target"},
	}
	m.textarea.SetValue("/")
	m.slashCommandIndex = 1
	m.resize()
	m.refreshViewport(true)

	rawView := m.View()
	view := xansi.Strip(rawView)
	lines := strings.Split(view, "\n")
	suggestionsTop := lines[m.viewport.Height]
	composerTop := lines[m.viewport.Height+m.slashCommandSuggestionsHeight()]

	assert.Contains(t, suggestionsTop, "/goal")
	assert.Contains(t, suggestionsTop, "Set the active goal")
	assert.NotContains(t, suggestionsTop, "objective")
	assert.Contains(t, view, "/review")
	assert.NotContains(t, view, "target")
	assert.Contains(t, composerTop, "default")
	assert.Equal(t, tuiLeftMargin+m.inputOuterWidth(), lipgloss.Width(suggestionsTop))
	assert.Contains(t, rawView, "\x1b[38;5;183m/review")
	assert.Contains(t, rawView, "48;5;")
	assert.NotContains(t, suggestionsTop, "│")
	assert.NotContains(t, suggestionsTop, "╰")
}

func TestSlashCommandUsageHintRendersInComposerPlaceholder(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 24
	m.resize()
	m.slashCommands = []slashcommands.Command{{
		Name:        "review",
		Description: "Review changes",
		Placeholder: "/review [target=HEAD] additional instructions",
	}}
	m.textarea.SetValue("/review ")
	m.resize()

	view := xansi.Strip(m.View())

	assert.Contains(t, view, "/review [target=HEAD] additional instructions")
	assert.NotContains(t, view, "/review \n")
}

func TestSlashCommandSuggestionsUseCompactRowLimits(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 120
	m.height = 40
	m.resize()
	for i := 0; i < 10; i++ {
		m.slashCommands = append(m.slashCommands, slashcommands.Command{Name: fmt.Sprintf("cmd-%d", i), Description: "Command description"})
	}
	m.textarea.SetValue("/")
	m.resize()

	assert.Equal(t, slashCommandBareQueryMaxRows, m.slashCommandSuggestionsHeight())

	m.textarea.SetValue("/cmd")
	m.resize()

	assert.Equal(t, slashCommandFilteredQueryMaxRows, m.slashCommandSuggestionsHeight())
}

func TestUISelectDialogRendersVisibleWindowAndScrollMarkers(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 14
	m.resize()
	options := []string{"one", "two", "three", "four", "five", "six"}
	m.openUIPrompt(uiPromptState{
		mode:        uiPromptSelect,
		title:       "Pick one",
		message:     "Choose carefully",
		options:     options,
		selectIndex: 4,
		response:    make(chan extensions.UIInputResponse, 1),
	})
	m.resize()

	plain := xansi.Strip(m.renderUIDialog())

	assert.Equal(t, 4, m.maxUISelectRows())
	assert.Equal(t, 1, visibleUISelectStart(len(options), 4, m.maxUISelectRows()))
	assert.Contains(t, plain, "↑ more")
	assert.Contains(t, plain, "› five")
	assert.Contains(t, plain, "↓ more")
	assert.NotContains(t, plain, "│   one")
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
		assert.NotEmpty(t, theme.SlashCommand.Selected)
		assert.Equal(t, theme.ThoughtBody, theme.SlashCommand.Description)
		assert.Equal(t, theme.ThoughtBody, theme.SlashCommand.Hint)
	}
	assert.Equal(t, themes[DefaultThemeName].Markdown.Code, themes[DefaultThemeName].SlashCommand.Command)
}

func TestThemeColorsAreHex(t *testing.T) {
	for name, theme := range themes {
		t.Run(name, func(t *testing.T) {
			assertThemeColorsAreHex(t, theme, "theme")
		})
	}
}

func assertThemeColorsAreHex(t *testing.T, value any, path string) {
	t.Helper()

	v := reflect.ValueOf(value)
	assertThemeValueColorsAreHex(t, v, path)
}

func assertThemeValueColorsAreHex(t *testing.T, value reflect.Value, path string) {
	t.Helper()

	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return
		}
		value = value.Elem()
	}

	switch value.Kind() {
	case reflect.Struct:
		typ := value.Type()
		for i := range value.NumField() {
			field := typ.Field(i)
			if field.Name == "Name" {
				continue
			}
			assertThemeValueColorsAreHex(t, value.Field(i), path+"."+field.Name)
		}
	case reflect.Slice:
		for i := range value.Len() {
			assertThemeValueColorsAreHex(t, value.Index(i), path+"[]")
		}
	case reflect.String:
		color := value.String()
		assert.Truef(t, isHexColor(color), "theme color %s must be empty or #rrggbb, got %q", path, color)
	}
}

func isHexColor(value string) bool {
	if value == "" {
		return true
	}
	if len(value) != 7 || value[0] != '#' {
		return false
	}
	for _, r := range value[1:] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
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
