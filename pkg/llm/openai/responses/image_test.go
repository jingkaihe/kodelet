package responses

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/llm/base"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessImage(t *testing.T) {
	t.Run("handles https URLs", func(t *testing.T) {
		url := "https://example.com/image.png"
		result, err := processImage(url)

		require.NoError(t, err)
		require.NotNil(t, result.OfInputImage)
		assert.Equal(t, url, result.OfInputImage.ImageURL.Value)
	})

	t.Run("handles http URLs", func(t *testing.T) {
		url := "http://example.com/image.png"
		_, err := processImage(url)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "only HTTPS URLs are supported for security")
	})

	t.Run("handles data URLs from ACP", func(t *testing.T) {
		// This is the format ACP sends - already encoded data URL
		dataURL := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
		result, err := processImage(dataURL)

		require.NoError(t, err)
		require.NotNil(t, result.OfInputImage)
		assert.Equal(t, dataURL, result.OfInputImage.ImageURL.Value)
	})

	t.Run("handles data URLs with different mime types", func(t *testing.T) {
		tests := []struct {
			name    string
			dataURL string
		}{
			{"jpeg", "data:image/jpeg;base64,/9j/4AAQSkZJRg=="},
			{"gif", "data:image/gif;base64,R0lGODlhAQAB"},
			{"webp", "data:image/webp;base64,UklGRh4AAABXRUJQVlA4"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				result, err := processImage(tt.dataURL)

				require.NoError(t, err)
				require.NotNil(t, result.OfInputImage)
				assert.Equal(t, tt.dataURL, result.OfInputImage.ImageURL.Value)
			})
		}
	})

	t.Run("handles local file", func(t *testing.T) {
		// Create a temporary PNG file
		tmpDir := t.TempDir()
		tmpFile := filepath.Join(tmpDir, "test.png")

		// Minimal valid PNG (1x1 transparent pixel)
		pngData := []byte{
			0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
			0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
			0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
			0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
			0x89, 0x00, 0x00, 0x00, 0x0A, 0x49, 0x44, 0x41,
			0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
			0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00,
			0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE,
			0x42, 0x60, 0x82,
		}
		err := os.WriteFile(tmpFile, pngData, 0o644)
		require.NoError(t, err)

		result, err := processImage(tmpFile)

		require.NoError(t, err)
		require.NotNil(t, result.OfInputImage)
		// Should be converted to data URL
		assert.Contains(t, result.OfInputImage.ImageURL.Value, "data:image/png;base64,")
	})

	t.Run("returns error for non-existent file", func(t *testing.T) {
		_, err := processImage("/non/existent/path/image.png")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to stat image file")
	})

	t.Run("returns error for oversized local file", func(t *testing.T) {
		tmpDir := t.TempDir()
		tmpFile := filepath.Join(tmpDir, "large.png")
		tooLarge := make([]byte, base.MaxImageFileSize+1)
		err := os.WriteFile(tmpFile, tooLarge, 0o644)
		require.NoError(t, err)

		_, err = processImage(tmpFile)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "image file too large")
	})
}

func TestGetMimeType(t *testing.T) {
	tests := []struct {
		path     string
		expected string
		wantErr  bool
	}{
		{"/path/to/image.jpg", "image/jpeg", false},
		{"/path/to/image.jpeg", "image/jpeg", false},
		{"/path/to/image.JPG", "image/jpeg", false},
		{"/path/to/image.png", "image/png", false},
		{"/path/to/image.PNG", "image/png", false},
		{"/path/to/image.gif", "image/gif", false},
		{"/path/to/image.webp", "image/webp", false},
		{"/path/to/image.unknown", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result, err := getMimeType(tt.path)
			if tt.wantErr {
				require.Error(t, err)
				assert.Empty(t, result)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}
