package tui

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

var imageLinkBaseKey = parser.NewContextKey()

// imageLinkTransformer resolves relative Kodelet image links in link and image nodes.
type imageLinkTransformer struct{}

func (imageLinkTransformer) Transform(doc *ast.Document, _ text.Reader, pc parser.Context) {
	base, _ := pc.Get(imageLinkBaseKey).(string)
	if base == "" {
		return
	}
	resolve := func(destination []byte) []byte {
		shortCode, ok := strings.CutPrefix(string(destination), "/i/")
		if !ok || !validImageShortCode(shortCode) {
			return destination
		}
		return []byte(base + string(destination))
	}
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			switch node := node.(type) {
			case *ast.Link:
				node.Destination = resolve(node.Destination)
			case *ast.Image:
				node.Destination = resolve(node.Destination)
			}
		}
		return ast.WalkContinue, nil
	})
}

// imageLinkBase returns the base for relative image links, or "" if not HTTP.
func imageLinkBase(serverURL string) string {
	base, err := url.Parse(strings.TrimSpace(serverURL))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return ""
	}
	return (&url.URL{Scheme: base.Scheme, Host: base.Host, Path: strings.TrimRight(base.Path, "/")}).String()
}

const previewMarkerPrefix = "KODELETIMAGEPREVIEW"

func previewMarker(index int) string {
	return previewMarkerPrefix + strconv.Itoa(index)
}

// markdownPreviews replaces top-level, single-line image paragraphs with
// numbered markers and returns the images' short codes.
func markdownPreviews(md goldmark.Markdown, source []byte, previewable func(string) bool) ([]byte, []string) {
	doc := md.Parser().Parse(text.NewReader(source))
	var (
		edited   []byte
		previews []string
		copied   int
	)
	for node := doc.FirstChild(); node != nil; node = node.NextSibling() {
		paragraph, ok := node.(*ast.Paragraph)
		if !ok || paragraph.ChildCount() != 1 || paragraph.Lines().Len() != 1 {
			continue
		}
		image, ok := paragraph.FirstChild().(*ast.Image)
		if !ok {
			continue
		}
		shortCode, ok := imageLinkShortCode(string(image.Destination))
		if !ok || !previewable(shortCode) {
			continue
		}
		line := paragraph.Lines().At(0)
		edited = append(edited, source[copied:line.Start]...)
		edited = append(edited, previewMarker(len(previews))...)
		copied = line.Stop
		previews = append(previews, shortCode)
	}
	if previews == nil {
		return source, nil
	}
	return append(edited, source[copied:]...), previews
}

// kodeletShortCode matches the short codes the server issues for image links.
var kodeletShortCode = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)

// imageLinkShortCode returns the short code of a relative or absolute image link.
// Previews are fetched from the connected server, never the link's host.
func imageLinkShortCode(link string) (string, bool) {
	parsed, err := url.Parse(link)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", false
	}
	if !parsed.IsAbs() {
		shortCode, ok := strings.CutPrefix(parsed.Path, "/i/")
		return shortCode, ok && parsed.Host == "" && validImageShortCode(shortCode)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", false
	}
	index := strings.LastIndex(parsed.Path, "/i/")
	if index < 0 {
		return "", false
	}
	shortCode := parsed.Path[index+len("/i/"):]
	return shortCode, kodeletShortCode.MatchString(shortCode)
}
