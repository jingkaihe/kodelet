package acceptance

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConversationListCommand(t *testing.T) {
	// Test conversation list command
	cmd := exec.Command("kodelet", "conversation", "list")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Failed to execute conversation list command")

	outputStr := strings.TrimSpace(string(output))

	// Should either show conversations or indicate no conversations found
	// The command should execute successfully regardless of whether conversations exist
	assert.False(t, strings.Contains(outputStr, "error") || strings.Contains(outputStr, "Error"), "Conversation list command returned error: %s", outputStr)
}

func TestConversationListWithOptions(t *testing.T) {
	// Test conversation list with sort options
	cmd := exec.Command("kodelet", "conversation", "list", "--sort-by", "updated", "--sort-order", "desc")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Failed to execute conversation list with options")

	outputStr := strings.TrimSpace(string(output))

	// Should execute successfully
	assert.False(t, strings.Contains(outputStr, "error") || strings.Contains(outputStr, "Error"), "Conversation list with options returned error: %s", outputStr)
}

func TestConversationShowInvalidID(t *testing.T) {
	// Test conversation show with invalid ID (should fail gracefully)
	cmd := exec.Command("kodelet", "conversation", "show", "invalid-id")
	output, err := cmd.CombinedOutput()

	// This should fail, but gracefully
	if err == nil {
		t.Log("Show invalid conversation succeeded (may be expected behavior)")
	}

	outputStr := strings.TrimSpace(string(output))

	// Should contain some form of error message about not finding the conversation
	assert.True(t, strings.Contains(outputStr, "not found") || strings.Contains(outputStr, "invalid") || strings.Contains(outputStr, "error"), "Expected error message for invalid conversation ID, got: %s", outputStr)
}

func TestConversationDeleteInvalidID(t *testing.T) {
	// Test conversation delete with invalid ID (should fail gracefully)
	cmd := exec.Command("kodelet", "conversation", "delete", "--no-confirm", "invalid-id")
	output, err := cmd.CombinedOutput()

	// This should fail, but gracefully
	if err == nil {
		t.Log("Delete invalid conversation succeeded (may be expected behavior)")
	}

	outputStr := strings.TrimSpace(string(output))

	// Should either succeed silently or provide appropriate error message
	// We don't want crashes or panics
	assert.False(t, strings.Contains(outputStr, "panic") || strings.Contains(outputStr, "fatal"), "Conversation delete command crashed: %s", outputStr)
}