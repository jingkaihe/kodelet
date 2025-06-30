package main

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

func TestParseTimeSpec(t *testing.T) {
	tests := []struct {
		name     string
		spec     string
		wantErr  bool
		validate func(time.Time) bool
	}{
		{
			name:    "empty string",
			spec:    "",
			wantErr: false,
			validate: func(t time.Time) bool {
				return t.IsZero()
			},
		},
		{
			name:    "absolute date",
			spec:    "2025-06-01",
			wantErr: false,
			validate: func(t time.Time) bool {
				return t.Year() == 2025 && t.Month() == 6 && t.Day() == 1
			},
		},
		{
			name:    "1 day ago",
			spec:    "1d",
			wantErr: false,
			validate: func(t time.Time) bool {
				return t.Before(time.Now()) && t.After(time.Now().AddDate(0, 0, -2))
			},
		},
		{
			name:    "1 week ago",
			spec:    "1w",
			wantErr: false,
			validate: func(t time.Time) bool {
				return t.Before(time.Now()) && t.After(time.Now().AddDate(0, 0, -8))
			},
		},
		{
			name:    "1 hour ago",
			spec:    "1h",
			wantErr: false,
			validate: func(t time.Time) bool {
				return t.Before(time.Now()) && t.After(time.Now().Add(-2*time.Hour))
			},
		},
		{
			name:    "invalid format",
			spec:    "invalid",
			wantErr: true,
		},
		{
			name:    "invalid unit",
			spec:    "1x",
			wantErr: true,
		},
		{
			name:    "invalid number",
			spec:    "xd",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseTimeSpec(tt.spec)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseTimeSpec() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && tt.validate != nil && !tt.validate(result) {
				t.Errorf("parseTimeSpec() result validation failed for spec %s, got %v", tt.spec, result)
			}
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
			name:     "small number",
			input:    123,
			expected: "123",
		},
		{
			name:     "thousands",
			input:    1234,
			expected: "1,234",
		},
		{
			name:     "millions",
			input:    1234567,
			expected: "1,234,567",
		},
		{
			name:     "zero",
			input:    0,
			expected: "0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatNumber(tt.input)
			if result != tt.expected {
				t.Errorf("formatNumber() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestDisplayUsageJSON(t *testing.T) {
	// Create test data
	testStats := &UsageStats{
		Daily: []DailyUsage{
			{
				Date:          time.Date(2025, 6, 30, 0, 0, 0, 0, time.UTC),
				Conversations: 5,
				Usage: llmtypes.Usage{
					InputTokens:              1000,
					OutputTokens:             500,
					CacheCreationInputTokens: 100,
					CacheReadInputTokens:     200,
					InputCost:                0.01,
					OutputCost:               0.02,
					CacheCreationCost:        0.001,
					CacheReadCost:            0.002,
				},
			},
			{
				Date:          time.Date(2025, 6, 29, 0, 0, 0, 0, time.UTC),
				Conversations: 3,
				Usage: llmtypes.Usage{
					InputTokens:              800,
					OutputTokens:             400,
					CacheCreationInputTokens: 80,
					CacheReadInputTokens:     160,
					InputCost:                0.008,
					OutputCost:               0.016,
					CacheCreationCost:        0.0008,
					CacheReadCost:            0.0016,
				},
			},
		},
		Total: llmtypes.Usage{
			InputTokens:              1800,
			OutputTokens:             900,
			CacheCreationInputTokens: 180,
			CacheReadInputTokens:     360,
			InputCost:                0.018,
			OutputCost:               0.036,
			CacheCreationCost:        0.0018,
			CacheReadCost:            0.0036,
		},
	}

	// Capture output
	var buf bytes.Buffer
	displayUsageJSON(&buf, testStats)

	// Parse the output JSON
	var output UsageJSONOutput
	if err := json.Unmarshal(buf.Bytes(), &output); err != nil {
		t.Fatalf("Failed to parse JSON output: %v", err)
	}

	// Verify structure
	if len(output.Daily) != 2 {
		t.Errorf("Expected 2 daily entries, got %d", len(output.Daily))
	}

	// Verify first daily entry
	daily1 := output.Daily[0]
	if daily1.Date != "2025-06-30" {
		t.Errorf("Expected date 2025-06-30, got %s", daily1.Date)
	}
	if daily1.Conversations != 5 {
		t.Errorf("Expected 5 conversations, got %d", daily1.Conversations)
	}
	if daily1.InputTokens != 1000 {
		t.Errorf("Expected 1000 input tokens, got %d", daily1.InputTokens)
	}

	// Verify total
	if output.Total.Conversations != 8 { // 5 + 3
		t.Errorf("Expected 8 total conversations, got %d", output.Total.Conversations)
	}
	if output.Total.InputTokens != 1800 {
		t.Errorf("Expected 1800 total input tokens, got %d", output.Total.InputTokens)
	}

	// Verify that JSON is properly formatted (no parsing errors)
	var prettyJSON bytes.Buffer
	if err := json.Indent(&prettyJSON, buf.Bytes(), "", "  "); err != nil {
		t.Errorf("JSON output is not properly formatted: %v", err)
	}
}
