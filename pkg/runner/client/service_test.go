package client

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type staticRuntimeProvider struct {
	runtime *extensions.Runtime
}

func TestDecorateRunContextEnablesBackgroundTasks(t *testing.T) {
	ctx := (&Service{}).decorateRunContext(context.Background(), "run-1", "conversation-1")
	capabilities := extensions.RuntimeCapabilitiesFromContext(ctx)
	assert.True(t, capabilities.BackgroundTasks)
	_, ok := extensions.BackgroundTaskHostFromContext(ctx)
	assert.True(t, ok)
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

type leaseRecordingRuntimeProvider struct {
	runtime      *extensions.Runtime
	operationCtx context.Context
	leaseCtx     context.Context
}

type isolatedRuntimeProvider struct {
	runtime        *extensions.Runtime
	releaseStarted chan struct{}
	releaseGate    chan struct{}
	startedOnce    sync.Once
}

type reusableIsolatedRuntimeProvider struct {
	mu       sync.Mutex
	runtime  *extensions.Runtime
	calls    int
	releases int
}

func (p *reusableIsolatedRuntimeProvider) RuntimeWithConfigAndCallContext(context.Context, string, string, extensions.Config, extensions.ExtensionCallContext) (*extensions.Runtime, error) {
	return p.runtime, nil
}

func (p *reusableIsolatedRuntimeProvider) RuntimeWithConfigAndCallContextForIsolatedLease(context.Context, context.Context, string, string, extensions.Config, extensions.ExtensionCallContext) (*extensions.Runtime, func() error, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	return p.runtime, func() error {
		p.mu.Lock()
		p.releases++
		p.mu.Unlock()
		return nil
	}, nil
}

func (p *reusableIsolatedRuntimeProvider) counts() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, p.releases
}

func (p *isolatedRuntimeProvider) RuntimeWithConfigAndCallContext(context.Context, string, string, extensions.Config, extensions.ExtensionCallContext) (*extensions.Runtime, error) {
	return p.runtime, nil
}

func (p *isolatedRuntimeProvider) RuntimeWithConfigAndCallContextForIsolatedLease(context.Context, context.Context, string, string, extensions.Config, extensions.ExtensionCallContext) (*extensions.Runtime, func() error, error) {
	return p.runtime, func() error {
		p.startedOnce.Do(func() { close(p.releaseStarted) })
		<-p.releaseGate
		return nil
	}, nil
}

func (p *leaseRecordingRuntimeProvider) RuntimeWithConfigAndCallContext(ctx context.Context, _ string, _ string, _ extensions.Config, _ extensions.ExtensionCallContext) (*extensions.Runtime, error) {
	p.operationCtx = ctx
	p.leaseCtx = ctx
	return p.runtime, nil
}

func (p *leaseRecordingRuntimeProvider) RuntimeWithConfigAndCallContextForLease(ctx, leaseCtx context.Context, _ string, _ string, _ extensions.Config, _ extensions.ExtensionCallContext) (*extensions.Runtime, error) {
	p.operationCtx = ctx
	p.leaseCtx = leaseCtx
	return p.runtime, nil
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
	workspace         string
	resolvedWorkspace string
	resolveErr        error
	err               error
	closeErr          error
	returnNil         bool
	specs             []ExecutionInstanceSpec
	instances         []*recordingExecutionInstance
}

func (p *recordingExecutionInstanceProvider) ResolveWorkingDirectory(ctx context.Context, requestedCWD string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if p.resolveErr != nil {
		return "", p.resolveErr
	}
	if p.resolvedWorkspace != "" {
		return p.resolvedWorkspace, nil
	}
	if p.workspace != "" {
		return p.workspace, nil
	}
	if requestedCWD != "" {
		return requestedCWD, nil
	}
	return "/runner/workspace", nil
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

type blockingOpenEnvironment struct {
	agentenv.Environment
	started    chan struct{}
	closeCalls atomic.Int32
}

type blockingToolEnvironment struct {
	agentenv.Environment
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

type recordingCommandEnvironment struct {
	agentenv.Environment
	request agentenv.CommandRequest
}

func (e *recordingCommandEnvironment) ExecuteCommand(ctx context.Context, request agentenv.CommandRequest) (agentenv.CommandResult, error) {
	e.request = request
	return e.Environment.ExecuteCommand(ctx, request)
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

func (e *blockingOpenEnvironment) Open(ctx context.Context, _ agentenv.RunSpec) (agentenv.Manifest, error) {
	close(e.started)
	<-ctx.Done()
	return agentenv.Manifest{}, ctx.Err()
}

func (e *blockingOpenEnvironment) Close(context.Context) error {
	e.closeCalls.Add(1)
	return nil
}

type recordingPeer struct {
	mu            sync.Mutex
	calls         []string
	callParams    []any
	notifications []string
	updates       []runnerpayload.ToolUpdateParams
}

func (p *recordingPeer) Call(_ context.Context, method string, params any, result any) error {
	p.mu.Lock()
	p.calls = append(p.calls, method)
	p.callParams = append(p.callParams, params)
	p.mu.Unlock()
	switch method {
	case protocol.MethodUIInput:
		value := params.(runnerpayload.UIInputParams)
		response := result.(*extensions.UIInputResponse)
		*response = extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted, Value: value.Request.DefaultValue}
	case protocol.MethodUIConfirm:
		response := result.(*extensions.UIInputResponse)
		*response = extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted, Confirmed: true, Value: "true"}
	case protocol.MethodUISelect:
		value := params.(runnerpayload.UISelectParams)
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
	p.updates = append(p.updates, params.(runnerpayload.ToolUpdateParams))
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

	manifest := callService[runnerpayload.Manifest](t, service, protocol.MethodRunOpen, protocol.RunOpenParams{
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
	assert.True(t, manifest.Capabilities.PersistentWidgets)
	assert.NotContains(t, manifestToolNames(manifest), "get_goal")
	assert.Contains(t, manifestToolNames(manifest), "file_read")
	require.NotEmpty(t, manifest.ContextFiles)
	assert.Contains(t, manifest.ContextFiles[0].Content, "Workspace rules")
	assert.NotEmpty(t, mustProbeManifestDigest(t, service))
	state, activeRunID, heartbeatDigest = service.HeartbeatSnapshot()
	assert.Equal(t, protocol.RunnerStateRunning, state)
	assert.Equal(t, "run-1", activeRunID)
	assert.Equal(t, manifest.Digest, heartbeatDigest)
	require.ErrorContains(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner-2", Generation: 5}), "active run")

	_, rpcErr := service.HandleRequest(t.Context(), protocol.MethodRunOpen, mustJSON(t, protocol.RunOpenParams{
		RunID: "run-2", ConversationID: "conversation-1",
	}))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeBusy, rpcErr.Code)

	initResult := callService[runnerpayload.LifecycleDispatchResult](t, service, protocol.MethodLifecycleDispatch, runnerpayload.LifecycleDispatchParams{
		RunID:        "run-1",
		Event:        runnerpayload.LifecycleAgentInit,
		SystemPrompt: "base prompt",
		AllowedTools: []string{"file_read"},
	})
	assert.Equal(t, "base prompt", initResult.SystemPrompt)
	assert.Equal(t, []string{"file_read"}, initResult.AllowedTools)

	commandResult := callService[runnerpayload.CommandExecuteResult](t, service, protocol.MethodCommandExecute, runnerpayload.CommandExecuteParams{
		RunID: "run-1", Message: "/review focus on tests",
	})
	assert.True(t, commandResult.Matched)
	assert.Equal(t, "review", commandResult.CommandName)
	assert.Contains(t, commandResult.Prompt, "Review the runner workspace")
	assert.Equal(t, []string{"file_read"}, commandResult.AllowedTools)

	userMessage := callService[runnerpayload.LifecycleDispatchResult](t, service, protocol.MethodLifecycleDispatch, runnerpayload.LifecycleDispatchParams{
		RunID: "run-1", Event: runnerpayload.LifecycleUserMessage, Message: "hello",
	})
	assert.Equal(t, "hello", userMessage.Message)
	callService[runnerpayload.LifecycleDispatchResult](t, service, protocol.MethodLifecycleDispatch, runnerpayload.LifecycleDispatchParams{
		RunID: "run-1", Event: runnerpayload.LifecycleAgentStart,
	})
	callService[runnerpayload.LifecycleDispatchResult](t, service, protocol.MethodLifecycleDispatch, runnerpayload.LifecycleDispatchParams{
		RunID: "run-1", Event: runnerpayload.LifecycleTurnStart, TurnNumber: 2,
	})
	callService[runnerpayload.LifecycleDispatchResult](t, service, protocol.MethodLifecycleDispatch, runnerpayload.LifecycleDispatchParams{
		RunID: "run-1", Event: runnerpayload.LifecycleTurnEnd, FinalOutput: "done", TurnCount: 2,
	})
	agentEnd := callService[runnerpayload.LifecycleDispatchResult](t, service, protocol.MethodLifecycleDispatch, runnerpayload.LifecycleDispatchParams{
		RunID: "run-1", Event: runnerpayload.LifecycleAgentEnd,
	})
	assert.Empty(t, agentEnd.FollowUpMessages)
	toolCall := callService[runnerpayload.LifecycleDispatchResult](t, service, protocol.MethodLifecycleDispatch, runnerpayload.LifecycleDispatchParams{
		RunID: "run-1", Event: runnerpayload.LifecycleToolCall, ToolName: "file_read", ToolCallID: "policy-1", ToolInput: json.RawMessage(`{}`),
	})
	assert.JSONEq(t, `{}`, string(toolCall.ToolInput))
	structured := tooltypes.StructuredToolResult{ToolName: "file_read", Success: true}
	toolUpdate := callService[runnerpayload.LifecycleDispatchResult](t, service, protocol.MethodLifecycleDispatch, runnerpayload.LifecycleDispatchParams{
		RunID: "run-1", Event: runnerpayload.LifecycleToolUpdate, ToolName: "file_read", ToolCallID: "policy-1", ToolInput: json.RawMessage(`{}`), StructuredResult: &structured,
	})
	assert.True(t, toolUpdate.Accepted)
	toolLifecycleResult := callService[runnerpayload.LifecycleDispatchResult](t, service, protocol.MethodLifecycleDispatch, runnerpayload.LifecycleDispatchParams{
		RunID: "run-1", Event: runnerpayload.LifecycleToolResult, ToolName: "file_read", ToolCallID: "policy-1", ToolInput: json.RawMessage(`{}`), StructuredResult: &structured,
	})
	assert.True(t, toolLifecycleResult.Accepted)

	_, rpcErr = service.HandleRequest(t.Context(), protocol.MethodLifecycleDispatch, mustJSON(t, runnerpayload.LifecycleDispatchParams{
		RunID: "run-1", Event: runnerpayload.LifecycleToolResult,
	}))
	require.NotNil(t, rpcErr)
	assert.Contains(t, rpcErr.Message, "structuredResult")
	_, rpcErr = service.HandleRequest(t.Context(), protocol.MethodLifecycleDispatch, mustJSON(t, runnerpayload.LifecycleDispatchParams{
		RunID: "run-1", Event: runnerpayload.LifecycleEvent("unknown"),
	}))
	require.NotNil(t, rpcErr)
	assert.Contains(t, rpcErr.Message, "unsupported lifecycle")

	toolResult := callService[runnerpayload.ToolExecuteResult](t, service, protocol.MethodToolExecute, runnerpayload.ToolExecuteParams{
		RunID:      "run-1",
		ToolCallID: "tool-1",
		Name:       "file_read",
		Input:      json.RawMessage(`{"file_path":"` + filePath + `","offset":1,"line_limit":10}`),
	})
	assert.Contains(t, toolResult.Result.AssistantFacing, "hello runner")
	assert.Contains(t, toolResult.Result.DisplayOutput, "hello runner")
	assert.True(t, toolResult.Result.Structured.Success)
	_, rpcErr = service.HandleRequest(t.Context(), protocol.MethodToolExecute, mustJSON(t, runnerpayload.ToolExecuteParams{RunID: "run-1"}))
	require.NotNil(t, rpcErr)
	assert.Contains(t, rpcErr.Message, "toolCallId and name")

	uiResponse, err := service.Input(t.Context(), extensions.UIInputRequest{Title: "Input", DefaultValue: "answer"})
	require.NoError(t, err)
	assert.Equal(t, "answer", uiResponse.Value)
	service.HandleNotification(t.Context(), protocol.MethodUISurfaceInput, mustJSON(t, runnerpayload.UISurfaceInputParams{
		RunID: "run-1", Owner: runnerpayload.ExtensionOwner{ExtensionID: "missing", Generation: 1}, Lifecycle: 1,
		Request: extensions.UISurfaceInputNotification{ID: "surface", Sequence: 1, Kind: extensions.UISurfaceInputKey, Key: "enter"},
	}))
	service.HandleNotification(t.Context(), protocol.MethodUISurfaceResize, mustJSON(t, runnerpayload.UISurfaceResizeParams{
		RunID: "run-1", Owner: runnerpayload.ExtensionOwner{ExtensionID: "missing", Generation: 1}, Lifecycle: 1,
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

func TestServiceEmitsStructuredRunLifecycleLogs(t *testing.T) {
	logCtx, logOutput := newStructuredRunnerTestLogger(t)
	workspace := t.TempDir()
	runtime := extensions.EmptyRuntime()
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	service, err := NewService(logCtx, workspace, ServiceOptions{
		RuntimeProvider: staticRuntimeProvider{runtime: runtime},
		ConfigLoader:    func(string) (llmtypes.Config, error) { return llmtypes.Config{}, nil },
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	require.NoError(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner-logs", Generation: 3}))

	params := protocol.RunOpenParams{
		RunID:          "run-logs",
		ConversationID: "conversation-logs",
		Agent: protocol.AgentDescriptor{
			Provider:           "openai",
			Model:              "gpt-test",
			Profile:            "control-profile",
			EnvironmentProfile: "runner-profile",
			InvokedBy:          "subagent",
		},
	}
	value, rpcErr := service.HandleRequest(logCtx, protocol.MethodRunOpen, mustJSON(t, params))
	require.Nil(t, rpcErr)
	manifest, ok := value.(runnerpayload.Manifest)
	require.True(t, ok)
	require.NotEmpty(t, manifest.Digest)
	_, rpcErr = service.HandleRequest(logCtx, protocol.MethodRunClose, mustJSON(t, protocol.RunCloseParams{RunID: params.RunID}))
	require.Nil(t, rpcErr)

	entries := decodeStructuredRunnerLogs(t, logOutput)
	openingEntry := requireStructuredRunnerLog(t, entries, "opening runner run")
	assert.Equal(t, workspace, openingEntry["workspace"])
	assert.Equal(t, "runner-logs", openingEntry["runner_id"])
	assert.Equal(t, float64(3), openingEntry["generation"])
	assert.Equal(t, params.RunID, openingEntry["run_id"])
	assert.Equal(t, params.ConversationID, openingEntry["conversation_id"])
	assert.Equal(t, params.Agent.Provider, openingEntry["provider"])
	assert.Equal(t, params.Agent.Model, openingEntry["model"])
	assert.Equal(t, params.Agent.EnvironmentProfile, openingEntry["environment_profile"])
	assert.Equal(t, params.Agent.InvokedBy, openingEntry["invoked_by"])

	openedEntry := requireStructuredRunnerLog(t, entries, "runner run opened")
	assert.Equal(t, manifest.Digest, openedEntry["manifest_digest"])
	assert.Equal(t, workspace, openedEntry["working_directory"])
	assert.NotNil(t, openedEntry["duration"])

	closingEntry := requireStructuredRunnerLog(t, entries, "closing runner run")
	assert.Equal(t, params.RunID, closingEntry["run_id"])
	closedEntry := requireStructuredRunnerLog(t, entries, "runner run closed")
	assert.Equal(t, params.ConversationID, closedEntry["conversation_id"])
	assert.NotNil(t, closedEntry["duration"])
}

func TestSerializeToolResultCapsRemoteDisplayOutput(t *testing.T) {
	result := serializeToolResult(
		tooltypes.BaseToolResult{Result: strings.Repeat("界", maxToolDisplayOutputBytes)},
		tooltypes.StructuredToolResult{ToolName: "custom", Success: true},
	)

	assert.LessOrEqual(t, len(result.DisplayOutput), maxToolDisplayOutputBytes)
	assert.True(t, strings.HasSuffix(result.DisplayOutput, toolDisplayOutputTruncationMarker))
	assert.True(t, utf8.ValidString(result.DisplayOutput))
}

func TestServiceSupportsConcurrentRunsAndProfileProbes(t *testing.T) {
	workspace := t.TempDir()
	runtime := extensions.EmptyRuntime()
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	service, err := NewService(t.Context(), workspace, ServiceOptions{
		RuntimeProvider: staticRuntimeProvider{runtime: runtime},
		ConfigLoader: func(string) (llmtypes.Config, error) {
			return llmtypes.Config{AllowedTools: []string{"file_read"}}, nil
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	require.NoError(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner-1", Generation: 1}))

	for _, params := range []protocol.RunOpenParams{
		{RunID: "run-1", ConversationID: "conversation-1", ClientCapabilities: protocol.ClientCapabilities{InteractiveUI: true}, Agent: protocol.AgentDescriptor{EnvironmentProfile: "gpu"}},
		{RunID: "run-2", ConversationID: "conversation-2", ClientCapabilities: protocol.ClientCapabilities{PersistentSurfaces: true}, Agent: protocol.AgentDescriptor{EnvironmentProfile: "workspace"}},
	} {
		manifest := callService[runnerpayload.Manifest](t, service, protocol.MethodRunOpen, params)
		assert.Equal(t, params.RunID, manifest.RunID)
	}

	state, runIDs, _ := service.HeartbeatSnapshotRuns()
	assert.Equal(t, protocol.RunnerStateRunning, state)
	assert.Equal(t, []string{"run-1", "run-2"}, runIDs)
	service.Attach(&recordingPeer{})
	_, uiRunID, capabilities, err := service.uiTarget(context.WithValue(t.Context(), runnerRunIDContextKey{}, "run-2"))
	require.NoError(t, err)
	assert.Equal(t, "run-2", uiRunID)
	assert.False(t, capabilities.InteractiveUI)
	assert.True(t, capabilities.PersistentSurfaces)
	manifest, err := service.ProbeManifest(t.Context(), "review")
	require.NoError(t, err)
	assert.NotEmpty(t, manifest.Digest)

	callService[any](t, service, protocol.MethodRunClose, protocol.RunCloseParams{RunID: "run-1"})
	state, runIDs, _ = service.HeartbeatSnapshotRuns()
	assert.Equal(t, protocol.RunnerStateRunning, state)
	assert.Equal(t, []string{"run-2"}, runIDs)
	_, rpcErr := service.HandleRequest(t.Context(), protocol.MethodRunClose, mustJSON(t, protocol.RunCloseParams{RunID: "run-1"}))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeStale, rpcErr.Code)
	assert.Equal(t, protocol.ErrorReasonRunNotActive, rpcErr.Reason())
	state, runIDs, _ = service.HeartbeatSnapshotRuns()
	assert.Equal(t, protocol.RunnerStateRunning, state)
	assert.Equal(t, []string{"run-2"}, runIDs)
	callService[any](t, service, protocol.MethodRunClose, protocol.RunCloseParams{RunID: "run-2"})
}

func TestServiceCloseWaitsForIsolatedExtensionRuntimeRelease(t *testing.T) {
	runtime := extensions.EmptyRuntime()
	provider := &isolatedRuntimeProvider{
		runtime:        runtime,
		releaseStarted: make(chan struct{}),
		releaseGate:    make(chan struct{}),
	}
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(provider.releaseGate) }) })
	service, err := NewService(t.Context(), t.TempDir(), ServiceOptions{
		RuntimeProvider: provider,
		CleanupTimeout:  time.Second,
		ConfigLoader:    func(string) (llmtypes.Config, error) { return llmtypes.Config{}, nil },
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, service.Close())
		require.NoError(t, runtime.Close())
	})
	require.NoError(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner-1", Generation: 1}))
	callService[runnerpayload.Manifest](t, service, protocol.MethodRunOpen, protocol.RunOpenParams{RunID: "run-1", ConversationID: "conversation-1"})

	done := make(chan *protocol.RPCError, 1)
	go func() {
		_, rpcErr := service.HandleRequest(t.Context(), protocol.MethodRunClose, mustJSON(t, protocol.RunCloseParams{RunID: "run-1"}))
		done <- rpcErr
	}()
	select {
	case <-provider.releaseStarted:
	case <-time.After(time.Second):
		t.Fatal("run.close did not start isolated extension runtime release")
	}
	select {
	case <-done:
		t.Fatal("run.close completed before isolated extension runtime release")
	case <-time.After(30 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(provider.releaseGate) })
	select {
	case rpcErr := <-done:
		require.Nil(t, rpcErr)
	case <-time.After(time.Second):
		t.Fatal("run.close did not complete after isolated extension runtime release")
	}
}

func TestServiceBackgroundLeaseRetainsAndReattachesRunnerResources(t *testing.T) {
	runtime := extensions.EmptyRuntime()
	provider := &reusableIsolatedRuntimeProvider{runtime: runtime}
	instances := &recordingExecutionInstanceProvider{workspace: t.TempDir()}
	service, err := NewService(t.Context(), t.TempDir(), ServiceOptions{
		RuntimeProvider:           provider,
		ExecutionInstanceProvider: instances,
		ConfigLoader:              func(string) (llmtypes.Config, error) { return llmtypes.Config{}, nil },
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, service.Close())
		require.NoError(t, runtime.Close())
	})
	peer := &recordingPeer{}
	service.Attach(peer)
	require.NoError(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner-1", Generation: 1}))
	callService[runnerpayload.Manifest](t, service, protocol.MethodRunOpen, protocol.RunOpenParams{
		RunID: "run-1", ConversationID: "conversation-1",
		ClientCapabilities: protocol.ClientCapabilities{PersistentWidgets: true},
	})

	source := &recordingUIExtensionSource{owner: extensions.UIExtensionOwner{ExtensionID: "subagent", Generation: 7}}
	runOneCtx := service.decorateRunContext(t.Context(), "run-1", "conversation-1")
	lease, err := service.AcquireBackgroundTask(runOneCtx, source, extensions.BackgroundTaskAcquireRequest{Description: "subagent worker"})
	require.NoError(t, err)
	assert.NotEmpty(t, lease.LeaseID)

	callService[any](t, service, protocol.MethodRunClose, protocol.RunCloseParams{RunID: "run-1"})
	state, runIDs, _ := service.HeartbeatSnapshotRuns()
	assert.Equal(t, protocol.RunnerStateIdle, state)
	assert.Empty(t, runIDs)
	require.Len(t, instances.instances, 1)
	assert.False(t, instances.instances[0].closed)
	calls, releases := provider.counts()
	assert.Equal(t, 1, calls)
	assert.Zero(t, releases)
	service.Attach(nil)
	require.NoError(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner-1", Generation: 2}))
	service.Attach(peer)

	frame := extensions.UIFrame{Sequence: 1, Lines: []extensions.UIFrameLine{{Spans: []extensions.UIStyledSpan{{Text: "still running"}}}}}
	response, err := service.SetWidget(runOneCtx, source, extensions.UIWidgetSetRequest{
		ScopeID: "conversation-1", ID: "background-agents", Placement: extensions.UIWidgetPlacementAboveComposer, Frame: frame,
	})
	require.NoError(t, err)
	assert.True(t, response.Accepted)

	manifest := callService[runnerpayload.Manifest](t, service, protocol.MethodRunOpen, protocol.RunOpenParams{
		RunID: "run-2", ConversationID: "conversation-1",
		ClientCapabilities: protocol.ClientCapabilities{PersistentWidgets: true},
	})
	assert.Equal(t, "run-2", manifest.RunID)
	require.Len(t, instances.instances, 1)
	calls, releases = provider.counts()
	assert.Equal(t, 1, calls)
	assert.Zero(t, releases)

	frame.Sequence++
	_, err = service.UpdateWidget(runOneCtx, source, extensions.UIWidgetFrameRequest{
		ScopeID: "conversation-1", ID: "background-agents", Frame: frame,
	})
	require.NoError(t, err)
	peer.mu.Lock()
	lastParams := peer.callParams[len(peer.callParams)-1].(runnerpayload.UIWidgetFrameParams)
	peer.mu.Unlock()
	assert.Equal(t, "run-2", lastParams.RunID)

	callService[any](t, service, protocol.MethodRunClose, protocol.RunCloseParams{RunID: "run-2"})
	assert.False(t, instances.instances[0].closed)
	service.mu.Lock()
	resources := service.backgrounds["conversation-1"]
	service.mu.Unlock()
	require.NotNil(t, resources)
	released, err := service.ReleaseBackgroundTask(runOneCtx, source, extensions.BackgroundTaskReleaseRequest(lease))
	require.NoError(t, err)
	assert.True(t, released.Released)
	require.NotNil(t, released.AfterResponse)
	released.AfterResponse()
	select {
	case <-resources.cleanupDone:
	case <-time.After(time.Second):
		t.Fatal("background resources were not released")
	}
	assert.True(t, instances.instances[0].closed)
	_, releasedCount := provider.counts()
	assert.Equal(t, 1, releasedCount)
}

func TestServiceBackgroundReattachValidatesRequestedCWD(t *testing.T) {
	workspace := t.TempDir()
	nested := filepath.Join(workspace, "nested")
	require.NoError(t, os.Mkdir(nested, 0o755))
	other := t.TempDir()
	runtime := extensions.EmptyRuntime()
	provider := &reusableIsolatedRuntimeProvider{runtime: runtime}
	service, err := NewService(t.Context(), workspace, ServiceOptions{
		RuntimeProvider: provider,
		ConfigLoader:    func(string) (llmtypes.Config, error) { return llmtypes.Config{}, nil },
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, service.Close())
		require.NoError(t, runtime.Close())
	})
	service.Attach(&recordingPeer{})
	require.NoError(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner-1", Generation: 1}))
	callService[runnerpayload.Manifest](t, service, protocol.MethodRunOpen, protocol.RunOpenParams{
		RunID:          "run-1",
		ConversationID: "conversation-1",
		CWD:            "nested",
		ExpectedCWD:    nested,
	})

	source := &recordingUIExtensionSource{owner: extensions.UIExtensionOwner{ExtensionID: "subagent", Generation: 7}}
	runOneCtx := service.decorateRunContext(t.Context(), "run-1", "conversation-1")
	lease, err := service.AcquireBackgroundTask(runOneCtx, source, extensions.BackgroundTaskAcquireRequest{Description: "subagent worker"})
	require.NoError(t, err)
	callService[any](t, service, protocol.MethodRunClose, protocol.RunCloseParams{RunID: "run-1"})

	manifest := callService[runnerpayload.Manifest](t, service, protocol.MethodRunOpen, protocol.RunOpenParams{
		RunID:          "run-2",
		ConversationID: "conversation-1",
		CWD:            "nested",
		ExpectedCWD:    nested,
	})
	assert.Equal(t, nested, manifest.WorkingDirectory)
	callService[any](t, service, protocol.MethodRunClose, protocol.RunCloseParams{RunID: "run-2"})

	_, rpcErr := service.HandleRequest(t.Context(), protocol.MethodRunOpen, mustJSON(t, protocol.RunOpenParams{
		RunID:          "run-3",
		ConversationID: "conversation-1",
		CWD:            other,
		ExpectedCWD:    nested,
	}))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeConflict, rpcErr.Code)
	assert.Contains(t, rpcErr.Message, "conversation is bound to working directory")
	service.mu.Lock()
	resources := service.backgrounds["conversation-1"]
	service.mu.Unlock()
	require.NotNil(t, resources)
	assert.Empty(t, resources.attachedRunID)

	released, err := service.ReleaseBackgroundTask(runOneCtx, source, extensions.BackgroundTaskReleaseRequest(lease))
	require.NoError(t, err)
	require.NotNil(t, released.AfterResponse)
	released.AfterResponse()
	select {
	case <-resources.cleanupDone:
	case <-time.After(time.Second):
		t.Fatal("background resources were not released")
	}
}

func TestServiceCloseReleasesOutstandingBackgroundResources(t *testing.T) {
	runtime := extensions.EmptyRuntime()
	provider := &reusableIsolatedRuntimeProvider{runtime: runtime}
	instances := &recordingExecutionInstanceProvider{workspace: t.TempDir()}
	service, err := NewService(t.Context(), t.TempDir(), ServiceOptions{
		RuntimeProvider:           provider,
		ExecutionInstanceProvider: instances,
		ConfigLoader:              func(string) (llmtypes.Config, error) { return llmtypes.Config{}, nil },
	})
	require.NoError(t, err)
	require.NoError(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner-1", Generation: 1}))
	callService[runnerpayload.Manifest](t, service, protocol.MethodRunOpen, protocol.RunOpenParams{RunID: "run-1", ConversationID: "conversation-1"})
	source := &recordingUIExtensionSource{owner: extensions.UIExtensionOwner{ExtensionID: "subagent", Generation: 3}}
	_, err = service.AcquireBackgroundTask(service.decorateRunContext(t.Context(), "run-1", "conversation-1"), source, extensions.BackgroundTaskAcquireRequest{})
	require.NoError(t, err)
	callService[any](t, service, protocol.MethodRunClose, protocol.RunCloseParams{RunID: "run-1"})

	require.NoError(t, service.Close())
	require.Len(t, instances.instances, 1)
	assert.True(t, instances.instances[0].closed)
	_, releases := provider.counts()
	assert.Equal(t, 1, releases)
	require.NoError(t, runtime.Close())
}

func TestManifestHelpersLoadSystemPromptAndDefensivelyCloneSchemas(t *testing.T) {
	workspace := t.TempDir()
	promptPath := filepath.Join(workspace, "prompt.md")
	require.NoError(t, os.WriteFile(promptPath, []byte("runner prompt"), 0o600))

	path, content, err := loadSystemPrompt(llmtypes.Config{Sysprompt: "prompt.md"}, workspace)
	require.NoError(t, err)
	assert.Equal(t, promptPath, path)
	assert.Equal(t, "runner prompt", content)

	path, content, err = loadSystemPrompt(llmtypes.Config{}, workspace)
	require.NoError(t, err)
	assert.Empty(t, path)
	assert.Empty(t, content)

	_, _, err = loadSystemPrompt(llmtypes.Config{Sysprompt: "bad\x00path"}, workspace)
	require.ErrorContains(t, err, "null byte")
	_, _, err = loadSystemPrompt(llmtypes.Config{Sysprompt: "~another-user/prompt"}, workspace)
	require.ErrorContains(t, err, "unsupported custom system prompt path")
	_, _, err = loadSystemPrompt(llmtypes.Config{Sysprompt: "missing.md"}, workspace)
	require.ErrorContains(t, err, "failed to stat custom system prompt")
	_, _, err = loadSystemPrompt(llmtypes.Config{Sysprompt: "."}, workspace)
	require.ErrorContains(t, err, "must be a regular file")

	invalidUTF8Path := filepath.Join(workspace, "invalid.md")
	require.NoError(t, os.WriteFile(invalidUTF8Path, []byte{0xff, 0xfe}, 0o600))
	_, _, err = loadSystemPrompt(llmtypes.Config{Sysprompt: invalidUTF8Path}, workspace)
	require.ErrorContains(t, err, "not valid UTF-8")

	schema := map[string]any{
		"properties": map[string]any{
			"paths": []any{map[string]any{"type": "string"}, "literal"},
		},
	}
	cloned := cloneJSONMap(schema)
	clonedProperties := cloned["properties"].(map[string]any)
	clonedPaths := clonedProperties["paths"].([]any)
	clonedPaths[0].(map[string]any)["type"] = "integer"
	originalPaths := schema["properties"].(map[string]any)["paths"].([]any)
	assert.Equal(t, "string", originalPaths[0].(map[string]any)["type"])
	assert.Nil(t, cloneJSONMap(nil))
	assert.Nil(t, runtimeToolByName(nil, "missing"))
	assert.Nil(t, runtimeToolByName(extensions.EmptyRuntime(), "missing"))
}

func TestBuildWireManifestSortsContentAndRejectsReservedToolCollisions(t *testing.T) {
	workspace := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(workspace, ".git"), 0o700))
	manifest, err := buildWireManifest(agentenv.Manifest{
		WorkingDirectory: workspace,
		Contexts: map[string]string{
			"z/AGENTS.md": "z rules",
			"a/AGENTS.md": "a rules",
		},
		Tools: []agentenv.ToolDefinition{
			{Name: "z_tool", Description: "z", InputSchema: map[string]any{"type": "object"}, Placement: agentenv.ToolPlacementEnvironment},
			{Name: "control", Placement: agentenv.ToolPlacementControlPlane},
			{Name: "a_tool", Description: "a", InputSchema: map[string]any{"type": "object"}, Placement: agentenv.ToolPlacementEnvironment},
		},
	}, llmtypes.Config{
		AllowedCommands:     []string{"go test ./..."},
		EnableFSSearchTools: true,
		SyspromptArgs:       map[string]string{"audience": "developer"},
	}, nil, "runner-1", "run-1", 4, []string{"get_goal", " "})
	require.NoError(t, err)
	require.Len(t, manifest.ContextFiles, 2)
	assert.Equal(t, "a/AGENTS.md", manifest.ContextFiles[0].Path)
	assert.Equal(t, "z/AGENTS.md", manifest.ContextFiles[1].Path)
	require.Len(t, manifest.Tools, 2)
	assert.Equal(t, "a_tool", manifest.Tools[0].Name)
	assert.Equal(t, "z_tool", manifest.Tools[1].Name)
	assert.NotEmpty(t, manifest.ContextFiles[0].Digest)
	assert.NotEmpty(t, manifest.Digest)
	assert.True(t, manifest.Config.EnableFSSearchTools)
	assert.Equal(t, map[string]string{"audience": "developer"}, manifest.Config.SystemPromptArgs)
	require.NotNil(t, manifest.Config.SystemInformation)
	assert.True(t, manifest.Config.SystemInformation.IsGitRepo)
	assert.Equal(t, runtime.GOOS, manifest.Config.SystemInformation.Platform)
	assert.NotEmpty(t, manifest.Config.SystemInformation.OSVersion)
	assert.Regexp(t, `^\d{4}-\d{2}-\d{2}$`, manifest.Config.SystemInformation.Date)

	manifest.Tools[0].InputSchema["type"] = "changed"
	assert.Equal(t, "object", manifest.Tools[1].InputSchema["type"])

	_, err = buildWireManifest(agentenv.Manifest{
		WorkingDirectory: workspace,
		Tools: []agentenv.ToolDefinition{{
			Name:      "get_goal",
			Placement: agentenv.ToolPlacementEnvironment,
		}},
	}, llmtypes.Config{}, nil, "runner-1", "run-1", 1, []string{"get_goal"})
	require.ErrorContains(t, err, "collides with a reserved control-plane tool")
}

func TestServiceOpenRunWaitsForManifestSnapshotRefresh(t *testing.T) {
	workspace := t.TempDir()
	runtime := extensions.EmptyRuntime()
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	service, err := NewService(t.Context(), workspace, ServiceOptions{
		RuntimeProvider:     staticRuntimeProvider{runtime: runtime},
		SnapshotWaitTimeout: time.Second,
		ConfigLoader:        func(string) (llmtypes.Config, error) { return llmtypes.Config{}, nil },
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	service.Attach(&recordingPeer{})
	require.NoError(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner-1", Generation: 1}))

	require.NoError(t, service.snapshotGate.Acquire(t.Context(), 1))
	type response struct {
		result any
		err    *protocol.RPCError
	}
	done := make(chan response, 1)
	go func() {
		result, rpcErr := service.HandleRequest(t.Context(), protocol.MethodRunOpen, mustJSON(t, protocol.RunOpenParams{
			RunID: "run-1", ConversationID: "conversation-1",
		}))
		done <- response{result: result, err: rpcErr}
	}()
	select {
	case <-done:
		service.snapshotGate.Release(1)
		t.Fatal("run.open failed instead of waiting for the in-flight manifest refresh")
	case <-time.After(30 * time.Millisecond):
	}
	state, runIDs, _ := service.HeartbeatSnapshotRuns()
	assert.Equal(t, protocol.RunnerStateRunning, state)
	assert.Equal(t, []string{"run-1"}, runIDs)
	service.snapshotGate.Release(1)

	select {
	case response := <-done:
		require.Nil(t, response.err)
		assert.Equal(t, "run-1", response.result.(runnerpayload.Manifest).RunID)
	case <-time.After(time.Second):
		t.Fatal("run.open did not continue after the manifest refresh completed")
	}
	callService[any](t, service, protocol.MethodRunClose, protocol.RunCloseParams{RunID: "run-1"})
}

func TestServiceManifestDigestProbeUsesCachedDigestWhileSnapshotBusy(t *testing.T) {
	workspace := t.TempDir()
	runtime := extensions.EmptyRuntime()
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	service, err := NewService(t.Context(), workspace, ServiceOptions{
		RuntimeProvider: staticRuntimeProvider{runtime: runtime},
		ConfigLoader:    func(string) (llmtypes.Config, error) { return llmtypes.Config{}, nil },
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	service.Attach(&recordingPeer{})
	require.NoError(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner-1", Generation: 1}))
	manifest := callService[runnerpayload.Manifest](t, service, protocol.MethodRunOpen, protocol.RunOpenParams{
		RunID: "run-1", ConversationID: "conversation-1",
	})
	require.NoError(t, service.snapshotGate.Acquire(t.Context(), 1))
	defer service.snapshotGate.Release(1)

	probeCtx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	digest, err := service.ProbeManifestDigest(probeCtx)
	require.NoError(t, err)
	assert.Equal(t, manifest.Digest, digest)
	require.NoError(t, service.closeRun(t.Context(), "run-1"))
}

func TestServicePinsExtensionRuntimeForRunLifetime(t *testing.T) {
	workspace := t.TempDir()
	runtime := extensions.EmptyRuntime()
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	provider := &leaseRecordingRuntimeProvider{runtime: runtime}
	service, err := NewService(t.Context(), workspace, ServiceOptions{
		RuntimeProvider: provider,
		ConfigLoader:    func(string) (llmtypes.Config, error) { return llmtypes.Config{}, nil },
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	service.Attach(&recordingPeer{})
	require.NoError(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner-1", Generation: 1}))

	callService[runnerpayload.Manifest](t, service, protocol.MethodRunOpen, protocol.RunOpenParams{
		RunID: "run-1", ConversationID: "conversation-1",
	})
	require.Eventually(t, func() bool { return provider.operationCtx.Err() != nil }, time.Second, time.Millisecond)
	assert.NoError(t, provider.leaseCtx.Err())

	callService[any](t, service, protocol.MethodRunClose, protocol.RunCloseParams{RunID: "run-1"})
	require.Eventually(t, func() bool { return provider.leaseCtx.Err() != nil }, time.Second, time.Millisecond)
}

func TestServiceManifestProbeDoesNotStartRunLifecycle(t *testing.T) {
	workspace := t.TempDir()
	runtime := extensions.EmptyRuntime()
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	provider := &recordingRuntimeProvider{runtime: runtime}
	var loadedProfiles []string
	var commandEnvironment *recordingCommandEnvironment
	service, err := NewService(t.Context(), workspace, ServiceOptions{
		RuntimeProvider: provider,
		EnvironmentFactory: func(workingDirectory string, runtime *extensions.Runtime) agentenv.Environment {
			commandEnvironment = &recordingCommandEnvironment{Environment: agentenv.NewLocalEnvironment(workingDirectory, runtime)}
			return commandEnvironment
		},
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

	callService[runnerpayload.Manifest](t, service, protocol.MethodRunOpen, protocol.RunOpenParams{
		RunID: "run-1", ConversationID: "conversation-1",
		Agent: protocol.AgentDescriptor{Provider: "anthropic", Model: "claude-test", EnvironmentProfile: "runner-work", InvokedBy: "subagent"},
	})
	assert.Equal(t, 1, provider.activeCalls)
	assert.Equal(t, []string{"", "runner-work"}, loadedProfiles)
	assert.Equal(t, "runner-work", provider.activeVariant)
	assert.False(t, provider.activeConfig.Enabled)
	assert.Equal(t, "conversation-1", provider.activeCallContext.ConversationID)
	assert.Equal(t, "anthropic", provider.activeCallContext.Provider)
	assert.Equal(t, "claude-test", provider.activeCallContext.Model)
	assert.Equal(t, "subagent", provider.activeCallContext.InvokedBy)
	callService[runnerpayload.CommandExecuteResult](t, service, protocol.MethodCommandExecute, runnerpayload.CommandExecuteParams{RunID: "run-1", Message: "hello"})
	require.NotNil(t, commandEnvironment)
	assert.Equal(t, "subagent", commandEnvironment.request.RunSpec.InvokedBy)
	callService[any](t, service, protocol.MethodRunClose, protocol.RunCloseParams{RunID: "run-1"})
}

func TestServiceCloseRunDoesNotPoisonRunnerAfterCanceledOperationTimeout(t *testing.T) {
	workspace := t.TempDir()
	runtime := extensions.EmptyRuntime()
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	blocking := &blockingToolEnvironment{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	environmentCalls := 0
	service, err := NewService(t.Context(), workspace, ServiceOptions{
		RuntimeProvider: staticRuntimeProvider{runtime: runtime},
		EnvironmentFactory: func(workingDirectory string, runtime *extensions.Runtime) agentenv.Environment {
			environmentCalls++
			if environmentCalls == 1 {
				blocking.Environment = agentenv.NewLocalEnvironment(workingDirectory, runtime)
				return blocking
			}
			return agentenv.NewLocalEnvironment(workingDirectory, runtime)
		},
		CleanupTimeout: 20 * time.Millisecond,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	service.Attach(&recordingPeer{})
	require.NoError(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner-1", Generation: 1}))
	callService[runnerpayload.Manifest](t, service, protocol.MethodRunOpen, protocol.RunOpenParams{
		RunID: "run-1", ConversationID: "conversation-1",
	})

	operationDone := make(chan error, 1)
	go func() {
		_, operationErr := service.executeTool(context.Background(), runnerpayload.ToolExecuteParams{
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
	state, activeRunIDs, _ := service.HeartbeatSnapshotRuns()
	assert.Equal(t, protocol.RunnerStateIdle, state)
	assert.Empty(t, activeRunIDs)

	_, rpcErr := service.HandleRequest(t.Context(), protocol.MethodRunOpen, mustJSON(t, protocol.RunOpenParams{
		RunID: "run-same-conversation", ConversationID: "conversation-1",
	}))
	require.Nil(t, rpcErr)
	state, activeRunIDs, _ = service.HeartbeatSnapshotRuns()
	assert.Equal(t, protocol.RunnerStateRunning, state)
	assert.Equal(t, []string{"run-same-conversation"}, activeRunIDs)

	close(blocking.release)
	require.NoError(t, <-operationDone)
	require.NoError(t, service.closeRun(t.Context(), "run-same-conversation"))
}

func TestServiceRunCleanupFailureReleasesAffectedRun(t *testing.T) {
	provider := &recordingExecutionInstanceProvider{workspace: t.TempDir(), closeErr: errors.New("instance close failed")}
	runtime := extensions.EmptyRuntime()
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	service, err := NewService(t.Context(), t.TempDir(), ServiceOptions{
		RuntimeProvider:           staticRuntimeProvider{runtime: runtime},
		ConfigLoader:              func(string) (llmtypes.Config, error) { return llmtypes.Config{}, nil },
		ExecutionInstanceProvider: provider,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = service.Close() })
	service.Attach(&recordingPeer{})
	require.NoError(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner-1", Generation: 1}))
	callService[runnerpayload.Manifest](t, service, protocol.MethodRunOpen, protocol.RunOpenParams{
		RunID: "run-1", ConversationID: "conversation-1",
	})

	require.ErrorContains(t, service.closeRun(t.Context(), "run-1"), "instance close failed")
	state, activeRunIDs, _ := service.HeartbeatSnapshotRuns()
	assert.Equal(t, protocol.RunnerStateIdle, state)
	assert.Empty(t, activeRunIDs)
	provider.closeErr = nil
	_, rpcErr := service.HandleRequest(t.Context(), protocol.MethodRunOpen, mustJSON(t, protocol.RunOpenParams{
		RunID: "run-same-conversation", ConversationID: "conversation-1",
	}))
	require.Nil(t, rpcErr)
	state, activeRunIDs, _ = service.HeartbeatSnapshotRuns()
	assert.Equal(t, protocol.RunnerStateRunning, state)
	assert.Equal(t, []string{"run-same-conversation"}, activeRunIDs)
	require.NoError(t, service.closeRun(t.Context(), "run-same-conversation"))
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
	callService[runnerpayload.Manifest](t, service, protocol.MethodRunOpen, protocol.RunOpenParams{
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
	callService[runnerpayload.Manifest](t, service, protocol.MethodRunOpen, protocol.RunOpenParams{
		RunID: "run-1", ConversationID: "conversation-1",
		ClientCapabilities: protocol.ClientCapabilities{InteractiveUI: true, PersistentWidgets: true, PersistentSurfaces: true},
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
	inputParams := peer.callParams[0].(runnerpayload.UIInputParams)
	confirmParams := peer.callParams[1].(runnerpayload.UIConfirmParams)
	selectParams := peer.callParams[2].(runnerpayload.UISelectParams)
	widgetParams := peer.callParams[4].(runnerpayload.UIWidgetSetParams)
	peer.mu.Unlock()
	assert.Equal(t, runnerpayload.ExtensionOwner{ExtensionID: "extension-1", Generation: 4}, inputParams.Owner)
	assert.Equal(t, scopedInteractiveUIRequestID(inputParams.Owner, "shared-id"), inputParams.Request.ID)
	assert.NotEqual(t, scopedInteractiveUIRequestID(runnerpayload.ExtensionOwner{ExtensionID: "extension-2", Generation: 4}, "shared-id"), inputParams.Request.ID)
	assert.NotEmpty(t, confirmParams.Request.ID)
	assert.NotEmpty(t, selectParams.Request.ID)
	assert.NotEqual(t, confirmParams.Request.ID, selectParams.Request.ID)
	assert.Equal(t, "run-1", widgetParams.RunID)
	assert.Equal(t, runnerpayload.ExtensionOwner{ExtensionID: "extension-1", Generation: 4}, widgetParams.Owner)

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
	callService[runnerpayload.Manifest](t, service, protocol.MethodRunOpen, protocol.RunOpenParams{
		RunID: "run-1", ConversationID: "conversation-1",
		ClientCapabilities: protocol.ClientCapabilities{InteractiveUI: true},
	})

	_, err = service.SetWidget(t.Context(), nil, extensions.UIWidgetSetRequest{})
	require.ErrorContains(t, err, "source is required")
	source := &recordingUIExtensionSource{owner: extensions.UIExtensionOwner{ExtensionID: "extension-1", Generation: 1}}
	frameResponse, err := service.SetWidget(t.Context(), source, extensions.UIWidgetSetRequest{ID: "widget", Frame: extensions.UIFrame{Sequence: 1}})
	require.NoError(t, err)
	assert.False(t, frameResponse.Accepted)
	assert.Contains(t, frameResponse.Reason, "not available")
	frameResponse, err = service.OpenSurface(t.Context(), source, extensions.UISurfaceOpenRequest{ID: "surface"})
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

	manifest := callService[runnerpayload.Manifest](t, service, protocol.MethodRunOpen, protocol.RunOpenParams{
		RunID: "run-1", ConversationID: "conversation-1", CWD: "/requested/workspace",
	})

	assert.Equal(t, instanceWorkspace, environmentWorkspace)
	assert.Equal(t, instanceWorkspace, manifest.WorkingDirectory)
	require.Len(t, provider.specs, 1)
	assert.Equal(t, ExecutionInstanceSpec{RunID: "run-1", ConversationID: "conversation-1", CWD: instanceWorkspace}, provider.specs[0])
	require.Len(t, provider.instances, 1)
	assert.False(t, provider.instances[0].closed)
	callService[any](t, service, protocol.MethodRunClose, protocol.RunCloseParams{RunID: "run-1"})
	assert.True(t, provider.instances[0].closed)
}

func TestServiceRejectsExecutionInstanceWorkingDirectoryMismatch(t *testing.T) {
	resolvedWorkspace := t.TempDir()
	instanceWorkspace := t.TempDir()
	provider := &recordingExecutionInstanceProvider{
		resolvedWorkspace: resolvedWorkspace,
		workspace:         instanceWorkspace,
	}
	service, err := NewService(t.Context(), t.TempDir(), ServiceOptions{
		ExecutionInstanceProvider: provider,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	service.Attach(&recordingPeer{})
	require.NoError(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner-1", Generation: 1}))

	_, rpcErr := service.HandleRequest(t.Context(), protocol.MethodRunOpen, mustJSON(t, protocol.RunOpenParams{
		RunID: "run-1", ConversationID: "conversation-1", CWD: "/requested/workspace",
	}))

	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeInternal, rpcErr.Code)
	assert.Contains(t, rpcErr.Message, "returned working directory")
	assert.Contains(t, rpcErr.Message, resolvedWorkspace)
	require.Len(t, provider.instances, 1)
	assert.True(t, provider.instances[0].closed)
	state, runIDs, _ := service.HeartbeatSnapshotRuns()
	assert.Equal(t, protocol.RunnerStateIdle, state)
	assert.Empty(t, runIDs)
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

func TestServiceCloseWhileRunOpeningClosesResourcesOnce(t *testing.T) {
	provider := &recordingExecutionInstanceProvider{workspace: t.TempDir()}
	environment := &blockingOpenEnvironment{started: make(chan struct{})}
	runtime := extensions.EmptyRuntime()
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	service, err := NewService(t.Context(), t.TempDir(), ServiceOptions{
		RuntimeProvider:           staticRuntimeProvider{runtime: runtime},
		ConfigLoader:              func(string) (llmtypes.Config, error) { return llmtypes.Config{}, nil },
		EnvironmentFactory:        func(string, *extensions.Runtime) agentenv.Environment { return environment },
		ExecutionInstanceProvider: provider,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	service.Attach(&recordingPeer{})
	require.NoError(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner-1", Generation: 1}))

	openDone := make(chan *protocol.RPCError, 1)
	go func() {
		_, rpcErr := service.HandleRequest(t.Context(), protocol.MethodRunOpen, mustJSON(t, protocol.RunOpenParams{
			RunID: "run-1", ConversationID: "conversation-1",
		}))
		openDone <- rpcErr
	}()
	select {
	case <-environment.started:
	case <-time.After(time.Second):
		t.Fatal("runner environment did not start opening")
	}

	closeDone := make(chan *protocol.RPCError, 1)
	go func() {
		_, rpcErr := service.HandleRequest(t.Context(), protocol.MethodRunClose, mustJSON(t, protocol.RunCloseParams{RunID: "run-1"}))
		closeDone <- rpcErr
	}()
	select {
	case rpcErr := <-openDone:
		require.NotNil(t, rpcErr)
		assert.Contains(t, rpcErr.Message, "context canceled")
	case <-time.After(time.Second):
		t.Fatal("opening runner run did not stop")
	}
	select {
	case rpcErr := <-closeDone:
		require.Nil(t, rpcErr)
	case <-time.After(time.Second):
		t.Fatal("runner run close did not finish")
	}

	assert.Equal(t, int32(1), environment.closeCalls.Load())
	require.Len(t, provider.instances, 1)
	assert.True(t, provider.instances[0].closed)
}

func TestServiceFailedOpenCleanupDoesNotPoisonRunner(t *testing.T) {
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
	assert.Equal(t, protocol.RunnerStateIdle, state)
	assert.Empty(t, activeRunID)
	_, rpcErr = service.HandleRequest(t.Context(), protocol.MethodRunOpen, mustJSON(t, protocol.RunOpenParams{
		RunID: "run-2", ConversationID: "conversation-2",
	}))
	require.NotNil(t, rpcErr)
	assert.Contains(t, rpcErr.Message, "factory returned nil")
	assert.NotContains(t, rpcErr.Message, "requires restart")
}

func TestServiceManifestProbeCleanupFailureDoesNotPoisonRunner(t *testing.T) {
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
	assert.Equal(t, protocol.RunnerStateIdle, state)
	assert.Empty(t, activeRunID)
	assert.Empty(t, digest)
	provider.closeErr = nil
	_, rpcErr := service.HandleRequest(t.Context(), protocol.MethodRunOpen, mustJSON(t, protocol.RunOpenParams{
		RunID: "run-1", ConversationID: "conversation-1",
	}))
	require.Nil(t, rpcErr)
	require.NoError(t, service.closeRun(t.Context(), "run-1"))
}

func TestDirectWorkspaceInstanceProviderReturnsFreshHandles(t *testing.T) {
	workspace := t.TempDir()
	relativeWorkspace := filepath.Join(workspace, "nested")
	require.NoError(t, os.Mkdir(relativeWorkspace, 0o755))
	outsideWorkspace := t.TempDir()
	homeWorkspace := t.TempDir()
	t.Setenv("HOME", homeWorkspace)
	provider, err := NewDirectWorkspaceInstanceProvider(workspace)
	require.NoError(t, err)

	first, err := provider.Create(t.Context(), ExecutionInstanceSpec{RunID: "run-1"})
	require.NoError(t, err)
	second, err := provider.Create(t.Context(), ExecutionInstanceSpec{RunID: "run-2", CWD: "nested"})
	require.NoError(t, err)
	outside, err := provider.Create(t.Context(), ExecutionInstanceSpec{RunID: "run-3", CWD: outsideWorkspace})
	require.NoError(t, err)
	home, err := provider.Create(t.Context(), ExecutionInstanceSpec{RunID: "run-4", CWD: "~"})
	require.NoError(t, err)

	assert.NotSame(t, first, second)
	assert.Equal(t, workspace, first.WorkingDirectory())
	assert.Equal(t, relativeWorkspace, second.WorkingDirectory())
	assert.Equal(t, outsideWorkspace, outside.WorkingDirectory())
	assert.Equal(t, homeWorkspace, home.WorkingDirectory())
	require.NoError(t, first.Close(t.Context()))
	require.NoError(t, second.Close(t.Context()))
	require.NoError(t, outside.Close(t.Context()))
	require.NoError(t, home.Close(t.Context()))

	_, err = provider.Create(t.Context(), ExecutionInstanceSpec{RunID: "run-missing", CWD: filepath.Join(workspace, "missing")})
	require.ErrorContains(t, err, "cwd directory does not exist")
	assert.ErrorIs(t, err, ErrInvalidWorkingDirectory)
	_, err = provider.Create(t.Context(), ExecutionInstanceSpec{RunID: "run-other-home", CWD: "~another-user/project"})
	require.ErrorContains(t, err, "supports only ~ or ~/")
	assert.ErrorIs(t, err, ErrInvalidWorkingDirectory)
}

func TestServiceClassifiesInvalidRequestedCWD(t *testing.T) {
	workspace := t.TempDir()
	service, err := NewService(t.Context(), workspace, ServiceOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	service.Attach(&recordingPeer{})
	require.NoError(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner-1", Generation: 1}))

	_, rpcErr := service.HandleRequest(t.Context(), protocol.MethodRunOpen, mustJSON(t, protocol.RunOpenParams{
		RunID:          "run-1",
		ConversationID: "conversation-1",
		CWD:            filepath.Join(workspace, "missing"),
	}))

	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeInvalidParams, rpcErr.Code)
	assert.Contains(t, rpcErr.Message, "cwd directory does not exist")
	state, runIDs, _ := service.HeartbeatSnapshotRuns()
	assert.Equal(t, protocol.RunnerStateIdle, state)
	assert.Empty(t, runIDs)
}

func TestServiceClassifiesProviderInvalidWorkingDirectory(t *testing.T) {
	provider := &recordingExecutionInstanceProvider{
		resolveErr: errors.Join(errors.New("custom provider rejected cwd"), ErrInvalidWorkingDirectory),
	}
	service, err := NewService(t.Context(), t.TempDir(), ServiceOptions{
		ExecutionInstanceProvider: provider,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	service.Attach(&recordingPeer{})
	require.NoError(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner-1", Generation: 1}))

	_, rpcErr := service.HandleRequest(t.Context(), protocol.MethodRunOpen, mustJSON(t, protocol.RunOpenParams{
		RunID:          "run-1",
		ConversationID: "conversation-1",
		CWD:            "/provider-specific/path",
	}))

	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeInvalidParams, rpcErr.Code)
	assert.Contains(t, rpcErr.Message, "custom provider rejected cwd")
	assert.Empty(t, provider.instances)
}

func TestServiceResolvesRequestedCWDBeforeCheckingConversationBinding(t *testing.T) {
	workspace := t.TempDir()
	nested := filepath.Join(workspace, "nested")
	require.NoError(t, os.Mkdir(nested, 0o755))
	runtime := extensions.EmptyRuntime()
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	service, err := NewService(t.Context(), workspace, ServiceOptions{
		RuntimeProvider: staticRuntimeProvider{runtime: runtime},
		ConfigLoader:    func(string) (llmtypes.Config, error) { return llmtypes.Config{}, nil },
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	service.Attach(&recordingPeer{})
	require.NoError(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner-1", Generation: 1}))

	manifest := callService[runnerpayload.Manifest](t, service, protocol.MethodRunOpen, protocol.RunOpenParams{
		RunID:          "run-1",
		ConversationID: "conversation-1",
		CWD:            "nested",
		ExpectedCWD:    nested,
	})
	assert.Equal(t, nested, manifest.WorkingDirectory)
	callService[any](t, service, protocol.MethodRunClose, protocol.RunCloseParams{RunID: "run-1"})
}

func TestServiceRejectsChangedConversationCWD(t *testing.T) {
	workspace := t.TempDir()
	other := t.TempDir()
	service, err := NewService(t.Context(), workspace, ServiceOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	service.Attach(&recordingPeer{})
	require.NoError(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner-1", Generation: 1}))

	_, rpcErr := service.HandleRequest(t.Context(), protocol.MethodRunOpen, mustJSON(t, protocol.RunOpenParams{
		RunID:          "run-1",
		ConversationID: "conversation-1",
		CWD:            other,
		ExpectedCWD:    workspace,
	}))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeConflict, rpcErr.Code)
	assert.Contains(t, rpcErr.Message, "conversation is bound to working directory")
	state, runIDs, _ := service.HeartbeatSnapshotRuns()
	assert.Equal(t, protocol.RunnerStateIdle, state)
	assert.Empty(t, runIDs)
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
	callService[runnerpayload.Manifest](t, service, protocol.MethodRunOpen, protocol.RunOpenParams{
		RunID: "run-1", ConversationID: "conversation-1",
	})

	done := make(chan *protocol.RPCError, 1)
	toolParams := mustJSON(t, runnerpayload.ToolExecuteParams{
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
	callService[runnerpayload.Manifest](t, service, protocol.MethodRunOpen, protocol.RunOpenParams{RunID: "run-1", ConversationID: "conversation-1"})
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

func manifestToolNames(manifest runnerpayload.Manifest) []string {
	names := make([]string, 0, len(manifest.Tools))
	for _, definition := range manifest.Tools {
		names = append(names, definition.Name)
	}
	return names
}
