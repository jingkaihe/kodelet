package tui

import (
	"testing"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
)

func TestFormatUsageStats(t *testing.T) {
	tests := []struct {
		name     string
		usage    llmtypes.Usage
		wantText bool
		wantCost bool
	}{
		{
			name: "usage with all fields",
			usage: llmtypes.Usage{
				InputTokens:              100,
				OutputTokens:             50,
				CacheCreationInputTokens: 10,
				CacheReadInputTokens:     5,
				MaxContextWindow:         200000,
				CurrentContextWindow:     155,
				InputCost:                0.01,
				OutputCost:               0.02,
				CacheCreationCost:        0.001,
				CacheReadCost:            0.0005,
			},
			wantText: true,
			wantCost: true,
		},
		{
			name: "usage without cost",
			usage: llmtypes.Usage{
				InputTokens:          100,
				OutputTokens:         50,
				MaxContextWindow:     200000,
				CurrentContextWindow: 150,
			},
			wantText: true,
			wantCost: false,
		},
		{
			name: "zero usage",
			usage: llmtypes.Usage{
				InputTokens:  0,
				OutputTokens: 0,
			},
			wantText: false,
			wantCost: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usageText, costText := FormatUsageStats(tt.usage)

			if tt.wantText {
				assert.NotEmpty(t, usageText)
				assert.Contains(t, usageText, "Tokens:")
			} else {
				assert.Empty(t, usageText)
			}

			if tt.wantCost {
				assert.NotEmpty(t, costText)
				assert.Contains(t, costText, "Cost:")
			} else {
				assert.Empty(t, costText)
			}
		})
	}
}

func TestGetSpinnerChar(t *testing.T) {
	// Test that we get different characters for different indices
	char1 := GetSpinnerChar(0)
	char2 := GetSpinnerChar(1)
	char3 := GetSpinnerChar(7)
	char4 := GetSpinnerChar(8) // Should wrap around

	assert.NotEmpty(t, char1)
	assert.NotEmpty(t, char2)
	assert.NotEmpty(t, char3)
	assert.Equal(t, char1, char4) // Should wrap around

	// Test that different indices give different characters
	assert.NotEqual(t, char1, char2)
}

func TestShouldShowCommandDropdown(t *testing.T) {
	commands := GetAvailableCommands()

	tests := []struct {
		name         string
		input        string
		isProcessing bool
		expected     bool
	}{
		{
			name:         "slash command input not processing",
			input:        "/ba",
			isProcessing: false,
			expected:     true,
		},
		{
			name:         "slash command input while processing",
			input:        "/ba",
			isProcessing: true,
			expected:     false,
		},
		{
			name:         "complete command",
			input:        "/bash ls",
			isProcessing: false,
			expected:     false,
		},
		{
			name:         "not a command",
			input:        "hello",
			isProcessing: false,
			expected:     false,
		},
		{
			name:         "empty input",
			input:        "",
			isProcessing: false,
			expected:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ShouldShowCommandDropdown(tt.input, commands, tt.isProcessing)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFormatModelInfo(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		model    string
		expected string
	}{
		{
			name:     "anthropic model",
			provider: "anthropic",
			model:    "claude-sonnet-4-20250514",
			expected: "anthropic/claude-sonnet-4-20250514",
		},
		{
			name:     "openai model",
			provider: "openai",
			model:    "gpt-4o",
			expected: "openai/gpt-4o",
		},
		{
			name:     "google model",
			provider: "google",
			model:    "gemini-2.0-flash-exp",
			expected: "google/gemini-2.0-flash-exp",
		},
		{
			name:     "claude haiku",
			provider: "anthropic",
			model:    "claude-haiku-4-5-20251001",
			expected: "anthropic/claude-haiku-4-5-20251001",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatModelInfo(tt.provider, tt.model)
			assert.Equal(t, tt.expected, result)
		})
	}
}
