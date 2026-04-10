package vision

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMakeViewImageResultUsesDecodedImageFormat(t *testing.T) {
	tempDir := t.TempDir()
	imagePath := filepath.Join(tempDir, "wrong-extension.jpg")

	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	for y := 0; y < 3; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, color.RGBA{R: 10, G: 20, B: 30, A: 255})
		}
	}

	file, err := os.Create(imagePath)
	require.NoError(t, err)
	require.NoError(t, png.Encode(file, img))
	require.NoError(t, file.Close())

	result, err := MakeViewImageResult(imagePath, "", "gpt-5", "openai")
	require.NoError(t, err)
	assert.Equal(t, "image/png", result.MimeType)
	assert.Contains(t, result.ImageURL, "data:image/png;base64,")
}
