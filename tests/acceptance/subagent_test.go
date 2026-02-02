package acceptance

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSubagentFlagParsing(t *testing.T) {
	// Test combined flags - if parsing works for combined flags, individual flags work too
	args := []string{"run", "--as-subagent", "--result-only", "--no-tools", "--no-save", "test query"}

	cmd := exec.Command("kodelet", args...)
	cmd.Env = []string{} // Clear environment to trigger missing API key
	output, _ := cmd.CombinedOutput()

	outputStr := strings.TrimSpace(string(output))

	// Should not fail due to flag parsing error
	assert.False(t, strings.Contains(outputStr, "unknown flag"), "Flag parsing failed: %s", outputStr)
	assert.False(t, strings.Contains(outputStr, "invalid flag"), "Flag parsing failed: %s", outputStr)

	// Should not crash or panic
	assert.False(t, strings.Contains(outputStr, "panic"), "Command should not panic: %s", outputStr)
	assert.False(t, strings.Contains(outputStr, "fatal"), "Command should not crash: %s", outputStr)
}

func TestSubagentModes(t *testing.T) {
	// Skip if no API key is available
	if os.Getenv("ANTHROPIC_API_KEY") == "" && os.Getenv("OPENAI_API_KEY") == "" && os.Getenv("GOOGLE_API_KEY") == "" {
		t.Skip("Skipping test: no API key configured")
	}

	tests := []struct {
		name              string
		flags             []string
		query             string
		expectCleanOutput bool // no usage stats with --result-only
		expectNoToolUse   bool // no tool calls with --no-tools
	}{
		{
			name:              "as-subagent with result-only",
			flags:             []string{"--as-subagent", "--result-only", "--no-save"},
			query:             "What is 2+2? Reply with just the number.",
			expectCleanOutput: true,
		},
		{
			name:            "no-tools mode",
			flags:           []string{"--no-tools", "--no-save"},
			query:           "What is the capital of France? Reply with just the city name.",
			expectNoToolUse: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"run"}, test.flags...)
			args = append(args, test.query)

			cmd := exec.Command("kodelet", args...)
			output, err := cmd.CombinedOutput()

			outputStr := strings.TrimSpace(string(output))
			t.Logf("Output: %s", outputStr)

			// Should not crash
			assert.False(t, strings.Contains(outputStr, "panic"), "Command should not panic: %s", outputStr)

			// Skip validation if command failed due to missing API keys
			if err != nil && (strings.Contains(outputStr, "API key") || strings.Contains(outputStr, "api key") || strings.Contains(outputStr, "API_KEY")) {
				t.Skipf("Skipping test due to API key issue: %v", err)
				return
			}

			// Should produce a response
			assert.True(t, len(outputStr) > 0, "Should produce some output")

			if test.expectCleanOutput {
				assert.False(t, strings.Contains(outputStr, "Input tokens:"), "Result-only should not show token counts")
			}

			if test.expectNoToolUse {
				assert.False(t, strings.Contains(outputStr, "Using tool:"), "No-tools should not attempt tool usage")
			}
		})
	}
}
