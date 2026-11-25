package tui

import (
	"bytes"
	"regexp"
	"strings"
	"sync"

	"charm.land/glamour/v2/ansi"
	glamourstyles "charm.land/glamour/v2/styles"
	"github.com/alecthomas/chroma/v2"
	chromastyles "github.com/alecthomas/chroma/v2/styles"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

const codeBlockThemePrefix = "kodelet-"

var codeBlockThemeRegistryMu sync.RWMutex

func (m *model) renderMarkdown(text string, width int, kind markdownKind) string {
	rendered, _ := m.renderMarkdownPreviews(text, width, kind, nil)
	return rendered
}

// renderMarkdownPreviews renders markdown with Kodelet image links resolved. With
// previewable set, each image in a top-level paragraph moves to a paragraph of
// its own, below a marker row for the returned image.
func (m *model) renderMarkdownPreviews(
	text string,
	width int,
	kind markdownKind,
	previewable func(shortCode string) bool,
) (string, []string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil
	}

	md := m.markdownRenderer(max(10, width), kind)
	rendered, previews, ok := renderMarkdownWithRenderer(md, []byte(text), imageLinkBase(m.serverURL), previewable)
	if !ok {
		return wrapText(text, width), nil
	}
	return strings.TrimSpace(rendered), previews
}

func renderMarkdownWithRenderer(
	md goldmark.Markdown,
	source []byte,
	imageBase string,
	previewable func(shortCode string) bool,
) (rendered string, previews []string, ok bool) {
	codeBlockThemeRegistryMu.RLock()
	defer codeBlockThemeRegistryMu.RUnlock()
	defer func() {
		if recover() != nil {
			rendered, previews, ok = "", nil, false
		}
	}()

	doc := md.Parser().Parse(text.NewReader(source))
	source = resolveLinks(doc, source, imageBase)
	if previewable != nil {
		source, previews = splitImagePreviews(doc.(*ast.Document), source, previewable)
	}
	var buf bytes.Buffer
	err := md.Renderer().Render(&buf, source, doc)
	return dropUnsafeHyperlinks(buf.String()), previews, err == nil
}

// maxHyperlinkData is the escape sequence data the terminal renderer keeps;
// see the parser pool in github.com/charmbracelet/x/ansi.
const maxHyperlinkData = 4 << 10

var hyperlinkPattern = regexp.MustCompile("\x1b\\]8;([^;\x07\x1b]*);([^\x07\x1b]*)(?:\x07|\x1b\\\\)")

// dropUnsafeHyperlinks removes hyperlinks that the terminal renderer would
// truncate or discard, so a click never opens a different URL. Their URLs
// remain visible as text. Wrapping re-emits each hyperlink through a parser
// that keeps maxHyperlinkData bytes, so one that fills it may already be cut.
func dropUnsafeHyperlinks(rendered string) string {
	if !strings.Contains(rendered, "\x1b]8;") {
		return rendered
	}
	return hyperlinkPattern.ReplaceAllStringFunc(rendered, func(sequence string) string {
		match := hyperlinkPattern.FindStringSubmatch(sequence)
		params, url := match[1], match[2]
		if url == "" || (!strings.Contains(url, ";") && len("8;"+params+";"+url) < maxHyperlinkData) {
			return sequence
		}
		return ""
	})
}

func (m *model) markdownRenderer(width int, kind markdownKind) goldmark.Markdown {
	if kind == markdownThought {
		if m.thoughtMarkdownRenderer == nil || m.thoughtMarkdownRendererWidth != width {
			m.thoughtMarkdownRenderer = newMarkdownRenderer(width, thoughtMarkdownStyle)
			m.thoughtMarkdownRendererWidth = width
		}
		return m.thoughtMarkdownRenderer
	}
	if m.assistantMarkdownRenderer == nil || m.assistantMarkdownRendererWidth != width {
		m.assistantMarkdownRenderer = newMarkdownRenderer(width, assistantMarkdownStyle)
		m.assistantMarkdownRendererWidth = width
	}
	return m.assistantMarkdownRenderer
}

// newMarkdownRenderer mirrors glamour.NewTermRenderer.
func newMarkdownRenderer(width int, style ansi.StyleConfig) goldmark.Markdown {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM, extension.DefinitionList),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	)
	terminal := ansi.NewRenderer(ansi.Options{
		WordWrap:         max(10, width),
		Styles:           style,
		PreserveNewLines: true,
	})
	md.SetRenderer(renderer.NewRenderer(renderer.WithNodeRenderers(util.Prioritized(terminal, 1000))))
	return md
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
