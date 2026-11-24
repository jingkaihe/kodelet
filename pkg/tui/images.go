package tui

import (
	"cmp"
	"context"
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
	// maxKittyUploads bounds Kitty's memory; evicted images upload again when visible.
	maxKittyUploads = 32
	// imageCacheBytes bounds the downloads and renditions kept away from the viewport.
	imageCacheBytes = 64 << 20
	// Decoding allocates a full-resolution bitmap, so decodes run one at a time.
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

	// placed is where the latest render put each image, previous where the one
	// before did; placing collects the next.
	placed, previous, placing []imagePlacement
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
	// data is nil until downloaded and once evicted; the kept size reserves rows.
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

// previewable reports whether an image can be previewed and has not failed.
func (s *tuiImages) previewable(shortCode string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	source := s.sources[shortCode]
	return s.enabled() && validImageShortCode(shortCode) && (source == nil || source.err == nil)
}

func (s *tuiImages) available() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enabled()
}

// lines returns an attachment's rows at a transcript line, or nil to show only
// its link.
func (s *tuiImages) lines(attachment tooltypes.ToolAttachment, maxCols, maxRows, line int) []string {
	if s == nil || attachment.Type != "image" || attachment.Error != "" || !validImageShortCode(attachment.ShortCode) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.enabled() || maxCols <= 0 || maxRows <= 0 {
		return nil
	}

	source := s.sources[attachment.ShortCode]
	if source == nil {
		source = &imageSource{shortCode: attachment.ShortCode, width: attachment.Width, height: attachment.Height}
		s.sources[attachment.ShortCode] = source
	}
	if source.err != nil {
		return []string{mutedStyle.Render(imageUnavailableText(source.err, maxCols))}
	}
	cols, height := termimage.Fit(source.width, source.height, maxCols, maxRows, s.cell)
	rows := imageLoadingLines(height)
	if rendition := findRendition(source, cols, height); rendition != nil {
		switch {
		case rendition.err != nil:
			rows = []string{mutedStyle.Render(imageUnavailableText(rendition.err, maxCols))}
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
	return rows
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
	s.previous, s.placed, s.placing = s.placed, s.placing, nil
}

// rowsAddedAbove returns the rows that images above transcript line top gained,
// or lost, between the previous render and the latest.
func (s *tuiImages) rowsAddedAbove(top int) int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	added := 0
	for i, before := range s.previous {
		if i >= len(s.placed) || before.line >= top {
			break
		}
		after := s.placed[i]
		if after.source != before.source || after.line != before.line+added {
			break // other content above changed too
		}
		added += after.height - before.height
	}
	return added
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
			go s.render(rendition)
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

// load downloads an image once a fetch slot is free, unless it has since left
// the viewport.
func (s *tuiImages) load(source *imageSource, fetcher imageFetcher) {
	if !acquire(s.ctx, s.fetches) {
		return
	}
	s.mu.Lock()
	if source.near != s.near {
		source.loading = false
		s.mu.Unlock()
		<-s.fetches
		return
	}
	s.mu.Unlock()
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

// render decodes and resizes an image once a render slot is free, unless it
// has since left the viewport.
func (s *tuiImages) render(rendition *imageRendition) {
	if !acquire(s.ctx, s.renders) {
		return
	}
	s.mu.Lock()
	data, cell := rendition.source.data, s.cell
	if rendition.near != s.near || data == nil {
		s.dropRenditionLocked(rendition)
		s.mu.Unlock()
		<-s.renders
		return
	}
	s.mu.Unlock()
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
		s.output.Present(frame.Signature, frame.Placements, s.encodeITerm2)
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

// encodeITerm2 returns the command that draws a placement's rows at the cursor.
func (s *tuiImages) encodeITerm2(placement termimage.Placement) string {
	s.mu.Lock()
	var rendition *termimage.Rendition
	if image := s.byID[placement.ID]; image != nil {
		rendition = image.rendition
	}
	s.mu.Unlock()
	if rendition == nil {
		return ""
	}
	data, err := rendition.Slice(placement.FirstRow, placement.Rows)
	if err != nil {
		return ""
	}
	return termimage.ITerm2Image(data, placement.Cols, placement.Rows)
}

// renderTranscriptImages writes image previews indented like a tool body,
// each separated from the text above by a blank line.
func (m *model) renderTranscriptImages(b *strings.Builder, line *int, attachments []tooltypes.ToolAttachment, indent string) {
	if len(attachments) == 0 {
		return
	}
	maxCols, maxRows := m.imagePreviewLimits(len(indent) + 2)
	for _, attachment := range attachments {
		rows := m.images.lines(attachment, maxCols, maxRows, *line+1)
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

// renderAssistantText renders assistant markdown, previewing images in top-level paragraphs.
func (m *model) renderAssistantText(b *strings.Builder, line *int, text string) {
	var previewable func(string) bool
	if m.images.available() {
		previewable = m.images.previewable
	}
	rows, markers := m.assistantTextRows(text, previewable)
	if previewable != nil && markers == nil {
		// A marker was not on its own row, so show links instead.
		rows, _ = m.assistantTextRows(text, nil)
	}

	maxCols, maxRows := m.imagePreviewLimits(0)
	for i, row := range rows {
		if shortCode, ok := markers[i]; ok {
			attachment := tooltypes.ToolAttachment{Type: "image", ShortCode: shortCode}
			preview := m.images.lines(attachment, maxCols, maxRows, *line)
			for _, previewRow := range preview {
				b.WriteString(previewRow + "\n")
				*line++
			}
			continue
		}
		b.WriteString(row + "\n")
		*line++
	}
}

// assistantTextRows maps marker rows to images; markers is nil if one is missing.
func (m *model) assistantTextRows(text string, previewable func(string) bool) (rows []string, markers map[int]string) {
	rendered, previews := m.renderMarkdownPreviews(text, m.transcriptTextWidth(), markdownAssistant, previewable)
	rows = strings.Split(renderPersistentStyle(assistantStyle, rendered), "\n")
	markers = map[int]string{}
	for index, shortCode := range previews {
		marker := previewMarker(index)
		row := slices.IndexFunc(rows, func(row string) bool { return strings.TrimSpace(ansi.Strip(row)) == marker })
		if row < 0 {
			return rows, nil
		}
		markers[row] = shortCode
	}
	return rows, markers
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
