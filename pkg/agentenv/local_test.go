package agentenv

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalEnvironmentPinsManifestForRun(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	contextPath := filepath.Join(workspace, "AGENTS.md")
	require.NoError(t, os.WriteFile(contextPath, []byte("# Version one"), 0o644))

	environment := NewLocalEnvironment(workspace, nil)
	config := llmtypes.Config{
		WorkingDirectory: workspace,
		AllowedTools:     []string{"file_read", "get_goal"},
	}
	manifest, err := environment.Open(context.Background(), RunSpec{ConversationID: "conv-1", Config: config})
	require.NoError(t, err)
	t.Cleanup(func() { _ = environment.Close(context.Background()) })

	assert.Equal(t, "# Version one", manifest.Contexts[contextPath])
	require.NoError(t, os.WriteFile(contextPath, []byte("# Version two"), 0o644))
	assert.Equal(t, "# Version one", environment.Manifest().Contexts[contextPath])
	assert.Equal(t, "# Version one", environment.State().DiscoverContexts()[contextPath])

	require.NoError(t, environment.Close(context.Background()))
	manifest, err = environment.Open(context.Background(), RunSpec{ConversationID: "conv-2", Config: config})
	require.NoError(t, err)
	assert.Equal(t, "# Version two", manifest.Contexts[contextPath])
}

func TestLocalEnvironmentManifestIncludesSerializableToolDefinitionsAndPlacement(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workspace := t.TempDir()
	environment := NewLocalEnvironment(workspace, nil)

	manifest, err := environment.Open(context.Background(), RunSpec{Config: llmtypes.Config{
		WorkingDirectory: workspace,
		AllowedTools:     []string{"file_read", "get_goal"},
	}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = environment.Close(context.Background()) })

	fileRead, ok := manifest.ToolDefinition("file_read")
	require.True(t, ok)
	assert.Equal(t, ToolPlacementEnvironment, fileRead.Placement)
	assert.NotEmpty(t, fileRead.Description)
	assert.Equal(t, "object", fileRead.InputSchema["type"])
	require.NotNil(t, fileRead.Tool)

	getGoal, ok := manifest.ToolDefinition("get_goal")
	require.True(t, ok)
	assert.Equal(t, ToolPlacementControlPlane, getGoal.Placement)

	fileRead.InputSchema["type"] = "changed"
	unchanged, ok := environment.Manifest().ToolDefinition("file_read")
	require.True(t, ok)
	assert.Equal(t, "object", unchanged.InputSchema["type"])

	payload, err := json.Marshal(fileRead)
	require.NoError(t, err)
	var serialized map[string]any
	require.NoError(t, json.Unmarshal(payload, &serialized))
	assert.NotContains(t, serialized, "Tool")
	assert.NotContains(t, serialized, "tool")
}

func TestManifestCloneDeepCopiesToolSchemas(t *testing.T) {
	manifest := Manifest{Tools: []ToolDefinition{{
		Name: "nested",
		InputSchema: map[string]any{
			"properties": map[string]any{
				"path": map[string]any{
					"type": "string",
					"enum": []any{"one", "two"},
				},
			},
		},
	}}}

	clone := manifest.Clone()
	properties := clone.Tools[0].InputSchema["properties"].(map[string]any)
	path := properties["path"].(map[string]any)
	path["type"] = "integer"
	path["enum"].([]any)[0] = "changed"

	originalProperties := manifest.Tools[0].InputSchema["properties"].(map[string]any)
	originalPath := originalProperties["path"].(map[string]any)
	assert.Equal(t, "string", originalPath["type"])
	assert.Equal(t, []any{"one", "two"}, originalPath["enum"])
}

func TestLocalEnvironmentExecutesWorkspaceRecipeCommand(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workspace := t.TempDir()
	recipeDir := filepath.Join(workspace, ".kodelet", "recipes")
	require.NoError(t, os.MkdirAll(recipeDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(recipeDir, "review.md"), []byte(`---
description: Review the workspace
allowed_tools:
  - file_read
allowed_commands:
  - git status
---
Review this workspace carefully.`), 0o644))

	environment := NewLocalEnvironment(workspace, nil)
	result, err := environment.ExecuteCommand(context.Background(), CommandRequest{
		Message: "/review focus on tests",
		RunSpec: RunSpec{Config: llmtypes.Config{WorkingDirectory: workspace}},
	})
	require.NoError(t, err)

	assert.True(t, result.Matched)
	assert.Equal(t, CommandActionRunAgent, result.Action)
	assert.Equal(t, "review", result.CommandName)
	assert.Contains(t, result.Prompt, "Review this workspace carefully.")
	assert.Contains(t, result.Prompt, "focus on tests")
	assert.Equal(t, []string{"file_read"}, result.AllowedTools)
	assert.Equal(t, []string{"git status"}, result.AllowedCommands)
}

func TestLocalEnvironmentLifecycleAndToolExecutionFromProvidedState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workspace := t.TempDir()
	filePath := filepath.Join(workspace, "hello.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("hello environment\n"), 0o600))
	baseConfig := llmtypes.Config{WorkingDirectory: workspace, AllowedTools: []string{"bash", "file_read"}}
	state := tools.NewBasicState(t.Context(),
		tools.WithWorkingDirectory(workspace),
		tools.WithLLMConfig(baseConfig),
		tools.WithMainTools(),
	)
	runtime := extensions.EmptyRuntime()
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	environment := NewLocalEnvironmentFromState(state, runtime)
	environment.SetExtensions(runtime)

	spec := RunSpec{
		ConversationID: "conversation-1",
		Config: llmtypes.Config{
			WorkingDirectory: workspace,
			Provider:         "openai",
			Model:            "gpt-test",
			AllowedTools:     []string{"file_read"},
		},
		Metadata:  map[string]any{"recipe_name": "metadata-recipe"},
		InvokedBy: "test",
	}
	manifest, err := environment.Open(t.Context(), spec)
	require.NoError(t, err)
	assert.True(t, environment.IsOpen())
	assert.Equal(t, workspace, manifest.WorkingDirectory)
	assert.Equal(t, workspace, environment.State().WorkingDirectory())
	assert.NotEmpty(t, environment.State().BasicTools())
	assert.NotEmpty(t, environment.State().Tools())
	assert.Equal(t, state.GetLLMConfig(), environment.State().GetLLMConfig())
	environment.State().LockFile(filePath)
	environment.State().UnlockFile(filePath)

	_, err = environment.Open(t.Context(), spec)
	require.ErrorContains(t, err, "already open")

	message, err := environment.ProcessUserMessage(t.Context(), "hello")
	require.NoError(t, err)
	assert.Equal(t, "hello", message)
	require.NoError(t, environment.DispatchAgentStart(t.Context()))
	require.NoError(t, environment.DispatchTurnStart(t.Context(), 2))
	initDecision, err := environment.ProcessAgentInit(t.Context(), "prompt", []string{"file_read"})
	require.NoError(t, err)
	assert.Equal(t, "prompt", initDecision.SystemPrompt)
	assert.Equal(t, []string{"file_read"}, initDecision.AllowedTools)
	require.NoError(t, environment.DispatchTurnEnd(t.Context(), "done", 3))
	followUps, err := environment.DispatchAgentEnd(t.Context(), nil)
	require.NoError(t, err)
	assert.Empty(t, followUps)

	callDecision, err := environment.DispatchToolCall(t.Context(), ToolRequest{Name: "file_read", Input: `{}`, ToolCallID: "call-1"})
	require.NoError(t, err)
	assert.JSONEq(t, `{}`, callDecision.Input)
	structured := tooltypes.StructuredToolResult{ToolName: "file_read", Success: true}
	updateDecision, err := environment.DispatchToolUpdate(t.Context(), ToolOutputRequest{Name: "file_read", Input: `{}`, ToolCallID: "call-1", StructuredResult: structured})
	require.NoError(t, err)
	assert.True(t, updateDecision.Accepted)
	resultDecision, err := environment.DispatchToolResult(t.Context(), ToolOutputRequest{Name: "file_read", Input: `{}`, ToolCallID: "call-1", StructuredResult: structured})
	require.NoError(t, err)
	assert.True(t, resultDecision.Accepted)
	assert.True(t, environment.CanStreamToolUpdates())

	execution, err := environment.ExecuteTool(t.Context(), ToolRequest{
		Name:       "file_read",
		Input:      `{"file_path":"` + filePath + `","offset":1,"line_limit":10}`,
		ToolCallID: "call-read",
	}, func(ToolUpdate) {})
	require.NoError(t, err)
	assert.Contains(t, execution.Result.AssistantFacing(), "hello environment")
	assert.True(t, execution.StructuredResult.Success)

	blocked, err := environment.ExecuteTool(t.Context(), ToolRequest{Name: "bash", Input: `{"command":"echo no"}`, ToolCallID: "call-blocked"}, nil)
	require.NoError(t, err)
	assert.True(t, blocked.Result.IsError())
	assert.Contains(t, blocked.Result.GetError(), "not allowed")

	controlPlane, err := environment.ExecuteTool(t.Context(), ToolRequest{Name: "get_goal", Input: `{}`, ToolCallID: "call-control"}, nil)
	require.NoError(t, err)
	assert.True(t, controlPlane.Result.IsError())
	assert.Contains(t, controlPlane.Result.GetError(), "control-plane tool")

	environment.ApplyCommandResult(CommandResult{
		Matched:         true,
		RecipeName:      "updated-recipe",
		AllowedTools:    []string{"bash"},
		AllowedCommands: []string{"echo *"},
	})
	pinned := environment.runSpec()
	assert.Equal(t, "updated-recipe", pinned.Config.RecipeName)
	assert.Equal(t, []string{"bash"}, pinned.Config.AllowedTools)
	assert.Equal(t, []string{"echo *"}, pinned.Config.AllowedCommands)
	invalidCommand, err := environment.ExecuteTool(t.Context(), ToolRequest{Name: "bash", Input: `{"command":"rm -rf nowhere"}`, ToolCallID: "call-command"}, nil)
	require.NoError(t, err)
	assert.True(t, invalidCommand.Result.IsError())

	plain, err := environment.ExecuteCommand(t.Context(), CommandRequest{Message: "not a command", RunSpec: spec})
	require.NoError(t, err)
	assert.False(t, plain.Matched)
	goal, err := environment.ExecuteCommand(t.Context(), CommandRequest{Message: "/goal", RunSpec: spec})
	require.NoError(t, err)
	assert.False(t, goal.Matched)
	rename, err := environment.ExecuteCommand(t.Context(), CommandRequest{Message: "/rename new name", RunSpec: spec})
	require.NoError(t, err)
	assert.False(t, rename.Matched)

	require.NoError(t, environment.Close(t.Context()))
	assert.False(t, environment.IsOpen())
	assert.Empty(t, environment.Manifest().WorkingDirectory)
	assert.Equal(t, state, environment.State())
}

func TestLocalEnvironmentHandlesClosedAndNilReceivers(t *testing.T) {
	workspace := t.TempDir()
	environment := NewLocalEnvironment(workspace, nil)
	execution, err := environment.ExecuteTool(t.Context(), ToolRequest{Name: "file_read", Input: `{}`, ToolCallID: "call-1"}, nil)
	require.NoError(t, err)
	assert.True(t, execution.Result.IsError())
	assert.Contains(t, execution.Result.GetError(), "not open")

	environment.ApplyCommandResult(CommandResult{Matched: true, RecipeName: "ignored"})
	environment.ApplyCommandResult(CommandResult{})
	assert.Nil(t, environment.State())

	var nilEnvironment *LocalEnvironment
	assert.False(t, nilEnvironment.IsOpen())
	assert.Empty(t, nilEnvironment.Manifest())
	assert.Nil(t, nilEnvironment.State())
	nilEnvironment.SetExtensions(nil)
	nilEnvironment.ApplyCommandResult(CommandResult{Matched: true})
	require.NoError(t, nilEnvironment.Close(t.Context()))
	message, err := nilEnvironment.ProcessUserMessage(t.Context(), "hello")
	require.NoError(t, err)
	assert.Equal(t, "hello", message)
	assert.True(t, nilEnvironment.CanStreamToolUpdates())

	_, err = nilEnvironment.Open(t.Context(), RunSpec{})
	require.ErrorContains(t, err, "required")
}

func TestEnvironmentToolAllowed(t *testing.T) {
	assert.True(t, environmentToolAllowed(llmtypes.Config{}, "bash"))
	assert.True(t, environmentToolAllowed(llmtypes.Config{AllowedTools: []string{" file_read "}}, "file_read"))
	assert.False(t, environmentToolAllowed(llmtypes.Config{AllowedTools: []string{"file_read"}}, "grep_tool"))
	assert.True(t, environmentToolAllowed(llmtypes.Config{AllowedTools: []string{"file_read"}, EnableFSSearchTools: true}, "grep_tool"))
	assert.True(t, environmentToolAllowed(llmtypes.Config{AllowedTools: []string{"file_read"}, EnableFSSearchTools: true}, "glob_tool"))
}
