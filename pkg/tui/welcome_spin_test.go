package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func welcomeLogoClick(m model) tea.MouseClickMsg {
	rows, top := m.welcomeLogoRows(), 0
	if m.welcomeSpin.active() {
		rows, top = m.welcomeParticleRows(), m.welcomeParticleRows()/2
	} else if rows == welcomeCanvasHeight {
		top = welcomeLogoTop
	}
	return tea.MouseClickMsg{
		Button: tea.MouseLeft,
		X:      tuiLeftMargin + m.viewport.Width()/2,
		Y:      max(0, (m.viewport.Height()-rows-2)/2) + top,
	}
}

func TestWelcomeSpinLifecycle(t *testing.T) {
	for _, style := range []string{"block", "plain"} {
		t.Run(style, func(t *testing.T) {
			m := newWelcomeTestModel(t, Config{WelcomeStyle: style})
			settled := m.View().Content
			composer := strings.Split(settled, "\n")[m.viewport.Height():]
			updated, cmd := m.Update(welcomeLogoClick(m))
			m = updated.(model)
			require.True(t, m.welcomeSpin.active())
			require.NotNil(t, cmd)
			assert.IsType(t, welcomeSpinTickMsg{}, cmd())
			assert.True(t, m.welcome.done)
			assert.NotContains(t, xansi.Strip(m.View().Content), "? for shortcuts")
			assert.Equal(t, composer, strings.Split(m.View().Content, "\n")[m.viewport.Height():])
			initial := m.View().Content
			stale := welcomeSpinTickMsg{startedAt: m.welcomeSpin.startedAt, now: m.welcomeSpin.startedAt.Add(time.Second)}
			updated, cmd = m.Update(stale)
			m = updated.(model)
			assert.NotNil(t, cmd)
			assert.Equal(t, time.Second, m.welcomeSpin.elapsed)
			assert.NotEqual(t, initial, m.View().Content)
			finished := welcomeSpinTickMsg{startedAt: m.welcomeSpin.startedAt, now: m.welcomeSpin.startedAt.Add(welcomeSpinDuration)}
			updated, cmd = m.Update(finished)
			m = updated.(model)
			require.Nil(t, cmd, "the gathered logo must rest without background animation ticks")
			require.True(t, m.welcomeSpin.active(), "keep the sculpture visible after gathering")
			require.Equal(t, welcomeSpinDuration, m.welcomeSpin.elapsed)
			sculpture := m.View().Content
			assert.Equal(t, initial, sculpture, "the turn must finish at the exact gathered starting pose")
			assert.Equal(t, composer, strings.Split(sculpture, "\n")[m.viewport.Height():])
			assert.Nil(t, m.updateWelcomeSpin(finished), "duplicate final ticks cannot restart motion")

			updated, cmd = m.Update(welcomeLogoClick(m))
			m = updated.(model)
			require.NotNil(t, cmd)
			assert.Equal(t, sculpture, m.View().Content, "an impulse starts from the displayed particle positions")
			assert.Nil(t, m.updateWelcomeSpin(stale), "old ticks cannot create a second tick chain")
			assert.Zero(t, m.welcomeSpin.elapsed)
			burst := welcomeSpinTickMsg{startedAt: m.welcomeSpin.startedAt, now: m.welcomeSpin.startedAt.Add(200 * time.Millisecond)}
			updated, cmd = m.Update(burst)
			m = updated.(model)
			require.NotNil(t, cmd)
			assert.NotEqual(t, sculpture, m.View().Content, "the click pushes particles out of the sculpture")
			finished = welcomeSpinTickMsg{startedAt: m.welcomeSpin.startedAt, now: m.welcomeSpin.startedAt.Add(m.welcomeSpin.duration())}
			updated, cmd = m.Update(finished)
			m = updated.(model)
			assert.Nil(t, cmd)
			assert.Equal(t, sculpture, m.View().Content, "scattered particles return to the same dotted logo")
		})
	}
}

func TestWelcomeSpinClickBounds(t *testing.T) {
	m := newWelcomeTestModel(t, Config{})
	click := welcomeLogoClick(m)
	left := tuiLeftMargin + (m.viewport.Width()-welcomeCanvasWidth)/2
	assert.True(t, m.welcomeLogoContains(left, click.Y))
	assert.True(t, m.welcomeLogoContains(left+welcomeCanvasWidth-1, click.Y+welcomeLogoHeight-1))
	assert.False(t, m.welcomeLogoContains(left-1, click.Y))
	assert.False(t, m.welcomeLogoContains(left+welcomeCanvasWidth, click.Y))
	assert.False(t, m.welcomeLogoContains(click.X, click.Y-1))
	assert.False(t, m.welcomeLogoContains(click.X, click.Y+welcomeLogoHeight))
	assert.False(t, m.welcomeLogoContains(click.X, click.Y+welcomeLogoHeight+1), "the shortcut hint is not the logo")
	for _, msg := range []tea.Msg{
		tea.MouseClickMsg{X: click.X, Y: click.Y, Button: tea.MouseRight},
		tea.MouseClickMsg{X: click.X, Y: click.Y, Button: tea.MouseLeft, Mod: tea.ModShift},
		tea.MouseReleaseMsg{X: click.X, Y: click.Y, Button: tea.MouseLeft},
		tea.MouseMotionMsg{X: click.X, Y: click.Y, Button: tea.MouseLeft},
		tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft},
	} {
		updated, _ := m.Update(msg)
		assert.False(t, updated.(model).welcomeSpin.active(), "%T must not activate the easter egg", msg)
	}
}

func TestWelcomeSpinUnavailable(t *testing.T) {
	for _, tt := range []struct {
		name    string
		noColor string
		term    string
		setup   func(*model)
	}{
		{name: "no color", noColor: "1"},
		{name: "dumb terminal", term: "dumb"},
		{name: "draft", setup: func(m *model) { m.textarea.SetValue("hello") }},
		{name: "conversation", setup: func(m *model) { m.conversationID = "saved" }},
		{name: "transcript", setup: func(m *model) { m.entries = []chatEntry{{kind: entryUser, content: "hello"}} }},
		{name: "running", setup: func(m *model) { m.running = true }},
		{name: "pending", setup: func(m *model) { m.startupPending = true }},
		{name: "shortcuts", setup: func(m *model) { m.shortcutsOpen = true }},
		{name: "model picker", setup: func(m *model) { m.modelPickerOpen = true }},
		{name: "narrow", setup: func(m *model) { m.viewport.SetWidth(30) }},
		{name: "short", setup: func(m *model) { m.viewport.SetHeight(10) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newWelcomeTestModel(t, Config{})
			click := welcomeLogoClick(m)
			t.Setenv("NO_COLOR", tt.noColor)
			if tt.term != "" {
				t.Setenv("TERM", tt.term)
			}
			if tt.setup != nil {
				tt.setup(&m)
			}
			assert.False(t, m.welcomeLogoContains(click.X, click.Y))
			updated, _ := m.Update(click)
			assert.False(t, updated.(model).welcomeSpin.active())
		})
	}
}

func TestWelcomeSpinStopsWhenLeavingWelcome(t *testing.T) {
	for _, state := range []string{"moving", "settled"} {
		for _, reason := range []string{"resize", "conversation", "transcript", "overlay", "draft"} {
			t.Run(state+"/"+reason, func(t *testing.T) {
				m := newWelcomeTestModel(t, Config{})
				updated, _ := m.Update(welcomeLogoClick(m))
				m = updated.(model)
				tick := welcomeSpinTickMsg{startedAt: m.welcomeSpin.startedAt, now: m.welcomeSpin.startedAt.Add(time.Second)}
				if state == "settled" {
					tick.now = m.welcomeSpin.startedAt.Add(welcomeSpinDuration)
					require.Nil(t, m.updateWelcomeSpin(tick))
				}
				require.True(t, m.welcomeSpin.active())
				switch reason {
				case "resize":
					updated, _ = m.Update(tea.WindowSizeMsg{Width: 30, Height: 10})
					m = updated.(model)
				case "conversation":
					m.createNewConversation()
				case "transcript":
					m.entries = []chatEntry{{kind: entryUser, content: "hello"}}
				case "overlay":
					m.shortcutsOpen = true
				case "draft":
					m.textarea.SetValue("hello")
				}
				if state == "settled" {
					m.refreshViewport(false) // Idle sculptures must notice a hidden welcome without another tick.
				} else {
					assert.Nil(t, m.updateWelcomeSpin(tick))
				}
				assert.False(t, m.welcomeSpin.active())
				assert.Nil(t, m.updateWelcomeSpin(tick), "dismissed particles cannot be revived by a late tick")
			})
		}
	}
}

func TestWelcomeParticlesResponsiveCanvas(t *testing.T) {
	for _, tt := range []struct {
		name                string
		width, height, rows int
	}{
		{name: "regular", width: 80, height: 24, rows: 12},
		{name: "tall", width: 80, height: 40, rows: 18},
		{name: "large", width: 80, height: 50, rows: 24},
		{name: "narrow tall", width: 62, height: 50, rows: 18},
		{name: "narrow", width: 50, height: 50, rows: 12},
		{name: "minimum", width: 80, height: 16, rows: 9},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newWelcomeTestModel(t, Config{})
			updated, _ := m.Update(tea.WindowSizeMsg{Width: tt.width, Height: tt.height})
			m = updated.(model)
			composer := strings.Split(m.View().Content, "\n")[m.viewport.Height():]
			updated, _ = m.Update(welcomeLogoClick(m))
			m = updated.(model)
			require.True(t, m.welcomeSpin.active())
			require.Equal(t, tt.rows, m.welcomeParticleRows())
			assert.Zero(t, m.viewport.YOffset(), "the fixed canvas must not inherit transcript scrolling")
			assert.LessOrEqual(t, len(m.renderInitialMessage()), m.viewport.Height())
			for _, elapsed := range []time.Duration{time.Second, welcomeSpinDuration} {
				m.updateWelcomeSpin(welcomeSpinTickMsg{startedAt: m.welcomeSpin.startedAt, now: m.welcomeSpin.startedAt.Add(elapsed)})
				assert.Equal(t, composer, strings.Split(m.View().Content, "\n")[m.viewport.Height():], "particles must never push or paint over the composer")
			}
			width := welcomeParticleWidth(tt.rows)
			left := tuiLeftMargin + (m.viewport.Width()-width)/2
			top := max(0, (m.viewport.Height()-tt.rows-2)/2)
			assert.True(t, m.welcomeLogoContains(left, top))
			assert.True(t, m.welcomeLogoContains(left+width-1, top+tt.rows-1))
			assert.False(t, m.welcomeLogoContains(left, top-1))
			assert.False(t, m.welcomeLogoContains(left, top+tt.rows))

			// Translate a real screen click back to the same canonical particle
			// scene at every size, including terminal-cell center offsets.
			click := tea.MouseClickMsg{Button: tea.MouseLeft, X: left + 15, Y: top + tt.rows/2}
			scale := float64(tt.rows) / welcomeParticleCanvasHeight
			impact := welcomeSpinPoint{
				x: welcomeCanvasWidth/2.0 + (15.5-float64(width)/2)/scale,
				y: (float64(tt.rows/2*2) + 1) / scale,
			}
			expected := scatterWelcomeParticles(m.welcomeSpin.particles, welcomeSpinDuration, impact, 0, 1)
			updated, cmd := m.Update(click)
			m = updated.(model)
			require.NotNil(t, cmd)
			assert.Equal(t, expected, m.welcomeSpin.particles, "click impulse must match the displayed particle positions")
		})
	}
}

func TestWelcomeParticlesResizePreservesMotion(t *testing.T) {
	m := newWelcomeTestModel(t, Config{})
	updated, _ := m.Update(welcomeLogoClick(m))
	m = updated.(model)
	m.updateWelcomeSpin(welcomeSpinTickMsg{startedAt: m.welcomeSpin.startedAt, now: m.welcomeSpin.startedAt.Add(time.Second)})
	state := m.welcomeSpin
	for _, height := range []int{18, 40, 50, 16, 24} {
		updated, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: height})
		m = updated.(model)
		assert.Equal(t, state, m.welcomeSpin, "resizing only scales the scene, not its spring state")
		assert.LessOrEqual(t, len(m.renderInitialMessage()), m.viewport.Height())
	}
	updated, cmd := m.Update(welcomeLogoClick(m))
	m = updated.(model)
	assert.NotNil(t, cmd, "the recentered sculpture stays interactive after resizing")
	assert.Zero(t, m.welcomeSpin.elapsed)
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 15})
	m = updated.(model)
	assert.False(t, m.welcomeSpin.active(), "below compact size, restore the plain welcome")
}

func TestWelcomeSpinClickPreservesTurn(t *testing.T) {
	for _, elapsed := range []time.Duration{time.Second, 2700 * time.Millisecond, 5800 * time.Millisecond} {
		t.Run(elapsed.String(), func(t *testing.T) {
			m := newWelcomeTestModel(t, Config{})
			updated, _ := m.Update(welcomeLogoClick(m))
			m = updated.(model)
			initial := m.View().Content
			tick := welcomeSpinTickMsg{startedAt: m.welcomeSpin.startedAt, now: m.welcomeSpin.startedAt.Add(elapsed)}
			m.updateWelcomeSpin(tick)
			before := m.View().Content
			updated, cmd := m.Update(welcomeLogoClick(m))
			m = updated.(model)
			require.NotNil(t, cmd)
			assert.Equal(t, before, m.View().Content, "a mid-turn click must not reset the rotating silhouette or its dust")
			assert.Equal(t, elapsed, m.welcomeSpin.rotationOffset, "even clicks during the final front-facing hold must preserve the turn's progress")
			assert.Nil(t, m.updateWelcomeSpin(tick), "the superseded timer must not create a second chain")
			duration := m.welcomeSpin.duration()
			assert.GreaterOrEqual(t, duration, welcomeParticleDuration, "late impulses have time to gather after the turn finishes")
			assert.Nil(t, m.updateWelcomeSpin(welcomeSpinTickMsg{startedAt: m.welcomeSpin.startedAt, now: m.welcomeSpin.startedAt.Add(duration + time.Hour)}))
			assert.Equal(t, initial, m.View().Content)
			assert.Equal(t, duration, m.welcomeSpin.elapsed, "long scheduling delays clamp at rest")
		})
	}
}
