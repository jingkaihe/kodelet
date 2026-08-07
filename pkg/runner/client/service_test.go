package client

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type staticRuntimeProvider struct {
	runtime *extensions.Runtime
}

func (p staticRuntimeProvider) RuntimeWithConfigAndCallContext(context.Context, string, string, extensions.Config, extensions.ExtensionCallContext) (*extensions.Runtime, error) {
	return p.runtime, nil
}

type recordingRuntimeProvider struct {
	runtime           *extensions.Runtime
	discoveryCalls    int
	activeCalls       int
	activeVariant     string
	activeConfig      extensions.Config
	activeCallContext extensions.ExtensionCallContext
}

func (p *recordingRuntimeProvider) RuntimeForCommandDiscoveryWithConfig(context.Context, string, string, extensions.Config) (*extensions.Runtime, error) {
	p.discoveryCalls++
	return p.runtime, nil
}

func (p *recordingRuntimeProvider) RuntimeWithConfigAndCallContext(_ context.Context, _ string, variant string, config extensions.Config, callContext extensions.ExtensionCallContext) (*extensions.Runtime, error) {
	p.activeCalls++
	p.activeVariant = variant
	p.activeConfig = config
	p.activeCallContext = callContext
	return p.runtime, nil
}

type recordingExecutionInstanceProvider struct {
	workspace string
	err       error
	closeErr  error
	returnNil bool
	specs     []ExecutionInstanceSpec
	instances []*recordingExecutionInstance
}

func (p *recordingExecutionInstanceProvider) Create(_ context.Context, spec ExecutionInstanceSpec) (ExecutionInstance, error) {
	if p.err != nil {
		return nil, p.err
	}
	if p.returnNil {
		return nil, nil //nolint:nilnil // Deliberately violates the provider contract to test validation.
	}
	instance := &recordingExecutionInstance{workspace: p.workspace, closeErr: p.closeErr}
	p.specs = append(p.specs, spec)
	p.instances = append(p.instances, instance)
	return instance, nil
}

type recordingExecutionInstance struct {
	workspace string
	closed    bool
	closeErr  error
}

type failingRuntimeProvider struct {
	err error
}

func (p failingRuntimeProvider) RuntimeWithConfigAndCallContext(context.Context, string, string, extensions.Config, extensions.ExtensionCallContext) (*extensions.Runtime, error) {
	return nil, p.err
}

func (i *recordingExecutionInstance) WorkingDirectory() string { return i.workspace }
func (i *recordingExecutionInstance) Close(context.Context) error {
	i.closed = true
	return i.closeErr
}

type failingOpenEnvironment struct {
	agentenv.Environment
	closed bool
}

type blockingToolEnvironment struct {
	agentenv.Environment
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (e *blockingToolEnvironment) ExecuteTool(_ context.Context, request agentenv.ToolRequest, _ agentenv.ToolUpdateSink) (agentenv.ToolExecution, error) {
	e.once.Do(func() { close(e.started) })
	<-e.release
	return agentenv.ToolExecution{
		Input:  request.Input,
		Result: tooltypes.BaseToolResult{Result: "released"},
	}, nil
}

func (*failingOpenEnvironment) Open(context.Context, agentenv.RunSpec) (agentenv.Manifest, error) {
	return agentenv.Manifest{}, errors.New("open failed")
}

func (e *failingOpenEnvironment) Close(context.Context) error {
	e.closed = true
	return nil
}

type recordingPeer struct {
	mu            sync.Mutex
	calls         []string
	callParams    []any
	notifications []string
	updates       []protocol.ToolUpdateParams
}

func (p *recordingPeer) Call(_ context.Context, method string, params any, result any) error {
	p.mu.Lock()
	p.calls = append(p.calls, method)
	p.callParams = append(p.callParams, params)
	p.mu.Unlock()
	switch method {
	case protocol.MethodUIInput:
		value := params.(protocol.UIInputParams)
		response := result.(*extensions.UIInputResponse)
		*response = extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted, Value: value.Request.DefaultValue}
	case protocol.MethodUIConfirm:
		response := result.(*extensions.UIInputResponse)
		*response = extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted, Confirmed: true, Value: "true"}
	case protocol.MethodUISelect:
		value := params.(protocol.UISelectParams)
		response := result.(*extensions.UIInputResponse)
		*response = extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted, Value: value.Request.Options[0]}
	case protocol.MethodUINotify:
		response := result.(*extensions.UIInputResponse)
		*response = extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted}
	case protocol.MethodUITranscriptAppend:
		response := result.(*extensions.UITranscriptAppendResponse)
		*response = extensions.UITranscriptAppendResponse{Accepted: true}
	case protocol.MethodUIWidgetSet,
		protocol.MethodUIWidgetFrame,
		protocol.MethodUIWidgetRemove,
		protocol.MethodUISurfaceOpen,
		protocol.MethodUISurfaceFrame,
		protocol.MethodUISurfaceClose:
		response := result.(*extensions.UIFrameResponse)
		*response = extensions.UIFrameResponse{Accepted: true, LatestSequence: 7}
	}
	return nil
}

func (p *recordingPeer) Notify(_ context.Context, method string, _ any) error {
	p.mu.Lock()
	p.notifications = append(p.notifications, method)
	p.mu.Unlock()
	return nil
}

func (p *recordingPeer) NotifyUpdate(_ string, params any) error {
	p.mu.Lock()
	p.updates = append(p.updates, params.(protocol.ToolUpdateParams))
	p.mu.Unlock()
	return nil
}

func TestServiceOpensPinnedManifestAndExecutesRunnerTool(t *testing.T) {
	workspace := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte("# Workspace rules"), 0o600))
	filePath := filepath.Join(workspace, "hello.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("hello runner\n"), 0o600))
	recipeDir := filepath.Join(workspace, ".kodelet", "recipes")
	require.NoError(t, os.MkdirAll(recipeDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(recipeDir, "review.md"), []byte(`---
allowed_tools:
  - file_read
allowed_commands:
  - go test *
---
Review the runner workspace.`), 0o600))
	promptPath := filepath.Join(workspace, "runner.tmpl")
	require.NoError(t, os.WriteFile(promptPath, []byte("runner prompt {{.WorkingDirectory}}"), 0o600))

	runtime := extensions.EmptyRuntime()
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	loadedEnvironmentProfile := ""
	service, err := NewService(t.Context(), workspace, ServiceOptions{
		RuntimeProvider: staticRuntimeProvider{runtime: runtime},
		ConfigLoader: func(profile string) (llmtypes.Config, error) {
			loadedEnvironmentProfile = profile
			return llmtypes.Config{
				Provider:        "runner-default",
				Model:           "runner-model",
				Sysprompt:       promptPath,
				AllowedCommands: []string{"go test *"},
				AllowedTools:    []string{"file_read"},
				ToolMode:        llmtypes.ToolModeFull,
			}, nil
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	peer := &recordingPeer{}
	service.Attach(peer)
	require.NoError(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner-1", Generation: 4}))
	probeDigest, err := service.ProbeManifestDigest(t.Context())
	require.NoError(t, err)
	assert.NotEmpty(t, probeDigest)
	state, activeRunID, heartbeatDigest := service.HeartbeatSnapshot()
	assert.Equal(t, protocol.RunnerStateIdle, state)
	assert.Empty(t, activeRunID)
	assert.Equal(t, probeDigest, heartbeatDigest)

	manifest := callService[protocol.Manifest](t, service, protocol.MethodRunOpen, protocol.RunOpenParams{
		RunID:          "run-1",
		ConversationID: "conversation-1",
		ClientCapabilities: protocol.ClientCapabilities{
			InteractiveUI: true,
		},
		Agent: protocol.AgentDescriptor{
			Provider:           "openai",
			Model:              "gpt-test",
			Profile:            "control-work",
			EnvironmentProfile: "runner-work",
		},
		ReservedToolNames: []string{"get_goal", "update_goal", "read_conversation"},
	})
	assert.Equal(t, "runner-1", manifest.RunnerID)
	assert.Equal(t, "runner-work", loadedEnvironmentProfile)
	assert.Equal(t, "run-1", manifest.RunID)
	assert.Equal(t, int64(4), manifest.Generation)
	assert.Equal(t, workspace, manifest.WorkingDirectory)
	assert.Equal(t, "runner prompt {{.WorkingDirectory}}", manifest.Config.SystemPromptContent)
	assert.NotEmpty(t, manifest.Digest)
	assert.NotContains(t, manifestToolNames(manifest), "get_goal")
	assert.Contains(t, manifestToolNames(manifest), "file_read")
	require.NotEmpty(t, manifest.ContextFiles)
	assert.Contains(t, manifest.ContextFiles[0].Content, "Workspace rules")
	assert.Equal(t, manifest.Digest, mustProbeManifestDigest(t, service))
	state, activeRunID, heartbeatDigest = service.HeartbeatSnapshot()
	assert.Equal(t, protocol.RunnerStateRunning, state)
	assert.Equal(t, "run-1", activeRunID)
	assert.Equal(t, manifest.Digest, heartbeatDigest)
	require.ErrorContains(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner-2", Generation: 5}), "active run")

	_, rpcErr := service.HandleRequest(t.Context(), protocol.MethodRunOpen, mustJSON(t, protocol.RunOpenParams{
		RunID: "run-2", ConversationID: "conversation-2",
	}))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeBusy, rpcErr.Code)

	initResult := callService[protocol.LifecycleDispatchResult](t, service, protocol.MethodLifecycleDispatch, protocol.LifecycleDispatchParams{
		RunID:        "run-1",
		Event:        protocol.LifecycleAgentInit,
		SystemPrompt: "base prompt",
		AllowedTools: []string{"file_read"},
	})
	assert.Equal(t, "base prompt", initResult.SystemPrompt)
	assert.Equal(t, []string{"file_read"}, initResult.AllowedTools)

	commandResult := callService[protocol.CommandExecuteResult](t, service, protocol.MethodCommandExecute, protocol.CommandExecuteParams{
		RunID: "run-1", Message: "/review focus on tests",
	})
	assert.True(t, commandResult.Matched)
	assert.Equal(t, "review", commandResult.CommandName)
	assert.Contains(t, commandResult.Prompt, "Review the runner workspace")
	assert.Equal(t, []string{"file_read"}, commandResult.AllowedTools)

	userMessage := callService[protocol.LifecycleDispatchResult](t, service, protocol.MethodLifecycleDispatch, protocol.LifecycleDispatchParams{
		RunID: "run-1", Event: protocol.LifecycleUserMessage, Message: "hello",
	})
	assert.Equal(t, "hello", userMessage.Message)
	callService[protocol.LifecycleDispatchResult](t, service, protocol.MethodLifecycleDispatch, protocol.LifecycleDispatchParams{
		RunID: "run-1", Event: protocol.LifecycleAgentStart,
	})
	callService[protocol.LifecycleDispatchResult](t, service, protocol.MethodLifecycleDispatch, protocol.LifecycleDispatchParams{
		RunID: "run-1", Event: protocol.LifecycleTurnStart, TurnNumber: 2,
	})
	callService[protocol.LifecycleDispatchResult](t, service, protocol.MethodLifecycleDispatch, protocol.LifecycleDispatchParams{
		RunID: "run-1", Event: protocol.LifecycleTurnEnd, FinalOutput: "done", TurnCount: 2,
	})
	agentEnd := callService[protocol.LifecycleDispatchResult](t, service, protocol.MethodLifecycleDispatch, protocol.LifecycleDispatchParams{
		RunID: "run-1", Event: protocol.LifecycleAgentEnd,
	})
	assert.Empty(t, agentEnd.FollowUpMessages)
	toolCall := callService[protocol.LifecycleDispatchResult](t, service, protocol.MethodLifecycleDispatch, protocol.LifecycleDispatchParams{
		RunID: "run-1", Event: protocol.LifecycleToolCall, ToolName: "file_read", ToolCallID: "policy-1", ToolInput: json.RawMessage(`{}`),
	})
	assert.JSONEq(t, `{}`, string(toolCall.ToolInput))
	structured := tooltypes.StructuredToolResult{ToolName: "file_read", Success: true}
	toolUpdate := callService[protocol.LifecycleDispatchResult](t, service, protocol.MethodLifecycleDispatch, protocol.LifecycleDispatchParams{
		RunID: "run-1", Event: protocol.LifecycleToolUpdate, ToolName: "file_read", ToolCallID: "policy-1", ToolInput: json.RawMessage(`{}`), StructuredResult: &structured,
	})
	assert.True(t, toolUpdate.Accepted)
	toolLifecycleResult := callService[protocol.LifecycleDispatchResult](t, service, protocol.MethodLifecycleDispatch, protocol.LifecycleDispatchParams{
		RunID: "run-1", Event: protocol.LifecycleToolResult, ToolName: "file_read", ToolCallID: "policy-1", ToolInput: json.RawMessage(`{}`), StructuredResult: &structured,
	})
	assert.True(t, toolLifecycleResult.Accepted)

	_, rpcErr = service.HandleRequest(t.Context(), protocol.MethodLifecycleDispatch, mustJSON(t, protocol.LifecycleDispatchParams{
		RunID: "run-1", Event: protocol.LifecycleToolResult,
	}))
	require.NotNil(t, rpcErr)
	assert.Contains(t, rpcErr.Message, "structuredResult")
	_, rpcErr = service.HandleRequest(t.Context(), protocol.MethodLifecycleDispatch, mustJSON(t, protocol.LifecycleDispatchParams{
		RunID: "run-1", Event: protocol.LifecycleEvent("unknown"),
	}))
	require.NotNil(t, rpcErr)
	assert.Contains(t, rpcErr.Message, "unsupported lifecycle")

	toolResult := callService[protocol.ToolExecuteResult](t, service, protocol.MethodToolExecute, protocol.ToolExecuteParams{
		RunID:      "run-1",
		ToolCallID: "tool-1",
		Name:       "file_read",
		Input:      json.RawMessage(`{"file_path":"` + filePath + `","offset":1,"line_limit":10}`),
	})
	assert.Contains(t, toolResult.Result.AssistantFacing, "hello runner")
	assert.True(t, toolResult.Result.Structured.Success)
	_, rpcErr = service.HandleRequest(t.Context(), protocol.MethodToolExecute, mustJSON(t, protocol.ToolExecuteParams{RunID: "run-1"}))
	require.NotNil(t, rpcErr)
	assert.Contains(t, rpcErr.Message, "toolCallId and name")

	uiResponse, err := service.Input(t.Context(), extensions.UIInputRequest{Title: "Input", DefaultValue: "answer"})
	require.NoError(t, err)
	assert.Equal(t, "answer", uiResponse.Value)
	service.HandleNotification(t.Context(), protocol.MethodUISurfaceInput, mustJSON(t, protocol.UISurfaceInputParams{
		RunID: "run-1", Owner: protocol.ExtensionOwner{ExtensionID: "missing", Generation: 1}, Lifecycle: 1,
		Request: extensions.UISurfaceInputNotification{ID: "surface", Sequence: 1, Kind: extensions.UISurfaceInputKey, Key: "enter"},
	}))
	service.HandleNotification(t.Context(), protocol.MethodUISurfaceResize, mustJSON(t, protocol.UISurfaceResizeParams{
		RunID: "run-1", Owner: protocol.ExtensionOwner{ExtensionID: "missing", Generation: 1}, Lifecycle: 1,
		Request: extensions.UISurfaceResizeNotification{ID: "surface", Sequence: 1, Width: 80, Height: 24},
	}))
	service.HandleNotification(t.Context(), protocol.MethodUISurfaceInput, json.RawMessage(`not-json`))

	_, rpcErr = service.HandleRequest(t.Context(), "runner.unknown", json.RawMessage(`{}`))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeMethodNotFound, rpcErr.Code)
	_, rpcErr = service.HandleRequest(t.Context(), protocol.MethodRunCancel, json.RawMessage(`not-json`))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeInvalidParams, rpcErr.Code)
	require.ErrorContains(t, service.closeRun(t.Context(), "another-run"), "another runner run")

	callService[any](t, service, protocol.MethodRunClose, protocol.RunCloseParams{RunID: "run-1"})
	state, activeRunID, heartbeatDigest = service.HeartbeatSnapshot()
	assert.Equal(t, protocol.RunnerStateIdle, state)
	assert.Empty(t, activeRunID)
	assert.Equal(t, manifest.Digest, heartbeatDigest)
}

func TestServiceManifestProbeDoesNotStartRunLifecycle(t *testing.T) {
	workspace := t.TempDir()
	runtime := extensions.EmptyRuntime()
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	provider := &recordingRuntimeProvider{runtime: runtime}
	var loadedProfiles []string
	service, err := NewService(t.Context(), workspace, ServiceOptions{
		RuntimeProvider: provider,
		ConfigLoader: func(profile string) (llmtypes.Config, error) {
			loadedProfiles = append(loadedProfiles, profile)
			return llmtypes.Config{
				Provider:          "openai",
				Model:             "gpt-test",
				ExtensionSettings: map[string]any{"enabled": false},
			}, nil
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	service.Attach(&recordingPeer{})
	require.NoError(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner-1", Generation: 1}))

	_, err = service.ProbeManifestDigest(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, provider.discoveryCalls)
	assert.Zero(t, provider.activeCalls)
	assert.Equal(t, []string{""}, loadedProfiles)

	callService[protocol.Manifest](t, service, protocol.MethodRunOpen, protocol.RunOpenParams{
		RunID: "run-1", ConversationID: "conversation-1",
		Agent: protocol.AgentDescriptor{Provider: "anthropic", Model: "claude-test", EnvironmentProfile: "runner-work"},
	})
	assert.Equal(t, 1, provider.activeCalls)
	assert.Equal(t, []string{"", "runner-work"}, loadedProfiles)
	assert.Equal(t, "runner-work", provider.activeVariant)
	assert.False(t, provider.activeConfig.Enabled)
	assert.Equal(t, "conversation-1", provider.activeCallContext.ConversationID)
	assert.Equal(t, "anthropic", provider.activeCallContext.Provider)
	assert.Equal(t, "claude-test", provider.activeCallContext.Model)
	callService[any](t, service, protocol.MethodRunClose, protocol.RunCloseParams{RunID: "run-1"})
}

func TestServiceCloseRunDoesNotWaitForeverForCanceledOperation(t *testing.T) {
	workspace := t.TempDir()
	runtime := extensions.EmptyRuntime()
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	blocking := &blockingToolEnvironment{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	service, err := NewService(t.Context(), workspace, ServiceOptions{
		RuntimeProvider: staticRuntimeProvider{runtime: runtime},
		EnvironmentFactory: func(workingDirectory string, runtime *extensions.Runtime) agentenv.Environment {
			blocking.Environment = agentenv.NewLocalEnvironment(workingDirectory, runtime)
			return blocking
		},
		CleanupTimeout: 20 * time.Millisecond,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	service.Attach(&recordingPeer{})
	require.NoError(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner-1", Generation: 1}))
	callService[protocol.Manifest](t, service, protocol.MethodRunOpen, protocol.RunOpenParams{
		RunID: "run-1", ConversationID: "conversation-1",
	})

	operationDone := make(chan error, 1)
	go func() {
		_, operationErr := service.executeTool(context.Background(), protocol.ToolExecuteParams{
			RunID: "run-1", ToolCallID: "tool-1", Name: "file_read", Input: json.RawMessage(`{}`),
		})
		operationDone <- operationErr
	}()
	select {
	case <-blocking.started:
	case <-time.After(time.Second):
		t.Fatal("tool operation did not start")
	}

	startedAt := time.Now()
	err = service.closeRun(t.Context(), "run-1")
	require.ErrorContains(t, err, "timed out waiting for runner operations to stop")
	assert.Less(t, time.Since(startedAt), time.Second)
	state, activeRunID, _ := service.HeartbeatSnapshot()
	assert.Equal(t, protocol.RunnerStateError, state)
	assert.Empty(t, activeRunID)
	_, rpcErr := service.HandleRequest(t.Context(), protocol.MethodRunOpen, mustJSON(t, protocol.RunOpenParams{
		RunID: "run-2", ConversationID: "conversation-2",
	}))
	require.NotNil(t, rpcErr)
	assert.Contains(t, rpcErr.Message, "requires restart")

	close(blocking.release)
	require.NoError(t, <-operationDone)
}

func TestServiceReturnsUnavailableWhenClientDoesNotSupportInteractiveUI(t *testing.T) {
	workspace := t.TempDir()
	runtime := extensions.EmptyRuntime()
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	service, err := NewService(t.Context(), workspace, ServiceOptions{
		RuntimeProvider: staticRuntimeProvider{runtime: runtime},
		ConfigLoader: func(string) (llmtypes.Config, error) {
			return llmtypes.Config{}, nil
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	peer := &recordingPeer{}
	service.Attach(peer)
	require.NoError(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner-1", Generation: 1}))
	callService[protocol.Manifest](t, service, protocol.MethodRunOpen, protocol.RunOpenParams{
		RunID: "run-1", ConversationID: "conversation-1",
	})

	response, err := service.Input(t.Context(), extensions.UIInputRequest{Title: "Input"})

	require.NoError(t, err)
	assert.Equal(t, extensions.UIInputStatusUnavailable, response.Status)
	assert.Contains(t, response.Reason, "not available")
	peer.mu.Lock()
	assert.NotContains(t, peer.calls, protocol.MethodUIInput)
	peer.mu.Unlock()
	callService[any](t, service, protocol.MethodRunClose, protocol.RunCloseParams{RunID: "run-1"})
}

type recordingUIExtensionSource struct {
	owner         extensions.UIExtensionOwner
	notifications []string
}

func (s *recordingUIExtensionSource) ExtensionUIOwner() extensions.UIExtensionOwner {
	return s.owner
}

func (s *recordingUIExtensionSource) NotifyExtensionUI(_ context.Context, method string, _ any) error {
	s.notifications = append(s.notifications, method)
	return nil
}

func TestServiceProxiesInteractiveAndPersistentUI(t *testing.T) {
	runtime := extensions.EmptyRuntime()
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	service, err := NewService(t.Context(), t.TempDir(), ServiceOptions{
		RuntimeProvider: staticRuntimeProvider{runtime: runtime},
		ConfigLoader: func(string) (llmtypes.Config, error) {
			return llmtypes.Config{}, nil
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	peer := &recordingPeer{}
	service.Attach(peer)
	require.NoError(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner-1", Generation: 1}))
	callService[protocol.Manifest](t, service, protocol.MethodRunOpen, protocol.RunOpenParams{
		RunID: "run-1", ConversationID: "conversation-1",
		ClientCapabilities: protocol.ClientCapabilities{InteractiveUI: true, PersistentSurfaces: true},
	})

	source := &recordingUIExtensionSource{owner: extensions.UIExtensionOwner{ExtensionID: "extension-1", Generation: 4}}
	interactiveCtx := extensions.ContextWithUIExtensionOwner(t.Context(), source.owner)
	input, err := service.Input(interactiveCtx, extensions.UIInputRequest{ID: "shared-id", Title: "Input", DefaultValue: "draft"})
	require.NoError(t, err)
	assert.Equal(t, "draft", input.Value)
	confirmation, err := service.Confirm(t.Context(), extensions.UIConfirmRequest{Title: "Confirm"})
	require.NoError(t, err)
	assert.True(t, confirmation.Confirmed)
	selection, err := service.Select(t.Context(), extensions.UISelectRequest{Title: "Select", Options: []string{"one", "two"}})
	require.NoError(t, err)
	assert.Equal(t, "one", selection.Value)
	notification, err := service.Notify(t.Context(), extensions.UINotifyRequest{Message: "done"})
	require.NoError(t, err)
	assert.Equal(t, extensions.UIInputStatusSubmitted, notification.Status)

	frame := extensions.UIFrame{Sequence: 7, Lines: []extensions.UIFrameLine{{Spans: []extensions.UIStyledSpan{{Text: "frame"}}}}}
	frameResponses := []extensions.UIFrameResponse{}
	response, err := service.SetWidget(t.Context(), source, extensions.UIWidgetSetRequest{ID: "widget", Placement: extensions.UIWidgetPlacementAboveComposer, Frame: frame})
	require.NoError(t, err)
	frameResponses = append(frameResponses, response)
	response, err = service.UpdateWidget(t.Context(), source, extensions.UIWidgetFrameRequest{ID: "widget", Frame: frame})
	require.NoError(t, err)
	frameResponses = append(frameResponses, response)
	response, err = service.RemoveWidget(t.Context(), source, extensions.UIWidgetRemoveRequest{ID: "widget", Sequence: 8})
	require.NoError(t, err)
	frameResponses = append(frameResponses, response)
	transcript, err := service.AppendTranscript(t.Context(), source, extensions.UITranscriptAppendRequest{Message: "entry"})
	require.NoError(t, err)
	assert.True(t, transcript.Accepted)
	response, err = service.OpenSurface(t.Context(), source, extensions.UISurfaceOpenRequest{ID: "surface", Frame: frame})
	require.NoError(t, err)
	frameResponses = append(frameResponses, response)
	response, err = service.UpdateSurface(t.Context(), source, extensions.UISurfaceFrameRequest{ID: "surface", Frame: frame})
	require.NoError(t, err)
	frameResponses = append(frameResponses, response)
	response, err = service.CloseSurface(t.Context(), source, extensions.UISurfaceCloseRequest{ID: "surface", Sequence: 8})
	require.NoError(t, err)
	frameResponses = append(frameResponses, response)
	for _, response := range frameResponses {
		assert.True(t, response.Accepted)
		assert.Equal(t, uint64(7), response.LatestSequence)
	}
	service.CleanupExtensionUI(source.owner)

	peer.mu.Lock()
	assert.Equal(t, []string{
		protocol.MethodUIInput,
		protocol.MethodUIConfirm,
		protocol.MethodUISelect,
		protocol.MethodUINotify,
		protocol.MethodUIWidgetSet,
		protocol.MethodUIWidgetFrame,
		protocol.MethodUIWidgetRemove,
		protocol.MethodUITranscriptAppend,
		protocol.MethodUISurfaceOpen,
		protocol.MethodUISurfaceFrame,
		protocol.MethodUISurfaceClose,
	}, peer.calls)
	inputParams := peer.callParams[0].(protocol.UIInputParams)
	confirmParams := peer.callParams[1].(protocol.UIConfirmParams)
	selectParams := peer.callParams[2].(protocol.UISelectParams)
	widgetParams := peer.callParams[4].(protocol.UIWidgetSetParams)
	peer.mu.Unlock()
	assert.Equal(t, protocol.ExtensionOwner{ExtensionID: "extension-1", Generation: 4}, inputParams.Owner)
	assert.Equal(t, scopedInteractiveUIRequestID(inputParams.Owner, "shared-id"), inputParams.Request.ID)
	assert.NotEqual(t, scopedInteractiveUIRequestID(protocol.ExtensionOwner{ExtensionID: "extension-2", Generation: 4}, "shared-id"), inputParams.Request.ID)
	assert.NotEmpty(t, confirmParams.Request.ID)
	assert.NotEmpty(t, selectParams.Request.ID)
	assert.NotEqual(t, confirmParams.Request.ID, selectParams.Request.ID)
	assert.Equal(t, "run-1", widgetParams.RunID)
	assert.Equal(t, protocol.ExtensionOwner{ExtensionID: "extension-1", Generation: 4}, widgetParams.Owner)

	callService[any](t, service, protocol.MethodRunClose, protocol.RunCloseParams{RunID: "run-1"})
	_, err = service.Input(t.Context(), extensions.UIInputRequest{Title: "closed"})
	require.ErrorContains(t, err, "no active UI run")
}

func TestServicePersistentUIRequiresCapabilityAndSource(t *testing.T) {
	runtime := extensions.EmptyRuntime()
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	service, err := NewService(t.Context(), t.TempDir(), ServiceOptions{
		RuntimeProvider: staticRuntimeProvider{runtime: runtime},
		ConfigLoader:    func(string) (llmtypes.Config, error) { return llmtypes.Config{}, nil },
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	peer := &recordingPeer{}
	service.Attach(peer)
	require.NoError(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner-1", Generation: 1}))
	callService[protocol.Manifest](t, service, protocol.MethodRunOpen, protocol.RunOpenParams{
		RunID: "run-1", ConversationID: "conversation-1",
		ClientCapabilities: protocol.ClientCapabilities{InteractiveUI: true},
	})

	_, err = service.SetWidget(t.Context(), nil, extensions.UIWidgetSetRequest{})
	require.ErrorContains(t, err, "source is required")
	source := &recordingUIExtensionSource{owner: extensions.UIExtensionOwner{ExtensionID: "extension-1", Generation: 1}}
	frameResponse, err := service.OpenSurface(t.Context(), source, extensions.UISurfaceOpenRequest{ID: "surface"})
	require.NoError(t, err)
	assert.False(t, frameResponse.Accepted)
	assert.Contains(t, frameResponse.Reason, "not available")
	transcriptResponse, err := service.AppendTranscript(t.Context(), source, extensions.UITranscriptAppendRequest{Message: "entry"})
	require.NoError(t, err)
	assert.False(t, transcriptResponse.Accepted)
	assert.Contains(t, transcriptResponse.Reason, "not available")

	peer.mu.Lock()
	assert.Empty(t, peer.calls)
	peer.mu.Unlock()
	callService[any](t, service, protocol.MethodRunClose, protocol.RunCloseParams{RunID: "run-1"})

	service.Attach(nil)
	_, err = service.Notify(t.Context(), extensions.UINotifyRequest{Message: "missing"})
	require.ErrorContains(t, err, "connection is unavailable")
}

func TestServiceCreatesEnvironmentInsideExecutionInstanceAndCleansItUp(t *testing.T) {
	sourceWorkspace := t.TempDir()
	instanceWorkspace := t.TempDir()
	runtime := extensions.EmptyRuntime()
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	provider := &recordingExecutionInstanceProvider{workspace: instanceWorkspace}
	var environmentWorkspace string
	service, err := NewService(t.Context(), sourceWorkspace, ServiceOptions{
		RuntimeProvider: staticRuntimeProvider{runtime: runtime},
		ConfigLoader: func(string) (llmtypes.Config, error) {
			return llmtypes.Config{AllowedTools: []string{"file_read"}}, nil
		},
		EnvironmentFactory: func(workingDirectory string, runtime *extensions.Runtime) agentenv.Environment {
			environmentWorkspace = workingDirectory
			return agentenv.NewLocalEnvironment(workingDirectory, runtime)
		},
		ExecutionInstanceProvider: provider,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	service.Attach(&recordingPeer{})
	require.NoError(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner-1", Generation: 1}))

	manifest := callService[protocol.Manifest](t, service, protocol.MethodRunOpen, protocol.RunOpenParams{
		RunID: "run-1", ConversationID: "conversation-1",
	})

	assert.Equal(t, instanceWorkspace, environmentWorkspace)
	assert.Equal(t, instanceWorkspace, manifest.WorkingDirectory)
	require.Len(t, provider.specs, 1)
	assert.Equal(t, ExecutionInstanceSpec{RunID: "run-1", ConversationID: "conversation-1"}, provider.specs[0])
	require.Len(t, provider.instances, 1)
	assert.False(t, provider.instances[0].closed)
	callService[any](t, service, protocol.MethodRunClose, protocol.RunCloseParams{RunID: "run-1"})
	assert.True(t, provider.instances[0].closed)
}

func TestServiceCleansExecutionInstanceWhenEnvironmentOpenFails(t *testing.T) {
	provider := &recordingExecutionInstanceProvider{workspace: t.TempDir()}
	environment := &failingOpenEnvironment{}
	runtime := extensions.EmptyRuntime()
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	service, err := NewService(t.Context(), t.TempDir(), ServiceOptions{
		RuntimeProvider: staticRuntimeProvider{runtime: runtime},
		ConfigLoader: func(string) (llmtypes.Config, error) {
			return llmtypes.Config{}, nil
		},
		EnvironmentFactory:        func(string, *extensions.Runtime) agentenv.Environment { return environment },
		ExecutionInstanceProvider: provider,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	service.Attach(&recordingPeer{})
	require.NoError(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner-1", Generation: 1}))

	_, rpcErr := service.HandleRequest(t.Context(), protocol.MethodRunOpen, mustJSON(t, protocol.RunOpenParams{
		RunID: "run-1", ConversationID: "conversation-1",
	}))

	require.NotNil(t, rpcErr)
	assert.Contains(t, rpcErr.Message, "open failed")
	assert.True(t, environment.closed)
	require.Len(t, provider.instances, 1)
	assert.True(t, provider.instances[0].closed)
}

func TestServiceRequiresRestartWhenFailedOpenCleanupFails(t *testing.T) {
	provider := &recordingExecutionInstanceProvider{workspace: t.TempDir(), closeErr: errors.New("instance close failed")}
	runtime := extensions.EmptyRuntime()
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	service, err := NewService(t.Context(), t.TempDir(), ServiceOptions{
		RuntimeProvider:           staticRuntimeProvider{runtime: runtime},
		ConfigLoader:              func(string) (llmtypes.Config, error) { return llmtypes.Config{}, nil },
		EnvironmentFactory:        func(string, *extensions.Runtime) agentenv.Environment { return nil },
		ExecutionInstanceProvider: provider,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	service.Attach(&recordingPeer{})
	require.NoError(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner-1", Generation: 1}))

	_, rpcErr := service.HandleRequest(t.Context(), protocol.MethodRunOpen, mustJSON(t, protocol.RunOpenParams{
		RunID: "run-1", ConversationID: "conversation-1",
	}))
	require.NotNil(t, rpcErr)
	assert.Contains(t, rpcErr.Message, "factory returned nil")
	state, activeRunID, _ := service.HeartbeatSnapshot()
	assert.Equal(t, protocol.RunnerStateError, state)
	assert.Empty(t, activeRunID)
	_, rpcErr = service.HandleRequest(t.Context(), protocol.MethodRunOpen, mustJSON(t, protocol.RunOpenParams{
		RunID: "run-2", ConversationID: "conversation-2",
	}))
	require.NotNil(t, rpcErr)
	assert.Contains(t, rpcErr.Message, "requires restart")
}

func TestServiceRequiresRestartWhenManifestProbeCleanupFails(t *testing.T) {
	provider := &recordingExecutionInstanceProvider{workspace: t.TempDir(), closeErr: errors.New("instance close failed")}
	runtime := extensions.EmptyRuntime()
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	service, err := NewService(t.Context(), t.TempDir(), ServiceOptions{
		RuntimeProvider:           staticRuntimeProvider{runtime: runtime},
		ConfigLoader:              func(string) (llmtypes.Config, error) { return llmtypes.Config{}, nil },
		ExecutionInstanceProvider: provider,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	service.Attach(&recordingPeer{})
	require.NoError(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner-1", Generation: 1}))

	_, err = service.ProbeManifestDigest(t.Context())
	require.ErrorContains(t, err, "failed to clean up runner manifest probe resources")
	state, activeRunID, digest := service.HeartbeatSnapshot()
	assert.Equal(t, protocol.RunnerStateError, state)
	assert.Empty(t, activeRunID)
	assert.Empty(t, digest)
}

func TestDirectWorkspaceInstanceProviderReturnsFreshHandles(t *testing.T) {
	workspace := t.TempDir()
	provider, err := NewDirectWorkspaceInstanceProvider(workspace)
	require.NoError(t, err)

	first, err := provider.Create(t.Context(), ExecutionInstanceSpec{RunID: "run-1"})
	require.NoError(t, err)
	second, err := provider.Create(t.Context(), ExecutionInstanceSpec{RunID: "run-2"})
	require.NoError(t, err)

	assert.NotSame(t, first, second)
	assert.Equal(t, workspace, first.WorkingDirectory())
	assert.Equal(t, workspace, second.WorkingDirectory())
	require.NoError(t, first.Close(t.Context()))
	require.NoError(t, second.Close(t.Context()))
}

func TestServiceRunCancelPropagatesToActiveToolOperation(t *testing.T) {
	workspace := t.TempDir()
	runtime := extensions.EmptyRuntime()
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	blocking := &blockingEnvironment{started: make(chan struct{})}
	service, err := NewService(t.Context(), workspace, ServiceOptions{
		RuntimeProvider: staticRuntimeProvider{runtime: runtime},
		ConfigLoader: func(string) (llmtypes.Config, error) {
			return llmtypes.Config{}, nil
		},
		EnvironmentFactory: func(string, *extensions.Runtime) agentenv.Environment { return blocking },
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	service.Attach(&recordingPeer{})
	require.NoError(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner-1", Generation: 1}))
	callService[protocol.Manifest](t, service, protocol.MethodRunOpen, protocol.RunOpenParams{
		RunID: "run-1", ConversationID: "conversation-1",
	})

	done := make(chan *protocol.RPCError, 1)
	toolParams := mustJSON(t, protocol.ToolExecuteParams{
		RunID: "run-1", ToolCallID: "tool-1", Name: "wait", Input: json.RawMessage(`{}`),
	})
	go func() {
		_, rpcErr := service.HandleRequest(t.Context(), protocol.MethodToolExecute, toolParams)
		done <- rpcErr
	}()
	<-blocking.started
	callService[any](t, service, protocol.MethodRunCancel, protocol.RunCancelParams{RunID: "run-1"})
	rpcErr := <-done
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeUnavailable, rpcErr.Code)
	callService[any](t, service, protocol.MethodRunClose, protocol.RunCloseParams{RunID: "run-1"})
}

func TestServiceOpenRunFailurePathsReleaseCapacityAndInstances(t *testing.T) {
	tests := []struct {
		name               string
		provider           *recordingExecutionInstanceProvider
		runtimeProvider    RuntimeProvider
		configLoader       ConfigLoader
		environmentFactory EnvironmentFactory
		wantError          string
		wantClosedInstance bool
	}{
		{
			name:      "instance provider error",
			provider:  &recordingExecutionInstanceProvider{err: errors.New("instance failed")},
			wantError: "create runner execution instance",
		},
		{
			name:      "nil instance",
			provider:  &recordingExecutionInstanceProvider{returnNil: true},
			wantError: "returned nil",
		},
		{
			name:               "empty instance workspace",
			provider:           &recordingExecutionInstanceProvider{},
			wantError:          "empty working directory",
			wantClosedInstance: true,
		},
		{
			name:               "config failure",
			provider:           &recordingExecutionInstanceProvider{workspace: t.TempDir()},
			configLoader:       func(string) (llmtypes.Config, error) { return llmtypes.Config{}, errors.New("config failed") },
			wantError:          "load runner configuration",
			wantClosedInstance: true,
		},
		{
			name:               "runtime failure",
			provider:           &recordingExecutionInstanceProvider{workspace: t.TempDir()},
			runtimeProvider:    failingRuntimeProvider{err: errors.New("runtime failed")},
			wantError:          "initialize runner extensions",
			wantClosedInstance: true,
		},
		{
			name:               "nil environment",
			provider:           &recordingExecutionInstanceProvider{workspace: t.TempDir()},
			environmentFactory: func(string, *extensions.Runtime) agentenv.Environment { return nil },
			wantError:          "factory returned nil",
			wantClosedInstance: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := extensions.EmptyRuntime()
			t.Cleanup(func() { require.NoError(t, runtime.Close()) })
			runtimeProvider := test.runtimeProvider
			if runtimeProvider == nil {
				runtimeProvider = staticRuntimeProvider{runtime: runtime}
			}
			configLoader := test.configLoader
			if configLoader == nil {
				configLoader = func(string) (llmtypes.Config, error) { return llmtypes.Config{}, nil }
			}
			service, err := NewService(t.Context(), t.TempDir(), ServiceOptions{
				RuntimeProvider:           runtimeProvider,
				ConfigLoader:              configLoader,
				EnvironmentFactory:        test.environmentFactory,
				ExecutionInstanceProvider: test.provider,
			})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, service.Close()) })
			service.Attach(&recordingPeer{})
			require.NoError(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner-1", Generation: 1}))

			_, rpcErr := service.HandleRequest(t.Context(), protocol.MethodRunOpen, mustJSON(t, protocol.RunOpenParams{
				RunID: "run-1", ConversationID: "conversation-1",
			}))

			require.NotNil(t, rpcErr)
			assert.Contains(t, rpcErr.Message, test.wantError)
			state, activeRunID, _ := service.HeartbeatSnapshot()
			assert.Equal(t, protocol.RunnerStateIdle, state)
			assert.Empty(t, activeRunID)
			if test.wantClosedInstance {
				require.Len(t, test.provider.instances, 1)
				assert.True(t, test.provider.instances[0].closed)
			}
		})
	}
}

func TestServiceAbortAndValidationHelpers(t *testing.T) {
	_, err := NewService(t.Context(), "", ServiceOptions{})
	require.ErrorContains(t, err, "workspace")
	var nilService *Service
	state, runID, digest := nilService.HeartbeatSnapshot()
	assert.Equal(t, protocol.RunnerStateError, state)
	assert.Empty(t, runID)
	assert.Empty(t, digest)
	require.ErrorContains(t, nilService.SetRegistration(protocol.RegisterResult{}), "required")
	_, rpcErr := nilService.HandleRequest(t.Context(), protocol.MethodRunOpen, json.RawMessage(`{}`))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeInternal, rpcErr.Code)
	require.NoError(t, nilService.AbortActiveRun(t.Context()))
	require.NoError(t, nilService.Close())

	runtime := extensions.EmptyRuntime()
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	service, err := NewService(nil, t.TempDir(), ServiceOptions{ //nolint:staticcheck // Verify a nil parent defaults to a background context.
		RuntimeProvider: staticRuntimeProvider{runtime: runtime},
		ConfigLoader:    func(string) (llmtypes.Config, error) { return llmtypes.Config{}, nil },
	})
	require.NoError(t, err)
	service.Attach(&recordingPeer{})
	require.ErrorContains(t, service.SetRegistration(protocol.RegisterResult{}), "incomplete")
	require.NoError(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner-1", Generation: 1}))
	callService[protocol.Manifest](t, service, protocol.MethodRunOpen, protocol.RunOpenParams{RunID: "run-1", ConversationID: "conversation-1"})
	require.NoError(t, service.AbortActiveRun(t.Context()))
	state, runID, _ = service.HeartbeatSnapshot()
	assert.Equal(t, protocol.RunnerStateIdle, state)
	assert.Empty(t, runID)
	require.NoError(t, service.AbortActiveRun(t.Context()))
	require.NoError(t, service.Close())
	require.NoError(t, service.Close())
	_, err = service.ProbeManifestDigest(t.Context())
	require.ErrorContains(t, err, "closed")
}

type blockingEnvironment struct {
	agentenv.Environment
	mu       sync.Mutex
	manifest agentenv.Manifest
	opened   bool
	started  chan struct{}
}

func (e *blockingEnvironment) Open(context.Context, agentenv.RunSpec) (agentenv.Manifest, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.manifest = agentenv.Manifest{WorkingDirectory: "/workspace"}
	e.opened = true
	return e.manifest, nil
}

func (e *blockingEnvironment) IsOpen() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.opened
}

func (e *blockingEnvironment) Manifest() agentenv.Manifest {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.manifest.Clone()
}

func (e *blockingEnvironment) ExecuteTool(ctx context.Context, request agentenv.ToolRequest, _ agentenv.ToolUpdateSink) (agentenv.ToolExecution, error) {
	close(e.started)
	<-ctx.Done()
	return agentenv.ToolExecution{Input: request.Input}, ctx.Err()
}

func (e *blockingEnvironment) Close(context.Context) error {
	e.mu.Lock()
	e.opened = false
	e.mu.Unlock()
	return nil
}

func callService[T any](t *testing.T, service *Service, method string, params any) T {
	t.Helper()
	result, rpcErr := service.HandleRequest(t.Context(), method, mustJSON(t, params))
	require.Nil(t, rpcErr)
	if result == nil {
		var zero T
		return zero
	}
	typed, ok := result.(T)
	require.True(t, ok, "unexpected result type %T", result)
	return typed
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(value)
	require.NoError(t, err)
	return payload
}

func mustProbeManifestDigest(t *testing.T, service *Service) string {
	t.Helper()
	digest, err := service.ProbeManifestDigest(t.Context())
	require.NoError(t, err)
	return digest
}

func manifestToolNames(manifest protocol.Manifest) []string {
	names := make([]string, 0, len(manifest.Tools))
	for _, definition := range manifest.Tools {
		names = append(names, definition.Name)
	}
	return names
}
