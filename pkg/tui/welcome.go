package tui

import (
	"image/color"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/pkg/errors"
)

const (
	welcomeEffectBeams  = "beams"
	welcomeEffectMatrix = "matrix"
	welcomeEffectNone   = "none"

	// DefaultWelcomeEffect is used when no effect is selected.
	DefaultWelcomeEffect = welcomeEffectBeams
)

var welcomeEffects = []string{welcomeEffectBeams, welcomeEffectMatrix, welcomeEffectNone}

const (
	welcomeFrameInterval = time.Second / 60
	welcomeFrames        = 240
	welcomeLogoHeight    = (len(welcomeLogo) + 1) / 2
	welcomeCanvasWidth   = 41
	welcomeCanvasHeight  = 9
	welcomeLogoTop       = (welcomeCanvasHeight - welcomeLogoHeight) / 2
	welcomePaletteSize   = 21
)

var welcomeLogo = [...]string{
	"#   #  ###  ####  ##### #     ##### #####",
	"#  #  #   # #   # #     #     #       #  ",
	"###   #   # #   # ####  #     ####    #  ",
	"#  #  #   # #   # #     #     #       #  ",
	"#   #  ###  ####  ##### ##### #####   #  ",
}

type welcomeAnimation struct {
	effect    string
	animated  bool
	startedAt time.Time
	frame     int
	done      bool
}

type welcomeTickMsg time.Time

// AvailableWelcomeEffects returns the supported startup animation names.
func AvailableWelcomeEffects() []string {
	return slices.Clone(welcomeEffects)
}

// ParseWelcomeEffect normalizes an effect name; blank selects the default.
func ParseWelcomeEffect(effect string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(effect))
	if name == "" {
		return DefaultWelcomeEffect, nil
	}
	if !slices.Contains(welcomeEffects, name) {
		return "", errors.Errorf(
			"unknown welcome effect %q (available: %s)",
			strings.TrimSpace(effect),
			strings.Join(welcomeEffects, ", "),
		)
	}
	return name, nil
}

func newWelcomeAnimation(effect string) welcomeAnimation {
	name, err := ParseWelcomeEffect(effect)
	if err != nil {
		name = DefaultWelcomeEffect
	}
	animated := name != welcomeEffectNone && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
	return welcomeAnimation{effect: name, animated: animated, done: !animated}
}

func welcomeTick() tea.Cmd {
	return tea.Tick(welcomeFrameInterval, func(now time.Time) tea.Msg {
		return welcomeTickMsg(now)
	})
}

func (m model) welcomeLogoRows() int {
	switch {
	case m.viewport.Width() < welcomeCanvasWidth || m.viewport.Height() < welcomeLogoHeight+6:
		return 0
	case m.welcome.animated && m.viewport.Height() >= welcomeCanvasHeight+5:
		return welcomeCanvasHeight
	default:
		return welcomeLogoHeight
	}
}

func (m model) welcomeLogoPending() bool {
	return m.startupPending || m.initialHistoryPending
}

func (m model) welcomeLogoVisible() bool {
	return !m.welcomeLogoPending() && len(m.entries) == 0 && m.conversationID == "" && m.welcomeLogoRows() > 0
}

func (m *model) startWelcomeAnimation() tea.Cmd {
	if m.welcome.done || !m.welcome.startedAt.IsZero() || m.startupPending || m.width <= 0 || m.height <= 0 {
		return nil
	}
	if !m.welcomeLogoVisible() || m.welcomeLogoRows() != welcomeCanvasHeight || m.textarea.Value() != "" {
		m.welcome.done = true
		return nil
	}
	m.welcome.startedAt = time.Now()
	return welcomeTick()
}

func (m *model) finishWelcomeAnimation() {
	if m.welcome.done {
		return
	}
	m.welcome.done = true
	if len(m.entries) == 0 {
		m.refreshViewport(false)
	}
}

func (m *model) updateWelcomeAnimation(now time.Time) tea.Cmd {
	if m.welcome.done || m.welcome.startedAt.IsZero() {
		return nil
	}
	if !m.welcomeLogoVisible() || m.welcomeLogoRows() != welcomeCanvasHeight || m.running || m.textarea.Value() != "" {
		m.finishWelcomeAnimation()
		return nil
	}
	m.welcome.frame = max(0, int(now.Sub(m.welcome.startedAt)/welcomeFrameInterval))
	if m.welcome.frame >= welcomeFrames {
		m.finishWelcomeAnimation()
		return nil
	}
	m.refreshViewport(false)
	return welcomeTick()
}

func (m model) renderWelcomeLogo(width int) []string {
	palette := m.welcomePalette()
	frame := m.welcome.frame
	if m.welcome.done {
		frame = welcomeFrames
	}
	height, top := welcomeCanvasHeight, 0
	if m.welcomeLogoRows() != welcomeCanvasHeight {
		height, top, frame = welcomeLogoHeight, welcomeLogoTop, welcomeFrames
	}
	lines := make([]string, height)
	for y := range height {
		var line strings.Builder
		line.Grow(len(welcomeLogo[0]) * 8)
		previousColor := -1
		for x := range len(welcomeLogo[0]) {
			symbol, color := welcomeCell(x, y+top, frame, m.welcome.effect)
			if symbol != ' ' && color != previousColor {
				line.WriteString(palette[color])
				previousColor = color
			}
			line.WriteRune(symbol)
		}
		line.WriteString(ansiResetSequence)
		lines[y] = centerVisible(line.String(), width)
	}
	return lines
}

type welcomePaletteKey struct {
	accent, highlight string
	dark, matrix      bool
}

var welcomePalettes = struct {
	sync.Mutex
	byKey map[welcomePaletteKey]*[welcomePaletteSize]string
}{byKey: map[welcomePaletteKey]*[welcomePaletteSize]string{}}

func (m model) welcomePalette() *[welcomePaletteSize]string {
	key := welcomePaletteKey{dark: m.theme.Dark, matrix: m.welcome.effect == welcomeEffectMatrix}
	if !key.matrix {
		key.accent, key.highlight = m.theme.ComposerFlow, m.theme.Assistant
	}
	welcomePalettes.Lock()
	defer welcomePalettes.Unlock()
	palette, ok := welcomePalettes.byKey[key]
	if !ok {
		palette = buildWelcomePalette(key)
		welcomePalettes.byKey[key] = palette
	}
	return palette
}

func buildWelcomePalette(key welcomePaletteKey) *[welcomePaletteSize]string {
	var dim, accent, highlight color.Color
	if key.matrix {
		dim, accent, highlight = themeColor("#185318"), themeColor("#92be92"), themeColor("#dbffdb")
		if !key.dark {
			dim, accent, highlight = themeColor("#b2ccb6"), themeColor("#28723a"), themeColor("#0b3916")
		}
	} else {
		accent, highlight = themeColor(key.accent), themeColor(key.highlight)
		background := color.Color(color.Black)
		if !key.dark {
			background = color.White
		}
		dim = welcomeBlend(background, accent, 0.3)
	}
	var palette [welcomePaletteSize]string
	for i := range palette {
		c := welcomeBlend(dim, accent, float64(min(i, 10))/10)
		if i > 10 {
			c = welcomeBlend(accent, highlight, float64(i-10)/10)
		}
		palette[i], _ = styleSequences(lipgloss.NewStyle().Foreground(c))
	}
	return &palette
}

func welcomeBlend(from, to color.Color, amount float64) color.RGBA {
	r1, g1, b1, _ := from.RGBA()
	r2, g2, b2, _ := to.RGBA()
	return color.RGBA{
		R: uint8((float64(r1)*(1-amount) + float64(r2)*amount) / 257),
		G: uint8((float64(g1)*(1-amount) + float64(g2)*amount) / 257),
		B: uint8((float64(b1)*(1-amount) + float64(b2)*amount) / 257),
		A: 255,
	}
}

func welcomeSymbol(x, y int) rune {
	y -= welcomeLogoTop
	if y < 0 || y >= welcomeLogoHeight {
		return ' '
	}
	top := welcomeLogo[y*2][x] != ' '
	bottom := y*2+1 < len(welcomeLogo) && welcomeLogo[y*2+1][x] != ' '
	symbol := ' '
	switch {
	case top && bottom:
		symbol = '█'
	case top:
		symbol = '▀'
	case bottom:
		symbol = '▄'
	}
	return symbol
}

func welcomeCell(x, y, frame int, effect string) (rune, int) {
	symbol := welcomeSymbol(x, y)
	if effect == welcomeEffectMatrix {
		return welcomeMatrixCell(x, y, frame, symbol)
	}
	if frame >= welcomeFrames || effect == welcomeEffectNone {
		return symbol, 10
	}
	return welcomeBeamCell(x, y, frame, symbol)
}
