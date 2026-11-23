package termimage

import (
	"fmt"
	"image/color"
	"math/rand/v2"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi/kitty"
)

// MaxCells bounds an image's width and height in cells, limited by Kitty's
// table of row and column diacritics.
var MaxCells = len(diacriticIndexes)

var placeholderText = string(kitty.Placeholder)

var diacriticIndexes = func() map[rune]int {
	first := kitty.Diacritic(0)
	indexes := map[rune]int{first: 0}
	// kitty.Diacritic returns the first entry for an out-of-range index.
	for i := 1; ; i++ {
		diacritic := kitty.Diacritic(i)
		if diacritic == first {
			return indexes
		}
		indexes[diacritic] = i
	}
}()

// PlaceholderLine returns one row of placeholder cells for an image. The
// foreground color identifies the image; for iTerm2 it also encodes the row, so
// the renderer rewrites, and thereby erases, image rows that move.
func PlaceholderLine(protocol Protocol, id uint32, row, cols int) string {
	if row < 0 || row >= MaxCells || cols <= 0 {
		return ""
	}
	cols = min(cols, MaxCells)
	rowMark := string(kitty.Diacritic(row))
	var b strings.Builder
	b.Grow(cols*8 + 24)
	fg := placeholderColor(protocol, id, row)
	fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm", fg.R, fg.G, fg.B)
	for col := range cols {
		b.WriteString(placeholderText)
		b.WriteString(rowMark)
		b.WriteRune(kitty.Diacritic(col))
	}
	b.WriteString("\x1b[39m")
	return b.String()
}

func placeholderColor(protocol Protocol, id uint32, row int) color.RGBA {
	if protocol == ITerm2 {
		return color.RGBA{R: uint8(id >> 8), G: uint8(id), B: uint8(row), A: 0xff}
	}
	return color.RGBA{R: uint8(id >> 16), G: uint8(id >> 8), B: uint8(id), A: 0xff}
}

func placeholderID(protocol Protocol, fg color.Color) (uint32, bool) {
	if fg == nil {
		return 0, false
	}
	r, g, b, _ := fg.RGBA()
	r, g, b = r>>8, g>>8, b>>8
	var id uint32
	if protocol == ITerm2 {
		id = r<<8 | g
	} else {
		id = r<<16 | g<<8 | b
	}
	return id, id != 0
}

// decodePlaceholder returns the row and column of a placeholder grapheme.
func decodePlaceholder(grapheme string) (row, col int, ok bool) {
	rest, found := strings.CutPrefix(grapheme, placeholderText)
	if !found {
		return 0, 0, false
	}
	rowMark, size := utf8.DecodeRuneInString(rest)
	row, rowOK := diacriticIndexes[rowMark]
	colMark, _ := utf8.DecodeRuneInString(rest[size:])
	col, colOK := diacriticIndexes[colMark]
	return row, col, rowOK && colOK
}

// IDAllocator hands out image ids that fit a protocol's placeholder encoding.
type IDAllocator struct {
	limit uint32
	next  uint32
}

// NewIDAllocator returns an allocator for protocol. Kitty ids start at random
// because other programs share the terminal's id space.
func NewIDAllocator(protocol Protocol) *IDAllocator {
	if protocol == ITerm2 {
		return &IDAllocator{limit: 1 << 16, next: 1}
	}
	const limit = 1 << 24
	return &IDAllocator{limit: limit, next: 1 + rand.Uint32N(limit-1)}
}

// Next returns a nonzero id, wrapping within the protocol's range.
func (a *IDAllocator) Next() uint32 {
	id := a.next
	a.next++
	if a.next >= a.limit {
		a.next = 1
	}
	return id
}
