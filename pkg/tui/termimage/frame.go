package termimage

import (
	"hash/fnv"
	"slices"
	"strconv"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// Placement is a run of an image's rows that is fully visible on screen.
type Placement struct {
	ID       uint32
	X        int
	Y        int
	FirstRow int
	Rows     int
	Cols     int
}

// Frame describes the images in a rendered frame.
type Frame struct {
	// Visible lists the ids of images with any visible cell.
	Visible []uint32
	// Placements are the fully visible row runs, which iTerm2 draws.
	Placements []Placement
	// Signature changes whenever the rows under Placements change.
	Signature uint64
}

type placeholderCell struct {
	x, y     int
	id       uint32
	row, col int
}

// ComposeFrame finds the placeholder cells in rendered content. For iTerm2 it
// also returns the content with placeholders blanked. imageCols reports an
// image's width, so horizontally clipped rows are not drawn.
func ComposeFrame(protocol Protocol, content string, height int, imageCols func(uint32) int) (string, Frame) {
	if protocol == None || !strings.Contains(content, placeholderText) {
		return content, Frame{}
	}

	lines := strings.Split(content, "\n")
	original := slices.Clone(lines)
	var cells []placeholderCell
	for y, line := range lines {
		if !strings.Contains(line, placeholderText) {
			continue
		}
		var found []placeholderCell
		found, lines[y] = scanLine(protocol, line, y, protocol == ITerm2)
		cells = append(cells, found...)
	}

	var frame Frame
	for _, cell := range cells {
		if !slices.Contains(frame.Visible, cell.id) {
			frame.Visible = append(frame.Visible, cell.id)
		}
	}
	if protocol != ITerm2 {
		return content, frame
	}
	content = strings.Join(lines, "\n")

	// A span is one image row as it appears on screen. The same image may
	// appear more than once, so a span starts at every column 0, and it is
	// complete when the following columns continue it on the same line.
	type rowKey struct {
		id  uint32
		row int
	}
	type rowSpan struct {
		id         uint32
		row        int
		x, y, cols int
		complete   bool
	}
	var spans []*rowSpan
	open := map[rowKey]*rowSpan{}
	for _, cell := range cells {
		key := rowKey{cell.id, cell.row}
		if span := open[key]; span != nil && cell.col != 0 {
			if cell.y == span.y && cell.col == span.cols && cell.x == span.x+span.cols {
				span.cols++
			} else {
				span.complete = false
			}
			continue
		}
		span := &rowSpan{id: cell.id, row: cell.row, x: cell.x, y: cell.y, cols: 1, complete: cell.col == 0}
		open[key] = span
		spans = append(spans, span)
	}

	// Join consecutive rows of an image at the same column into placements.
	type nextRow struct {
		id   uint32
		x, y int
		row  int
	}
	runs := map[nextRow]int{}
	hash := fnv.New64a()
	for _, span := range spans {
		cols := imageCols(span.id)
		// The cursor may advance below an image, which would scroll the screen
		// if the image touched its last row.
		if !span.complete || span.cols != cols || span.y >= height-1 {
			continue
		}
		key := nextRow{id: span.id, x: span.x, y: span.y, row: span.row}
		index, ok := runs[key]
		if ok {
			delete(runs, key)
			frame.Placements[index].Rows++
		} else {
			frame.Placements = append(frame.Placements, Placement{
				ID: span.id, X: span.x, Y: span.y, FirstRow: span.row, Rows: 1, Cols: cols,
			})
			index = len(frame.Placements) - 1
		}
		runs[nextRow{id: span.id, x: span.x, y: span.y + 1, row: span.row + 1}] = index
		_, _ = hash.Write([]byte(original[span.y]))
	}
	for _, placement := range frame.Placements {
		for _, value := range []int{int(placement.ID), placement.X, placement.Y, placement.FirstRow, placement.Rows, placement.Cols} {
			_, _ = hash.Write(strconv.AppendInt(nil, int64(value), 10))
			_, _ = hash.Write([]byte{0})
		}
	}
	if len(frame.Placements) > 0 {
		frame.Signature = hash.Sum64()
	}
	return content, frame
}

// scanLine returns the placeholder cells in one rendered line and, when blank
// is set, the line with each placeholder replaced by a space.
func scanLine(protocol Protocol, line string, y int, blank bool) ([]placeholderCell, string) {
	parser := ansi.GetParser()
	defer ansi.PutParser(parser)

	var (
		cells []placeholderCell
		out   strings.Builder
		style uv.Style
		state byte
		x     int
	)
	if blank {
		out.Grow(len(line))
	}
	for rest := line; len(rest) > 0; {
		seq, width, n, next := ansi.DecodeSequence(rest, state, parser)
		replaced := false
		switch {
		case width > 0:
			if row, col, ok := decodePlaceholder(seq); ok {
				if id, ok := placeholderID(protocol, style.Fg); ok {
					cells = append(cells, placeholderCell{x: x, y: y, id: id, row: row, col: col})
				}
				replaced = blank
			}
			x += width
		case ansi.HasCsiPrefix(seq) && parser.Command() == 'm':
			uv.ReadStyle(parser.Params(), &style)
		}
		if blank {
			if replaced {
				out.WriteByte(' ')
			} else {
				out.WriteString(seq)
			}
		}
		state, rest = next, rest[n:]
	}
	if !blank {
		return cells, line
	}
	return cells, out.String()
}
