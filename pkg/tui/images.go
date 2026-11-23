package tui

import (
	"cmp"
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
	// imageCacheBytes bounds the downloaded and resized images kept for
	// images away from the viewport.
	imageCacheBytes = 64 << 20
	// Downloads run a few at a time. Decoding allocates a full-resolution
	// bitmap, so decodes run one at a time.
	maxImageFetches = 4
	maxImageRenders = 1
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

	// placed records where the latest transcript render put each image, so
	// only images near the viewport are loaded. placing collects the render
	// in progress.
	placed, placing []imagePlacement
	// near counts loadNear calls; the cache keeps what the latest one used.
	near       uint64
	cacheBytes int
	fetches    chan struct{}
	renders    chan struct{}
}

type imageSource struct {
	shortCode string
	loading   bool
	err       error
	// data is nil until downloaded and again once evicted. The size is kept,
	// so the image's rows stay reserved.
	data          []byte
	width, height int
	renditions    []*imageRendition
	near          uint64
}

type imageRendition struct {
	source     *imageSource
	id         uint32
	cols, rows int
	rendering  bool
	rendition  *termimage.Rendition
	err        error
	lines      []string
	lastSeen   uint64
	near       uint64
}

// imagePlacement is an image's size and transcript lines in a render.
type imagePlacement struct {
	source     *imageSource
	cols, rows int
	line       int
	height     int
}

func newTUIImages(ctx context.Context, notify chan<- tea.Msg) *tuiImages {
	return &tuiImages{
		ctx:        ctx,
		notify:     notify,
		cacheBytes: imageCacheBytes,
		fetches:    make(chan struct{}, maxImageFetches),
		renders:    make(chan struct{}, maxImageRenders),
	}
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

// lines returns the transcript rows for an image attachment placed at a
// transcript line, or nil when it cannot be previewed and only its link should
// be shown. ok is false when the rows explain why loading the image failed.
// Images are loaded by loadNear once the viewport is known.
func (s *tuiImages) lines(attachment tooltypes.ToolAttachment, maxCols, maxRows, line int) (rows []string, ok bool) {
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
		source = &imageSource{shortCode: attachment.ShortCode, width: attachment.Width, height: attachment.Height}
		s.sources[attachment.ShortCode] = source
	}
	if source.err != nil {
		return []string{mutedStyle.Render(imageUnavailableText(source.err, maxCols))}, false
	}
	cols, height := termimage.Fit(source.width, source.height, maxCols, maxRows, s.cell)
	rows, ok = imageLoadingLines(height), true
	if rendition := findRendition(source, cols, height); rendition != nil {
		switch {
		case rendition.err != nil:
			rows, ok = []string{mutedStyle.Render(imageUnavailableText(rendition.err, maxCols))}, false
		case rendition.rendition != nil:
			if rendition.lines == nil {
				rendition.lines = make([]string, rendition.rows)
				for row := range rendition.rows {
					rendition.lines[row] = termimage.PlaceholderLine(s.protocol, rendition.id, row, rendition.cols)
				}
			}
			rows = rendition.lines
		}
	}
	s.placing = append(s.placing, imagePlacement{source: source, cols: cols, rows: height, line: line, height: len(rows)})
	return rows, ok
}

func findRendition(source *imageSource, cols, rows int) *imageRendition {
	for _, rendition := range source.renditions {
		if rendition.cols == cols && rendition.rows == rows {
			return rendition
		}
	}
	return nil
}

// beginPass starts a transcript render.
func (s *tuiImages) beginPass() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.placing = nil
}

// endPass records where the finished transcript render placed its images.
func (s *tuiImages) endPass() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.placed, s.placing = s.placing, nil
}

// loadNear downloads and resizes the images within a viewport height of the
// viewport at transcript line top, then evicts cached images away from it.
func (s *tuiImages) loadNear(top, height int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.enabled() {
		return
	}
	s.near++
	low, high := top-height, top+2*height
	for _, placement := range s.placed {
		if placement.line+placement.height <= low || placement.line >= high {
			continue
		}
		source := placement.source
		source.near = s.near
		rendition := findRendition(source, placement.cols, placement.rows)
		switch {
		case rendition != nil:
			rendition.near = s.near
		case source.err != nil || source.loading:
		case source.data == nil:
			source.loading = true
			go s.load(source, s.fetcher)
		case placement.cols > 0:
			rendition = &imageRendition{
				source: source, id: s.ids.Next(), cols: placement.cols, rows: placement.rows,
				rendering: true, near: s.near,
			}
			s.byID[rendition.id] = rendition
			source.renditions = append(source.renditions, rendition)
			go s.render(source.data, rendition, s.cell)
		}
	}
	s.evictLocked()
}

// evictLocked frees the least recently viewed downloads and renditions,
// keeping those near the viewport, until the cache fits its budget.
func (s *tuiImages) evictLocked() {
	type entry struct {
		near  uint64
		size  int
		evict func()
	}
	var entries []entry
	total := 0
	for _, source := range s.sources {
		total += len(source.data)
		if source.data != nil && source.near != s.near {
			entries = append(entries, entry{source.near, len(source.data), func() { source.data = nil }})
		}
		for _, rendition := range source.renditions {
			if rendition.rendition == nil {
				continue
			}
			total += len(rendition.rendition.PNG)
			if rendition.near != s.near {
				entries = append(entries, entry{rendition.near, len(rendition.rendition.PNG), func() { s.dropRenditionLocked(rendition) }})
			}
		}
	}
	slices.SortFunc(entries, func(a, b entry) int { return cmp.Compare(a.near, b.near) })
	for _, entry := range entries {
		if total <= s.cacheBytes {
			return
		}
		entry.evict()
		total -= entry.size
	}
}

func (s *tuiImages) dropRenditionLocked(rendition *imageRendition) {
	delete(s.byID, rendition.id)
	rendition.source.renditions = slices.DeleteFunc(rendition.source.renditions, func(r *imageRendition) bool { return r == rendition })
	s.output.Release(rendition.id)
}

func (s *tuiImages) load(source *imageSource, fetcher imageFetcher) {
	if !acquire(s.ctx, s.fetches) {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, imageFetchTimeout)
	data, err := fetcher.FetchImage(ctx, source.shortCode)
	cancel()
	<-s.fetches
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

func acquire(ctx context.Context, slots chan struct{}) bool {
	select {
	case slots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *tuiImages) render(data []byte, rendition *imageRendition, cell termimage.CellSize) {
	if !acquire(s.ctx, s.renders) {
		return
	}
	picture, err := termimage.Decode(data)
	var rendered *termimage.Rendition
	if err == nil {
		rendered, err = termimage.Render(picture, rendition.cols, rendition.rows, cell)
	}
	<-s.renders
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
		rows, _ := m.images.lines(attachment, maxCols, maxRows, *line+1)
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
			start := *line
			if wrote {
				start++
			}
			attachment := tooltypes.ToolAttachment{Type: "image", ShortCode: segment.shortCode}
			if preview, ok := m.images.lines(attachment, maxCols, maxRows, start); ok {
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
