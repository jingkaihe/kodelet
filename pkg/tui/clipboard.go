package tui

import (
	"context"
	"os"
	"sync"
	"time"

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
	state *conversationState
	text  string
	path  string
	err   error
}

type tuiClipboard struct {
	read clipboardReader
	mu   sync.Mutex
}

type clipboardContents struct {
	format clipboard.Format
	data   []byte
	err    error
}

func (c *tuiClipboard) contents(ctx context.Context) clipboardContents {
	// Native reads can ignore cancellation and hold the library's global lock.
	// Bound the wait for the UI without starting more workers behind a stuck read.
	if !c.mu.TryLock() {
		return clipboardContents{err: errors.New("the previous clipboard read has not finished")}
	}
	done := make(chan clipboardContents, 1)
	go func() {
		format := clipboard.FmtImage
		data, err := c.read(ctx, format)
		if errors.Is(err, clipboard.ErrNoData) && ctx.Err() == nil {
			format = clipboard.FmtText
			data, err = c.read(ctx, format)
		}
		c.mu.Unlock()
		done <- clipboardContents{format: format, data: data, err: err}
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
		case content.format == clipboard.FmtImage:
			msg.path, msg.err = stageClipboardImage(ctx, content.data, runner, target)
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
	// Only insert paths on the runner's own machine; even a loopback URL
	// can point at a remote runner. The turn can outlive this TUI, so leave
	// successfully pasted files to OS temp cleanup.
	local, err := localstate.LoadDefaultHostIdentity()
	if err == nil && runner != nil {
		status, err := runner.RunnerStatus(ctx, target)
		if status.Runner != nil && status.Runner.Host.InstanceID == local.InstanceID {
			return writeComposerTempFile("kodelet-paste-*.png", data)
		}
		if err != nil {
			return "", errors.Wrap(err, "could not identify the clipboard image's runner")
		}
	}
	return "", errors.New("pasting an image path requires a runner on this machine")
}

func (m *model) finishClipboardPaste(msg clipboardPasteMsg) tea.Cmd {
	msg.state.pastePending = false
	if msg.state != m.conversationState || !m.composerAcceptsPaste() {
		if msg.path != "" {
			_ = os.Remove(msg.path)
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
	case msg.path != "":
		msg.text = msg.path
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
