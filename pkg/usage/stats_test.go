package usage

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jingkaihe/kodelet/pkg/logger"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

type testSetup struct {
	buf *bytes.Buffer
	ctx context.Context
}

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

func (ts *testSetup) parseLogEntry(t *testing.T) map[string]any {
	var logEntry map[string]any
	err := json.Unmarshal(ts.buf.Bytes(), &logEntry)
	require.NoError(t, err)
	return logEntry
}

func (ts *testSetup) assertLogMessage(t *testing.T, logEntry map[string]any) {
	assert.Equal(t, "Turn completed", logEntry["msg"])
	assert.Equal(t, "info", logEntry["level"])
}

type testConversationSummary struct {
	id           string
	createdAt    time.Time
	updatedAt    time.Time
	messageCount int
	usage        llmtypes.Usage
	provider     string
}

func (s testConversationSummary) GetID() string            { return s.id }
func (s testConversationSummary) GetCreatedAt() time.Time  { return s.createdAt }
func (s testConversationSummary) GetUpdatedAt() time.Time  { return s.updatedAt }
func (s testConversationSummary) GetMessageCount() int     { return s.messageCount }
func (s testConversationSummary) GetUsage() llmtypes.Usage { return s.usage }
func (s testConversationSummary) GetProvider() string      { return s.provider }
func testUsage(input, output, cacheWrite, cacheRead int) llmtypes.Usage {
	return llmtypes.Usage{
		InputTokens:              input,
		OutputTokens:             output,
		CacheCreationInputTokens: cacheWrite,
		CacheReadInputTokens:     cacheRead,
		InputCost:                float64(input) / 1000,
		OutputCost:               float64(output) / 1000,
		CacheCreationCost:        float64(cacheWrite) / 1000,
		CacheReadCost:            float64(cacheRead) / 1000,
	}
}

func TestCalculateUsageStatsAggregatesFiltersAndSortsByDay(t *testing.T) {
	base := time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC)
	summaries := []ConversationSummary{
		testConversationSummary{id: "old", updatedAt: base.AddDate(0, 0, -2), usage: testUsage(1, 1, 1, 1)},
		testConversationSummary{id: "a", updatedAt: base, usage: testUsage(100, 50, 10, 5)},
		testConversationSummary{id: "b", updatedAt: base.Add(3 * time.Hour), usage: testUsage(200, 70, 20, 7)},
		testConversationSummary{id: "c", updatedAt: base.AddDate(0, 0, 1), usage: testUsage(300, 90, 30, 9)},
		testConversationSummary{id: "future", updatedAt: base.AddDate(0, 0, 3), usage: testUsage(999, 999, 999, 999)},
	}

	stats := CalculateUsageStats(summaries, base.Truncate(24*time.Hour), base.AddDate(0, 0, 1).Truncate(24*time.Hour))

	require.Len(t, stats.Daily, 2)
	assert.Equal(t, base.AddDate(0, 0, 1).Truncate(24*time.Hour), stats.Daily[0].Date)
	assert.Equal(t, 1, stats.Daily[0].Conversations)
	assert.Equal(t, 300, stats.Daily[0].Usage.InputTokens)
	assert.Equal(t, base.Truncate(24*time.Hour), stats.Daily[1].Date)
	assert.Equal(t, 2, stats.Daily[1].Conversations)
	assert.Equal(t, 300, stats.Daily[1].Usage.InputTokens)
	assert.Equal(t, 120, stats.Daily[1].Usage.OutputTokens)
	assert.Equal(t, 30, stats.Daily[1].Usage.CacheCreationInputTokens)
	assert.Equal(t, 12, stats.Daily[1].Usage.CacheReadInputTokens)

	assert.Equal(t, 600, stats.Total.InputTokens)
	assert.Equal(t, 210, stats.Total.OutputTokens)
	assert.Equal(t, 60, stats.Total.CacheCreationInputTokens)
	assert.Equal(t, 21, stats.Total.CacheReadInputTokens)
	assert.InDelta(t, 0.6, stats.Total.InputCost, 0.0001)
	assert.InDelta(t, 0.21, stats.Total.OutputCost, 0.0001)
	assert.InDelta(t, 0.06, stats.Total.CacheCreationCost, 0.0001)
	assert.InDelta(t, 0.021, stats.Total.CacheReadCost, 0.0001)
}

func TestCalculateConversationUsageStatsTotalsAllFields(t *testing.T) {
	summaries := []ConversationSummary{
		testConversationSummary{messageCount: 2, usage: llmtypes.Usage{InputTokens: 100, OutputTokens: 50, CacheReadInputTokens: 10, CacheCreationInputTokens: 5, InputCost: 0.01, OutputCost: 0.02, CacheReadCost: 0.001, CacheCreationCost: 0.002}},
		testConversationSummary{messageCount: 3, usage: llmtypes.Usage{InputTokens: 200, OutputTokens: 60, CacheReadInputTokens: 20, CacheCreationInputTokens: 6, InputCost: 0.03, OutputCost: 0.04, CacheReadCost: 0.003, CacheCreationCost: 0.004}},
	}

	stats := CalculateConversationUsageStats(summaries)

	assert.Equal(t, 2, stats.TotalConversations)
	assert.Equal(t, 5, stats.TotalMessages)
	assert.Equal(t, 451, stats.TotalTokens)
	assert.Equal(t, 300, stats.InputTokens)
	assert.Equal(t, 110, stats.OutputTokens)
	assert.Equal(t, 30, stats.CacheReadTokens)
	assert.Equal(t, 11, stats.CacheWriteTokens)
	assert.InDelta(t, 0.04, stats.InputCost, 0.0001)
	assert.InDelta(t, 0.06, stats.OutputCost, 0.0001)
	assert.InDelta(t, 0.004, stats.CacheReadCost, 0.0001)
	assert.InDelta(t, 0.006, stats.CacheWriteCost, 0.0001)
	assert.InDelta(t, 0.11, stats.TotalCost, 0.0001)

	empty := CalculateConversationUsageStats(nil)
	assert.Equal(t, 0, empty.TotalConversations)
	assert.Equal(t, 0, empty.TotalTokens)
}

func TestCalculateProviderBreakdownStatsAggregatesByProvider(t *testing.T) {
	base := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	summaries := []ConversationSummary{
		testConversationSummary{updatedAt: base, provider: "anthropic", usage: testUsage(100, 10, 1, 2)},
		testConversationSummary{updatedAt: base.Add(time.Hour), provider: "anthropic", usage: testUsage(200, 20, 2, 3)},
		testConversationSummary{updatedAt: base.AddDate(0, 0, 1), provider: "openai", usage: testUsage(300, 30, 3, 4)},
		testConversationSummary{updatedAt: base.AddDate(0, 0, -1), provider: "ignored", usage: testUsage(999, 999, 999, 999)},
	}

	stats := CalculateProviderBreakdownStats(summaries, base, time.Time{})

	assert.Equal(t, 3, stats.TotalConversations)
	assert.Equal(t, 600, stats.Total.InputTokens)
	require.Contains(t, stats.ProviderStats, "anthropic")
	require.Contains(t, stats.ProviderStats, "openai")
	assert.Equal(t, 2, stats.ProviderStats["anthropic"].Conversations)
	assert.Equal(t, 300, stats.ProviderStats["anthropic"].Usage.InputTokens)
	assert.Equal(t, 1, stats.ProviderStats["openai"].Conversations)
	assert.Equal(t, 300, stats.ProviderStats["openai"].Usage.InputTokens)
	assert.NotContains(t, stats.ProviderStats, "ignored")
}

func TestCalculateDailyProviderBreakdownStatsAggregatesAndSorts(t *testing.T) {
	base := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	summaries := []ConversationSummary{
		testConversationSummary{updatedAt: base, provider: "anthropic", usage: testUsage(100, 10, 1, 2)},
		testConversationSummary{updatedAt: base.Add(2 * time.Hour), provider: "openai", usage: testUsage(200, 20, 2, 3)},
		testConversationSummary{updatedAt: base.AddDate(0, 0, 1), provider: "openai", usage: testUsage(300, 30, 3, 4)},
	}

	stats := CalculateDailyProviderBreakdownStats(summaries, time.Time{}, time.Time{})

	require.Len(t, stats.Daily, 2)
	assert.Equal(t, base.AddDate(0, 0, 1).Truncate(24*time.Hour), stats.Daily[0].Date)
	assert.Equal(t, 1, stats.Daily[0].TotalConversations)
	assert.Equal(t, 300, stats.Daily[0].TotalUsage.InputTokens)
	assert.Equal(t, 300, stats.Daily[0].ProviderUsage["openai"].Usage.InputTokens)

	assert.Equal(t, base.Truncate(24*time.Hour), stats.Daily[1].Date)
	assert.Equal(t, 2, stats.Daily[1].TotalConversations)
	assert.Equal(t, 300, stats.Daily[1].TotalUsage.InputTokens)
	assert.Equal(t, 100, stats.Daily[1].ProviderUsage["anthropic"].Usage.InputTokens)
	assert.Equal(t, 200, stats.Daily[1].ProviderUsage["openai"].Usage.InputTokens)

	assert.Equal(t, 3, stats.TotalConversations)
	assert.Equal(t, 600, stats.Total.InputTokens)
}

func TestFormatNumberAndCost(t *testing.T) {
	assert.Equal(t, "999", FormatNumber(999))
	assert.Equal(t, "1,000", FormatNumber(1000))
	assert.Equal(t, "1,234,567", FormatNumber(1234567))
	assert.Equal(t, "$1.2346", FormatCost(1.23456))
}

func TestDailyProviderBreakdownProviderKeys(t *testing.T) {
	stats := CalculateDailyProviderBreakdownStats([]ConversationSummary{
		testConversationSummary{updatedAt: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC), provider: "zeta", usage: testUsage(1, 0, 0, 0)},
		testConversationSummary{updatedAt: time.Date(2026, 5, 20, 1, 0, 0, 0, time.UTC), provider: "alpha", usage: testUsage(2, 0, 0, 0)},
	}, time.Time{}, time.Time{})

	keys := make([]string, 0, len(stats.Daily[0].ProviderUsage))
	for key := range stats.Daily[0].ProviderUsage {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	assert.Equal(t, []string{"alpha", "zeta"}, keys)
}

func TestLogLLMUsage_NormalCase(t *testing.T) {
	ts := setupTestLogger(logrus.InfoLevel)

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

	model := "claude-sonnet-4-6"
	startTime := time.Now().Add(-2 * time.Second) // 2 seconds ago
	requestOutputTokens := 150

	LogLLMUsage(ts.ctx, usage, model, startTime, requestOutputTokens)

	logEntry := ts.parseLogEntry(t)
	ts.assertLogMessage(t, logEntry)

	assert.Equal(t, model, logEntry["model"])
	assert.Equal(t, float64(1000), logEntry["input_tokens"])
	assert.Equal(t, float64(500), logEntry["output_tokens"])
	assert.Equal(t, 0.003, logEntry["total_cost"]) // 0.001 + 0.002 + 0.0001 + 0.0002 rounded to 3 decimals
	assert.Equal(t, float64(8000), logEntry["max_context_window"])

	assert.Equal(t, 0.25, logEntry["context_window_usage_ratio"]) // 2000/8000 = 0.25

	// Verify tokens per second calculation (should be around 75 tokens/s for 150 tokens in ~2s)
	tokensPerSecond, ok := logEntry["output_tokens/s"].(float64)
	require.True(t, ok)
	assert.Greater(t, tokensPerSecond, 50.0) // Should be around 75
	assert.Less(t, tokensPerSecond, 100.0)   // Should be around 75
}

func TestLogLLMUsage_ZeroMaxContextWindow(t *testing.T) {
	ts := setupTestLogger(logrus.InfoLevel)

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
		{"Anthropic Claude", "claude-sonnet-4-6"},
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

	requiredFields := []string{
		"model", "input_tokens", "output_tokens",
		"total_cost", "max_context_window", "context_window_usage_ratio", "output_tokens/s",
	}

	for _, field := range requiredFields {
		assert.Contains(t, logEntry, field, "Field %s should be present in log entry", field)
	}

	assert.Equal(t, 0.003, logEntry["total_cost"])                 // Sum of all costs rounded to 3 decimals
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

	// Verify total cost calculation (rounded to 3 decimal places)
	// Original: 0.0012345 + 0.0067890 + 0.0001111 + 0.0002222 = 0.0083568
	expectedRoundedTotalCost := 0.008 // rounded to 3 decimal places
	assert.Equal(t, expectedRoundedTotalCost, logEntry["total_cost"])

	// Verify context window ratio (rounded to 3 decimal places)
	// Original: 250/3000 = 0.08333333333333333
	expectedRoundedRatio := 0.083 // rounded to 3 decimal places
	assert.Equal(t, expectedRoundedRatio, logEntry["context_window_usage_ratio"])

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
