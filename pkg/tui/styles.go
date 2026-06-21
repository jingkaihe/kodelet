package tui

import (
	"sort"
	"strings"

	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/lipgloss"
	"github.com/pkg/errors"
)

const (
	DefaultThemeName            = "catppuccin-mocha"
	ansiResetSequence           = "\x1b[0m"
	ansiForegroundResetSequence = "\x1b[39m"
)

type tuiTheme struct {
	Name             string
	User             string
	Assistant        string
	Muted            string
	ThoughtHeader    string
	ThoughtBody      string
	ToolHeader       string
	ToolBody         string
	DiffAdded        string
	DiffRemoved      string
	Steering         string
	SteeringError    string
	InputBorder      string
	InputLabel       string
	InputPlaceholder string
	ComposerLabel    string
	ComposerFlow     string
	ComposerText     string
	ComposerCursor   string
	SlashCommand     slashCommandTheme
	ProfileColors    []string
	ProfileSelected  string
	UI               uiTheme
	Markdown         markdownTheme
}

type slashCommandTheme struct {
	Selected    string
	Command     string
	Description string
	Hint        string
	Error       string
}

type uiTheme struct {
	DialogBorder       string
	DialogTitle        string
	DialogBody         string
	DialogMuted        string
	DialogSelected     string
	DialogButton       string
	DialogCancel       string
	NotificationBorder string
	NotificationTitle  string
	NotificationBody   string
}

type markdownTheme struct {
	BlockQuote            string
	Heading               string
	HeadingPrimary        string
	HeadingMuted          string
	HorizontalRule        string
	Link                  string
	LinkText              string
	Image                 string
	ImageText             string
	Code                  string
	CodeBlock             string
	ChromaText            string
	ChromaError           string
	ChromaErrorBackground string
	ChromaComment         string
	ChromaCommentPreproc  string
	ChromaKeyword         string
	ChromaKeywordType     string
	ChromaOperator        string
	ChromaPunctuation     string
	ChromaName            string
	ChromaNameBuiltin     string
	ChromaNameTag         string
	ChromaNameAttribute   string
	ChromaNameDecorator   string
	ChromaNameFunction    string
	ChromaNumber          string
	ChromaString          string
	ChromaStringEscape    string
	ChromaGenericDeleted  string
	ChromaGenericInserted string
	ChromaGenericHeading  string
}

var themes = map[string]tuiTheme{
	DefaultThemeName: {
		Name:             DefaultThemeName,
		User:             "#a6e3a1", // green
		Assistant:        "#cdd6f4", // text
		Muted:            "#7f849c", // overlay1
		ThoughtHeader:    "#f9e2af", // yellow
		ThoughtBody:      "#9399b2", // overlay2
		ToolHeader:       "#94e2d5", // teal
		ToolBody:         "#a6adc8", // subtext0
		DiffAdded:        "#a6e3a1", // green
		DiffRemoved:      "#f38ba8", // red
		Steering:         "#cba6f7", // mauve
		SteeringError:    "#f38ba8", // red
		InputBorder:      "#cdd6f4", // text
		InputLabel:       "#cdd6f4", // text
		InputPlaceholder: "#9399b2", // overlay2
		ComposerLabel:    "#9399b2", // overlay2
		ComposerFlow:     "#89b4fa", // blue
		ComposerText:     "#cdd6f4", // text
		ComposerCursor:   "#cdd6f4", // text
		SlashCommand: slashCommandTheme{
			Selected:    "#313244", // surface0
			Command:     "#94e2d5", // teal (matches inline code)
			Description: "#9399b2", // overlay2
			Hint:        "#9399b2", // overlay2
			Error:       "#f38ba8", // red
		},
		ProfileColors: []string{
			"#89b4fa", // blue
			"#a6e3a1", // green
			"#f9e2af", // yellow
			"#cba6f7", // mauve
			"#94e2d5", // teal
			"#fab387", // peach
			"#f38ba8", // red
			"#b4befe", // lavender
		},
		ProfileSelected: "#313244", // surface0
		UI: uiTheme{
			DialogBorder:       "#cdd6f4", // text, matches input border
			DialogTitle:        "#94e2d5", // teal
			DialogBody:         "#cdd6f4", // text
			DialogMuted:        "#9399b2", // overlay2
			DialogSelected:     "#313244", // surface0
			DialogButton:       "#a6e3a1", // green
			DialogCancel:       "#fab387", // peach
			NotificationBorder: "#89b4fa", // blue
			NotificationTitle:  "#94e2d5", // teal
			NotificationBody:   "#cdd6f4", // text
		},
		Markdown: markdownTheme{
			BlockQuote:            "#7f849c", // overlay1
			Heading:               "#b4befe", // lavender
			HeadingPrimary:        "#cba6f7", // mauve
			HeadingMuted:          "#7f849c", // overlay1
			HorizontalRule:        "#45475a", // surface1
			Link:                  "#89b4fa", // blue
			LinkText:              "#94e2d5", // teal
			Image:                 "#89b4fa", // blue
			ImageText:             "#94e2d5", // teal
			Code:                  "#94e2d5", // teal
			CodeBlock:             "#bac2de", // subtext1
			ChromaText:            "#cdd6f4", // text
			ChromaError:           "#f38ba8", // red
			ChromaErrorBackground: "#45475a", // surface1
			ChromaComment:         "#7f849c", // overlay1
			ChromaCommentPreproc:  "#94e2d5", // teal
			ChromaKeyword:         "#cba6f7", // mauve
			ChromaKeywordType:     "#94e2d5", // teal
			ChromaOperator:        "#cba6f7", // mauve
			ChromaPunctuation:     "#9399b2", // overlay2
			ChromaName:            "#cdd6f4", // text
			ChromaNameBuiltin:     "#94e2d5", // teal
			ChromaNameTag:         "#cba6f7", // mauve
			ChromaNameAttribute:   "#94e2d5", // teal
			ChromaNameDecorator:   "#94e2d5", // teal
			ChromaNameFunction:    "#89b4fa", // blue
			ChromaNumber:          "#fab387", // peach
			ChromaString:          "#a6e3a1", // green
			ChromaStringEscape:    "#94e2d5", // teal
			ChromaGenericDeleted:  "#f38ba8", // red
			ChromaGenericInserted: "#a6e3a1", // green
			ChromaGenericHeading:  "#b4befe", // lavender
		},
	},
	"tokyo-night": {
		Name:             "tokyo-night",
		User:             "#87d787",
		Assistant:        "#d0d0d0",
		Muted:            "#8a8a8a",
		ThoughtHeader:    "#ffffaf",
		ThoughtBody:      "#808080",
		ToolHeader:       "#afd7af",
		ToolBody:         "#949494",
		DiffAdded:        "#87d787",
		DiffRemoved:      "#ff5f5f",
		Steering:         "#d7afff",
		SteeringError:    "#ff5f5f",
		InputBorder:      "#afafff",
		InputLabel:       "#afafff",
		InputPlaceholder: "#585858",
		ComposerLabel:    "#808080",
		ComposerFlow:     "#afafff",
		ComposerText:     "#d0d0d0",
		ComposerCursor:   "#ffffaf",
		SlashCommand: slashCommandTheme{
			Selected:    "#303030",
			Command:     "#d7afff",
			Description: "#808080",
			Hint:        "#808080",
			Error:       "#ff5f5f",
		},
		ProfileColors: []string{
			"#afafff",
			"#afd7af",
			"#ffffaf",
			"#d7afff",
			"#d7d7af",
			"#ff5f5f",
			"#87d787",
			"#d0d0d0",
		},
		ProfileSelected: "#444444",
		UI: uiTheme{
			DialogBorder:       "#afafff",
			DialogTitle:        "#d7afff",
			DialogBody:         "#d0d0d0",
			DialogMuted:        "#808080",
			DialogSelected:     "#303030",
			DialogButton:       "#87d787",
			DialogCancel:       "#ff5f5f",
			NotificationBorder: "#afd7af",
			NotificationTitle:  "#ffffaf",
			NotificationBody:   "#d0d0d0",
		},
		Markdown: markdownTheme{
			BlockQuote:            "#8a8a8a",
			Heading:               "#afafff",
			HeadingPrimary:        "#d7afff",
			HeadingMuted:          "#8a8a8a",
			HorizontalRule:        "#585858",
			Link:                  "#afafff",
			LinkText:              "#afd7af",
			Image:                 "#afafff",
			ImageText:             "#afd7af",
			Code:                  "#afd7af",
			CodeBlock:             "#a8a8a8",
			ChromaText:            "#d0d0d0",
			ChromaError:           "#d0d0d0",
			ChromaErrorBackground: "#585858",
			ChromaComment:         "#808080",
			ChromaCommentPreproc:  "#afd7af",
			ChromaKeyword:         "#afafff",
			ChromaKeywordType:     "#afd7af",
			ChromaOperator:        "#afafff",
			ChromaPunctuation:     "#8a8a8a",
			ChromaName:            "#d0d0d0",
			ChromaNameBuiltin:     "#afd7af",
			ChromaNameTag:         "#afafff",
			ChromaNameAttribute:   "#afd7af",
			ChromaNameDecorator:   "#afd7af",
			ChromaNameFunction:    "#afd7af",
			ChromaNumber:          "#d7afff",
			ChromaString:          "#d7d7af",
			ChromaStringEscape:    "#afd7af",
			ChromaGenericDeleted:  "#d7afff",
			ChromaGenericInserted: "#afd7af",
			ChromaGenericHeading:  "#afafff",
		},
	},
}

var (
	userStyle      lipgloss.Style
	assistantStyle lipgloss.Style
	mutedStyle     lipgloss.Style

	assistantMarkdownStyle ansi.StyleConfig
	thoughtMarkdownStyle   ansi.StyleConfig

	thoughtHeaderStyle lipgloss.Style
	thoughtBodyStyle   lipgloss.Style
	toolHeaderStyle    lipgloss.Style
	toolBodyStyle      lipgloss.Style
	diffAddedStyle     lipgloss.Style
	diffRemovedStyle   lipgloss.Style
	steeringStyle      lipgloss.Style
	steeringErrorStyle lipgloss.Style

	inputBorderStyle             lipgloss.Style
	inputLabelStyle              lipgloss.Style
	inputPlaceholderStyle        lipgloss.Style
	composerLabelStyle           lipgloss.Style
	composerFlowStyle            lipgloss.Style
	composerTextStyle            lipgloss.Style
	composerCursorStyle          lipgloss.Style
	slashCommandSelectedStyle    lipgloss.Style
	slashCommandNameStyle        lipgloss.Style
	slashCommandDescriptionStyle lipgloss.Style
	slashCommandErrorStyle       lipgloss.Style
	uiDialogBorderStyle          lipgloss.Style
	uiDialogTitleStyle           lipgloss.Style
	uiDialogBodyStyle            lipgloss.Style
	uiDialogMutedStyle           lipgloss.Style
	uiDialogSelectedStyle        lipgloss.Style
	uiDialogButtonStyle          lipgloss.Style
	uiDialogCancelStyle          lipgloss.Style
	uiNotificationBorderStyle    lipgloss.Style
	uiNotificationTitleStyle     lipgloss.Style
	uiNotificationBodyStyle      lipgloss.Style
)

func init() {
	applyTheme(themes[DefaultThemeName])
}

func AvailableThemeNames() []string {
	names := make([]string, 0, len(themes))
	for name := range themes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func ValidateThemeName(name string) error {
	if _, ok := themeByName(name); ok {
		return nil
	}
	return errors.Errorf("unknown TUI theme %q (available: %s)", strings.TrimSpace(name), strings.Join(AvailableThemeNames(), ", "))
}

func themeByName(name string) (tuiTheme, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		name = DefaultThemeName
	}
	theme, ok := themes[name]
	return theme, ok
}

func applyTheme(theme tuiTheme) {
	userStyle = lipgloss.NewStyle().Foreground(themeColor(theme.User)).Italic(true)
	assistantStyle = lipgloss.NewStyle().Foreground(themeColor(theme.Assistant))
	mutedStyle = lipgloss.NewStyle().Foreground(themeColor(theme.Muted))

	assistantMarkdownStyle = compactMarkdownStyle(theme.Markdown)
	thoughtMarkdownStyle = compactMarkdownStyle(theme.Markdown)

	thoughtHeaderStyle = lipgloss.NewStyle().Foreground(themeColor(theme.ThoughtHeader))
	thoughtBodyStyle = lipgloss.NewStyle().Foreground(themeColor(theme.ThoughtBody)).Italic(true)
	toolHeaderStyle = lipgloss.NewStyle().Foreground(themeColor(theme.ToolHeader))
	toolBodyStyle = lipgloss.NewStyle().Foreground(themeColor(theme.ToolBody))
	diffAddedStyle = lipgloss.NewStyle().Foreground(themeColor(theme.DiffAdded))
	diffRemovedStyle = lipgloss.NewStyle().Foreground(themeColor(theme.DiffRemoved))
	steeringStyle = lipgloss.NewStyle().Foreground(themeColor(theme.Steering)).Italic(true)
	steeringErrorStyle = lipgloss.NewStyle().Foreground(themeColor(theme.SteeringError))

	inputBorderStyle = lipgloss.NewStyle().Foreground(themeColor(theme.InputBorder))
	inputLabelStyle = lipgloss.NewStyle().Foreground(themeColor(theme.InputLabel))
	inputPlaceholderStyle = lipgloss.NewStyle().Foreground(themeColor(theme.InputPlaceholder))
	composerLabelStyle = lipgloss.NewStyle().Foreground(themeColor(theme.ComposerLabel))
	composerFlowStyle = lipgloss.NewStyle().Foreground(themeColor(theme.ComposerFlow))
	composerTextStyle = lipgloss.NewStyle().Foreground(themeColor(theme.ComposerText))
	composerCursorStyle = lipgloss.NewStyle().Foreground(themeColor(theme.ComposerCursor))
	slashCommandSelectedStyle = lipgloss.NewStyle().Background(themeColor(theme.SlashCommand.Selected))
	slashCommandNameStyle = lipgloss.NewStyle().Foreground(themeColor(theme.SlashCommand.Command))
	slashCommandDescriptionStyle = lipgloss.NewStyle().Foreground(themeColor(theme.SlashCommand.Description))
	slashCommandErrorStyle = lipgloss.NewStyle().Foreground(themeColor(theme.SlashCommand.Error))
	uiDialogBorderStyle = lipgloss.NewStyle().Foreground(themeColor(theme.UI.DialogBorder))
	uiDialogTitleStyle = lipgloss.NewStyle().Foreground(themeColor(theme.UI.DialogTitle)).Bold(true)
	uiDialogBodyStyle = lipgloss.NewStyle().Foreground(themeColor(theme.UI.DialogBody))
	uiDialogMutedStyle = lipgloss.NewStyle().Foreground(themeColor(theme.UI.DialogMuted))
	uiDialogSelectedStyle = lipgloss.NewStyle().Foreground(themeColor(theme.UI.DialogBody)).Background(themeColor(theme.UI.DialogSelected))
	uiDialogButtonStyle = lipgloss.NewStyle().Foreground(themeColor(theme.UI.DialogButton))
	uiDialogCancelStyle = lipgloss.NewStyle().Foreground(themeColor(theme.UI.DialogCancel))
	uiNotificationBorderStyle = lipgloss.NewStyle().Foreground(themeColor(theme.UI.NotificationBorder))
	uiNotificationTitleStyle = lipgloss.NewStyle().Foreground(themeColor(theme.UI.NotificationTitle)).Bold(true)
	uiNotificationBodyStyle = lipgloss.NewStyle().Foreground(themeColor(theme.UI.NotificationBody))
}

func themeColor(color string) lipgloss.Color {
	return lipgloss.Color(color)
}

func renderPersistentStyle(style lipgloss.Style, text string) string {
	rendered := style.Render(text)
	start, end := styleSequences(style)
	if text == "" || start == "" || end == "" {
		return rendered
	}

	return reapplyStyleAfterResets(rendered, start, end)
}

func reapplyStyleAfterResets(rendered, start, end string) string {
	lines := strings.SplitAfter(rendered, "\n")
	for i, line := range lines {
		newline := ""
		if strings.HasSuffix(line, "\n") {
			line = strings.TrimSuffix(line, "\n")
			newline = "\n"
		}

		if strings.HasSuffix(line, end) {
			line = strings.TrimSuffix(line, end)
			line = reapplyStyleAfterInnerResets(line, start)
			lines[i] = line + end + newline
			continue
		}

		lines[i] = reapplyStyleAfterInnerResets(line, start) + newline
	}
	return strings.Join(lines, "")
}

func reapplyStyleAfterInnerResets(text, start string) string {
	text = strings.ReplaceAll(text, ansiResetSequence, ansiResetSequence+start)
	return strings.ReplaceAll(text, ansiForegroundResetSequence, ansiForegroundResetSequence+start)
}

func styleSequences(style lipgloss.Style) (start, end string) {
	empty := style.Render("")
	if strings.HasSuffix(empty, ansiResetSequence) {
		return strings.TrimSuffix(empty, ansiResetSequence), ansiResetSequence
	}
	if strings.HasSuffix(empty, ansiForegroundResetSequence) {
		return strings.TrimSuffix(empty, ansiForegroundResetSequence), ansiForegroundResetSequence
	}
	return "", ""
}
