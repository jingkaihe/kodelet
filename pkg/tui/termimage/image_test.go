package termimage

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFit(t *testing.T) {
	cell := CellSize{Width: 10, Height: 20}
	for _, test := range []struct {
		name               string
		width, height      int
		maxCols, maxRows   int
		wantCols, wantRows int
		wantZero           bool
	}{
		{name: "limited by width", width: 1600, height: 900, maxCols: 60, maxRows: 40, wantCols: 60, wantRows: 17},
		{name: "limited by height", width: 900, height: 1600, maxCols: 60, maxRows: 20, wantCols: 23, wantRows: 20},
		{name: "small image keeps natural size", width: 40, height: 40, maxCols: 60, maxRows: 30, wantCols: 4, wantRows: 2},
		{name: "tiny image takes one cell", width: 2, height: 2, maxCols: 60, maxRows: 30, wantCols: 1, wantRows: 1},
		{name: "unknown size", width: 0, height: 10, maxCols: 60, maxRows: 30, wantZero: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			cols, rows := Fit(test.width, test.height, test.maxCols, test.maxRows, cell)
			if test.wantZero {
				assert.Zero(t, cols)
				assert.Zero(t, rows)
				return
			}
			assert.Equal(t, test.wantCols, cols)
			assert.Equal(t, test.wantRows, rows)
		})
	}

	cols, rows := Fit(1000, 1000, 1000, 1000, CellSize{})
	assert.Equal(t, 100, cols, "an implausible cell size uses the default")
	assert.Equal(t, 50, rows)
	cols, rows = Fit(100_000, 100, 1000, 1000, cell)
	assert.Equal(t, MaxCells, cols, "placeholders cannot address more columns")
	assert.Equal(t, 1, rows)
}

func TestRenderAndSlice(t *testing.T) {
	data := testPNG(t, 300, 160)
	width, height, err := Size(data)
	require.NoError(t, err)
	assert.Equal(t, []int{300, 160}, []int{width, height})

	picture, err := Decode(data)
	require.NoError(t, err)
	rendition, err := Render(picture, 10, 4, CellSize{Width: 15, Height: 30})
	require.NoError(t, err)
	assert.Equal(t, []int{150, 120}, pngSize(t, rendition.PNG), "one pixel per reported pixel")

	whole, err := rendition.Slice(0, 4)
	require.NoError(t, err)
	assert.Equal(t, rendition.PNG, whole)
	slice, err := rendition.Slice(1, 2)
	require.NoError(t, err)
	assert.Equal(t, []int{150, 60}, pngSize(t, slice))

	_, err = rendition.Slice(3, 2)
	assert.ErrorContains(t, err, "invalid image rows 3+2 of 4")
}

func TestRenderUsesDensityForSmallCells(t *testing.T) {
	picture, err := Decode(testPNG(t, 400, 400))
	require.NoError(t, err)

	rendition, err := Render(picture, 10, 5, CellSize{Width: 8, Height: 16})
	require.NoError(t, err)
	assert.Equal(t, []int{160, 160}, pngSize(t, rendition.PNG), "logical pixels are doubled")

	rendition, err = Render(picture, 40, 20, CellSize{Width: 8, Height: 16})
	require.NoError(t, err)
	assert.Equal(t, []int{400, 400}, pngSize(t, rendition.PNG), "never more pixels than the source")
}

func TestSizeRejectsInvalidImages(t *testing.T) {
	_, _, err := Size([]byte("not an image"))
	assert.ErrorContains(t, err, "unsupported image format")

	_, _, err = Size(make([]byte, MaxSourceBytes+1))
	assert.ErrorContains(t, err, "too large")

	var huge bytes.Buffer
	require.NoError(t, png.Encode(&huge, image.NewGray(image.Rect(0, 0, 1, 1))))
	data := huge.Bytes()
	// Rewrite the IHDR dimensions, and its checksum, to claim a decompression bomb.
	copy(data[16:24], []byte{0, 0, 0x4e, 0x20, 0, 0, 0x4e, 0x20})
	binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
	_, _, err = Size(data)
	assert.ErrorContains(t, err, "20000x20000 are too large")
}

func testPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.Set(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 0x80, A: 0xff})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func pngSize(t *testing.T, data []byte) []int {
	t.Helper()
	config, err := png.DecodeConfig(bytes.NewReader(data))
	require.NoError(t, err)
	return []int{config.Width, config.Height}
}
