package tui

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"testing/synctest"

	tea "charm.land/bubbletea/v2"
	"github.com/jingkaihe/kodelet/pkg/llm/base"
	"github.com/jingkaihe/kodelet/pkg/runner/localstate"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/jingkaihe/kodelet/pkg/runner/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.design/x/clipboard"
)

func TestPasteImageIntoDraft(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	store, err := localstate.NewStore()
	require.NoError(t, err)
	local, err := store.LoadOrCreateHostIdentity()
	require.NoError(t, err)
	runner := &aboutRunner{}
	runner.runnerStatus.Runner = &registry.Runner{Host: protocol.Host{InstanceID: local.InstanceID}}
	m := newThemeTestModel(t, Config{Runner: runner, Remote: true})
	png := imageTestPNG(t)
	m.clipboard.read = func(_ context.Context, format clipboard.Format) ([]byte, error) {
		assert.Equal(t, clipboard.FmtImage, format)
		return png, nil
	}
	m.textarea.SetValue("Inspect ")
	updated, paste := m.Update(keyPressWithMod('v', tea.ModCtrl))
	require.NotNil(t, paste)
	result := paste().(clipboardPasteMsg)
	require.NoError(t, result.err)
	require.True(t, filepath.IsAbs(result.path))
	data, err := os.ReadFile(result.path)
	require.NoError(t, err)
	assert.Equal(t, png, data)
	info, err := os.Stat(result.path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	updated, _ = updated.Update(result)
	draft := "Inspect " + result.path
	assert.Equal(t, draft, updated.(model).textarea.Value())
	assert.False(t, updated.(model).running)
	_, send := updated.Update(keyPress(tea.KeyEnter))
	require.NotNil(t, send)
	send()
	receiveRunMsg(t, m.runCh)
	assert.Equal(t, draft, runner.req.Message)
}

func TestPasteTextWithoutLocalRunner(t *testing.T) {
	m := newThemeTestModel(t, Config{Runner: &recordingRunner{}, Remote: true})
	m.clipboard.read = func(_ context.Context, format clipboard.Format) ([]byte, error) {
		if format == clipboard.FmtImage {
			return nil, clipboard.ErrNoData
		}
		return []byte("one\ntwo"), nil
	}
	key := keyPressWithMod('v', tea.ModCtrl)
	if runtime.GOOS == "darwin" {
		key = keyPressWithMod('b', tea.ModSuper)
	}
	updated, paste := m.Update(key)
	require.NotNil(t, paste)
	updated, _ = updated.Update(paste())
	assert.Equal(t, "one\ntwo", updated.(model).textarea.Value())
}

func TestStalledClipboardReadReleasesComposer(t *testing.T) {
	m := newThemeTestModel(t, Config{Runner: &recordingRunner{}, Remote: true})
	m.textarea.SetValue("unfinished draft")
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		defer close(release)
		calls := 0
		m.clipboard.read = func(context.Context, clipboard.Format) ([]byte, error) {
			calls++
			<-release // Simulate a backend that ignores its context.
			return nil, clipboard.ErrNoData
		}
		pasteKey := keyPressWithMod('v', tea.ModCtrl)
		updated, paste := m.Update(pasteKey)
		assert.Contains(t, updated.(model).inputBottomLeftLabel(), "Reading clipboard")
		_, send := updated.Update(keyPress(tea.KeyEnter))
		require.Nil(t, send)

		result := paste().(clipboardPasteMsg) // Virtual time advances to the timeout.
		require.ErrorIs(t, result.err, context.DeadlineExceeded)
		updated, _ = updated.Update(result)
		updated, retry := updated.Update(pasteKey)
		result = retry().(clipboardPasteMsg)
		require.ErrorContains(t, result.err, "previous clipboard read")
		synctest.Wait()
		assert.Equal(t, 1, calls)
		updated, _ = updated.Update(result)
		assert.Equal(t, "unfinished draft", updated.(model).textarea.Value())
		_, send = updated.Update(keyPress(tea.KeyEnter))
		require.NotNil(t, send)
	})
}

func TestPasteDiscardsStagedImageWhenComposerLosesFocus(t *testing.T) {
	m := newThemeTestModel(t, Config{})
	m.textarea.SetValue("keep this draft")
	m.pastePending = true
	m.openShortcutsDialog()
	path := filepath.Join(t.TempDir(), "clipboard.png")
	require.NoError(t, os.WriteFile(path, imageTestPNG(t), 0o600))
	updated, _ := m.Update(clipboardPasteMsg{state: m.conversationState, path: path})
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
		{"remote runner", imageTestPNG(t), "runner on this machine"},
		{"invalid PNG", []byte("not PNG"), "PNG image"},
		{"oversized", make([]byte, base.MaxImageFileSize+1), "file limit"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newThemeTestModel(t, Config{Runner: runner, Remote: true})
			m.textarea.SetValue("keep this draft")
			m.clipboard.read = func(context.Context, clipboard.Format) ([]byte, error) {
				return tt.data, nil
			}
			paste := m.pasteClipboard()
			result := paste().(clipboardPasteMsg)
			require.Empty(t, result.path)
			m.finishClipboardPaste(result)
			assert.Equal(t, "keep this draft", m.textarea.Value())
			require.Len(t, m.uiNotifications, 1)
			assert.Contains(t, m.uiNotifications[0].message, tt.want)
		})
	}
}
