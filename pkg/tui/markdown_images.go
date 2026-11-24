package tui

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// resolveLinks resolves relative Kodelet image links in link and image nodes
// against base. A link whose text is its own URL becomes an autolink, so the
// URL is shown once rather than as text and URL. It returns source with those
// URLs appended.
func resolveLinks(doc ast.Node, source []byte, base string) []byte {
	var links []*ast.Link
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			switch node := node.(type) {
			case *ast.Link:
				links = append(links, node)
			case *ast.Image:
				node.Destination = resolveImageLink(node.Destination, base)
			}
		}
		return ast.WalkContinue, nil
	})
	for _, link := range links {
		label, original := linkLabel(link, source), string(link.Destination)
		link.Destination = resolveImageLink(link.Destination, base)
		if label == "" || (label != original && label != string(link.Destination)) {
			continue
		}
		start := len(source)
		source = append(source, link.Destination...)
		autoLink := ast.NewAutoLink(ast.AutoLinkURL, ast.NewTextSegment(text.NewSegment(start, len(source))))
		link.Parent().ReplaceChild(link.Parent(), link, autoLink)
	}
	return source
}

// resolveImageLink resolves a relative Kodelet image link, keeping its query
// and fragment, such as ?download=1.
func resolveImageLink(destination []byte, base string) []byte {
	if base == "" {
		return destination
	}
	link, err := url.Parse(string(destination))
	if err != nil || link.Scheme != "" || link.Host != "" || link.User != nil {
		return destination
	}
	shortCode, ok := strings.CutPrefix(link.Path, "/i/")
	if !ok || !validImageShortCode(shortCode) {
		return destination
	}
	return []byte(base + string(destination))
}

// linkLabel returns a link's text if it is plain, or "".
func linkLabel(link *ast.Link, source []byte) string {
	var label strings.Builder
	for child := link.FirstChild(); child != nil; child = child.NextSibling() {
		text, ok := child.(*ast.Text)
		if !ok || text.SoftLineBreak() || text.HardLineBreak() {
			return ""
		}
		label.Write(text.Segment.Value(source))
	}
	return label.String()
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
