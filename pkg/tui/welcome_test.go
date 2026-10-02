package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newWelcomeTestModel(t *testing.T, config Config) model {
	t.Helper()
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	config.Remote = true
	m := newModel(t.Context(), config)
	t.Cleanup(m.cancel)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return updated.(model)
}

func TestWelcomeAnimationLifecycle(t *testing.T) {
	for _, effect := range []string{"beams", "matrix"} {
		t.Run(effect, func(t *testing.T) {
			interval := welcomeFrameInterval
			frames := welcomeMatrixFrames
			if effect == welcomeEffectBeams {
				interval = welcomeBeamFrameInterval
				frames = welcomeFrames
			}
			t.Setenv("NO_COLOR", "")
			t.Setenv("TERM", "xterm-256color")
			m := newModel(t.Context(), Config{Remote: true, Theme: DefaultThemeName, WelcomeEffect: effect})
			t.Cleanup(m.cancel)
			assert.Nil(t, m.startWelcomeAnimation(), "wait for the first terminal size")
			assert.Nil(t, m.updateWelcomeAnimation(time.Now()), "ignore ticks before starting")

			updated, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
			m = updated.(model)
			require.NotNil(t, cmd)
			assert.IsType(t, welcomeTickMsg{}, cmd())
			assert.False(t, m.welcome.startedAt.IsZero())
			assert.Nil(t, m.startWelcomeAnimation(), "do not create duplicate tick chains")
			initial := m.View().Content
			assert.NotContains(t, xansi.Strip(initial), "? for shortcuts")

			updated, cmd = m.Update(welcomeTickMsg(m.welcome.startedAt.Add(20 * interval)))
			m = updated.(model)
			assert.Equal(t, 20, m.welcome.frame, "elapsed time skips missed frames")
			assert.NotNil(t, cmd)
			assert.NotEqual(t, initial, m.View().Content)
			assert.NotContains(t, xansi.Strip(m.View().Content), "? for shortcuts")
			canvasStart := (m.viewport.Height() - welcomeCanvasHeight - 2) / 2
			assert.Equal(t, m.renderWelcomeLogo(m.viewport.Width()), m.renderInitialMessage()[canvasStart:canvasStart+welcomeCanvasHeight], "the hidden hint must not overwrite beam or rain cells")

			updated, cmd = m.Update(welcomeTickMsg(m.welcome.startedAt.Add(time.Duration(frames)*interval - time.Nanosecond)))
			m = updated.(model)
			assert.False(t, m.welcome.done, "keep animating through the final fade")
			assert.NotNil(t, cmd)
			before := strings.Split(xansi.Strip(m.View().Content), "\n")
			assert.NotContains(t, strings.Join(before, "\n"), "? for shortcuts")

			updated, cmd = m.Update(welcomeTickMsg(m.welcome.startedAt.Add(time.Duration(frames) * interval)))
			m = updated.(model)
			assert.True(t, m.welcome.done)
			assert.Nil(t, cmd, "stop scheduling work after the final frame")
			settled := m.View().Content
			hintRow := welcomeShortcutRow(t, m)
			assert.Empty(t, strings.TrimSpace(before[hintRow]), "the hidden hint reserves a blank row")
			after := strings.Split(xansi.Strip(settled), "\n")
			assert.Empty(t, strings.TrimSpace(after[hintRow-1]), "one blank row separates logo and hint")
			assert.NotEmpty(t, strings.TrimSpace(after[hintRow-2]), "place the hint below the visible logo, not the animation canvas")
			assert.Equal(t, before[:hintRow], after[:hintRow], "revealing the hint must not move the logo")
			assert.Equal(t, before[hintRow+1:], after[hintRow+1:], "revealing the hint must not move the composer")
			updated, cmd = m.Update(welcomeTickMsg(time.Now()))
			m = updated.(model)
			assert.Nil(t, cmd, "stale ticks must not restart the effect")
			assert.Equal(t, settled, m.View().Content)
			assert.Nil(t, m.startWelcomeAnimation())
		})
	}
}

func TestWelcomeInputIsNotConsumed(t *testing.T) {
	for _, scene := range []struct {
		name, effect string
		particles    bool
	}{
		{name: "beams", effect: "beams"},
		{name: "particles", effect: "beams", particles: true},
	} {
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
			t.Run(scene.name+"/"+tt.name, func(t *testing.T) {
				m := newWelcomeTestModel(t, Config{WelcomeEffect: scene.effect})
				require.False(t, m.welcome.done)
				if scene.particles {
					updated, _ := m.Update(welcomeLogoClick(m))
					m = updated.(model)
					require.True(t, m.welcomeSpin.active())
					assert.True(t, m.welcome.done, "clicking during the intro must cancel it")
					assert.Nil(t, m.updateWelcomeAnimation(time.Now()))
				}
				assert.NotContains(t, xansi.Strip(m.viewport.View()), "? for shortcuts")

				updated, _ := m.Update(tt.msg)
				m = updated.(model)
				assert.True(t, m.welcome.done)
				assert.False(t, m.welcomeSpin.active())
				assert.Equal(t, tt.want, m.textarea.Value())
				assert.Equal(t, tt.name == "shortcuts", m.shortcutsOpen)
				assert.Nil(t, m.updateWelcomeAnimation(time.Now()))
				assert.Contains(t, xansi.Strip(m.viewport.View()), "? for shortcuts", "interrupting the effect reveals the hint immediately")
			})
		}
	}
}

func TestWelcomeAnimationStaticFallbacks(t *testing.T) {
	for _, tt := range []struct {
		name    string
		config  Config
		width   int
		height  int
		noColor string
		term    string
		compact bool
		plain   bool
	}{
		{name: "default", width: 80, height: 24, compact: true},
		{name: "narrow", width: 30, height: 24, config: Config{WelcomeEffect: "beams"}, plain: true},
		{name: "short", width: 80, height: 10, config: Config{WelcomeEffect: "beams"}, plain: true},
		{name: "plain", width: 80, height: 24, config: Config{WelcomeStyle: "plain", WelcomeEffect: "beams"}, plain: true},
		{name: "no color", width: 80, height: 24, noColor: "1", config: Config{WelcomeEffect: "beams"}, compact: true},
		{name: "dumb terminal", width: 80, height: 24, term: "dumb", config: Config{WelcomeEffect: "beams"}, compact: true},
		{name: "resume", width: 80, height: 24, config: Config{ConversationID: "saved", WelcomeEffect: "beams"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", tt.noColor)
			t.Setenv("TERM", tt.term)
			m := newModel(t.Context(), tt.config)
			t.Cleanup(m.cancel)
			updated, _ := m.Update(tea.WindowSizeMsg{Width: tt.width, Height: tt.height})
			m = updated.(model)
			assert.True(t, m.welcome.done)
			assert.True(t, m.welcome.startedAt.IsZero())
			assert.Nil(t, m.startWelcomeAnimation())
			assert.Contains(t, xansi.Strip(m.View().Content), "? for shortcuts", "static and compact welcomes show the hint immediately")
			if tt.compact {
				assert.True(t, m.welcomeLogoVisible())
				assert.Equal(t, welcomeLogoHeight, m.welcomeLogoRows())
				assert.Len(t, m.renderWelcomeLogo(m.viewport.Width()), welcomeLogoHeight)
			}
			if tt.plain {
				assert.True(t, m.welcomeLogoVisible())
				assert.Equal(t, 1, m.welcomeLogoRows())
				logo := m.renderWelcomeLogo(m.viewport.Width())
				require.Len(t, logo, 1)
				assert.Equal(t, "kodelet.", strings.TrimSpace(xansi.Strip(logo[0])))
			}
		})
	}
}

func TestWelcomeDeferredStartupAndLayout(t *testing.T) {
	for _, tt := range []struct {
		name    string
		style   string
		effect  string
		noColor string
		resumed bool
	}{
		{name: "default"},
		{name: "beams", effect: "beams"},
		{name: "matrix", effect: "matrix"},
		{name: "plain", style: "plain", effect: "matrix"},
		{name: "no color", effect: "beams", noColor: "1"},
		{name: "resumed", effect: "beams", resumed: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", tt.noColor)
			t.Setenv("TERM", "xterm-256color")
			m := newModel(t.Context(), Config{WelcomeStyle: tt.style, WelcomeEffect: tt.effect, Initialize: func(context.Context) (Config, error) {
				return Config{}, nil
			}})
			t.Cleanup(m.cancel)
			updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
			m = updated.(model)
			welcome := m.welcome
			assert.True(t, welcome.startedAt.IsZero(), "do not animate before knowing whether this is a resume")
			assert.False(t, m.welcomeLogoVisible(), "do not show a frozen first frame during initialization")
			starting := len(m.renderInitialMessage())
			hintRow := starting - 1
			if m.welcomeLogoRows() == welcomeCanvasHeight {
				hintRow -= welcomeCanvasHeight - welcomeLogoTop - welcomeLogoHeight
			}
			if m.welcome.animated {
				assert.NotContains(t, xansi.Strip(m.View().Content), "? for shortcuts", "do not flash the hint before the animation starts")
			} else {
				assert.Equal(t, hintRow, welcomeShortcutRow(t, m))
			}

			config := Config{Runner: &recordingRunner{}, Remote: true}
			if tt.resumed {
				config.ConversationID = "saved"
			}
			updated, _ = m.Update(initializedMsg{config: config})
			m = updated.(model)
			assert.Equal(t, welcome.style, m.welcome.style, "daemon configuration must preserve the client's style")
			assert.Equal(t, welcome.effect, m.welcome.effect, "daemon configuration must preserve the client's effect")
			animated := welcome.animated && !tt.resumed
			assert.Equal(t, !animated, m.welcome.startedAt.IsZero())
			assert.Equal(t, !animated, m.welcome.done)
			assert.Equal(t, starting, len(m.renderInitialMessage()), "keep the same area reserved through startup")
			if m.welcome.done {
				assert.Equal(t, hintRow, welcomeShortcutRow(t, m))
			} else {
				assert.NotContains(t, xansi.Strip(m.View().Content), "? for shortcuts")
			}
			if tt.resumed {
				assert.False(t, m.welcomeLogoVisible(), "resumed conversations keep blank space until history arrives")
				return
			}
			require.True(t, m.welcomeLogoVisible())
			m.finishWelcomeAnimation()
			assert.Equal(t, hintRow, welcomeShortcutRow(t, m), "the hint appears in the reserved row when the intro settles")
		})
	}
}

func welcomeShortcutRow(t *testing.T, m model) int {
	t.Helper()
	for row, line := range strings.Split(xansi.Strip(m.View().Content), "\n") {
		if strings.Contains(line, "? for shortcuts") {
			return row
		}
	}
	require.FailNow(t, "the shortcut hint is not rendered")
	return -1
}

func TestWelcomeAnimationStopsWhenLeavingWelcome(t *testing.T) {
	for _, reason := range []string{"resize", "transcript", "new conversation"} {
		t.Run(reason, func(t *testing.T) {
			m := newWelcomeTestModel(t, Config{WelcomeEffect: "beams"})
			switch reason {
			case "resize":
				updated, _ := m.Update(tea.WindowSizeMsg{Width: 30, Height: 10})
				m = updated.(model)
			case "transcript":
				m.entries = []chatEntry{{kind: entryUser, content: "work"}}
				m.refreshViewport(true)
			case "new conversation":
				m.createNewConversation()
			}
			updated, cmd := m.Update(welcomeTickMsg(time.Now()))
			m = updated.(model)
			assert.True(t, m.welcome.done)
			assert.Nil(t, cmd)
			if reason == "transcript" {
				assert.Contains(t, m.View().Content, "work")
			}
			updated, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
			m = updated.(model)
			assert.Nil(t, m.startWelcomeAnimation(), "resizing must not replay the intro")
		})
	}
}

func TestWelcomeLogoFramesAndLayout(t *testing.T) {
	for _, effect := range []string{"beams", "matrix"} {
		for _, themeName := range []string{DefaultThemeName, LightThemeName} {
			t.Run(effect+"/"+themeName, func(t *testing.T) {
				m := newWelcomeTestModel(t, Config{Theme: themeName, WelcomeEffect: effect})
				for _, frame := range []int{0, 50, m.welcome.frames() / 2, m.welcome.frames()} {
					m.welcome.frame = frame
					lines := m.renderWelcomeLogo(78)
					require.Len(t, lines, welcomeCanvasHeight)
					for y, line := range lines {
						assert.Equal(t, 78, lipgloss.Width(line), "frame %d row %d", frame, y)
					}
				}
				m.welcome.frame = 50
				animatedFrame := strings.Join(m.renderWelcomeLogo(78), "\n")
				m.welcome.done = true
				settled := strings.Join(m.renderWelcomeLogo(78), "\n")
				assert.NotEqual(t, animatedFrame, settled)
				m.welcome.animated = false
				static := m.renderWelcomeLogo(78)
				require.Len(t, static, welcomeLogoHeight, "static welcomes omit the empty rain rows")
				assert.Equal(t, strings.Split(settled, "\n")[welcomeLogoTop:welcomeLogoTop+welcomeLogoHeight], static)
				m.welcome.effect = welcomeEffectNone
				assert.Equal(t, static, m.renderWelcomeLogo(78), "both effects settle on the same static wordmark")
			})
		}
	}
}

func TestWelcomeStyleSelection(t *testing.T) {
	for _, tt := range []struct {
		input string
		want  string
	}{
		{input: "", want: "block"},
		{input: " BLOCK ", want: "block"},
		{input: " Plain ", want: "plain"},
	} {
		t.Run(tt.input, func(t *testing.T) {
			style, err := ParseWelcomeStyle(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.want, style)
		})
	}
	err := Run(t.Context(), Config{Runner: &recordingRunner{}, WelcomeStyle: "unknown"})
	require.ErrorContains(t, err, `unknown welcome style "unknown"`)
	assert.ErrorContains(t, err, "available: block, plain")
}

func TestWelcomeResponsiveLayout(t *testing.T) {
	for _, style := range AvailableWelcomeStyles() {
		m := newWelcomeTestModel(t, Config{WelcomeStyle: style, WelcomeEffect: "beams"})
		for _, width := range []int{1, 7, 8, 30, welcomeCanvasWidth - 1, welcomeCanvasWidth, 80} {
			for _, height := range []int{1, 2, 3, welcomeLogoHeight + 1, welcomeLogoHeight + 2, welcomeCanvasHeight + 1, welcomeCanvasHeight + 2, 24} {
				m.viewport.SetWidth(width)
				m.viewport.SetHeight(height)
				lines := m.renderInitialMessage()
				require.LessOrEqual(t, len(lines), height, "%s: %dx%d", style, width, height)
				for _, line := range lines {
					require.LessOrEqual(t, lipgloss.Width(line), width, "%s: %dx%d", style, width, height)
				}
				if width >= len("kodelet.") && height >= 3 && (style == "plain" || width < welcomeCanvasWidth || height < welcomeLogoHeight+2) {
					assert.Contains(t, xansi.Strip(strings.Join(lines, "\n")), "kodelet.")
				}
			}
		}
	}
}

func TestWelcomeFallbackDoesNotChangeSelectedStyle(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	m := newModel(t.Context(), Config{Remote: true, WelcomeStyle: "block", WelcomeEffect: "beams"})
	t.Cleanup(m.cancel)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 30, Height: 24})
	m = updated.(model)
	assert.Equal(t, "block", m.welcome.style)
	assert.Contains(t, xansi.Strip(m.viewport.View()), "kodelet.")
	assert.True(t, m.welcome.done)

	updated, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(model)
	assert.Nil(t, cmd, "growing the terminal must not replay the effect")
	assert.Equal(t, "block", m.welcome.style)
	assert.NotContains(t, xansi.Strip(m.viewport.View()), "kodelet.")
	assert.Contains(t, xansi.Strip(m.viewport.View()), "█")
}

func TestWelcomeEffectSelection(t *testing.T) {
	for _, tt := range []struct {
		input string
		want  string
	}{
		{input: "", want: "none"},
		{input: " Beams ", want: "beams"},
		{input: " Matrix ", want: "matrix"},
		{input: " none ", want: "none"},
	} {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseWelcomeEffect(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
	err := Run(t.Context(), Config{Runner: &recordingRunner{}, WelcomeEffect: "unknown"})
	require.ErrorContains(t, err, `unknown welcome effect "unknown"`)
	assert.ErrorContains(t, err, "available: beams, matrix, none")
}

func TestWelcomeColorFadePreservesWordmark(t *testing.T) {
	for _, effect := range []string{"beams", "matrix"} {
		for _, theme := range []string{DefaultThemeName, LightThemeName} {
			t.Run(effect+"/"+theme, func(t *testing.T) {
				m := newWelcomeTestModel(t, Config{Theme: theme, WelcomeEffect: effect})
				settleFrame := welcomeBeamSettleFrame
				if effect == welcomeEffectMatrix {
					settleFrame = welcomeMatrixSettleFrame
				}
				m.welcome.frame = settleFrame
				start := strings.Join(m.renderWelcomeLogo(78), "\n")
				m.welcome.frame = (settleFrame + m.welcome.frames()) / 2
				middle := strings.Join(m.renderWelcomeLogo(78), "\n")
				m.welcome.frame = m.welcome.frames()
				settled := strings.Join(m.renderWelcomeLogo(78), "\n")
				assert.NotEqual(t, start, middle, "fade instead of snapping to the resting colors")
				assert.NotEqual(t, middle, settled)
				assert.Equal(t, xansi.Strip(start), xansi.Strip(middle))
				assert.Equal(t, xansi.Strip(middle), xansi.Strip(settled), "fading must not change geometry")
				text, _ := styleSequences(lipgloss.NewStyle().Foreground(themeColor(m.theme.Assistant)))
				dot, _ := styleSequences(lipgloss.NewStyle().Foreground(themeColor(m.welcomeDotColor())))
				assert.Contains(t, settled, text)
				assert.Contains(t, settled, dot)
				if effect == welcomeEffectMatrix {
					assert.Contains(t, middle, dot, "matrix fading must preserve the orange punctuation")
				}
			})
		}
	}
}

func BenchmarkWelcomeFrame(b *testing.B) {
	for _, effect := range []string{"beams", "matrix"} {
		b.Run(effect, func(b *testing.B) {
			m := model{theme: themes[DefaultThemeName]}
			m.viewport.SetWidth(80)
			m.viewport.SetHeight(20)
			m.welcome.effect, m.welcome.animated = effect, true
			b.ReportAllocs()
			for i := 0; b.Loop(); i++ {
				m.welcome.frame = i % m.welcome.frames()
				m.renderWelcomeLogo(78)
			}
		})
	}
}
