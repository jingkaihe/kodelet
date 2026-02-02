package acceptance

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubagentFlagParsing(t *testing.T) {
	tests := []struct {
		name  string
		flags []string
	}{
		{
			name:  "as-subagent flag",
			flags: []string{"--as-subagent"},
		},
		{
			name:  "no-tools flag",
			flags: []string{"--no-tools"},
		},
		{
			name:  "result-only flag",
			flags: []string{"--result-only"},
		},
		{
			name:  "combined subagent flags",
			flags: []string{"--as-subagent", "--result-only"},
		},
		{
			name:  "all subagent-related flags",
			flags: []string{"--as-subagent", "--result-only", "--no-save"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"run"}, test.flags...)
			args = append(args, "test query")

			cmd := exec.Command("kodelet", args...)
			cmd.Env = []string{} // Clear environment to trigger missing API key
			output, _ := cmd.CombinedOutput()

			outputStr := strings.TrimSpace(string(output))

			// Should not fail due to flag parsing error
			assert.False(t, strings.Contains(outputStr, "unknown flag"), "Flag parsing failed for %v: %s", test.flags, outputStr)
			assert.False(t, strings.Contains(outputStr, "invalid flag"), "Flag parsing failed for %v: %s", test.flags, outputStr)

			// Should not crash or panic
			assert.False(t, strings.Contains(outputStr, "panic"), "Command should not panic with flags %v: %s", test.flags, outputStr)
			assert.False(t, strings.Contains(outputStr, "fatal"), "Command should not crash with flags %v: %s", test.flags, outputStr)
		})
	}
}

func TestNoToolsFlagHelp(t *testing.T) {
	cmd := exec.Command("kodelet", "run", "--help")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Failed to execute run --help")

	outputStr := string(output)

	// Verify the new flags are documented in help
	assert.Contains(t, outputStr, "--no-tools", "Help should document --no-tools flag")
	assert.Contains(t, outputStr, "--as-subagent", "Help should document --as-subagent flag")
	assert.Contains(t, outputStr, "--result-only", "Help should document --result-only flag")
}

func TestAsSubagentSimpleQuery(t *testing.T) {
	// Skip if no API key is available
	if os.Getenv("ANTHROPIC_API_KEY") == "" && os.Getenv("OPENAI_API_KEY") == "" && os.Getenv("GOOGLE_API_KEY") == "" {
		t.Skip("Skipping test: no API key configured")
	}

	// Test that --as-subagent with --result-only produces clean output
	cmd := exec.Command("kodelet", "run", "--as-subagent", "--result-only", "--no-save", "What is 2+2? Reply with just the number.")
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

	// With --result-only, output should be clean (no usage stats, no intermediate output)
	assert.False(t, strings.Contains(outputStr, "Usage:"), "Result-only should not show usage stats")
	assert.False(t, strings.Contains(outputStr, "Input tokens:"), "Result-only should not show token counts")

	// Should contain a response (the answer to 2+2)
	assert.True(t, len(outputStr) > 0, "Should produce some output")
}

func TestNoToolsSimpleQuery(t *testing.T) {
	// Skip if no API key is available
	if os.Getenv("ANTHROPIC_API_KEY") == "" && os.Getenv("OPENAI_API_KEY") == "" && os.Getenv("GOOGLE_API_KEY") == "" {
		t.Skip("Skipping test: no API key configured")
	}

	// Test that --no-tools works for simple query-response usage
	cmd := exec.Command("kodelet", "run", "--no-tools", "--no-save", "What is the capital of France? Reply with just the city name.")
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

	// With --no-tools, it shouldn't try to use any tools (no tool call output)
	assert.False(t, strings.Contains(outputStr, "Using tool:"), "No-tools should not attempt tool usage")
}

func TestSubagentPreventsSelfRecursion(t *testing.T) {
	// This test verifies that when running with --as-subagent, the subagent tool
	// is not available (to prevent infinite recursion)

	// Skip if no API key is available
	if os.Getenv("ANTHROPIC_API_KEY") == "" && os.Getenv("OPENAI_API_KEY") == "" && os.Getenv("GOOGLE_API_KEY") == "" {
		t.Skip("Skipping test: no API key configured")
	}

	// Ask a question that might trigger subagent usage in normal mode
	// With --as-subagent, this should NOT spawn another subagent
	cmd := exec.Command("kodelet", "run", "--as-subagent", "--result-only", "--no-save",
		"Search the codebase for 'TODO' comments and summarize them. If you cannot use tools, just say 'no tools available'.")
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

	// The subagent should NOT spawn another subagent (no "Subagent" tool output)
	assert.False(t, strings.Contains(outputStr, "Subagent:"), "Subagent should not spawn another subagent")
}

func TestCombinedSubagentFlags(t *testing.T) {
	// Test the typical subagent invocation pattern: --result-only --as-subagent --no-save

	// Skip if no API key is available
	if os.Getenv("ANTHROPIC_API_KEY") == "" && os.Getenv("OPENAI_API_KEY") == "" && os.Getenv("GOOGLE_API_KEY") == "" {
		t.Skip("Skipping test: no API key configured")
	}

	cmd := exec.Command("kodelet", "run", "--result-only", "--as-subagent", "--no-save", "Say 'hello world'")
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

	// Should succeed and produce output
	assert.NoError(t, err, "Combined subagent flags should work together")
	assert.True(t, len(outputStr) > 0, "Should produce some output")

	// Output should be clean (result-only)
	assert.False(t, strings.Contains(outputStr, "Input tokens:"), "Result-only should suppress token stats")
}
