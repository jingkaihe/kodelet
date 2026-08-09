package base

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/goals"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"

	"github.com/jingkaihe/kodelet/pkg/tools/renderers"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

type multimodalTool struct{}

func (t multimodalTool) GenerateSchema() *jsonschema.Schema { return &jsonschema.Schema{} }
func (t multimodalTool) Name() string                       { return "view_image" }
func (t multimodalTool) Description() string                { return "test multimodal tool" }
func (t multimodalTool) ValidateInput(tooltypes.State, string) error {
	return nil
}

func (t multimodalTool) Execute(context.Context, tooltypes.State, string) tooltypes.ToolResult {
	return multimodalToolResult{BaseToolResult: tooltypes.BaseToolResult{Result: "image available"}}
}
func (t multimodalTool) TracingKVs(string) ([]attribute.KeyValue, error) { return nil, nil }

type multimodalToolResult struct {
	tooltypes.BaseToolResult
}

type lateUpdateTool struct {
	callback tooltypes.ToolUpdateCallback
}

func (t *lateUpdateTool) GenerateSchema() *jsonschema.Schema { return &jsonschema.Schema{} }
func (t *lateUpdateTool) Name() string                       { return "late_update" }
func (t *lateUpdateTool) Description() string                { return "test streaming updates" }
func (t *lateUpdateTool) ValidateInput(tooltypes.State, string) error {
	return nil
}

func (t *lateUpdateTool) Execute(context.Context, tooltypes.State, string) tooltypes.ToolResult {
	return tooltypes.BaseToolResult{Result: "complete"}
}

func (t *lateUpdateTool) ExecuteStreaming(
	_ context.Context,
	_ tooltypes.State,
	_ string,
	onUpdate tooltypes.ToolUpdateCallback,
) tooltypes.ToolResult {
	t.callback = onUpdate
	onUpdate(tooltypes.BaseToolResult{Result: "running"})
	return tooltypes.BaseToolResult{Result: "complete"}
}
func (t *lateUpdateTool) TracingKVs(string) ([]attribute.KeyValue, error) { return nil, nil }

type toolUpdateHandler struct {
	updates []string
}

type environmentThreadStub struct {
	*threadStub
	environment agentenv.Environment
}

func (t *environmentThreadStub) GetEnvironment() agentenv.Environment {
	return t.environment
}

func (t *environmentThreadStub) SetEnvironment(environment agentenv.Environment) {
	t.environment = environment
}

func (t *environmentThreadStub) SetEnvironmentState(state tooltypes.State) {
	t.state = state
}

func (t *environmentThreadStub) ApplyEnvironmentConfig(config agentenv.EnvironmentConfig) {
	t.config.WorkingDirectory = t.environment.Manifest().WorkingDirectory
	t.config.AllowedCommands = append([]string(nil), config.AllowedCommands...)
	t.config.ToolMode = config.ToolMode
	t.config.EnableFSSearchTools = config.EnableFSSearchTools
	t.config.Sysprompt = config.SystemPromptPath
	t.config.SyspromptContent = config.SystemPromptContent
	t.config.SyspromptInline = config.SystemPromptPath != "" || config.SystemPromptContent != ""
	t.config.SyspromptArgs = config.SystemPromptArgs
	t.config.SystemInformation = config.SystemInformation.Clone()
}

type projectedEnvironment struct {
	agentenv.Environment
	manifest agentenv.Manifest
	opened   bool
}

func (e *projectedEnvironment) Open(context.Context, agentenv.RunSpec) (agentenv.Manifest, error) {
	e.opened = true
	return e.manifest.Clone(), nil
}

func (e *projectedEnvironment) IsOpen() bool                { return e.opened }
func (e *projectedEnvironment) Manifest() agentenv.Manifest { return e.manifest.Clone() }
func (e *projectedEnvironment) Close(context.Context) error {
	e.opened = false
	return nil
}

func (h *toolUpdateHandler) HandleText(string)                                     {}
func (h *toolUpdateHandler) HandleToolUse(string, string, string)                  {}
func (h *toolUpdateHandler) HandleToolResult(string, string, tooltypes.ToolResult) {}
func (h *toolUpdateHandler) HandleThinking(string)                                 {}
func (h *toolUpdateHandler) HandleDone()                                           {}
func (h *toolUpdateHandler) HandleToolUpdate(_, _ string, result tooltypes.ToolResult) {
	h.updates = append(h.updates, result.GetResult())
}

func (r multimodalToolResult) StructuredData() tooltypes.StructuredToolResult {
	return tooltypes.StructuredToolResult{
		ToolName:  "view_image",
		Success:   true,
		Timestamp: time.Now(),
	}
}

func (r multimodalToolResult) ContentParts() []tooltypes.ToolResultContentPart {
	return []tooltypes.ToolResultContentPart{{
		Type:     tooltypes.ToolResultContentPartTypeImage,
		ImageURL: "data:image/png;base64,ZmFrZQ==",
		MimeType: "image/png",
	}}
}

func TestNewThreadInitializesRendererRegistry(t *testing.T) {
	bt := NewThread(llmtypes.Config{}, "conv-id")
	require.NotNil(t, bt.RendererRegistry)
}

func TestExecuteToolPanicsWithNilRendererRegistry(t *testing.T) {
	assert.PanicsWithValue(t, "rendererRegistry must not be nil", func() {
		ExecuteTool(
			context.Background(),
			nil,
			&mockState{},
			nil,
			"unknown_tool",
			"{}",
			"call-id",
		)
	})
}

func TestExecuteToolWithInjectedRendererRegistry(t *testing.T) {
	execution := ExecuteTool(
		context.Background(),
		nil,
		&mockState{},
		renderers.NewRendererRegistry(),
		"unknown_tool",
		"{}",
		"call-id",
	)

	assert.NotNil(t, execution.Result)
	assert.NotEmpty(t, execution.RenderedOutput)
}

func TestExecuteToolPreservesMultimodalResultWhenExtensionRuntimeIsIdle(t *testing.T) {
	state := &toolState{tools: []tooltypes.Tool{multimodalTool{}}}
	thread := &threadStub{
		config:         llmtypes.Config{Extensions: extensions.EmptyRuntime()},
		conversationID: "conv-id",
		state:          state,
	}

	execution := ExecuteTool(
		context.Background(),
		thread,
		state,
		renderers.NewRendererRegistry(),
		"view_image",
		"{}",
		"call-id",
	)

	multimodalResult, ok := execution.Result.(tooltypes.MultiModalToolResult)
	require.True(t, ok)
	assert.Equal(t, []tooltypes.ToolResultContentPart{{
		Type:     tooltypes.ToolResultContentPartTypeImage,
		ImageURL: "data:image/png;base64,ZmFrZQ==",
		MimeType: "image/png",
	}}, multimodalResult.ContentParts())
}

func TestExecuteToolWithHandlerForwardsUpdatesAndRejectsLateCallbacks(t *testing.T) {
	tool := &lateUpdateTool{}
	state := &toolState{tools: []tooltypes.Tool{tool}}
	handler := &toolUpdateHandler{}

	execution := ExecuteToolWithHandler(
		context.Background(),
		nil,
		state,
		renderers.NewRendererRegistry(),
		tool.Name(),
		`{}`,
		"call-id",
		handler,
	)

	assert.Equal(t, "complete", execution.Result.GetResult())
	assert.Equal(t, []string{"running"}, handler.updates)
	require.NotNil(t, tool.callback)

	tool.callback(tooltypes.BaseToolResult{Result: "too late"})
	assert.Equal(t, []string{"running"}, handler.updates)
}

func TestExecuteEnvironmentToolRoutesControlPlaneToolsOutsideWorkspaceEnvironment(t *testing.T) {
	thread := &environmentThreadStub{
		threadStub: &threadStub{
			config:         llmtypes.Config{WorkingDirectory: t.TempDir()},
			conversationID: "conv-control-plane-tool",
			metadata: map[string]any{
				goals.MetadataKey: goals.Goal{Objective: "ship phase one", Status: goals.StatusActive, Version: 1},
			},
			state: &toolState{tools: []tooltypes.Tool{namedTool("file_read"), namedTool("get_goal")}},
		},
	}

	_, err := OpenEnvironment(context.Background(), thread)
	require.NoError(t, err)
	t.Cleanup(func() { _ = CloseEnvironment(context.Background(), thread) })

	execution := ExecuteEnvironmentTool(
		context.Background(),
		thread,
		renderers.NewRendererRegistry(),
		"get_goal",
		`{}`,
		"call-goal",
	)

	require.NotNil(t, execution.Result)
	assert.False(t, execution.Result.IsError())
	assert.Contains(t, execution.Result.GetResult(), "ship phase one")
}

func TestOpenEnvironmentAppliesPinnedRunnerConfiguration(t *testing.T) {
	environment := &projectedEnvironment{manifest: agentenv.Manifest{
		WorkingDirectory: "/runner/workspace",
		Config: &agentenv.EnvironmentConfig{
			AllowedCommands:     []string{"go test *"},
			ToolMode:            llmtypes.ToolModePatch,
			EnableFSSearchTools: true,
			SystemPromptPath:    "/runner/custom.tmpl",
			SystemPromptContent: "runner prompt",
			SystemPromptArgs:    map[string]string{"project": "kodelet"},
			SystemInformation: &llmtypes.SystemInformation{
				IsGitRepo: true,
				Platform:  "darwin",
				OSVersion: "macOS 26.0",
				Date:      "2026-08-09",
			},
		},
	}}
	thread := &environmentThreadStub{
		threadStub:  &threadStub{config: llmtypes.Config{Provider: "openai"}},
		environment: environment,
	}

	_, err := OpenEnvironment(t.Context(), thread)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, CloseEnvironment(t.Context(), thread)) })
	config := thread.GetConfig()
	assert.Equal(t, "/runner/workspace", config.WorkingDirectory)
	assert.Equal(t, []string{"go test *"}, config.AllowedCommands)
	assert.Equal(t, llmtypes.ToolModePatch, config.ToolMode)
	assert.True(t, config.EnableFSSearchTools)
	assert.Equal(t, "/runner/custom.tmpl", config.Sysprompt)
	assert.Equal(t, "runner prompt", config.SyspromptContent)
	assert.True(t, config.SyspromptInline)
	assert.Equal(t, map[string]string{"project": "kodelet"}, config.SyspromptArgs)
	require.NotNil(t, config.SystemInformation)
	assert.True(t, config.SystemInformation.IsGitRepo)
	assert.Equal(t, "darwin", config.SystemInformation.Platform)
	assert.Equal(t, "macOS 26.0", config.SystemInformation.OSVersion)
	assert.Equal(t, "2026-08-09", config.SystemInformation.Date)
}

func TestExecuteEnvironmentToolForwardsUpdatesAndNormalizesResults(t *testing.T) {
	var lateUpdate agentenv.ToolUpdateSink
	environment := &recordingAgentEnvironment{
		open: true,
		manifest: agentenv.Manifest{
			WorkingDirectory: "/runner/workspace",
			Tools: []agentenv.ToolDefinition{{
				Name:      "remote_tool",
				Placement: agentenv.ToolPlacementEnvironment,
			}},
		},
		executeTool: func(_ context.Context, request agentenv.ToolRequest, updates agentenv.ToolUpdateSink) (agentenv.ToolExecution, error) {
			assert.Equal(t, "remote_tool", request.Name)
			assert.Equal(t, "call-remote", request.ToolCallID)
			lateUpdate = updates
			updates(agentenv.ToolUpdate{})
			updates(agentenv.ToolUpdate{Result: tooltypes.BaseToolResult{Result: "running"}})
			updates(agentenv.ToolUpdate{
				Result:           tooltypes.BaseToolResult{Result: "replaced"},
				StructuredResult: tooltypes.StructuredToolResult{ToolName: "remote_tool", Success: true},
				Modified:         true,
			})
			return agentenv.ToolExecution{
				Input:            `{"path":"normalized"}`,
				Result:           tooltypes.BaseToolResult{Result: "complete"},
				StructuredResult: tooltypes.StructuredToolResult{ToolName: "remote_tool", Success: true},
			}, nil
		},
	}
	thread := &environmentThreadStub{
		threadStub: &threadStub{
			config:         llmtypes.Config{RecipeName: "recipe"},
			conversationID: "conversation-remote",
			metadata:       map[string]any{"recipe_name": "metadata-recipe"},
		},
		environment: environment,
	}
	handler := &toolUpdateHandler{}

	execution := ExecuteEnvironmentToolWithHandler(
		t.Context(),
		thread,
		renderers.NewRendererRegistry(),
		"remote_tool",
		`{"path":"original"}`,
		"call-remote",
		handler,
	)

	require.NoError(t, execution.Err)
	assert.Equal(t, `{"path":"normalized"}`, execution.Input)
	assert.Equal(t, "complete", execution.Result.GetResult())
	assert.Equal(t, "remote_tool", execution.StructuredResult.ToolName)
	assert.NotEmpty(t, execution.RenderedOutput)
	require.Len(t, handler.updates, 2)
	assert.Equal(t, "running", handler.updates[0])
	require.NotNil(t, lateUpdate)
	lateUpdate(agentenv.ToolUpdate{Result: tooltypes.BaseToolResult{Result: "too late"}})
	assert.Len(t, handler.updates, 2)
}

func TestExecuteEnvironmentToolHandlesUnavailableAndFailedRunnerTools(t *testing.T) {
	environment := &recordingAgentEnvironment{
		open:     true,
		manifest: agentenv.Manifest{WorkingDirectory: "/runner/workspace"},
	}
	thread := &environmentThreadStub{threadStub: &threadStub{}, environment: environment}
	registry := renderers.NewRendererRegistry()

	unavailable := ExecuteEnvironmentTool(t.Context(), thread, registry, "get_goal", `{}`, "call-goal")
	require.NotNil(t, unavailable.Result)
	assert.True(t, unavailable.Result.IsError())
	assert.Contains(t, unavailable.Result.GetError(), "not available in the active run")

	sentinel := errors.New("runner link closed")
	environment.manifest.Tools = []agentenv.ToolDefinition{{Name: "remote", Placement: agentenv.ToolPlacementEnvironment}}
	environment.executeTool = func(context.Context, agentenv.ToolRequest, agentenv.ToolUpdateSink) (agentenv.ToolExecution, error) {
		return agentenv.ToolExecution{}, sentinel
	}
	failed := ExecuteEnvironmentTool(t.Context(), thread, registry, "remote", `{}`, "call-remote")
	require.ErrorIs(t, failed.Err, sentinel)
	assert.Equal(t, `{}`, failed.Input)

	environment.executeTool = func(_ context.Context, request agentenv.ToolRequest, _ agentenv.ToolUpdateSink) (agentenv.ToolExecution, error) {
		return agentenv.ToolExecution{Input: request.Input}, nil
	}
	missing := ExecuteEnvironmentTool(t.Context(), thread, registry, "remote", `{}`, "call-missing")
	require.NotNil(t, missing.Result)
	assert.True(t, missing.Result.IsError())
	assert.Contains(t, missing.Result.GetError(), "returned no tool result")
}

func TestExecuteControlPlaneToolHonorsEnvironmentPolicyAndResultMutation(t *testing.T) {
	mutated := tooltypes.StructuredToolResult{ToolName: "get_goal", Success: false, Error: "redacted by runner policy"}
	environment := &recordingAgentEnvironment{
		open: true,
		manifest: agentenv.Manifest{
			WorkingDirectory: "/runner/workspace",
			Tools: []agentenv.ToolDefinition{{
				Name:      "get_goal",
				Placement: agentenv.ToolPlacementControlPlane,
			}},
		},
		toolCallDecision:   agentenv.ToolCallDecision{Blocked: true, Reason: "policy denied", Input: `{"changed":true}`},
		toolResultDecision: agentenv.ToolOutputDecision{StructuredResult: mutated, Modified: true, Accepted: true},
	}
	thread := &environmentThreadStub{threadStub: &threadStub{conversationID: "conversation"}, environment: environment}

	execution := ExecuteEnvironmentTool(t.Context(), thread, renderers.NewRendererRegistry(), "get_goal", `{}`, "call-goal")

	require.NoError(t, execution.Err)
	assert.Equal(t, `{"changed":true}`, execution.Input)
	assert.True(t, execution.Result.IsError())
	assert.Equal(t, "redacted by runner policy", execution.Result.GetError())
	assert.Equal(t, mutated, execution.StructuredResult)
}

func TestControlPlaneStateAndStructuredResultAdapter(t *testing.T) {
	environment := &recordingAgentEnvironment{
		open:     true,
		manifest: agentenv.Manifest{WorkingDirectory: "/runner/workspace"},
	}
	state := controlPlaneToolState(&threadStub{config: llmtypes.Config{Model: "test-model"}}, environment)
	assert.NotEmpty(t, state.BasicTools())
	assert.NotEmpty(t, state.Tools())
	assert.Nil(t, state.DiscoverContexts())
	assert.Equal(t, "test-model", state.GetLLMConfig().(llmtypes.Config).Model)
	assert.Equal(t, "/runner/workspace", state.WorkingDirectory())
	state.LockFile("file")
	state.UnlockFile("file")

	result := StructuredResultToolResult{
		Result: tooltypes.StructuredToolResult{ToolName: "remote", Success: false, Error: "failed"},
	}
	assert.True(t, result.IsError())
	assert.Equal(t, "failed", result.GetError())
	assert.NotEmpty(t, result.GetResult())
	assert.Contains(t, result.AssistantFacing(), "failed")
	assert.Equal(t, result.Result, result.StructuredData())
	require.Len(t, result.ContentParts(), 1)
	assert.Equal(t, result.GetResult(), result.ContentParts()[0].Text)
	assert.Equal(t, result.GetResult(), result.String())
}
