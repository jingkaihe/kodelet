package tui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/gif"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/runner/localstate"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/jingkaihe/kodelet/pkg/runner/registry"
	"github.com/jingkaihe/kodelet/pkg/vision"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func installTestClipboardHelper(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	const prelude = `#!/bin/sh
set -eu
[ "$#" -eq 1 ] && [ "$1" = read-v1 ] || exit 2
`
	path := filepath.Join(dir, "kodelet-clipboard")
	require.NoError(t, os.WriteFile(path, []byte(prelude+body), 0o700))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func serveClipboardResponse(t *testing.T, kind byte, data []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "response")
	require.NoError(t, os.WriteFile(path, append([]byte{kind}, data...), 0o600))
	t.Setenv("KODELET_TEST_CLIPBOARD_RESPONSE", path)
	installTestClipboardHelper(t, `exec /bin/cat "$KODELET_TEST_CLIPBOARD_RESPONSE"`)
}

func localClipboardRunner(t *testing.T) *aboutRunner {
	t.Helper()
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	store, err := localstate.NewStore()
	require.NoError(t, err)
	local, err := store.LoadOrCreateHostIdentity()
	require.NoError(t, err)
	runner := &aboutRunner{}
	runner.runnerStatus.Runner = &registry.Runner{Host: protocol.Host{InstanceID: local.InstanceID}}
	return runner
}

func TestPasteImageIntoDraft(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	runner := localClipboardRunner(t)
	m := newThemeTestModel(t, Config{Runner: runner, Remote: true})
	png := imageTestPNG(t)
	serveClipboardResponse(t, 'I', png)
	m.textarea.SetValue("Inspect ")
	updated, paste := m.Update(keyPressWithMod('v', tea.ModCtrl))
	require.NotNil(t, paste)
	result := paste().(clipboardPasteMsg)
	require.NoError(t, result.err)
	require.True(t, filepath.IsAbs(result.tempPath))
	data, err := os.ReadFile(result.tempPath)
	require.NoError(t, err)
	assert.Equal(t, png, data)
	info, err := os.Stat(result.tempPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	updated, _ = updated.Update(result)
	draft := "Inspect " + result.tempPath
	assert.Equal(t, draft, updated.(model).textarea.Value())
	assert.False(t, updated.(model).running)
	_, send := updated.Update(keyPress(tea.KeyEnter))
	require.NotNil(t, send)
	send()
	receiveRunMsg(t, m.runCh)
	assert.Equal(t, draft, runner.req.Message)
}

func TestClipboardImagePreservesEncoding(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	runner := localClipboardRunner(t)
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	var jpg, gifData bytes.Buffer
	require.NoError(t, jpeg.Encode(&jpg, img, nil))
	require.NoError(t, gif.Encode(&gifData, img, nil))
	// A single lossless WebP pixel (there's no WebP encoder in the Go standard library).
	webp, err := base64.StdEncoding.DecodeString("UklGRh4AAABXRUJQVlA4TBEAAAAvAAAAAAdQsYZ1rP+BiOh/AAA=")
	require.NoError(t, err)
	for ext, data := range map[string][]byte{".jpg": jpg.Bytes(), ".gif": gifData.Bytes(), ".webp": webp} {
		t.Run(ext, func(t *testing.T) {
			path, err := stageClipboardImage(t.Context(), data, runner, chat.WorkspaceTarget{})
			require.NoError(t, err)
			assert.Equal(t, ext, filepath.Ext(path))
			got, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, data, got)
		})
	}
}

func TestPasteFinderFilesPreservesOriginals(t *testing.T) {
	runner := localClipboardRunner(t)
	m := newThemeTestModel(t, Config{Runner: runner, Remote: true})
	original := filepath.Join(t.TempDir(), "my document.pdf")
	require.NoError(t, os.WriteFile(original, []byte("original"), 0o600))
	files, err := json.Marshal([]string{original, "/some/other file"})
	require.NoError(t, err)
	serveClipboardResponse(t, 'F', files)
	result := m.pasteClipboard()().(clipboardPasteMsg)
	require.NoError(t, result.err)
	m.finishClipboardPaste(result)
	assert.Equal(t, original+"\n/some/other file", m.textarea.Value())

	m.openShortcutsDialog()
	m.finishClipboardPaste(result)
	assert.FileExists(t, original)
	runner.runnerStatus.Runner.Host.InstanceID = "another-machine"
	result = m.pasteClipboard()().(clipboardPasteMsg)
	require.ErrorIs(t, result.err, errClipboardNeedsLocalRunner)
}

func TestPasteTextWithoutLocalRunner(t *testing.T) {
	m := newThemeTestModel(t, Config{Runner: &recordingRunner{}, Remote: true})
	m.textarea.SetValue("prefix:")
	serveClipboardResponse(t, 'T', []byte("one\ntwo"))
	updated, paste := m.Update(keyPressWithMod('v', tea.ModCtrl))
	require.NotNil(t, paste)
	updated, _ = updated.Update(paste())
	assert.Equal(t, "prefix:one\ntwo", updated.(model).textarea.Value())
}

func TestPastePathAtCursor(t *testing.T) {
	for _, tt := range []struct {
		draft string
		col   int // Cursor on the last logical line.
		want  string
	}{
		{"first\n猫犬", 1, "first\n猫 /img 犬"},
		{"猫　犬", 2, "猫　/img 犬"},
		{"first\n犬", 0, "first\n/img 犬"},
		{"猫", 1, "猫 /img"},
	} {
		m := newThemeTestModel(t, Config{})
		m.textarea.SetValue(tt.draft)
		m.textarea.SetCursorColumn(tt.col)
		m.finishClipboardPaste(clipboardPasteMsg{state: m.conversationState, tempPath: "/img"})
		assert.Equal(t, tt.want, m.textarea.Value())
	}
}

func TestStalledClipboardHelperReleasesComposer(t *testing.T) {
	m := newThemeTestModel(t, Config{Runner: &recordingRunner{}, Remote: true})
	installTestClipboardHelper(t, `
if [ "$KODELET_TEST_CLIPBOARD_STALL" = yes ]; then exec /bin/sleep 5; fi
printf 'T recovered'
`)
	t.Setenv("KODELET_TEST_CLIPBOARD_STALL", "yes")
	m.clipboard = func(ctx context.Context) (clipboardContents, error) {
		ctx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
		defer cancel()
		return readClipboardHelper(ctx)
	}
	m.textarea.SetValue("unfinished draft")
	pasteKey := keyPressWithMod('v', tea.ModCtrl)
	updated, paste := m.Update(pasteKey)
	assert.Contains(t, updated.(model).inputBottomLeftLabel(), "Reading clipboard")
	_, send := updated.Update(keyPress(tea.KeyEnter))
	require.Nil(t, send)

	start := time.Now()
	result := paste().(clipboardPasteMsg)
	require.ErrorIs(t, result.err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 3*time.Second, "do not wait for the helper to exit on its own")
	updated, _ = updated.Update(result)
	after := updated.(model)
	require.Len(t, after.uiNotifications, 1)
	assert.Contains(t, after.uiNotifications[0].message, "Clipboard paste timed out")
	assert.Equal(t, "unfinished draft", after.textarea.Value())

	t.Setenv("KODELET_TEST_CLIPBOARD_STALL", "no")
	after.clipboard = readClipboardHelper
	updated, retry := after.Update(pasteKey)
	require.NotNil(t, retry)
	updated, _ = updated.Update(retry())
	assert.Equal(t, "unfinished draft recovered", updated.(model).textarea.Value())
	_, send = updated.Update(keyPress(tea.KeyEnter))
	require.NotNil(t, send)
}

func TestMissingClipboardHelperDoesNotBlockTerminalPaste(t *testing.T) {
	m := newThemeTestModel(t, Config{})
	t.Setenv("PATH", t.TempDir())
	updated, paste := m.Update(keyPressWithMod('v', tea.ModCtrl))
	require.NotNil(t, paste)
	result := paste().(clipboardPasteMsg)
	require.ErrorContains(t, result.err, "kodelet-clipboard")
	updated, _ = updated.Update(result)
	updated, _ = updated.Update(tea.PasteMsg{Content: "from the terminal"})
	assert.Equal(t, "from the terminal", updated.(model).textarea.Value())
}

func TestRejectsMalformedClipboardResponse(t *testing.T) {
	for _, tt := range []struct{ response, reason string }{
		{"?unexpected protocol", "unrecognized"},
		{`F["relative.pdf"]`, "invalid clipboard file path"},
		{"E" + strings.Repeat("x", maxClipboardErrorBytes+1), "size limit"},
	} {
		contents, err := readClipboardResponse(bufio.NewReader(strings.NewReader(tt.response)))
		require.ErrorContains(t, err, tt.reason)
		assert.Empty(t, contents)
	}
}

func TestFailedClipboardHelperReportsDiagnostic(t *testing.T) {
	installTestClipboardHelper(t, `
exec 1>&-
printf '\033[31munknown read-v1 command\033[0m\n' >&2
printf '%8192snot retained' '' >&2
exit 2
`)
	_, err := readClipboardHelper(t.Context())
	require.ErrorContains(t, err, "exit status 2")
	require.ErrorContains(t, err, "unknown read-v1 command")
	assert.NotContains(t, err.Error(), "\x1b")
	assert.NotContains(t, err.Error(), "not retained")
}

func TestPasteDiscardsStagedImageWhenComposerLosesFocus(t *testing.T) {
	m := newThemeTestModel(t, Config{})
	m.textarea.SetValue("keep this draft")
	m.pastePending = true
	m.openShortcutsDialog()
	path := filepath.Join(t.TempDir(), "clipboard.png")
	require.NoError(t, os.WriteFile(path, imageTestPNG(t), 0o600))
	updated, _ := m.Update(clipboardPasteMsg{state: m.conversationState, tempPath: path})
	assert.False(t, updated.(model).pastePending)
	assert.Equal(t, "keep this draft", updated.(model).textarea.Value())
	assert.NoFileExists(t, path)
}

func TestRejectedClipboardImageLeavesDraftAlone(t *testing.T) {
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	store, err := localstate.NewStore()
	require.NoError(t, err)
	_, err = store.LoadOrCreateHostIdentity()
	require.NoError(t, err)
	runner := &aboutRunner{}
	runner.runnerStatus.Runner = &registry.Runner{Host: protocol.Host{InstanceID: "another-machine"}}
	for _, tt := range []struct {
		name string
		data []byte
		want string
	}{
		{"remote runner", imageTestPNG(t), "To paste a file or image, use a runner on the same machine as this chat."},
		{"invalid image", []byte("not PNG"), "unsupported image"},
		{"oversized", make([]byte, vision.MaxImageInputBytes+1), "file limit"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newThemeTestModel(t, Config{Runner: runner, Remote: true})
			m.textarea.SetValue("keep this draft")
			m.clipboard = func(context.Context) (clipboardContents, error) {
				return clipboardContents{kind: 'I', data: tt.data}, nil
			}
			paste := m.pasteClipboard()
			result := paste().(clipboardPasteMsg)
			require.Empty(t, result.tempPath)
			m.finishClipboardPaste(result)
			assert.Equal(t, "keep this draft", m.textarea.Value())
			require.Len(t, m.uiNotifications, 1)
			assert.Contains(t, m.uiNotifications[0].message, tt.want)
		})
	}
}
