package tui

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
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
	m.readClipboard = func(_ context.Context, format clipboard.Format) ([]byte, error) {
		require.Equal(t, clipboard.FmtImage, format)
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
	m.readClipboard = func(_ context.Context, format clipboard.Format) ([]byte, error) {
		if format == clipboard.FmtImage {
			return nil, clipboard.ErrNoData
		}
		return []byte("one\ntwo"), nil
	}
	updated, paste := m.Update(keyPressWithMod('b', tea.ModSuper))
	require.NotNil(t, paste)
	updated, _ = updated.Update(paste())
	assert.Equal(t, "one\ntwo", updated.(model).textarea.Value())
}
