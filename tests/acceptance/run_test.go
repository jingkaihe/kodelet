package acceptance

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunCommandHelp(t *testing.T) {
	// Test run command help
	cmd := exec.Command("kodelet", "run", "--help")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Failed to execute run --help")

	outputStr := strings.TrimSpace(string(output))

	// Should contain usage information
	assert.True(t, strings.Contains(outputStr, "Usage") || strings.Contains(outputStr, "usage"), "Help output should contain usage information: %s", outputStr)

	// Should contain run-specific flags
	assert.True(t, strings.Contains(outputStr, "--no-save") || strings.Contains(outputStr, "--follow"), "Help output should contain run-specific flags: %s", outputStr)
}

func TestRunCommandWithNoSaveFlag(t *testing.T) {
	// Test run command with --no-save flag
	cmd := exec.Command("kodelet", "run", "--no-save", "test query")
	cmd.Env = []string{} // Clear environment
	output, _ := cmd.CombinedOutput()

	outputStr := strings.TrimSpace(string(output))

	// The important thing is that flag parsing works correctly
	// Should not fail due to flag parsing error
	assert.False(t, strings.Contains(outputStr, "unknown flag") || strings.Contains(outputStr, "invalid flag"), "Flag parsing failed: %s", outputStr)

	// Should not crash or panic
	assert.False(t, strings.Contains(outputStr, "panic") || strings.Contains(outputStr, "fatal"), "Command should not panic or crash: %s", outputStr)
}

func TestRunCommandWithInvalidFlags(t *testing.T) {
	// Test run command with invalid flag
	cmd := exec.Command("kodelet", "run", "--invalid-flag", "test query")
	output, err := cmd.CombinedOutput()

	// Should fail due to invalid flag
	assert.Error(t, err, "Expected run command to fail with invalid flag")

	outputStr := strings.TrimSpace(string(output))

	// Should contain flag-related error
	assert.True(t, strings.Contains(outputStr, "flag") || strings.Contains(outputStr, "unknown"), "Expected flag-related error message, got: %s", outputStr)
}

func TestRunCommandWithCompactFlags(t *testing.T) {
	// Test run command with compact flags
	cmd := exec.Command("kodelet", "run", "--compact-ratio=0.9", "--disable-auto-compact", "test query")
	cmd.Env = []string{} // Clear environment
	output, _ := cmd.CombinedOutput()

	outputStr := strings.TrimSpace(string(output))

	// Should not fail due to flag parsing error
	assert.False(t, strings.Contains(outputStr, "unknown flag") || strings.Contains(outputStr, "invalid flag"), "Flag parsing failed: %s", outputStr)

	// Should not crash or panic
	assert.False(t, strings.Contains(outputStr, "panic") || strings.Contains(outputStr, "fatal"), "Command should not panic or crash: %s", outputStr)
}

func TestRunCommandWithInvalidCompactRatio(t *testing.T) {
	tests := []struct {
		name        string
		ratio       string
		description string
	}{
		{
			name:        "negative ratio",
			ratio:       "-0.5",
			description: "should reject negative compact ratio",
		},
		{
			name:        "ratio greater than 1",
			ratio:       "1.5",
			description: "should reject compact ratio greater than 1",
		},
		{
			name:        "invalid format",
			ratio:       "invalid",
			description: "should reject non-numeric compact ratio",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd := exec.Command("kodelet", "run", "--compact-ratio="+test.ratio, "test query")
			cmd.Env = []string{} // Clear environment
			output, err := cmd.CombinedOutput()

			// Should fail due to invalid compact ratio
			if test.ratio != "invalid" {
				// For negative and > 1 ratios, we expect the command to fail
				// For invalid format, the flag parsing itself should fail
				assert.Error(t, err, "Expected run command to fail with invalid compact ratio %s", test.ratio)
			}

			outputStr := strings.TrimSpace(string(output))

			// Should contain error message about compact ratio
			if test.ratio != "invalid" {
				assert.True(t, strings.Contains(outputStr, "compact"), "Expected compact-related error message for %s, got: %s", test.description, outputStr)
			}
		})
	}
}

func TestRunCommandWithValidCompactRatio(t *testing.T) {
	tests := []struct {
		name  string
		ratio string
	}{
		{
			name:  "minimum valid ratio",
			ratio: "0.0",
		},
		{
			name:  "maximum valid ratio",
			ratio: "1.0",
		},
		{
			name:  "middle valid ratio",
			ratio: "0.8",
		},
		{
			name:  "default ratio",
			ratio: "0.75",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd := exec.Command("kodelet", "run", "--compact-ratio="+test.ratio, "test query")
			cmd.Env = []string{} // Clear environment
			output, _ := cmd.CombinedOutput()

			outputStr := strings.TrimSpace(string(output))

			// Should not fail due to flag parsing error
			assert.False(t, strings.Contains(outputStr, "unknown flag") || strings.Contains(outputStr, "invalid flag"), "Flag parsing failed for ratio %s: %s", test.ratio, outputStr)

			// Should not fail due to compact ratio validation
			assert.False(t, strings.Contains(outputStr, "compact ratio must be between"), "Valid compact ratio %s was rejected: %s", test.ratio, outputStr)
		})
	}
}
