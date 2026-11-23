package termimage

import (
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComposeFrameKittyKeepsPlaceholders(t *testing.T) {
	content := strings.Join([]string{
		"header",
		"  " + PlaceholderLine(Kitty, 0x123456, 0, 3),
		"  " + PlaceholderLine(Kitty, 0x123456, 1, 3),
		"  " + PlaceholderLine(Kitty, 0x000007, 0, 2) + " tail",
	}, "\n")

	got, frame := ComposeFrame(Kitty, content, 10, nil)

	assert.Equal(t, content, got)
	assert.Equal(t, []uint32{0x123456, 7}, frame.Visible)
	assert.Empty(t, frame.Placements)
	assert.Zero(t, frame.Signature)
}

func TestComposeFrameWithoutImages(t *testing.T) {
	got, frame := ComposeFrame(ITerm2, "plain\ntext", 10, nil)
	assert.Equal(t, "plain\ntext", got)
	assert.Equal(t, Frame{}, frame)
}

func TestComposeFrameITerm2BlanksAndPlacesVisibleRows(t *testing.T) {
	cols := func(id uint32) int { return map[uint32]int{7: 4, 9: 3}[id] }
	lines := []string{
		// Image 7's row 0 has scrolled off; rows 1-3 are visible.
		"  " + PlaceholderLine(ITerm2, 7, 1, 4) + "  text",
		"  " + PlaceholderLine(ITerm2, 7, 2, 4),
		"  " + PlaceholderLine(ITerm2, 7, 3, 4),
		"divider",
		// An overlay covers image 9's middle row.
		" " + PlaceholderLine(ITerm2, 9, 0, 3),
		" " + xansi.Truncate(PlaceholderLine(ITerm2, 9, 1, 3), 1, "") + "[dialog]",
		" " + PlaceholderLine(ITerm2, 9, 2, 3),
		// A row on the last screen line is never drawn.
		" " + PlaceholderLine(ITerm2, 9, 3, 3),
	}

	got, frame := ComposeFrame(ITerm2, strings.Join(lines, "\n"), len(lines), cols)

	assert.NotContains(t, got, placeholderText)
	plain := strings.Split(xansi.Strip(got), "\n")
	assert.Equal(t, "        text", plain[0])
	assert.Equal(t, "  [dialog]", plain[5])
	assert.Equal(t, []uint32{7, 9}, frame.Visible)
	assert.Equal(t, []Placement{
		{ID: 7, X: 2, Y: 0, FirstRow: 1, Rows: 3, Cols: 4},
		{ID: 9, X: 1, Y: 4, FirstRow: 0, Rows: 1, Cols: 3},
		{ID: 9, X: 1, Y: 6, FirstRow: 2, Rows: 1, Cols: 3},
	}, frame.Placements)
	assert.NotZero(t, frame.Signature)
}

func TestComposeFrameITerm2PlacesRepeatedImages(t *testing.T) {
	lines := []string{
		// The first copy is scrolled so only its last row shows.
		" " + PlaceholderLine(ITerm2, 4, 1, 3),
		"between",
		"  " + PlaceholderLine(ITerm2, 4, 0, 3),
		"  " + PlaceholderLine(ITerm2, 4, 1, 3),
		"and " + PlaceholderLine(ITerm2, 4, 0, 3),
		"composer",
	}

	_, frame := ComposeFrame(ITerm2, strings.Join(lines, "\n"), len(lines), func(uint32) int { return 3 })

	assert.Equal(t, []Placement{
		{ID: 4, X: 1, Y: 0, FirstRow: 1, Rows: 1, Cols: 3},
		{ID: 4, X: 2, Y: 2, FirstRow: 0, Rows: 2, Cols: 3},
		{ID: 4, X: 4, Y: 4, FirstRow: 0, Rows: 1, Cols: 3},
	}, frame.Placements)
}

func TestComposeFrameITerm2SkipsHorizontallyClippedRows(t *testing.T) {
	line := xansi.Truncate(PlaceholderLine(ITerm2, 3, 0, 6), 4, "")
	_, frame := ComposeFrame(ITerm2, line+"\n", 5, func(uint32) int { return 6 })
	assert.Equal(t, []uint32{3}, frame.Visible)
	assert.Empty(t, frame.Placements)
	assert.Zero(t, frame.Signature)
}

func TestComposeFrameSignatureTracksImageRows(t *testing.T) {
	cols := func(uint32) int { return 2 }
	signature := func(lines ...string) uint64 {
		_, frame := ComposeFrame(ITerm2, strings.Join(append(lines, "composer"), "\n"), 10, cols)
		require.NotEmpty(t, frame.Placements)
		return frame.Signature
	}
	image := PlaceholderLine(ITerm2, 1, 0, 2)
	base := signature("title", image)

	assert.Equal(t, base, signature("other title", image), "rows without images do not matter")
	assert.NotEqual(t, base, signature("title", image+" changed"), "text beside the image")
	assert.NotEqual(t, base, signature("", "title", image), "the image moved")
	assert.NotEqual(t, base, signature("title", " "+image), "the image moved sideways")
}

func TestPlaceholderLineEncodesRowsDistinctly(t *testing.T) {
	assert.NotEqual(t, PlaceholderLine(ITerm2, 1, 0, 3), PlaceholderLine(ITerm2, 1, 1, 3))
	assert.Equal(t, 3, xansi.StringWidth(PlaceholderLine(Kitty, 1, 0, 3)))
	assert.Empty(t, PlaceholderLine(Kitty, 1, MaxCells, 3))

	cells, _ := scanLine(Kitty, PlaceholderLine(Kitty, 0xabcdef, 5, 2), 4, false)
	assert.Equal(t, []placeholderCell{
		{x: 0, y: 4, id: 0xabcdef, row: 5, col: 0},
		{x: 1, y: 4, id: 0xabcdef, row: 5, col: 1},
	}, cells)
}

func TestIDAllocator(t *testing.T) {
	ids := NewIDAllocator(ITerm2)
	assert.Equal(t, uint32(1), ids.Next())
	ids.next = 1<<16 - 1
	assert.Equal(t, uint32(1<<16-1), ids.Next())
	assert.Equal(t, uint32(1), ids.Next(), "ids wrap without using zero")

	kittyID := NewIDAllocator(Kitty).Next()
	assert.Positive(t, kittyID)
	assert.Less(t, kittyID, uint32(1<<24))
}
