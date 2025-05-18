package sysprompt

import (
	"strings"
	"testing"
)

// TestSubAgentPrompt verifies that key elements from templates appear in the generated subagent prompt
func TestSubAgentPrompt(t *testing.T) {
	// Generate a subagent prompt
	prompt := SubAgentPrompt("claude-3-sonnet-20240229")

	// Define expected fragments that should appear in the prompt
	expectedFragments := []string{
		// Main introduction
		"You are an AI SWE Agent",
		"open ended code search, architecture analysis",

		// Tone and style sections
		"Tone and Style",
		"Be concise, direct and to the point",

		// Tool usage section
		"Tool Usage",
		"invoke multiple INDEPENDENT tools",

		// Task management section
		"Task Management",
		"todo_write",
		"todo_read",

		// System information section
		"System Information",
		"Current working directory",
		"Operating system",
	}

	// Verify each fragment appears in the prompt
	for _, fragment := range expectedFragments {
		if !strings.Contains(prompt, fragment) {
			t.Errorf("Expected subagent prompt to contain: %q", fragment)
		}
	}
}
