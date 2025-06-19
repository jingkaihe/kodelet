package sysprompt

import (
	"strings"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/jingkaihe/kodelet/pkg/types/llm"
)

// TestSubAgentPrompt verifies that key elements from templates appear in the generated subagent prompt
func TestSubAgentPrompt(t *testing.T) {
	// Generate a subagent prompt
	prompt := SubAgentPrompt("claude-3-sonnet-20240229", llm.Config{})

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

// TestSubAgentPromptBashBannedCommands verifies that banned commands appear in the default subagent prompt
func TestSubAgentPromptBashBannedCommands(t *testing.T) {
	prompt := SubAgentPrompt("claude-3-sonnet-20240229", llm.Config{})

	// Should contain bash command restrictions section
	if !strings.Contains(prompt, "Bash Command Restrictions") {
		t.Error("Expected subagent prompt to contain 'Bash Command Restrictions' section")
	}

	// Should contain banned commands section (default behavior)
	if !strings.Contains(prompt, "Banned Commands") {
		t.Error("Expected subagent prompt to contain 'Banned Commands' section")
	}

	// Should NOT contain allowed commands section in default mode
	if strings.Contains(prompt, "Allowed Commands") {
		t.Error("Did not expect subagent prompt to contain 'Allowed Commands' section in default mode")
	}

	// Verify all banned commands from tools package are present
	for _, bannedCmd := range tools.BannedCommands {
		if !strings.Contains(prompt, bannedCmd) {
			t.Errorf("Expected subagent prompt to contain banned command: %q", bannedCmd)
		}
	}
}

// TestSubAgentPromptBashAllowedCommands verifies that allowed commands work correctly in subagent prompts
func TestSubAgentPromptBashAllowedCommands(t *testing.T) {
	// Create a prompt context with allowed commands
	promptCtx := NewPromptContext()
	config := NewDefaultConfig().WithModel("claude-3-sonnet-20240229")
	allowedCommands := []string{"find *", "grep *", "cat *", "head *", "tail *"}
	llmConfig := &llm.Config{
		AllowedCommands: allowedCommands,
	}

	updateContextWithConfig(promptCtx, config)
	promptCtx.BashAllowedCommands = llmConfig.AllowedCommands

	renderer := NewRenderer(TemplateFS)
	prompt, err := renderer.RenderSubagentPrompt(promptCtx)
	if err != nil {
		t.Fatalf("Failed to render subagent prompt: %v", err)
	}

	// Should contain bash command restrictions section
	if !strings.Contains(prompt, "Bash Command Restrictions") {
		t.Error("Expected subagent prompt to contain 'Bash Command Restrictions' section")
	}

	// Should contain allowed commands section
	if !strings.Contains(prompt, "Allowed Commands") {
		t.Error("Expected subagent prompt to contain 'Allowed Commands' section")
	}

	// Should NOT contain banned commands section when allowed commands are set
	if strings.Contains(prompt, "Banned Commands") {
		t.Error("Did not expect subagent prompt to contain 'Banned Commands' section when allowed commands are configured")
	}

	// Verify all allowed commands are present
	for _, allowedCmd := range allowedCommands {
		if !strings.Contains(prompt, allowedCmd) {
			t.Errorf("Expected subagent prompt to contain allowed command: %q", allowedCmd)
		}
	}

	// Should contain the rejection message
	if !strings.Contains(prompt, "Commands not matching these patterns will be rejected") {
		t.Error("Expected subagent prompt to contain rejection message for non-matching commands")
	}
}

// TestSubAgentPromptContextConsistency verifies that both system and subagent prompts have consistent bash restrictions
func TestSubAgentPromptContextConsistency(t *testing.T) {
	// Test that both system and subagent prompts render the same bash restrictions with the same context
	promptCtx := NewPromptContext()
	config := NewDefaultConfig().WithModel("claude-3-sonnet-20240229")
	allowedCommands := []string{"test *", "verify *"}
	llmConfig := &llm.Config{
		AllowedCommands: allowedCommands,
	}

	updateContextWithConfig(promptCtx, config)
	promptCtx.BashAllowedCommands = llmConfig.AllowedCommands

	renderer := NewRenderer(TemplateFS)

	systemPrompt, err := renderer.RenderSystemPrompt(promptCtx)
	if err != nil {
		t.Fatalf("Failed to render system prompt: %v", err)
	}

	subagentPrompt, err := renderer.RenderSubagentPrompt(promptCtx)
	if err != nil {
		t.Fatalf("Failed to render subagent prompt: %v", err)
	}

	// Both should contain the same allowed commands
	for _, allowedCmd := range allowedCommands {
		if !strings.Contains(systemPrompt, allowedCmd) {
			t.Errorf("Expected system prompt to contain allowed command: %q", allowedCmd)
		}
		if !strings.Contains(subagentPrompt, allowedCmd) {
			t.Errorf("Expected subagent prompt to contain allowed command: %q", allowedCmd)
		}
	}

	// Both should contain allowed commands section and NOT banned commands section
	if !strings.Contains(systemPrompt, "Allowed Commands") {
		t.Error("Expected system prompt to contain 'Allowed Commands' section")
	}
	if !strings.Contains(subagentPrompt, "Allowed Commands") {
		t.Error("Expected subagent prompt to contain 'Allowed Commands' section")
	}

	if strings.Contains(systemPrompt, "Banned Commands") {
		t.Error("Did not expect system prompt to contain 'Banned Commands' section when allowed commands are configured")
	}
	if strings.Contains(subagentPrompt, "Banned Commands") {
		t.Error("Did not expect subagent prompt to contain 'Banned Commands' section when allowed commands are configured")
	}
}

