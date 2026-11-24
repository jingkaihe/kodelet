package termimage

import (
	"bytes"
	"image"
	_ "image/gif" // Register GIF decoding for previews.
	"image/jpeg"
	"image/png"
	"math"

	"github.com/pkg/errors"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // Register WebP decoding for previews.
)

const (
	// MaxSourceBytes bounds the encoded image a preview will decode.
	MaxSourceBytes  = 32 << 20
	maxSourcePixels = 40_000_000
	// maxITerm2Payload is the PNG slice size above which JPEG is tried.
	maxITerm2Payload = 700 << 10
)

// CellSize is the size of one terminal cell in pixels.
type CellSize struct {
	Width  int
	Height int
}

// DefaultCellSize is used when the terminal does not report its pixel size.
var DefaultCellSize = CellSize{Width: 10, Height: 20}

func (c CellSize) valid() bool {
	return c.Width >= 2 && c.Width <= 256 && c.Height >= 4 && c.Height <= 512
}

// orDefault returns c, or DefaultCellSize when c is implausible.
func (c CellSize) orDefault() CellSize {
	if c.valid() {
		return c
	}
	return DefaultCellSize
}

// Picture is a decoded source image.
type Picture struct {
	img    image.Image
	Width  int
	Height int
}

// Size validates encoded image data and returns its pixel size without decoding it.
func Size(data []byte) (width, height int, err error) {
	if len(data) > MaxSourceBytes {
		return 0, 0, errors.New("image is too large to preview")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0, errors.Wrap(err, "unsupported image format")
	}
	width, height = config.Width, config.Height
	if width <= 0 || height <= 0 || width > maxSourcePixels/height {
		return 0, 0, errors.Errorf("image dimensions %dx%d are too large to preview", width, height)
	}
	return width, height, nil
}

// Decode decodes PNG, JPEG, GIF, or WebP data. GIFs show their first frame.
func Decode(data []byte) (*Picture, error) {
	if _, _, err := Size(data); err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode image")
	}
	bounds := img.Bounds()
	return &Picture{img: img, Width: bounds.Dx(), Height: bounds.Dy()}, nil
}

// Fit returns the cell box for an image within maxCols x maxRows, preserving
// its aspect ratio and never enlarging it.
func Fit(width, height, maxCols, maxRows int, cell CellSize) (cols, rows int) {
	if width <= 0 || height <= 0 || maxCols <= 0 || maxRows <= 0 {
		return 0, 0
	}
	cell = cell.orDefault()
	maxCols, maxRows = min(maxCols, MaxCells), min(maxRows, MaxCells)
	scale := math.Min(1, math.Min(
		float64(maxCols*cell.Width)/float64(width),
		float64(maxRows*cell.Height)/float64(height),
	))
	cols = clamp(int(math.Round(float64(width)*scale/float64(cell.Width))), 1, maxCols)
	rows = clamp(int(math.Round(float64(height)*scale/float64(cell.Height))), 1, maxRows)
	return cols, rows
}

func clamp(value, low, high int) int {
	return max(low, min(high, value))
}

// Rendition is a picture scaled to exactly fill a box of cells, kept as PNG.
type Rendition struct {
	Cols int
	Rows int
	PNG  []byte

	rowPx int
}

// Render scales p to fill cols x rows cells, at up to twice the reported cell
// resolution for high-density displays.
func Render(p *Picture, cols, rows int, cell CellSize) (*Rendition, error) {
	if p == nil || cols <= 0 || rows <= 0 {
		return nil, errors.New("invalid image rendition size")
	}
	cell = cell.orDefault()
	density := 1.0
	if cell.Width < 12 {
		density = 2
	}
	sourceScale := math.Min(
		float64(p.Width)/float64(cols*cell.Width),
		float64(p.Height)/float64(rows*cell.Height),
	)
	density = math.Max(1, math.Min(density, sourceScale))
	colPx := max(1, int(math.Round(float64(cell.Width)*density)))
	rowPx := max(1, int(math.Round(float64(cell.Height)*density)))

	dst := image.NewNRGBA(image.Rect(0, 0, cols*colPx, rows*rowPx))
	draw.CatmullRom.Scale(dst, dst.Bounds(), p.img, p.img.Bounds(), draw.Src, nil)
	data, err := encodePNG(dst)
	if err != nil {
		return nil, err
	}
	return &Rendition{Cols: cols, Rows: rows, PNG: data, rowPx: rowPx}, nil
}

// Slice returns rows [first, first+count) encoded for iTerm2, which cannot
// crop images itself. [Output] keeps the crops on screen, so they are not cached.
func (r *Rendition) Slice(first, count int) ([]byte, error) {
	if first < 0 || count <= 0 || first+count > r.Rows {
		return nil, errors.Errorf("invalid image rows %d+%d of %d", first, count, r.Rows)
	}
	if first == 0 && count == r.Rows {
		return r.PNG, nil
	}
	img, err := png.Decode(bytes.NewReader(r.PNG))
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode image rendition")
	}
	cropper, ok := img.(interface {
		SubImage(image.Rectangle) image.Image
	})
	if !ok {
		return nil, errors.New("image rendition cannot be cropped")
	}
	bounds := img.Bounds()
	sub := cropper.SubImage(image.Rect(
		bounds.Min.X,
		bounds.Min.Y+first*r.rowPx,
		bounds.Max.X,
		bounds.Min.Y+(first+count)*r.rowPx,
	))
	data, err := encodePNG(sub)
	if err != nil {
		return nil, err
	}
	if len(data) > maxITerm2Payload {
		var jpg bytes.Buffer
		if jpeg.Encode(&jpg, sub, &jpeg.Options{Quality: 88}) == nil && jpg.Len() < len(data) {
			data = jpg.Bytes()
		}
	}
	return data, nil
}

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, errors.Wrap(err, "failed to encode image")
	}
	return buf.Bytes(), nil
}
