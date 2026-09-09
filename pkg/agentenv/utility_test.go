package agentenv

import (
	"context"
	"testing"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUtilityEnvironmentHasNoWorkspaceOrTools(t *testing.T) {
	environment := &UtilityEnvironment{}
	assert.False(t, environment.IsOpen())
	manifest, err := environment.Open(t.Context(), RunSpec{Config: llmtypes.Config{
		WorkingDirectory: "/nonexistent/runner/workspace", Extensions: "must not be inspected", AllowedTools: []string{"bash"}, Sysprompt: "/nonexistent/prompt",
	}})
	require.NoError(t, err, "utility environment must ignore workspace configuration")
	assert.True(t, environment.IsOpen())
	assert.Empty(t, manifest)
	assert.Empty(t, environment.Manifest())
	assert.False(t, environment.CanStreamToolUpdates())
	message, err := environment.ProcessUserMessage(t.Context(), "complete in-memory input")
	require.NoError(t, err)
	assert.Equal(t, "complete in-memory input", message)
	init, err := environment.ProcessAgentInit(t.Context(), "central prompt", []string{"bash"})
	require.NoError(t, err)
	assert.Equal(t, "central prompt", init.SystemPrompt)
	assert.Empty(t, init.AllowedTools)
	assert.True(t, init.ToolsModified)
	assert.NoError(t, environment.DispatchAgentStart(t.Context()))
	assert.NoError(t, environment.DispatchTurnStart(t.Context(), 1))
	assert.NoError(t, environment.DispatchTurnEnd(t.Context(), "answer", 1))
	followUps, err := environment.DispatchAgentEnd(t.Context(), nil)
	require.NoError(t, err)
	assert.Empty(t, followUps)
	decision, err := environment.DispatchToolCall(t.Context(), ToolRequest{Name: "bash"})
	require.NoError(t, err)
	assert.True(t, decision.Blocked)
	assert.Contains(t, decision.Reason, "cannot execute tools")
	_, err = environment.ExecuteTool(t.Context(), ToolRequest{Name: "bash"}, nil)
	assert.ErrorContains(t, err, "cannot execute tools")
	_, err = environment.ExecuteCommand(t.Context(), CommandRequest{})
	assert.ErrorContains(t, err, "cannot execute commands")
	_, err = environment.DispatchToolUpdate(t.Context(), ToolOutputRequest{})
	assert.ErrorContains(t, err, "cannot execute tools")
	_, err = environment.DispatchToolResult(t.Context(), ToolOutputRequest{})
	assert.ErrorContains(t, err, "cannot execute tools")
	require.NoError(t, environment.Close(t.Context()))
	assert.False(t, environment.IsOpen())
	assert.NoError(t, environment.Close(t.Context()))
}

func TestUtilityEnvironmentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	environment := &UtilityEnvironment{}
	_, err := environment.Open(ctx, RunSpec{})
	assert.ErrorIs(t, err, context.Canceled)
	assert.False(t, environment.IsOpen())
	_, err = environment.ProcessUserMessage(ctx, "input")
	assert.ErrorIs(t, err, context.Canceled)
	_, err = environment.ProcessAgentInit(ctx, "prompt", nil)
	assert.ErrorIs(t, err, context.Canceled)
	assert.ErrorIs(t, environment.DispatchAgentStart(ctx), context.Canceled)
	assert.ErrorIs(t, environment.DispatchTurnStart(ctx, 1), context.Canceled)
	assert.ErrorIs(t, environment.DispatchTurnEnd(ctx, "output", 1), context.Canceled)
	_, err = environment.DispatchAgentEnd(ctx, nil)
	assert.ErrorIs(t, err, context.Canceled)
	assert.NoError(t, environment.Close(ctx), "cancellation must not prevent cleanup")
}
