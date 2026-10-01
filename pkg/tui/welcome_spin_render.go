package tui

import (
	"fmt"
	"strings"
)

const welcomeSpinShades = 16

type welcomeSpinPoint struct{ x, y, z float64 }

// One sample per Braille dot: two columns and four rows per terminal cell.
// Ink 1..16 is theme text, 17..32 the warm underscore or detached dust. Empty
// dots stay transparent; no terminal background color is assumed or emitted.
type welcomeSpinCanvas struct {
	width, height int
	depth         [welcomeParticleMaxHeight * 4][welcomeCanvasWidth * welcomeParticleMaxHeight / welcomeParticleCanvasHeight * 2]float32
	shade         [welcomeParticleMaxHeight * 4][welcomeCanvasWidth * welcomeParticleMaxHeight / welcomeParticleCanvasHeight * 2]uint8
}

func (c *welcomeSpinCanvas) plot(x, y int, depth float32, shade uint8, accent bool) {
	if x < 0 || x >= c.width || y < 0 || y >= c.height {
		return
	}
	if accent {
		shade += welcomeSpinShades
	}
	if depth > c.depth[y][x] || depth == c.depth[y][x] && shade > c.shade[y][x] {
		c.depth[y][x], c.shade[y][x] = depth, shade
	}
}

func (c *welcomeSpinCanvas) cell(x, y int) (rune, int) {
	bits := [4][2]rune{{1, 8}, {2, 16}, {4, 32}, {64, 128}}
	mask, body, accent, bodyCount, accentCount := rune(0), 0, 0, 0, 0
	for dy := range 4 {
		for dx := range 2 {
			ink := int(c.shade[y*4+dy][x*2+dx])
			if ink == 0 {
				continue
			}
			mask |= bits[dy][dx]
			if ink <= welcomeSpinShades {
				body, bodyCount = body+ink, bodyCount+1
			} else {
				accent, accentCount = accent+ink-welcomeSpinShades, accentCount+1
			}
		}
	}
	if mask == 0 {
		return ' ', 0
	}
	// Keep the body neutral when a stray warm dot overlaps it. Detached cells
	// and the underscore retain their accent without an orange halo on the K.
	if bodyCount > 0 {
		return 0x2800 + mask, (body + bodyCount/2) / bodyCount
	}
	return 0x2800 + mask, welcomeSpinShades + (accent+accentCount/2)/accentCount
}

func (m model) renderWelcomeSpinCanvas(width, height int, canvas welcomeSpinCanvas) []string {
	if height <= 0 {
		return nil
	}
	lines := make([]string, height)
	if width <= 0 {
		return lines
	}
	var foreground [welcomeSpinShades*2 + 1]string
	for group, base := range []string{m.theme.Assistant, m.welcomeDotColor()} {
		for i := 1; i <= welcomeSpinShades; i++ {
			level := float64(i-1) / float64(welcomeSpinShades-1)
			c := welcomeBlend(themeColor(m.theme.Muted), themeColor(base), 0.3+0.7*level)
			foreground[group*welcomeSpinShades+i] = fmt.Sprintf("\x1b[38;2;%d;%d;%dm", c.R, c.G, c.B)
		}
	}
	rows, columns := canvas.height/4, canvas.width/2
	for y := range height {
		row := y - (height-rows)/2
		if row < 0 || row >= rows {
			lines[y] = strings.Repeat(" ", width)
			continue
		}
		var line strings.Builder
		fg := 0
		for x := range min(columns, width) {
			glyph, nextFG := canvas.cell(x+max(0, (columns-width)/2), row)
			if nextFG == 0 {
				if fg != 0 {
					line.WriteString(ansiResetSequence)
					fg = 0
				}
				line.WriteByte(' ')
				continue
			}
			if fg != nextFG {
				line.WriteString(foreground[nextFG])
			}
			line.WriteRune(glyph)
			fg = nextFG
		}
		if fg != 0 {
			line.WriteString(ansiResetSequence)
		}
		lines[y] = centerVisible(line.String(), width)
	}
	return lines
}
