package tui

import (
	"strings"

	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/lipgloss"
)

const (
	AutoThemeName               = "auto"
	DefaultThemeName            = "catppuccin-mocha"
	LightThemeName              = "catppuccin-latte"
	ansiResetSequence           = "\x1b[0m"
	ansiForegroundResetSequence = "\x1b[39m"
)

type tuiTheme struct {
	Name             string             `yaml:"-"`
	Dark             bool               `yaml:"dark"`
	User             string             `yaml:"user"`
	Assistant        string             `yaml:"assistant"`
	Muted            string             `yaml:"muted"`
	ThoughtHeader    string             `yaml:"thought_header"`
	ThoughtBody      string             `yaml:"thought_body"`
	ToolHeader       string             `yaml:"tool_header"`
	ToolBody         string             `yaml:"tool_body"`
	DiffAdded        string             `yaml:"diff_added"`
	DiffRemoved      string             `yaml:"diff_removed"`
	Steering         string             `yaml:"steering"`
	SteeringError    string             `yaml:"steering_error"`
	InputBorder      string             `yaml:"input_border"`
	InputLabel       string             `yaml:"input_label"`
	InputPlaceholder string             `yaml:"input_placeholder"`
	ComposerLabel    string             `yaml:"composer_label"`
	ComposerFlow     string             `yaml:"composer_flow"`
	ComposerText     string             `yaml:"composer_text"`
	ComposerCursor   string             `yaml:"composer_cursor"`
	SlashCommand     slashCommandTheme  `yaml:"slash_command"`
	HistorySearch    historySearchTheme `yaml:"history_search"`
	ProfileColors    []string           `yaml:"profile_colors"`
	ProfileSelected  string             `yaml:"profile_selected"`
	UI               uiTheme            `yaml:"ui"`
	Markdown         markdownTheme      `yaml:"markdown"`
}

type slashCommandTheme struct {
	Selected    string `yaml:"selected"`
	Command     string `yaml:"command"`
	Description string `yaml:"description"`
	Hint        string `yaml:"hint"`
	Error       string `yaml:"error"`
}

type historySearchTheme struct {
	Label string `yaml:"label"`
	Query string `yaml:"query"`
	Error string `yaml:"error"`
}

type uiTheme struct {
	DialogBorder              string `yaml:"dialog_border"`
	DialogTitle               string `yaml:"dialog_title"`
	DialogBody                string `yaml:"dialog_body"`
	DialogMuted               string `yaml:"dialog_muted"`
	DialogSelected            string `yaml:"dialog_selected"`
	DialogButton              string `yaml:"dialog_button"`
	DialogCancel              string `yaml:"dialog_cancel"`
	NotificationBorder        string `yaml:"notification_border"`
	NotificationTitle         string `yaml:"notification_title"`
	NotificationBody          string `yaml:"notification_body"`
	NotificationWarningBorder string `yaml:"notification_warning_border"`
	NotificationWarningTitle  string `yaml:"notification_warning_title"`
	NotificationErrorBorder   string `yaml:"notification_error_border"`
	NotificationErrorTitle    string `yaml:"notification_error_title"`
}

type markdownTheme struct {
	BlockQuote            string `yaml:"block_quote"`
	Heading               string `yaml:"heading"`
	HeadingPrimary        string `yaml:"heading_primary"`
	HeadingMuted          string `yaml:"heading_muted"`
	HorizontalRule        string `yaml:"horizontal_rule"`
	Link                  string `yaml:"link"`
	LinkText              string `yaml:"link_text"`
	Image                 string `yaml:"image"`
	ImageText             string `yaml:"image_text"`
	Code                  string `yaml:"code"`
	CodeBlock             string `yaml:"code_block"`
	ChromaText            string `yaml:"chroma_text"`
	ChromaError           string `yaml:"chroma_error"`
	ChromaErrorBackground string `yaml:"chroma_error_background"`
	ChromaComment         string `yaml:"chroma_comment"`
	ChromaCommentPreproc  string `yaml:"chroma_comment_preproc"`
	ChromaKeyword         string `yaml:"chroma_keyword"`
	ChromaKeywordType     string `yaml:"chroma_keyword_type"`
	ChromaOperator        string `yaml:"chroma_operator"`
	ChromaPunctuation     string `yaml:"chroma_punctuation"`
	ChromaName            string `yaml:"chroma_name"`
	ChromaNameBuiltin     string `yaml:"chroma_name_builtin"`
	ChromaNameTag         string `yaml:"chroma_name_tag"`
	ChromaNameAttribute   string `yaml:"chroma_name_attribute"`
	ChromaNameDecorator   string `yaml:"chroma_name_decorator"`
	ChromaNameFunction    string `yaml:"chroma_name_function"`
	ChromaNumber          string `yaml:"chroma_number"`
	ChromaString          string `yaml:"chroma_string"`
	ChromaStringEscape    string `yaml:"chroma_string_escape"`
	ChromaGenericDeleted  string `yaml:"chroma_generic_deleted"`
	ChromaGenericInserted string `yaml:"chroma_generic_inserted"`
	ChromaGenericHeading  string `yaml:"chroma_generic_heading"`
}

var themes = map[string]tuiTheme{
	DefaultThemeName: {
		Name:             DefaultThemeName,
		Dark:             true,
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
		HistorySearch: historySearchTheme{
			Label: "#9399b2", // overlay2
			Query: "#cdd6f4", // text
			Error: "#f38ba8", // red
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
			DialogBorder:              "#cdd6f4", // text, matches input border
			DialogTitle:               "#cdd6f4", // text, matches composer text
			DialogBody:                "#cdd6f4", // text
			DialogMuted:               "#9399b2", // overlay2
			DialogSelected:            "#313244", // surface0
			DialogButton:              "#a6e3a1", // green
			DialogCancel:              "#fab387", // peach
			NotificationBorder:        "#89b4fa", // blue
			NotificationTitle:         "#94e2d5", // teal
			NotificationBody:          "#cdd6f4", // text
			NotificationWarningBorder: "#fab387", // peach
			NotificationWarningTitle:  "#f9e2af", // yellow
			NotificationErrorBorder:   "#f38ba8", // red
			NotificationErrorTitle:    "#f38ba8", // red
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
	LightThemeName: {
		Name:             LightThemeName,
		Dark:             false,
		User:             "#40a02b", // green
		Assistant:        "#4c4f69", // text
		Muted:            "#8c8fa1", // overlay1
		ThoughtHeader:    "#df8e1d", // yellow
		ThoughtBody:      "#7c7f93", // overlay2
		ToolHeader:       "#179299", // teal
		ToolBody:         "#6c6f85", // subtext0
		DiffAdded:        "#40a02b", // green
		DiffRemoved:      "#d20f39", // red
		Steering:         "#8839ef", // mauve
		SteeringError:    "#d20f39", // red
		InputBorder:      "#4c4f69", // text
		InputLabel:       "#4c4f69", // text
		InputPlaceholder: "#7c7f93", // overlay2
		ComposerLabel:    "#7c7f93", // overlay2
		ComposerFlow:     "#1e66f5", // blue
		ComposerText:     "#4c4f69", // text
		ComposerCursor:   "#4c4f69", // text
		SlashCommand: slashCommandTheme{
			Selected:    "#ccd0da", // surface0
			Command:     "#179299", // teal (matches inline code)
			Description: "#7c7f93", // overlay2
			Hint:        "#7c7f93", // overlay2
			Error:       "#d20f39", // red
		},
		HistorySearch: historySearchTheme{
			Label: "#7c7f93", // overlay2
			Query: "#4c4f69", // text
			Error: "#d20f39", // red
		},
		ProfileColors: []string{
			"#1e66f5", // blue
			"#40a02b", // green
			"#df8e1d", // yellow
			"#8839ef", // mauve
			"#179299", // teal
			"#fe640b", // peach
			"#d20f39", // red
			"#7287fd", // lavender
		},
		ProfileSelected: "#ccd0da", // surface0
		UI: uiTheme{
			DialogBorder:              "#4c4f69", // text, matches input border
			DialogTitle:               "#4c4f69", // text, matches composer text
			DialogBody:                "#4c4f69", // text
			DialogMuted:               "#7c7f93", // overlay2
			DialogSelected:            "#ccd0da", // surface0
			DialogButton:              "#40a02b", // green
			DialogCancel:              "#fe640b", // peach
			NotificationBorder:        "#1e66f5", // blue
			NotificationTitle:         "#179299", // teal
			NotificationBody:          "#4c4f69", // text
			NotificationWarningBorder: "#fe640b", // peach
			NotificationWarningTitle:  "#df8e1d", // yellow
			NotificationErrorBorder:   "#d20f39", // red
			NotificationErrorTitle:    "#d20f39", // red
		},
		Markdown: markdownTheme{
			BlockQuote:            "#8c8fa1", // overlay1
			Heading:               "#7287fd", // lavender
			HeadingPrimary:        "#8839ef", // mauve
			HeadingMuted:          "#8c8fa1", // overlay1
			HorizontalRule:        "#bcc0cc", // surface1
			Link:                  "#1e66f5", // blue
			LinkText:              "#179299", // teal
			Image:                 "#1e66f5", // blue
			ImageText:             "#179299", // teal
			Code:                  "#179299", // teal
			CodeBlock:             "#5c5f77", // subtext1
			ChromaText:            "#4c4f69", // text
			ChromaError:           "#d20f39", // red
			ChromaErrorBackground: "#bcc0cc", // surface1
			ChromaComment:         "#8c8fa1", // overlay1
			ChromaCommentPreproc:  "#179299", // teal
			ChromaKeyword:         "#8839ef", // mauve
			ChromaKeywordType:     "#179299", // teal
			ChromaOperator:        "#8839ef", // mauve
			ChromaPunctuation:     "#7c7f93", // overlay2
			ChromaName:            "#4c4f69", // text
			ChromaNameBuiltin:     "#179299", // teal
			ChromaNameTag:         "#8839ef", // mauve
			ChromaNameAttribute:   "#179299", // teal
			ChromaNameDecorator:   "#179299", // teal
			ChromaNameFunction:    "#1e66f5", // blue
			ChromaNumber:          "#fe640b", // peach
			ChromaString:          "#40a02b", // green
			ChromaStringEscape:    "#179299", // teal
			ChromaGenericDeleted:  "#d20f39", // red
			ChromaGenericInserted: "#40a02b", // green
			ChromaGenericHeading:  "#7287fd", // lavender
		},
	},
	"tokyo-night": {
		Name:             "tokyo-night",
		Dark:             true,
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
		HistorySearch: historySearchTheme{
			Label: "#808080",
			Query: "#d0d0d0",
			Error: "#ff5f5f",
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
			DialogBorder:              "#afafff",
			DialogTitle:               "#d7afff",
			DialogBody:                "#d0d0d0",
			DialogMuted:               "#808080",
			DialogSelected:            "#303030",
			DialogButton:              "#87d787",
			DialogCancel:              "#ff5f5f",
			NotificationBorder:        "#afd7af",
			NotificationTitle:         "#ffffaf",
			NotificationBody:          "#d0d0d0",
			NotificationWarningBorder: "#ffffaf",
			NotificationWarningTitle:  "#ffffaf",
			NotificationErrorBorder:   "#ff5f5f",
			NotificationErrorTitle:    "#ff5f5f",
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

	inputBorderStyle                 lipgloss.Style
	inputLabelStyle                  lipgloss.Style
	inputPlaceholderStyle            lipgloss.Style
	composerLabelStyle               lipgloss.Style
	composerFlowStyle                lipgloss.Style
	composerTextStyle                lipgloss.Style
	composerCursorStyle              lipgloss.Style
	slashCommandSelectedStyle        lipgloss.Style
	slashCommandNameStyle            lipgloss.Style
	slashCommandDescriptionStyle     lipgloss.Style
	slashCommandErrorStyle           lipgloss.Style
	historySearchLabelStyle          lipgloss.Style
	historySearchQueryStyle          lipgloss.Style
	historySearchErrorStyle          lipgloss.Style
	uiDialogBorderStyle              lipgloss.Style
	uiDialogTitleStyle               lipgloss.Style
	uiDialogBodyStyle                lipgloss.Style
	uiDialogMutedStyle               lipgloss.Style
	uiDialogSelectedStyle            lipgloss.Style
	uiDialogButtonStyle              lipgloss.Style
	uiDialogCancelStyle              lipgloss.Style
	uiNotificationBorderStyle        lipgloss.Style
	uiNotificationTitleStyle         lipgloss.Style
	uiNotificationBodyStyle          lipgloss.Style
	uiNotificationWarningBorderStyle lipgloss.Style
	uiNotificationWarningTitleStyle  lipgloss.Style
	uiNotificationErrorBorderStyle   lipgloss.Style
	uiNotificationErrorTitleStyle    lipgloss.Style
)

func init() {
	applyTheme(themes[DefaultThemeName])
}

func applyTheme(theme tuiTheme) {
	userStyle = lipgloss.NewStyle().Foreground(themeColor(theme.User)).Italic(true)
	assistantStyle = lipgloss.NewStyle().Foreground(themeColor(theme.Assistant))
	mutedStyle = lipgloss.NewStyle().Foreground(themeColor(theme.Muted))

	assistantMarkdownStyle = compactMarkdownStyle(theme.Markdown, theme.Dark)
	thoughtMarkdownStyle = compactMarkdownStyle(theme.Markdown, theme.Dark)

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
	historySearchLabelStyle = lipgloss.NewStyle().Foreground(themeColor(theme.HistorySearch.Label))
	historySearchQueryStyle = lipgloss.NewStyle().Foreground(themeColor(theme.HistorySearch.Query))
	historySearchErrorStyle = lipgloss.NewStyle().Foreground(themeColor(theme.HistorySearch.Error))
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
	uiNotificationWarningBorderStyle = lipgloss.NewStyle().Foreground(themeColor(theme.UI.NotificationWarningBorder))
	uiNotificationWarningTitleStyle = lipgloss.NewStyle().Foreground(themeColor(theme.UI.NotificationWarningTitle)).Bold(true)
	uiNotificationErrorBorderStyle = lipgloss.NewStyle().Foreground(themeColor(theme.UI.NotificationErrorBorder))
	uiNotificationErrorTitleStyle = lipgloss.NewStyle().Foreground(themeColor(theme.UI.NotificationErrorTitle)).Bold(true)
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
