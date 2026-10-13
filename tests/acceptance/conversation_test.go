package acceptance

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConversationListCommand(t *testing.T) {
	daemon := startCoreDaemon(t, "openai", "gpt-4o", []string{"OPENAI_API_KEY=unused-test-key"})
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := daemon.command(ctx, "conversation", "list")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Failed to execute conversation list command: %s", output)

	outputStr := strings.TrimSpace(string(output))

	assert.Contains(t, outputStr, "ID")
	assert.Contains(t, outputStr, "Summary")
	assert.False(t, strings.Contains(outputStr, "[ERROR]"), "Conversation list command returned error: %s", outputStr)
}

func TestConversationListWithOptions(t *testing.T) {
	daemon := startCoreDaemon(t, "openai", "gpt-4o", []string{"OPENAI_API_KEY=unused-test-key"})
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := daemon.command(ctx, "conversation", "list", "--sort-by", "updated_at", "--sort-order", "desc", "--json")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Failed to execute conversation list with options: %s", output)
	assert.JSONEq(t, `{"conversations": []}`, string(output))
}

func TestConversationShowInvalidID(t *testing.T) {
	daemon := startCoreDaemon(t, "openai", "gpt-4o", []string{"OPENAI_API_KEY=unused-test-key"})
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := daemon.command(ctx, "conversation", "show", "invalid-id")
	output, err := cmd.CombinedOutput()
	require.Error(t, err, "Showing a missing conversation must fail: %s", output)
	assert.Contains(t, string(output), "conversation not found")
	assert.NotContains(t, string(output), "panic:")
}

func TestConversationDeleteInvalidID(t *testing.T) {
	daemon := startCoreDaemon(t, "openai", "gpt-4o", []string{"OPENAI_API_KEY=unused-test-key"})
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := daemon.command(ctx, "conversation", "delete", "--no-confirm", "invalid-id")
	output, err := cmd.CombinedOutput()
	// Deleting a missing conversation is idempotent.
	require.NoError(t, err, "Deleting a missing conversation must succeed: %s", output)
	assert.Contains(t, string(output), "Conversation invalid-id deleted successfully")
}
