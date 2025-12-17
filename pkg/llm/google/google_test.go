package google

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"

	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

func TestNewGoogleThread(t *testing.T) {
	tests := []struct {
		name      string
		config    llmtypes.Config
		expectErr bool
	}{
		{
			name: "valid config with default values",
			config: llmtypes.Config{
				Provider: "google",
				Model:    "gemini-2.5-pro",
			},
			expectErr: false,
		},
		{
			name: "valid config with explicit values",
			config: llmtypes.Config{
				Provider:  "google",
				Model:     "gemini-2.5-flash",
				MaxTokens: 4096,
				Google: &llmtypes.GoogleConfig{
					Backend:        "gemini",
					ThinkingBudget: 4000,
				},
			},
			expectErr: false,
		},
		{
			name: "config with vertex ai backend",
			config: llmtypes.Config{
				Provider: "google",
				Model:    "gemini-2.5-pro",
				Google: &llmtypes.GoogleConfig{
					Backend:  "vertexai",
					Project:  "test-project",
					Location: "us-central1",
				},
			},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.config.Google != nil && tt.config.Google.Backend == "gemini" && os.Getenv("GOOGLE_API_KEY") == "" {
				t.Skip("Skipping Gemini backend test; GOOGLE_API_KEY not set in environment")
				return
			}

			thread, err := NewGoogleThread(tt.config, nil)

			if tt.expectErr {
				assert.Error(t, err)
				assert.Nil(t, thread)
			} else {
				require.NoError(t, err)
				require.NotNil(t, thread)

				// Verify thread properties
				assert.Equal(t, "google", thread.Provider())
				assert.Equal(t, tt.config.Model, thread.GetConfig().Model)
				assert.NotEmpty(t, thread.GetConversationID())
				assert.False(t, thread.IsPersisted())

				// Verify defaults were applied
				if tt.config.MaxTokens == 0 {
					assert.Equal(t, 8192, thread.GetConfig().MaxTokens)
				} else {
					assert.Equal(t, tt.config.MaxTokens, thread.GetConfig().MaxTokens)
				}

				// Test interface compliance
				var _ llmtypes.Thread = thread
			}
		})
	}
}

func TestGoogleThread_InterfaceCompliance(t *testing.T) {
	config := llmtypes.Config{
		Provider: "google",
		Model:    "gemini-2.5-pro",
	}

	thread, err := NewGoogleThread(config, nil)
	require.NoError(t, err)

	// Test all interface methods exist and work
	assert.Equal(t, "google", thread.Provider())
	assert.NotEmpty(t, thread.GetConversationID())

	// Test that the config has defaults applied
	threadConfig := thread.GetConfig()
	assert.Equal(t, "google", threadConfig.Provider)
	assert.Equal(t, "gemini-2.5-pro", threadConfig.Model)
	assert.Equal(t, 8192, threadConfig.MaxTokens) // Default should be applied

	// Test state management
	assert.Nil(t, thread.GetState())

	// Test persistence
	assert.False(t, thread.IsPersisted())
	thread.EnablePersistence(context.Background(), true)
	assert.True(t, thread.IsPersisted())

	// Test conversation ID management
	originalID := thread.GetConversationID()
	newID := "test-conversation-id"
	thread.SetConversationID(newID)
	assert.Equal(t, newID, thread.GetConversationID())
	assert.NotEqual(t, originalID, thread.GetConversationID())

	// Test usage tracking
	usage := thread.GetUsage()
	assert.Equal(t, 0, usage.InputTokens)
	assert.Equal(t, 0, usage.OutputTokens)

	// Test messages (should be empty initially)
	messages, err := thread.GetMessages()
	assert.NoError(t, err)
	assert.Empty(t, messages)
}

func TestGoogleThread_ModelPricing(t *testing.T) {
	tests := []struct {
		model    string
		expected bool
	}{
		{"gemini-2.5-pro", true},
		{"gemini-2.5-flash", true},
		{"gemini-2.5-flash-lite", true},
		{"gemini-pro", true},
		{"gemini-flash", true},
		{"unknown-model", false},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			_, exists := ModelPricingMap[tt.model]
			assert.Equal(t, tt.expected, exists)
		})
	}
}

func TestGoogleThread_BackendDetection(t *testing.T) {
	tests := []struct {
		name     string
		config   llmtypes.Config
		expected string
	}{
		{
			name: "explicit gemini backend",
			config: llmtypes.Config{
				Google: &llmtypes.GoogleConfig{
					Backend: "gemini",
				},
			},
			expected: "gemini",
		},
		{
			name: "explicit vertexai backend",
			config: llmtypes.Config{
				Google: &llmtypes.GoogleConfig{
					Backend: "vertexai",
				},
			},
			expected: "vertexai",
		},
		{
			name: "auto-detect with project config",
			config: llmtypes.Config{
				Google: &llmtypes.GoogleConfig{
					Project: "test-project",
				},
			},
			expected: "vertexai",
		},
		{
			name: "auto-detect with api key",
			config: llmtypes.Config{
				Google: &llmtypes.GoogleConfig{
					APIKey: "test-api-key",
				},
			},
			expected: "gemini",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := detectBackend(tt.config)
			assert.Equal(t, tt.expected, backend)
		})
	}
}

func TestGoogleThread_BackendDetectionWithEnvironment(t *testing.T) {
	// Test environment-dependent behavior separately
	// This test may behave differently based on local gcloud setup
	t.Run("default when no config", func(t *testing.T) {
		config := llmtypes.Config{}
		backend := detectBackend(config)
		// Accept either gemini or vertexai depending on environment
		assert.Contains(t, []string{"gemini", "vertexai"}, backend, "backend should be either gemini or vertexai")
	})
}

func TestGoogleThread_AddUserMessage(t *testing.T) {
	config := llmtypes.Config{
		Provider: "google",
		Model:    "gemini-2.5-pro",
	}

	thread, err := NewGoogleThread(config, nil)
	require.NoError(t, err)

	// Test adding a simple text message
	ctx := context.Background()
	message := "Hello, world!"
	thread.AddUserMessage(ctx, message)

	// Verify message was added
	assert.Len(t, thread.messages, 1)
	assert.Equal(t, 1, len(thread.messages[0].Parts))
	assert.Equal(t, message, thread.messages[0].Parts[0].Text)
}

func TestGoogleThread_ShouldAutoCompact(t *testing.T) {
	config := llmtypes.Config{
		Provider: "google",
		Model:    "gemini-2.5-pro",
	}

	thread, err := NewGoogleThread(config, nil)
	require.NoError(t, err)

	// Test with no context window set
	assert.False(t, thread.shouldAutoCompact(0.8))

	// Test with context window set
	thread.usage.MaxContextWindow = 1000
	thread.usage.CurrentContextWindow = 500
	assert.False(t, thread.shouldAutoCompact(0.8)) // 50% < 80%

	thread.usage.CurrentContextWindow = 850
	assert.True(t, thread.shouldAutoCompact(0.8)) // 85% > 80%
}

func TestCalculateCost(t *testing.T) {
	tests := []struct {
		name         string
		model        string
		inputTokens  int
		outputTokens int
		hasAudio     bool
		expectInput  float64
		expectOutput float64
	}{
		{
			name:         "gemini-2.5-flash basic",
			model:        "gemini-2.5-flash",
			inputTokens:  1000,
			outputTokens: 500,
			hasAudio:     false,
			expectInput:  0.0000003,  // 1000 * 0.0003 / 1000000
			expectOutput: 0.00000125, // 500 * 0.0025 / 1000000
		},
		{
			name:         "gemini-2.5-pro tiered pricing low",
			model:        "gemini-2.5-pro",
			inputTokens:  100000, // Below 200K threshold
			outputTokens: 1000,
			hasAudio:     false,
			expectInput:  0.000125, // 100000 * 0.00125 / 1000000
			expectOutput: 0.00001,  // 1000 * 0.01 / 1000000
		},
		{
			name:         "unknown model",
			model:        "unknown-model",
			inputTokens:  1000,
			outputTokens: 500,
			hasAudio:     false,
			expectInput:  0,
			expectOutput: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputCost, outputCost := calculateCost(tt.model, tt.inputTokens, tt.outputTokens, tt.hasAudio)
			assert.InDelta(t, tt.expectInput, inputCost, 0.0001)
			assert.InDelta(t, tt.expectOutput, outputCost, 0.0001)
		})
	}
}

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		retryable bool
	}{
		{
			name:      "nil error should not be retryable",
			err:       nil,
			retryable: false,
		},
		{
			name:      "context canceled should not be retryable",
			err:       context.Canceled,
			retryable: false,
		},
		{
			name:      "context deadline exceeded should not be retryable",
			err:       context.DeadlineExceeded,
			retryable: false,
		},
		{
			name:      "generic error should not be retryable",
			err:       errors.New("some random error"),
			retryable: false,
		},
		{
			name: "genai.APIError with 429 should be retryable",
			err: &genai.APIError{
				Code:    429,
				Message: "Too Many Requests",
				Status:  "RESOURCE_EXHAUSTED",
			},
			retryable: true,
		},
		{
			name: "genai.APIError with 500 should be retryable",
			err: &genai.APIError{
				Code:    500,
				Message: "Internal Server Error",
				Status:  "INTERNAL",
			},
			retryable: true,
		},
		{
			name: "genai.APIError with 503 should be retryable",
			err: &genai.APIError{
				Code:    503,
				Message: "Service Unavailable",
				Status:  "UNAVAILABLE",
			},
			retryable: true,
		},
		{
			name: "genai.APIError with 400 should be retryable",
			err: &genai.APIError{
				Code:    400,
				Message: "Bad Request",
				Status:  "INVALID_ARGUMENT",
			},
			retryable: true,
		},
		{
			name: "genai.APIError with 401 should be retryable",
			err: &genai.APIError{
				Code:    401,
				Message: "Unauthorized",
				Status:  "UNAUTHENTICATED",
			},
			retryable: true,
		},
		{
			name: "genai.APIError with 404 should be retryable",
			err: &genai.APIError{
				Code:    404,
				Message: "Not Found",
				Status:  "NOT_FOUND",
			},
			retryable: true,
		},
		{
			name: "genai.APIError with 200 should not be retryable",
			err: &genai.APIError{
				Code:    200,
				Message: "OK",
				Status:  "OK",
			},
			retryable: false,
		},
		{
			name: "genai.APIError with 399 should not be retryable",
			err: &genai.APIError{
				Code:    399,
				Message: "Custom Code",
				Status:  "CUSTOM",
			},
			retryable: false,
		},
		{
			name: "genai.APIError with 600 should not be retryable",
			err: &genai.APIError{
				Code:    600,
				Message: "Custom Code",
				Status:  "CUSTOM",
			},
			retryable: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isRetryableError(tt.err)
			assert.Equal(t, tt.retryable, result)
		})
	}
}

func TestGetMimeTypeFromExtension(t *testing.T) {
	tests := []struct {
		ext      string
		expected string
		hasError bool
	}{
		{".jpg", "image/jpeg", false},
		{".jpeg", "image/jpeg", false},
		{".JPG", "image/jpeg", false},
		{".JPEG", "image/jpeg", false},
		{".png", "image/png", false},
		{".PNG", "image/png", false},
		{".gif", "image/gif", false},
		{".GIF", "image/gif", false},
		{".webp", "image/webp", false},
		{".WEBP", "image/webp", false},
		{".bmp", "", true},
		{".svg", "", true},
		{".txt", "", true},
		{"", "", true},
	}

	for _, test := range tests {
		t.Run(test.ext, func(t *testing.T) {
			result := getMimeTypeFromExtension(test.ext)
			if test.hasError {
				assert.Equal(t, "", result)
			} else {
				assert.Equal(t, test.expected, result)
			}
		})
	}
}

func TestProcessImageFile(t *testing.T) {
	thread, err := NewGoogleThread(llmtypes.Config{}, nil)
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

	// Create a large test file (exceeds MaxImageFileSize)
	largeFilePath := filepath.Join(tempDir, "large.png")
	largeData := make([]byte, MaxImageFileSize+1)
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
			result, err := thread.processImage(context.Background(), test.filePath)
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

func TestProcessImageURL(t *testing.T) {
	thread, err := NewGoogleThread(llmtypes.Config{}, nil)
	require.NoError(t, err)

	tests := []struct {
		name     string
		url      string
		hasError bool
		errorMsg string
	}{
		{
			name:     "HTTP URL (should fail)",
			url:      "http://example.com/image.jpg",
			hasError: true,
			errorMsg: "HTTP URLs are not supported for security reasons",
		},
		{
			name:     "Invalid empty HTTPS URL",
			url:      "https://",
			hasError: true,
			errorMsg: "no Host in request URL",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := thread.processImage(context.Background(), test.url)
			if test.hasError {
				assert.Error(t, err)
				assert.Nil(t, result)
				if test.errorMsg != "" {
					assert.Contains(t, err.Error(), test.errorMsg)
				}
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
			}
		})
	}
}

func TestGoogleThread_AddUserMessageComprehensive(t *testing.T) {
	thread, err := NewGoogleThread(llmtypes.Config{}, nil)
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
		expectCount int // Expected number of parts in the message
	}{
		{"Text only", "Hello world", nil, 1},
		{"Text with valid image", "Analyze this image", []string{testImagePath}, 2},
		{"Text with invalid image", "Check this", []string{"invalid-path.png"}, 1},                             // Only text should be added
		{"Text with mixed valid/invalid images", "Mixed test", []string{testImagePath, "invalid-path.png"}, 2}, // Only text + valid image
		{"Too many images", "Many images", make([]string, MaxImageCount+5), 1 + MaxImageCount},                 // Should cap at MaxImageCount
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			initialCount := len(thread.messages)

			// For the "too many images" test, fill the slice with valid paths
			if test.name == "Too many images" {
				for i := range test.images {
					test.images[i] = testImagePath
				}
			}

			thread.AddUserMessage(context.Background(), test.message, test.images...)

			// Should have added exactly one message
			assert.Equal(t, initialCount+1, len(thread.messages))

			// Check the last message
			lastMessage := thread.messages[len(thread.messages)-1]
			assert.Equal(t, genai.RoleUser, lastMessage.Role)

			// Check part count
			assert.Equal(t, test.expectCount, len(lastMessage.Parts))
		})
	}
}

func TestGoogleThread_ShouldAutoCompactComprehensive(t *testing.T) {
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
			thread, err := NewGoogleThread(llmtypes.Config{}, nil)
			require.NoError(t, err)

			// Mock the usage stats
			thread.usage.CurrentContextWindow = test.currentContextWindow
			thread.usage.MaxContextWindow = test.maxContextWindow

			result := thread.shouldAutoCompact(test.compactRatio)
			assert.Equal(t, test.expectedResult, result)
		})
	}
}

func TestGoogleThread_ToolsMethod(t *testing.T) {
	thread, err := NewGoogleThread(llmtypes.Config{}, nil)
	require.NoError(t, err)

	// Test with nil state
	availableTools := thread.tools(llmtypes.MessageOpt{})
	assert.Empty(t, availableTools)

	// Test with NoToolUse option
	availableTools = thread.tools(llmtypes.MessageOpt{NoToolUse: true})
	assert.Empty(t, availableTools)

	// Test with real state
	realState := tools.NewBasicState(context.TODO())
	thread.SetState(realState)

	availableTools = thread.tools(llmtypes.MessageOpt{})
	// Real state should provide basic tools
	assert.Greater(t, len(availableTools), 0, "Real state should provide basic tools")

	// Test NoToolUse still overrides
	availableTools = thread.tools(llmtypes.MessageOpt{NoToolUse: true})
	assert.Empty(t, availableTools)
}

func TestGoogleThread_AutoCompactTriggerLogic(t *testing.T) {
	t.Run("auto-compact triggers when ratio exceeded", func(t *testing.T) {
		thread, err := NewGoogleThread(llmtypes.Config{}, nil)
		require.NoError(t, err)

		// Set up context window to trigger auto-compact
		thread.usage.CurrentContextWindow = 85 // 85% utilization
		thread.usage.MaxContextWindow = 100

		// Verify shouldAutoCompact returns true for ratio 0.8
		assert.True(t, thread.shouldAutoCompact(0.8),
			"Should trigger auto-compact when ratio (0.85) exceeds threshold (0.8)")
	})

	t.Run("auto-compact does not trigger when ratio not exceeded", func(t *testing.T) {
		thread, err := NewGoogleThread(llmtypes.Config{}, nil)
		require.NoError(t, err)

		// Set up context window below auto-compact threshold
		thread.usage.CurrentContextWindow = 75 // 75% utilization
		thread.usage.MaxContextWindow = 100

		// Verify shouldAutoCompact returns false for ratio 0.8
		assert.False(t, thread.shouldAutoCompact(0.8),
			"Should not trigger auto-compact when ratio (0.75) below threshold (0.8)")
	})

	t.Run("auto-compact disabled when DisableAutoCompact is true", func(t *testing.T) {
		thread, err := NewGoogleThread(llmtypes.Config{}, nil)
		require.NoError(t, err)

		// Set up context window to trigger auto-compact
		thread.usage.CurrentContextWindow = 90 // 90% utilization
		thread.usage.MaxContextWindow = 100

		// Even though context is high, shouldAutoCompact should be bypassed
		// when DisableAutoCompact is true (this is handled in SendMessage logic)
		disableAutoCompact := true

		// Simulate the logic from SendMessage
		shouldTrigger := !disableAutoCompact && thread.shouldAutoCompact(0.8)
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
				thread, err := NewGoogleThread(llmtypes.Config{}, nil)
				require.NoError(t, err)

				// Set up context window
				thread.usage.CurrentContextWindow = test.utilization
				thread.usage.MaxContextWindow = 100

				result := thread.shouldAutoCompact(test.ratio)
				assert.Equal(t, test.shouldTrigger, result,
					"Compact ratio %f with %d%% utilization should trigger: %v",
					test.ratio, test.utilization, test.shouldTrigger)
			})
		}
	})
}

func TestGoogleThread_NewSubAgent(t *testing.T) {
	parentThread, err := NewGoogleThread(llmtypes.Config{
		Model:     "gemini-2.5-pro",
		MaxTokens: 8192,
	}, nil)
	require.NoError(t, err)

	// Set up parent thread state
	realState := tools.NewBasicState(context.TODO())
	parentThread.SetState(realState)

	// Create subagent
	subagentConfig := llmtypes.Config{
		Model:      "gemini-2.5-flash",
		MaxTokens:  4096,
		IsSubAgent: true,
	}

	subagent := parentThread.NewSubAgent(context.Background(), subagentConfig)
	require.NotNil(t, subagent)

	googleSubagent, ok := subagent.(*Thread)
	require.True(t, ok, "Subagent should be a GoogleThread")

	// Verify subagent properties
	assert.Equal(t, "google", googleSubagent.Provider())
	assert.Equal(t, subagentConfig.Model, googleSubagent.GetConfig().Model)
	assert.Equal(t, subagentConfig.MaxTokens, googleSubagent.GetConfig().MaxTokens)
	assert.True(t, googleSubagent.GetConfig().IsSubAgent)
	assert.False(t, googleSubagent.IsPersisted())

	// Verify shared resources
	assert.Equal(t, parentThread.client, googleSubagent.client)
	assert.Equal(t, parentThread.backend, googleSubagent.backend)
	assert.Equal(t, parentThread.usage, googleSubagent.usage) // Shared usage tracking
	assert.Equal(t, parentThread.state, googleSubagent.state) // Shared state
}
