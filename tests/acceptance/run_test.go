package acceptance

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestRunCommandNoAPIKey(t *testing.T) {
	// Clear API keys to test behavior without API keys
	originalAnthropic := os.Getenv("ANTHROPIC_API_KEY")
	originalOpenAI := os.Getenv("OPENAI_API_KEY")
	
	defer func() {
		os.Setenv("ANTHROPIC_API_KEY", originalAnthropic)
		os.Setenv("OPENAI_API_KEY", originalOpenAI)
	}()
	
	os.Unsetenv("ANTHROPIC_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")
	
	// Test run command without API keys
	cmd := exec.Command("../../bin/kodelet", "run", "test query")
	cmd.Env = []string{} // Clear environment
	output, err := cmd.CombinedOutput()
	
	outputStr := strings.TrimSpace(string(output))
	
	// Command should either fail gracefully or provide meaningful output about missing configuration
	// We test that it doesn't crash and provides some useful feedback
	if strings.Contains(outputStr, "panic") || strings.Contains(outputStr, "fatal") {
		t.Errorf("Command should not panic or crash without API key: %s", outputStr)
	}
	
	// Should either succeed with a warning/error message or fail gracefully
	if err != nil && !strings.Contains(strings.ToLower(outputStr), "error") && 
	   !strings.Contains(strings.ToLower(outputStr), "home") {
		t.Errorf("If command fails, it should provide meaningful error message: %s", outputStr)
	}
}

func TestRunCommandHelp(t *testing.T) {
	// Test run command help
	cmd := exec.Command("../../bin/kodelet", "run", "--help")
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
	// Clear API keys to test flag parsing
	originalAnthropic := os.Getenv("ANTHROPIC_API_KEY")
	originalOpenAI := os.Getenv("OPENAI_API_KEY")
	
	defer func() {
		os.Setenv("ANTHROPIC_API_KEY", originalAnthropic)
		os.Setenv("OPENAI_API_KEY", originalOpenAI)
	}()
	
	os.Unsetenv("ANTHROPIC_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")
	
	// Test run command with --no-save flag
	cmd := exec.Command("../../bin/kodelet", "run", "--no-save", "test query")
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
	cmd := exec.Command("../../bin/kodelet", "run", "--invalid-flag", "test query")
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