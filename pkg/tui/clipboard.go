package tui

import (
	"bytes"
	"context"
	"image/png"
	"io"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/llm/base"
	"github.com/jingkaihe/kodelet/pkg/runner/localstate"
	"github.com/pkg/errors"
	"golang.design/x/clipboard"
)

var errClipboardImageTooLarge = errors.New("image exceeds the 5 MiB file limit for view_image")

type clipboardReader func(context.Context, clipboard.Format) ([]byte, error)

func readDesktopClipboard(ctx context.Context, format clipboard.Format) ([]byte, error) {
	// Init is idempotent. Ordinary terminal paste doesn't need a desktop.
	if err := clipboard.Init(); err != nil {
		return nil, err
	}
	if runtime.GOOS != "linux" || os.Getenv("DISPLAY") == "" || os.Getenv("WAYLAND_DISPLAY") != "" {
		return clipboard.Read(ctx, format)
	}
	// Some X11 owners return their text even for a PNG request.
	formats, err := clipboard.Formats(ctx)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(formats, format) {
		return nil, clipboard.ErrNoData
	}
	// The Go X11 backend cannot receive incremental transfers. Reading with
	// it first can abort the transfer, so use xclip up front when available.
	if path, err := exec.LookPath("xclip"); err == nil {
		return readX11Selection(ctx, path, format)
	}
	data, err := clipboard.Read(ctx, format)
	// With INCR the initial property is a 32-bit size, not the image bytes.
	if format == clipboard.FmtImage && (err != nil || len(data) == 4) {
		return nil, errors.New("could not read the X11 image; install xclip for large clipboard transfers")
	}
	return data, err
}

func readX11Selection(ctx context.Context, path string, format clipboard.Format) ([]byte, error) {
	target := "UTF8_STRING"
	if format == clipboard.FmtImage {
		target = "image/png"
	}
	cmd := exec.CommandContext(ctx, path, "-selection", "clipboard", "-out", "-target", target)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	var src io.Reader = stdout
	if format == clipboard.FmtImage {
		src = io.LimitReader(stdout, base.MaxImageFileSize+1)
	}
	data, readErr := io.ReadAll(src)
	_ = stdout.Close()
	err = cmd.Wait()
	if readErr != nil {
		return nil, readErr
	}
	if format == clipboard.FmtImage && len(data) > base.MaxImageFileSize {
		return nil, errClipboardImageTooLarge
	}
	return data, err
}

type clipboardPasteMsg struct {
	state *conversationState
	text  string
	path  string
	err   error
}

func (m *model) pasteClipboard() tea.Cmd {
	state := m.conversationState
	state.pastePending = true
	ctx, read := m.ctx, m.readClipboard
	runner, _ := m.runner.(runnerStatusProvider)
	target := m.workspaceTargetForState(state)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		msg := clipboardPasteMsg{state: state}
		data, err := read(ctx, clipboard.FmtImage)
		switch {
		case err == nil:
			msg.path, msg.err = stageClipboardImage(ctx, data, runner, target)
		case errors.Is(err, clipboard.ErrNoData):
			data, msg.err = read(ctx, clipboard.FmtText)
			msg.text = string(data)
		default:
			msg.err = err
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
	if len(data) > base.MaxImageFileSize {
		return "", errClipboardImageTooLarge
	}
	if _, err := png.DecodeConfig(bytes.NewReader(data)); err != nil {
		return "", errors.Wrap(err, "clipboard did not return a PNG image")
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
	if msg.state != m.conversationState || m.historySearch != nil {
		if msg.path != "" {
			_ = os.Remove(msg.path)
		}
		return nil
	}
	switch {
	case errors.Is(msg.err, clipboard.ErrUnavailable), errors.Is(msg.err, clipboard.ErrNoData):
		return nil
	case msg.err != nil:
		return m.addUINotification(uiNotification{
			level: uiNotificationWarning, title: "Paste failed", message: msg.err.Error(),
		})
	case msg.path != "":
		msg.text = msg.path
	case msg.text == "":
		return nil
	}
	return m.updateComposer(tea.PasteMsg{Content: msg.text})
}
