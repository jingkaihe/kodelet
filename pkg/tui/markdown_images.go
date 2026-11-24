package tui

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"

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

// splitImagePreviews moves each previewable image in a top-level paragraph into
// a paragraph of its own holding a numbered marker, keeping the text around it
// in separate paragraphs. It returns source with the markers' text appended and
// the images' short codes.
func splitImagePreviews(doc *ast.Document, source []byte, previewable func(string) bool) ([]byte, []string) {
	var previews []string
	for node := doc.FirstChild(); node != nil; {
		next := node.NextSibling()
		paragraph, ok := node.(*ast.Paragraph)
		for ok {
			image, shortCode := firstPreview(paragraph, previewable)
			if image == nil {
				if len(previews) > 0 && !trimParagraph(paragraph, source) {
					doc.RemoveChild(doc, paragraph)
				}
				break
			}
			before := ast.NewParagraph()
			for paragraph.FirstChild() != image {
				before.AppendChild(before, paragraph.FirstChild())
			}
			paragraph.RemoveChild(paragraph, image)
			if trimParagraph(before, source) {
				doc.InsertBefore(doc, paragraph, before)
			}
			start := len(source)
			source = append(source, previewMarker(len(previews))...)
			marker := ast.NewParagraph()
			marker.AppendChild(marker, ast.NewTextSegment(text.NewSegment(start, len(source))))
			doc.InsertBefore(doc, paragraph, marker)
			previews = append(previews, shortCode)
		}
		node = next
	}
	return source, previews
}

func firstPreview(paragraph *ast.Paragraph, previewable func(string) bool) (*ast.Image, string) {
	for child := paragraph.FirstChild(); child != nil; child = child.NextSibling() {
		if image, ok := child.(*ast.Image); ok {
			if shortCode, ok := imageLinkShortCode(string(image.Destination)); ok && previewable(shortCode) {
				return image, shortCode
			}
		}
	}
	return nil, ""
}

// trimParagraph drops the spaces and line breaks at the edges of a split
// paragraph, reporting whether anything is left.
func trimParagraph(paragraph *ast.Paragraph, source []byte) bool {
	for child, ok := paragraph.FirstChild().(*ast.Text); ok; child, ok = paragraph.FirstChild().(*ast.Text) {
		child.Segment = child.Segment.TrimLeftSpace(source)
		if child.Segment.Len() > 0 {
			break
		}
		paragraph.RemoveChild(paragraph, child)
	}
	for child, ok := paragraph.LastChild().(*ast.Text); ok; child, ok = paragraph.LastChild().(*ast.Text) {
		child.Segment = child.Segment.TrimRightSpace(source)
		child.SetSoftLineBreak(false)
		child.SetHardLineBreak(false)
		if child.Segment.Len() > 0 {
			break
		}
		paragraph.RemoveChild(paragraph, child)
	}
	return paragraph.HasChildren()
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
