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
