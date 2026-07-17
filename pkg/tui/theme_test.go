package tui

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func TestThemePickerOptionsIncludeThemesAndMarkCurrent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	labels, values, selected := themePickerOptions(LightThemeName)

	require.Equal(t, len(values), len(labels))
	assert.Contains(t, values, AutoThemeName)
	assert.Contains(t, values, DefaultThemeName)
	require.Equal(t, LightThemeName, values[selected])
	assert.Equal(t, LightThemeName+currentThemeSuffix, labels[selected])
	for index := range labels {
		if index != selected {
			assert.Equal(t, values[index], labels[index])
		}
	}
}

func TestThemeSlashCommandOpensPickerWithoutStartingConversation(t *testing.T) {
	m := newThemeTestModel(t, Config{Theme: DefaultThemeName})
	m.textarea.SetValue("/theme")

	cmd := m.submit()

	assert.Nil(t, cmd)
	assert.False(t, m.running)
	assert.Empty(t, m.conversationID)
	assert.Empty(t, m.textarea.Value())
	require.NotNil(t, m.activeUIPrompt)
	assert.Equal(t, uiPromptTheme, m.activeUIPrompt.origin)
	assert.Equal(t, uiPromptSelect, m.activeUIPrompt.mode)
	plain := xansi.Strip(m.renderUIDialog())
	assert.Contains(t, plain, DefaultThemeName+currentThemeSuffix)
}

func TestThemePickerSelectionAppliesThemeImmediately(t *testing.T) {
	withANSI256ColorProfile(t)
	m := newThemeTestModel(t, Config{Theme: DefaultThemeName})
	require.NotEmpty(t, m.renderMarkdown("`code`", 40, markdownAssistant))
	require.NotNil(t, m.assistantMarkdownRenderer)
	previousComposerStyle, _ := styleSequences(composerTextStyle)

	m.openThemePicker()
	require.NotNil(t, m.activeUIPrompt)
	lightIndex := slices.Index(m.activeUIPrompt.optionValues, LightThemeName)
	require.NotEqual(t, -1, lightIndex)
	m.activeUIPrompt.selectIndex = lightIndex

	cmd := m.submitUIPrompt()

	assert.NotNil(t, cmd)
	assert.Nil(t, m.activeUIPrompt)
	assert.Equal(t, LightThemeName, m.themeSelection)
	assert.Equal(t, LightThemeName, m.theme.Name)
	assert.Nil(t, m.assistantMarkdownRenderer)
	m.textarea.SetValue("draft")
	composer := m.textarea.View()
	currentComposerStyle, _ := styleSequences(composerTextStyle)
	assert.Contains(t, composer, currentComposerStyle+"draft")
	assert.NotContains(t, composer, previousComposerStyle+"draft")
}

func TestThemeSlashCommandAcceptsDirectThemeName(t *testing.T) {
	m := newThemeTestModel(t, Config{Theme: DefaultThemeName})
	m.textarea.SetValue("/theme catppuccin-latte")

	cmd := m.submit()

	assert.NotNil(t, cmd)
	assert.Nil(t, m.activeUIPrompt)
	assert.Equal(t, LightThemeName, m.themeSelection)
	assert.Equal(t, LightThemeName, m.theme.Name)
	assert.False(t, m.running)
	assert.Empty(t, m.conversationID)
}

func TestThemeSlashCommandReportsInvalidDirectTheme(t *testing.T) {
	m := newThemeTestModel(t, Config{Theme: DefaultThemeName})
	m.textarea.SetValue("/theme missing-theme")

	cmd := m.submit()

	require.NotNil(t, cmd)
	require.Len(t, m.uiNotifications, 1)
	assert.Equal(t, uiNotificationError, m.uiNotifications[0].level)
	assert.Equal(t, "Theme unavailable", m.uiNotifications[0].title)
	assert.Contains(t, m.uiNotifications[0].message, "unknown TUI theme")
	assert.Equal(t, DefaultThemeName, m.themeSelection)
}

func TestMixedCaseThemeRecipeSlashCommandIsForwardedToRunner(t *testing.T) {
	runner := &recordingRunner{conversationID: "conversation-done"}
	m := newThemeTestModel(t, Config{Theme: DefaultThemeName, Runner: runner})
	m.textarea.SetValue("/Theme")

	runCmd := m.submit()

	require.NotNil(t, runCmd)
	assert.Nil(t, m.activeUIPrompt)
	assert.True(t, m.running)
	assert.Nil(t, runCmd())
	_ = receiveRunMsg(t, m.runCh)
	_ = receiveRunMsg(t, m.runCh)
	assert.Equal(t, "/Theme", runner.req.Message)
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
}

func TestCustomThemeDefaultsToCatppuccinMochaBase(t *testing.T) {
	writeCustomTheme(t, "minimal.theme", `assistant: "#112233"`)

	theme, err := resolveTheme("minimal")
	require.NoError(t, err)

	assert.True(t, theme.Dark)
	assert.Equal(t, "#112233", theme.Assistant)
	assert.Equal(t, themes[DefaultThemeName].User, theme.User)
}

func TestCustomThemeValidation(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		wantErr  string
	}{
		{
			name: "unknown field",
			contents: `
base: catppuccin-mocha
assistant_typo: "#112233"
`,
			wantErr: "field assistant_typo not found",
		},
		{name: "invalid color", contents: `assistant: red`, wantErr: "must be a #rrggbb color"},
		{name: "unknown base", contents: `base: missing-theme`, wantErr: `unknown base theme "missing-theme"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writeCustomTheme(t, "broken.theme", tt.contents)

			err := ValidateThemeName("broken")

			require.Error(t, err)
			assert.ErrorContains(t, err, tt.wantErr)
			assert.NotContains(t, AvailableThemeNames(), "broken")
		})
	}
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

func newThemeTestModel(t *testing.T, config Config) *model {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	m := newModel(context.Background(), config)
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()
	return &m
}
