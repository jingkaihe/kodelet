package tui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	chat "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/version"
)

const (
	aboutDialogTitle      = "About Kodelet"
	aboutLabelWidth       = 14
	aboutShortCommitWidth = 7
	aboutUnknownValue     = "Unknown"
)

// serverStatusProvider is implemented by daemon-backed runners that can report
// the connected server's build metadata.
type serverStatusProvider interface {
	ServerStatus(context.Context) (chat.ServerStatus, error)
}

type aboutDialogState struct {
	requestID   int
	loading     bool
	unsupported bool
	status      chat.ServerStatus
	err         error
}

type serverStatusMsg struct {
	requestID int
	status    chat.ServerStatus
	err       error
}

func (m model) infoDialogOpen() bool {
	return m.shortcutsOpen || m.aboutDialog != nil
}

func (m *model) closeInfoDialogs() {
	m.shortcutsOpen = false
	m.aboutDialog = nil
}

// dismissInfoDialogs closes the shortcuts and about dialogs and restores focus
// to any extension surface they were covering.
func (m *model) dismissInfoDialogs() tea.Cmd {
	oldFocusKey, oldFocused := m.focusedExtensionSurfaceKey()
	var oldFocus tuiExtensionSurface
	if oldFocused {
		oldFocus = m.extensionSurfaces[oldFocusKey]
	}
	m.closeInfoDialogs()
	m.refreshViewport(false)
	return tea.Sequence(m.extensionSurfaceFocusTransitionCommands(oldFocusKey, oldFocused, oldFocus)...)
}

func (m *model) openAboutDialog() tea.Cmd {
	oldFocusKey, oldFocused := m.focusedExtensionSurfaceKey()
	var oldFocus tuiExtensionSurface
	if oldFocused {
		oldFocus = m.extensionSurfaces[oldFocusKey]
	}
	m.reasoningPickerOpen = false
	m.modelPickerOpen = false
	m.dismissSlashCommandSuggestions()
	m.shortcutsOpen = false
	m.aboutDialog = &aboutDialogState{}
	load := m.reloadServerStatus()
	m.resize()
	m.refreshViewport(false)
	return tea.Batch(load, tea.Sequence(m.extensionSurfaceFocusTransitionCommands(oldFocusKey, oldFocused, oldFocus)...))
}

// reloadServerStatus starts a new server status request; responses to earlier
// requests are discarded so a retry cannot be overwritten by a stale failure.
func (m *model) reloadServerStatus() tea.Cmd {
	if m.aboutDialog == nil {
		return nil
	}
	m.nextAboutRequestID++
	requestID := m.nextAboutRequestID
	provider, ok := m.runner.(serverStatusProvider)
	if !ok {
		*m.aboutDialog = aboutDialogState{requestID: requestID, unsupported: true}
		return nil
	}
	*m.aboutDialog = aboutDialogState{requestID: requestID, loading: true}
	return loadServerStatus(m.ctx, requestID, provider)
}

func loadServerStatus(ctx context.Context, requestID int, provider serverStatusProvider) tea.Cmd {
	return func() tea.Msg {
		status, err := provider.ServerStatus(ctx)
		return serverStatusMsg{requestID: requestID, status: status, err: err}
	}
}

func (m *model) applyServerStatus(msg serverStatusMsg) {
	if m.aboutDialog == nil || msg.requestID != m.aboutDialog.requestID {
		return
	}
	m.aboutDialog.loading = false
	m.aboutDialog.status = msg.status
	m.aboutDialog.err = msg.err
}

func (m *model) updateAboutDialogKey(key string) tea.Cmd {
	switch key {
	case "esc", "enter", "q", "Q", "ctrl+c", "ctrl+d":
		return m.dismissInfoDialogs()
	case "r", "R":
		if m.aboutDialog != nil && m.aboutDialog.err != nil {
			return m.reloadServerStatus()
		}
	}
	return nil
}

func (m model) overlayAboutDialog(lines []string) []string {
	return m.overlayCenteredDialog(lines, m.renderAboutDialog())
}

func (m model) renderAboutDialog() string {
	about := m.aboutDialog
	width := m.uiDialogWidth()
	if about == nil || width <= 4 {
		return ""
	}
	contentWidth := max(1, width-4)
	labelWidth := min(aboutLabelWidth, max(1, contentWidth/2))
	valueWidth := max(1, contentWidth-labelWidth-2)
	row := func(label, value string) string {
		label = renderPersistentStyle(uiDialogMutedStyle, padVisible(fitVisible(label, labelWidth), labelWidth))
		return label + "  " + renderPersistentStyle(uiDialogBodyStyle, fitVisible(value, valueWidth))
	}
	heading := func(text string) string {
		return renderPersistentStyle(uiDialogButtonStyle, fitVisible(text, contentWidth))
	}

	lines := []string{
		renderPersistentStyle(uiDialogTitleStyle, fitVisible(aboutDialogTitle, contentWidth)),
		"",
		heading("Client"),
		row("Version", aboutValue(version.Version)),
		row("Build commit", formatAboutCommit(version.GitCommit)),
		row("Build date", formatAboutBuildDate(version.BuildTime)),
		"",
		heading("Server"),
	}
	if m.serverURL != "" {
		lines = append(lines, row("Address", m.serverURL))
	}
	footer := "Press Esc, Enter, or q to close."
	var notice string
	switch {
	case about.loading:
		lines = append(lines, row("Version", "Loading…"))
	case about.unsupported:
		lines = append(lines, row("Version", "Unavailable"))
		notice = "This connection does not report server details."
	case about.err != nil:
		lines = append(lines, row("Version", "Unavailable"))
		notice = "Could not load server details: " + about.err.Error()
		footer = "Press r to try again, or Esc to close."
	default:
		lines = append(lines,
			row("Version", aboutValue(about.status.Version)),
			row("Build commit", formatAboutCommit(about.status.GitCommit)),
			row("Build date", formatAboutBuildDate(about.status.BuildTime)),
		)
	}
	if notice != "" {
		lines = append(lines, "")
		for _, line := range strings.Split(wrapText(notice, contentWidth), "\n") {
			lines = append(lines, renderPersistentStyle(uiDialogMutedStyle, fitVisible(line, contentWidth)))
		}
	}
	lines = append(lines, "", renderPersistentStyle(uiDialogMutedStyle, fitVisible(footer, contentWidth)))
	return renderDialogBox(width, lines)
}

func aboutValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "unknown" {
		return aboutUnknownValue
	}
	return value
}

func formatAboutCommit(commit string) string {
	commit = aboutValue(commit)
	if commit == aboutUnknownValue || len(commit) <= aboutShortCommitWidth {
		return commit
	}
	return commit[:aboutShortCommitWidth]
}

// formatAboutBuildDate accepts both release (RFC 3339) and local mise
// (date(1) UnixDate) build timestamps and shows them as a UTC calendar date.
func formatAboutBuildDate(buildTime string) string {
	buildTime = aboutValue(buildTime)
	if buildTime == aboutUnknownValue {
		return buildTime
	}
	for _, layout := range []string{time.RFC3339, time.UnixDate} {
		if parsed, err := time.Parse(layout, buildTime); err == nil {
			return parsed.UTC().Format("Jan 2, 2006")
		}
	}
	return buildTime
}
