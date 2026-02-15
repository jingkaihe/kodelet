package base

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImageMIMETypeFromExtension(t *testing.T) {
	tests := []struct {
		ext      string
		expected string
		hasError bool
	}{
		{".jpg", "image/jpeg", false},
		{".JPEG", "image/jpeg", false},
		{".png", "image/png", false},
		{".GIF", "image/gif", false},
		{".webp", "image/webp", false},
		{".txt", "", true},
		{"", "", true},
	}

	for _, test := range tests {
		t.Run(test.ext, func(t *testing.T) {
			mimeType, err := ImageMIMETypeFromExtension(test.ext)
			if test.hasError {
				assert.Error(t, err)
				assert.Empty(t, mimeType)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.expected, mimeType)
		})
	}
}

func TestValidateHTTPSImageURL(t *testing.T) {
	assert.NoError(t, ValidateHTTPSImageURL("https://example.com/image.png"))
	assert.Error(t, ValidateHTTPSImageURL("http://example.com/image.png"))
	assert.Error(t, ValidateHTTPSImageURL("ftp://example.com/image.png"))
}

func TestValidateDataURLPrefix(t *testing.T) {
	assert.NoError(t, ValidateDataURLPrefix("data:image/png;base64,abcd"))
	assert.Error(t, ValidateDataURLPrefix("image/png;base64,abcd"))
	assert.Error(t, ValidateDataURLPrefix(""))
}

func TestParseBase64DataURL(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		mimeType, data, err := ParseBase64DataURL("data:image/png;base64,abcd")
		require.NoError(t, err)
		assert.Equal(t, "image/png", mimeType)
		assert.Equal(t, "abcd", data)
	})

	t.Run("invalid prefix", func(t *testing.T) {
		_, _, err := ParseBase64DataURL("image/png;base64,abcd")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid data URL")
	})

	t.Run("missing separator", func(t *testing.T) {
		_, _, err := ParseBase64DataURL("data:image/png,abcd")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must contain ';base64,' separator")
	})
}

func TestReadImageFileAsBase64(t *testing.T) {
	tempDir := t.TempDir()

	t.Run("valid file", func(t *testing.T) {
		imageData := []byte("fake-image-data")
		imagePath := filepath.Join(tempDir, "sample.jpg")
		err := os.WriteFile(imagePath, imageData, 0o644)
		require.NoError(t, err)

		mimeType, encoded, err := ReadImageFileAsBase64(imagePath)
		require.NoError(t, err)
		assert.Equal(t, "image/jpeg", mimeType)
		assert.Equal(t, base64.StdEncoding.EncodeToString(imageData), encoded)
	})

	t.Run("non-existent file", func(t *testing.T) {
		_, _, err := ReadImageFileAsBase64(filepath.Join(tempDir, "missing.jpg"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "image file not found")
	})

	t.Run("unsupported format", func(t *testing.T) {
		filePath := filepath.Join(tempDir, "sample.txt")
		err := os.WriteFile(filePath, []byte("not an image"), 0o644)
		require.NoError(t, err)

		_, _, err = ReadImageFileAsBase64(filePath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported image format")
	})

	t.Run("file too large", func(t *testing.T) {
		filePath := filepath.Join(tempDir, "large.png")
		largeData := make([]byte, MaxImageFileSize+1)
		err := os.WriteFile(filePath, largeData, 0o644)
		require.NoError(t, err)

		_, _, err = ReadImageFileAsBase64(filePath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "image file too large")
	})
}

func TestReadImageFileAsDataURL(t *testing.T) {
	tempDir := t.TempDir()
	imageData := []byte("fake-image-data")
	imagePath := filepath.Join(tempDir, "sample.jpg")
	err := os.WriteFile(imagePath, imageData, 0o644)
	require.NoError(t, err)

	dataURL, err := ReadImageFileAsDataURL(imagePath)
	require.NoError(t, err)
	assert.Equal(t, "data:image/jpeg;base64,"+base64.StdEncoding.EncodeToString(imageData), dataURL)
}
