package acceptance

import (
	"os/exec"
	"strings"
	"testing"
)

func TestRunCommandHelp(t *testing.T) {
	// Test run command help
	cmd := exec.Command("kodelet", "run", "--help")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to execute run --help: %v", err)
	}

	outputStr := strings.TrimSpace(string(output))

	// Should contain usage information
	if !strings.Contains(outputStr, "Usage") && !strings.Contains(outputStr, "usage") {
		t.Errorf("Help output should contain usage information: %s", outputStr)
	}

	// Should contain run-specific flags
	if !strings.Contains(outputStr, "--no-save") && !strings.Contains(outputStr, "--follow") {
		t.Errorf("Help output should contain run-specific flags: %s", outputStr)
	}
}

func TestRunCommandWithNoSaveFlag(t *testing.T) {
	// Test run command with --no-save flag
	cmd := exec.Command("kodelet", "run", "--no-save", "test query")
	cmd.Env = []string{} // Clear environment
	output, _ := cmd.CombinedOutput()

	outputStr := strings.TrimSpace(string(output))

	// The important thing is that flag parsing works correctly
	// Should not fail due to flag parsing error
	if strings.Contains(outputStr, "unknown flag") || strings.Contains(outputStr, "invalid flag") {
		t.Errorf("Flag parsing failed: %s", outputStr)
	}

	// Should not crash or panic
	if strings.Contains(outputStr, "panic") || strings.Contains(outputStr, "fatal") {
		t.Errorf("Command should not panic or crash: %s", outputStr)
	}
}

func TestRunCommandWithInvalidFlags(t *testing.T) {
	// Test run command with invalid flag
	cmd := exec.Command("kodelet", "run", "--invalid-flag", "test query")
	output, err := cmd.CombinedOutput()

	// Should fail due to invalid flag
	if err == nil {
		t.Error("Expected run command to fail with invalid flag")
	}

	outputStr := strings.TrimSpace(string(output))

	// Should contain flag-related error
	if !strings.Contains(outputStr, "flag") && !strings.Contains(outputStr, "unknown") {
		t.Errorf("Expected flag-related error message, got: %s", outputStr)
	}
}
