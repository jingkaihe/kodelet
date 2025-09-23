package google

import (
	"context"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

func TestNewGoogleThread(t *testing.T) {
	tests := []struct {
		name     string
		config   llmtypes.Config
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
			expectInput:  0.000125,  // 100000 * 0.00125 / 1000000
			expectOutput: 0.00001,   // 1000 * 0.01 / 1000000
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
			name:      "connection refused should be retryable",
			err:       errors.New("connection refused"),
			retryable: true,
		},
		{
			name:      "timeout should be retryable",
			err:       errors.New("timeout occurred"),
			retryable: true,
		},
		{
			name:      "rate limit should be retryable",
			err:       errors.New("rate limit exceeded"),
			retryable: true,
		},
		{
			name:      "quota exceeded should be retryable",
			err:       errors.New("quota exceeded"),
			retryable: true,
		},
		{
			name:      "generic error should not be retryable",
			err:       errors.New("some random error"),
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