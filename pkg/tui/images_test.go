package tui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	chat "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/tui/termimage"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type imageTestRunner struct {
	images  map[string][]byte
	fetches atomic.Int32
	// gate, when set, holds each fetch until it is closed.
	gate        chan struct{}
	mu          sync.Mutex
	fetched     map[string]int
	active, max int
}

func (*imageTestRunner) Run(context.Context, chat.ChatRequest, chat.ChatEventSink) (string, error) {
	return "", nil
}

func (r *imageTestRunner) FetchImage(_ context.Context, shortCode string) ([]byte, error) {
	r.fetches.Add(1)
	r.mu.Lock()
	if r.fetched == nil {
		r.fetched = map[string]int{}
	}
	r.fetched[shortCode]++
	r.active++
	r.max = max(r.max, r.active)
	r.mu.Unlock()
	if r.gate != nil {
		<-r.gate
	}
	r.mu.Lock()
	r.active--
	r.mu.Unlock()
	if data, ok := r.images[shortCode]; ok {
		return data, nil
	}
	return nil, errors.New("server returned HTTP 404")
}

func (r *imageTestRunner) fetchCount(shortCode string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fetched[shortCode]
}

func TestTranscriptPreviewsKittyImages(t *testing.T) {
	runner := &imageTestRunner{images: map[string][]byte{"chart": imageTestPNG(t)}}
	m, output, written := newImageTestModel(t, runner, termimage.Kitty)
	m.entries = imageTestEntries()

	m.refreshViewport(true)
	plain := ansi.Strip(m.viewport.GetContent())
	assert.Contains(t, plain, "Generated image - https://connected.example/i/chart\n\n  Loading image…\n"+strings.Repeat("  \n", 9))

	waitForImages(t, &m, func() bool { return len(m.images.byID) == 1 && m.images.byID[firstImageID(&m)].rendition != nil })
	id := firstImageID(&m)
	lines := strings.Split(m.viewport.GetContent(), "\n")
	header := slicesIndexFunc(lines, func(line string) bool { return strings.Contains(ansi.Strip(line), "Generated image") })
	require.GreaterOrEqual(t, header, 0)
	assert.Empty(t, lines[header+1], "a blank line separates the link from the image")
	for row := range 10 {
		assert.Equal(t, "  "+termimage.PlaceholderLine(termimage.Kitty, id, row, 30), lines[header+2+row])
	}
	assert.Equal(t, int32(1), runner.fetches.Load(), "collapsed view_image previews are not loaded")

	// The placeholders survive the viewport, overlays, and margins unchanged.
	write(t, output, ansi.SetModeAltScreenSaveCursor)
	view := m.View().Content
	assert.Contains(t, view, "\n   "+termimage.PlaceholderLine(termimage.Kitty, id, 0, 30)+" ")
	assert.True(t, m.images.output.Uploaded(id))
	require.Eventually(t, func() bool {
		return strings.Contains(written(), fmt.Sprintf("\x1b_Ga=T,U=1,f=100,t=d,q=2,i=%d,c=30,r=10,", id))
	}, time.Second, 5*time.Millisecond)
	uploads := strings.Count(written(), "a=T,")
	_ = m.View()
	assert.Equal(t, uploads, strings.Count(written(), "a=T,"), "images are uploaded once")
}

func TestTranscriptPreviewsITerm2Images(t *testing.T) {
	runner := &imageTestRunner{images: map[string][]byte{"chart": imageTestPNG(t)}}
	m, output, written := newImageTestModel(t, runner, termimage.ITerm2)
	m.entries = imageTestEntries()
	m.refreshViewport(true)
	waitForImages(t, &m, func() bool { return len(m.images.byID) == 1 && m.images.byID[firstImageID(&m)].rendition != nil })

	write(t, output, ansi.SetModeAltScreenSaveCursor)
	view := m.View().Content
	assert.NotContains(t, view, string(rune(0x10EEEE)), "iTerm2 would print placeholders as text")
	lines := strings.Split(ansi.Strip(view), "\n")
	header := slicesIndexFunc(lines, func(line string) bool { return strings.Contains(line, "Generated image") })
	require.GreaterOrEqual(t, header, 0)

	// The left margin and tool body indent place the image at column 4.
	position := ansi.CursorPosition(4, header+3)
	require.Eventually(t, func() bool {
		return strings.Contains(written(), position+"\x1b]1337;File=size=")
	}, time.Second, 5*time.Millisecond)
	assert.Contains(t, written(), ";width=30;height=10;preserveAspectRatio=0;inline=1;doNotMoveCursor=1:")
}

func TestTranscriptImagePreviewStates(t *testing.T) {
	runner := &imageTestRunner{images: map[string][]byte{"broken": []byte("not an image")}}
	m, _, _ := newImageTestModel(t, runner, termimage.Kitty)
	m.entries = imageTestEntries()
	viewImage := &m.entries[0].blocks[0].tools[1]
	viewImage.expanded = true
	viewImage.structured.Attachments[0].ShortCode = "broken"
	viewImage.structured.Attachments[0].ViewURL = "/i/broken"

	m.refreshViewport(true)
	waitForImages(t, &m, func() bool {
		plain := ansi.Strip(m.viewport.GetContent())
		return strings.Contains(plain, "unsupported image format") && strings.Contains(plain, "HTTP 404")
	})
	plain := ansi.Strip(m.viewport.GetContent())
	assert.Contains(t, plain, "Image preview unavailable: server returned HTTP 404")
	assert.Contains(t, plain, "https://connected.example/i/broken\n\n  Image preview unavailable: unsupported image format")
}

func TestTranscriptWithoutImagePreviews(t *testing.T) {
	runner := &imageTestRunner{}
	m := newModel(t.Context(), Config{Runner: runner, Remote: true, ServerURL: "https://connected.example"})
	t.Cleanup(m.cancel)
	m.width, m.height = 100, 40
	m.resize()
	m.entries = imageTestEntries()

	m.refreshViewport(true)
	plain := ansi.Strip(m.viewport.GetContent())
	assert.NotContains(t, plain, "Loading image")
	assert.Equal(t, m.View().Content, m.images.compose(m.View().Content, m.height))
	assert.Zero(t, runner.fetches.Load())
}

func newImageTestModel(t *testing.T, runner *imageTestRunner, protocol termimage.Protocol) (model, *termimage.Output, func() string) {
	t.Helper()
	m := newModel(t.Context(), Config{Runner: runner, Remote: true, ServerURL: "https://connected.example"})
	t.Cleanup(m.cancel)
	path := filepath.Join(t.TempDir(), "tty")
	file, err := os.Create(path)
	require.NoError(t, err)
	output := termimage.NewOutput(file, protocol)
	t.Cleanup(func() {
		_ = output.Close()
		_ = file.Close()
	})
	m.images.enable(protocol, output, termimage.CellSize{Width: 10, Height: 20})
	m.width, m.height = 100, 40
	m.resize()
	return m, output, func() string {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		return string(data)
	}
}

func imageTestEntries() []chatEntry {
	return []chatEntry{{
		kind: entryAssistant,
		blocks: []assistantBlock{{
			kind: blockTools,
			tools: []toolCall{
				{
					name: "draw_chart",
					done: true,
					structured: &tooltypes.StructuredToolResult{
						ToolName: "draw_chart",
						Success:  true,
						Attachments: []tooltypes.ToolAttachment{{
							Type: "image", ArtifactID: "artifact-1", ShortCode: "chart", ViewURL: "/i/chart",
							Width: 300, Height: 200,
						}},
					},
				},
				{
					name:  "view_image",
					input: `{"path":"/tmp/screenshot.png"}`,
					done:  true,
					structured: &tooltypes.StructuredToolResult{
						ToolName: "view_image",
						Success:  true,
						Attachments: []tooltypes.ToolAttachment{{
							Type: "image", ArtifactID: "artifact-2", ShortCode: "screenshot", ViewURL: "/i/screenshot",
						}},
					},
				},
			},
		}},
	}}
}

// waitForImages handles image updates until ready reports true.
func waitForImages(t *testing.T, m *model, ready func() bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		m.images.mu.Lock()
		done := ready()
		m.images.mu.Unlock()
		if done {
			return
		}
		select {
		case msg := <-m.runCh:
			updated, _ := m.Update(msg)
			*m = updated.(model)
			if m.pendingRefresh {
				updated, _ = m.Update(transcriptRefreshMsg{})
				*m = updated.(model)
			}
		case <-deadline:
			t.Fatal("timed out waiting for image previews")
		}
	}
}

func firstImageID(m *model) uint32 {
	for id := range m.images.byID {
		return id
	}
	return 0
}

func slicesIndexFunc(lines []string, match func(string) bool) int {
	for i, line := range lines {
		if match(line) {
			return i
		}
	}
	return -1
}

func write(t *testing.T, output *termimage.Output, text string) {
	t.Helper()
	_, err := output.Write([]byte(text))
	require.NoError(t, err)
}

func imageTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 300, 200))
	for y := range 200 {
		for x := range 300 {
			img.Set(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 0x80, A: 0xff})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func TestMarkdownPreviewsReplaceOnlyTopLevelImageParagraphs(t *testing.T) {
	const code = "bFMUeAa9fd0DT7VVvGwxpg"
	fence := strings.Repeat("~", 3)
	md := newMarkdownRenderer(80, assistantMarkdownStyle)
	all := func(string) bool { return true }
	for _, test := range []struct {
		name, text, want string
		previews         []string
	}{
		{
			name:     "image paragraph",
			text:     "Here it is:\n\n![bridge](/i/" + code + ")\n\nAI-generated.",
			want:     "Here it is:\n\nKODELETIMAGEPREVIEW0\n\nAI-generated.",
			previews: []string{code},
		},
		{
			name:     "absolute link",
			text:     "![chart](https://kodelet.example/base/i/" + code + " \"Chart\")",
			want:     "KODELETIMAGEPREVIEW0",
			previews: []string{code},
		},
		{name: "inline image", text: "See ![x](/i/" + code + ") here"},
		{name: "list item", text: "- ![x](/i/" + code + ")"},
		{name: "code fence", text: fence + "\n![x](/i/" + code + ")\n" + fence},
		{name: "other image", text: "![x](https://example.com/cat.png)"},
		{name: "query string", text: "![x](/i/" + code + "?download=1)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source, previews := markdownPreviews(md, []byte(test.text), all)
			want := test.want
			if want == "" {
				want = test.text
			}
			assert.Equal(t, want, string(source))
			assert.Equal(t, test.previews, previews)
		})
	}

	source, previews := markdownPreviews(md, []byte("![x](/i/"+code+")"), func(string) bool { return false })
	assert.Equal(t, "![x](/i/"+code+")", string(source), "images that cannot be previewed keep their markdown")
	assert.Nil(t, previews)
}

func TestMarkdownResolvesOnlyImageLinkNodes(t *testing.T) {
	m := newModel(t.Context(), Config{Runner: &imageTestRunner{}, Remote: true, ServerURL: "https://connected.example/base/"})
	t.Cleanup(m.cancel)
	fence := strings.Repeat("~", 3)
	render := func(text string) string { return ansi.Strip(m.renderMarkdown(text, 120, markdownAssistant)) }

	assert.Contains(t, render("Open [the image](/i/abc)."), "https://connected.example/base/i/abc")
	assert.Contains(t, render("![chart](/i/abc)"), "https://connected.example/base/i/abc")
	assert.Contains(t, render("See [the chart][chart].\n\n[chart]: /i/abc"), "https://connected.example/base/i/abc")
	for _, text := range []string{
		"Use \x60[x](/i/abc)\x60 in docs.",
		fence + "\n[x](/i/abc)\n" + fence,
		"See [images.go](pkg/tui/images.go).",
		"[x](https://other.example/i/abc)",
	} {
		assert.NotContains(t, render(text), "connected.example", text)
	}

	m.serverURL = ""
	assert.NotContains(t, render("[x](/i/abc)"), "https://")
}

func TestTranscriptPreviewsKeepReferenceLinks(t *testing.T) {
	const code = "bFMUeAa9fd0DT7VVvGwxpg"
	runner := &imageTestRunner{images: map[string][]byte{code: imageTestPNG(t)}}
	m, _, _ := newImageTestModel(t, runner, termimage.Kitty)
	m.entries = []chatEntry{{kind: entryAssistant, blocks: []assistantBlock{{
		kind: blockText,
		text: "See [the chart][chart].\n\n![bridge](/i/" + code + ")\n\n[chart]: /i/otherImageCode",
	}}}}

	m.refreshViewport(true)
	waitForImages(t, &m, func() bool { return len(m.images.byID) == 1 && m.images.byID[firstImageID(&m)].rendition != nil })
	plain := ansi.Strip(m.viewport.GetContent())
	assert.Contains(t, plain, "https://connected.example/i/otherImageCode", "the definition still applies across the image")
	assert.NotContains(t, plain, "KODELETIMAGEPREVIEW")
	assert.Contains(t, m.viewport.GetContent(), termimage.PlaceholderLine(termimage.Kitty, firstImageID(&m), 0, 30))
}

func TestTranscriptPreviewsMarkdownImages(t *testing.T) {
	const code = "bFMUeAa9fd0DT7VVvGwxpg"
	text := "Here's London Bridge.\n\n![London Bridge at sunset](/i/" + code + ")\n\nThe image is AI-generated."
	entries := []chatEntry{{kind: entryAssistant, blocks: []assistantBlock{{kind: blockText, text: text}}}}

	disabled := newModel(t.Context(), Config{Runner: &imageTestRunner{}, Remote: true})
	t.Cleanup(disabled.cancel)
	disabled.width, disabled.height = 100, 40
	disabled.resize()
	disabled.entries = entries
	content, _ := disabled.renderTranscript()
	want := renderPersistentStyle(assistantStyle, disabled.renderMarkdown(text, disabled.transcriptTextWidth(), markdownAssistant))
	assert.Equal(t, want+"\n", content, "without previews the reply renders as before")

	runner := &imageTestRunner{images: map[string][]byte{code: imageTestPNG(t)}}
	m, _, _ := newImageTestModel(t, runner, termimage.Kitty)
	m.entries = entries
	m.refreshViewport(true)
	waitForImages(t, &m, func() bool { return len(m.images.byID) == 1 && m.images.byID[firstImageID(&m)].rendition != nil })

	assert.NotContains(t, ansi.Strip(m.viewport.GetContent()), "London Bridge at sunset", "the preview replaces the link")
	lines := strings.Split(m.viewport.GetContent(), "\n")
	assert.Equal(t, "Here's London Bridge.", strings.TrimSpace(ansi.Strip(lines[0])))
	assert.Empty(t, strings.TrimSpace(ansi.Strip(lines[1])))
	id := firstImageID(&m)
	for row := range 10 {
		assert.Equal(t, termimage.PlaceholderLine(termimage.Kitty, id, row, 30), lines[2+row])
	}
	assert.Empty(t, strings.TrimSpace(ansi.Strip(lines[12])))
	assert.Equal(t, "The image is AI-generated.", strings.TrimSpace(ansi.Strip(lines[13])))
}

func TestTranscriptKeepsLinksForUnavailableMarkdownImages(t *testing.T) {
	const code = "bFMUeAa9fd0DT7VVvGwxpg"
	m, _, _ := newImageTestModel(t, &imageTestRunner{}, termimage.Kitty)
	m.entries = []chatEntry{{kind: entryAssistant, blocks: []assistantBlock{{
		kind: blockText,
		text: "Here it is:\n\n![London Bridge at sunset](/i/" + code + ")",
	}}}}

	m.refreshViewport(true)
	assert.Contains(t, ansi.Strip(m.viewport.GetContent()), "Loading image…")
	waitForImages(t, &m, func() bool { return m.images.sources[code] != nil && !m.images.sources[code].loading })
	m.refreshViewport(true)

	plain := ansi.Strip(m.viewport.GetContent())
	assert.Contains(t, plain, "London Bridge at sunset")
	assert.Contains(t, plain, "https://connected.example/i/"+code, "the link is resolved against the server")
	assert.NotContains(t, plain, "Image preview unavailable")
}

func TestTerminalVersionEnablesImages(t *testing.T) {
	runner := &imageTestRunner{images: map[string][]byte{"chart": imageTestPNG(t)}}
	m, output, _ := newImageTestModel(t, runner, termimage.None)
	m.images.awaitTerminalVersion(output, termimage.CellSize{Width: 10, Height: 20})
	m.entries = imageTestEntries()
	m.refreshViewport(true)
	assert.NotContains(t, ansi.Strip(m.viewport.GetContent()), "Loading image")

	batch, ok := m.Init()().(tea.BatchMsg)
	require.True(t, ok)
	assert.True(t, slices.ContainsFunc(batch, func(cmd tea.Cmd) bool {
		return reflect.ValueOf(cmd).Pointer() == reflect.ValueOf(tea.RequestTerminalVersion).Pointer()
	}), "the terminal is asked for its name")

	updated, cmd := m.Update(tea.TerminalVersionMsg{Name: "ghostty 1.4.0"})
	m = updated.(model)
	require.NotNil(t, cmd)
	assert.Equal(t, tea.ColorProfileMsg{Profile: colorprofile.TrueColor}, cmd())
	assert.False(t, m.images.awaitingTerminalVersion())
	assert.Contains(t, ansi.Strip(m.viewport.GetContent()), "Loading image")
	waitForImages(t, &m, func() bool { return len(m.images.byID) == 1 && m.images.byID[firstImageID(&m)].rendition != nil })
	assert.Contains(t, m.viewport.GetContent(), termimage.PlaceholderLine(termimage.Kitty, firstImageID(&m), 0, 30))
}

func TestTerminalVersionIgnoresUnsupportedTerminals(t *testing.T) {
	m, output, _ := newImageTestModel(t, &imageTestRunner{}, termimage.None)
	m.images.awaitTerminalVersion(output, termimage.CellSize{})

	updated, cmd := m.Update(tea.TerminalVersionMsg{Name: "WezTerm 20260101"})
	m = updated.(model)
	assert.Nil(t, cmd)
	assert.False(t, m.images.protocolEnabled())
	assert.False(t, m.images.awaitingTerminalVersion())

	updated, cmd = m.Update(tea.TerminalVersionMsg{Name: "ghostty 1.4.0"})
	assert.Nil(t, cmd, "only the first reply is used")
	assert.False(t, updated.(model).images.protocolEnabled())
}

func TestTranscriptShowsFullImageLinksInReplies(t *testing.T) {
	m := newModel(t.Context(), Config{Runner: &imageTestRunner{}, Remote: true, ServerURL: "https://connected.example"})
	t.Cleanup(m.cancel)
	m.width, m.height = 100, 40
	m.resize()
	m.entries = []chatEntry{{kind: entryAssistant, blocks: []assistantBlock{{
		kind: blockText,
		text: "Open this link directly: [the image](/i/bFMUeAa9fd0DT7VVvGwxpg).",
	}}}}

	content, _ := m.renderTranscript()
	assert.Contains(t, ansi.Strip(content), "https://connected.example/i/bFMUeAa9fd0DT7VVvGwxpg")
}

func TestImageSizesRequestedInOnePassAreKept(t *testing.T) {
	runner := &imageTestRunner{images: map[string][]byte{"chart": imageTestPNG(t)}}
	m, _, _ := newImageTestModel(t, runner, termimage.Kitty)
	attachment := tooltypes.ToolAttachment{Type: "image", ShortCode: "chart", Width: 300, Height: 200}
	// Indentation gives the same image three widths on a narrow terminal.
	renderPass := func() {
		m.images.beginPass()
		for i, cols := range []int{20, 18, 16} {
			m.images.lines(attachment, cols, 30, i*20)
		}
		m.images.endPass()
		m.images.loadNear(0, 60)
	}
	ready := func() bool {
		m.images.mu.Lock()
		defer m.images.mu.Unlock()
		if len(m.images.byID) != 3 {
			return false
		}
		for _, rendition := range m.images.byID {
			if rendition.rendition == nil {
				return false
			}
		}
		return true
	}
	ids := func() []uint32 {
		m.images.mu.Lock()
		defer m.images.mu.Unlock()
		ids := slices.Collect(maps.Keys(m.images.byID))
		slices.Sort(ids)
		return ids
	}

	deadline := time.After(5 * time.Second)
	for renderPass(); !ready(); renderPass() {
		select {
		case <-m.runCh:
		case <-deadline:
			t.Fatal("timed out waiting for image renditions")
		}
	}
	rendered := ids()
	for range 3 {
		renderPass()
	}
	assert.Equal(t, rendered, ids(), "no size is evicted and rendered again")
	select {
	case msg := <-m.runCh:
		t.Fatalf("unexpected image update %T", msg)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestImagesLoadNearTheViewport(t *testing.T) {
	runner := &imageTestRunner{images: map[string][]byte{}}
	var tools []toolCall
	for i := range 8 {
		code := fmt.Sprintf("image%d", i)
		runner.images[code] = imageTestPNG(t)
		tools = append(tools, toolCall{
			name: "draw_chart",
			done: true,
			structured: &tooltypes.StructuredToolResult{
				ToolName: "draw_chart",
				Success:  true,
				Attachments: []tooltypes.ToolAttachment{{
					Type: "image", ArtifactID: code, ShortCode: code, ViewURL: "/i/" + code, Width: 300, Height: 200,
				}},
			},
		})
	}
	m, _, _ := newImageTestModel(t, runner, termimage.Kitty)
	m.entries = []chatEntry{{kind: entryAssistant, blocks: []assistantBlock{{kind: blockTools, tools: tools}}}}

	m.refreshViewport(true)
	waitForImages(t, &m, func() bool { return m.images.sources["image7"].data != nil })
	assert.Zero(t, runner.fetchCount("image0"), "images far above the viewport are not loaded")
	assert.Equal(t, 1, runner.fetchCount("image7"))
	lines := m.viewport.TotalLineCount()

	m.viewport.GotoTop()
	m.loadVisibleImages()
	waitForImages(t, &m, func() bool { return m.images.sources["image0"].data != nil })
	assert.Equal(t, 1, runner.fetchCount("image0"))
	assert.Equal(t, lines, m.viewport.TotalLineCount(), "rows are reserved before images load")
}

func TestImageFetchesAreBounded(t *testing.T) {
	runner := &imageTestRunner{images: map[string][]byte{}, gate: make(chan struct{})}
	m, _, _ := newImageTestModel(t, runner, termimage.Kitty)
	m.images.beginPass()
	for i := range 6 {
		code := fmt.Sprintf("image%d", i)
		runner.images[code] = imageTestPNG(t)
		m.images.lines(tooltypes.ToolAttachment{Type: "image", ShortCode: code}, 30, 10, i)
	}
	m.images.endPass()
	m.images.loadNear(0, 10)

	require.Eventually(t, func() bool { return runner.fetches.Load() == maxImageFetches }, time.Second, 5*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(maxImageFetches), runner.fetches.Load(), "later downloads wait for a slot")
	close(runner.gate)
	for loaded := 0; loaded < 6; loaded++ {
		select {
		case <-m.runCh:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for downloads")
		}
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	assert.Equal(t, maxImageFetches, runner.max)
}

func TestImageCacheEvictsImagesAwayFromTheViewport(t *testing.T) {
	runner := &imageTestRunner{images: map[string][]byte{"near": imageTestPNG(t), "far": imageTestPNG(t)}}
	m, _, _ := newImageTestModel(t, runner, termimage.Kitty)
	m.images.cacheBytes = 1
	attachment := func(code string) tooltypes.ToolAttachment {
		return tooltypes.ToolAttachment{Type: "image", ShortCode: code, Width: 300, Height: 200}
	}
	renderPass := func(top int) {
		m.images.beginPass()
		m.images.lines(attachment("near"), 30, 10, 0)
		m.images.lines(attachment("far"), 30, 10, 100)
		m.images.endPass()
		m.images.loadNear(top, 10)
	}
	rendered := func(code string) bool {
		m.images.mu.Lock()
		defer m.images.mu.Unlock()
		source := m.images.sources[code]
		return source != nil && len(source.renditions) == 1 && source.renditions[0].rendition != nil
	}
	waitUntil := func(top int, ready func() bool) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for renderPass(top); !ready(); renderPass(top) {
			select {
			case <-m.runCh:
			case <-deadline:
				t.Fatal("timed out waiting for images")
			}
		}
	}

	waitUntil(0, func() bool { return rendered("near") })
	waitUntil(100, func() bool { return rendered("far") })
	m.images.mu.Lock()
	assert.Nil(t, m.images.sources["near"].data, "the download away from the viewport is freed")
	assert.Empty(t, m.images.sources["near"].renditions, "the rendition away from the viewport is freed")
	assert.NotNil(t, m.images.sources["far"].data, "images near the viewport stay cached")
	m.images.mu.Unlock()

	rows, ok := m.images.lines(attachment("near"), 30, 10, 0)
	assert.True(t, ok)
	assert.Len(t, rows, 10, "an evicted image keeps its rows reserved")
	waitUntil(0, func() bool { return rendered("near") })
	assert.Equal(t, 2, runner.fetchCount("near"), "scrolling back downloads it again")
}

func TestTranscriptDrawsRepeatedITerm2Images(t *testing.T) {
	const code = "bFMUeAa9fd0DT7VVvGwxpg"
	runner := &imageTestRunner{images: map[string][]byte{code: imageTestPNG(t)}}
	m, output, written := newImageTestModel(t, runner, termimage.ITerm2)
	image := "![London Bridge at sunset](/i/" + code + ")"
	m.entries = []chatEntry{{kind: entryAssistant, blocks: []assistantBlock{{
		kind: blockText,
		text: "Here it is:\n\n" + image + "\n\nAnd again:\n\n" + image,
	}}}}
	m.refreshViewport(true)
	waitForImages(t, &m, func() bool { return len(m.images.byID) == 1 && m.images.byID[firstImageID(&m)].rendition != nil })

	write(t, output, ansi.SetModeAltScreenSaveCursor)
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	first := slicesIndexFunc(lines, func(line string) bool { return strings.Contains(line, "Here it is:") })
	second := slicesIndexFunc(lines, func(line string) bool { return strings.Contains(line, "And again:") })
	require.GreaterOrEqual(t, first, 0)
	require.Greater(t, second, first)
	// Each copy follows its paragraph and a blank line, after the left margin.
	require.Eventually(t, func() bool {
		drawn := written()
		return strings.Contains(drawn, ansi.CursorPosition(2, first+3)+"\x1b]1337;File=") &&
			strings.Contains(drawn, ansi.CursorPosition(2, second+3)+"\x1b]1337;File=")
	}, time.Second, 5*time.Millisecond)
}
