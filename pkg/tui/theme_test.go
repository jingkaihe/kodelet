package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/lipgloss"
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
