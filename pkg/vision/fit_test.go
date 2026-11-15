package vision

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"math/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFitBase64Image(t *testing.T) {
	t.Run("keeps an image that already fits", func(t *testing.T) {
		img := Base64Image{MimeType: "image/png", Data: encodedTestPNG(t, testVisionImage(10, 10))}

		fitted, changed, err := FitBase64Image(img, ImageLimits{MaxDimension: 10, MaxBase64Size: len(img.Data)})

		require.NoError(t, err)
		assert.False(t, changed)
		assert.Equal(t, img, fitted)
	})

	for _, tt := range []struct {
		name         string
		src          image.Image
		maxDimension int
		wantSize     image.Point
	}{
		{"landscape rounding", testVisionImage(3001, 2), 3000, image.Pt(3000, 1)},
		{"portrait", testVisionImage(20, 40), 10, image.Pt(5, 10)},
		{
			name:         "compact paletted PNG",
			src:          image.NewPaletted(image.Rect(0, 0, 40, 20), color.Palette{color.Transparent, color.White}),
			maxDimension: 10,
			wantSize:     image.Pt(10, 5),
		},
		{"grayscale PNG", image.NewGray(image.Rect(0, 0, 40, 20)), 10, image.Pt(10, 5)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fitted, changed, err := FitBase64Image(
				Base64Image{MimeType: "image/png", Data: encodedTestPNG(t, tt.src)},
				ImageLimits{MaxDimension: tt.maxDimension},
			)

			require.NoError(t, err)
			assert.True(t, changed)
			assert.Equal(t, "image/png", fitted.MimeType)
			decoded := decodedTestImage(t, fitted.Data)
			assert.Equal(t, tt.wantSize, decoded.Bounds().Size())
			assert.IsType(t, tt.src, decoded, "resizing must preserve compact pixel formats")
		})
	}

	for _, tt := range []struct {
		name                   string
		maxDimension, maxWidth int
	}{
		{"byte limit only", 0, 200},
		{"dimension and byte limits", 150, 150},
	} {
		t.Run(tt.name, func(t *testing.T) {
			img := Base64Image{MimeType: "image/png", Data: encodedTestPNG(t, noisyTestImage(200, 200))}

			fitted, changed, err := FitBase64Image(img, ImageLimits{MaxDimension: tt.maxDimension, MaxBase64Size: 40_000})

			require.NoError(t, err)
			assert.True(t, changed)
			assert.LessOrEqual(t, len(fitted.Data), 40_000)
			size := decodedTestImage(t, fitted.Data).Bounds().Size()
			assert.Less(t, size.X, tt.maxWidth)
			assert.Equal(t, size.X, size.Y)
		})
	}

	t.Run("returns the original when the byte limit cannot be met", func(t *testing.T) {
		img := Base64Image{MimeType: "image/png", Data: encodedTestPNG(t, noisyTestImage(20, 20))}

		fitted, changed, err := FitBase64Image(img, ImageLimits{MaxBase64Size: 10})

		require.ErrorContains(t, err, "could not fit within the 10 byte base64 limit")
		assert.False(t, changed)
		assert.Equal(t, img, fitted)
	})

	t.Run("keeps the canvas of a gif with an offset first frame", func(t *testing.T) {
		data := encodedOffsetGIF(t)

		fitted, changed, err := FitBase64Image(
			Base64Image{MimeType: "image/gif", Data: base64.StdEncoding.EncodeToString(data)},
			ImageLimits{MaxDimension: 30},
		)

		require.NoError(t, err)
		assert.True(t, changed)
		assert.Equal(t, "image/gif", fitted.MimeType)
		decoded := decodedTestImage(t, fitted.Data)
		require.Equal(t, image.Rect(0, 0, 30, 9), decoded.Bounds())
		assert.Equal(t, color.RGBAModel.Convert(color.White), color.RGBAModel.Convert(decoded.At(15, 4)))
		_, _, _, alpha := decoded.At(0, 0).RGBA()
		assert.Zero(t, alpha, "the area outside the first frame stays transparent")
	})

	t.Run("rejects invalid input", func(t *testing.T) {
		_, _, err := FitBase64Image(
			Base64Image{MimeType: "image/png", Data: encodedTestPNG(t, testVisionImage(2, 2))},
			ImageLimits{MaxDimension: -1},
		)
		require.ErrorContains(t, err, "invalid image limits")

		img := Base64Image{MimeType: "image/png", Data: base64.StdEncoding.EncodeToString([]byte("not an image"))}
		fitted, changed, err := FitBase64Image(img, ImageLimits{MaxDimension: 10})
		require.ErrorContains(t, err, "failed to read image dimensions")
		assert.False(t, changed)
		assert.Equal(t, img, fitted)
	})
}

// encodedOffsetGIF returns a 32x10 GIF whose only frame is a white
// 2x2 square at (15, 4).
func encodedOffsetGIF(t *testing.T) []byte {
	t.Helper()

	palette := color.Palette{color.Black, color.White}
	frame := image.NewPaletted(image.Rect(15, 4, 17, 6), palette)
	for i := range frame.Pix {
		frame.Pix[i] = 1
	}
	var buffer bytes.Buffer
	require.NoError(t, gif.EncodeAll(&buffer, &gif.GIF{
		Image:  []*image.Paletted{frame},
		Delay:  []int{0},
		Config: image.Config{Width: 32, Height: 10, ColorModel: palette},
	}))
	return buffer.Bytes()
}

func noisyTestImage(width, height int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	_, _ = rand.New(rand.NewSource(1)).Read(img.Pix)
	return img
}

func encodedTestPNG(t *testing.T, img image.Image) string {
	t.Helper()

	var buffer bytes.Buffer
	require.NoError(t, png.Encode(&buffer, img))
	return base64.StdEncoding.EncodeToString(buffer.Bytes())
}

func decodedTestImage(t *testing.T, data string) image.Image {
	t.Helper()

	img, _, err := image.Decode(base64.NewDecoder(base64.StdEncoding, strings.NewReader(data)))
	require.NoError(t, err)
	return img
}
