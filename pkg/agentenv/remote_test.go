package agentenv

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeRemoteController struct {
	manifest       runnerpayload.Manifest
	openErr        error
	callErr        error
	executeErr     error
	cancelErr      error
	closeErr       error
	omitStructured bool
	openRunnerID   string
	openParams     protocol.RunOpenParams
	calls          []string
	toolParams     runnerpayload.ToolExecuteParams
	canceledRunID  string
	closedRunID    string
	closedStatus   protocol.RunStatus
	closedError    error
}

func (c *fakeRemoteController) OpenRun(_ context.Context, runnerID string, params protocol.RunOpenParams) (runnerpayload.Manifest, error) {
	c.openRunnerID = runnerID
	c.openParams = params
	return c.manifest, c.openErr
}

func (c *fakeRemoteController) CallRun(_ context.Context, _ string, method string, params any, result any) error {
	c.calls = append(c.calls, method)
	if c.callErr != nil {
		return c.callErr
	}
	switch method {
	case protocol.MethodCommandExecute:
		return assignRemoteResult(result, runnerpayload.CommandExecuteResult{
			Matched:         true,
			Action:          string(CommandActionRunAgent),
			CommandName:     "review",
			Prompt:          "review this",
			RecipeName:      "review",
			AllowedTools:    []string{"file_read"},
			AllowedCommands: []string{"go test *"},
		})
	case protocol.MethodLifecycleDispatch:
		value := params.(runnerpayload.LifecycleDispatchParams)
		response := runnerpayload.LifecycleDispatchResult{}
		switch value.Event {
		case runnerpayload.LifecycleUserMessage:
			response.Message = "modified: " + value.Message
		case runnerpayload.LifecycleAgentInit:
			response.SystemPrompt = value.SystemPrompt + "\npatched"
			response.AllowedTools = []string{"file_read"}
			response.ToolsModified = true
		case runnerpayload.LifecycleAgentEnd:
			response.FollowUpMessages = []string{"follow up"}
		case runnerpayload.LifecycleToolCall:
			response.ToolInput = json.RawMessage(`{"path":"changed"}`)
		case runnerpayload.LifecycleToolUpdate, runnerpayload.LifecycleToolResult:
			if !c.omitStructured {
				response.StructuredResult = value.StructuredResult
			}
			response.Accepted = true
		}
		return assignRemoteResult(result, response)
	default:
		return nil
	}
}

func (c *fakeRemoteController) ExecuteTool(_ context.Context, params runnerpayload.ToolExecuteParams, updates func(runnerpayload.ToolUpdateParams)) (runnerpayload.ToolExecuteResult, error) {
	c.toolParams = params
	if c.executeErr != nil {
		return runnerpayload.ToolExecuteResult{}, c.executeErr
	}
	structured := tooltypes.StructuredToolResult{ToolName: params.Name, Success: true, Timestamp: time.Unix(1, 0).UTC()}
	if updates != nil {
		updates(runnerpayload.ToolUpdateParams{
			RunID:      params.RunID,
			ToolCallID: params.ToolCallID,
			Sequence:   1,
			Result: runnerpayload.ToolResult{
				AssistantFacing: "partial",
				Structured:      structured,
			},
			Modified: true,
		})
	}
	return runnerpayload.ToolExecuteResult{
		Input: json.RawMessage(`{"path":"effective"}`),
		Result: runnerpayload.ToolResult{
			AssistantFacing: "complete",
			Structured:      structured,
			ContentParts: []tooltypes.ToolResultContentPart{{
				Type: tooltypes.ToolResultContentPartTypeText,
				Text: "rich",
			}},
		},
		Modified: true,
	}, nil
}

func (c *fakeRemoteController) CancelRun(_ context.Context, runID, _ string) error {
	c.canceledRunID = runID
	return c.cancelErr
}

func (c *fakeRemoteController) CloseRun(_ context.Context, runID string, status protocol.RunStatus, runErr error) error {
	c.closedRunID = runID
	c.closedStatus = status
	c.closedError = runErr
	return c.closeErr
}

func assignRemoteResult(target any, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(payload, target)
}

func TestRemoteEnvironmentProxiesPinnedRunnerContract(t *testing.T) {
	contextContent := "# Runner instructions"
	controller := &fakeRemoteController{}
	controller.manifest = runnerpayload.Manifest{
		ProtocolVersion:  protocol.Version,
		RunnerID:         "runner-1",
		RunID:            "run-1",
		Generation:       3,
		WorkingDirectory: "/runner/workspace",
		ContextFiles: []runnerpayload.ContextFile{{
			Path:    "/runner/workspace/AGENTS.md",
			Content: contextContent,
			Digest:  remoteContentDigest(contextContent),
		}},
		Tools: []runnerpayload.ToolDefinition{{
			Name:        "file_read",
			Description: "Read a runner file",
			InputSchema: map[string]any{"type": "object"},
			Placement:   string(ToolPlacementEnvironment),
		}},
		Config: runnerpayload.EnvironmentConfig{
			AllowedCommands:     []string{"go test *"},
			ToolMode:            llmtypes.ToolModePatch,
			EnableFSSearchTools: true,
			SystemPromptPath:    "/runner/custom.tmpl",
			SystemPromptContent: "runner prompt {{.WorkingDirectory}}",
			SystemPromptArgs:    map[string]string{"scope": "runner"},
			SystemInformation: &llmtypes.SystemInformation{
				IsGitRepo: true,
				Platform:  "darwin",
				OSVersion: "macOS 26.0",
				Date:      "2026-08-09",
			},
		},
		Capabilities: runnerpayload.EnvironmentCapabilities{ToolUpdates: true, Commands: true},
	}

	environment := NewRemoteEnvironment(controller, "runner-1", WithRemoteRunIDGenerator(func() (string, error) {
		return "run-1", nil
	}))
	manifest, err := environment.Open(t.Context(), RunSpec{
		ConversationID:     "conversation-1",
		EnvironmentProfile: "runner-workspace",
		Config: llmtypes.Config{
			Provider:   "openai",
			Model:      "gpt-test",
			Profile:    "workspace",
			RecipeName: "initial",
		},
		InvokedBy: "main",
	})
	require.NoError(t, err)
	assert.True(t, environment.IsOpen())
	assert.Equal(t, "runner-1", controller.openRunnerID)
	assert.Equal(t, "conversation-1", controller.openParams.ConversationID)
	assert.Equal(t, "workspace", controller.openParams.Agent.Profile)
	assert.Equal(t, "runner-workspace", controller.openParams.Agent.EnvironmentProfile)
	assert.ElementsMatch(t, []string{"get_goal", "update_goal", "read_conversation"}, controller.openParams.ReservedToolNames)
	assert.Equal(t, contextContent, manifest.Contexts["/runner/workspace/AGENTS.md"])
	require.NotNil(t, manifest.Config)
	assert.Equal(t, "/runner/custom.tmpl", manifest.Config.SystemPromptPath)
	assert.Equal(t, "runner prompt {{.WorkingDirectory}}", manifest.Config.SystemPromptContent)
	require.NotNil(t, manifest.Config.SystemInformation)
	assert.True(t, manifest.Config.SystemInformation.IsGitRepo)
	assert.Equal(t, "darwin", manifest.Config.SystemInformation.Platform)
	assert.Equal(t, "macOS 26.0", manifest.Config.SystemInformation.OSVersion)
	assert.Equal(t, "2026-08-09", manifest.Config.SystemInformation.Date)

	fileDefinition, ok := manifest.ToolDefinition("file_read")
	require.True(t, ok)
	assert.Equal(t, ToolPlacementEnvironment, fileDefinition.Placement)
	goalDefinition, ok := manifest.ToolDefinition("get_goal")
	require.True(t, ok)
	assert.Equal(t, ToolPlacementControlPlane, goalDefinition.Placement)

	command, err := environment.ExecuteCommand(t.Context(), CommandRequest{Message: "/review"})
	require.NoError(t, err)
	assert.Equal(t, CommandActionRunAgent, command.Action)
	assert.Equal(t, "review this", command.Prompt)

	message, err := environment.ProcessUserMessage(t.Context(), "hello")
	require.NoError(t, err)
	assert.Equal(t, "modified: hello", message)
	initDecision, err := environment.ProcessAgentInit(t.Context(), "base", []string{"file_read", "get_goal"})
	require.NoError(t, err)
	assert.Equal(t, "base\npatched", initDecision.SystemPrompt)
	assert.Equal(t, []string{"file_read"}, initDecision.AllowedTools)
	assert.True(t, initDecision.ToolsModified)

	callDecision, err := environment.DispatchToolCall(t.Context(), ToolRequest{Name: "get_goal", Input: `{}`, ToolCallID: "control-1"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"path":"changed"}`, callDecision.Input)

	var updates []ToolUpdate
	execution, err := environment.ExecuteTool(t.Context(), ToolRequest{
		Name:       "file_read",
		Input:      `{"path":"original"}`,
		ToolCallID: "tool-1",
	}, func(update ToolUpdate) {
		updates = append(updates, update)
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"path":"effective"}`, execution.Input)
	assert.Equal(t, "complete", execution.Result.AssistantFacing())
	assert.True(t, execution.Modified)
	require.Len(t, updates, 1)
	assert.Equal(t, "partial", updates[0].Result.AssistantFacing())
	assert.True(t, updates[0].Modified)
	rich, ok := execution.Result.(tooltypes.MultiModalToolResult)
	require.True(t, ok)
	assert.Equal(t, "rich", rich.ContentParts()[0].Text)

	require.NoError(t, environment.CloseWithError(t.Context(), context.Canceled))
	assert.False(t, environment.IsOpen())
	assert.Equal(t, "run-1", controller.canceledRunID)
	assert.Equal(t, "run-1", controller.closedRunID)
	assert.Equal(t, protocol.RunStatusCanceled, controller.closedStatus)
	assert.ErrorIs(t, controller.closedError, context.Canceled)
}

func TestRemoteEnvironmentRejectsMismatchedContextDigest(t *testing.T) {
	controller := &fakeRemoteController{manifest: runnerpayload.Manifest{
		ProtocolVersion: protocol.Version,
		RunnerID:        "runner-1",
		RunID:           "run-1",
		Generation:      1,
		ContextFiles: []runnerpayload.ContextFile{{
			Path:    "AGENTS.md",
			Content: "changed",
			Digest:  "sha256:not-the-content",
		}},
	}}
	environment := NewRemoteEnvironment(controller, "runner-1", WithRemoteRunIDGenerator(func() (string, error) {
		return "run-1", nil
	}))
	_, err := environment.Open(t.Context(), RunSpec{ConversationID: "conversation-1"})
	require.ErrorContains(t, err, "context digest")
	assert.Equal(t, protocol.RunStatusFailed, controller.closedStatus)
}

func TestRemoteEnvironmentLifecycleHelpersAndToolProxy(t *testing.T) {
	content := "# Runner context"
	controller := &fakeRemoteController{manifest: runnerpayload.Manifest{
		ProtocolVersion:  protocol.Version,
		RunnerID:         "runner-1",
		RunID:            "run-1",
		Generation:       1,
		WorkingDirectory: "/runner/workspace",
		ContextFiles: []runnerpayload.ContextFile{{
			Path:    "/runner/workspace/AGENTS.md",
			Content: content,
			Digest:  remoteContentDigest(content),
		}},
		Tools: []runnerpayload.ToolDefinition{{
			Name:        "file_read",
			Description: "Read a file",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}},
		}},
		Capabilities: runnerpayload.EnvironmentCapabilities{ToolUpdates: true},
	}}
	environment := NewRemoteEnvironment(controller, "runner-1",
		WithRemoteClientCapabilities(protocol.ClientCapabilities{InteractiveUI: true, PersistentSurfaces: true}),
		WithRemoteRunIDGenerator(func() (string, error) { return "run-1", nil }),
	)
	manifest, err := environment.Open(t.Context(), RunSpec{ConversationID: "conversation-1"})
	require.NoError(t, err)
	assert.Equal(t, "run-1", environment.RunID())
	assert.Equal(t, manifest.WorkingDirectory, environment.Manifest().WorkingDirectory)
	assert.True(t, controller.openParams.ClientCapabilities.InteractiveUI)
	assert.True(t, controller.openParams.ClientCapabilities.PersistentSurfaces)
	assert.True(t, environment.CanStreamToolUpdates())

	require.NoError(t, environment.DispatchAgentStart(t.Context()))
	require.NoError(t, environment.DispatchTurnStart(t.Context(), 2))
	require.NoError(t, environment.DispatchTurnEnd(t.Context(), "done", 3))
	followUps, err := environment.DispatchAgentEnd(t.Context(), []llmtypes.Message{{Role: "user", Content: "hello"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"follow up"}, followUps)
	structured := tooltypes.StructuredToolResult{ToolName: "file_read", Success: true}
	update, err := environment.DispatchToolUpdate(t.Context(), ToolOutputRequest{Name: "file_read", Input: `{}`, ToolCallID: "tool-1", StructuredResult: structured})
	require.NoError(t, err)
	assert.True(t, update.Accepted)
	result, err := environment.DispatchToolResult(t.Context(), ToolOutputRequest{Name: "file_read", Input: `{}`, ToolCallID: "tool-1", StructuredResult: structured})
	require.NoError(t, err)
	assert.True(t, result.Accepted)

	definition, ok := environment.Manifest().ToolDefinition("file_read")
	require.True(t, ok)
	proxy, ok := definition.Tool.(*remoteToolProxy)
	require.True(t, ok)
	assert.Equal(t, "file_read", proxy.Name())
	assert.Equal(t, "Read a file", proxy.Description())
	assert.Equal(t, "object", proxy.RawInputSchema()["type"])
	assert.NotNil(t, proxy.GenerateSchema())
	require.NoError(t, proxy.ValidateInput(nil, `{}`))
	require.Error(t, proxy.ValidateInput(nil, `not-json`))
	proxyResult := proxy.Execute(t.Context(), nil, `{}`)
	assert.True(t, proxyResult.IsError())
	assert.Contains(t, proxyResult.GetError(), "RemoteEnvironment")
	tracing, err := proxy.TracingKVs(`{}`)
	require.NoError(t, err)
	assert.Nil(t, tracing)

	require.NoError(t, environment.Close(t.Context()))
	assert.Equal(t, protocol.RunStatusSucceeded, controller.closedStatus)
	assert.Empty(t, environment.RunID())
	assert.False(t, environment.IsOpen())
}

func TestRemoteEnvironmentValidationAndFailurePaths(t *testing.T) {
	var nilEnvironment *RemoteEnvironment
	assert.False(t, nilEnvironment.IsOpen())
	assert.Empty(t, nilEnvironment.Manifest())
	assert.Empty(t, nilEnvironment.RunID())
	assert.False(t, nilEnvironment.CanStreamToolUpdates())
	require.NoError(t, nilEnvironment.Close(t.Context()))
	_, err := nilEnvironment.activeRunID()
	require.ErrorContains(t, err, "required")

	_, err = NewRemoteEnvironment(nil, "runner-1").Open(t.Context(), RunSpec{ConversationID: "conversation-1"})
	require.ErrorContains(t, err, "controller")
	_, err = NewRemoteEnvironment(&fakeRemoteController{}, "").Open(t.Context(), RunSpec{ConversationID: "conversation-1"})
	require.ErrorContains(t, err, "runner id")
	_, err = NewRemoteEnvironment(&fakeRemoteController{}, "runner-1").Open(t.Context(), RunSpec{})
	require.ErrorContains(t, err, "conversation id")

	controller := &fakeRemoteController{}
	environment := NewRemoteEnvironment(controller, "runner-1", WithRemoteRunIDGenerator(func() (string, error) {
		return "", errors.New("id failed")
	}))
	_, err = environment.Open(t.Context(), RunSpec{ConversationID: "conversation-1"})
	require.ErrorContains(t, err, "generate remote run id")
	assert.False(t, environment.IsOpen())

	controller.openErr = errors.New("offline")
	environment = NewRemoteEnvironment(controller, "runner-1", WithRemoteRunIDGenerator(func() (string, error) { return "run-1", nil }))
	_, err = environment.Open(t.Context(), RunSpec{ConversationID: "conversation-1"})
	require.ErrorContains(t, err, "offline")

	validController := &fakeRemoteController{manifest: runnerpayload.Manifest{
		ProtocolVersion: protocol.Version,
		RunnerID:        "runner-1",
		RunID:           "run-1",
		Generation:      1,
	}}
	environment = NewRemoteEnvironment(validController, "runner-1", WithRemoteRunIDGenerator(func() (string, error) { return "run-1", nil }))
	_, err = environment.Open(t.Context(), RunSpec{ConversationID: "conversation-1"})
	require.NoError(t, err)
	_, err = environment.Open(t.Context(), RunSpec{ConversationID: "conversation-1"})
	require.ErrorContains(t, err, "already active")
	_, err = environment.DispatchToolCall(t.Context(), ToolRequest{Name: "tool", Input: "not-json"})
	require.ErrorContains(t, err, "valid JSON")
	_, err = environment.ExecuteTool(t.Context(), ToolRequest{Name: "tool", Input: "not-json"}, nil)
	require.ErrorContains(t, err, "valid JSON")
	validController.omitStructured = true
	_, err = environment.DispatchToolResult(t.Context(), ToolOutputRequest{Name: "tool", Input: `{}`, StructuredResult: tooltypes.StructuredToolResult{ToolName: "tool"}})
	require.ErrorContains(t, err, "omitted structured result")
	validController.closeErr = errors.New("close failed")
	err = environment.CloseWithError(t.Context(), errors.New("run failed"))
	require.ErrorContains(t, err, "close failed")
	assert.Equal(t, protocol.RunStatusFailed, validController.closedStatus)
	assert.False(t, environment.IsOpen())
}

func TestRemoteToolResultAccessors(t *testing.T) {
	structured := tooltypes.StructuredToolResult{ToolName: "tool", Success: false, Error: "structured error"}
	result := newRemoteToolResult(runnerpayload.ToolResult{
		AssistantFacing: "assistant",
		Error:           "wire error",
		Structured:      structured,
		ContentParts:    []tooltypes.ToolResultContentPart{{Type: tooltypes.ToolResultContentPartTypeText, Text: "part"}},
	})
	assert.Equal(t, "assistant", result.AssistantFacing())
	assert.Equal(t, "assistant", result.GetResult())
	assert.True(t, result.IsError())
	assert.Equal(t, "wire error", result.GetError())
	assert.Equal(t, structured, result.StructuredData())
	parts := result.ContentParts()
	require.Len(t, parts, 1)
	parts[0].Text = "changed"
	assert.Equal(t, "part", result.ContentParts()[0].Text)
}

func TestRemoteEnvironmentReturnsOversizedToolResultAsInBandError(t *testing.T) {
	controller := &fakeRemoteController{
		manifest: runnerpayload.Manifest{
			ProtocolVersion: protocol.Version,
			RunnerID:        "runner-1",
			RunID:           "run-1",
			Generation:      1,
		},
		executeErr: &protocol.RPCError{
			Code:    protocol.ErrorCodeUnavailable,
			Message: "runner rpc result exceeds the connection message-size limit; return a smaller result",
			Data:    protocol.RPCErrorData{Reason: protocol.ErrorReasonResultTooLarge},
		},
	}
	environment := NewRemoteEnvironment(controller, "runner-1", WithRemoteRunIDGenerator(func() (string, error) { return "run-1", nil }))
	_, err := environment.Open(t.Context(), RunSpec{ConversationID: "conversation-1"})
	require.NoError(t, err)

	execution, err := environment.ExecuteTool(t.Context(), ToolRequest{
		Name:       "file_read",
		Input:      `{"path":"large.log"}`,
		ToolCallID: "tool-1",
	}, nil)
	require.NoError(t, err)
	require.NotNil(t, execution.Result)
	assert.True(t, execution.Result.IsError())
	assert.Contains(t, execution.Result.GetError(), "smaller result")
	assert.False(t, execution.StructuredResult.Success)
	assert.Equal(t, "file_read", execution.StructuredResult.ToolName)
	assert.JSONEq(t, `{"path":"large.log"}`, execution.Input)
}

func TestConvertRemoteManifestRejectsInvalidContextPaths(t *testing.T) {
	environment := NewRemoteEnvironment(&fakeRemoteController{}, "runner-1")
	_, err := environment.convertManifest(runnerpayload.Manifest{ContextFiles: []runnerpayload.ContextFile{{Content: "missing path", Digest: remoteContentDigest("missing path")}}}, llmtypes.Config{})
	require.ErrorContains(t, err, "without a path")

	content := "same"
	_, err = environment.convertManifest(runnerpayload.Manifest{ContextFiles: []runnerpayload.ContextFile{
		{Path: "AGENTS.md", Content: content, Digest: remoteContentDigest(content)},
		{Path: "AGENTS.md", Content: content, Digest: remoteContentDigest(content)},
	}}, llmtypes.Config{})
	require.ErrorContains(t, err, "duplicate context path")
}
