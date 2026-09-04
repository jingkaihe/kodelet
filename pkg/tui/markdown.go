package tui

import (
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	chromastyles "github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	glamourstyles "github.com/charmbracelet/glamour/styles"
)

const codeBlockThemePrefix = "kodelet-"

var codeBlockThemeRegistryMu sync.RWMutex

func (m *model) renderMarkdown(text string, width int, kind markdownKind) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	renderer, err := m.markdownRenderer(max(10, width), kind)
	if err != nil {
		return wrapText(text, width)
	}
	rendered, ok := renderMarkdownWithRenderer(renderer, text)
	if !ok {
		return wrapText(text, width)
	}
	return strings.TrimSpace(rendered)
}

func renderMarkdownWithRenderer(renderer *glamour.TermRenderer, text string) (rendered string, ok bool) {
	codeBlockThemeRegistryMu.RLock()
	defer codeBlockThemeRegistryMu.RUnlock()
	defer func() {
		if recover() != nil {
			rendered = ""
			ok = false
		}
	}()

	rendered, err := renderer.Render(text)
	return rendered, err == nil
}

func (m *model) markdownRenderer(width int, kind markdownKind) (*glamour.TermRenderer, error) {
	if kind == markdownThought {
		if m.thoughtMarkdownRenderer != nil && m.thoughtMarkdownRendererWidth == width {
			return m.thoughtMarkdownRenderer, nil
		}
		renderer, err := newMarkdownRenderer(width, thoughtMarkdownStyle)
		if err != nil {
			return nil, err
		}
		m.thoughtMarkdownRenderer = renderer
		m.thoughtMarkdownRendererWidth = width
		return renderer, nil
	}

	if m.assistantMarkdownRenderer != nil && m.assistantMarkdownRendererWidth == width {
		return m.assistantMarkdownRenderer, nil
	}
	renderer, err := newMarkdownRenderer(width, assistantMarkdownStyle)
	if err != nil {
		return nil, err
	}
	m.assistantMarkdownRenderer = renderer
	m.assistantMarkdownRendererWidth = width
	return renderer, nil
}

func newMarkdownRenderer(width int, style ansi.StyleConfig) (*glamour.TermRenderer, error) {
	return glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(max(10, width)),
		glamour.WithPreservedNewLines(),
	)
}

func compactMarkdownStyle(themeName string, theme markdownTheme, dark bool) ansi.StyleConfig {
	style := glamourstyles.LightStyleConfig
	if dark {
		style = glamourstyles.DarkStyleConfig
	}
	style.Document.BlockPrefix = ""
	style.Document.BlockSuffix = ""
	style.Document.Color = nil
	style.Document.Margin = uintPtr(0)
	style.BlockQuote.Color = stringPtr(theme.BlockQuote)
	style.Paragraph.Margin = uintPtr(0)
	style.Heading.Color = stringPtr(theme.Heading)
	style.Heading.Margin = uintPtr(0)
	style.H1.Margin = uintPtr(0)
	style.H1.Color = stringPtr(theme.HeadingPrimary)
	style.H1.BackgroundColor = nil
	style.H2.Color = stringPtr(theme.Heading)
	style.H3.Color = stringPtr(theme.Heading)
	style.H4.Color = stringPtr(theme.Heading)
	style.H5.Color = stringPtr(theme.Heading)
	style.H6.Color = stringPtr(theme.HeadingMuted)
	style.HorizontalRule.Color = stringPtr(theme.HorizontalRule)
	style.Link.Color = stringPtr(theme.Link)
	style.LinkText.Color = stringPtr(theme.LinkText)
	style.Image.Color = stringPtr(theme.Image)
	style.ImageText.Color = stringPtr(theme.ImageText)
	style.Code.Prefix = ""
	style.Code.Suffix = ""
	style.Code.Color = stringPtr(theme.Code)
	style.Code.BackgroundColor = nil
	style.H2.Margin = uintPtr(0)
	style.H3.Margin = uintPtr(0)
	style.H4.Margin = uintPtr(0)
	style.H5.Margin = uintPtr(0)
	style.H6.Margin = uintPtr(0)
	style.List.Margin = uintPtr(0)
	style.CodeBlock.Margin = uintPtr(0)
	style.CodeBlock.Color = stringPtr(theme.CodeBlock)
	if style.CodeBlock.Chroma != nil {
		chromaTheme := *style.CodeBlock.Chroma
		chromaTheme.Text.Color = stringPtr(theme.ChromaText)
		chromaTheme.Error.Color = stringPtr(theme.ChromaError)
		chromaTheme.Error.BackgroundColor = stringPtr(theme.ChromaErrorBackground)
		chromaTheme.Comment.Color = stringPtr(theme.ChromaComment)
		chromaTheme.CommentPreproc.Color = stringPtr(theme.ChromaCommentPreproc)
		chromaTheme.Keyword.Color = stringPtr(theme.ChromaKeyword)
		chromaTheme.KeywordReserved.Color = stringPtr(theme.ChromaKeyword)
		chromaTheme.KeywordNamespace.Color = stringPtr(theme.ChromaKeyword)
		chromaTheme.KeywordType.Color = stringPtr(theme.ChromaKeywordType)
		chromaTheme.Operator.Color = stringPtr(theme.ChromaOperator)
		chromaTheme.Punctuation.Color = stringPtr(theme.ChromaPunctuation)
		chromaTheme.Name.Color = stringPtr(theme.ChromaName)
		chromaTheme.NameBuiltin.Color = stringPtr(theme.ChromaNameBuiltin)
		chromaTheme.NameTag.Color = stringPtr(theme.ChromaNameTag)
		chromaTheme.NameAttribute.Color = stringPtr(theme.ChromaNameAttribute)
		chromaTheme.NameClass.Color = stringPtr(theme.ChromaName)
		chromaTheme.NameClass.Underline = nil
		chromaTheme.NameClass.Bold = nil
		chromaTheme.NameDecorator.Color = stringPtr(theme.ChromaNameDecorator)
		chromaTheme.NameFunction.Color = stringPtr(theme.ChromaNameFunction)
		chromaTheme.LiteralNumber.Color = stringPtr(theme.ChromaNumber)
		chromaTheme.LiteralString.Color = stringPtr(theme.ChromaString)
		chromaTheme.LiteralStringEscape.Color = stringPtr(theme.ChromaStringEscape)
		chromaTheme.GenericDeleted.Color = stringPtr(theme.ChromaGenericDeleted)
		chromaTheme.GenericInserted.Color = stringPtr(theme.ChromaGenericInserted)
		chromaTheme.GenericSubheading.Color = stringPtr(theme.ChromaGenericHeading)
		chromaTheme.Background.BackgroundColor = nil

		style.CodeBlock.Theme = registerCodeBlockTheme(themeName, chromaTheme)
		style.CodeBlock.Chroma = nil
	}
	return style
}

func registerCodeBlockTheme(themeName string, theme ansi.Chroma) string {
	name := codeBlockThemePrefix + themeName

	codeBlockThemeRegistryMu.Lock()
	defer codeBlockThemeRegistryMu.Unlock()
	if _, ok := chromastyles.Registry[name]; !ok {
		chromastyles.Register(chroma.MustNewStyle(name, chromaStyleEntries(theme)))
	}
	return name
}

func chromaStyleEntries(theme ansi.Chroma) chroma.StyleEntries {
	return chroma.StyleEntries{
		chroma.Text:                chromaStyleEntry(theme.Text),
		chroma.Error:               chromaStyleEntry(theme.Error),
		chroma.Comment:             chromaStyleEntry(theme.Comment),
		chroma.CommentPreproc:      chromaStyleEntry(theme.CommentPreproc),
		chroma.Keyword:             chromaStyleEntry(theme.Keyword),
		chroma.KeywordReserved:     chromaStyleEntry(theme.KeywordReserved),
		chroma.KeywordNamespace:    chromaStyleEntry(theme.KeywordNamespace),
		chroma.KeywordType:         chromaStyleEntry(theme.KeywordType),
		chroma.Operator:            chromaStyleEntry(theme.Operator),
		chroma.Punctuation:         chromaStyleEntry(theme.Punctuation),
		chroma.Name:                chromaStyleEntry(theme.Name),
		chroma.NameBuiltin:         chromaStyleEntry(theme.NameBuiltin),
		chroma.NameTag:             chromaStyleEntry(theme.NameTag),
		chroma.NameAttribute:       chromaStyleEntry(theme.NameAttribute),
		chroma.NameClass:           chromaStyleEntry(theme.NameClass),
		chroma.NameConstant:        chromaStyleEntry(theme.NameConstant),
		chroma.NameDecorator:       chromaStyleEntry(theme.NameDecorator),
		chroma.NameException:       chromaStyleEntry(theme.NameException),
		chroma.NameFunction:        chromaStyleEntry(theme.NameFunction),
		chroma.NameOther:           chromaStyleEntry(theme.NameOther),
		chroma.Literal:             chromaStyleEntry(theme.Literal),
		chroma.LiteralNumber:       chromaStyleEntry(theme.LiteralNumber),
		chroma.LiteralDate:         chromaStyleEntry(theme.LiteralDate),
		chroma.LiteralString:       chromaStyleEntry(theme.LiteralString),
		chroma.LiteralStringEscape: chromaStyleEntry(theme.LiteralStringEscape),
		chroma.GenericDeleted:      chromaStyleEntry(theme.GenericDeleted),
		chroma.GenericEmph:         chromaStyleEntry(theme.GenericEmph),
		chroma.GenericInserted:     chromaStyleEntry(theme.GenericInserted),
		chroma.GenericStrong:       chromaStyleEntry(theme.GenericStrong),
		chroma.GenericSubheading:   chromaStyleEntry(theme.GenericSubheading),
		chroma.Background:          chromaStyleEntry(theme.Background),
	}
}

func chromaStyleEntry(style ansi.StylePrimitive) string {
	parts := make([]string, 0, 5)
	if style.Color != nil {
		parts = append(parts, *style.Color)
	}
	if style.BackgroundColor != nil {
		parts = append(parts, "bg:"+*style.BackgroundColor)
	}
	if style.Italic != nil && *style.Italic {
		parts = append(parts, "italic")
	}
	if style.Bold != nil && *style.Bold {
		parts = append(parts, "bold")
	}
	if style.Underline != nil && *style.Underline {
		parts = append(parts, "underline")
	}
	return strings.Join(parts, " ")
}

func uintPtr(value uint) *uint {
	return &value
}

func stringPtr(value string) *string {
	return &value
}
