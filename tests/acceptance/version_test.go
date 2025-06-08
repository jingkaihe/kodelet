package acceptance

import (
	"os/exec"
	"strings"
	"testing"
)

func TestVersionCommand(t *testing.T) {
	// Test version command
	cmd := exec.Command("../../bin/kodelet", "version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to execute version command: %v", err)
	}

	outputStr := strings.TrimSpace(string(output))
	
	// Version output should contain version information in JSON format
	// Expected to contain version and gitCommit fields
	if !strings.Contains(outputStr, "version") || !strings.Contains(outputStr, "gitCommit") {
		t.Errorf("Version output should contain version and gitCommit fields. Got: %s", outputStr)
	}
}

func TestVersionCommandHelp(t *testing.T) {
	// Test version --help to ensure the subcommand works
	cmd := exec.Command("../../bin/kodelet", "version", "--help")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to execute version --help: %v", err)
	}

	outputStr := strings.TrimSpace(string(output))
	
	// Help output should contain usage information
	if !strings.Contains(strings.ToLower(outputStr), "usage") && !strings.Contains(strings.ToLower(outputStr), "version") {
		t.Errorf("Version help should contain usage information. Got: %s", outputStr)
	}
}