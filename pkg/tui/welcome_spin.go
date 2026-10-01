package tui

import (
	"math"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
)

const (
	welcomeSpinFrameInterval = time.Second / 30
	welcomeSpinDuration      = 6 * time.Second
)

type welcomeSpinAnimation struct {
	startedAt      time.Time
	elapsed        time.Duration
	rotationOffset time.Duration
	particles      []welcomeParticle
}

type welcomeSpinTickMsg struct {
	startedAt time.Time
	now       time.Time
}

func (w welcomeSpinAnimation) active() bool {
	return !w.startedAt.IsZero()
}

func (w welcomeSpinAnimation) duration() time.Duration {
	return max(welcomeParticleDuration, welcomeSpinDuration-w.rotationOffset)
}

func (w welcomeSpinAnimation) pose() (yaw, phase float64) {
	phase = min(1, max(0, float64(w.rotationOffset+w.elapsed)/float64(welcomeSpinDuration)))
	// Brief, readable front-facing holds bookend one eased turn. Return the
	// exact starting orientation, rather than accumulating trigonometric drift.
	turn := min(1, max(0, (phase-0.08)/0.84))
	if turn == 0 || turn == 1 {
		return 0, phase
	}
	return 2 * math.Pi * turn * turn * (3 - 2*turn), phase
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

func (m model) welcomeParticleRows() int {
	// A larger terminal buys real detail, not just additional raster samples:
	// retain some breathing room instead of filling every available row.
	for _, rows := range []int{welcomeParticleMaxHeight, 18} {
		if m.viewport.Width() >= welcomeParticleWidth(rows) && m.viewport.Height() >= rows*3/2 {
			return rows
		}
	}
	if m.viewport.Height() >= welcomeParticleCanvasHeight+2 {
		return welcomeParticleCanvasHeight
	}
	return welcomeCanvasHeight
}

func (m model) welcomeLogoContains(x, y int) bool {
	if !m.welcomeSpinAvailable() || os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	rows, width, top, height := m.welcomeLogoRows(), welcomeCanvasWidth, 0, welcomeLogoHeight
	if m.welcomeSpin.active() {
		rows, height = m.welcomeParticleRows(), m.welcomeParticleRows()
		width = welcomeParticleWidth(rows)
	} else if rows == 1 {
		width, height = len("kodelet."), 1
	} else if rows == welcomeCanvasHeight {
		top = welcomeLogoTop
	}
	left := tuiLeftMargin + (m.viewport.Width()-width)/2
	top += max(0, (m.viewport.Height()-rows-2)/2) - m.viewport.YOffset()
	return x >= left && x < left+width && y >= top && y < top+height
}

func (m *model) playWelcomeParticles(x, y int) tea.Cmd {
	now := time.Now()
	var particles []welcomeParticle
	var rotationOffset time.Duration
	if m.welcomeSpin.active() {
		rows := m.welcomeParticleRows()
		width := welcomeParticleWidth(rows)
		left := tuiLeftMargin + (m.viewport.Width()-width)/2
		top := max(0, (m.viewport.Height()-rows-2)/2) - m.viewport.YOffset()
		scale := float64(rows) / welcomeParticleCanvasHeight
		impact := welcomeSpinPoint{
			x: welcomeCanvasWidth/2.0 + (float64(x-left)+0.5-float64(width)/2)/scale,
			y: (float64((y-top)*2) + 1) / scale,
		}
		// Continue from the last painted pose, even if a tick is delayed. A
		// click adds an impulse; it must not snap the rotation back to the front.
		yaw, phase := m.welcomeSpin.pose()
		particles = scatterWelcomeParticles(m.welcomeSpin.particles, m.welcomeSpin.elapsed, impact, yaw, phase)
		rotationOffset = min(welcomeSpinDuration, m.welcomeSpin.rotationOffset+m.welcomeSpin.elapsed)
		if rotationOffset == welcomeSpinDuration {
			rotationOffset = 0
		}
	} else {
		m.finishWelcomeAnimation()
		particles = newWelcomeParticles()
	}
	m.welcomeSpin = welcomeSpinAnimation{startedAt: now, rotationOffset: rotationOffset, particles: particles}
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
	// A fresh impulse invalidates the previous animation's tick chain.
	if !m.welcomeSpin.active() || !msg.startedAt.Equal(m.welcomeSpin.startedAt) {
		return nil
	}
	if !m.welcomeSpinAvailable() {
		m.stopWelcomeSpin()
		return nil
	}
	duration := m.welcomeSpin.duration()
	if m.welcomeSpin.elapsed >= duration {
		return nil
	}
	m.welcomeSpin.elapsed = min(duration, max(m.welcomeSpin.elapsed, msg.now.Sub(msg.startedAt)))
	m.refreshViewport(false)
	if m.welcomeSpin.elapsed == duration {
		return nil // Keep the sculpture visible, with no animation work while idle.
	}
	return welcomeSpinTick(msg.startedAt)
}
