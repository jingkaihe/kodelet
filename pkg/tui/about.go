package tui

import (
	"context"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	chat "github.com/jingkaihe/kodelet/pkg/chat"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	"github.com/jingkaihe/kodelet/pkg/version"
)

const (
	aboutDialogTitle      = "About Kodelet"
	aboutLabelWidth       = 17
	aboutShortCommitWidth = 7
	aboutUnknownValue     = "Unknown"
)

// serverStatusProvider is implemented by daemon-backed runners that can report
// the connected server's build metadata.
type serverStatusProvider interface {
	ServerStatus(context.Context) (chat.ServerStatus, error)
}

type runnerStatusProvider interface {
	RunnerStatus(context.Context, chat.WorkspaceTarget) (chat.RunnerStatus, error)
}

type aboutDialogState struct {
	requestID       int
	scrollOffset    int
	loading         bool
	unsupported     bool
	status          chat.ServerStatus
	err             error
	runnerSupported bool
	runnerLoading   bool
	runnerStatus    chat.RunnerStatus
	runnerErr       error
}

type serverStatusMsg struct {
	requestID int
	status    chat.ServerStatus
	err       error
}

type runnerStatusMsg struct {
	requestID int
	status    chat.RunnerStatus
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

// reloadServerStatus loads server and runner details independently; responses to
// earlier requests are discarded so a retry cannot be overwritten by a stale failure.
func (m *model) reloadServerStatus() tea.Cmd {
	if m.aboutDialog == nil {
		return nil
	}
	m.nextAboutRequestID++
	requestID := m.nextAboutRequestID
	*m.aboutDialog = aboutDialogState{requestID: requestID}
	var cmds []tea.Cmd
	provider, ok := m.runner.(serverStatusProvider)
	if ok {
		m.aboutDialog.loading = true
		cmds = append(cmds, loadServerStatus(m.ctx, requestID, provider))
	} else {
		m.aboutDialog.unsupported = true
	}
	if provider, ok := m.runner.(runnerStatusProvider); ok {
		m.aboutDialog.runnerSupported = true
		m.aboutDialog.runnerLoading = true
		target := chat.WorkspaceTarget{ConversationID: m.conversationID}
		if target.ConversationID == "" {
			target.Profile = profileForRequest(m.profile)
		}
		ctx := m.ctx
		cmds = append(cmds, func() tea.Msg {
			status, err := provider.RunnerStatus(ctx, target)
			return runnerStatusMsg{requestID: requestID, status: status, err: err}
		})
	}
	return tea.Batch(cmds...)
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

func (m *model) applyRunnerStatus(msg runnerStatusMsg) {
	if m.aboutDialog == nil || msg.requestID != m.aboutDialog.requestID {
		return
	}
	m.aboutDialog.runnerLoading = false
	m.aboutDialog.runnerStatus = msg.status
	m.aboutDialog.runnerErr = msg.err
}

func (m *model) updateAboutDialogKey(key string) tea.Cmd {
	switch key {
	case "esc", "enter", "q", "Q", "ctrl+c", "ctrl+d":
		return m.dismissInfoDialogs()
	case "r", "R":
		if m.aboutDialog != nil && (m.aboutDialog.err != nil || m.aboutDialog.runnerErr != nil) {
			return m.reloadServerStatus()
		}
	case "up", "down", "pgup", "pgdown", "home", "end":
		lines, _ := m.aboutDialogContent()
		height := max(1, m.height-4) // Reserve the borders, title, and footer.
		maxOffset := max(0, len(lines)-height)
		offset := min(m.aboutDialog.scrollOffset, maxOffset)
		switch key {
		case "up":
			offset--
		case "down":
			offset++
		case "pgup":
			offset -= height
		case "pgdown":
			offset += height
		case "home":
			offset = 0
		case "end":
			offset = maxOffset
		}
		m.aboutDialog.scrollOffset = max(0, min(offset, maxOffset))
	}
	return nil
}

func (m model) overlayAboutDialog(lines []string) []string {
	return m.overlayCenteredDialog(lines, m.renderAboutDialog())
}

func (m model) renderAboutDialog() string {
	width := m.uiDialogWidth()
	if m.aboutDialog == nil || width <= 4 || m.height <= 0 {
		return ""
	}
	contentWidth := max(1, width-4)
	lines, footer := m.aboutDialogContent()
	bodyHeight := max(1, m.height-4) // Reserve the borders, title, and footer.
	scrollable := len(lines) > bodyHeight
	if scrollable {
		offset := min(m.aboutDialog.scrollOffset, len(lines)-bodyHeight)
		lines = lines[offset : offset+bodyHeight]
	}
	if scrollable || contentWidth < 40 {
		footer = "Esc close"
		if m.aboutDialog.err != nil || m.aboutDialog.runnerErr != nil {
			footer = "r retry · " + footer
		}
		if scrollable && m.height >= 5 {
			footer = "↑/↓ scroll · " + footer
		}
	}
	footer = renderPersistentStyle(uiDialogMutedStyle, fitVisible(footer, contentWidth))
	// When even one body row cannot fit, keep dismissal/retry discoverable.
	if m.height < 3 {
		return footer
	}
	if m.height < 5 {
		return renderDialogBox(width, []string{footer})
	}
	content := []string{renderPersistentStyle(uiDialogTitleStyle, fitVisible(aboutDialogTitle, contentWidth))}
	if len(lines)+6 <= m.height {
		content = append(content, "")
		lines = append(lines, "")
	}
	content = append(content, lines...)
	content = append(content, footer)
	return renderDialogBox(width, content)
}

func (m model) aboutDialogContent() ([]string, string) {
	about := m.aboutDialog
	contentWidth := max(1, m.uiDialogWidth()-4)
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
	if about.err != nil || about.runnerErr != nil {
		footer = "Press r to try again, or Esc to close."
	}
	var notices []string
	switch {
	case about.loading:
		lines = append(lines, row("Version", "Loading…"))
	case about.unsupported:
		lines = append(lines, row("Version", "Unavailable"))
		notices = append(notices, "This connection does not report server details.")
	case about.err != nil:
		lines = append(lines, row("Version", "Unavailable"))
		notices = append(notices, "Could not load server details: "+about.err.Error())
	default:
		lines = append(lines,
			row("Version", aboutValue(about.status.Version)),
			row("Build commit", formatAboutCommit(about.status.GitCommit)),
			row("Build date", formatAboutBuildDate(about.status.BuildTime)),
		)
	}
	if about.runnerSupported {
		lines = append(lines, "", heading("Runner"))
		runner := about.runnerStatus.Runner
		switch {
		case about.runnerLoading:
			lines = append(lines, row("Name", "Loading…"))
		case runner != nil:
			name := runner.DisplayName
			if name == "" {
				name = runner.Workspace.Name
			}
			if name == "" {
				name = runner.ID
			}
			status := string(runner.Status)
			if !runner.Connected {
				status = "offline"
			} else if runner.Status == runnerregistry.RunnerStatusBusy {
				status = strconv.Itoa(max(1, len(runner.ActiveRunIDs))) + " active"
			}
			lines = append(lines,
				row("Name", aboutValue(name)),
				row("ID", runner.ID),
				row("Version", aboutValue(runner.KodeletVersion)),
				row("Status", aboutValue(status)),
			)
		default:
			lines = append(lines, row("Name", "Unavailable"))
		}
		if runner == nil {
			runner = &runnerregistry.Runner{}
		}
		available := runner.Connected && (runner.Status == runnerregistry.RunnerStatusIdle || runner.Status == runnerregistry.RunnerStatusBusy)
		lines = append(lines, "", heading("Capabilities"))
		for _, capability := range []struct {
			label   string
			enabled bool
		}{
			{label: "Git diffs", enabled: runner.WorkspaceGitDiff},
			{label: "Terminal", enabled: runner.WorkspaceTerminal && about.runnerStatus.TerminalAuthorized},
			{label: "Browser", enabled: runner.WorkspaceBrowser && about.runnerStatus.TerminalAuthorized},
			{label: "Slash commands", enabled: runner.WorkspaceDiscovery},
			{label: "Working directory", enabled: runner.WorkspaceCWD},
			{label: "Parallel runs", enabled: runner.ConcurrentRuns},
		} {
			value := "Not enabled"
			switch {
			case about.runnerLoading:
				value = "Loading…"
			case !available:
				value = "Unavailable"
			case capability.enabled:
				value = "Enabled"
			}
			lines = append(lines, row(capability.label, value))
		}
		switch {
		case about.runnerLoading:
		case about.runnerErr != nil:
			notices = append(notices, "Could not load runner details: "+about.runnerErr.Error())
		case about.runnerStatus.Runner == nil:
			notices = append(notices, "No runner is selected.")
		case !available:
			notices = append(notices, "This runner is not ready. Capabilities are unavailable until it reconnects.")
		}
	}
	for _, notice := range notices {
		lines = append(lines, "")
		for _, line := range strings.Split(wrapText(notice, contentWidth), "\n") {
			lines = append(lines, renderPersistentStyle(uiDialogMutedStyle, fitVisible(line, contentWidth)))
		}
	}
	return lines, footer
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
