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
	for _, effect := range []string{DefaultWelcomeEffect, "matrix"} {
		t.Run(effect, func(t *testing.T) {
			interval := welcomeFrameInterval
			frames := welcomeMatrixFrames
			if effect == welcomeEffectBeams {
				interval = 10 * time.Millisecond
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
		{name: "matrix", effect: "matrix"},
		{name: "particles", particles: true},
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
		{name: "narrow", width: 30, height: 24, plain: true},
		{name: "short", width: 80, height: 10, plain: true},
		{name: "plain", width: 80, height: 24, config: Config{WelcomeStyle: "plain"}, plain: true},
		{name: "plain matrix", width: 80, height: 24, config: Config{WelcomeStyle: "plain", WelcomeEffect: "matrix"}, plain: true},
		{name: "plain none", width: 80, height: 24, config: Config{WelcomeStyle: "plain", WelcomeEffect: "none"}, plain: true},
		{name: "no color", width: 80, height: 24, noColor: "1", compact: true},
		{name: "dumb terminal", width: 80, height: 24, term: "dumb", compact: true},
		{name: "resume", width: 80, height: 24, config: Config{ConversationID: "saved"}},
		{name: "none", width: 80, height: 24, config: Config{WelcomeEffect: "none"}, compact: true},
		{name: "matrix no color", width: 80, height: 24, noColor: "1", config: Config{WelcomeEffect: "matrix"}, compact: true},
		{name: "matrix narrow", width: 30, height: 24, config: Config{WelcomeEffect: "matrix"}, plain: true},
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
		{name: "beams", effect: DefaultWelcomeEffect},
		{name: "matrix", effect: "matrix"},
		{name: "none", effect: "none"},
		{name: "plain", style: "plain", effect: "matrix"},
		{name: "no color", effect: DefaultWelcomeEffect, noColor: "1"},
		{name: "resumed", effect: DefaultWelcomeEffect, resumed: true},
		{name: "resumed matrix", effect: "matrix", resumed: true},
		{name: "resumed none", effect: "none", resumed: true},
		{name: "resumed plain", style: "plain", resumed: true},
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

func TestWelcomePaletteIsCachedPerThemeAndEffect(t *testing.T) {
	m := model{theme: themes[DefaultThemeName]}
	m.welcome.effect = DefaultWelcomeEffect
	palette := m.welcomePalette()
	assert.Same(t, palette, m.welcomePalette(), "frames must reuse the resolved palette")
	for i, hex := range map[int]string{0: "#8a008a", 10: "#00d1ff", 20: "#ffffff"} {
		color, _ := styleSequences(lipgloss.NewStyle().Foreground(themeColor(hex)))
		assert.Equal(t, color, palette[i], "beams need distinct tails, bodies, and tips")
	}

	m.welcome.effect = "matrix"
	assert.NotSame(t, palette, m.welcomePalette(), "matrix has its own colors")

	m.welcome.effect = DefaultWelcomeEffect
	m.theme = themes[LightThemeName]
	light := m.welcomePalette()
	assert.NotSame(t, palette, light)
	for i, hex := range map[int]string{0: "#8a008a", 10: "#007c91", 20: "#173b6c"} {
		color, _ := styleSequences(lipgloss.NewStyle().Foreground(themeColor(hex)))
		assert.Equal(t, color, light[i], "light backgrounds need darker beams, not white tips")
	}
}

func TestWelcomeBeamColorRevealAndSettle(t *testing.T) {
	for _, theme := range []string{DefaultThemeName, LightThemeName, "tokyo-night"} {
		t.Run(theme, func(t *testing.T) {
			m := newModel(t.Context(), Config{Remote: true, Theme: theme})
			t.Cleanup(m.cancel)
			m.welcome.animated, m.welcome.done = true, false
			m.welcome.frame = welcomeBeamSettleFrame
			lines := m.renderWelcomeLogo(78)
			rowColors := map[string]bool{}
			for y := welcomeLogoTop; y < welcomeLogoTop+welcomeLogoHeight; y++ {
				palette := m.welcomeBeamLogoPalette(y, welcomeBeamSettleFrame, m.theme.Assistant)
				assert.Same(t, palette, m.welcomeBeamLogoPalette(y, 0, m.theme.Assistant), "retain the gradient until the entire wipe completes")
				assert.Contains(t, lines[y], palette[10], "the actual renderer must use the colored reveal")
				rowColors[palette[10]] = true
				for _, finalColor := range []string{m.theme.Assistant, m.welcomeDotColor()} {
					start := m.welcomeBeamLogoPalette(y, welcomeBeamSettleFrame, finalColor)
					middle := m.welcomeBeamLogoPalette(y, (welcomeBeamSettleFrame+welcomeFrames)/2, finalColor)
					end := m.welcomeBeamLogoPalette(y, welcomeFrames, finalColor)
					assert.Equal(t, palette, start, "even the dot remains colored until the settle")
					assert.NotEqual(t, start[10], middle[10], "fade rather than snapping to brand colors")
					assert.NotEqual(t, middle[10], end[10])
					want, _ := styleSequences(lipgloss.NewStyle().Foreground(themeColor(finalColor)))
					assert.Equal(t, want, end[10])
				}
			}
			assert.Len(t, rowColors, welcomeLogoHeight, "the reveal needs a spatial gradient, not a flat accent")
		})
	}
}

func TestWelcomeAnimationStopsWhenLeavingWelcome(t *testing.T) {
	for _, reason := range []string{"resize", "transcript", "new conversation"} {
		t.Run(reason, func(t *testing.T) {
			m := newWelcomeTestModel(t, Config{})
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
	for _, row := range welcomeLogo {
		assert.Len(t, row, welcomeCanvasWidth)
	}
	for _, effect := range []string{DefaultWelcomeEffect, "matrix"} {
		for _, themeName := range []string{DefaultThemeName, LightThemeName, "tokyo-night"} {
			t.Run(effect+"/"+themeName, func(t *testing.T) {
				m := newModel(t.Context(), Config{Theme: themeName, WelcomeEffect: effect})
				t.Cleanup(m.cancel)
				m.welcome.animated, m.welcome.done = true, false
				for frame := range m.welcome.frames() + 1 {
					m.welcome.frame = frame
					lines := m.renderWelcomeLogo(78)
					require.Len(t, lines, welcomeCanvasHeight)
					for y, line := range lines {
						assert.Equal(t, 78, lipgloss.Width(line), "frame %d row %d", frame, y)
						if frame == m.welcome.frames() {
							if y < welcomeLogoTop || y >= welcomeLogoTop+welcomeLogoHeight {
								assert.Empty(t, strings.TrimSpace(xansi.Strip(line)))
								continue
							}
							left := (78 - len(welcomeLogo[0])) / 2
							cells := []rune(xansi.Strip(line))[left : left+len(welcomeLogo[0])]
							logoY := (y - welcomeLogoTop) * 2
							for x, cell := range cells {
								assert.Equal(t, welcomeLogo[logoY][x] == '#', cell == '█' || cell == '▀')
								bottom := logoY+1 < len(welcomeLogo) && welcomeLogo[logoY+1][x] == '#'
								assert.Equal(t, bottom, cell == '█' || cell == '▄')
							}
						}
					}
				}
				m.welcome.frame = 50
				animatedFrame := strings.Join(m.renderWelcomeLogo(78), "\n")
				if effect == DefaultWelcomeEffect {
					assert.True(t, strings.ContainsAny(animatedFrame, "▂▁_▌▍▎▏"))
				} else {
					assert.True(t, strings.ContainsAny(animatedFrame, string(welcomeMatrixSymbols)))
				}
				m.welcome.done = true
				settled := strings.Join(m.renderWelcomeLogo(78), "\n")
				assert.False(t, strings.ContainsAny(xansi.Strip(settled), "▂▁_▌▍▎▏"))
				assert.NotEqual(t, animatedFrame, settled)
				text, _ := styleSequences(lipgloss.NewStyle().Foreground(themeColor(m.theme.Assistant)))
				dot, _ := styleSequences(lipgloss.NewStyle().Foreground(themeColor(m.welcomeDotColor())))
				assert.Equal(t, welcomeLogoHeight, strings.Count(settled, text), "one text span per settled row")
				assert.Equal(t, 1, strings.Count(settled, dot), "only the punctuation is orange")
				m.welcome.animated = false
				static := m.renderWelcomeLogo(78)
				require.Len(t, static, welcomeLogoHeight, "static welcomes omit the empty rain rows")
				assert.Equal(t, strings.Split(settled, "\n")[welcomeLogoTop:welcomeLogoTop+welcomeLogoHeight], static)
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
			m := newModel(t.Context(), Config{Remote: true, WelcomeStyle: tt.input})
			t.Cleanup(m.cancel)
			assert.Equal(t, tt.want, m.welcome.style)
			if tt.want == "plain" {
				assert.False(t, m.welcome.animated)
				assert.True(t, m.welcome.done)
			}
		})
	}
	assert.Equal(t, []string{"block", "plain"}, AvailableWelcomeStyles())
	_, err := ParseWelcomeStyle("unknown")
	require.ErrorContains(t, err, "available: block, plain")
	err = Run(t.Context(), Config{Runner: &recordingRunner{}, WelcomeStyle: "unknown"})
	require.ErrorContains(t, err, `unknown welcome style "unknown"`)
}

func TestWelcomePlainWordmarkUsesBrandColors(t *testing.T) {
	for _, theme := range []string{DefaultThemeName, LightThemeName, "tokyo-night"} {
		t.Run(theme, func(t *testing.T) {
			m := newModel(t.Context(), Config{Remote: true, Theme: theme, WelcomeStyle: "plain"})
			t.Cleanup(m.cancel)
			logo := m.renderWelcomeLogo(78)
			require.Len(t, logo, 1)
			assert.Equal(t, 78, lipgloss.Width(logo[0]))
			assert.Equal(t, "kodelet.", strings.TrimSpace(xansi.Strip(logo[0])))
			text := lipgloss.NewStyle().Foreground(themeColor(m.theme.Assistant)).Bold(true).Render("kodelet")
			dot := lipgloss.NewStyle().Foreground(themeColor(m.welcomeDotColor())).Bold(true).Render(".")
			assert.Contains(t, logo[0], text+dot)
		})
	}
}

func TestWelcomeResponsiveLayout(t *testing.T) {
	for _, style := range AvailableWelcomeStyles() {
		for _, effect := range AvailableWelcomeEffects() {
			m := newModel(t.Context(), Config{Remote: true, WelcomeStyle: style, WelcomeEffect: effect})
			t.Cleanup(m.cancel)
			for _, width := range []int{1, 7, 8, 30, welcomeCanvasWidth - 1, welcomeCanvasWidth, 80} {
				for _, height := range []int{1, 2, 3, welcomeLogoHeight + 1, welcomeLogoHeight + 2, welcomeCanvasHeight + 1, welcomeCanvasHeight + 2, 24} {
					m.viewport.SetWidth(width)
					m.viewport.SetHeight(height)
					lines := m.renderInitialMessage()
					require.LessOrEqual(t, len(lines), height, "%s/%s: %dx%d", style, effect, width, height)
					for _, line := range lines {
						require.LessOrEqual(t, lipgloss.Width(line), width, "%s/%s: %dx%d", style, effect, width, height)
					}
					if width >= len("kodelet.") && height >= 3 && (style == "plain" || width < welcomeCanvasWidth || height < welcomeLogoHeight+2) {
						assert.Contains(t, xansi.Strip(strings.Join(lines, "\n")), "kodelet.")
					}
				}
			}
		}
	}
}

func TestWelcomeFallbackDoesNotChangeSelectedStyle(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	m := newModel(t.Context(), Config{Remote: true, WelcomeStyle: "block"})
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
		{input: "", want: "beams"},
		{input: "beams", want: "beams"},
		{input: " Matrix ", want: "matrix"},
		{input: " none ", want: "none"},
	} {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseWelcomeEffect(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			m := newModel(t.Context(), Config{WelcomeEffect: tt.input})
			t.Cleanup(m.cancel)
			assert.Equal(t, tt.want, m.welcome.effect)
			if tt.want == "none" {
				assert.True(t, m.welcome.done)
				assert.Nil(t, m.startWelcomeAnimation())
			}
		})
	}
	assert.Equal(t, []string{"beams", "matrix", "none"}, AvailableWelcomeEffects())
	_, err := ParseWelcomeEffect("unknown")
	require.ErrorContains(t, err, "available: beams, matrix, none")
	err = Run(t.Context(), Config{Runner: &recordingRunner{}, WelcomeEffect: "unknown"})
	require.ErrorContains(t, err, `unknown welcome effect "unknown"`)
}

func TestWelcomeMatrixRainAndResolve(t *testing.T) {
	for _, glyph := range welcomeMatrixSymbols {
		assert.Equal(t, 1, lipgloss.Width(string(glyph)), "half-width kana must not widen the canvas")
	}
	filled := 0
	resolve := welcomeFrames
	for x, col := range welcomeMatrixColumns {
		filled = max(filled, col.fillStart+(welcomeCanvasHeight-1)*col.fillDelay)
		for y := range welcomeCanvasHeight {
			resolve = min(resolve, col.resolve[y])
			assert.LessOrEqual(t, col.resolve[y]+24+15, welcomeMatrixSettleFrame, "hold the complete green wordmark for at least a quarter second before fading")
			frame := col.start + y*col.delay
			if frame >= welcomeMatrixRainEnd {
				continue
			}
			before, _ := welcomeCell(x, y, frame-1, "matrix")
			assert.Equal(t, ' ', before)
			head, color := welcomeCell(x, y, frame, "matrix")
			assert.NotEqual(t, ' ', head)
			assert.Equal(t, 20, color, "the falling head should be highlighted")
		}
	}
	assert.Less(t, filled, resolve, "hold the dense rain before beginning the resolve")
	for y := range welcomeCanvasHeight {
		for x := range welcomeCanvasWidth {
			glyph, _ := welcomeCell(x, y, filled, "matrix")
			assert.NotEqual(t, ' ', glyph, "all columns fill before resolving")
			want := welcomeSymbol(x, y)
			for _, effect := range []string{"matrix", "none"} {
				frame := welcomeMatrixFrames - 1
				if effect == "none" {
					frame = 0
				}
				got, color := welcomeCell(x, y, frame, effect)
				assert.Equal(t, want, got, "both effects must settle on the same wordmark")
				assert.LessOrEqual(t, color, 10)
				assert.GreaterOrEqual(t, color, 0)
			}
		}
	}
}

func TestWelcomeMatrixGreenHoldAndNeutralFade(t *testing.T) {
	assert.Equal(t, 270, welcomeMatrixFrames, "Matrix lasts 4.5 seconds at 60 frames per second")
	resolved := 0
	for _, col := range welcomeMatrixColumns {
		for _, frame := range col.resolve {
			resolved = max(resolved, frame+24)
		}
	}
	for _, theme := range []string{DefaultThemeName, LightThemeName, "tokyo-night"} {
		t.Run(theme, func(t *testing.T) {
			m := newWelcomeTestModel(t, Config{Theme: theme, WelcomeEffect: "matrix"})
			green := m.welcomePalette()
			text, _ := styleSequences(lipgloss.NewStyle().Foreground(themeColor(m.theme.Assistant)))
			dot, _ := styleSequences(lipgloss.NewStyle().Foreground(themeColor(m.welcomeDotColor())))
			x, y := 0, welcomeLogoTop
			for _, age := range []int{0, 12, 24} {
				frame := welcomeMatrixColumns[x].resolve[y] + age
				m.updateWelcomeAnimation(m.welcome.startedAt.Add(time.Duration(frame) * welcomeFrameInterval))
				glyph, shade := welcomeMatrixCell(x, y, frame, welcomeSymbol(x, y))
				assert.Contains(t, m.renderWelcomeLogo(78)[y], green[shade]+string(glyph), "letters remain green throughout their reveal")
			}
			var held string
			for _, frame := range []int{resolved, welcomeMatrixSettleFrame} {
				m.updateWelcomeAnimation(m.welcome.startedAt.Add(time.Duration(frame) * welcomeFrameInterval))
				logo := strings.Join(m.renderWelcomeLogo(78), "\n")
				assert.Equal(t, welcomeLogoHeight, strings.Count(logo, green[10]), "hold a uniform green logo after all rain resolves")
				assert.Equal(t, 1, strings.Count(logo, dot))
				assert.NotContains(t, xansi.Strip(m.View().Content), "? for shortcuts")
				if held != "" {
					assert.Equal(t, held, logo, "the hold must not shimmer")
				}
				held = logo
			}
			midpoint := (welcomeMatrixSettleFrame + welcomeMatrixFrames) / 2
			m.updateWelcomeAnimation(m.welcome.startedAt.Add(time.Duration(midpoint) * welcomeFrameInterval))
			middle := m.welcomeMatrixLogoPalette(midpoint)
			assert.Same(t, middle, m.welcomeMatrixLogoPalette(midpoint), "reuse cached fade colors")
			assert.NotEqual(t, green[10], middle[10])
			assert.NotEqual(t, text, middle[10], "blend smoothly rather than snapping to neutral")
			logo := strings.Join(m.renderWelcomeLogo(78), "\n")
			assert.Equal(t, welcomeLogoHeight, strings.Count(logo, middle[10]), "the entire wordmark fades together")
			assert.Equal(t, 1, strings.Count(logo, dot), "the orange dot does not fade to neutral")
			assert.Equal(t, xansi.Strip(held), xansi.Strip(logo), "fading must not change geometry")
			assert.NotContains(t, xansi.Strip(m.View().Content), "? for shortcuts", "wait for the final fade")
			assert.Equal(t, text, m.welcomeMatrixLogoPalette(welcomeMatrixFrames)[10], "the last fade color matches the resting theme")
			interrupted := m
			updated, _ := interrupted.Update(textKeyPress("hello"))
			interrupted = updated.(model)
			assert.True(t, interrupted.welcome.done)
			assert.Equal(t, "hello", interrupted.textarea.Value())
			assert.Nil(t, m.updateWelcomeAnimation(m.welcome.startedAt.Add(welcomeMatrixFrames*welcomeFrameInterval)))
			settled := strings.Join(m.renderWelcomeLogo(78), "\n")
			assert.Equal(t, settled, strings.Join(interrupted.renderWelcomeLogo(78), "\n"), "input skips directly to the neutral resting wordmark")
			assert.Equal(t, welcomeLogoHeight, strings.Count(settled, text))
			assert.Equal(t, 1, strings.Count(settled, dot))
			assert.Contains(t, xansi.Strip(m.View().Content), "? for shortcuts")
		})
	}
}

func BenchmarkWelcomeFrame(b *testing.B) {
	for _, effect := range []string{DefaultWelcomeEffect, "matrix"} {
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

func TestWelcomeBeamSchedule(t *testing.T) {
	assert.Equal(t, 2400*time.Millisecond, welcomeFrames*welcomeBeamFrameInterval)
	assert.Equal(t, welcomeBeams, buildWelcomeBeamSchedule(), "the activation schedule must be reproducible")
	var rows, columns [welcomeCanvasHeight][welcomeCanvasWidth]int
	lastHit := 0
	for y := range welcomeCanvasHeight {
		for x := range welcomeCanvasWidth {
			hits := welcomeBeams.hits[y][x]
			assert.NotEqual(t, hits[0].column, hits[1].column, "cell (%d,%d) needs one row and one column sweep", x, y)
			assert.LessOrEqual(t, hits[0].frame, hits[1].frame, "cell (%d,%d) hits must be ordered", x, y)
			for _, hit := range hits {
				assert.GreaterOrEqual(t, hit.frame, 0, "every sweep must reach cell (%d,%d)", x, y)
				assert.Less(t, hit.frame+41, welcomeBeams.wipeStart, "the complete beam and fade scene must precede the wipe")
				lastHit = max(lastHit, hit.frame)
				if hit.column {
					columns[y][x] = hit.frame
				} else {
					rows[y][x] = hit.frame
				}
			}
		}
	}
	assert.Equal(t, lastHit+42, welcomeBeams.wipeStart, "the wipe must wait for the last complete scene")
	assert.LessOrEqual(t, welcomeBeams.wipeStart+(welcomeCanvasWidth+welcomeCanvasHeight-2)/3+40, welcomeBeamSettleFrame, "finish the colored wipe before fading to the quiet wordmark")

	rowForward, rowReverse, columnForward, columnReverse := false, false, false, false
	for y := range welcomeCanvasHeight {
		forward := rows[y][0] < rows[y][welcomeCanvasWidth-1]
		assert.NotEqual(t, rows[y][0], rows[y][welcomeCanvasWidth-1], "row sweep must traverse the canvas")
		rowForward, rowReverse = rowForward || forward, rowReverse || !forward
		for x := 1; x < welcomeCanvasWidth; x++ {
			if forward {
				assert.GreaterOrEqual(t, rows[y][x], rows[y][x-1])
			} else {
				assert.LessOrEqual(t, rows[y][x], rows[y][x-1])
			}
		}
	}
	for x := range welcomeCanvasWidth {
		forward := columns[0][x] < columns[welcomeCanvasHeight-1][x]
		assert.NotEqual(t, columns[0][x], columns[welcomeCanvasHeight-1][x], "column sweep must traverse the canvas")
		columnForward, columnReverse = columnForward || forward, columnReverse || !forward
		for y := 1; y < welcomeCanvasHeight; y++ {
			if forward {
				assert.GreaterOrEqual(t, columns[y][x], columns[y-1][x])
			} else {
				assert.LessOrEqual(t, columns[y][x], columns[y-1][x])
			}
		}
	}
	assert.True(t, rowForward && rowReverse, "row beams must enter from both sides")
	assert.True(t, columnForward && columnReverse, "column beams must enter from both sides")
	t.Logf("last crossing: frame %d; wipe starts: frame %d", lastHit, welcomeBeams.wipeStart)
}

func TestWelcomeBeamFrameBoundsAndSettle(t *testing.T) {
	seen := map[rune]bool{}
	beamColors := map[int]bool{}
	for frame := -1; frame <= welcomeFrames; frame++ {
		for y := range welcomeCanvasHeight {
			for x := range welcomeCanvasWidth {
				final := welcomeSymbol(x, y)
				glyph, color := welcomeBeamCell(x, y, frame, final)
				require.GreaterOrEqual(t, color, 0, "frame %d cell (%d,%d)", frame, x, y)
				require.LessOrEqual(t, color, 20, "frame %d cell (%d,%d)", frame, x, y)
				require.Equal(t, 1, lipgloss.Width(string(glyph)), "frame %d cell (%d,%d)", frame, x, y)
				seen[glyph] = true
				if glyph != final && glyph != ' ' {
					beamColors[color] = true
				}
				if frame < welcomeBeams.hits[y][x][0].frame {
					assert.Equal(t, ' ', glyph, "cells must remain hidden before their first sweep")
				}
				if frame == welcomeBeams.wipeStart-1 {
					assert.Equal(t, final, glyph, "every beam must have resolved before the wipe")
					assert.Zero(t, color, "revealed text must be at 30%% brightness before the wipe")
				}
				if frame == welcomeFrames {
					assert.Equal(t, final, glyph)
					assert.Equal(t, 10, color, "the final frame must be fully settled without relying on the wrapper")
				}
			}
		}
	}
	for _, glyph := range "▂▁_▌▍▎▏" {
		assert.True(t, seen[glyph], "the animation must display thinning glyph %q", glyph)
	}
	for _, shade := range []int{20, 10, 2} {
		assert.True(t, beamColors[shade], "moving trails must span the palette, not just its brightest half")
	}
}

func TestWelcomeBeamCrossingRestartsScene(t *testing.T) {
	overlaps := 0
	for y := range welcomeCanvasHeight {
		for x := range welcomeCanvasWidth {
			hits := welcomeBeams.hits[y][x]
			if hits[1].frame <= hits[0].frame || hits[1].frame >= hits[0].frame+42 {
				continue
			}
			overlaps++
			final := welcomeSymbol(x, y)
			_, before := welcomeBeamCell(x, y, hits[1].frame-1, final)
			glyph, color := welcomeBeamCell(x, y, hits[1].frame, final)
			want := '▂'
			if hits[1].column {
				want = '▌'
			}
			assert.Equal(t, want, glyph, "the crossing must restart with the new direction's thick head")
			assert.Equal(t, 20, color)
			assert.GreaterOrEqual(t, color, before)
			glyph, color = welcomeBeamCell(x, y, hits[1].frame+20, final)
			assert.Equal(t, final, glyph, "the restarted trail must reveal the actual final glyph")
			assert.Equal(t, 10, color, "fade timing must start from the later crossing")
		}
	}
	assert.Positive(t, overlaps, "the schedule must exercise overlapping beam scenes")
}

func TestWelcomeBeamDiagonalBrighten(t *testing.T) {
	for y := range welcomeCanvasHeight {
		for x := range welcomeCanvasWidth {
			final := welcomeSymbol(x, y)
			start := welcomeBeams.wipeStart + (x+y)/3
			_, before := welcomeBeamCell(x, y, start-1, final)
			assert.Zero(t, before)
			for step := 0; step <= 10; step++ {
				for offset := range 4 {
					glyph, color := welcomeBeamCell(x, y, start+step*4+offset, final)
					assert.Equal(t, final, glyph, "brightening must never replace the wordmark with beam glyphs")
					assert.Equal(t, step, color, "each diagonal brightens in four-frame steps")
				}
			}
		}
	}
}
