package tui

import "math/rand/v2"

var welcomeMatrixSymbols = []rune(`2598Z*):."=+-¦|_ｦｱｳｴｵｶｷｹｺｻｼｽｾｿﾀﾂﾃﾅﾆﾇﾈﾊﾋﾎﾏﾐﾑﾒﾓﾔﾕﾗﾘﾜ`)

const (
	welcomeMatrixRainEnd     = 90
	welcomeMatrixSettleFrame = welcomeFrames
	welcomeMatrixFrames      = welcomeMatrixSettleFrame + 30
)

func (m model) welcomeMatrixLogoPalette(frame int) *[welcomePaletteSize]string {
	return cachedWelcomePalette(welcomePaletteKey{
		matrix:       true,
		dark:         m.theme.Dark,
		accent:       m.theme.Assistant,
		matrixSettle: min(welcomeMatrixFrames-welcomeMatrixSettleFrame, max(0, frame-welcomeMatrixSettleFrame)),
	})
}

type welcomeRainColumn struct {
	start, delay, length int
	fillStart, fillDelay int
	resolve              [welcomeCanvasHeight]int
	symbols              [welcomeCanvasHeight]int
	colors               [welcomeCanvasHeight]int
}

var welcomeMatrixColumns = buildWelcomeMatrixColumns()

func buildWelcomeMatrixColumns() [welcomeCanvasWidth]welcomeRainColumn {
	rng := rand.New(rand.NewPCG(0x6d6174726978, 0x7261696e))
	var columns [welcomeCanvasWidth]welcomeRainColumn
	order := rng.Perm(welcomeCanvasWidth)
	start, remaining, filled := 0, 0, 0
	for _, x := range order {
		if remaining == 0 {
			start += 3 + rng.IntN(7)
			remaining = 1 + rng.IntN(3)
		}
		remaining--
		col := &columns[x]
		col.start, col.delay, col.length = start, 2+rng.IntN(7), 2+rng.IntN(welcomeCanvasHeight-1)
		col.fillStart = welcomeMatrixRainEnd
		if start < welcomeMatrixRainEnd {
			col.fillStart += 8 + rng.IntN(18)
		}
		col.fillDelay = 1 + rng.IntN(3)
		filled = max(filled, col.fillStart+(welcomeCanvasHeight-1)*col.fillDelay)
		for y := range welcomeCanvasHeight {
			col.symbols[y] = rng.IntN(len(welcomeMatrixSymbols))
			col.colors[y] = 2 + rng.IntN(9)
		}
	}
	for x := range columns {
		for order, y := range rng.Perm(welcomeCanvasHeight) {
			columns[x].resolve[y] = filled + 8 + rng.IntN(25) + order*3
		}
	}
	return columns
}

func welcomeMatrixCell(x, y, frame int, symbol rune) (rune, int) {
	col := &welcomeMatrixColumns[x]
	if frame >= col.resolve[y] {
		step := min(8, (frame-col.resolve[y])/3)
		// Resolve into one steady green before the whole wordmark fades together.
		return symbol, 20 - 10*step/8
	}
	if frame < col.start && frame < col.fillStart {
		return ' ', 0
	}
	age := max(0, frame-col.start)
	cycleRows := welcomeCanvasHeight + col.length + 3
	head := (age / col.delay) % cycleRows
	length := col.length
	if frame >= col.fillStart {
		head = min(welcomeCanvasHeight-1, (frame-col.fillStart)/col.fillDelay)
		length = welcomeCanvasHeight
	} else if frame >= welcomeMatrixRainEnd {
		if col.start >= welcomeMatrixRainEnd {
			return ' ', 0
		}
		head = ((welcomeMatrixRainEnd-col.start)/col.delay)%cycleRows + (frame-welcomeMatrixRainEnd)/max(1, col.delay/2)
	}
	distance := head - y
	if distance < 0 || distance >= length {
		return ' ', 0
	}
	swap := (frame + col.symbols[y]*3) / (24 + col.symbols[y])
	glyph := welcomeMatrixSymbols[(col.symbols[y]+swap*7)%len(welcomeMatrixSymbols)]
	if distance == 0 && (frame < col.fillStart || frame-col.fillStart < welcomeCanvasHeight*col.fillDelay) {
		return glyph, 20
	}
	shade := col.colors[y]
	if distance == length-1 && frame < col.fillStart {
		shade = min(shade, 2)
	}
	return glyph, shade
}
