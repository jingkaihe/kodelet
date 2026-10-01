package tui

import (
	"math"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWelcomeSpinDeterministicRotation(t *testing.T) {
	m := model{theme: themes[DefaultThemeName]}
	initial := m.renderWelcomeSpin(80, 9, 0)
	assert.Equal(t, initial, m.renderWelcomeSpin(80, 9, 0))
	assert.Equal(t, initial, m.renderWelcomeSpin(80, 9, welcomeSpinPeriod))
	assert.NotEqual(t, initial, m.renderWelcomeSpin(80, 9, welcomeSpinPeriod/4))
	assert.NotEqual(t, initial, m.renderWelcomeSpin(80, 9, welcomeSpinPeriod/2))
	assert.Equal(t, themes[DefaultThemeName], m.theme, "rendering must not alter the theme")

	// Cancel the initial yaw to lock the optically adjusted front silhouette,
	// including the separate underscore and pitch that exposes the extrusion.
	front := m.renderWelcomeSpin(39, 9, -welcomeSpinPeriod/24)
	for i, line := range front {
		front[i] = strings.TrimRight(xansi.Strip(line), " ")
	}
	assert.Equal(t, `            ▄▄▄
            ██▌  ▄██▖
            ██▌▄██▛▘
            █████▙
            ▜█▛▀▜█▙▖
            ▐█▌  ▀█▙   ▗▄▄▄▖
            ▝▀▀   ▝    ▐███▛

`, strings.Join(front, "\n"))
}

func TestWelcomeSpinUnderscoreSeparation(t *testing.T) {
	// Check the displayed quadrant pixels, not just the underlying mesh: a
	// geometric gap can still close during downsampling to terminal resolution.
	for _, elapsed := range []time.Duration{-welcomeSpinPeriod / 8, -welcomeSpinPeriod / 24, 0, welcomeSpinPeriod / 24} {
		canvas := rasterWelcomeSpin(welcomeCanvasWidth, welcomeCanvasHeight, elapsed)
		runs, previous := 0, false
		for x := range canvas.width / 2 {
			occupied := false
			for y := range canvas.height / 2 {
				if canvas.pixel(x, y) != 0 {
					occupied = true
					break
				}
			}
			if occupied && !previous {
				runs++
			}
			previous = occupied
		}
		assert.Equal(t, 2, runs, "K and underscore must have clear space between them at %s", elapsed)
	}
}

func TestWelcomeSpinBoundsAndClipping(t *testing.T) {
	m := model{theme: themes[DefaultThemeName]}
	for _, size := range [][2]int{{1, 1}, {7, 1}, {20, 4}, {39, 9}, {80, 9}, {160, 40}} {
		for frame := range 120 {
			elapsed := time.Duration(frame) * welcomeSpinPeriod / 120
			lines := m.renderWelcomeSpin(size[0], size[1], elapsed)
			require.Len(t, lines, size[1])
			if size[0] >= welcomeCanvasWidth && size[1] >= welcomeCanvasHeight {
				require.NotEmpty(t, strings.TrimSpace(xansi.Strip(strings.Join(lines, ""))), "logo must not disappear during rotation")
			}
			for _, line := range lines {
				require.Equal(t, size[0], lipgloss.Width(line))
				assert.NotContains(t, line, "\n")
				assert.NotContains(t, line, "NaN")
				assert.NotContains(t, line, "Inf")
				for _, glyph := range xansi.Strip(line) {
					require.Contains(t, " ▘▝▀▖▌▞▛▗▚▐▜▄▙▟█", string(glyph), "use quadrant geometry, never random characters")
				}
			}
			canvas := rasterWelcomeSpin(size[0], size[1], elapsed)
			for y := range canvas.height {
				for x := range canvas.width {
					depth := canvas.depth[y][x]
					require.False(t, math.IsNaN(depth) || math.IsInf(depth, 0))
					require.GreaterOrEqual(t, depth, 0.0)
					require.LessOrEqual(t, canvas.shade[y][x], uint8(welcomeSpinShades))
					if size[0] >= welcomeCanvasWidth && size[1] >= welcomeCanvasHeight &&
						(x == 0 || x == canvas.width-1 || y == 0 || y == canvas.height-1) {
						require.Zero(t, canvas.shade[y][x], "full-size mesh must not be clipped at frame %d", frame)
					}
				}
			}
		}
	}
	assert.Nil(t, m.renderWelcomeSpin(39, 0, 0))
	assert.Nil(t, m.renderWelcomeSpin(39, -1, 0))
	assert.Equal(t, []string{"", ""}, m.renderWelcomeSpin(0, 2, 0))
	assert.Equal(t, []string{"", ""}, m.renderWelcomeSpin(-1, 2, 0))
	assert.Len(t, m.renderWelcomeSpin(39, 9, time.Duration(math.MaxInt64)), 9)
	assert.Len(t, m.renderWelcomeSpin(39, 9, time.Duration(math.MinInt64)), 9)
	assert.Equal(t, rasterWelcomeSpin(39, 9, 0), rasterWelcomeSpin(10000, 10000, 0), "raster work is bounded independently of terminal size")
}

func TestWelcomeSpinThemesAndTransparentPadding(t *testing.T) {
	dark := model{theme: themes[DefaultThemeName]}
	light := model{theme: themes[LightThemeName]}
	darkLines, lightLines := dark.renderWelcomeSpin(80, 9, 0), light.renderWelcomeSpin(80, 9, 0)
	assert.NotEqual(t, darkLines, lightLines, "light and dark themes need different shading")
	for y, line := range darkLines {
		assert.Equal(t, xansi.Strip(line), xansi.Strip(lightLines[y]), "theme changes colors, not geometry")
		assert.True(t, strings.HasPrefix(line, strings.Repeat(" ", (80-welcomeCanvasWidth)/2)), "padding must have no background color")
		assert.True(t, strings.HasSuffix(line, strings.Repeat(" ", (80-welcomeCanvasWidth+1)/2)))
	}
}

func TestWelcomeSpinQuadrantEncoding(t *testing.T) {
	// Explicit expected symbols also guard the bit ordering of the lookup table.
	for mask, want := range []rune(" ▘▝▀▖▌▞▛▗▚▐▜▄▙▟█") {
		var pixels [4]int
		for i := range pixels {
			if mask&(1<<i) != 0 {
				pixels[i] = 12
			}
		}
		glyph, fg, bg := welcomeSpinCell(pixels)
		assert.Equal(t, want, glyph, "quadrant mask %04b", mask)
		assert.Zero(t, bg, "empty quadrants must retain the terminal background")
		if mask == 0 {
			assert.Zero(t, fg)
		} else {
			assert.Equal(t, 12, fg)
		}
		assert.Equal(t, 1, lipgloss.Width(string(glyph)))
	}
	for _, tt := range []struct {
		pixels [4]int
		glyph  rune
		fg, bg int
	}{
		{pixels: [4]int{16, 4, 16, 4}, glyph: '▌', fg: 16, bg: 4},
		{pixels: [4]int{4, 16, 4, 16}, glyph: '▐', fg: 16, bg: 4},
		{pixels: [4]int{16, 4, 4, 16}, glyph: '▚', fg: 16, bg: 4},
		{pixels: [4]int{4, 16, 16, 4}, glyph: '▞', fg: 16, bg: 4},
		{pixels: [4]int{8, 10, 12, 16}, glyph: '▗', fg: 16, bg: 10},
		{pixels: [4]int{0, 8, 0, 16}, glyph: '▐', fg: 12, bg: 0},
	} {
		glyph, fg, bg := welcomeSpinCell(tt.pixels)
		assert.Equal(t, tt.glyph, glyph)
		assert.Equal(t, tt.fg, fg)
		assert.Equal(t, tt.bg, bg)
	}
}

func TestWelcomeSpinQuadrantCoverage(t *testing.T) {
	var canvas welcomeSpinCanvas
	assert.Zero(t, canvas.pixel(0, 0))
	canvas.shade[0][0] = 8
	assert.Zero(t, canvas.pixel(0, 0), "a single sample must not thicken the silhouette")
	canvas.shade[1][0] = 12
	assert.Equal(t, 10, canvas.pixel(0, 0), "average lighting once at least half the quadrant is covered")
	canvas.shade[0][1] = 16
	canvas.shade[1][1] = 16
	assert.Equal(t, 13, canvas.pixel(0, 0))
	assert.Zero(t, canvas.pixel(1, 0), "neighboring quadrants sample independently")

	full := rasterWelcomeSpin(welcomeCanvasWidth, welcomeCanvasHeight, 0)
	assert.Equal(t, welcomeCanvasWidth*2, full.width/2, "two visible horizontal samples per cell")
	assert.Equal(t, welcomeCanvasHeight*2, full.height/2, "two visible vertical samples per cell")
}

func TestWelcomeSpinDepthBuffer(t *testing.T) {
	near := [3]welcomeSpinPoint{{-4, -4, 1.0 / 80}, {12, -4, 1.0 / 80}, {-4, 12, 1.0 / 80}}
	far := near
	for i := range far {
		far[i].z = 1.0 / 120
	}
	first, second := welcomeSpinCanvas{width: 8, height: 8}, welcomeSpinCanvas{width: 8, height: 8}
	first.triangle(near[0], near[1], near[2], 12)
	first.triangle(far[0], far[1], far[2], 3)
	second.triangle(far[0], far[1], far[2], 3)
	second.triangle(near[0], near[1], near[2], 12)
	assert.Equal(t, first, second, "closest faces win independently of draw order")
	assert.Equal(t, uint8(12), first.shade[0][0])
	assert.InDelta(t, 1.0/80, first.depth[0][0], 1e-12)
	before := first
	first.triangle(near[0], near[0], near[0], 1)
	assert.Equal(t, before, first, "degenerate triangles do not divide by zero or paint pixels")
}

func BenchmarkWelcomeSpin(b *testing.B) {
	m := model{theme: themes[DefaultThemeName]}
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		m.renderWelcomeSpin(80, 9, time.Duration(i)*time.Second/30)
	}
}
