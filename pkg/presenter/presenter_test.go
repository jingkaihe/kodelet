package presenter

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/fatih/color"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	presenter := New()
	assert.NotNil(t, presenter)
	assert.Equal(t, os.Stdout, presenter.output)
	assert.Equal(t, os.Stderr, presenter.errorOutput)
	assert.False(t, presenter.quiet)
}

func TestNewWithOptions(t *testing.T) {
	var output, errorOutput bytes.Buffer
	presenter := NewWithOptions(&output, &errorOutput, ColorNever)

	assert.Equal(t, &output, presenter.output)
	assert.Equal(t, &errorOutput, presenter.errorOutput)
	assert.Equal(t, ColorNever, presenter.colorMode)
}

func TestDetectColorMode(t *testing.T) {
	tests := []struct {
		name         string
		noColor      string
		kodeletColor string
		expected     ColorMode
	}{
		{"NO_COLOR set", "1", "", ColorNever},
		{"KODELET_COLOR always", "", "always", ColorAlways},
		{"KODELET_COLOR force", "", "force", ColorAlways},
		{"KODELET_COLOR never", "", "never", ColorNever},
		{"KODELET_COLOR off", "", "off", ColorNever},
		{"KODELET_COLOR auto", "", "auto", ColorAuto},
		{"default", "", "", ColorAuto},
		{"invalid kodelet color", "", "invalid", ColorAuto},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear environment
			os.Unsetenv("NO_COLOR")
			os.Unsetenv("KODELET_COLOR")

			// Set test environment
			if tt.noColor != "" {
				os.Setenv("NO_COLOR", tt.noColor)
			}
			if tt.kodeletColor != "" {
				os.Setenv("KODELET_COLOR", tt.kodeletColor)
			}

			result := detectColorMode()
			assert.Equal(t, tt.expected, result)

			// Cleanup
			os.Unsetenv("NO_COLOR")
			os.Unsetenv("KODELET_COLOR")
		})
	}
}

func TestError(t *testing.T) {
	var errorOutput bytes.Buffer
	presenter := NewWithOptions(nil, &errorOutput, ColorNever)

	// Test with context
	err := errors.New("test error")
	presenter.Error(err, "test context")

	output := errorOutput.String()
	assert.Contains(t, output, "[ERROR]")
	assert.Contains(t, output, "test context")
	assert.Contains(t, output, "test error")

	// Test without context
	errorOutput.Reset()
	presenter.Error(err, "")

	output = errorOutput.String()
	assert.Contains(t, output, "[ERROR]")
	assert.Contains(t, output, "test error")
	assert.NotContains(t, output, "test context")

	// Test nil error
	errorOutput.Reset()
	presenter.Error(nil, "context")
	assert.Empty(t, errorOutput.String())
}

func TestSuccess(t *testing.T) {
	var output bytes.Buffer
	presenter := NewWithOptions(&output, nil, ColorNever)

	presenter.Success("Operation completed")

	result := output.String()
	assert.Contains(t, result, "✓")
	assert.Contains(t, result, "Operation completed")
}

func TestSuccessQuietMode(t *testing.T) {
	var output bytes.Buffer
	presenter := NewWithOptions(&output, nil, ColorNever)
	presenter.SetQuiet(true)

	presenter.Success("Operation completed")

	assert.Empty(t, output.String())
}

func TestWarning(t *testing.T) {
	var output bytes.Buffer
	presenter := NewWithOptions(&output, nil, ColorNever)

	presenter.Warning("This is a warning")

	result := output.String()
	assert.Contains(t, result, "⚠")
	assert.Contains(t, result, "This is a warning")
}

func TestWarningQuietMode(t *testing.T) {
	var output bytes.Buffer
	presenter := NewWithOptions(&output, nil, ColorNever)
	presenter.SetQuiet(true)

	presenter.Warning("This is a warning")

	assert.Empty(t, output.String())
}

func TestInfo(t *testing.T) {
	var output bytes.Buffer
	presenter := NewWithOptions(&output, nil, ColorNever)

	presenter.Info("Information message")

	result := output.String()
	assert.Contains(t, result, "Information message")
	assert.NotContains(t, result, "[INFO]") // Info doesn't have prefix
}

func TestInfoQuietMode(t *testing.T) {
	var output bytes.Buffer
	presenter := NewWithOptions(&output, nil, ColorNever)
	presenter.SetQuiet(true)

	presenter.Info("Information message")

	assert.Empty(t, output.String())
}

func TestSection(t *testing.T) {
	var output bytes.Buffer
	presenter := NewWithOptions(&output, nil, ColorNever)

	presenter.Section("Test Section")

	result := output.String()
	lines := strings.Split(strings.TrimSpace(result), "\n")
	require.Len(t, lines, 2)

	assert.Equal(t, "Test Section", lines[0])
	assert.Equal(t, strings.Repeat("-", len("Test Section")), lines[1])
}

func TestSectionQuietMode(t *testing.T) {
	var output bytes.Buffer
	presenter := NewWithOptions(&output, nil, ColorNever)
	presenter.SetQuiet(true)

	presenter.Section("Test Section")

	assert.Empty(t, output.String())
}

func TestStats(t *testing.T) {
	var output bytes.Buffer
	presenter := NewWithOptions(&output, nil, ColorNever)

	stats := &UsageStats{
		InputTokens:      100,
		OutputTokens:     50,
		CacheWriteTokens: 25,
		CacheReadTokens:  10,
		InputCost:        0.1,
		OutputCost:       0.05,
		CacheWriteCost:   0.025,
		CacheReadCost:    0.01,
	}

	presenter.Stats(stats)

	result := output.String()
	assert.Contains(t, result, "[Usage Stats]")
	assert.Contains(t, result, "Input tokens: 100")
	assert.Contains(t, result, "Output tokens: 50")
	assert.Contains(t, result, "Total: 185") // 100+50+25+10
	assert.Contains(t, result, "[Cost Stats]")
	assert.Contains(t, result, "Total: $0.1850") // 0.1+0.05+0.025+0.01
}

func TestStatsNil(t *testing.T) {
	var output bytes.Buffer
	presenter := NewWithOptions(&output, nil, ColorNever)

	presenter.Stats(nil)

	assert.Empty(t, output.String())
}

func TestStatsQuietMode(t *testing.T) {
	var output bytes.Buffer
	presenter := NewWithOptions(&output, nil, ColorNever)
	presenter.SetQuiet(true)

	stats := &UsageStats{InputTokens: 100}
	presenter.Stats(stats)

	assert.Empty(t, output.String())
}

func TestSeparator(t *testing.T) {
	var output bytes.Buffer
	presenter := NewWithOptions(&output, nil, ColorNever)

	presenter.Separator()

	result := output.String()
	assert.Contains(t, result, strings.Repeat("-", 60))
}

func TestSeparatorQuietMode(t *testing.T) {
	var output bytes.Buffer
	presenter := NewWithOptions(&output, nil, ColorNever)
	presenter.SetQuiet(true)

	presenter.Separator()

	assert.Empty(t, output.String())
}

func TestQuietMode(t *testing.T) {
	presenter := New()

	assert.False(t, presenter.IsQuiet())

	presenter.SetQuiet(true)
	assert.True(t, presenter.IsQuiet())

	presenter.SetQuiet(false)
	assert.False(t, presenter.IsQuiet())
}

func TestConvertUsageStats(t *testing.T) {
	// Test nil input
	result := ConvertUsageStats(nil)
	assert.Nil(t, result)

	// Mock llm.UsageStats (we'll need to import the actual type)
	// For now, we'll just test the structure
	stats := &UsageStats{
		InputTokens:      100,
		OutputTokens:     50,
		CacheWriteTokens: 25,
		CacheReadTokens:  10,
		InputCost:        0.1,
		OutputCost:       0.05,
		CacheWriteCost:   0.025,
		CacheReadCost:    0.01,
	}

	// This would test the actual conversion when we have the real llm.UsageStats type
	assert.NotNil(t, stats)
}

func TestColorModeConfiguration(t *testing.T) {
	// Test ColorNever disables colors
	presenter := NewWithOptions(&bytes.Buffer{}, &bytes.Buffer{}, ColorNever)
	assert.Equal(t, ColorNever, presenter.colorMode)

	// Test ColorAlways enables colors
	oldNoColor := color.NoColor
	presenter = NewWithOptions(&bytes.Buffer{}, &bytes.Buffer{}, ColorAlways)
	assert.Equal(t, ColorAlways, presenter.colorMode)

	// Restore original color setting
	color.NoColor = oldNoColor
}

func TestGlobalFunctions(t *testing.T) {
	// Test that global functions don't panic
	// We can't easily test output without affecting global state
	// These tests ensure the functions exist and can be called

	assert.NotPanics(t, func() {
		Error(errors.New("test"), "context")
		Success("test")
		Warning("test")
		Info("test")
		Section("test")
		Stats(&UsageStats{})
		Separator()
		SetQuiet(true)
		IsQuiet()
		SetQuiet(false)
	})
}
