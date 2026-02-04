package anthropic

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/invopop/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"

	"github.com/jingkaihe/kodelet/pkg/llm/base"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
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
	thread, err := NewAnthropicThread(llmtypes.Config{})
	require.NoError(t, err)

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

func TestProcessImageDataURL(t *testing.T) {
	thread, err := NewAnthropicThread(llmtypes.Config{})
	require.NoError(t, err)

	// A minimal valid 1x1 PNG image encoded in base64
	validPNGBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="

	tests := []struct {
		name     string
		dataURL  string
		hasError bool
	}{
		{"Valid PNG data URL", "data:image/png;base64," + validPNGBase64, false},
		{"Valid JPEG data URL", "data:image/jpeg;base64," + validPNGBase64, false},
		{"Valid GIF data URL", "data:image/gif;base64," + validPNGBase64, false},
		{"Valid WebP data URL", "data:image/webp;base64," + validPNGBase64, false},
		{"Missing data: prefix", "image/png;base64," + validPNGBase64, true},
		{"Missing base64 separator", "data:image/png," + validPNGBase64, true},
		{"Unsupported mime type", "data:image/bmp;base64," + validPNGBase64, true},
		{"Unsupported mime type svg", "data:image/svg+xml;base64," + validPNGBase64, true},
		{"Empty data URL", "", true},
		// Note: Invalid base64 is not validated client-side by Anthropic SDK, validation happens server-side
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := thread.processImageDataURL(test.dataURL)
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

func TestMimeTypeToAnthropicMediaType(t *testing.T) {
	tests := []struct {
		mimeType string
		expected anthropic.Base64ImageSourceMediaType
		hasError bool
	}{
		{"image/jpeg", anthropic.Base64ImageSourceMediaTypeImageJPEG, false},
		{"image/png", anthropic.Base64ImageSourceMediaTypeImagePNG, false},
		{"image/gif", anthropic.Base64ImageSourceMediaTypeImageGIF, false},
		{"image/webp", anthropic.Base64ImageSourceMediaTypeImageWebP, false},
		{"IMAGE/JPEG", anthropic.Base64ImageSourceMediaTypeImageJPEG, false}, // Case insensitive
		{"IMAGE/PNG", anthropic.Base64ImageSourceMediaTypeImagePNG, false},
		{"image/bmp", "", true},
		{"image/svg+xml", "", true},
		{"text/plain", "", true},
		{"", "", true},
	}

	for _, test := range tests {
		t.Run(test.mimeType, func(t *testing.T) {
			result, err := mimeTypeToAnthropicMediaType(test.mimeType)
			if test.hasError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, test.expected, result)
			}
		})
	}
}

func TestProcessImage_DataURLRouting(t *testing.T) {
	thread, err := NewAnthropicThread(llmtypes.Config{})
	require.NoError(t, err)

	validPNGBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="

	tests := []struct {
		name      string
		imagePath string
		hasError  bool
	}{
		{"Data URL is routed correctly", "data:image/png;base64," + validPNGBase64, false},
		{"HTTPS URL is routed correctly", "https://example.com/image.jpg", false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := thread.processImage(test.imagePath)
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
	thread, err := NewAnthropicThread(llmtypes.Config{})
	require.NoError(t, err)

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
	err = os.WriteFile(testImagePath, pngData, 0o644)
	require.NoError(t, err)

	// Create a large test file (exceeds base.MaxImageFileSize)
	largeFilePath := filepath.Join(tempDir, "large.png")
	largeData := make([]byte, base.MaxImageFileSize+1)
	err = os.WriteFile(largeFilePath, largeData, 0o644)
	require.NoError(t, err)

	// Create a file with unsupported extension
	unsupportedPath := filepath.Join(tempDir, "test.bmp")
	err = os.WriteFile(unsupportedPath, pngData, 0o644)
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
	thread, err := NewAnthropicThread(llmtypes.Config{})
	require.NoError(t, err)

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
	err = os.WriteFile(testImagePath, pngData, 0o644)
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
		{"Too many images", "Many images", make([]string, base.MaxImageCount+5), 1 + base.MaxImageCount},       // Should cap at base.MaxImageCount
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

func TestShouldAutoCompact(t *testing.T) {
	tests := []struct {
		name                 string
		compactRatio         float64
		currentContextWindow int
		maxContextWindow     int
		expectedResult       bool
	}{
		{
			name:                 "should compact when ratio exceeded",
			compactRatio:         0.8,
			currentContextWindow: 80,
			maxContextWindow:     100,
			expectedResult:       true,
		},
		{
			name:                 "should not compact when ratio not exceeded",
			compactRatio:         0.8,
			currentContextWindow: 70,
			maxContextWindow:     100,
			expectedResult:       false,
		},
		{
			name:                 "should not compact when ratio is zero",
			compactRatio:         0.0,
			currentContextWindow: 90,
			maxContextWindow:     100,
			expectedResult:       false,
		},
		{
			name:                 "should not compact when ratio is negative",
			compactRatio:         -0.5,
			currentContextWindow: 90,
			maxContextWindow:     100,
			expectedResult:       false,
		},
		{
			name:                 "should not compact when ratio is greater than 1",
			compactRatio:         1.5,
			currentContextWindow: 90,
			maxContextWindow:     100,
			expectedResult:       false,
		},
		{
			name:                 "should not compact when max context window is zero",
			compactRatio:         0.8,
			currentContextWindow: 80,
			maxContextWindow:     0,
			expectedResult:       false,
		},
		{
			name:                 "should compact when ratio is exactly at threshold",
			compactRatio:         0.8,
			currentContextWindow: 80,
			maxContextWindow:     100,
			expectedResult:       true,
		},
		{
			name:                 "should compact when ratio is 1.0 and context is full",
			compactRatio:         1.0,
			currentContextWindow: 100,
			maxContextWindow:     100,
			expectedResult:       true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			thread, err := NewAnthropicThread(llmtypes.Config{})
			require.NoError(t, err)

			// Mock the usage stats
			thread.Usage.CurrentContextWindow = test.currentContextWindow
			thread.Usage.MaxContextWindow = test.maxContextWindow

			result := thread.ShouldAutoCompact(test.compactRatio)
			assert.Equal(t, test.expectedResult, result)
		})
	}
}

func TestCompactContextIntegration(t *testing.T) {
	// Skip if no API key is available
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY not set, skipping integration test")
	}

	t.Run("real compact context with API call", func(t *testing.T) {
		thread, err := NewAnthropicThread(llmtypes.Config{
			Model:     "claude-haiku-4-5-20251001", // Use faster/cheaper model for testing
			MaxTokens: 1000,                        // Limit tokens for test
		})
		require.NoError(t, err)

		// Set up some realistic conversation history
		thread.AddUserMessage(context.Background(), "Help me debug this Python function", []string{}...)
		thread.messages = append(thread.messages, anthropic.MessageParam{
			Role: anthropic.MessageParamRoleAssistant,
			Content: []anthropic.ContentBlockParamUnion{
				anthropic.NewTextBlock("I'd be happy to help you debug your Python function. Could you please share the code?"),
			},
		})
		thread.AddUserMessage(context.Background(), "Here's the function: def add(a, b): return a + b", []string{}...)
		thread.messages = append(thread.messages, anthropic.MessageParam{
			Role: anthropic.MessageParamRoleAssistant,
			Content: []anthropic.ContentBlockParamUnion{
				anthropic.NewTextBlock("Your function looks correct. It's a simple addition function that takes two parameters and returns their sum."),
			},
		})

		// Add some tool results to verify they get cleared
		thread.ToolResults = map[string]tooltypes.StructuredToolResult{
			"tool1": {ToolName: "test_tool", Success: true, Timestamp: time.Now()},
			"tool2": {ToolName: "another_tool", Success: false, Error: "test error", Timestamp: time.Now()},
		}

		// Record initial state
		initialMessageCount := len(thread.messages)
		initialToolResultCount := len(thread.ToolResults)

		// Verify we have multiple messages and tool results
		assert.Greater(t, initialMessageCount, 2, "Should have multiple messages for meaningful test")
		assert.Greater(t, initialToolResultCount, 0, "Should have tool results to verify clearing")

		// Call the real CompactContext method with timeout
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err = thread.CompactContext(ctx)
		require.NoError(t, err, "CompactContext should succeed with real API")

		// Verify the compacting worked
		assert.Equal(t, 1, len(thread.messages), "Should be compacted to single user message")
		assert.Equal(t, 0, len(thread.ToolResults), "Tool results should be cleared")

		// Verify the single remaining message is a user message containing a summary
		if len(thread.messages) > 0 {
			assert.Equal(t, anthropic.MessageParamRoleUser, thread.messages[0].Role)
			assert.Greater(t, len(thread.messages[0].Content), 0, "Compact message should have content")

			// Extract text content and verify it's a reasonable summary
			var messageText string
			for _, block := range thread.messages[0].Content {
				if block.OfText != nil {
					messageText += block.OfText.Text
				}
			}
			assert.Greater(t, len(messageText), 50, "Compact summary should be substantial")
			assert.Contains(t, messageText, "Python", "Summary should mention the context discussed")
		}
	})

	t.Run("compact context preserves thread functionality", func(t *testing.T) {
		// Skip if no API key is available
		if os.Getenv("ANTHROPIC_API_KEY") == "" {
			t.Skip("ANTHROPIC_API_KEY not set, skipping integration test")
		}

		thread, err := NewAnthropicThread(llmtypes.Config{
			Model:     "claude-haiku-4-5-20251001",
			MaxTokens: 500,
		})
		require.NoError(t, err)

		// Add some conversation history
		thread.AddUserMessage(context.Background(), "What is 2+2?", []string{}...)
		thread.messages = append(thread.messages, anthropic.MessageParam{
			Role: anthropic.MessageParamRoleAssistant,
			Content: []anthropic.ContentBlockParamUnion{
				anthropic.NewTextBlock("2+2 equals 4."),
			},
		})

		// Compact the context with timeout
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err = thread.CompactContext(ctx)
		require.NoError(t, err)

		// Verify thread is still functional by sending a new message
		thread.AddUserMessage(context.Background(), "What about 3+3?", []string{}...)

		// Should now have 2 messages: the compact summary + new user message
		assert.Equal(t, 2, len(thread.messages))
		assert.Equal(t, anthropic.MessageParamRoleUser, thread.messages[1].Role)
	})
}

func TestAutoCompactTriggerLogic(t *testing.T) {
	t.Run("auto-compact triggers when ratio exceeded", func(t *testing.T) {
		thread, err := NewAnthropicThread(llmtypes.Config{})
		require.NoError(t, err)

		// Set up context window to trigger auto-compact
		thread.Usage.CurrentContextWindow = 85 // 85% utilization
		thread.Usage.MaxContextWindow = 100

		// Verify ShouldAutoCompact returns true for ratio 0.8
		assert.True(t, thread.ShouldAutoCompact(0.8),
			"Should trigger auto-compact when ratio (0.85) exceeds threshold (0.8)")
	})

	t.Run("auto-compact does not trigger when ratio not exceeded", func(t *testing.T) {
		thread, err := NewAnthropicThread(llmtypes.Config{})
		require.NoError(t, err)

		// Set up context window below auto-compact threshold
		thread.Usage.CurrentContextWindow = 75 // 75% utilization
		thread.Usage.MaxContextWindow = 100

		// Verify ShouldAutoCompact returns false for ratio 0.8
		assert.False(t, thread.ShouldAutoCompact(0.8),
			"Should not trigger auto-compact when ratio (0.75) below threshold (0.8)")
	})

	t.Run("auto-compact disabled when DisableAutoCompact is true", func(t *testing.T) {
		thread, err := NewAnthropicThread(llmtypes.Config{})
		require.NoError(t, err)

		// Set up context window to trigger auto-compact
		thread.Usage.CurrentContextWindow = 90 // 90% utilization
		thread.Usage.MaxContextWindow = 100

		// Even though context is high, ShouldAutoCompact should be bypassed
		// when DisableAutoCompact is true (this is handled in SendMessage logic)
		disableAutoCompact := true

		// Simulate the logic from SendMessage
		shouldTrigger := !disableAutoCompact && thread.ShouldAutoCompact(0.8)
		assert.False(t, shouldTrigger,
			"Should not trigger auto-compact when DisableAutoCompact is true")
	})

	t.Run("auto-compact respects different compact ratios", func(t *testing.T) {
		tests := []struct {
			name          string
			ratio         float64
			utilization   int
			shouldTrigger bool
		}{
			{
				name:          "conservative ratio - should not trigger",
				ratio:         0.9,
				utilization:   85,
				shouldTrigger: false,
			},
			{
				name:          "conservative ratio - should trigger",
				ratio:         0.9,
				utilization:   95,
				shouldTrigger: true,
			},
			{
				name:          "aggressive ratio - should trigger",
				ratio:         0.5,
				utilization:   60,
				shouldTrigger: true,
			},
			{
				name:          "aggressive ratio - should not trigger",
				ratio:         0.5,
				utilization:   40,
				shouldTrigger: false,
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				thread, err := NewAnthropicThread(llmtypes.Config{})
				require.NoError(t, err)

				// Set up context window
				thread.Usage.CurrentContextWindow = test.utilization
				thread.Usage.MaxContextWindow = 100

				result := thread.ShouldAutoCompact(test.ratio)
				assert.Equal(t, test.shouldTrigger, result,
					"Compact ratio %f with %d%% utilization should trigger: %v",
					test.ratio, test.utilization, test.shouldTrigger)
			})
		}
	})
}

func TestNormalizeToolName(t *testing.T) {
	tests := []struct {
		name            string
		useSubscription bool
		toolName        string
		expected        string
	}{
		{
			name:            "subscription mode decapitalizes",
			useSubscription: true,
			toolName:        "File_read",
			expected:        "file_read",
		},
		{
			name:            "subscription mode already lowercase",
			useSubscription: true,
			toolName:        "file_read",
			expected:        "file_read",
		},
		{
			name:            "subscription mode empty string",
			useSubscription: true,
			toolName:        "",
			expected:        "",
		},
		{
			name:            "non-subscription mode normal name",
			useSubscription: false,
			toolName:        "file_read",
			expected:        "file_read",
		},
		{
			name:            "non-subscription mode capitalized input preserved",
			useSubscription: false,
			toolName:        "File_read",
			expected:        "File_read",
		},
		{
			name:            "non-subscription mode empty string",
			useSubscription: false,
			toolName:        "",
			expected:        "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			thread := &Thread{useSubscription: tt.useSubscription}
			result := thread.normalizeToolName(tt.toolName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCapitalizeToolName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "lowercase first letter",
			input:    "file_read",
			expected: "File_read",
		},
		{
			name:     "already capitalized",
			input:    "File_read",
			expected: "File_read",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "single character",
			input:    "a",
			expected: "A",
		},
		{
			name:     "single uppercase character",
			input:    "A",
			expected: "A",
		},
		{
			name:     "underscore first",
			input:    "_test",
			expected: "_test",
		},
		{
			name:     "unicode character",
			input:    "über_tool",
			expected: "Über_tool",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := capitalizeToolName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDecapitalizeToolName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "uppercase first letter",
			input:    "File_read",
			expected: "file_read",
		},
		{
			name:     "already lowercase",
			input:    "file_read",
			expected: "file_read",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "single character",
			input:    "A",
			expected: "a",
		},
		{
			name:     "single lowercase character",
			input:    "a",
			expected: "a",
		},
		{
			name:     "underscore first",
			input:    "_Test",
			expected: "_Test",
		},
		{
			name:     "unicode character",
			input:    "Über_tool",
			expected: "über_tool",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := decapitalizeToolName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestToAnthropicTools(t *testing.T) {
	t.Run("with subscription", func(t *testing.T) {
		tool := testTool{name: "file_read"}
		tools := toAnthropicTools([]tooltypes.Tool{tool}, true)
		require.Len(t, tools, 1)
		require.NotNil(t, tools[0].OfTool)
		assert.Equal(t, "File_read", tools[0].OfTool.Name)
	})

	t.Run("without subscription", func(t *testing.T) {
		tool := testTool{name: "file_read"}
		tools := toAnthropicTools([]tooltypes.Tool{tool}, false)
		require.Len(t, tools, 1)
		require.NotNil(t, tools[0].OfTool)
		assert.Equal(t, "file_read", tools[0].OfTool.Name)
	})

	t.Run("empty tools slice", func(t *testing.T) {
		tools := toAnthropicTools([]tooltypes.Tool{}, true)
		require.Len(t, tools, 0)
	})

	t.Run("multiple tools", func(t *testing.T) {
		toolList := []tooltypes.Tool{
			testTool{name: "file_read"},
			testTool{name: "bash"},
			testTool{name: "grep_tool"},
		}
		tools := toAnthropicTools(toolList, true)
		require.Len(t, tools, 3)
		assert.Equal(t, "File_read", tools[0].OfTool.Name)
		assert.Equal(t, "Bash", tools[1].OfTool.Name)
		assert.Equal(t, "Grep_tool", tools[2].OfTool.Name)
	})
}

type testTool struct {
	name string
}

func (t testTool) GenerateSchema() *jsonschema.Schema {
	return &jsonschema.Schema{}
}

func (t testTool) Name() string {
	return t.name
}

func (t testTool) Description() string {
	return "test"
}

func (t testTool) ValidateInput(_ tooltypes.State, _ string) error {
	return nil
}

func (t testTool) Execute(_ context.Context, _ tooltypes.State, _ string) tooltypes.ToolResult {
	return tooltypes.BaseToolResult{}
}

func (t testTool) TracingKVs(_ string) ([]attribute.KeyValue, error) {
	return nil, nil
}
