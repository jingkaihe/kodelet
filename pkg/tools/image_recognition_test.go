package tools

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImageRecognitionTool_Name(t *testing.T) {
	tool := &ImageRecognitionTool{}
	assert.Equal(t, "image_recognition", tool.Name())
}

func TestImageRecognitionTool_GenerateSchema(t *testing.T) {
	tool := &ImageRecognitionTool{}
	schema := tool.GenerateSchema()

	assert.NotNil(t, schema)
	assert.NotNil(t, schema.Properties)

	// Check that image_path and prompt properties exist
	_, hasImagePath := schema.Properties.Get("image_path")
	assert.True(t, hasImagePath)

	_, hasPrompt := schema.Properties.Get("prompt")
	assert.True(t, hasPrompt)
}

func TestImageRecognitionTool_ValidateInput(t *testing.T) {
	tool := &ImageRecognitionTool{}

	tests := []struct {
		name        string
		parameters  string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "valid input with HTTPS URL",
			parameters:  `{"image_path": "https://httpbin.org/base64/iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8/5+hHgAHggJ/PchI7wAAAABJRU5ErkJggg==", "prompt": "What is in this image?"}`,
			expectError: false,
		},
		{
			name:        "empty image_path",
			parameters:  `{"image_path": "", "prompt": "What is in this image?"}`,
			expectError: true,
			errorMsg:    "image_path is required",
		},
		{
			name:        "empty prompt",
			parameters:  `{"image_path": "https://example.com/image.jpg", "prompt": ""}`,
			expectError: true,
			errorMsg:    "prompt is required",
		},
		{
			name:        "invalid JSON",
			parameters:  `{"image_path": "https://example.com/image.jpg"`,
			expectError: true,
		},
		{
			name:        "HTTP URL (not HTTPS)",
			parameters:  `{"image_path": "http://example.com/image.jpg", "prompt": "What is in this image?"}`,
			expectError: true,
			errorMsg:    "only HTTPS URLs are supported for security",
		},
	}

	// Add a test with a fake file to check unsupported format validation
	tempDir := t.TempDir()
	txtPath := filepath.Join(tempDir, "test.txt")
	err := os.WriteFile(txtPath, []byte("not an image"), 0o644)
	require.NoError(t, err)

	tests = append(tests, struct {
		name        string
		parameters  string
		expectError bool
		errorMsg    string
	}{
		name:        "unsupported file format",
		parameters:  `{"image_path": "` + txtPath + `", "prompt": "What is in this image?"}`,
		expectError: true,
		errorMsg:    "unsupported image format",
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tool.ValidateInput(nil, tt.parameters)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestImageRecognitionTool_ValidateLocalImageFile(t *testing.T) {
	tool := &ImageRecognitionTool{}

	// Create a temporary image file for testing
	tempDir := t.TempDir()

	t.Run("valid image file", func(t *testing.T) {
		// Create a small PNG file
		imagePath := filepath.Join(tempDir, "test.png")
		err := os.WriteFile(imagePath, []byte("fake png content"), 0o644)
		require.NoError(t, err)

		// Test with file:// prefix
		err = tool.validateImagePath("file://" + imagePath)
		assert.NoError(t, err)

		// Test without file:// prefix
		err = tool.validateImagePath(imagePath)
		assert.NoError(t, err)
	})

	t.Run("nonexistent file", func(t *testing.T) {
		err := tool.validateImagePath("/nonexistent/file.jpg")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "image file not found")
	})

	t.Run("unsupported format", func(t *testing.T) {
		txtPath := filepath.Join(tempDir, "test.txt")
		err := os.WriteFile(txtPath, []byte("not an image"), 0o644)
		require.NoError(t, err)

		err = tool.validateImagePath(txtPath)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported image format")
	})

	t.Run("file too large", func(t *testing.T) {
		// Create a file larger than 5MB
		largePath := filepath.Join(tempDir, "large.jpg")
		largeContent := make([]byte, 6*1024*1024) // 6MB
		err := os.WriteFile(largePath, largeContent, 0o644)
		require.NoError(t, err)

		err = tool.validateImagePath(largePath)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "image file too large")
	})
}

func TestImageRecognitionTool_TracingKVs(t *testing.T) {
	tool := &ImageRecognitionTool{}

	t.Run("valid parameters", func(t *testing.T) {
		parameters := `{"image_path": "https://example.com/image.jpg", "prompt": "Describe this image"}`

		kvs, err := tool.TracingKVs(parameters)
		assert.NoError(t, err)
		assert.NotEmpty(t, kvs)

		// Check that we have the expected attributes
		attrs := make(map[string]interface{})
		for _, kv := range kvs {
			attrs[string(kv.Key)] = kv.Value.AsInterface()
		}

		assert.Equal(t, "https://example.com/image.jpg", attrs["image_path"])
		assert.Equal(t, "remote_url", attrs["image_type"])
		assert.Equal(t, int64(len("Describe this image")), attrs["prompt_length"])
	})

	t.Run("local file parameters", func(t *testing.T) {
		parameters := `{"image_path": "/path/to/image.png", "prompt": "Test"}`

		kvs, err := tool.TracingKVs(parameters)
		assert.NoError(t, err)

		attrs := make(map[string]interface{})
		for _, kv := range kvs {
			attrs[string(kv.Key)] = kv.Value.AsInterface()
		}

		assert.Equal(t, "local_file", attrs["image_type"])
		assert.Equal(t, ".png", attrs["image_format"])
	})

	t.Run("invalid JSON", func(t *testing.T) {
		parameters := `invalid json`

		_, err := tool.TracingKVs(parameters)
		assert.Error(t, err)
	})
}

// Note: Execute method tests are not included because the image_recognition tool
// uses shell-out pattern via exec.CommandContext. Testing the Execute method
// would require integration tests with actual kodelet binary or mocking the
// exec.Command, which is beyond unit test scope.
// The shell-out pattern is tested at the integration/acceptance level.
