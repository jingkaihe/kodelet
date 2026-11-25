package tui

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/runner/localstate"
	"github.com/jingkaihe/kodelet/pkg/vision"
	"github.com/pkg/errors"
	"golang.design/x/clipboard"
)

type clipboardReader func(context.Context, clipboard.Format) ([]byte, error)

func readNativeClipboard(ctx context.Context, format clipboard.Format) ([]byte, error) {
	if err := clipboard.Init(); err != nil {
		return nil, err
	}
	return clipboard.Read(ctx, format)
}

type clipboardPasteMsg struct {
	state    *conversationState
	text     string
	files    []string // Original file paths, never owned by the TUI.
	tempPath string
	err      error
}

type tuiClipboard struct {
	read      clipboardReader
	readFiles func(context.Context) ([]string, error)
	mu        sync.Mutex
}

func newTUIClipboard() *tuiClipboard {
	c := &tuiClipboard{read: readDesktopClipboard}
	if runtime.GOOS == "darwin" {
		c.readFiles = func(ctx context.Context) ([]string, error) {
			if err := clipboard.Init(); err != nil {
				return nil, err
			}
			return clipboard.ReadFiles(ctx)
		}
	}
	return c
}

type clipboardContents struct {
	format clipboard.Format
	data   []byte
	files  []string
	err    error
}

func (c *tuiClipboard) readPreferred(ctx context.Context) clipboardContents {
	// Finder may offer both a file URL and its icon as an image.
	if c.readFiles != nil {
		files, err := c.readFiles(ctx)
		if err != nil && !errors.Is(err, clipboard.ErrNoData) {
			return clipboardContents{err: err}
		}
		if len(files) > 0 {
			for _, path := range files {
				if !filepath.IsAbs(path) || strings.IndexFunc(path, unicode.IsControl) >= 0 {
					return clipboardContents{err: errors.New("invalid clipboard file path")}
				}
			}
			return clipboardContents{format: clipboard.FmtFiles, files: files}
		}
	}
	format := clipboard.FmtImage
	data, err := c.read(ctx, format)
	if errors.Is(err, clipboard.ErrNoData) && ctx.Err() == nil {
		format = clipboard.FmtText
		data, err = c.read(ctx, format)
	}
	return clipboardContents{format: format, data: data, err: err}
}

func (c *tuiClipboard) contents(ctx context.Context) clipboardContents {
	// Native reads can ignore cancellation and hold the library's global lock.
	// Bound the wait for the UI without starting more workers behind a stuck read.
	if !c.mu.TryLock() {
		return clipboardContents{err: errors.New("the previous clipboard read has not finished")}
	}
	done := make(chan clipboardContents, 1)
	go func() {
		result := c.readPreferred(ctx)
		c.mu.Unlock()
		done <- result
	}()
	select {
	case <-ctx.Done():
		return clipboardContents{err: ctx.Err()}
	case result := <-done:
		return result
	}
}

func (m *model) pasteClipboard() tea.Cmd {
	state := m.conversationState
	state.pastePending = true
	ctx, source := m.ctx, m.clipboard
	runner, _ := m.runner.(runnerStatusProvider)
	target := m.workspaceTargetForState(state)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		msg := clipboardPasteMsg{state: state}
		content := source.contents(ctx)
		switch {
		case ctx.Err() != nil:
			msg.err = errors.Wrap(ctx.Err(), "reading clipboard")
		case content.err != nil:
			msg.err = content.err
		case content.format == clipboard.FmtFiles:
			msg.err = requireLocalClipboardRunner(ctx, runner, target)
			if msg.err == nil {
				msg.files = content.files
			}
		case content.format == clipboard.FmtImage:
			msg.tempPath, msg.err = stageClipboardImage(ctx, content.data, runner, target)
		default:
			msg.text = string(content.data)
		}
		return msg
	}
}

func stageClipboardImage(
	ctx context.Context,
	data []byte,
	runner runnerStatusProvider,
	target chat.WorkspaceTarget,
) (string, error) {
	format, err := vision.ImageInputFormat(data)
	if err != nil {
		return "", err
	}
	if format != "png" {
		return "", errors.New("clipboard did not return a PNG image")
	}
	if err := requireLocalClipboardRunner(ctx, runner, target); err != nil {
		return "", err
	}
	// The turn can outlive this TUI, so leave successfully pasted files to OS temp cleanup.
	return writeComposerTempFile("kodelet-paste-*.png", data)
}

func requireLocalClipboardRunner(ctx context.Context, runner runnerStatusProvider, target chat.WorkspaceTarget) error {
	// Even a loopback URL can point at a remote runner.
	local, err := localstate.LoadDefaultHostIdentity()
	if err == nil && runner != nil {
		status, err := runner.RunnerStatus(ctx, target)
		if status.Runner != nil && status.Runner.Host.InstanceID == local.InstanceID {
			return nil
		}
		if err != nil {
			return errors.Wrap(err, "could not identify the runner for these paths")
		}
	}
	return errors.New("pasting a file path requires a runner on this machine")
}

func (m *model) finishClipboardPaste(msg clipboardPasteMsg) tea.Cmd {
	msg.state.pastePending = false
	if msg.state != m.conversationState || !m.composerAcceptsPaste() {
		if msg.tempPath != "" {
			_ = os.Remove(msg.tempPath)
		}
		return nil
	}
	switch {
	case errors.Is(msg.err, clipboard.ErrUnavailable), errors.Is(msg.err, clipboard.ErrNoData):
		return nil
	case msg.err != nil:
		detail := msg.err.Error()
		if errors.Is(msg.err, context.DeadlineExceeded) {
			detail = "Clipboard paste timed out. Try copying the item again."
		}
		return m.addUINotification(uiNotification{
			level: uiNotificationWarning, title: "Paste failed", message: detail,
		})
	case msg.tempPath != "":
		msg.text = msg.tempPath
	case len(msg.files) > 0:
		msg.text = strings.Join(msg.files, "\n")
	case msg.text == "":
		return nil
	}
	return m.updateComposer(tea.PasteMsg{Content: msg.text})
}

func (m model) composerAcceptsPaste() bool {
	if m.startupPending || m.startupErr != nil || m.status == "editing" {
		return false
	}
	if m.audio != nil && m.audio.mode == "voice" {
		return false // Dictation freezes the draft until accepted or cancelled.
	}
	_, surfaceFocused := m.focusedExtensionSurfaceKey()
	return !surfaceFocused && !m.infoDialogOpen() && m.activeUIPrompt == nil &&
		m.conversationPicker == nil && !m.modelPickerOpen && !m.reasoningPickerOpen && m.historySearch == nil
}
