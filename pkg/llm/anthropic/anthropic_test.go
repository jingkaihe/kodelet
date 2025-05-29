package anthropic

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

func TestGetMediaTypeFromExtension(t *testing.T) {
	tests := []struct {
		ext      string
		expected anthropic.Base64ImageSourceMediaType
		hasError bool
	}{
		{".jpg", anthropic.Base64ImageSourceMediaTypeImageJPEG, false},
		{".jpeg", anthropic.Base64ImageSourceMediaTypeImageJPEG, false},
		{".JPG", anthropic.Base64ImageSourceMediaTypeImageJPEG, false},
		{".JPEG", anthropic.Base64ImageSourceMediaTypeImageJPEG, false},
		{".png", anthropic.Base64ImageSourceMediaTypeImagePNG, false},
		{".PNG", anthropic.Base64ImageSourceMediaTypeImagePNG, false},
		{".gif", anthropic.Base64ImageSourceMediaTypeImageGIF, false},
		{".GIF", anthropic.Base64ImageSourceMediaTypeImageGIF, false},
		{".webp", anthropic.Base64ImageSourceMediaTypeImageWebP, false},
		{".WEBP", anthropic.Base64ImageSourceMediaTypeImageWebP, false},
		{".bmp", "", true},
		{".svg", "", true},
		{".txt", "", true},
		{"", "", true},
	}

	for _, test := range tests {
		t.Run(test.ext, func(t *testing.T) {
			result, err := getMediaTypeFromExtension(test.ext)
			if test.hasError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, test.expected, result)
			}
		})
	}
}

func TestProcessImageURL(t *testing.T) {
	thread := NewAnthropicThread(llmtypes.Config{})

	tests := []struct {
		name     string
		url      string
		hasError bool
	}{
		{"Valid HTTPS URL", "https://example.com/image.jpg", false},
		{"HTTP URL (should fail)", "http://example.com/image.jpg", true},
		{"Invalid URL format", "not-a-url", true},
		{"FTP URL (should fail)", "ftp://example.com/image.jpg", true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := thread.processImageURL(test.url)
			if test.hasError {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
			}
		})
	}
}

func TestProcessImageFile(t *testing.T) {
	thread := NewAnthropicThread(llmtypes.Config{})

	// Create a temporary directory for test files
	tempDir := t.TempDir()

	// Create a small test image file (PNG)
	pngData := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG signature
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52, // IHDR chunk header
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, // 1x1 pixel
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4, // bit depth, color type, etc.
		0x89, 0x00, 0x00, 0x00, 0x0A, 0x49, 0x44, 0x41, // IDAT chunk
		0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00,
		0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE, // IEND chunk
		0x42, 0x60, 0x82,
	}

	testImagePath := filepath.Join(tempDir, "test.png")
	err := os.WriteFile(testImagePath, pngData, 0644)
	require.NoError(t, err)

	// Create a large test file (exceeds MaxImageFileSize)
	largeFilePath := filepath.Join(tempDir, "large.png")
	largeData := make([]byte, MaxImageFileSize+1)
	err = os.WriteFile(largeFilePath, largeData, 0644)
	require.NoError(t, err)

	// Create a file with unsupported extension
	unsupportedPath := filepath.Join(tempDir, "test.bmp")
	err = os.WriteFile(unsupportedPath, pngData, 0644)
	require.NoError(t, err)

	tests := []struct {
		name     string
		filePath string
		hasError bool
	}{
		{"Valid PNG file", testImagePath, false},
		{"Non-existent file", filepath.Join(tempDir, "nonexistent.png"), true},
		{"File too large", largeFilePath, true},
		{"Unsupported format", unsupportedPath, true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := thread.processImageFile(test.filePath)
			if test.hasError {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
			}
		})
	}
}

func TestAddUserMessage(t *testing.T) {
	thread := NewAnthropicThread(llmtypes.Config{})

	// Create a temporary directory for test files
	tempDir := t.TempDir()

	// Create a small test image file
	pngData := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG signature
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52, // IHDR chunk header
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, // 1x1 pixel
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4, // bit depth, color type, etc.
		0x89, 0x00, 0x00, 0x00, 0x0A, 0x49, 0x44, 0x41, // IDAT chunk
		0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00,
		0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE, // IEND chunk
		0x42, 0x60, 0x82,
	}

	testImagePath := filepath.Join(tempDir, "test.png")
	err := os.WriteFile(testImagePath, pngData, 0644)
	require.NoError(t, err)

	tests := []struct {
		name        string
		message     string
		images      []string
		expectCount int // Expected number of content blocks
	}{
		{"Text only", "Hello world", nil, 1},
		{"Text with valid image", "Analyze this image", []string{testImagePath}, 2},
		{"Text with HTTPS URL", "Check this URL", []string{"https://example.com/image.jpg"}, 2},
		{"Text with mixed valid/invalid images", "Mixed test", []string{testImagePath, "invalid-path.png"}, 2}, // Only valid image should be added
		{"Too many images", "Many images", make([]string, MaxImageCount+5), 1 + MaxImageCount},                 // Should cap at MaxImageCount
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			initialCount := len(thread.messages)

			// For the "too many images" test, fill the slice with valid HTTPS URLs
			if test.name == "Too many images" {
				for i := range test.images {
					test.images[i] = "https://example.com/image.jpg"
				}
			}

			thread.AddUserMessage(context.Background(), test.message, test.images...)

			// Should have added exactly one message
			assert.Equal(t, initialCount+1, len(thread.messages))

			// Check the last message
			lastMessage := thread.messages[len(thread.messages)-1]
			assert.Equal(t, anthropic.MessageParamRoleUser, lastMessage.Role)

			// Check content block count (text + valid images)
			expectedBlocks := test.expectCount
			if test.name == "Text with mixed valid/invalid images" {
				// Only the text and the valid image should be added
				expectedBlocks = 2
			}
			assert.Equal(t, expectedBlocks, len(lastMessage.Content))
		})
	}
}
