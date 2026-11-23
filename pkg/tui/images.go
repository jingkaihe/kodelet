package tui

import (
	"context"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/jingkaihe/kodelet/pkg/tui/termimage"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

const (
	imagePreviewMaxCols = 64
	imagePreviewMaxRows = 30
	imageFetchTimeout   = 30 * time.Second
	// maxKittyUploads bounds the images kept in Kitty's memory. Images evicted
	// while off screen are uploaded again when scrolled back into view.
	maxKittyUploads = 32
)

// imageFetcher is implemented by daemon clients that serve image artifacts.
type imageFetcher interface {
	FetchImage(ctx context.Context, shortCode string) ([]byte, error)
}

type imagesChangedMsg struct{}

// tuiImages loads image attachments and previews them inline in the transcript.
type tuiImages struct {
	ctx    context.Context
	notify chan<- tea.Msg

	protocol termimage.Protocol
	output   *termimage.Output

	mu      sync.Mutex
	pending bool // awaiting the terminal's XTVERSION reply
	fetcher imageFetcher
	cell    termimage.CellSize
	ids     *termimage.IDAllocator
	sources map[string]*imageSource
	byID    map[uint32]*imageRendition
	frame   uint64
}

type imageSource struct {
	loading       bool
	err           error
	data          []byte
	width, height int
	// renditions holds the most recently requested sizes, newest first.
	renditions []*imageRendition
}

type imageRendition struct {
	id         uint32
	cols, rows int
	rendering  bool
	rendition  *termimage.Rendition
	err        error
	lines      []string
	lastSeen   uint64
}

func newTUIImages(ctx context.Context, notify chan<- tea.Msg) *tuiImages {
	return &tuiImages{ctx: ctx, notify: notify}
}

// enable turns on previews for a terminal protocol rendered through output.
func (s *tuiImages) enable(protocol termimage.Protocol, output *termimage.Output, cell termimage.CellSize) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.protocol, s.output, s.cell = protocol, output, cell
	s.ids = termimage.NewIDAllocator(protocol)
	s.sources = map[string]*imageSource{}
	s.byID = map[uint32]*imageRendition{}
}

// awaitTerminalVersion keeps output ready for images that are enabled once the
// terminal reports a supported name.
func (s *tuiImages) awaitTerminalVersion(output *termimage.Output, cell termimage.CellSize) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending, s.output, s.cell = true, output, cell
}

func (s *tuiImages) awaitingTerminalVersion() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pending
}

// terminalVersion enables images for a terminal identified by its XTVERSION
// reply, reporting whether it did.
func (s *tuiImages) terminalVersion(name string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	pending, output, cell := s.pending, s.output, s.cell
	s.pending = false
	s.mu.Unlock()
	protocol := termimage.FromTerminalVersion(name)
	if !pending || protocol == termimage.None {
		return false
	}
	output.SetProtocol(protocol)
	s.enable(protocol, output, cell)
	return true
}

func (s *tuiImages) setFetcher(runner any) {
	if s == nil {
		return
	}
	fetcher, _ := runner.(imageFetcher)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fetcher = fetcher
}

// refreshCellSize follows font size changes, which resize the terminal.
func (s *tuiImages) refreshCellSize() {
	if s == nil || s.output == nil {
		return
	}
	if cell, ok := termimage.QueryCellSize(s.output.Fd()); ok {
		s.mu.Lock()
		s.cell = cell
		s.mu.Unlock()
	}
}

func (s *tuiImages) enabled() bool {
	return s != nil && s.protocol != termimage.None && s.output != nil && s.fetcher != nil
}

func (s *tuiImages) protocolEnabled() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.protocol != termimage.None && s.output != nil
}

func (s *tuiImages) available() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enabled()
}

// lines returns the transcript rows previewing an image attachment, or nil when
// it cannot be previewed and only its link should be shown. ok is false when
// the rows explain why loading the image failed.
func (s *tuiImages) lines(attachment tooltypes.ToolAttachment, maxCols, maxRows int) (rows []string, ok bool) {
	if s == nil || attachment.Type != "image" || attachment.Error != "" || !validImageShortCode(attachment.ShortCode) {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.enabled() || maxCols <= 0 || maxRows <= 0 {
		return nil, false
	}

	source := s.sources[attachment.ShortCode]
	if source == nil {
		source = &imageSource{loading: true, width: attachment.Width, height: attachment.Height}
		s.sources[attachment.ShortCode] = source
		go s.load(attachment.ShortCode, source, s.fetcher)
	}
	if source.err != nil {
		return []string{mutedStyle.Render(imageUnavailableText(source.err, maxCols))}, false
	}
	cols, height := termimage.Fit(source.width, source.height, maxCols, maxRows, s.cell)
	if source.loading {
		return imageLoadingLines(height), true
	}

	rendition := s.renditionLocked(source, cols, height)
	switch {
	case rendition.err != nil:
		return []string{mutedStyle.Render(imageUnavailableText(rendition.err, maxCols))}, false
	case rendition.rendering:
		return imageLoadingLines(height), true
	}
	if rendition.lines == nil {
		rendition.lines = make([]string, rendition.rows)
		for row := range rendition.rows {
			rendition.lines[row] = termimage.PlaceholderLine(s.protocol, rendition.id, row, rendition.cols)
		}
	}
	return rendition.lines, true
}

// renditionLocked returns the rendition of source at cols x rows, starting to
// render it when needed. Only the two most recent sizes are kept.
func (s *tuiImages) renditionLocked(source *imageSource, cols, rows int) *imageRendition {
	for i, rendition := range source.renditions {
		if rendition.cols == cols && rendition.rows == rows {
			if i > 0 {
				source.renditions = slices.Insert(slices.Delete(source.renditions, i, i+1), 0, rendition)
			}
			return rendition
		}
	}
	rendition := &imageRendition{id: s.ids.Next(), cols: cols, rows: rows, rendering: true}
	s.byID[rendition.id] = rendition
	source.renditions = slices.Insert(source.renditions, 0, rendition)
	if len(source.renditions) > 2 {
		for _, stale := range source.renditions[2:] {
			delete(s.byID, stale.id)
			s.output.Release(stale.id)
		}
		source.renditions = source.renditions[:2]
	}
	go s.render(source.data, rendition, s.cell)
	return rendition
}

func (s *tuiImages) load(shortCode string, source *imageSource, fetcher imageFetcher) {
	ctx, cancel := context.WithTimeout(s.ctx, imageFetchTimeout)
	data, err := fetcher.FetchImage(ctx, shortCode)
	cancel()
	width, height := 0, 0
	if err == nil {
		width, height, err = termimage.Size(data)
	}
	s.mu.Lock()
	source.loading, source.err = false, err
	if err == nil {
		source.data, source.width, source.height = data, width, height
	}
	s.mu.Unlock()
	s.changed()
}

func (s *tuiImages) render(data []byte, rendition *imageRendition, cell termimage.CellSize) {
	picture, err := termimage.Decode(data)
	var rendered *termimage.Rendition
	if err == nil {
		rendered, err = termimage.Render(picture, rendition.cols, rendition.rows, cell)
	}
	s.mu.Lock()
	rendition.rendering, rendition.rendition, rendition.err = false, rendered, err
	s.mu.Unlock()
	s.changed()
}

func (s *tuiImages) changed() {
	select {
	case s.notify <- imagesChangedMsg{}:
	case <-s.ctx.Done():
	}
}

// compose finds the images in a rendered frame. Kitty images are uploaded when
// first visible; for iTerm2 the placeholders are blanked and the images drawn
// over them by the output.
func (s *tuiImages) compose(content string, height int) string {
	if s == nil || s.protocol == termimage.None || s.output == nil {
		return content
	}
	content, frame := termimage.ComposeFrame(s.protocol, content, height, s.imageCols)
	switch s.protocol {
	case termimage.Kitty:
		s.uploadVisible(frame.Visible)
	case termimage.ITerm2:
		s.output.Present(frame.Signature, s.drawer(frame.Placements))
	}
	return content
}

func (s *tuiImages) imageCols(id uint32) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rendition := s.byID[id]; rendition != nil {
		return rendition.cols
	}
	return 0
}

func (s *tuiImages) uploadVisible(visible []uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frame++
	uploaded := false
	for _, id := range visible {
		rendition := s.byID[id]
		if rendition == nil || rendition.rendition == nil {
			continue
		}
		rendition.lastSeen = s.frame
		if !s.output.Uploaded(id) {
			s.output.Upload(id, termimage.KittyUpload(id, rendition.rendition.PNG, rendition.cols, rendition.rows))
			uploaded = true
		}
	}
	if !uploaded {
		return
	}
	var resident []*imageRendition
	for id, rendition := range s.byID {
		if s.output.Uploaded(id) && rendition.lastSeen != s.frame {
			resident = append(resident, rendition)
		}
	}
	excess := len(resident) + len(visible) - maxKittyUploads
	if excess <= 0 {
		return
	}
	slices.SortFunc(resident, func(a, b *imageRendition) int { return int(a.lastSeen) - int(b.lastSeen) })
	for _, rendition := range resident[:min(excess, len(resident))] {
		s.output.Release(rendition.id)
	}
}

// drawer returns a function that draws the placements with iTerm2's protocol.
func (s *tuiImages) drawer(placements []termimage.Placement) func() string {
	s.mu.Lock()
	renditions := make([]*termimage.Rendition, len(placements))
	for i, placement := range placements {
		if rendition := s.byID[placement.ID]; rendition != nil {
			renditions[i] = rendition.rendition
		}
	}
	s.mu.Unlock()
	return func() string {
		var b strings.Builder
		for i, placement := range placements {
			if renditions[i] == nil {
				continue
			}
			data, err := renditions[i].Slice(placement.FirstRow, placement.Rows)
			if err != nil {
				continue
			}
			b.WriteString(ansi.CursorPosition(placement.X+1, placement.Y+1))
			b.WriteString(termimage.ITerm2Image(data, placement.Cols, placement.Rows))
		}
		return b.String()
	}
}

// renderTranscriptImages writes image previews indented like a tool body,
// each separated from the text above by a blank line.
func (m *model) renderTranscriptImages(b *strings.Builder, line *int, attachments []tooltypes.ToolAttachment, indent string) {
	if len(attachments) == 0 {
		return
	}
	maxCols, maxRows := m.imagePreviewLimits(len(indent) + 2)
	for _, attachment := range attachments {
		rows, _ := m.images.lines(attachment, maxCols, maxRows)
		if len(rows) == 0 {
			continue
		}
		b.WriteString("\n")
		*line++
		for _, row := range rows {
			b.WriteString(indent + "  " + row + "\n")
			*line++
		}
	}
}

func (m *model) imagePreviewLimits(indent int) (maxCols, maxRows int) {
	return min(imagePreviewMaxCols, m.transcriptTextWidth()-indent), min(imagePreviewMaxRows, max(4, m.height*3/5))
}

// renderAssistantText renders assistant markdown, replacing Kodelet images on
// their own line with previews. An image that cannot be loaded keeps its link.
func (m *model) renderAssistantText(b *strings.Builder, line *int, text string) {
	segments := []markdownSegment{{text: text}}
	if m.images.available() {
		segments = splitMarkdownImages(text)
	}
	maxCols, maxRows := m.imagePreviewLimits(0)
	wrote := false
	for _, segment := range segments {
		var rows []string
		if segment.shortCode != "" {
			attachment := tooltypes.ToolAttachment{Type: "image", ShortCode: segment.shortCode}
			if preview, ok := m.images.lines(attachment, maxCols, maxRows); ok {
				rows = preview
			}
		}
		if rows == nil {
			text := absoluteImageLinks(segment.text, m.serverURL)
			rendered := m.renderMarkdown(text, m.transcriptTextWidth(), markdownAssistant)
			if rendered == "" {
				continue
			}
			rows = strings.Split(renderPersistentStyle(assistantStyle, rendered), "\n")
		}
		// Separate paragraphs and images with a blank line, as markdown does.
		if wrote {
			b.WriteString("\n")
			*line++
		}
		for _, row := range rows {
			b.WriteString(row + "\n")
			*line++
		}
		wrote = true
	}
}

// markdownSegment is markdown text, or an image line previewing shortCode.
type markdownSegment struct {
	text      string
	shortCode string
}

var markdownImageLine = regexp.MustCompile(`^ {0,3}!\[[^\]]*\]\(\s*<?([^\s<>()]+)>?(?:\s+"[^"]*")?\s*\)\s*$`)

// splitMarkdownImages separates lines that are only an image linking to a
// Kodelet image, outside code fences, from the surrounding markdown.
func splitMarkdownImages(text string) []markdownSegment {
	if !strings.Contains(text, "![") {
		return []markdownSegment{{text: text}}
	}
	lines := strings.Split(text, "\n")
	images := map[int]string{}
	eachProseLine(lines, func(i int) {
		if match := markdownImageLine.FindStringSubmatch(lines[i]); match != nil {
			if shortCode, ok := imageLinkShortCode(match[1]); ok {
				images[i] = shortCode
			}
		}
	})
	if len(images) == 0 {
		return []markdownSegment{{text: text}}
	}

	var (
		segments []markdownSegment
		chunk    []string
	)
	flush := func() {
		if text := strings.Join(chunk, "\n"); strings.TrimSpace(text) != "" {
			segments = append(segments, markdownSegment{text: text})
		}
		chunk = nil
	}
	for i, line := range lines {
		if shortCode, ok := images[i]; ok {
			flush()
			segments = append(segments, markdownSegment{text: line, shortCode: shortCode})
			continue
		}
		chunk = append(chunk, line)
	}
	flush()
	return segments
}

var relativeImageLink = regexp.MustCompile(`(\]\(\s*<?)(/i/[A-Za-z0-9_-]+)`)

// absoluteImageLinks resolves relative Kodelet image links in markdown against
// the connected server, as tool image links are, so the terminal can open them.
func absoluteImageLinks(text, serverURL string) string {
	base, err := url.Parse(strings.TrimSpace(serverURL))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || !strings.Contains(text, "/i/") {
		return text
	}
	prefix := (&url.URL{Scheme: base.Scheme, Host: base.Host, Path: strings.TrimRight(base.Path, "/")}).String()
	lines := strings.Split(text, "\n")
	eachProseLine(lines, func(i int) {
		lines[i] = relativeImageLink.ReplaceAllStringFunc(lines[i], func(link string) string {
			match := relativeImageLink.FindStringSubmatch(link)
			return match[1] + prefix + match[2]
		})
	})
	return strings.Join(lines, "\n")
}

// eachProseLine calls visit with the index of each line outside code fences.
func eachProseLine(lines []string, visit func(int)) {
	fence := ""
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case fence != "":
			if strings.HasPrefix(trimmed, fence) {
				fence = ""
			}
		case strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~"):
			fence = trimmed[:3]
		default:
			visit(i)
		}
	}
}

// kodeletShortCode matches the short codes the server issues for image links.
var kodeletShortCode = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)

// imageLinkShortCode recognizes relative /i/<code> links and absolute ones
// whose host may be the server's public address. Previews are always fetched
// from the connected server, never from the link's host.
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

func imageLoadingLines(rows int) []string {
	lines := make([]string, max(1, rows))
	lines[0] = mutedStyle.Render("Loading image…")
	return lines
}

func imageUnavailableText(err error, width int) string {
	text := "Image preview unavailable"
	if reason := sanitizeExtensionUIText(err.Error()); reason != "" {
		text += ": " + reason
	}
	return fitVisiblePrefix(text, width)
}

func validImageShortCode(shortCode string) bool {
	if shortCode == "" || len(shortCode) > 128 {
		return false
	}
	return !strings.ContainsFunc(shortCode, func(ch rune) bool {
		return !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-", ch)
	})
}
