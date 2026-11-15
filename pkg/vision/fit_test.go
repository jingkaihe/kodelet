package vision

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFitBase64Image(t *testing.T) {
	t.Run("keeps an image that already fits", func(t *testing.T) {
		img := Base64Image{MimeType: "image/png", Data: encodedTestPNG(t, 10, 10)}

		fitted, changed, err := FitBase64Image(img, 10)

		require.NoError(t, err)
		assert.False(t, changed)
		assert.Equal(t, img, fitted)
	})

	t.Run("downscales a landscape png", func(t *testing.T) {
		fitted, changed, err := FitBase64Image(
			Base64Image{MimeType: "image/png", Data: encodedTestPNG(t, 40, 20)},
			10,
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
			10,
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
			Base64Image{MimeType: "image/png", Data: encodedTestPNG(t, 3001, 2)},
			3000,
		)

		require.NoError(t, err)
		assert.True(t, changed)
		width, height := encodedImageSize(t, fitted.Data)
		assert.Equal(t, 3000, width)
		assert.Equal(t, 1, height)
	})

	t.Run("rejects invalid input", func(t *testing.T) {
		_, _, err := FitBase64Image(Base64Image{MimeType: "image/png", Data: encodedTestPNG(t, 2, 2)}, 0)
		require.ErrorContains(t, err, "invalid maximum image dimension")

		img := Base64Image{MimeType: "image/png", Data: base64.StdEncoding.EncodeToString([]byte("not an image"))}
		fitted, changed, err := FitBase64Image(img, 10)
		require.ErrorContains(t, err, "failed to read image dimensions")
		assert.False(t, changed)
		assert.Equal(t, img, fitted)
	})
}

func encodedTestPNG(t *testing.T, width, height int) string {
	t.Helper()

	var buffer bytes.Buffer
	require.NoError(t, png.Encode(&buffer, testVisionImage(width, height)))
	return base64.StdEncoding.EncodeToString(buffer.Bytes())
}

func encodedImageSize(t *testing.T, data string) (int, int) {
	t.Helper()

	config, _, err := image.DecodeConfig(base64.NewDecoder(base64.StdEncoding, strings.NewReader(data)))
	require.NoError(t, err)
	return config.Width, config.Height
}
