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

func newWelcomeSpinTestModel(t *testing.T, config Config) model {
	t.Helper()
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	config.Remote = true
	m := newModel(t.Context(), config)
	t.Cleanup(m.cancel)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return updated.(model)
}

func welcomeLogoClick(m model) tea.MouseClickMsg {
	rows, top := m.welcomeLogoRows(), 0
	if m.welcomeSpin.active() {
		rows = welcomeCanvasHeight
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
	for _, config := range []Config{
		{WelcomeEffect: "beams"},
		{WelcomeEffect: "matrix"},
		{WelcomeEffect: "none"},
		{WelcomeStyle: "plain"},
	} {
		t.Run(config.WelcomeEffect+config.WelcomeStyle, func(t *testing.T) {
			m := newWelcomeSpinTestModel(t, config)
			m.finishWelcomeAnimation()
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
			updated, cmd = m.Update(welcomeLogoClick(m))
			m = updated.(model)
			assert.False(t, m.welcomeSpin.active())
			assert.Nil(t, cmd)
			assert.Equal(t, settled, m.View().Content)
			assert.Nil(t, m.updateWelcomeSpin(stale), "stopped ticks cannot restart the animation")
			updated, _ = m.Update(welcomeLogoClick(m))
			m = updated.(model)
			require.True(t, m.welcomeSpin.active())
			assert.Nil(t, m.updateWelcomeSpin(stale), "old ticks cannot create a second tick chain")
			assert.Zero(t, m.welcomeSpin.elapsed)
		})
	}
}

func TestWelcomeSpinInputIsNotConsumed(t *testing.T) {
	for _, tt := range []struct {
		name string
		msg  tea.Msg
		want string
	}{
		{name: "typing", msg: textKeyPress("hello"), want: "hello"},
		{name: "paste", msg: tea.PasteMsg{Content: "pasted draft"}, want: "pasted draft"},
		{name: "newline", msg: keyPressWithMod(tea.KeyEnter, tea.ModShift), want: "\n"},
		{name: "shortcuts", msg: textKeyPress("?")},
		{name: "escape", msg: tea.KeyPressMsg{Code: tea.KeyEscape}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newWelcomeSpinTestModel(t, Config{})
			updated, _ := m.Update(welcomeLogoClick(m))
			m = updated.(model)
			require.True(t, m.welcomeSpin.active())
			assert.True(t, m.welcome.done, "clicking during the intro must cancel it")
			assert.Nil(t, m.updateWelcomeAnimation(time.Now()))
			updated, _ = m.Update(tt.msg)
			m = updated.(model)
			assert.False(t, m.welcomeSpin.active())
			assert.Equal(t, tt.want, m.textarea.Value())
			assert.Equal(t, tt.name == "shortcuts", m.shortcutsOpen)
		})
	}
}

func TestWelcomeSpinClickBounds(t *testing.T) {
	m := newWelcomeSpinTestModel(t, Config{})
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
			m := newWelcomeSpinTestModel(t, Config{})
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
	for _, reason := range []string{"resize", "conversation", "transcript", "overlay"} {
		t.Run(reason, func(t *testing.T) {
			m := newWelcomeSpinTestModel(t, Config{})
			updated, _ := m.Update(welcomeLogoClick(m))
			m = updated.(model)
			require.True(t, m.welcomeSpin.active())
			tick := welcomeSpinTickMsg{startedAt: m.welcomeSpin.startedAt, now: m.welcomeSpin.startedAt.Add(time.Second)}
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
			}
			assert.Nil(t, m.updateWelcomeSpin(tick))
			assert.False(t, m.welcomeSpin.active())
		})
	}
}
