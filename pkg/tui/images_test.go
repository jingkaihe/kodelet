package tui

import (
	"bytes"
	"cmp"
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
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	chat "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/tui/termimage"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testShortCode = "bFMUeAa9fd0DT7VVvGwxpg"

func TestTranscriptPreviewsKittyImages(t *testing.T) {
	runner := &imageTestRunner{images: map[string][]byte{"chart": imageTestPNG(t)}}
	m, output, written := newImageTestModel(t, runner, termimage.Kitty)
	m.entries = imageTestEntries()

	m.refreshViewport(true)
	plain := ansi.Strip(m.viewport.GetContent())
	assert.Contains(t, plain, "Generated image - https://connected.example/i/chart\n\n  Loading image…\n"+strings.Repeat("  \n", 9))

	waitForImages(t, &m, func() bool { return renditionsReady(m.images, 1) })
	id := firstImageID(&m)
	lines := strings.Split(m.viewport.GetContent(), "\n")
	header := slices.IndexFunc(lines, func(line string) bool { return strings.Contains(ansi.Strip(line), "Generated image") })
	require.GreaterOrEqual(t, header, 0)
	for row := range 10 {
		assert.Equal(t, "  "+termimage.PlaceholderLine(termimage.Kitty, id, row, 30), lines[header+2+row])
	}
	assert.Zero(t, runner.fetchCount("screenshot"), "collapsed view_image previews are not loaded")

	write(t, output, ansi.SetModeAltScreenSaveCursor)
	assert.Contains(t, m.View().Content, "\n   "+termimage.PlaceholderLine(termimage.Kitty, id, 0, 30)+" ")
	upload := fmt.Sprintf("\x1b_Ga=T,U=1,f=100,t=d,q=2,i=%d,c=30,r=10,", id)
	require.Eventually(t, func() bool { return strings.Contains(written(), upload) }, time.Second, 5*time.Millisecond)
	_ = m.View()
	assert.Equal(t, 1, strings.Count(written(), upload), "images are uploaded once")
}

func TestTranscriptDrawsITerm2Images(t *testing.T) {
	runner := &imageTestRunner{images: map[string][]byte{testShortCode: imageTestPNG(t)}}
	m, output, written := newImageTestModel(t, runner, termimage.ITerm2)
	image := "![London Bridge at sunset](/i/" + testShortCode + ")"
	m.entries = []chatEntry{{kind: entryAssistant, blocks: []assistantBlock{{
		kind: blockText,
		text: "Here it is:\n\n" + image + "\n\nAnd again:\n\n" + image,
	}}}}
	m.refreshViewport(true)
	waitForImages(t, &m, func() bool { return renditionsReady(m.images, 1) })

	write(t, output, ansi.SetModeAltScreenSaveCursor)
	view := m.View().Content
	assert.NotContains(t, view, string(rune(0x10EEEE)), "iTerm2 would print placeholders as text")
	lines := strings.Split(ansi.Strip(view), "\n")
	first := slices.IndexFunc(lines, func(line string) bool { return strings.Contains(line, "Here it is:") })
	second := slices.IndexFunc(lines, func(line string) bool { return strings.Contains(line, "And again:") })
	require.GreaterOrEqual(t, first, 0)
	// Each copy is drawn after its paragraph and a blank line.
	require.Eventually(t, func() bool {
		drawn := written()
		return strings.Contains(drawn, ansi.CursorPosition(2, first+3)+"\x1b]1337;File=") &&
			strings.Contains(drawn, ansi.CursorPosition(2, second+3)+"\x1b]1337;File=")
	}, time.Second, 5*time.Millisecond)
}

func TestTranscriptPreviewsMarkdownImages(t *testing.T) {
	text := "Here's London Bridge: ![London Bridge at sunset](/i/" + testShortCode + ")\n\n" +
		"See [the chart][chart].\n\n[chart]: /i/otherImageCode"
	entries := []chatEntry{{kind: entryAssistant, blocks: []assistantBlock{{kind: blockText, text: text}}}}

	disabled := newModel(t.Context(), Config{Runner: &imageTestRunner{}, Remote: true})
	t.Cleanup(disabled.cancel)
	disabled.entries = entries
	content, _ := disabled.renderTranscript()
	want := renderPersistentStyle(assistantStyle, disabled.renderMarkdown(text, disabled.transcriptTextWidth(), markdownAssistant))
	assert.Equal(t, want+"\n", content, "without previews the reply renders as before")

	runner := &imageTestRunner{images: map[string][]byte{testShortCode: imageTestPNG(t)}}
	m, _, _ := newImageTestModel(t, runner, termimage.Kitty)
	m.entries = entries
	m.refreshViewport(true)
	waitForImages(t, &m, func() bool { return renditionsReady(m.images, 1) })

	plain := ansi.Strip(m.viewport.GetContent())
	assert.NotContains(t, plain, "London Bridge at sunset", "the preview replaces the link")
	assert.Contains(t, plain, "https://connected.example/i/otherImageCode", "references still resolve across the image")
	lines := strings.Split(m.viewport.GetContent(), "\n")
	assert.Equal(t, "Here's London Bridge:", strings.TrimSpace(ansi.Strip(lines[0])))
	assert.Empty(t, strings.TrimSpace(ansi.Strip(lines[1])))
	for row := range 10 {
		assert.Equal(t, termimage.PlaceholderLine(termimage.Kitty, firstImageID(&m), row, 30), lines[2+row])
	}
	assert.Empty(t, strings.TrimSpace(ansi.Strip(lines[12])))
}

func TestTranscriptImageFailures(t *testing.T) {
	runner := &imageTestRunner{images: map[string][]byte{"broken": []byte("not an image")}}
	m, _, _ := newImageTestModel(t, runner, termimage.Kitty)
	m.entries = imageTestEntries()
	viewImage := &m.entries[0].blocks[0].tools[1]
	viewImage.expanded = true
	viewImage.structured.Attachments[0].ShortCode = "broken"
	viewImage.structured.Attachments[0].ViewURL = "/i/broken"
	m.entries = append(m.entries, chatEntry{kind: entryAssistant, blocks: []assistantBlock{{
		kind: blockText,
		text: "![London Bridge at sunset](/i/" + testShortCode + ")",
	}}})

	m.refreshViewport(true)
	waitForImages(t, &m, func() bool {
		return !m.images.sources["chart"].loading && !m.images.sources["broken"].loading && !m.images.sources[testShortCode].loading
	})
	m.refreshViewport(true)
	plain := ansi.Strip(m.viewport.GetContent())
	assert.Contains(t, plain, "/i/chart\n\n  Image preview unavailable: server returned HTTP 404")
	assert.Contains(t, plain, "/i/broken\n\n  Image preview unavailable: unsupported image format")
	assert.Contains(t, plain, "London Bridge at sunset", "a reply image that cannot load keeps its link")
}

func TestTranscriptWithoutImagePreviews(t *testing.T) {
	runner := &imageTestRunner{}
	m := newModel(t.Context(), Config{Runner: runner, Remote: true, ServerURL: "https://connected.example"})
	t.Cleanup(m.cancel)
	m.width, m.height = 100, 40
	m.resize()
	m.entries = imageTestEntries()

	m.refreshViewport(true)
	assert.NotContains(t, ansi.Strip(m.viewport.GetContent()), "Loading image")
	assert.Equal(t, m.View().Content, m.images.compose(m.View().Content, m.height))
	assert.Zero(t, runner.fetchCount("chart"))
}

func TestMarkdownPreviewsSplitImagesOutOfTopLevelParagraphs(t *testing.T) {
	m := newModel(t.Context(), Config{Runner: &imageTestRunner{}, Remote: true, ServerURL: "https://connected.example"})
	t.Cleanup(m.cancel)
	fence := strings.Repeat("~", 3)
	image := "![bridge](/i/" + testShortCode + ")"
	plain := func(rendered string) string {
		lines := strings.Split(ansi.Strip(rendered), "\n")
		for i, line := range lines {
			lines[i] = strings.TrimSpace(line)
		}
		return strings.Join(lines, "\n")
	}
	// Each marker row is followed by the image's link, shown if it fails.
	preview := func(index int) string {
		return fmt.Sprintf("KODELETIMAGEPREVIEW%d\nImage: bridge → https://connected.example/i/%s", index, testShortCode)
	}
	for _, test := range []struct {
		name, text, want string
		previews         int
	}{
		{
			name:     "image paragraph",
			text:     "Here it is:\n\n" + image + "\n\nAI-generated.",
			want:     "Here it is:\n\n" + preview(0) + "\n\nAI-generated.",
			previews: 1,
		},
		{name: "end of a sentence", text: "Here it is: " + image, want: "Here it is:\n\n" + preview(0), previews: 1},
		{name: "next line", text: "Here it is:\n" + image + "\nAI-generated.", want: "Here it is:\n\n" + preview(0) + "\n\nAI-generated.", previews: 1},
		{name: "consecutive lines", text: image + "\n" + image, want: preview(0) + "\n\n" + preview(1), previews: 2},
		{name: "mid-sentence", text: "See *this* " + image + " here", want: "See this\n\n" + preview(0) + "\n\nhere", previews: 1},
		{
			name:     "absolute link",
			text:     "![chart](https://kodelet.example/base/i/" + testShortCode + ")",
			want:     "KODELETIMAGEPREVIEW0\nImage: chart → https://kodelet.example/base/i/" + testShortCode,
			previews: 1,
		},
		{name: "list item", text: "- " + image},
		{name: "code fence", text: fence + "\n" + image + "\n" + fence},
		{name: "image link", text: "[" + image + "](https://example.com)"},
		{name: "other image", text: "![x](https://example.com/cat.png)"},
		{name: "query string", text: "![x](/i/" + testShortCode + "?download=1)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			rendered, previews := m.renderMarkdownPreviews(test.text, 80, markdownAssistant, func(string) bool { return true })
			assert.Equal(t, cmp.Or(test.want, plain(m.renderMarkdown(test.text, 80, markdownAssistant))), plain(rendered))
			assert.Len(t, previews, test.previews)
		})
	}
}

func TestMarkdownResolvesOnlyImageLinkNodes(t *testing.T) {
	m := newModel(t.Context(), Config{Runner: &imageTestRunner{}, Remote: true, ServerURL: "https://connected.example/base/"})
	t.Cleanup(m.cancel)
	fence := strings.Repeat("~", 3)
	render := func(text string) string { return ansi.Strip(m.renderMarkdown(text, 120, markdownAssistant)) }

	assert.Contains(t, render("Open [the image](/i/abc)."), "https://connected.example/base/i/abc")

	// Link text and URL open what the URL shows.
	for text, want := range map[string]string{
		"Open [the image](/i/abc).":                                "https://connected.example/base/i/abc",
		"Wiki [Foo](https://en.wikipedia.org/wiki/Foo_\\(bar\\)).": "https://en.wikipedia.org/wiki/Foo_(bar)",
	} {
		links := linkedCells(m.renderMarkdown(text, 120, markdownAssistant))
		assert.Equal(t, []string{want}, slices.Collect(maps.Keys(links)), text)
		assert.True(t, strings.HasSuffix(links[want], want), text)
	}
	// Wrapped links stay exact, and links the terminal renderer would truncate
	// or discard only show their URL.
	unwrapped := func(rendered string) string {
		lines := strings.Split(ansi.Strip(rendered), "\n")
		for i, line := range lines {
			lines[i] = strings.TrimSpace(line)
		}
		return strings.Join(lines, "")
	}
	signed := "https://example.com/file?sig=" + strings.Repeat("a", 3900)
	assert.Equal(t, []string{signed}, slices.Collect(maps.Keys(linkedCells(m.renderMarkdown("[file]("+signed+")", 80, markdownAssistant)))))
	for _, url := range []string{signed + strings.Repeat("a", 200), "https://example.com/a;b=" + strings.Repeat("c", 100)} {
		rendered := m.renderMarkdown("[file]("+url+")", 80, markdownAssistant)
		assert.Empty(t, linkedCells(rendered))
		assert.Contains(t, unwrapped(rendered), url)
	}
	assert.Contains(t, render("![chart](/i/abc)"), "https://connected.example/base/i/abc")
	assert.Contains(t, render("[Download](/i/abc?download=1)"), "https://connected.example/base/i/abc?download=1")
	assert.Equal(t, "See https://connected.example/base/i/abc.", strings.TrimSpace(render("See [/i/abc](/i/abc).")))
	assert.Equal(t, "https://example.com/x", strings.TrimSpace(render("[https://example.com/x](https://example.com/x)")))
	assert.Contains(t, render("See [#usage](#usage) and [README.md](README.md)."), "See #usage and README.md ", "labels of other links are kept")
	for _, text := range []string{
		"Use \x60[x](/i/abc)\x60 in docs.",
		fence + "\n[x](/i/abc)\n" + fence,
		"See [images.go](pkg/tui/images.go).",
	} {
		assert.NotContains(t, render(text), "connected.example", text)
	}
}

func TestTerminalVersionEnablesImages(t *testing.T) {
	for _, test := range []struct {
		name    string
		enabled bool
	}{
		{name: "ghostty 1.4.0", enabled: true},
		{name: "WezTerm 20260101"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &imageTestRunner{images: map[string][]byte{"chart": imageTestPNG(t)}}
			m, output, _ := newImageTestModel(t, runner, termimage.None)
			m.images.awaitTerminalVersion(output, termimage.CellSize{Width: 10, Height: 20})
			m.entries = imageTestEntries()
			batch, ok := m.Init()().(tea.BatchMsg)
			require.True(t, ok)
			assert.True(t, slices.ContainsFunc(batch, func(cmd tea.Cmd) bool {
				return reflect.ValueOf(cmd).Pointer() == reflect.ValueOf(tea.RequestTerminalVersion).Pointer()
			}))

			updated, cmd := m.Update(tea.TerminalVersionMsg{Name: test.name})
			m = updated.(model)
			assert.Equal(t, test.enabled, m.images.protocolEnabled())
			assert.Equal(t, test.enabled, strings.Contains(ansi.Strip(m.viewport.GetContent()), "Loading image"))
			if test.enabled {
				assert.Equal(t, tea.ColorProfileMsg{Profile: colorprofile.TrueColor}, cmd())
			}
			_, cmd = m.Update(tea.TerminalVersionMsg{Name: "kitty(0.48.2)"})
			assert.Nil(t, cmd, "only the first reply is used")
		})
	}
}

func TestImageSizesRequestedInOnePassAreKept(t *testing.T) {
	runner := &imageTestRunner{images: map[string][]byte{"chart": imageTestPNG(t)}}
	m, _, _ := newImageTestModel(t, runner, termimage.Kitty)
	attachment := tooltypes.ToolAttachment{Type: "image", ShortCode: "chart", Width: 300, Height: 200}
	// Indentation gives the same image three widths on a narrow terminal.
	renderPass := func() {
		m.images.beginPass()
		for i, cols := range []int{20, 18, 16} {
			m.images.lines(attachment, cols, 30, i*20, nil)
		}
		m.images.endPass()
		m.images.loadNear(0, 60)
	}
	ids := func() []uint32 {
		m.images.mu.Lock()
		defer m.images.mu.Unlock()
		return slices.Sorted(maps.Keys(m.images.byID))
	}

	drainImageUpdates(t, &m, renderPass, func() bool { return renditionsReady(m.images, 3) })
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
		tools = append(tools, toolCall{name: "draw_chart", done: true, structured: &tooltypes.StructuredToolResult{
			ToolName: "draw_chart",
			Success:  true,
			Attachments: []tooltypes.ToolAttachment{{
				Type: "image", ArtifactID: code, ShortCode: code, ViewURL: "/i/" + code, Width: 300, Height: 200,
			}},
		}})
	}
	m, _, _ := newImageTestModel(t, runner, termimage.Kitty)
	m.entries = []chatEntry{{kind: entryAssistant, blocks: []assistantBlock{{kind: blockTools, tools: tools}}}}

	m.refreshViewport(true)
	waitForImages(t, &m, func() bool { return m.images.sources["image7"].data != nil })
	assert.Zero(t, runner.fetchCount("image0"), "images far above the viewport are not loaded")
	lines := m.viewport.TotalLineCount()

	m.viewport.GotoTop()
	m.autoFollow = false // as scrolling up does
	m.loadVisibleImages()
	waitForImages(t, &m, func() bool { return m.images.sources["image0"].data != nil })
	assert.Equal(t, lines, m.viewport.TotalLineCount(), "rows are reserved before images load")
}

func TestImagesLoadingAboveTheViewportKeepTheTextInPlace(t *testing.T) {
	var paragraphs []string
	for i := range 60 {
		paragraphs = append(paragraphs, fmt.Sprintf("Paragraph %d.", i))
	}
	text := strings.Join(paragraphs, "\n\n")
	const missing = "missingImageCode000000"
	for _, test := range []struct {
		name  string
		entry chatEntry
	}{
		{
			name:  "a reply image grows once loaded",
			entry: chatEntry{kind: entryAssistant, blocks: []assistantBlock{{kind: blockText, text: "![bridge](/i/" + testShortCode + ")\n\n" + text}}},
		},
		{
			name:  "a reply image in a sentence fails",
			entry: chatEntry{kind: entryAssistant, blocks: []assistantBlock{{kind: blockText, text: "Here: ![missing](/i/" + missing + ") and more.\n\n" + text}}},
		},
		{
			name: "a tool image shrinks once it fails",
			entry: chatEntry{kind: entryAssistant, blocks: []assistantBlock{
				{kind: blockTools, tools: []toolCall{{name: "draw_chart", done: true, structured: &tooltypes.StructuredToolResult{
					ToolName:    "draw_chart",
					Success:     true,
					Attachments: []tooltypes.ToolAttachment{{Type: "image", ShortCode: missing, ViewURL: "/i/" + missing, Width: 300, Height: 200}},
				}}}},
				{kind: blockText, text: text},
			}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &imageTestRunner{images: map[string][]byte{testShortCode: imageTestPNG(t)}}
			m, _, _ := newImageTestModel(t, runner, termimage.Kitty)
			m.entries = []chatEntry{test.entry}
			m.refreshViewport(true)
			topLine := func() string { return strings.TrimSpace(ansi.Strip(strings.Split(m.viewport.View(), "\n")[0])) }

			m.autoFollow = false
			m.viewport.SetYOffset(20)
			for !strings.Contains(topLine(), "Paragraph") {
				m.viewport.ScrollDown(1)
			}
			reading := topLine()
			m.loadVisibleImages()
			waitForImages(t, &m, func() bool {
				source := m.images.sources[missing]
				return renditionsReady(m.images, 1) || (source != nil && source.err != nil)
			})
			m.refreshViewport(false) // as the pending refresh would
			assert.Equal(t, reading, topLine())
		})
	}
}

func TestImageFetchesAreBounded(t *testing.T) {
	runner := &imageTestRunner{images: map[string][]byte{}, gate: make(chan struct{})}
	m, _, _ := newImageTestModel(t, runner, termimage.Kitty)
	m.images.beginPass()
	for i := range 6 {
		code := fmt.Sprintf("image%d", i)
		runner.images[code] = imageTestPNG(t)
		m.images.lines(tooltypes.ToolAttachment{Type: "image", ShortCode: code}, 30, 10, i, nil)
	}
	m.images.endPass()
	m.images.loadNear(0, 10)

	time.Sleep(50 * time.Millisecond)
	close(runner.gate)
	for range 6 {
		<-m.runCh
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	assert.Len(t, runner.fetched, 6)
	assert.Equal(t, maxImageFetches, runner.maxActive)
}

func TestQueuedImageFetchesSkipImagesAwayFromTheViewport(t *testing.T) {
	runner := &imageTestRunner{images: map[string][]byte{}, gate: make(chan struct{})}
	m, _, _ := newImageTestModel(t, runner, termimage.Kitty)
	codes := []string{"image0", "image1", "image2", "image3", "image4", "image5", "visible"}
	for _, code := range codes {
		runner.images[code] = imageTestPNG(t)
	}
	place := func(near func(string) bool) {
		m.images.beginPass()
		for i, code := range codes {
			line := 1000 + i*10
			if near(code) {
				line = i
			}
			m.images.lines(tooltypes.ToolAttachment{Type: "image", ShortCode: code}, 30, 10, line, nil)
		}
		m.images.endPass()
		m.images.loadNear(0, 10)
	}

	place(func(code string) bool { return code != "visible" })
	require.Eventually(t, func() bool {
		runner.mu.Lock()
		defer runner.mu.Unlock()
		return runner.active == maxImageFetches
	}, 5*time.Second, 5*time.Millisecond)
	place(func(code string) bool { return code == "visible" })
	close(runner.gate)
	require.Eventually(t, func() bool {
		m.images.mu.Lock()
		defer m.images.mu.Unlock()
		for _, source := range m.images.sources {
			if source.loading {
				return false
			}
		}
		return m.images.sources["visible"].data != nil
	}, 5*time.Second, 5*time.Millisecond)
	runner.mu.Lock()
	defer runner.mu.Unlock()
	assert.Len(t, runner.fetched, maxImageFetches+1, "only downloads in flight and the visible image are fetched")
}

func TestQueuedImageRendersSkipImagesAwayFromTheViewport(t *testing.T) {
	m, _, _ := newImageTestModel(t, &imageTestRunner{}, termimage.Kitty)
	m.images.mu.Lock()
	for _, code := range []string{"old", "new"} {
		m.images.sources[code] = &imageSource{shortCode: code, data: imageTestPNG(t), width: 300, height: 200}
	}
	m.images.mu.Unlock()
	place := func(near string) {
		m.images.beginPass()
		for _, code := range []string{"old", "new"} {
			line := 1000
			if code == near {
				line = 0
			}
			m.images.lines(tooltypes.ToolAttachment{Type: "image", ShortCode: code}, 30, 10, line, nil)
		}
		m.images.endPass()
		m.images.loadNear(0, 10)
	}

	m.images.renders <- struct{}{} // a decode is running
	place("old")
	m.images.cacheBytes = 1
	place("new")
	<-m.images.renders
	require.Eventually(t, func() bool {
		m.images.mu.Lock()
		defer m.images.mu.Unlock()
		renditions := m.images.sources["new"].renditions
		return len(renditions) == 1 && renditions[0].rendition != nil
	}, 5*time.Second, 5*time.Millisecond)
	m.images.mu.Lock()
	defer m.images.mu.Unlock()
	assert.Empty(t, m.images.sources["old"].renditions, "a decode queued before scrolling away is skipped")
	assert.Nil(t, m.images.sources["old"].data)
}

func TestQueuedImageRenderRefetchesAnEvictedImage(t *testing.T) {
	runner := &imageTestRunner{images: map[string][]byte{"image": imageTestPNG(t)}}
	m, _, _ := newImageTestModel(t, runner, termimage.Kitty)
	m.images.mu.Lock()
	m.images.sources["image"] = &imageSource{shortCode: "image", data: imageTestPNG(t), width: 300, height: 200}
	m.images.mu.Unlock()
	place := func(line int) func() {
		return func() {
			m.images.beginPass()
			m.images.lines(tooltypes.ToolAttachment{Type: "image", ShortCode: "image"}, 30, 10, line, nil)
			m.images.endPass()
			m.images.loadNear(0, 10)
		}
	}

	m.images.renders <- struct{}{} // a decode is running
	place(0)()
	m.images.cacheBytes = 1
	place(1000)() // scrolls away, evicting the download
	place(0)()    // and back while the decode is still queued
	<-m.images.renders
	drainImageUpdates(t, &m, place(0), func() bool { return renditionsReady(m.images, 1) })
	assert.Equal(t, 1, runner.fetchCount("image"))
}

func TestImageCacheEvictsImagesAwayFromTheViewport(t *testing.T) {
	runner := &imageTestRunner{images: map[string][]byte{"near": imageTestPNG(t), "far": imageTestPNG(t)}}
	m, _, _ := newImageTestModel(t, runner, termimage.Kitty)
	m.images.cacheBytes = 1
	attachment := func(code string) tooltypes.ToolAttachment {
		return tooltypes.ToolAttachment{Type: "image", ShortCode: code, Width: 300, Height: 200}
	}
	renderPass := func(top int) func() {
		return func() {
			m.images.beginPass()
			m.images.lines(attachment("near"), 30, 10, 0, nil)
			m.images.lines(attachment("far"), 30, 10, 100, nil)
			m.images.endPass()
			m.images.loadNear(top, 10)
		}
	}
	rendered := func(code string) func() bool {
		return func() bool {
			source := m.images.sources[code]
			return source != nil && len(source.renditions) == 1 && source.renditions[0].rendition != nil
		}
	}

	drainImageUpdates(t, &m, renderPass(0), rendered("near"))
	drainImageUpdates(t, &m, renderPass(100), rendered("far"))
	m.images.mu.Lock()
	assert.Nil(t, m.images.sources["near"].data, "the download away from the viewport is freed")
	assert.Empty(t, m.images.sources["near"].renditions, "the rendition away from the viewport is freed")
	assert.NotNil(t, m.images.sources["far"].data)
	m.images.mu.Unlock()

	assert.Len(t, m.images.lines(attachment("near"), 30, 10, 0, nil), 10, "an evicted image keeps its rows reserved")
	drainImageUpdates(t, &m, renderPass(0), rendered("near"))
	assert.Equal(t, 2, runner.fetchCount("near"), "scrolling back downloads it again")
}

type imageTestRunner struct {
	images map[string][]byte
	// gate, when set, holds each fetch until it is closed.
	gate              chan struct{}
	mu                sync.Mutex
	fetched           map[string]int
	active, maxActive int
}

func (*imageTestRunner) Run(context.Context, chat.ChatRequest, chat.ChatEventSink) (string, error) {
	return "", nil
}

func (r *imageTestRunner) FetchImage(_ context.Context, shortCode string) ([]byte, error) {
	r.mu.Lock()
	if r.fetched == nil {
		r.fetched = map[string]int{}
	}
	r.fetched[shortCode]++
	r.active++
	r.maxActive = max(r.maxActive, r.active)
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

// imageTestEntries has a generated image, shown under its header, and a
// collapsed view_image result.
func imageTestEntries() []chatEntry {
	return []chatEntry{{kind: entryAssistant, blocks: []assistantBlock{{kind: blockTools, tools: []toolCall{
		{name: "draw_chart", done: true, structured: &tooltypes.StructuredToolResult{
			ToolName: "draw_chart",
			Success:  true,
			Attachments: []tooltypes.ToolAttachment{{
				Type: "image", ArtifactID: "artifact-1", ShortCode: "chart", ViewURL: "/i/chart", Width: 300, Height: 200,
			}},
		}},
		{name: "view_image", input: "{\"path\":\"/tmp/screenshot.png\"}", done: true, structured: &tooltypes.StructuredToolResult{
			ToolName: "view_image",
			Success:  true,
			Attachments: []tooltypes.ToolAttachment{{
				Type: "image", ArtifactID: "artifact-2", ShortCode: "screenshot", ViewURL: "/i/screenshot",
			}},
		}},
	}}}}}
}

// waitForImages handles image updates as the program would until ready, which
// runs with the image lock held, reports true.
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

// drainImageUpdates repeats step after each image update until ready, which
// runs with the image lock held, reports true.
func drainImageUpdates(t *testing.T, m *model, step func(), ready func() bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		step()
		m.images.mu.Lock()
		done := ready()
		m.images.mu.Unlock()
		if done {
			return
		}
		select {
		case <-m.runCh:
		case <-deadline:
			t.Fatal("timed out waiting for image updates")
		}
	}
}

func renditionsReady(images *tuiImages, count int) bool {
	if len(images.byID) != count {
		return false
	}
	for _, rendition := range images.byID {
		if rendition.rendition == nil {
			return false
		}
	}
	return true
}

// linkedCells maps the hyperlink targets of cells in rendered output, as a
// terminal would draw it, to the text they cover.
func linkedCells(rendered string) map[string]string {
	lines := strings.Split(rendered, "\n")
	width := 0
	for _, line := range lines {
		width = max(width, ansi.StringWidth(line))
	}
	buffer := uv.NewScreenBuffer(width, len(lines))
	uv.NewStyledString(rendered).Draw(buffer, buffer.Bounds())
	links := map[string]string{}
	for y := range lines {
		for x := range width {
			if cell := buffer.CellAt(x, y); cell != nil && cell.Link.URL != "" {
				links[cell.Link.URL] += cell.Content
			}
		}
	}
	return links
}

func firstImageID(m *model) uint32 {
	for id := range m.images.byID {
		return id
	}
	return 0
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
