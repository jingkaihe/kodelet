package tui

import (
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
)

const welcomeSpinFrameInterval = time.Second / 30

type welcomeSpinAnimation struct {
	startedAt time.Time
	elapsed   time.Duration
}

type welcomeSpinTickMsg struct {
	startedAt time.Time
	now       time.Time
}

func (w welcomeSpinAnimation) active() bool {
	return !w.startedAt.IsZero()
}

func welcomeSpinTick(startedAt time.Time) tea.Cmd {
	return tea.Tick(welcomeSpinFrameInterval, func(now time.Time) tea.Msg {
		return welcomeSpinTickMsg{startedAt: startedAt, now: now}
	})
}

func (m model) welcomeSpinAvailable() bool {
	_, extensionFocused := m.focusedExtensionSurfaceKey()
	return m.welcomeLogoVisible() && m.viewport.Width() >= welcomeCanvasWidth &&
		m.viewport.Height() >= welcomeCanvasHeight+2 && !m.running && m.textarea.Value() == "" &&
		!m.infoDialogOpen() && m.activeUIPrompt == nil && m.conversationPicker == nil &&
		!m.modelPickerOpen && !m.reasoningPickerOpen && m.historySearch == nil &&
		!extensionFocused && len(m.uiNotifications) == 0
}

func (m model) welcomeLogoContains(x, y int) bool {
	if !m.welcomeSpinAvailable() || os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	rows, width, top, height := m.welcomeLogoRows(), welcomeCanvasWidth, 0, welcomeLogoHeight
	if m.welcomeSpin.active() {
		rows, height = welcomeCanvasHeight, welcomeCanvasHeight
	} else if rows == 1 {
		width, height = len("kodelet."), 1
	} else if rows == welcomeCanvasHeight {
		top = welcomeLogoTop
	}
	left := tuiLeftMargin + (m.viewport.Width()-width)/2
	top += max(0, (m.viewport.Height()-rows-2)/2) - m.viewport.YOffset()
	return x >= left && x < left+width && y >= top && y < top+height
}

func (m *model) toggleWelcomeSpin() tea.Cmd {
	if m.welcomeSpin.active() {
		m.stopWelcomeSpin()
		return nil
	}
	m.finishWelcomeAnimation()
	m.welcomeSpin = welcomeSpinAnimation{startedAt: time.Now()}
	m.refreshViewport(false)
	return welcomeSpinTick(m.welcomeSpin.startedAt)
}

func (m *model) stopWelcomeSpin() {
	if !m.welcomeSpin.active() {
		return
	}
	m.welcomeSpin = welcomeSpinAnimation{}
	m.refreshViewport(false)
}

func (m *model) updateWelcomeSpin(msg welcomeSpinTickMsg) tea.Cmd {
	// A click can start a new spin before the previous one's last tick arrives.
	if !m.welcomeSpin.active() || !msg.startedAt.Equal(m.welcomeSpin.startedAt) {
		return nil
	}
	if !m.welcomeSpinAvailable() {
		m.stopWelcomeSpin()
		return nil
	}
	m.welcomeSpin.elapsed = max(0, msg.now.Sub(msg.startedAt))
	m.refreshViewport(false)
	return welcomeSpinTick(msg.startedAt)
}
