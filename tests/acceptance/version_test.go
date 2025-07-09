package acceptance

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVersionCommand(t *testing.T) {
	// Test version command
	cmd := exec.Command("kodelet", "version")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Failed to execute version command")

	outputStr := strings.TrimSpace(string(output))

	// Version output should contain version information in JSON format
	// Expected to contain version and gitCommit fields
	assert.True(t, strings.Contains(outputStr, "version") && strings.Contains(outputStr, "gitCommit"), "Version output should contain version and gitCommit fields. Got: %s", outputStr)
}

func TestVersionCommandHelp(t *testing.T) {
	// Test version --help to ensure the subcommand works
	cmd := exec.Command("kodelet", "version", "--help")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Failed to execute version --help")

	outputStr := strings.TrimSpace(string(output))

	// Help output should contain usage information
	assert.True(t, strings.Contains(strings.ToLower(outputStr), "usage") || strings.Contains(strings.ToLower(outputStr), "version"), "Version help should contain usage information. Got: %s", outputStr)
}
