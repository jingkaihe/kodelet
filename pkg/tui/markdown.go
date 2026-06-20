package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
)

func (m *model) renderMarkdown(text string, width int, kind markdownKind) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	renderer, err := m.markdownRenderer(max(10, width), kind)
	if err != nil {
		return wrapText(text, width)
	}
	rendered, err := renderer.Render(text)
	if err != nil {
		return wrapText(text, width)
	}
	return strings.TrimSpace(rendered)
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

func compactMarkdownStyle() ansi.StyleConfig {
	style := styles.DarkStyleConfig
	style.Document.BlockPrefix = ""
	style.Document.BlockSuffix = ""
	style.Document.Color = nil
	style.Document.Margin = uintPtr(0)
	style.BlockQuote.Color = stringPtr("245")
	style.Paragraph.Margin = uintPtr(0)
	style.Heading.Color = stringPtr("147")
	style.Heading.Margin = uintPtr(0)
	style.H1.Margin = uintPtr(0)
	style.H1.Color = stringPtr("183")
	style.H1.BackgroundColor = nil
	style.H2.Color = stringPtr("147")
	style.H3.Color = stringPtr("147")
	style.H4.Color = stringPtr("147")
	style.H5.Color = stringPtr("147")
	style.H6.Color = stringPtr("245")
	style.HorizontalRule.Color = stringPtr("240")
	style.Link.Color = stringPtr("147")
	style.LinkText.Color = stringPtr("151")
	style.Image.Color = stringPtr("147")
	style.ImageText.Color = stringPtr("151")
	style.Code.Color = stringPtr("151")
	style.Code.BackgroundColor = nil
	style.H2.Margin = uintPtr(0)
	style.H3.Margin = uintPtr(0)
	style.H4.Margin = uintPtr(0)
	style.H5.Margin = uintPtr(0)
	style.H6.Margin = uintPtr(0)
	style.List.Margin = uintPtr(0)
	style.CodeBlock.Margin = uintPtr(0)
	style.CodeBlock.Color = stringPtr("248")
	if style.CodeBlock.Chroma != nil {
		chroma := *style.CodeBlock.Chroma
		style.CodeBlock.Chroma = &chroma
		style.CodeBlock.Chroma.Text.Color = stringPtr("252")
		style.CodeBlock.Chroma.Error.Color = stringPtr("252")
		style.CodeBlock.Chroma.Error.BackgroundColor = stringPtr("240")
		style.CodeBlock.Chroma.Comment.Color = stringPtr("244")
		style.CodeBlock.Chroma.CommentPreproc.Color = stringPtr("151")
		style.CodeBlock.Chroma.Keyword.Color = stringPtr("147")
		style.CodeBlock.Chroma.KeywordReserved.Color = stringPtr("147")
		style.CodeBlock.Chroma.KeywordNamespace.Color = stringPtr("147")
		style.CodeBlock.Chroma.KeywordType.Color = stringPtr("151")
		style.CodeBlock.Chroma.Operator.Color = stringPtr("147")
		style.CodeBlock.Chroma.Punctuation.Color = stringPtr("245")
		style.CodeBlock.Chroma.Name.Color = stringPtr("252")
		style.CodeBlock.Chroma.NameBuiltin.Color = stringPtr("151")
		style.CodeBlock.Chroma.NameTag.Color = stringPtr("147")
		style.CodeBlock.Chroma.NameAttribute.Color = stringPtr("151")
		style.CodeBlock.Chroma.NameClass.Color = stringPtr("252")
		style.CodeBlock.Chroma.NameClass.Underline = nil
		style.CodeBlock.Chroma.NameClass.Bold = nil
		style.CodeBlock.Chroma.NameDecorator.Color = stringPtr("151")
		style.CodeBlock.Chroma.NameFunction.Color = stringPtr("151")
		style.CodeBlock.Chroma.LiteralNumber.Color = stringPtr("183")
		style.CodeBlock.Chroma.LiteralString.Color = stringPtr("187")
		style.CodeBlock.Chroma.LiteralStringEscape.Color = stringPtr("151")
		style.CodeBlock.Chroma.GenericDeleted.Color = stringPtr("183")
		style.CodeBlock.Chroma.GenericInserted.Color = stringPtr("151")
		style.CodeBlock.Chroma.GenericSubheading.Color = stringPtr("147")
		style.CodeBlock.Chroma.Background.BackgroundColor = nil
	}
	return style
}

func uintPtr(value uint) *uint {
	return &value
}

func stringPtr(value string) *string {
	return &value
}
