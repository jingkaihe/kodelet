package tui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
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
}

func (*imageTestRunner) Run(context.Context, chat.ChatRequest, chat.ChatEventSink) (string, error) {
	return "", nil
}

func (r *imageTestRunner) FetchImage(_ context.Context, shortCode string) ([]byte, error) {
	r.fetches.Add(1)
	if data, ok := r.images[shortCode]; ok {
		return data, nil
	}
	return nil, errors.New("server returned HTTP 404")
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

func TestSplitMarkdownImages(t *testing.T) {
	const code = "bFMUeAa9fd0DT7VVvGwxpg"
	fence := strings.Repeat("~", 3)
	fenced := fence + "\n![x](/i/" + code + ")\n" + fence
	for _, test := range []struct {
		name string
		text string
		want []markdownSegment
	}{
		{name: "no images", text: "plain text", want: []markdownSegment{{text: "plain text"}}},
		{
			name: "standalone image",
			text: "Here it is:\n\n![London Bridge](/i/" + code + ")\n\nAI-generated.",
			want: []markdownSegment{
				{text: "Here it is:\n"},
				{text: "![London Bridge](/i/" + code + ")", shortCode: code},
				{text: "\nAI-generated."},
			},
		},
		{
			name: "absolute link with title",
			text: "![chart](https://kodelet.example/base/i/" + code + " \"Chart\")",
			want: []markdownSegment{{text: "![chart](https://kodelet.example/base/i/" + code + " \"Chart\")", shortCode: code}},
		},
		{name: "inline image", text: "See ![x](/i/" + code + ") here", want: []markdownSegment{{text: "See ![x](/i/" + code + ") here"}}},
		{name: "code fence", text: fenced, want: []markdownSegment{{text: fenced}}},
		{name: "other image", text: "![x](https://example.com/cat.png)", want: []markdownSegment{{text: "![x](https://example.com/cat.png)"}}},
		{name: "absolute link needs a server short code", text: "![x](https://example.com/i/cat)", want: []markdownSegment{{text: "![x](https://example.com/i/cat)"}}},
		{name: "query string", text: "![x](/i/" + code + "?download=1)", want: []markdownSegment{{text: "![x](/i/" + code + "?download=1)"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, splitMarkdownImages(test.text))
		})
	}
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
	assert.Empty(t, lines[1])
	id := firstImageID(&m)
	for row := range 10 {
		assert.Equal(t, termimage.PlaceholderLine(termimage.Kitty, id, row, 30), lines[2+row])
	}
	assert.Empty(t, lines[12])
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

func TestAbsoluteImageLinks(t *testing.T) {
	const code = "bFMUeAa9fd0DT7VVvGwxpg"
	fence := strings.Repeat("~", 3)
	for _, test := range []struct {
		name, text, server, want string
	}{
		{
			name:   "image and link",
			text:   "![bridge](/i/" + code + ")\nOpen [it](/i/" + code + " \"Bridge\") or <" + code + ">.",
			server: "http://localhost:8080/",
			want:   "![bridge](http://localhost:8080/i/" + code + ")\nOpen [it](http://localhost:8080/i/" + code + " \"Bridge\") or <" + code + ">.",
		},
		{name: "server base path", text: "[x](/i/abc)", server: "https://kodelet.example/base", want: "[x](https://kodelet.example/base/i/abc)"},
		{name: "angle brackets", text: "[x](</i/abc>)", server: "https://kodelet.example", want: "[x](<https://kodelet.example/i/abc>)"},
		{name: "absolute links stay", text: "[x](https://other.example/i/abc)", server: "https://kodelet.example", want: "[x](https://other.example/i/abc)"},
		{name: "other paths stay", text: "[x](/c/abc)", server: "https://kodelet.example", want: "[x](/c/abc)"},
		{name: "code fences stay", text: fence + "\n[x](/i/abc)\n" + fence, server: "https://kodelet.example", want: fence + "\n[x](/i/abc)\n" + fence},
		{name: "no server", text: "[x](/i/abc)", want: "[x](/i/abc)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, absoluteImageLinks(test.text, test.server))
		})
	}
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
