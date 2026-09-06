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
	assert.Contains(t, outputStr, "--follow")
	assert.Contains(t, outputStr, "--server")
	assert.NotContains(t, outputStr, "--no-save")
}

func TestRunCommandRejectsRemovedNoSaveFlag(t *testing.T) {
	for _, flag := range []string{"--no-save", "--no-save=false"} {
		t.Run(flag, func(t *testing.T) {
			cmd := exec.Command("kodelet", "run", flag, "test query")
			cmd.Env = commandEnv()
			output, err := cmd.CombinedOutput()
			require.Error(t, err)
			assert.Contains(t, string(output), "unknown flag: --no-save")
			assert.NotContains(t, string(output), "panic:")
		})
	}
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

func TestRunCommandRejectsClientCompactRatio(t *testing.T) {
	// Compaction policy belongs to the daemon, including valid numeric values.
	for _, ratio := range []string{"0.1", "0.8", "1.0", "0.0", "-0.5", "1.5", "invalid"} {
		t.Run(ratio, func(t *testing.T) {
			cmd := exec.Command("kodelet", "run", "--compact-ratio="+ratio, "test query")
			cmd.Env = commandEnv()
			output, err := cmd.CombinedOutput()
			require.Error(t, err)
			if ratio == "invalid" {
				assert.Contains(t, string(output), `invalid argument "invalid" for "--compact-ratio" flag`)
			} else {
				assert.Contains(t, string(output), "--compact-ratio is not yet supported by daemon-backed run; configure it on the owning daemon or runner")
			}
		})
	}
}
