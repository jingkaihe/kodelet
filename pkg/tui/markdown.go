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

func compactMarkdownStyle(theme markdownTheme) ansi.StyleConfig {
	style := styles.DarkStyleConfig
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
		chroma := *style.CodeBlock.Chroma
		style.CodeBlock.Chroma = &chroma
		style.CodeBlock.Chroma.Text.Color = stringPtr(theme.ChromaText)
		style.CodeBlock.Chroma.Error.Color = stringPtr(theme.ChromaError)
		style.CodeBlock.Chroma.Error.BackgroundColor = stringPtr(theme.ChromaErrorBackground)
		style.CodeBlock.Chroma.Comment.Color = stringPtr(theme.ChromaComment)
		style.CodeBlock.Chroma.CommentPreproc.Color = stringPtr(theme.ChromaCommentPreproc)
		style.CodeBlock.Chroma.Keyword.Color = stringPtr(theme.ChromaKeyword)
		style.CodeBlock.Chroma.KeywordReserved.Color = stringPtr(theme.ChromaKeyword)
		style.CodeBlock.Chroma.KeywordNamespace.Color = stringPtr(theme.ChromaKeyword)
		style.CodeBlock.Chroma.KeywordType.Color = stringPtr(theme.ChromaKeywordType)
		style.CodeBlock.Chroma.Operator.Color = stringPtr(theme.ChromaOperator)
		style.CodeBlock.Chroma.Punctuation.Color = stringPtr(theme.ChromaPunctuation)
		style.CodeBlock.Chroma.Name.Color = stringPtr(theme.ChromaName)
		style.CodeBlock.Chroma.NameBuiltin.Color = stringPtr(theme.ChromaNameBuiltin)
		style.CodeBlock.Chroma.NameTag.Color = stringPtr(theme.ChromaNameTag)
		style.CodeBlock.Chroma.NameAttribute.Color = stringPtr(theme.ChromaNameAttribute)
		style.CodeBlock.Chroma.NameClass.Color = stringPtr(theme.ChromaName)
		style.CodeBlock.Chroma.NameClass.Underline = nil
		style.CodeBlock.Chroma.NameClass.Bold = nil
		style.CodeBlock.Chroma.NameDecorator.Color = stringPtr(theme.ChromaNameDecorator)
		style.CodeBlock.Chroma.NameFunction.Color = stringPtr(theme.ChromaNameFunction)
		style.CodeBlock.Chroma.LiteralNumber.Color = stringPtr(theme.ChromaNumber)
		style.CodeBlock.Chroma.LiteralString.Color = stringPtr(theme.ChromaString)
		style.CodeBlock.Chroma.LiteralStringEscape.Color = stringPtr(theme.ChromaStringEscape)
		style.CodeBlock.Chroma.GenericDeleted.Color = stringPtr(theme.ChromaGenericDeleted)
		style.CodeBlock.Chroma.GenericInserted.Color = stringPtr(theme.ChromaGenericInserted)
		style.CodeBlock.Chroma.GenericSubheading.Color = stringPtr(theme.ChromaGenericHeading)
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
