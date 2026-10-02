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
		for _, phase := range []float64{0, 0.25, 0.5, 0.75, 1} {
			canvas := rasterWelcomeParticles(particles, 0, height, phase*2*math.Pi, phase)
			lines := m.renderWelcomeSpinCanvas(width, height, canvas)
			require.Len(t, lines, height)
			require.NotEmpty(t, strings.TrimSpace(xansi.Strip(strings.Join(lines, ""))))
			for _, line := range lines {
				require.Equal(t, width, lipgloss.Width(line))
				assert.NotContains(t, line, "\x1b[48;", "never paint a cell background")
				for _, glyph := range xansi.Strip(line) {
					require.True(t, glyph == ' ' || glyph >= '\u2801' && glyph <= '\u28ff', "use Braille geometry, not solid blocks or randomized text")
				}
			}
		}
	}
	canvas := rasterWelcomeParticles(particles, 0, 24, 0, 0)
	for _, line := range m.renderWelcomeSpinCanvas(7, 5, canvas) {
		assert.Equal(t, 7, lipgloss.Width(line), "clipping also respects a caller's smaller rectangle")
	}
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
	glyph, ink := canvas.cell(0, 0)
	assert.Equal(t, ' ', glyph)
	assert.Zero(t, ink)
	for y := range 4 {
		for x := range 2 {
			canvas.plot(x, y, 1, 12, true)
		}
	}
	glyph, ink = canvas.cell(0, 0)
	assert.Equal(t, '⣿', glyph)
	assert.Equal(t, 12+welcomeSpinShades, ink)
	canvas.plot(0, 0, 2, 10, false)
	glyph, ink = canvas.cell(0, 0)
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
