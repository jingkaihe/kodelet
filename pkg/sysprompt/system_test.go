package sysprompt

import (
	"strings"
	"testing"
)

// TestSystemPrompt verifies that key elements from templates appear in the generated system prompt
func TestSystemPrompt(t *testing.T) {
	// Generate a system prompt
	prompt := SystemPrompt("claude-3-sonnet-20240229")

	// Define expected fragments that should appear in the prompt
	expectedFragments := []string{
		// Main introduction
		"You are an interactive CLI tool",
		
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
		
		// Context section
		"Context",
		"KODELET.md",
		
		// System information section
		"System Information",
		"Current working directory",
		"Operating system",
	}

	// Verify each fragment appears in the prompt
	for _, fragment := range expectedFragments {
		if !strings.Contains(prompt, fragment) {
			t.Errorf("Expected system prompt to contain: %q", fragment)
		}
	}
}