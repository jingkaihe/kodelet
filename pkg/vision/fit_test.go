package vision

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
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

	t.Run("downscales a landscape png", func(t *testing.T) {
		fitted, changed, err := FitBase64Image(
			Base64Image{MimeType: "image/png", Data: encodedTestPNG(t, testVisionImage(40, 20))},
			ImageLimits{MaxDimension: 10},
		)

		require.NoError(t, err)
		assert.True(t, changed)
		assert.Equal(t, "image/png", fitted.MimeType)
		width, height := encodedImageSize(t, fitted.Data)
		assert.Equal(t, 10, width)
		assert.Equal(t, 5, height)
	})

	t.Run("downscales a portrait jpeg", func(t *testing.T) {
		var buffer bytes.Buffer
		require.NoError(t, jpeg.Encode(&buffer, testVisionImage(20, 40), nil))

		fitted, changed, err := FitBase64Image(
			Base64Image{MimeType: "image/jpeg", Data: base64.StdEncoding.EncodeToString(buffer.Bytes())},
			ImageLimits{MaxDimension: 10},
		)

		require.NoError(t, err)
		assert.True(t, changed)
		assert.Equal(t, "image/jpeg", fitted.MimeType)
		width, height := encodedImageSize(t, fitted.Data)
		assert.Equal(t, 5, width)
		assert.Equal(t, 10, height)
	})

	t.Run("scales the longer side exactly to the limit", func(t *testing.T) {
		fitted, changed, err := FitBase64Image(
			Base64Image{MimeType: "image/png", Data: encodedTestPNG(t, testVisionImage(3001, 2))},
			ImageLimits{MaxDimension: 3000},
		)

		require.NoError(t, err)
		assert.True(t, changed)
		width, height := encodedImageSize(t, fitted.Data)
		assert.Equal(t, 3000, width)
		assert.Equal(t, 1, height)
	})

	t.Run("keeps a paletted png compact", func(t *testing.T) {
		// Converting this image to RGBA grew its payload from 6.2 MB to over 20 MB.
		rng := rand.New(rand.NewSource(1))
		palette := make(color.Palette, 16)
		for i := range palette {
			palette[i] = color.NRGBA{
				R: uint8(rng.Intn(256)),
				G: uint8(rng.Intn(256)),
				B: uint8(rng.Intn(256)),
				A: uint8(64 + rng.Intn(128)),
			}
		}
		src := image.NewPaletted(image.Rect(0, 0, 3100, 3000), palette)
		for i := range src.Pix {
			src.Pix[i] = uint8(rng.Intn(len(palette)))
		}
		img := Base64Image{MimeType: "image/png", Data: encodedTestPNG(t, src)}

		fitted, changed, err := FitBase64Image(
			img,
			ImageLimits{MaxDimension: 3000, MaxBase64Size: MaxAnthropicImageBase64Size},
		)

		require.NoError(t, err)
		assert.True(t, changed)
		assert.Less(t, len(fitted.Data), len(img.Data))
		decoded := decodedTestImage(t, fitted.Data)
		assert.IsType(t, &image.Paletted{}, decoded)
		assert.Equal(t, image.Rect(0, 0, 3000, 2903), decoded.Bounds())
	})

	t.Run("keeps a grayscale png grayscale", func(t *testing.T) {
		src := image.NewGray(image.Rect(0, 0, 40, 20))
		for i := range src.Pix {
			src.Pix[i] = uint8(i)
		}

		fitted, changed, err := FitBase64Image(
			Base64Image{MimeType: "image/png", Data: encodedTestPNG(t, src)},
			ImageLimits{MaxDimension: 10},
		)

		require.NoError(t, err)
		assert.True(t, changed)
		decoded := decodedTestImage(t, fitted.Data)
		assert.IsType(t, &image.Gray{}, decoded)
		assert.Equal(t, image.Rect(0, 0, 10, 5), decoded.Bounds())
	})

	t.Run("shrinks a resized image until it fits the byte limit", func(t *testing.T) {
		img := Base64Image{MimeType: "image/png", Data: encodedTestPNG(t, noisyTestImage(200, 200))}

		fitted, changed, err := FitBase64Image(img, ImageLimits{MaxDimension: 150, MaxBase64Size: 40_000})

		require.NoError(t, err)
		assert.True(t, changed)
		assert.LessOrEqual(t, len(fitted.Data), 40_000)
		width, height := encodedImageSize(t, fitted.Data)
		assert.Less(t, width, 150)
		assert.Equal(t, width, height)
	})

	t.Run("shrinks an image that only exceeds the byte limit", func(t *testing.T) {
		img := Base64Image{MimeType: "image/png", Data: encodedTestPNG(t, noisyTestImage(200, 200))}

		fitted, changed, err := FitBase64Image(img, ImageLimits{MaxBase64Size: 40_000})

		require.NoError(t, err)
		assert.True(t, changed)
		assert.LessOrEqual(t, len(fitted.Data), 40_000)
		width, height := encodedImageSize(t, fitted.Data)
		assert.Less(t, width, 200)
		assert.Equal(t, width, height)
	})

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
			ImageLimits{MaxDimension: 3000},
		)

		require.NoError(t, err)
		assert.True(t, changed)
		assert.Equal(t, "image/gif", fitted.MimeType)
		decoded := decodedTestImage(t, fitted.Data)
		require.Equal(t, image.Rect(0, 0, 3000, 937), decoded.Bounds())
		assert.Equal(t, color.RGBA{R: 255, G: 255, B: 255, A: 255}, color.RGBAModel.Convert(decoded.At(1500, 468)))
		_, _, _, alpha := decoded.At(10, 10).RGBA()
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

func TestMakeViewImageResultBytesKeepsGIFCanvas(t *testing.T) {
	result, err := MakeViewImageResultBytes(encodedOffsetGIF(t), "offset.gif", "", "claude-sonnet-4-6", "anthropic")

	require.NoError(t, err)
	assert.Equal(t, "image/gif", result.MimeType)
	assert.Equal(t, 2048, result.Width)
	assert.Equal(t, 640, result.Height)
}

// encodedOffsetGIF returns a 3200x1000 GIF whose only frame is a white
// 200x200 square at (1500, 400).
func encodedOffsetGIF(t *testing.T) []byte {
	t.Helper()

	palette := color.Palette{color.Black, color.White}
	frame := image.NewPaletted(image.Rect(1500, 400, 1700, 600), palette)
	for i := range frame.Pix {
		frame.Pix[i] = 1
	}
	var buffer bytes.Buffer
	require.NoError(t, gif.EncodeAll(&buffer, &gif.GIF{
		Image:  []*image.Paletted{frame},
		Delay:  []int{0},
		Config: image.Config{Width: 3200, Height: 1000, ColorModel: palette},
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

func encodedImageSize(t *testing.T, data string) (int, int) {
	t.Helper()

	config, _, err := image.DecodeConfig(base64.NewDecoder(base64.StdEncoding, strings.NewReader(data)))
	require.NoError(t, err)
	return config.Width, config.Height
}

func decodedTestImage(t *testing.T, data string) image.Image {
	t.Helper()

	img, _, err := image.Decode(base64.NewDecoder(base64.StdEncoding, strings.NewReader(data)))
	require.NoError(t, err)
	return img
}
