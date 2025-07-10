package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTimeSpec(t *testing.T) {
	// Use a fixed time for deterministic tests
	fixedTime := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	mockNow := func() time.Time { return fixedTime }

	tests := []struct {
		name     string
		input    string
		expected time.Time
		wantErr  bool
	}{
		{
			name:     "empty string",
			input:    "",
			expected: time.Time{},
			wantErr:  false,
		},
		{
			name:     "absolute date",
			input:    "2025-06-01",
			expected: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
			wantErr:  false,
		},
		{
			name:     "1 day ago",
			input:    "1d",
			expected: fixedTime.AddDate(0, 0, -1),
			wantErr:  false,
		},
		{
			name:     "7 days ago",
			input:    "7d",
			expected: fixedTime.AddDate(0, 0, -7),
			wantErr:  false,
		},
		{
			name:     "1 week ago",
			input:    "1w",
			expected: fixedTime.AddDate(0, 0, -7),
			wantErr:  false,
		},
		{
			name:     "2 weeks ago",
			input:    "2w",
			expected: fixedTime.AddDate(0, 0, -14),
			wantErr:  false,
		},
		{
			name:     "1 hour ago",
			input:    "1h",
			expected: fixedTime.Add(-time.Hour),
			wantErr:  false,
		},
		{
			name:     "24 hours ago",
			input:    "24h",
			expected: fixedTime.Add(-24 * time.Hour),
			wantErr:  false,
		},
		{
			name:    "invalid format",
			input:   "invalid",
			wantErr: true,
		},
		{
			name:    "invalid number",
			input:   "xd",
			wantErr: true,
		},
		{
			name:    "invalid unit",
			input:   "1x",
			wantErr: true,
		},
		{
			name:    "missing unit",
			input:   "1",
			wantErr: true,
		},
		{
			name:    "invalid date format",
			input:   "2025-13-01",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseTimeSpecWithClock(tt.input, mockNow)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFormatNumber(t *testing.T) {
	tests := []struct {
		name     string
		input    int
		expected string
	}{
		{
			name:     "zero",
			input:    0,
			expected: "0",
		},
		{
			name:     "single digit",
			input:    5,
			expected: "5",
		},
		{
			name:     "two digits",
			input:    42,
			expected: "42",
		},
		{
			name:     "three digits",
			input:    123,
			expected: "123",
		},
		{
			name:     "four digits",
			input:    1234,
			expected: "1,234",
		},
		{
			name:     "five digits",
			input:    12345,
			expected: "12,345",
		},
		{
			name:     "six digits",
			input:    123456,
			expected: "123,456",
		},
		{
			name:     "seven digits",
			input:    1234567,
			expected: "1,234,567",
		},
		{
			name:     "large number",
			input:    1000000,
			expected: "1,000,000",
		},
		{
			name:     "exactly 1000",
			input:    1000,
			expected: "1,000",
		},
		{
			name:     "exactly 10000",
			input:    10000,
			expected: "10,000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatNumber(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetUsageConfigFromFlags(t *testing.T) {
	tests := []struct {
		name     string
		flags    map[string]string
		expected *UsageConfig
	}{
		{
			name:  "default values",
			flags: map[string]string{},
			expected: &UsageConfig{
				Since:  "10d",
				Until:  "",
				Format: "table",
			},
		},
		{
			name: "custom since",
			flags: map[string]string{
				"since": "1w",
			},
			expected: &UsageConfig{
				Since:  "1w",
				Until:  "",
				Format: "table",
			},
		},
		{
			name: "custom until",
			flags: map[string]string{
				"until": "2025-06-01",
			},
			expected: &UsageConfig{
				Since:  "10d",
				Until:  "2025-06-01",
				Format: "table",
			},
		},
		{
			name: "custom format",
			flags: map[string]string{
				"format": "json",
			},
			expected: &UsageConfig{
				Since:  "10d",
				Until:  "",
				Format: "json",
			},
		},
		{
			name: "all custom",
			flags: map[string]string{
				"since":  "2025-05-01",
				"until":  "2025-06-01",
				"format": "json",
			},
			expected: &UsageConfig{
				Since:  "2025-05-01",
				Until:  "2025-06-01",
				Format: "json",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{}

			// Set up flags
			defaults := NewUsageConfig()
			cmd.Flags().String("since", defaults.Since, "")
			cmd.Flags().String("until", defaults.Until, "")
			cmd.Flags().String("format", defaults.Format, "")

			// Set flag values
			for key, value := range tt.flags {
				err := cmd.Flags().Set(key, value)
				require.NoError(t, err)
			}

			result := getUsageConfigFromFlags(cmd)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestAggregateUsageStats(t *testing.T) {
	// Create test data
	baseTime := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

	summaries := []conversations.ConversationSummary{
		{
			ID:           "test-1",
			MessageCount: 2,
			CreatedAt:    baseTime.Add(1 * 24 * time.Hour), // 2025-06-02
			UpdatedAt:    baseTime.Add(1 * 24 * time.Hour), // 2025-06-02
			Usage: llmtypes.Usage{
				InputTokens:              100,
				OutputTokens:             50,
				CacheCreationInputTokens: 10,
				CacheReadInputTokens:     5,
				InputCost:                0.01,
				OutputCost:               0.02,
				CacheCreationCost:        0.001,
				CacheReadCost:            0.0005,
			},
		},
		{
			ID:           "test-2",
			MessageCount: 3,
			CreatedAt:    baseTime.Add(1 * 24 * time.Hour), // 2025-06-02
			UpdatedAt:    baseTime.Add(1 * 24 * time.Hour), // 2025-06-02 (same day)
			Usage: llmtypes.Usage{
				InputTokens:              200,
				OutputTokens:             100,
				CacheCreationInputTokens: 20,
				CacheReadInputTokens:     10,
				InputCost:                0.02,
				OutputCost:               0.04,
				CacheCreationCost:        0.002,
				CacheReadCost:            0.001,
			},
		},
		{
			ID:           "test-3",
			MessageCount: 4,
			CreatedAt:    baseTime.Add(2 * 24 * time.Hour), // 2025-06-03
			UpdatedAt:    baseTime.Add(2 * 24 * time.Hour), // 2025-06-03
			Usage: llmtypes.Usage{
				InputTokens:              150,
				OutputTokens:             75,
				CacheCreationInputTokens: 15,
				CacheReadInputTokens:     7,
				InputCost:                0.015,
				OutputCost:               0.03,
				CacheCreationCost:        0.0015,
				CacheReadCost:            0.0007,
			},
		},
	}

	t.Run("no time filters", func(t *testing.T) {
		stats := aggregateUsageStats(summaries, time.Time{}, time.Time{})

		assert.Len(t, stats.Daily, 2) // 2 unique days

		// Check total
		assert.Equal(t, 450, stats.Total.InputTokens)
		assert.Equal(t, 225, stats.Total.OutputTokens)
		assert.Equal(t, 45, stats.Total.CacheCreationInputTokens)
		assert.Equal(t, 22, stats.Total.CacheReadInputTokens)
		assert.InDelta(t, 0.045, stats.Total.InputCost, 0.001)
		assert.InDelta(t, 0.09, stats.Total.OutputCost, 0.001)
		assert.InDelta(t, 0.0045, stats.Total.CacheCreationCost, 0.0001)
		assert.InDelta(t, 0.0022, stats.Total.CacheReadCost, 0.0001)

		// Check daily aggregation (should be sorted by date descending)
		assert.Equal(t, baseTime.Add(2*24*time.Hour).Truncate(24*time.Hour), stats.Daily[0].Date)
		assert.Equal(t, 1, stats.Daily[0].Conversations)
		assert.Equal(t, 150, stats.Daily[0].Usage.InputTokens)

		assert.Equal(t, baseTime.Add(1*24*time.Hour).Truncate(24*time.Hour), stats.Daily[1].Date)
		assert.Equal(t, 2, stats.Daily[1].Conversations)       // Two conversations on same day
		assert.Equal(t, 300, stats.Daily[1].Usage.InputTokens) // 100 + 200
	})

	t.Run("with start time filter", func(t *testing.T) {
		startTime := baseTime.Add(1*24*time.Hour + 12*time.Hour) // Middle of 2025-06-02
		stats := aggregateUsageStats(summaries, startTime, time.Time{})

		assert.Len(t, stats.Daily, 1) // Only 2025-06-03 should be included
		assert.Equal(t, baseTime.Add(2*24*time.Hour).Truncate(24*time.Hour), stats.Daily[0].Date)
		assert.Equal(t, 150, stats.Total.InputTokens)
	})

	t.Run("with end time filter", func(t *testing.T) {
		endTime := baseTime.Add(1*24*time.Hour + 12*time.Hour) // Middle of 2025-06-02
		stats := aggregateUsageStats(summaries, time.Time{}, endTime)

		assert.Len(t, stats.Daily, 1) // Only 2025-06-02 should be included
		assert.Equal(t, baseTime.Add(1*24*time.Hour).Truncate(24*time.Hour), stats.Daily[0].Date)
		assert.Equal(t, 300, stats.Total.InputTokens) // 100 + 200
	})

	t.Run("with both time filters", func(t *testing.T) {
		startTime := baseTime.Add(1 * 24 * time.Hour)          // Start of 2025-06-02
		endTime := baseTime.Add(1*24*time.Hour + 23*time.Hour) // End of 2025-06-02
		stats := aggregateUsageStats(summaries, startTime, endTime)

		assert.Len(t, stats.Daily, 1)                 // Only 2025-06-02 should be included
		assert.Equal(t, 300, stats.Total.InputTokens) // 100 + 200
	})

	t.Run("empty summaries", func(t *testing.T) {
		stats := aggregateUsageStats([]conversations.ConversationSummary{}, time.Time{}, time.Time{})

		assert.Len(t, stats.Daily, 0)
		assert.Equal(t, llmtypes.Usage{}, stats.Total)
	})
}

func TestDisplayUsageTable(t *testing.T) {
	stats := &UsageStats{
		Daily: []DailyUsage{
			{
				Date:          time.Date(2025, 6, 2, 0, 0, 0, 0, time.UTC),
				Conversations: 2,
				Usage: llmtypes.Usage{
					InputTokens:              1000,
					OutputTokens:             500,
					CacheCreationInputTokens: 100,
					CacheReadInputTokens:     50,
					InputCost:                0.01,
					OutputCost:               0.02,
					CacheCreationCost:        0.001,
					CacheReadCost:            0.0005,
				},
			},
			{
				Date:          time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
				Conversations: 1,
				Usage: llmtypes.Usage{
					InputTokens:              500,
					OutputTokens:             250,
					CacheCreationInputTokens: 50,
					CacheReadInputTokens:     25,
					InputCost:                0.005,
					OutputCost:               0.01,
					CacheCreationCost:        0.0005,
					CacheReadCost:            0.00025,
				},
			},
		},
		Total: llmtypes.Usage{
			InputTokens:              1500,
			OutputTokens:             750,
			CacheCreationInputTokens: 150,
			CacheReadInputTokens:     75,
			InputCost:                0.015,
			OutputCost:               0.03,
			CacheCreationCost:        0.0015,
			CacheReadCost:            0.00075,
		},
	}

	var buf bytes.Buffer
	displayUsageTable(&buf, stats)

	output := buf.String()

	// Check that the table contains expected data
	assert.Contains(t, output, "2025-06-02")
	assert.Contains(t, output, "2025-06-01")
	assert.Contains(t, output, "1,000") // Formatted number
	assert.Contains(t, output, "1,500") // Total formatted
	assert.Contains(t, output, "TOTAL")

	// Check that costs are formatted correctly
	assert.Contains(t, output, "$0.0315") // Total cost for first day
	assert.Contains(t, output, "$0.0158") // Total cost for second day
	assert.Contains(t, output, "$0.0473") // Overall total cost
}

func TestDisplayUsageJSON(t *testing.T) {
	stats := &UsageStats{
		Daily: []DailyUsage{
			{
				Date:          time.Date(2025, 6, 2, 0, 0, 0, 0, time.UTC),
				Conversations: 2,
				Usage: llmtypes.Usage{
					InputTokens:              1000,
					OutputTokens:             500,
					CacheCreationInputTokens: 100,
					CacheReadInputTokens:     50,
					InputCost:                0.01,
					OutputCost:               0.02,
					CacheCreationCost:        0.001,
					CacheReadCost:            0.0005,
				},
			},
		},
		Total: llmtypes.Usage{
			InputTokens:              1000,
			OutputTokens:             500,
			CacheCreationInputTokens: 100,
			CacheReadInputTokens:     50,
			InputCost:                0.01,
			OutputCost:               0.02,
			CacheCreationCost:        0.001,
			CacheReadCost:            0.0005,
		},
	}

	var buf bytes.Buffer
	displayUsageJSON(&buf, stats)

	output := buf.String()

	// Check that JSON contains expected structure
	assert.Contains(t, output, `"daily":`)
	assert.Contains(t, output, `"total":`)
	assert.Contains(t, output, `"2025-06-02"`)
	assert.Contains(t, output, `"conversations": 2`)
	assert.Contains(t, output, `"input_tokens": 1000`)
	assert.Contains(t, output, `"output_tokens": 500`)
	assert.Contains(t, output, `"cache_write_tokens": 100`)
	assert.Contains(t, output, `"cache_read_tokens": 50`)
	assert.Contains(t, output, `"total_cost": 0.0315`)

	// Verify it's valid JSON
	assert.True(t, strings.HasPrefix(strings.TrimSpace(output), "{"))
	assert.True(t, strings.HasSuffix(strings.TrimSpace(output), "}"))
}

func TestNewUsageConfig(t *testing.T) {
	config := NewUsageConfig()

	assert.Equal(t, "10d", config.Since)
	assert.Equal(t, "", config.Until)
	assert.Equal(t, "table", config.Format)
}

func TestDateRangeFiltering(t *testing.T) {
	// Fixed time for deterministic testing
	fixedTime := time.Date(2025, 6, 15, 15, 30, 0, 0, time.UTC) // Sunday 3:30 PM
	mockNow := func() time.Time { return fixedTime }

	tests := []struct {
		name          string
		since         string
		until         string
		expectedStart time.Time
		expectedEnd   time.Time
		description   string
	}{
		{
			name:          "7d - past 7 days",
			since:         "7d",
			until:         "",
			expectedStart: time.Date(2025, 6, 8, 0, 0, 0, 0, time.UTC), // 7 days ago, start of day
			expectedEnd:   time.Time{},                                 // No end time
			description:   "From June 8 00:00 to now",
		},
		{
			name:          "1w - past 1 week",
			since:         "1w",
			until:         "",
			expectedStart: time.Date(2025, 6, 8, 0, 0, 0, 0, time.UTC), // 1 week ago, start of day
			expectedEnd:   time.Time{},                                 // No end time
			description:   "From June 8 00:00 to now",
		},
		{
			name:          "1d - past 1 day",
			since:         "1d",
			until:         "",
			expectedStart: time.Date(2025, 6, 14, 0, 0, 0, 0, time.UTC), // 1 day ago, start of day
			expectedEnd:   time.Time{},                                  // No end time
			description:   "From June 14 00:00 to now",
		},
		{
			name:          "24h - past 24 hours",
			since:         "24h",
			until:         "",
			expectedStart: time.Date(2025, 6, 14, 0, 0, 0, 0, time.UTC), // 24 hours ago, start of day
			expectedEnd:   time.Time{},                                  // No end time
			description:   "From June 14 00:00 to now",
		},
		{
			name:          "absolute date range",
			since:         "2025-06-01",
			until:         "2025-06-10",
			expectedStart: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),     // Start of June 1
			expectedEnd:   time.Date(2025, 6, 10, 23, 59, 59, 0, time.UTC), // End of June 10
			description:   "From June 1 00:00 to June 10 23:59:59",
		},
		{
			name:          "mixed range - relative start, absolute end",
			since:         "7d",
			until:         "2025-06-12",
			expectedStart: time.Date(2025, 6, 8, 0, 0, 0, 0, time.UTC),     // 7 days ago, start of day
			expectedEnd:   time.Date(2025, 6, 12, 23, 59, 59, 0, time.UTC), // End of June 12
			description:   "From June 8 00:00 to June 12 23:59:59",
		},
		{
			name:          "default 10d",
			since:         "10d",
			until:         "",
			expectedStart: time.Date(2025, 6, 5, 0, 0, 0, 0, time.UTC), // 10 days ago, start of day
			expectedEnd:   time.Time{},                                 // No end time
			description:   "From June 5 00:00 to now",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Parse the time specifications
			var actualStart, actualEnd time.Time
			var err error

			if tt.since != "" {
				actualStart, err = parseTimeSpecWithClock(tt.since, mockNow)
				require.NoError(t, err)
				// Simulate what runUsageCmd does - truncate to start of day
				actualStart = actualStart.Truncate(24 * time.Hour)
			}

			if tt.until != "" {
				actualEnd, err = parseTimeSpecWithClock(tt.until, mockNow)
				require.NoError(t, err)
				// Simulate what runUsageCmd does - set to end of day
				actualEnd = actualEnd.Truncate(24 * time.Hour).Add(24*time.Hour - time.Second)
			}

			assert.Equal(t, tt.expectedStart, actualStart, "Start time mismatch for %s", tt.description)
			assert.Equal(t, tt.expectedEnd, actualEnd, "End time mismatch for %s", tt.description)

			// Log the actual date range for visibility
			t.Logf("Date range for %s: %s", tt.name, tt.description)
		})
	}
}

func TestDateRangeFilteringWithSummaries(t *testing.T) {
	// Fixed time for deterministic testing
	fixedTime := time.Date(2025, 6, 15, 15, 30, 0, 0, time.UTC) // Sunday 3:30 PM
	mockNow := func() time.Time { return fixedTime }

	// Create test summaries spanning multiple days
	summaries := []conversations.ConversationSummary{
		{
			ID:           "test-1",
			MessageCount: 2,
			CreatedAt:    time.Date(2025, 6, 3, 10, 0, 0, 0, time.UTC), // 12 days ago
			UpdatedAt:    time.Date(2025, 6, 3, 10, 0, 0, 0, time.UTC), // 12 days ago
			Usage:        llmtypes.Usage{InputTokens: 100, OutputTokens: 50},
		},
		{
			ID:           "test-2",
			MessageCount: 3,
			CreatedAt:    time.Date(2025, 6, 8, 14, 0, 0, 0, time.UTC), // 7 days ago
			UpdatedAt:    time.Date(2025, 6, 8, 14, 0, 0, 0, time.UTC), // 7 days ago
			Usage:        llmtypes.Usage{InputTokens: 200, OutputTokens: 100},
		},
		{
			ID:           "test-3",
			MessageCount: 4,
			CreatedAt:    time.Date(2025, 6, 12, 9, 0, 0, 0, time.UTC), // 3 days ago
			UpdatedAt:    time.Date(2025, 6, 12, 9, 0, 0, 0, time.UTC), // 3 days ago
			Usage:        llmtypes.Usage{InputTokens: 300, OutputTokens: 150},
		},
		{
			ID:           "test-4",
			MessageCount: 5,
			CreatedAt:    time.Date(2025, 6, 14, 16, 0, 0, 0, time.UTC), // 1 day ago
			UpdatedAt:    time.Date(2025, 6, 14, 16, 0, 0, 0, time.UTC), // 1 day ago
			Usage:        llmtypes.Usage{InputTokens: 400, OutputTokens: 200},
		},
	}

	tests := []struct {
		name                string
		since               string
		until               string
		expectedRecordCount int
		expectedInputTokens int
		description         string
	}{
		{
			name:                "7d - should include last 7 days",
			since:               "7d",
			until:               "",
			expectedRecordCount: 3,   // Summaries from June 8, 12, 14
			expectedInputTokens: 900, // 200 + 300 + 400
			description:         "Summaries from June 8 onwards",
		},
		{
			name:                "1w - should include last week (same as 7d)",
			since:               "1w",
			until:               "",
			expectedRecordCount: 3,   // Summaries from June 8, 12, 14
			expectedInputTokens: 900, // 200 + 300 + 400
			description:         "Summaries from June 8 onwards",
		},
		{
			name:                "3d - should include last 3 days",
			since:               "3d",
			until:               "",
			expectedRecordCount: 2,   // Summaries from June 12, 14
			expectedInputTokens: 700, // 300 + 400
			description:         "Summaries from June 12 onwards",
		},
		{
			name:                "1d - should include last day",
			since:               "1d",
			until:               "",
			expectedRecordCount: 1,   // Summary from June 14
			expectedInputTokens: 400, // 400
			description:         "Summaries from June 14 onwards",
		},
		{
			name:                "absolute range - June 8 to June 12",
			since:               "2025-06-08",
			until:               "2025-06-12",
			expectedRecordCount: 2,   // Summaries from June 8 and 12
			expectedInputTokens: 500, // 200 + 300
			description:         "Summaries from June 8 to June 12",
		},
		{
			name:                "no filters - all summaries",
			since:               "",
			until:               "",
			expectedRecordCount: 4,    // All summaries
			expectedInputTokens: 1000, // 100 + 200 + 300 + 400
			description:         "All summaries",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Parse time specifications
			var startTime, endTime time.Time
			var err error

			if tt.since != "" {
				startTime, err = parseTimeSpecWithClock(tt.since, mockNow)
				require.NoError(t, err)
				startTime = startTime.Truncate(24 * time.Hour)
			}

			if tt.until != "" {
				endTime, err = parseTimeSpecWithClock(tt.until, mockNow)
				require.NoError(t, err)
				endTime = endTime.Truncate(24 * time.Hour).Add(24*time.Hour - time.Second)
			}

			// Test the aggregation with the parsed time range
			stats := aggregateUsageStats(summaries, startTime, endTime)

			// Count total conversations and tokens
			totalConversations := 0
			for _, daily := range stats.Daily {
				totalConversations += daily.Conversations
			}

			assert.Equal(t, tt.expectedRecordCount, totalConversations,
				"Summary count mismatch for %s", tt.description)
			assert.Equal(t, tt.expectedInputTokens, stats.Total.InputTokens,
				"Input tokens mismatch for %s", tt.description)

			// Log the actual filtering results for visibility
			t.Logf("Filtered %d summaries with %d input tokens for %s",
				totalConversations, stats.Total.InputTokens, tt.description)
		})
	}
}
