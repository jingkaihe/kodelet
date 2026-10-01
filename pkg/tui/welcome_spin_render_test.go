package tui

import (
	"math"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWelcomeSpinCanvasOutput(t *testing.T) {
	m := model{theme: themes[DefaultThemeName]}
	particles := newWelcomeParticles()
	for _, height := range []int{9, 12, 18, 24} {
		width := welcomeParticleWidth(height) + 20
		for frame := range 37 {
			phase := float64(frame) / 36
			canvas := rasterWelcomeParticles(particles, 0, height, phase*2*math.Pi, phase)
			lines := m.renderWelcomeSpinCanvas(width, height, canvas)
			require.Len(t, lines, height)
			require.NotEmpty(t, strings.TrimSpace(xansi.Strip(strings.Join(lines, ""))))
			for _, line := range lines {
				require.Equal(t, width, lipgloss.Width(line))
				assert.NotContains(t, line, "\n")
				assert.NotContains(t, line, "\x1b[48;", "never paint a cell background")
				for _, glyph := range xansi.Strip(line) {
					require.True(t, glyph == ' ' || glyph >= '\u2801' && glyph <= '\u28ff', "use Braille geometry, not solid blocks or randomized text")
				}
			}
		}
	}
	assert.Equal(t, themes[DefaultThemeName], m.theme)
	assert.Nil(t, m.renderWelcomeSpinCanvas(39, 0, welcomeSpinCanvas{}))
	assert.Nil(t, m.renderWelcomeSpinCanvas(39, -1, welcomeSpinCanvas{}))
	assert.Equal(t, []string{"", ""}, m.renderWelcomeSpinCanvas(0, 2, welcomeSpinCanvas{}))
	assert.Equal(t, []string{"", ""}, m.renderWelcomeSpinCanvas(-1, 2, welcomeSpinCanvas{}))
	canvas := rasterWelcomeParticles(particles, 0, 24, 0, 0)
	for _, line := range m.renderWelcomeSpinCanvas(7, 5, canvas) {
		assert.Equal(t, 7, lipgloss.Width(line), "clipping also respects a caller's smaller rectangle")
	}
}

func TestWelcomeSpinThemesAndTransparentPadding(t *testing.T) {
	dark := model{theme: themes[DefaultThemeName]}
	light := model{theme: themes[LightThemeName]}
	canvas := rasterWelcomeParticles(newWelcomeParticles(), 0, 12, 0, 0)
	darkLines, lightLines := dark.renderWelcomeSpinCanvas(80, 12, canvas), light.renderWelcomeSpinCanvas(80, 12, canvas)
	assert.NotEqual(t, darkLines, lightLines)
	for y, line := range darkLines {
		assert.Equal(t, xansi.Strip(line), xansi.Strip(lightLines[y]), "theme changes color, not geometry")
		assert.True(t, strings.HasPrefix(line, strings.Repeat(" ", (80-welcomeCanvasWidth)/2)))
		assert.True(t, strings.HasSuffix(line, strings.Repeat(" ", (80-welcomeCanvasWidth+1)/2)))
	}
	// Deliberately distinct custom colors prove that text and accent palettes
	// come from their theme roles, rather than hard-coded orange cube shading.
	custom := dark
	custom.theme.Assistant, custom.theme.Muted = "#123456", "#123456"
	assert.Contains(t, strings.Join(custom.renderWelcomeSpinCanvas(80, 12, canvas), ""), "\x1b[38;2;18;52;86m")
}

func TestWelcomeSpinBrailleEncoding(t *testing.T) {
	// The bottom pair are dots 7 and 8, not a continuation of the column bits.
	for _, tt := range []struct {
		x, y  int
		glyph rune
	}{
		{0, 0, '⠁'},
		{1, 0, '⠈'},
		{0, 1, '⠂'},
		{1, 1, '⠐'},
		{0, 2, '⠄'},
		{1, 2, '⠠'},
		{0, 3, '⡀'},
		{1, 3, '⢀'},
	} {
		canvas := welcomeSpinCanvas{width: 2, height: 4}
		canvas.plot(tt.x, tt.y, 1, 12, false)
		glyph, ink := canvas.cell(0, 0)
		assert.Equal(t, tt.glyph, glyph)
		assert.Equal(t, 12, ink)
		assert.Equal(t, 1, lipgloss.Width(string(glyph)))
	}
	canvas := welcomeSpinCanvas{width: 2, height: 4}
	for mask := range 256 {
		bits := [4][2]int{{1, 8}, {2, 16}, {4, 32}, {64, 128}}
		for y := range 4 {
			for x := range 2 {
				canvas.shade[y][x] = 0
				if mask&bits[y][x] != 0 {
					canvas.shade[y][x] = 12 + welcomeSpinShades
				}
			}
		}
		glyph, ink := canvas.cell(0, 0)
		if mask == 0 {
			assert.Equal(t, ' ', glyph)
			assert.Zero(t, ink)
		} else {
			assert.Equal(t, rune(0x2800+mask), glyph)
			assert.Equal(t, 12+welcomeSpinShades, ink)
		}
	}
	canvas.plot(0, 0, 1, 10, false)
	glyph, ink := canvas.cell(0, 0)
	assert.Equal(t, '⣿', glyph)
	assert.Equal(t, 10, ink, "body cells stay neutral when an accent particle overlaps")
}

func TestWelcomeSpinDotDepthBuffer(t *testing.T) {
	first, second := welcomeSpinCanvas{width: 2, height: 4}, welcomeSpinCanvas{width: 2, height: 4}
	first.plot(0, 0, 30, 12, false)
	first.plot(0, 0, 20, 8, true)
	second.plot(0, 0, 20, 8, true)
	second.plot(0, 0, 30, 12, false)
	assert.True(t, first == second, "nearest dot wins independent of sampling order")
	assert.Equal(t, uint8(12), first.shade[0][0])
	before := first
	for _, point := range [][2]int{{-1, 0}, {2, 0}, {0, -1}, {0, 4}} {
		first.plot(point[0], point[1], 40, 16, true)
	}
	assert.True(t, before == first, "no write outside the canvas")
}

func BenchmarkWelcomeSpin(b *testing.B) {
	m := model{theme: themes[DefaultThemeName]}
	particles := newWelcomeParticles()
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		phase := float64(i%180) / 180
		canvas := rasterWelcomeParticles(particles, 0, 24, phase*2*math.Pi, phase)
		m.renderWelcomeSpinCanvas(80, 24, canvas)
	}
}
