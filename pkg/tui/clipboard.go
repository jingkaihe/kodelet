package tui

import (
	"context"
	"os"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/runner/localstate"
	"github.com/jingkaihe/kodelet/pkg/vision"
	"github.com/pkg/errors"
)

var errClipboardNeedsLocalRunner = errors.New("clipboard paths require a local runner")

type clipboardPasteMsg struct {
	state    *conversationState
	text     string
	files    []string // Original file paths, never owned by the TUI.
	tempPath string
	err      error
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
		content, err := source(ctx)
		if err != nil {
			msg.err = err
			return msg
		}
		switch content.kind {
		case 'F':
			msg.err = requireLocalClipboardRunner(ctx, runner, target)
			if msg.err == nil {
				msg.files = content.files
			}
		case 'I':
			msg.tempPath, msg.err = stageClipboardImage(ctx, content.data, runner, target)
		case 'T':
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
	// Name the file from its bytes, not the clipboard owner's MIME label.
	ext := format
	switch format {
	case "jpeg":
		ext = "jpg"
	case "png", "gif", "webp":
	default:
		return "", errors.Errorf("unsupported clipboard image format: %s", format)
	}
	if err := requireLocalClipboardRunner(ctx, runner, target); err != nil {
		return "", err
	}
	// The turn can outlive this TUI, so leave successfully pasted files to OS temp cleanup.
	return writeComposerTempFile("kodelet-paste-*."+ext, data)
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
	return errClipboardNeedsLocalRunner
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
	case msg.err != nil:
		detail := msg.err.Error()
		switch {
		case errors.Is(msg.err, errClipboardNeedsLocalRunner):
			detail = "To paste a file or image, use a runner on the same machine as this chat."
		case errors.Is(msg.err, context.DeadlineExceeded):
			detail = "Clipboard paste timed out. Try copying the item again."
		}
		return m.addUINotification(uiNotification{
			level: uiNotificationWarning, title: "Paste failed", message: detail,
		})
	case msg.tempPath != "":
		msg.text = m.spaceClipboardPaths(msg.tempPath)
	case len(msg.files) > 0:
		msg.text = m.spaceClipboardPaths(strings.Join(msg.files, "\n"))
	case msg.text == "":
		return nil
	}
	return m.updateComposer(tea.PasteMsg{Content: msg.text})
}

func (m *model) spaceClipboardPaths(paths string) string {
	// The textarea column counts runes within the logical (not wrapped) line.
	lines := strings.Split(m.textarea.Value(), "\n")
	line := []rune(lines[m.textarea.Line()])
	col := m.textarea.Column()
	if col > 0 && !unicode.IsSpace(line[col-1]) {
		paths = " " + paths
	}
	if col < len(line) && !unicode.IsSpace(line[col]) {
		paths += " "
	}
	return paths
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
