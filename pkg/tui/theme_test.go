package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAutomaticThemeNameUsesTerminalBackground(t *testing.T) {
	assert.Equal(t, LightThemeName, automaticThemeName(false))
	assert.Equal(t, DefaultThemeName, automaticThemeName(true))
}

func TestResolveAutoThemeUsesLipglossTerminalDetection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	previous := lipgloss.HasDarkBackground()
	t.Cleanup(func() {
		lipgloss.SetHasDarkBackground(previous)
	})

	lipgloss.SetHasDarkBackground(false)
	light, err := resolveTheme(AutoThemeName)
	require.NoError(t, err)
	assert.Equal(t, LightThemeName, light.Name)

	lipgloss.SetHasDarkBackground(true)
	dark, err := resolveTheme(AutoThemeName)
	require.NoError(t, err)
	assert.Equal(t, DefaultThemeName, dark.Name)
}

func TestAvailableThemeNamesIncludeAutoAndCatppuccinVariants(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	names := AvailableThemeNames()

	assert.Contains(t, names, AutoThemeName)
	assert.Contains(t, names, LightThemeName)
	assert.Contains(t, names, DefaultThemeName)
}

func TestThemePickerOptionsMarkCurrentThemeWithSuffix(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	labels, values, selected := themePickerOptions(LightThemeName)

	require.Equal(t, len(values), len(labels))
	require.Equal(t, LightThemeName, values[selected])
	assert.Equal(t, LightThemeName+currentThemeSuffix, labels[selected])
	currentCount := 0
	for _, label := range labels {
		if strings.HasSuffix(label, currentThemeSuffix) {
			currentCount++
		}
	}
	assert.Equal(t, 1, currentCount)
}

func TestThemeSlashCommandOpensPickerWithoutStartingConversation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := newModel(context.Background(), Config{Theme: DefaultThemeName})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()
	m.textarea.SetValue("/theme")

	cmd := m.submit()

	assert.Nil(t, cmd)
	assert.False(t, m.running)
	assert.Empty(t, m.conversationID)
	assert.Empty(t, m.textarea.Value())
	require.NotNil(t, m.activeUIPrompt)
	assert.Equal(t, uiPromptTheme, m.activeUIPrompt.origin)
	assert.Equal(t, uiPromptSelect, m.activeUIPrompt.mode)
	assert.Equal(t, "Select Theme", m.activeUIPrompt.title)
	assert.Equal(t, "Apply", m.activeUIPrompt.submitButtonText)
	assert.Equal(t, DefaultThemeName, m.activeUIPrompt.optionValues[m.activeUIPrompt.selectIndex])
	assert.Equal(t, DefaultThemeName+currentThemeSuffix, m.activeUIPrompt.options[m.activeUIPrompt.selectIndex])
	plain := xansi.Strip(m.renderUIDialog())
	assert.Contains(t, plain, DefaultThemeName+currentThemeSuffix)
}

func TestThemePickerSelectionAppliesThemeImmediately(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := newModel(context.Background(), Config{Theme: DefaultThemeName})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()
	require.NotEmpty(t, m.renderMarkdown("`code`", 40, markdownAssistant))
	require.NotNil(t, m.assistantMarkdownRenderer)

	m.openThemePicker()
	require.NotNil(t, m.activeUIPrompt)
	lightIndex := indexOfString(m.activeUIPrompt.optionValues, LightThemeName)
	require.NotEqual(t, -1, lightIndex)
	m.activeUIPrompt.selectIndex = lightIndex

	cmd := m.submitUIPrompt()

	assert.Nil(t, cmd)
	assert.Nil(t, m.activeUIPrompt)
	assert.Equal(t, LightThemeName, m.themeSelection)
	assert.Equal(t, LightThemeName, m.theme.Name)
	assert.False(t, m.theme.Dark)
	assert.Nil(t, m.assistantMarkdownRenderer)
	assert.Nil(t, m.thoughtMarkdownRenderer)
	assert.Equal(t, composerTextStyle, m.textarea.FocusedStyle.Text)
	assert.Equal(t, composerCursorStyle, m.textarea.Cursor.Style)
}

func TestThemeSlashCommandAcceptsDirectThemeName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := newModel(context.Background(), Config{Theme: DefaultThemeName})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()
	m.textarea.SetValue("/theme catppuccin-latte")

	cmd := m.submit()

	assert.Nil(t, cmd)
	assert.Nil(t, m.activeUIPrompt)
	assert.Equal(t, LightThemeName, m.themeSelection)
	assert.Equal(t, LightThemeName, m.theme.Name)
	assert.False(t, m.running)
	assert.Empty(t, m.conversationID)
}

func TestThemeSlashCommandReportsInvalidDirectTheme(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := newModel(context.Background(), Config{Theme: DefaultThemeName})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()
	m.textarea.SetValue("/theme missing-theme")

	cmd := m.submit()

	require.NotNil(t, cmd)
	require.Len(t, m.uiNotifications, 1)
	assert.Equal(t, uiNotificationError, m.uiNotifications[0].level)
	assert.Equal(t, "Theme unavailable", m.uiNotifications[0].title)
	assert.ErrorContains(t, ValidateThemeName("missing-theme"), "unknown TUI theme")
	assert.Equal(t, DefaultThemeName, m.themeSelection)
}

func TestBundledThemesAreValid(t *testing.T) {
	for name, theme := range themes {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, validateTheme(theme))
		})
	}
}

func TestCustomThemeLoadsFromKodeletThemesDirectory(t *testing.T) {
	writeCustomTheme(t, "forest.theme", `
base: catppuccin-latte
assistant: "#112233"
profile_colors:
  - "#112233"
  - "#445566"
ui:
  dialog_border: "#010203"
markdown:
  code: "#abcdef"
`)

	theme, err := resolveTheme("forest")
	require.NoError(t, err)

	assert.Equal(t, "forest", theme.Name)
	assert.False(t, theme.Dark)
	assert.Equal(t, "#112233", theme.Assistant)
	assert.Equal(t, themes[LightThemeName].User, theme.User)
	assert.Equal(t, []string{"#112233", "#445566"}, theme.ProfileColors)
	assert.Equal(t, "#010203", theme.UI.DialogBorder)
	assert.Equal(t, themes[LightThemeName].UI.DialogTitle, theme.UI.DialogTitle)
	assert.Equal(t, "#abcdef", theme.Markdown.Code)
	assert.Equal(t, themes[LightThemeName].Markdown.Link, theme.Markdown.Link)
	assert.Contains(t, AvailableThemeNames(), "forest")
	assert.NoError(t, ValidateThemeName("forest"))
}

func TestCustomThemeDefaultsToCatppuccinMochaBase(t *testing.T) {
	writeCustomTheme(t, "minimal.theme", `assistant: "#112233"`)

	theme, err := resolveTheme("minimal")
	require.NoError(t, err)

	assert.True(t, theme.Dark)
	assert.Equal(t, "#112233", theme.Assistant)
	assert.Equal(t, themes[DefaultThemeName].User, theme.User)
}

func TestCustomThemeRejectsUnknownFields(t *testing.T) {
	writeCustomTheme(t, "broken.theme", `
base: catppuccin-mocha
assistant_typo: "#112233"
`)

	err := ValidateThemeName("broken")

	require.Error(t, err)
	assert.ErrorContains(t, err, "field assistant_typo not found")
	assert.NotContains(t, AvailableThemeNames(), "broken")
}

func TestCustomThemeRejectsInvalidColors(t *testing.T) {
	writeCustomTheme(t, "broken.theme", `assistant: red`)

	err := ValidateThemeName("broken")

	require.Error(t, err)
	assert.ErrorContains(t, err, "must be a #rrggbb color")
}

func TestCustomThemeRejectsUnknownBase(t *testing.T) {
	writeCustomTheme(t, "broken.theme", `base: missing-theme`)

	err := ValidateThemeName("broken")

	require.Error(t, err)
	assert.ErrorContains(t, err, `unknown base theme "missing-theme"`)
}

func TestBundledThemeWinsOverSameNamedCustomFile(t *testing.T) {
	writeCustomTheme(t, DefaultThemeName+customThemeExtension, `assistant: "#112233"`)

	theme, err := resolveTheme(DefaultThemeName)
	require.NoError(t, err)

	assert.Equal(t, themes[DefaultThemeName].Assistant, theme.Assistant)
}

func writeCustomTheme(t *testing.T, filename, contents string) {
	t.Helper()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	themesDir := filepath.Join(homeDir, ".kodelet", "themes")
	require.NoError(t, os.MkdirAll(themesDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(themesDir, filename), []byte(contents), 0o644))
}

func indexOfString(values []string, target string) int {
	for index, value := range values {
		if value == target {
			return index
		}
	}
	return -1
}
