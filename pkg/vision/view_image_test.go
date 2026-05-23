package vision

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
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

func TestSupportsViewImageOriginalDetailIncludesFlagshipProModels(t *testing.T) {
	assert.True(t, SupportsViewImageOriginalDetail("gpt-5.5-pro"))
	assert.True(t, SupportsViewImageOriginalDetail("gpt-5.4-pro"))
}

func TestVisionSupportAndDetailHelpers(t *testing.T) {
	assert.True(t, SupportsImageInputs("any", "model"))
	assert.True(t, SupportsViewImageOriginalDetail(" GPT-5.4-MINI "))
	assert.False(t, SupportsViewImageOriginalDetail("gpt-5"))

	detail, err := NormalizeViewImageDetail("", "gpt-5")
	require.NoError(t, err)
	assert.Empty(t, detail)

	detail, err = NormalizeViewImageDetail(" original ", "gpt-5.5")
	require.NoError(t, err)
	assert.Equal(t, "original", detail)

	_, err = NormalizeViewImageDetail("full", "gpt-5.5")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only supports `original`")

	_, err = NormalizeViewImageDetail("original", "gpt-5")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "compatible models")

	assert.Equal(t, "data:image/png;base64,abc", dataURLFromBase64Payload("image/png", "abc"))
	assert.Equal(t, 3, maxInt(2, 3))
	assert.Equal(t, 4, maxInt(4, 1))
}

func TestMakeViewImageResultValidationErrors(t *testing.T) {
	dir := t.TempDir()

	_, err := MakeViewImageResult("   ", "", "gpt-5", "openai")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unable to locate image")

	_, err = MakeViewImageResult(filepath.Join(dir, "missing.png"), "", "gpt-5", "openai")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unable to locate image")

	_, err = MakeViewImageResult(dir, "", "gpt-5", "openai")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not a file")

	largePath := filepath.Join(dir, "large.png")
	require.NoError(t, os.WriteFile(largePath, make([]byte, maxImageFileSize+1), 0o644))
	_, err = MakeViewImageResult(largePath, "", "gpt-5", "openai")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "image file too large")

	textPath := filepath.Join(dir, "not-image.txt")
	require.NoError(t, os.WriteFile(textPath, []byte("hello"), 0o644))
	_, err = MakeViewImageResult("file://"+textPath, "", "gpt-5", "openai")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported image")
}

func TestMakeViewImageResultResizesAndPreservesOriginalWhenRequested(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "wide.png")
	writeVisionPNG(t, imagePath, ViewImageMaxWidth+256, 100)

	resized, err := MakeViewImageResult(imagePath, "", "gpt-5.5", "openai")
	require.NoError(t, err)
	assert.Equal(t, ViewImageMaxWidth, resized.Width)
	assert.Less(t, resized.Height, 100)
	assert.Equal(t, "image/png", resized.MimeType)
	assert.Contains(t, resized.Assistant, "Viewed image")

	original, err := MakeViewImageResult(imagePath, "original", "gpt-5.5", "openai")
	require.NoError(t, err)
	assert.Equal(t, ViewImageMaxWidth+256, original.Width)
	assert.Equal(t, 100, original.Height)
	assert.Equal(t, "original", original.Detail)
}

func TestMetadataFromResult(t *testing.T) {
	assert.Nil(t, MetadataFromResult(nil))

	metadata := MetadataFromResult(&Result{Path: "/tmp/a.png", MimeType: "image/png", Width: 4, Height: 5, Detail: "original"})
	require.NotNil(t, metadata)
	assert.Equal(t, "/tmp/a.png", metadata.Path)
	assert.Equal(t, "image/png", metadata.MimeType)
	assert.Equal(t, "original", metadata.Detail)
	assert.Equal(t, 4, metadata.ImageSize.Width)
	assert.Equal(t, 5, metadata.ImageSize.Height)
}

func TestMimeAndEncodingHelpers(t *testing.T) {
	for _, tt := range []struct {
		format string
		mime   string
	}{
		{format: "jpeg", mime: "image/jpeg"},
		{format: "jpg", mime: "image/jpeg"},
		{format: "png", mime: "image/png"},
		{format: "gif", mime: "image/gif"},
		{format: "webp", mime: "image/webp"},
	} {
		got, err := mimeTypeForDecodedImage(tt.format)
		require.NoError(t, err)
		assert.Equal(t, tt.mime, got)
	}
	_, err := mimeTypeForDecodedImage("bmp")
	require.Error(t, err)

	for _, mimeType := range []string{"image/jpeg", "image/png", "image/gif", "image/webp", "image/unknown"} {
		encoded, outMime, err := encodeImageBytes(testVisionImage(2, 2), mimeType)
		require.NoError(t, err)
		assert.NotEmpty(t, encoded)
		if mimeType == "image/webp" || mimeType == "image/unknown" {
			assert.Equal(t, "image/png", outMime)
		} else {
			assert.Equal(t, mimeType, outMime)
		}
	}

	mediaType, err := base64ImageSourceMediaType(" IMAGE/PNG ")
	require.NoError(t, err)
	assert.Equal(t, "image/png", mediaType)
	_, err = base64ImageSourceMediaType("image/svg+xml")
	require.Error(t, err)
}

func TestMakeViewImageResultSupportsJPEGAndGIF(t *testing.T) {
	dir := t.TempDir()
	jpegPath := filepath.Join(dir, "photo.jpeg")
	gifPath := filepath.Join(dir, "anim.gif")

	var jpegBuf bytes.Buffer
	require.NoError(t, jpeg.Encode(&jpegBuf, testVisionImage(3, 2), nil))
	require.NoError(t, os.WriteFile(jpegPath, jpegBuf.Bytes(), 0o644))

	gifFile, err := os.Create(gifPath)
	require.NoError(t, err)
	require.NoError(t, gif.Encode(gifFile, testVisionImage(2, 3), nil))
	require.NoError(t, gifFile.Close())

	jpegResult, err := MakeViewImageResult(jpegPath, "", "gpt-5", "openai")
	require.NoError(t, err)
	assert.Equal(t, "image/jpeg", jpegResult.MimeType)
	assert.Equal(t, 3, jpegResult.Width)
	assert.Equal(t, 2, jpegResult.Height)

	gifResult, err := MakeViewImageResult(gifPath, "", "gpt-5", "openai")
	require.NoError(t, err)
	assert.Equal(t, "image/gif", gifResult.MimeType)
	assert.Equal(t, 2, gifResult.Width)
	assert.Equal(t, 3, gifResult.Height)
}

func writeVisionPNG(t *testing.T, path string, width, height int) {
	t.Helper()

	file, err := os.Create(path)
	require.NoError(t, err)
	defer file.Close()
	require.NoError(t, png.Encode(file, testVisionImage(width, height)))
}

func testVisionImage(width, height int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 30, A: 255})
		}
	}
	return img
}
