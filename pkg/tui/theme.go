package tui

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jingkaihe/kodelet/pkg/slashcommands"
	"github.com/pkg/errors"
	"gopkg.in/yaml.v3"
)

const (
	customThemeExtension = ".theme"
	currentThemeSuffix   = " (current)"
)

type customThemeFile struct {
	Base  string   `yaml:"base"`
	Theme tuiTheme `yaml:",inline"`
}

type themeRegistry struct {
	themes       map[string]tuiTheme
	invalid      map[string]error
	discoveryErr error
}

// AvailableThemeNames returns automatic selection, bundled themes, and valid
// custom themes discovered under ~/.kodelet/themes.
func AvailableThemeNames() []string {
	return availableThemeNames(discoverThemes())
}

func availableThemeNames(registry themeRegistry) []string {
	names := make([]string, 0, len(registry.themes)+1)
	names = append(names, AutoThemeName)
	for name := range registry.themes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ValidateThemeName checks automatic, bundled, and user-installed themes.
func ValidateThemeName(name string) error {
	_, err := resolveTheme(name)
	return err
}

func resolveTheme(name string) (tuiTheme, error) {
	requestedName := normalizeThemeName(name)
	if requestedName == "" {
		requestedName = AutoThemeName
	}

	registry := discoverThemes()
	resolvedName := requestedName
	if requestedName == AutoThemeName {
		resolvedName = automaticThemeName(lipgloss.HasDarkBackground())
	}
	if theme, ok := registry.themes[resolvedName]; ok {
		return cloneTheme(theme), nil
	}
	if err, ok := registry.invalid[requestedName]; ok {
		return tuiTheme{}, errors.Wrapf(err, "failed to load custom TUI theme %q", requestedName)
	}
	if registry.discoveryErr != nil {
		return tuiTheme{}, errors.Wrap(registry.discoveryErr, "failed to discover custom TUI themes")
	}

	return tuiTheme{}, errors.Errorf(
		"unknown TUI theme %q (available: %s; custom themes: ~/.kodelet/themes/*%s)",
		strings.TrimSpace(name),
		strings.Join(availableThemeNames(registry), ", "),
		customThemeExtension,
	)
}

func automaticThemeName(hasDarkBackground bool) string {
	if hasDarkBackground {
		return DefaultThemeName
	}
	return LightThemeName
}

func normalizeThemeName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func normalizedThemeSelection(name string) string {
	name = normalizeThemeName(name)
	if name == "" {
		return AutoThemeName
	}
	return name
}

func tuiBuiltInSlashCommands() []slashcommands.Command {
	return []slashcommands.Command{{
		Name:        "theme",
		Description: "Select the TUI theme",
		Hint:        "name (optional)",
		Placeholder: "/theme [name]",
	}}
}

func withTUIBuiltInSlashCommands(commands []slashcommands.Command) []slashcommands.Command {
	builtIns := mergeSlashCommands(slashcommands.BuiltIns(), tuiBuiltInSlashCommands())
	return mergeSlashCommands(builtIns, commands)
}

func (m *model) handleLocalSlashCommand(message string) (tea.Cmd, bool) {
	command, args, found := slashcommands.Parse(message)
	if !found || command != "theme" {
		return nil, false
	}

	m.textarea.Reset()
	m.dismissSlashCommandSuggestions()
	if name := strings.TrimSpace(args); name != "" {
		cmd, err := m.setThemeSelection(name)
		if err != nil {
			return m.addUINotification(uiNotification{
				level:   uiNotificationError,
				title:   "Theme unavailable",
				message: err.Error(),
			}), true
		}
		return cmd, true
	}

	return m.openThemePicker(), true
}

func (m *model) openThemePicker() tea.Cmd {
	labels, values, selected := themePickerOptions(m.themeSelection)
	return m.openUIPrompt(uiPromptState{
		mode:             uiPromptSelect,
		origin:           uiPromptTheme,
		title:            "Select Theme",
		message:          "Choose a theme for this TUI session.",
		helpText:         "Auto follows the terminal's light or dark profile.",
		options:          labels,
		optionValues:     values,
		selectIndex:      selected,
		submitButtonText: "Apply",
	})
}

func themePickerOptions(current string) (labels []string, values []string, selected int) {
	current = normalizedThemeSelection(current)
	values = AvailableThemeNames()
	labels = make([]string, 0, len(values))
	selected = 0
	for index, name := range values {
		label := name
		if name == current {
			label += currentThemeSuffix
			selected = index
		}
		labels = append(labels, label)
	}
	return labels, values, selected
}

func (m *model) setThemeSelection(name string) (tea.Cmd, error) {
	selection := normalizedThemeSelection(name)
	theme, err := resolveTheme(selection)
	if err != nil {
		return nil, err
	}

	applyTheme(theme)
	m.theme = theme
	m.themeSelection = selection
	cmd := applyThemeToTextarea(&m.textarea)
	m.assistantMarkdownRenderer = nil
	m.assistantMarkdownRendererWidth = 0
	m.thoughtMarkdownRenderer = nil
	m.thoughtMarkdownRendererWidth = 0
	m.status = "ready"
	m.refreshViewport(false)
	return cmd, nil
}

func applyThemeToTextarea(input *textarea.Model) tea.Cmd {
	if input == nil {
		return nil
	}
	focused := input.Focused()
	input.FocusedStyle.Base = composerTextStyle
	input.FocusedStyle.CursorLine = composerTextStyle
	input.FocusedStyle.Placeholder = inputPlaceholderStyle
	input.FocusedStyle.Text = composerTextStyle
	input.FocusedStyle.EndOfBuffer = composerTextStyle
	input.FocusedStyle.Prompt = composerTextStyle
	input.BlurredStyle.Base = composerTextStyle
	input.BlurredStyle.CursorLine = composerTextStyle
	input.BlurredStyle.Placeholder = inputPlaceholderStyle
	input.BlurredStyle.Text = composerTextStyle
	input.BlurredStyle.EndOfBuffer = composerTextStyle
	input.BlurredStyle.Prompt = composerTextStyle
	input.Cursor.Style = composerCursorStyle
	input.Cursor.TextStyle = composerTextStyle

	// Bubbles keeps a private pointer to the active style. Rebind it after
	// replacing the exported styles so copied textarea models render the new
	// theme immediately.
	if focused {
		return input.Focus()
	}
	input.Blur()
	return nil
}

func discoverThemes() themeRegistry {
	registry := themeRegistry{
		themes:  make(map[string]tuiTheme, len(themes)),
		invalid: make(map[string]error),
	}
	for name, theme := range themes {
		registry.themes[name] = cloneTheme(theme)
	}

	themesDir, err := customThemesDir()
	if err != nil {
		registry.discoveryErr = err
		return registry
	}
	entries, err := os.ReadDir(themesDir)
	if err != nil {
		if !os.IsNotExist(err) {
			registry.discoveryErr = errors.Wrapf(err, "failed to read %s", themesDir)
		}
		return registry
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), customThemeExtension) {
			continue
		}
		name := normalizeThemeName(strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())))
		if name == "" || name == AutoThemeName {
			continue
		}
		if _, bundled := themes[name]; bundled {
			continue
		}
		if _, loaded := registry.themes[name]; loaded {
			continue
		}

		path := filepath.Join(themesDir, entry.Name())
		theme, err := loadCustomTheme(path, name)
		if err != nil {
			registry.invalid[name] = errors.Wrapf(err, "%s is invalid", path)
			continue
		}
		registry.themes[name] = theme
	}

	return registry
}

func customThemesDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", errors.Wrap(err, "failed to resolve home directory")
	}
	return filepath.Join(homeDir, ".kodelet", "themes"), nil
}

func loadCustomTheme(path, name string) (tuiTheme, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return tuiTheme{}, errors.Wrap(err, "failed to read theme file")
	}

	var header struct {
		Base string `yaml:"base"`
	}
	if err := yaml.Unmarshal(data, &header); err != nil {
		return tuiTheme{}, errors.Wrap(err, "failed to parse theme file")
	}
	baseName := normalizeThemeName(header.Base)
	if baseName == "" {
		baseName = DefaultThemeName
	}
	baseTheme, ok := themes[baseName]
	if !ok {
		return tuiTheme{}, errors.Errorf("unknown base theme %q (available bundled bases: %s)", baseName, strings.Join(bundledThemeNames(), ", "))
	}

	file := customThemeFile{
		Base:  baseName,
		Theme: cloneTheme(baseTheme),
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&file); err != nil {
		return tuiTheme{}, errors.Wrap(err, "failed to parse theme file")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return tuiTheme{}, errors.New("theme file must contain exactly one YAML document")
		}
		return tuiTheme{}, errors.Wrap(err, "failed to parse trailing theme content")
	}

	file.Theme.Name = name
	if err := validateTheme(file.Theme); err != nil {
		return tuiTheme{}, err
	}
	return file.Theme, nil
}

func bundledThemeNames() []string {
	names := make([]string, 0, len(themes))
	for name := range themes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func cloneTheme(theme tuiTheme) tuiTheme {
	theme.ProfileColors = append([]string(nil), theme.ProfileColors...)
	return theme
}

func validateTheme(theme tuiTheme) error {
	if strings.TrimSpace(theme.Name) == "" {
		return errors.New("theme name must not be empty")
	}
	if len(theme.ProfileColors) == 0 {
		return errors.New("profile_colors must contain at least one color")
	}
	return validateThemeValue(reflect.ValueOf(theme), "theme")
}

func validateThemeValue(value reflect.Value, path string) error {
	switch value.Kind() {
	case reflect.Struct:
		typ := value.Type()
		for i := range value.NumField() {
			field := typ.Field(i)
			if field.Name == "Name" || field.Name == "Dark" {
				continue
			}
			if err := validateThemeValue(value.Field(i), path+"."+field.Name); err != nil {
				return err
			}
		}
	case reflect.Slice:
		for i := range value.Len() {
			if err := validateThemeValue(value.Index(i), path); err != nil {
				return err
			}
		}
	case reflect.String:
		color := value.String()
		if color == "" {
			return errors.Errorf("%s must not be empty", path)
		}
		if !isThemeHexColor(color) {
			return errors.Errorf("%s must be a #rrggbb color, got %q", path, color)
		}
	}
	return nil
}

func isThemeHexColor(value string) bool {
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
