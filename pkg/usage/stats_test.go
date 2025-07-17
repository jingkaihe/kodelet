package usage

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jingkaihe/kodelet/pkg/logger"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// testSetup creates a test logger setup and returns the buffer, context, and a helper to parse log entries
type testSetup struct {
	buf *bytes.Buffer
	ctx context.Context
}

// setupTestLogger creates a standardized test logger setup
func setupTestLogger(logLevel logrus.Level) *testSetup {
	var buf bytes.Buffer
	testLogger := logrus.New()
	testLogger.SetOutput(&buf)
	testLogger.SetLevel(logLevel)
	testLogger.Formatter = &logrus.JSONFormatter{
		TimestampFormat: time.RFC3339Nano,
	}

	entry := logrus.NewEntry(testLogger)
	ctx := logger.WithLogger(context.Background(), entry)

	return &testSetup{
		buf: &buf,
		ctx: ctx,
	}
}

// parseLogEntry parses the JSON log entry from the buffer
func (ts *testSetup) parseLogEntry(t *testing.T) map[string]interface{} {
	var logEntry map[string]interface{}
	err := json.Unmarshal(ts.buf.Bytes(), &logEntry)
	require.NoError(t, err)
	return logEntry
}

// assertLogMessage verifies the basic log message properties
func (ts *testSetup) assertLogMessage(t *testing.T, logEntry map[string]interface{}) {
	assert.Equal(t, "LLM usage completed", logEntry["msg"])
	assert.Equal(t, "info", logEntry["level"])
}

func TestLogLLMUsage_NormalCase(t *testing.T) {
	ts := setupTestLogger(logrus.InfoLevel)

	// Create test usage data
	usage := llmtypes.Usage{
		InputTokens:              1000,
		OutputTokens:             500,
		CacheCreationInputTokens: 200,
		CacheReadInputTokens:     300,
		InputCost:                0.001,
		OutputCost:               0.002,
		CacheCreationCost:        0.0001,
		CacheReadCost:            0.0002,
		CurrentContextWindow:     2000,
		MaxContextWindow:         8000,
	}

	model := "claude-sonnet-4-20250514"
	startTime := time.Now().Add(-2 * time.Second) // 2 seconds ago
	requestOutputTokens := 150

	// Call the function
	LogLLMUsage(ts.ctx, usage, model, startTime, requestOutputTokens)

	// Parse and verify the log entry
	logEntry := ts.parseLogEntry(t)
	ts.assertLogMessage(t, logEntry)

	// Verify all expected fields are present and correct
	assert.Equal(t, model, logEntry["model"])
	assert.Equal(t, float64(1000), logEntry["input_tokens"])
	assert.Equal(t, float64(500), logEntry["output_tokens"])
	assert.Equal(t, float64(200), logEntry["cache_creation_input_tokens"])
	assert.Equal(t, float64(300), logEntry["cache_read_input_tokens"])
	assert.Equal(t, 0.001, logEntry["input_cost"])
	assert.Equal(t, 0.002, logEntry["output_cost"])
	assert.Equal(t, 0.0001, logEntry["cache_creation_cost"])
	assert.Equal(t, 0.0002, logEntry["cache_read_cost"])
	assert.Equal(t, 0.0033, logEntry["total_cost"])          // 0.001 + 0.002 + 0.0001 + 0.0002
	assert.Equal(t, float64(2000), logEntry["total_tokens"]) // 1000 + 500 + 200 + 300
	assert.Equal(t, float64(2000), logEntry["current_context_window"])
	assert.Equal(t, float64(8000), logEntry["max_context_window"])

	// Verify context window usage ratio
	assert.Equal(t, 0.25, logEntry["context_window_usage_ratio"]) // 2000/8000 = 0.25

	// Verify tokens per second calculation (should be around 75 tokens/s for 150 tokens in ~2s)
	tokensPerSecond, ok := logEntry["output_tokens/s"].(float64)
	require.True(t, ok)
	assert.Greater(t, tokensPerSecond, 50.0) // Should be around 75
	assert.Less(t, tokensPerSecond, 100.0)   // Should be around 75
}

func TestLogLLMUsage_ZeroMaxContextWindow(t *testing.T) {
	ts := setupTestLogger(logrus.InfoLevel)

	// Create usage with zero max context window
	usage := llmtypes.Usage{
		InputTokens:          100,
		OutputTokens:         50,
		CurrentContextWindow: 150,
		MaxContextWindow:     0, // Zero max context window
	}

	LogLLMUsage(ts.ctx, usage, "test-model", time.Now().Add(-1*time.Second), 50)

	logEntry := ts.parseLogEntry(t)

	// Verify context_window_usage_ratio is NOT present when max context window is zero
	_, exists := logEntry["context_window_usage_ratio"]
	assert.False(t, exists, "context_window_usage_ratio should not be calculated when max_context_window is 0")

	// Verify other fields are still present
	assert.Equal(t, float64(150), logEntry["current_context_window"])
	assert.Equal(t, float64(0), logEntry["max_context_window"])
}

func TestLogLLMUsage_ZeroDuration(t *testing.T) {
	ts := setupTestLogger(logrus.InfoLevel)

	usage := llmtypes.Usage{
		InputTokens:  100,
		OutputTokens: 50,
	}

	// Use future time (negative duration)
	LogLLMUsage(ts.ctx, usage, "test-model", time.Now().Add(1*time.Second), 50)

	logEntry := ts.parseLogEntry(t)

	// Verify output_tokens/s is NOT present when duration is negative/zero
	_, exists := logEntry["output_tokens/s"]
	assert.False(t, exists, "output_tokens/s should not be calculated when duration is zero or negative")
}

func TestLogLLMUsage_ZeroRequestOutputTokens(t *testing.T) {
	ts := setupTestLogger(logrus.InfoLevel)

	usage := llmtypes.Usage{
		InputTokens:  100,
		OutputTokens: 50,
	}

	// Zero request output tokens
	LogLLMUsage(ts.ctx, usage, "test-model", time.Now().Add(-1*time.Second), 0)

	logEntry := ts.parseLogEntry(t)

	// Verify output_tokens/s is NOT present when request output tokens is zero
	_, exists := logEntry["output_tokens/s"]
	assert.False(t, exists, "output_tokens/s should not be calculated when requestOutputTokens is 0")
}

func TestLogLLMUsage_DifferentModels(t *testing.T) {
	testCases := []struct {
		name  string
		model string
	}{
		{"Anthropic Claude", "claude-sonnet-4-20250514"},
		{"OpenAI GPT", "gpt-4.1"},
		{"Custom Model", "custom-model-v1"},
		{"Empty Model", ""},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ts := setupTestLogger(logrus.InfoLevel)

			usage := llmtypes.Usage{InputTokens: 100, OutputTokens: 50}
			LogLLMUsage(ts.ctx, usage, tc.model, time.Now().Add(-1*time.Second), 50)

			logEntry := ts.parseLogEntry(t)
			assert.Equal(t, tc.model, logEntry["model"])
		})
	}
}

func TestLogLLMUsage_AllFieldsPresent(t *testing.T) {
	ts := setupTestLogger(logrus.InfoLevel)

	usage := llmtypes.Usage{
		InputTokens:              100,
		OutputTokens:             50,
		CacheCreationInputTokens: 25,
		CacheReadInputTokens:     10,
		InputCost:                0.001,
		OutputCost:               0.002,
		CacheCreationCost:        0.0001,
		CacheReadCost:            0.0002,
		CurrentContextWindow:     185,
		MaxContextWindow:         1000,
	}

	LogLLMUsage(ts.ctx, usage, "test-model", time.Now().Add(-1*time.Second), 50)

	logEntry := ts.parseLogEntry(t)

	// Verify all required fields are present
	requiredFields := []string{
		"model", "input_tokens", "output_tokens", "cache_creation_input_tokens",
		"cache_read_input_tokens", "input_cost", "output_cost", "cache_creation_cost",
		"cache_read_cost", "total_cost", "total_tokens", "current_context_window",
		"max_context_window", "context_window_usage_ratio", "output_tokens/s",
	}

	for _, field := range requiredFields {
		assert.Contains(t, logEntry, field, "Field %s should be present in log entry", field)
	}

	// Verify calculated fields
	assert.Equal(t, 0.0033, logEntry["total_cost"])                // Sum of all costs
	assert.Equal(t, float64(185), logEntry["total_tokens"])        // Sum of all tokens
	assert.Equal(t, 0.185, logEntry["context_window_usage_ratio"]) // 185/1000
}

func TestLogLLMUsage_LargeNumbers(t *testing.T) {
	ts := setupTestLogger(logrus.InfoLevel)

	// Test with large numbers to ensure no overflow or precision issues
	usage := llmtypes.Usage{
		InputTokens:          1000000,
		OutputTokens:         500000,
		CurrentContextWindow: 1500000,
		MaxContextWindow:     2000000,
		InputCost:            1.5,
		OutputCost:           2.5,
	}

	LogLLMUsage(ts.ctx, usage, "large-model", time.Now().Add(-1*time.Second), 500000)

	logEntry := ts.parseLogEntry(t)

	assert.Equal(t, float64(1000000), logEntry["input_tokens"])
	assert.Equal(t, float64(500000), logEntry["output_tokens"])
	assert.Equal(t, 4.0, logEntry["total_cost"])                  // 1.5 + 2.5
	assert.Equal(t, 0.75, logEntry["context_window_usage_ratio"]) // 1500000/2000000

	// Verify tokens per second is reasonable for large numbers
	tokensPerSecond, ok := logEntry["output_tokens/s"].(float64)
	require.True(t, ok)
	assert.Greater(t, tokensPerSecond, 100000.0) // Should be around 500k tokens/s
}

func TestLogLLMUsage_PrecisionValidation(t *testing.T) {
	ts := setupTestLogger(logrus.InfoLevel)

	// Test precise calculations with fractional costs
	usage := llmtypes.Usage{
		InputTokens:              100,
		OutputTokens:             75,
		CacheCreationInputTokens: 50,
		CacheReadInputTokens:     25,
		InputCost:                0.0012345,
		OutputCost:               0.0067890,
		CacheCreationCost:        0.0001111,
		CacheReadCost:            0.0002222,
		CurrentContextWindow:     250,
		MaxContextWindow:         3000,
	}

	startTime := time.Now().Add(-500 * time.Millisecond) // 0.5 seconds
	LogLLMUsage(ts.ctx, usage, "precision-model", startTime, 75)

	logEntry := ts.parseLogEntry(t)

	// Verify precise total cost calculation
	expectedTotalCost := 0.0012345 + 0.0067890 + 0.0001111 + 0.0002222
	assert.InDelta(t, expectedTotalCost, logEntry["total_cost"], 0.0000001)

	// Verify precise context window ratio
	expectedRatio := float64(250) / float64(3000)
	assert.InDelta(t, expectedRatio, logEntry["context_window_usage_ratio"], 0.0000001)

	// Verify tokens per second (75 tokens in 0.5 seconds = 150 tokens/s)
	tokensPerSecond, ok := logEntry["output_tokens/s"].(float64)
	require.True(t, ok)
	assert.InDelta(t, 150.0, tokensPerSecond, 10.0) // Allow some variance due to timing
}

func TestLogLLMUsage_LogLevel(t *testing.T) {
	// Test with Warn level - should filter out Info messages
	tsWarn := setupTestLogger(logrus.WarnLevel)

	usage := llmtypes.Usage{InputTokens: 100, OutputTokens: 50}
	LogLLMUsage(tsWarn.ctx, usage, "test-model", time.Now().Add(-1*time.Second), 50)

	// With log level set to Warn, Info messages should be filtered out
	output := strings.TrimSpace(tsWarn.buf.String())
	assert.Empty(t, output, "No log output should be generated when log level is higher than Info")

	// Test with Info level enabled - should generate output
	tsInfo := setupTestLogger(logrus.InfoLevel)
	LogLLMUsage(tsInfo.ctx, usage, "test-model", time.Now().Add(-1*time.Second), 50)

	output = strings.TrimSpace(tsInfo.buf.String())
	assert.NotEmpty(t, output, "Log output should be generated when log level includes Info")
}
