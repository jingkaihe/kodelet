package tui

import (
	"fmt"
	"image/color"
	"math"
	"strings"
	"time"
)

const (
	welcomeSpinPeriod = 4 * time.Second
	welcomeSpinShades = 16
)

type welcomeSpinPoint struct{ x, y, z float64 }

type welcomeSpinFace struct {
	vertices [4]welcomeSpinPoint
	normal   welcomeSpinPoint
}

// Stroke geometry based on assets/logo.svg, with optical corrections for 3D:
// a shorter lower arm and a lighter, separated underscore. Literal SVG spacing
// makes the arm and underscore merge into one long foot at terminal resolution.
// The small overlaps at the K's joints make a solid union through the z-buffer.
var welcomeSpinFaces = buildWelcomeSpinFaces()

func buildWelcomeSpinFaces() [24]welcomeSpinFace {
	strokes := [...]struct {
		x1, y1, x2, y2 float64
		width, depth   float64
	}{
		{16, 15, 16, 49, 6, 8},
		{16, 37, 34, 20, 6, 8},
		{22, 31, 33, 46, 6, 8},
		{45, 49, 57, 49, 4, 4},
	}
	quads := [...][4]int{
		{4, 5, 6, 7},
		{0, 3, 2, 1},
		{0, 1, 5, 4},
		{1, 2, 6, 5},
		{2, 3, 7, 6},
		{3, 0, 4, 7},
	}
	var faces [24]welcomeSpinFace
	for strokeIndex, stroke := range strokes {
		dx, dy := stroke.x2-stroke.x1, stroke.y2-stroke.y1
		length := math.Hypot(dx, dy)
		px, py := -dy*stroke.width/(2*length), dx*stroke.width/(2*length)
		corners := [4]welcomeSpinPoint{
			{stroke.x1 + px - 33, stroke.y1 + py - 33.5, 0},
			{stroke.x1 - px - 33, stroke.y1 - py - 33.5, 0},
			{stroke.x2 - px - 33, stroke.y2 - py - 33.5, 0},
			{stroke.x2 + px - 33, stroke.y2 + py - 33.5, 0},
		}
		var vertices [8]welcomeSpinPoint
		for i, p := range corners {
			vertices[i] = welcomeSpinPoint{p.x, p.y, -stroke.depth / 2}
			vertices[i+4] = welcomeSpinPoint{p.x, p.y, stroke.depth / 2}
		}
		for faceIndex, quad := range quads {
			face := &faces[strokeIndex*6+faceIndex]
			for i, vertex := range quad {
				face.vertices[i] = vertices[vertex]
			}
			a, b, c := face.vertices[0], face.vertices[1], face.vertices[2]
			u := welcomeSpinPoint{b.x - a.x, b.y - a.y, b.z - a.z}
			v := welcomeSpinPoint{c.x - a.x, c.y - a.y, c.z - a.z}
			n := welcomeSpinPoint{u.y*v.z - u.z*v.y, u.z*v.x - u.x*v.z, u.x*v.y - u.y*v.x}
			length := math.Sqrt(n.x*n.x + n.y*n.y + n.z*n.z)
			face.normal = welcomeSpinPoint{n.x / length, n.y / length, n.z / length}
		}
	}
	return faces
}

// Each cell holds 2×2 quadrants, with two samples per axis per quadrant.
// Raster work is bounded even when the caller offers a very large viewport.
type welcomeSpinCanvas struct {
	width, height int
	depth         [welcomeCanvasHeight * 4][welcomeCanvasWidth * 4]float64
	shade         [welcomeCanvasHeight * 4][welcomeCanvasWidth * 4]uint8
}

func rasterWelcomeSpin(width, height int, elapsed time.Duration) welcomeSpinCanvas {
	canvas := welcomeSpinCanvas{
		width:  max(0, min(width, welcomeCanvasWidth)) * 4,
		height: max(0, min(height, welcomeCanvasHeight)) * 4,
	}
	if canvas.width == 0 || canvas.height == 0 {
		return canvas
	}
	// A modest fixed tilt shows the extrusion without rocking or pulsating.
	yaw := float64(elapsed%welcomeSpinPeriod)/float64(welcomeSpinPeriod)*2*math.Pi + math.Pi/12
	sy, cy := math.Sincos(yaw)
	sp, cp := math.Sincos(-math.Pi / 18)
	rotate := func(p welcomeSpinPoint) welcomeSpinPoint {
		x, z := cy*p.x+sy*p.z, -sy*p.x+cy*p.z
		return welcomeSpinPoint{x, cp*p.y - sp*z, sp*p.y + cp*z}
	}
	const camera = 110.0 // All rotated vertices remain well in front of the near plane.
	// Fixed framing accommodates the low underscore as it swings toward the
	// camera. Re-fitting each angle would make the logo appear to pulse in size.
	// Quadrants are half as wide as they are tall. Double the horizontal
	// sample density without stretching the mesh or enlarging its footprint.
	scale := min(float64(canvas.width)/104, float64(canvas.height)/52)
	for _, face := range welcomeSpinFaces {
		n, p := rotate(face.normal), rotate(face.vertices[0])
		if n.z*camera-n.x*p.x-n.y*p.y-n.z*p.z <= 0 {
			continue
		}
		// Camera-space upper-left lighting with ambient light on the side faces.
		light := max(0, -0.35*n.x-0.5*n.y+0.79*n.z)
		shade := uint8(1 + math.Round(float64(welcomeSpinShades-1)*min(1, 0.2+0.8*light)))
		var projected [4]welcomeSpinPoint
		for i, vertex := range face.vertices {
			p := rotate(vertex)
			depth := 1 / (camera - p.z)
			projected[i] = welcomeSpinPoint{
				x: float64(canvas.width)/2 + 2*scale*p.x*camera*depth,
				y: float64(canvas.height)/2 + scale*(p.y*camera*depth-5),
				z: depth,
			}
		}
		canvas.triangle(projected[0], projected[1], projected[2], shade)
		canvas.triangle(projected[0], projected[2], projected[3], shade)
	}
	return canvas
}

func (c *welcomeSpinCanvas) triangle(a, b, d welcomeSpinPoint, shade uint8) {
	edge := func(a, b welcomeSpinPoint, x, y float64) float64 {
		return (b.x-a.x)*(y-a.y) - (b.y-a.y)*(x-a.x)
	}
	area := edge(a, b, d.x, d.y)
	if math.Abs(area) < 1e-9 {
		return
	}
	left, right := max(0, int(math.Floor(min(a.x, b.x, d.x)))), min(c.width-1, int(math.Ceil(max(a.x, b.x, d.x))))
	top, bottom := max(0, int(math.Floor(min(a.y, b.y, d.y)))), min(c.height-1, int(math.Ceil(max(a.y, b.y, d.y))))
	for y := top; y <= bottom; y++ {
		for x := left; x <= right; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			wa, wb := edge(b, d, px, py)/area, edge(d, a, px, py)/area
			wd := 1 - wa - wb
			if wa < -1e-9 || wb < -1e-9 || wd < -1e-9 {
				continue
			}
			// Reciprocal camera distance interpolates correctly in screen space.
			depth := wa*a.z + wb*b.z + wd*d.z
			if depth > c.depth[y][x] {
				c.depth[y][x], c.shade[y][x] = depth, shade
			}
		}
	}
}

func (c *welcomeSpinCanvas) pixel(x, y int) int {
	total, covered := 0, 0
	for dy := range 2 {
		for dx := range 2 {
			if shade := c.shade[y*2+dy][x*2+dx]; shade > 0 {
				total += int(shade)
				covered++
			}
		}
	}
	// A lone subpixel should not inflate the silhouette by a whole quadrant.
	if covered < 2 {
		return 0
	}
	return (total + covered/2) / covered
}

// Bit order: top-left, top-right, bottom-left, bottom-right.
var welcomeSpinQuadrants = [...]rune{' ', '▘', '▝', '▀', '▖', '▌', '▞', '▛', '▗', '▚', '▐', '▜', '▄', '▙', '▟', '█'}

func welcomeSpinCell(pixels [4]int) (glyph rune, foreground, background int) {
	low, high, mask, total, covered := welcomeSpinShades, 0, 0, 0, 0
	for i, shade := range pixels {
		low, high = min(low, shade), max(high, shade)
		if shade > 0 {
			mask |= 1 << i
			total += shade
			covered++
		}
	}
	if covered == 0 {
		return ' ', 0, 0
	}
	if covered < 4 || low == high {
		// On the outline, keep empty quadrants transparent instead of guessing
		// the user's terminal background. The foreground follows face lighting.
		return welcomeSpinQuadrants[mask], (total + covered/2) / covered, 0
	}
	// Solid cells may contain four shades, but a terminal cell only has two
	// colors. Split light and dark quadrants to preserve the extrusion's faces.
	threshold := (low + high) / 2
	mask, total, covered = 0, 0, 0
	for i, shade := range pixels {
		if shade > threshold {
			mask |= 1 << i
			total += shade
			covered++
		} else {
			background += shade
		}
	}
	return welcomeSpinQuadrants[mask], (total + covered/2) / covered, (background + (4-covered)/2) / (4 - covered)
}

// renderWelcomeSpin is pure: it owns no timers, input handling, or model state.
// Extra rows surround the bounded canvas; compact canvases retain the same mesh.
func (m model) renderWelcomeSpin(width, height int, elapsed time.Duration) []string {
	if height <= 0 {
		return nil
	}
	lines := make([]string, height)
	if width <= 0 {
		return lines
	}
	canvas := rasterWelcomeSpin(width, height, elapsed)
	var foreground, background [welcomeSpinShades + 1]string
	base := themeColor(m.welcomeDotColor())
	for i := 1; i <= welcomeSpinShades; i++ {
		level := float64(i-1) / float64(welcomeSpinShades-1)
		c := welcomeBlend(color.Black, base, 0.38+0.62*level)
		if m.theme.Dark {
			c = welcomeBlend(color.Black, base, 0.5+0.5*level)
			if level > 0.75 {
				c = welcomeBlend(c, themeColor("#ffe2b3"), (level-0.75)*0.8)
			}
		}
		foreground[i] = fmt.Sprintf("\x1b[38;2;%d;%d;%dm", c.R, c.G, c.B)
		background[i] = fmt.Sprintf("\x1b[48;2;%d;%d;%dm", c.R, c.G, c.B)
	}
	background[0] = "\x1b[49m"
	rows, columns := canvas.height/4, canvas.width/4
	for y := range height {
		row := y - (height-rows)/2
		if row < 0 || row >= rows {
			lines[y] = strings.Repeat(" ", width)
			continue
		}
		var line strings.Builder
		fg, bg := 0, 0
		for x := range columns {
			glyph, nextFG, nextBG := welcomeSpinCell([4]int{
				canvas.pixel(x*2, row*2), canvas.pixel(x*2+1, row*2),
				canvas.pixel(x*2, row*2+1), canvas.pixel(x*2+1, row*2+1),
			})
			if nextFG == 0 {
				if fg != 0 {
					line.WriteString(ansiResetSequence)
					fg, bg = 0, 0
				}
				line.WriteByte(' ')
				continue
			}
			if fg != nextFG {
				line.WriteString(foreground[nextFG])
			}
			if bg != nextBG {
				line.WriteString(background[nextBG])
			}
			line.WriteRune(glyph)
			fg, bg = nextFG, nextBG
		}
		if fg != 0 {
			line.WriteString(ansiResetSequence)
		}
		lines[y] = centerVisible(line.String(), width)
	}
	return lines
}
