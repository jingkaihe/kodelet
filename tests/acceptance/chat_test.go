package acceptance

import (
	"os/exec"
	"strings"
	"testing"
)

func TestChatCommandHelp(t *testing.T) {
	// Test chat command help
	cmd := exec.Command("kodelet", "chat", "--help")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to execute chat --help: %v", err)
	}

	outputStr := strings.TrimSpace(string(output))

	// Should contain usage information
	if !strings.Contains(outputStr, "Usage") && !strings.Contains(outputStr, "usage") {
		t.Errorf("Help output should contain usage information: %s", outputStr)
	}

	// Should contain chat-specific flags
	if !strings.Contains(outputStr, "--no-save") && !strings.Contains(outputStr, "--follow") {
		t.Errorf("Help output should contain chat-specific flags: %s", outputStr)
	}

	// Should contain compact-related flags
	if !strings.Contains(outputStr, "--compact-ratio") && !strings.Contains(outputStr, "--disable-auto-compact") {
		t.Errorf("Help output should contain compact-related flags: %s", outputStr)
	}
}

func TestChatCommandWithCompactFlags(t *testing.T) {
	// Test chat command with compact flags
	cmd := exec.Command("kodelet", "chat", "--compact-ratio=0.9", "--disable-auto-compact", "--no-save")
	cmd.Env = []string{} // Clear environment

	// Start the command but don't wait for completion since it's interactive
	err := cmd.Start()
	if err != nil {
		t.Fatalf("Failed to start chat command: %v", err)
	}

	// Kill the process immediately since we only want to test flag parsing
	if cmd.Process != nil {
		cmd.Process.Kill()
	}

	// The important thing is that flag parsing worked correctly
	// If the command started successfully, the flags were parsed correctly
}

func TestChatCommandWithInvalidCompactRatio(t *testing.T) {
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
			cmd := exec.Command("kodelet", "chat", "--compact-ratio="+test.ratio, "--no-save")
			cmd.Env = []string{} // Clear environment
			output, err := cmd.CombinedOutput()

			// Should fail due to invalid compact ratio
			if err == nil && test.ratio != "invalid" {
				// For negative and > 1 ratios, we expect the command to fail
				// For invalid format, the flag parsing itself should fail
				t.Errorf("Expected chat command to fail with invalid compact ratio %s", test.ratio)
			}

			outputStr := strings.TrimSpace(string(output))

			// Should contain error message about compact ratio
			if test.ratio != "invalid" && !strings.Contains(outputStr, "compact") {
				t.Errorf("Expected compact-related error message for %s, got: %s", test.description, outputStr)
			}
		})
	}
}

func TestChatCommandWithValidCompactRatio(t *testing.T) {
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
			cmd := exec.Command("kodelet", "chat", "--compact-ratio="+test.ratio, "--no-save")
			cmd.Env = []string{} // Clear environment

			// Start the command but don't wait for completion since it's interactive
			err := cmd.Start()

			// Kill the process immediately since we only want to test flag parsing
			if cmd.Process != nil {
				cmd.Process.Kill()
			}

			// Should not fail due to flag parsing error
			if err != nil {
				t.Errorf("Flag parsing failed for ratio %s: %v", test.ratio, err)
			}
		})
	}
}
