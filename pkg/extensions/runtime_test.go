package extensions

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/logger"
	kodelettools "github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuntimeInitializesExtensionAndExecutesRegisteredTool(t *testing.T) {
	rootDir := t.TempDir()
	extDir := filepath.Join(rootDir, "weather")
	writeExecutable(t, filepath.Join(extDir, "kodelet-extension-weather"), helperExtensionScript(t))
	basePath := t.TempDir()
	t.Setenv("KODELET_BASE_PATH", basePath)

	runtime, err := NewRuntime(
		context.Background(),
		WithConfig(DefaultConfig()),
		WithWorkingDir(rootDir),
		WithRoots(Root{Dir: rootDir, Kind: SourceKindLocalStandalone}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, runtime.Close()) })

	tools := runtime.Tools()
	require.Len(t, tools, 1)
	assert.Equal(t, "get_weather", tools[0].Name())
	assert.Equal(t, "get the current weather", tools[0].Description())
	assert.DirExists(t, filepath.Join(basePath, "extensions", "data", "weather"))

	ctx := kodelettools.ContextWithToolContext(context.Background(), kodelettools.ToolContext{
		ConversationID: "conv-123",
		WorkingDir:     rootDir,
		Provider:       "anthropic",
		Model:          "claude-test",
		Profile:        "default",
	})
	result := tools[0].Execute(ctx, nil, `{"location":"London"}`)

	assert.False(t, result.IsError())
	assert.Equal(t, "Weather for London from conv-123", result.GetResult())

	structured := result.StructuredData()
	assert.True(t, structured.Success)
	assert.Equal(t, "get_weather", structured.ToolName)
	var metadata tooltypes.ExtensionToolMetadata
	require.True(t, tooltypes.ExtractMetadata(structured.Metadata, &metadata))
	assert.Equal(t, "weather", metadata.ExtensionID)
	assert.Equal(t, "get_weather", metadata.ToolName)
	assert.Equal(t, "Weather for London from conv-123", metadata.Output)
	assert.Equal(t, "celsius", metadata.Data["unit"])
}

func TestRuntimePassesWorkingDirToExtensionProcessEnvironment(t *testing.T) {
	rootDir := t.TempDir()
	extDir := filepath.Join(rootDir, "env")
	writeExecutable(t, filepath.Join(extDir, "kodelet-extension-env"), helperEnvExtensionScript(t))
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	t.Setenv(workspaceCWDEnvKey, "/stale/workspace")

	runtime, err := NewRuntime(
		context.Background(),
		WithConfig(DefaultConfig()),
		WithWorkingDir(rootDir),
		WithRoots(Root{Dir: rootDir, Kind: SourceKindLocalStandalone}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, runtime.Close()) })

	tools := runtime.Tools()
	require.Len(t, tools, 1)
	assert.Equal(t, "workspace_cwd", tools[0].Name())

	result := tools[0].Execute(context.Background(), nil, `{}`)

	assert.False(t, result.IsError())
	assert.Equal(t, rootDir, result.GetResult())
}

func TestRuntimeExtensionToolCanRequestUIInput(t *testing.T) {
	rootDir := t.TempDir()
	extDir := filepath.Join(rootDir, "ask")
	writeExecutable(t, filepath.Join(extDir, "kodelet-extension-ask"), helperExtensionScript(t))
	t.Setenv("KODELET_BASE_PATH", t.TempDir())

	runtime, err := NewRuntime(
		context.Background(),
		WithConfig(DefaultConfig()),
		WithWorkingDir(rootDir),
		WithRoots(Root{Dir: rootDir, Kind: SourceKindLocalStandalone}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, runtime.Close()) })

	tools := runtime.Tools()
	require.Len(t, tools, 1)

	ctx := ContextWithUIInputBroker(context.Background(), staticUIInputBroker{value: "2"})
	result := tools[0].Execute(ctx, nil, `{"location":"AskUI"}`)

	assert.False(t, result.IsError())
	assert.Equal(t, "User answered 2", result.GetResult())
}

func TestRuntimeExecutesRegisteredShortcut(t *testing.T) {
	rootDir := t.TempDir()
	extDir := filepath.Join(rootDir, "shortcut")
	writeExecutable(t, filepath.Join(extDir, "kodelet-extension-shortcut"), helperExtensionScript(t))
	t.Setenv("KODELET_BASE_PATH", t.TempDir())

	runtime, err := NewRuntime(
		context.Background(),
		WithConfig(DefaultConfig()),
		WithWorkingDir(rootDir),
		WithRoots(Root{Dir: rootDir, Kind: SourceKindLocalStandalone}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, runtime.Close()) })

	assert.Contains(t, runtime.Shortcuts(), Shortcut{
		Key:         "ctrl+alt+r",
		Description: "Refresh project context",
		ExtensionID: "shortcut",
	})
	ctx := ContextWithExtensionUIScope(context.Background(), "conversation-scope")
	matched, result, err := runtime.ExecuteShortcutWithResult(ctx, "alt+control+r", ExtensionCallContext{
		ConversationID: "conv-shortcut",
		CWD:            rootDir,
		InvokedBy:      "main",
	})
	require.NoError(t, err)
	assert.True(t, matched)
	require.NotNil(t, result)
	assert.Equal(t, ShortcutActionSubmit, result.Action)
	assert.Equal(t, "/refresh", result.Message)

	matched, err = runtime.ExecuteShortcut(ctx, "ctrl+alt+r", ExtensionCallContext{
		ConversationID: "conv-shortcut",
	})
	assert.True(t, matched)
	assert.ErrorContains(t, err, "use ExecuteShortcutWithResult")

	matched, result, err = runtime.ExecuteShortcutWithResult(context.Background(), "ctrl+alt+x", ExtensionCallContext{})
	require.NoError(t, err)
	assert.False(t, matched)
	assert.Nil(t, result)
}

func TestRuntimeExecutesShortcutWithoutResult(t *testing.T) {
	rootDir := t.TempDir()
	extDir := filepath.Join(rootDir, "shortcut")
	writeExecutable(t, filepath.Join(extDir, "kodelet-extension-shortcut"), helperExtensionScript(t))
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	t.Setenv("KODELET_TEST_SHORTCUT_NULL_RESULT", "1")

	runtime, err := NewRuntime(
		context.Background(),
		WithConfig(DefaultConfig()),
		WithWorkingDir(rootDir),
		WithRoots(Root{Dir: rootDir, Kind: SourceKindLocalStandalone}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, runtime.Close()) })

	ctx := ContextWithExtensionUIScope(context.Background(), "conversation-scope")
	matched, result, err := runtime.ExecuteShortcutWithResult(ctx, "ctrl+alt+r", ExtensionCallContext{
		ConversationID: "conv-shortcut",
	})
	require.NoError(t, err)
	assert.True(t, matched)
	assert.Nil(t, result)

	matched, err = runtime.ExecuteShortcut(ctx, "ctrl+alt+r", ExtensionCallContext{
		ConversationID: "conv-shortcut",
	})
	require.NoError(t, err)
	assert.True(t, matched)
}

func TestRuntimeExtensionToolStreamsAccumulatedUpdates(t *testing.T) {
	rootDir := t.TempDir()
	extDir := filepath.Join(rootDir, "stream")
	writeExecutable(t, filepath.Join(extDir, "kodelet-extension-stream"), helperExtensionScript(t))
	t.Setenv("KODELET_BASE_PATH", t.TempDir())

	runtime, err := NewRuntime(
		context.Background(),
		WithConfig(DefaultConfig()),
		WithWorkingDir(rootDir),
		WithRoots(Root{Dir: rootDir, Kind: SourceKindLocalStandalone}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, runtime.Close()) })

	registered := runtime.Tools()
	require.Len(t, registered, 1)
	streamingTool, ok := registered[0].(tooltypes.StreamingTool)
	require.True(t, ok)

	var updates []tooltypes.ToolResult
	result := streamingTool.ExecuteStreaming(context.Background(), nil, `{"location":"Stream"}`, func(update tooltypes.ToolResult) {
		updates = append(updates, update)
	})

	require.Len(t, updates, 1)
	assert.Equal(t, "Searching code", updates[0].GetResult())
	var updateMetadata tooltypes.ExtensionToolMetadata
	require.True(t, tooltypes.ExtractMetadata(updates[0].StructuredData().Metadata, &updateMetadata))
	assert.Equal(t, float64(1), mustJSONNumber(t, updateMetadata.Data["revision"]))
	assert.Equal(t, "Weather for Stream from ", result.GetResult())
}

type staticUIInputBroker struct {
	value string
}

func (b staticUIInputBroker) Input(_ context.Context, request UIInputRequest) (UIInputResponse, error) {
	if request.Title == "" {
		return UIInputResponse{Status: UIInputStatusDismissed}, nil
	}
	return UIInputResponse{Status: UIInputStatusSubmitted, Value: b.value}, nil
}

func (b staticUIInputBroker) Confirm(_ context.Context, request UIConfirmRequest) (UIInputResponse, error) {
	if request.Title == "" {
		return UIInputResponse{Status: UIInputStatusDismissed}, nil
	}
	return UIInputResponse{Status: UIInputStatusSubmitted, Confirmed: true}, nil
}

func (b staticUIInputBroker) Select(_ context.Context, request UISelectRequest) (UIInputResponse, error) {
	if len(request.Options) == 0 {
		return UIInputResponse{Status: UIInputStatusDismissed}, nil
	}
	return UIInputResponse{Status: UIInputStatusSubmitted, Value: request.Options[0]}, nil
}

func (b staticUIInputBroker) Notify(context.Context, UINotifyRequest) (UIInputResponse, error) {
	return UIInputResponse{Status: UIInputStatusSubmitted}, nil
}

func TestRuntimeTimeoutPrecedence(t *testing.T) {
	runtime := EmptyRuntime()
	sdkToolTimeout := 15.0
	sdkToolNoTimeout := 0.0
	sdkInvalidTimeout := -1.0
	sdkCommandTimeout := 20.0
	sdkCommandNoTimeout := 0.0
	sdkEventTimeout := 2.0
	sdkEventNoTimeout := 0.0

	assert.Equal(t, 15*time.Second, runtime.toolTimeout(ToolRegistration{Name: "research", TimeoutInSec: &sdkToolTimeout}))
	assert.Zero(t, runtime.toolTimeout(ToolRegistration{Name: "forever", TimeoutInSec: &sdkToolNoTimeout}))
	assert.Zero(t, runtime.toolTimeout(ToolRegistration{Name: "invalid", TimeoutInSec: &sdkInvalidTimeout}))
	assert.Equal(t, 10*time.Minute, runtime.toolTimeout(ToolRegistration{Name: "default"}))

	assert.Equal(t, 20*time.Second, commandTimeout(CommandRegistration{Name: "/research", TimeoutInSec: &sdkCommandTimeout}))
	assert.Zero(t, commandTimeout(CommandRegistration{Name: "/forever", TimeoutInSec: &sdkCommandNoTimeout}))
	assert.Zero(t, commandTimeout(CommandRegistration{Name: "default"}))

	assert.Equal(t, 2*time.Second, eventTimeout(eventHandler{sub: Subscription{Event: EventToolCall, TimeoutInSec: &sdkEventTimeout}}))
	assert.Zero(t, eventTimeout(eventHandler{sub: Subscription{Event: EventToolCall, TimeoutInSec: &sdkEventNoTimeout}}))
	assert.Equal(t, 30*time.Second, eventTimeout(eventHandler{sub: Subscription{Event: EventToolCall}}))
}

func TestNewRuntimeFromViperHonorsDisabledExtensions(t *testing.T) {
	originalSettings := viper.AllSettings()
	defer func() {
		viper.Reset()
		for key, value := range originalSettings {
			viper.Set(key, value)
		}
	}()
	viper.Reset()
	viper.Set("extensions.enabled", false)

	runtime, err := NewRuntimeFromViper(t.Context(), t.TempDir())
	require.NoError(t, err)
	require.NotNil(t, runtime)
	assert.Empty(t, runtime.Tools())
	require.NoError(t, runtime.Close())

	runtime, err = newRuntimeFromViper(t.Context(), t.TempDir(), false)
	require.NoError(t, err)
	require.NotNil(t, runtime)
	require.NoError(t, runtime.Close())
}

func TestRuntimeDispatchesToolCallAndToolResultEvents(t *testing.T) {
	rootDir := t.TempDir()
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	extDir := filepath.Join(rootDir, "events")
	writeExecutable(t, filepath.Join(extDir, "kodelet-extension-events"), helperExtensionScript(t))

	runtime, err := NewRuntime(
		context.Background(),
		WithConfig(DefaultConfig()),
		WithWorkingDir(rootDir),
		WithRoots(Root{Dir: rootDir, Kind: SourceKindLocalStandalone}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, runtime.Close()) })

	callContext := ExtensionCallContext{ConversationID: "conv-events", CWD: rootDir, InvokedBy: "main"}
	decision := runtime.DispatchToolCall(context.Background(), callContext, "get_weather", `{"location":"London"}`, "call-1")
	require.False(t, decision.Blocked)
	assert.JSONEq(t, `{"location":"Paris"}`, decision.Input)

	original := tooltypes.StructuredToolResult{
		ToolName:  "get_weather",
		Success:   true,
		Timestamp: time.Now(),
		Metadata: &tooltypes.ExtensionToolMetadata{
			ExtensionID: "events",
			ToolName:    "get_weather",
			Output:      "Weather for Paris",
		},
	}
	modified, changed := runtime.DispatchToolResult(context.Background(), callContext, "get_weather", decision.Input, "call-1", original)

	require.True(t, changed)
	require.True(t, modified.Success)
	require.NotNil(t, modified.Metadata)
	var metadata tooltypes.ExtensionToolMetadata
	require.True(t, tooltypes.ExtractMetadata(modified.Metadata, &metadata))
	assert.Equal(t, "event modified output", metadata.Output)
	runtime.config.MaxOutputSize = 96
	presented, changed := runtime.DispatchToolResult(context.Background(), callContext, "get_weather", `{"location":"Presentation"}`, "call-1", original)
	require.True(t, changed)
	presentation, _, ok := tooltypes.ExtractExtensionToolPresentation(&presented)
	require.True(t, ok)
	assert.Equal(t, "Event presentation", presentation.Summary)
	assert.Equal(t, "markdown", presentation.Format)
	assert.LessOrEqual(t, len(presentation.Body), runtime.config.MaxOutputSize)
	assert.Contains(t, presentation.Body, "[TRUNCATED")

	updated, changed, accepted := runtime.DispatchToolUpdate(context.Background(), callContext, "get_weather", decision.Input, "call-1", original)
	require.True(t, accepted)
	assert.False(t, changed)
	assert.Equal(t, original, updated)

	rejected, changed, accepted := runtime.DispatchToolUpdate(context.Background(), callContext, "get_weather", `{"location":"InvalidUpdate"}`, "call-1", original)
	assert.True(t, accepted)
	assert.False(t, changed)
	assert.Equal(t, original, rejected)
}

func TestRuntimeDispatchToolCallCanBlock(t *testing.T) {
	rootDir := t.TempDir()
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	extDir := filepath.Join(rootDir, "events")
	writeExecutable(t, filepath.Join(extDir, "kodelet-extension-events"), helperExtensionScript(t))

	runtime, err := NewRuntime(
		context.Background(),
		WithConfig(DefaultConfig()),
		WithWorkingDir(rootDir),
		WithRoots(Root{Dir: rootDir, Kind: SourceKindLocalStandalone}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, runtime.Close()) })

	decision := runtime.DispatchToolCall(context.Background(), ExtensionCallContext{}, "bash", `{"command":"rm -rf /"}`, "call-1")

	assert.True(t, decision.Blocked)
	assert.Equal(t, "dangerous command denied", decision.Reason)
	assert.JSONEq(t, `{"command":"rm -rf /"}`, decision.Input)
}

func TestRuntimeDispatchesUserMessageEvent(t *testing.T) {
	rootDir := t.TempDir()
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	extDir := filepath.Join(rootDir, "events")
	writeExecutable(t, filepath.Join(extDir, "kodelet-extension-events"), helperExtensionScript(t))

	runtime, err := NewRuntime(
		context.Background(),
		WithConfig(DefaultConfig()),
		WithWorkingDir(rootDir),
		WithRoots(Root{Dir: rootDir, Kind: SourceKindLocalStandalone}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, runtime.Close()) })

	decision := runtime.DispatchUserMessage(context.Background(), ExtensionCallContext{ConversationID: "conv-events"}, "hello")
	require.False(t, decision.Blocked)
	assert.Equal(t, "hello [mutated]", decision.Message)

	blocked := runtime.DispatchUserMessage(context.Background(), ExtensionCallContext{}, "please block me")
	assert.True(t, blocked.Blocked)
	assert.Equal(t, "blocked by user.message", blocked.Reason)
	assert.Equal(t, "please block me", blocked.Message)
}

func TestRuntimeDispatchesAgentInitAndEndEvents(t *testing.T) {
	rootDir := t.TempDir()
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	extDir := filepath.Join(rootDir, "events")
	writeExecutable(t, filepath.Join(extDir, "kodelet-extension-events"), helperExtensionScript(t))

	runtime, err := NewRuntime(
		context.Background(),
		WithConfig(DefaultConfig()),
		WithWorkingDir(rootDir),
		WithRoots(Root{Dir: rootDir, Kind: SourceKindLocalStandalone}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, runtime.Close()) })

	callContext := ExtensionCallContext{ConversationID: "conv-events", CWD: rootDir, InvokedBy: "main"}
	systemPrompt := runtime.DispatchAgentInit(context.Background(), callContext, "base prompt")
	assert.Equal(t, "preface\nbase prompt\nappendix", systemPrompt)

	agentInit := runtime.DispatchAgentInitDecision(context.Background(), callContext, "base prompt", []string{"bash", "file_read"})
	assert.Equal(t, "preface\nbase prompt\nappendix", agentInit.SystemPrompt)
	assert.Equal(t, []string{"file_read", "get_weather"}, agentInit.AllowedTools)

	runtime.DispatchTurnEnd(context.Background(), callContext, "final response", 3)

	followUps := runtime.DispatchAgentEnd(context.Background(), callContext, []llmtypes.Message{{Role: "assistant", Content: "done"}})
	assert.Equal(t, []string{"inspect tests", "update docs"}, followUps)
}

func TestRuntimeDispatchesSessionLifecycleEvents(t *testing.T) {
	rootDir := t.TempDir()
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	statePath := filepath.Join(rootDir, "events.log")
	extDir := filepath.Join(rootDir, "events")
	writeExecutable(t, filepath.Join(extDir, "kodelet-extension-events"), helperExtensionScript(t))

	runtime, err := NewRuntime(
		context.Background(),
		WithConfig(DefaultConfig()),
		WithWorkingDir(rootDir),
		WithRoots(Root{Dir: rootDir, Kind: SourceKindLocalStandalone}),
	)
	require.NoError(t, err)
	require.NoError(t, runtime.Close())

	data, err := os.ReadFile(statePath)
	require.NoError(t, err)
	assert.Contains(t, string(data), EventSessionStart+"\n")
	assert.Contains(t, string(data), EventResourcesDiscover+"\n")
	assert.Contains(t, string(data), EventSessionEnd+"\n")
}

func TestRuntimeTryCommandRoutesExtensionCommand(t *testing.T) {
	rootDir := t.TempDir()
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	extDir := filepath.Join(rootDir, "commands")
	writeExecutable(t, filepath.Join(extDir, "kodelet-extension-commands"), helperExtensionScript(t))

	runtime, err := NewRuntime(
		context.Background(),
		WithConfig(DefaultConfig()),
		WithWorkingDir(rootDir),
		WithRoots(Root{Dir: rootDir, Kind: SourceKindLocalStandalone}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, runtime.Close()) })

	result, err := runtime.TryCommand(context.Background(), "/doctor verbose=true", "doctor", "verbose=true", ExtensionCallContext{ConversationID: "conv-command"})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Matched)
	assert.Equal(t, CommandActionRespond, result.Action)
	assert.Equal(t, "All extensions are healthy for conv-command.", result.Response)
	assert.Equal(t, "/doctor verbose=true", result.Display)
	assert.False(t, result.DisplayOverride)
}

func TestRuntimeTryCommandReturnsRunAgent(t *testing.T) {
	rootDir := t.TempDir()
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	extDir := filepath.Join(rootDir, "commands")
	writeExecutable(t, filepath.Join(extDir, "kodelet-extension-commands"), helperExtensionScript(t))

	runtime, err := NewRuntime(
		context.Background(),
		WithConfig(DefaultConfig()),
		WithWorkingDir(rootDir),
		WithRoots(Root{Dir: rootDir, Kind: SourceKindLocalStandalone}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, runtime.Close()) })

	result, err := runtime.TryCommand(context.Background(), "/review target=HEAD", "review", "target=HEAD", ExtensionCallContext{})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Matched)
	assert.Equal(t, CommandActionRunAgent, result.Action)
	assert.Equal(t, "Review HEAD", result.Prompt)
	assert.Equal(t, "review", result.RecipeName)
	assert.Equal(t, "Please review HEAD", result.Display)
	assert.True(t, result.DisplayOverride)
}

func TestRuntimeProcessSurvivesInitializationContextCancellation(t *testing.T) {
	rootDir := t.TempDir()
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	extDir := filepath.Join(rootDir, "commands")
	writeExecutable(t, filepath.Join(extDir, "kodelet-extension-commands"), helperExtensionScript(t))

	ctx, cancel := context.WithCancel(context.Background())
	runtime, err := NewRuntime(
		ctx,
		WithConfig(DefaultConfig()),
		WithWorkingDir(rootDir),
		WithRoots(Root{Dir: rootDir, Kind: SourceKindLocalStandalone}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, runtime.Close()) })
	cancel()
	time.Sleep(50 * time.Millisecond)

	result, err := runtime.TryCommand(context.Background(), "/doctor", "doctor", "", ExtensionCallContext{ConversationID: "conv-after-cancel"})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Matched)
	assert.Equal(t, "All extensions are healthy for conv-after-cancel.", result.Response)
}

func TestRuntimeInitializationReturnsCallerCancellationWithoutWarning(t *testing.T) {
	rootDir := t.TempDir()
	startedPath := filepath.Join(rootDir, "started")
	extDir := filepath.Join(rootDir, "blocking")
	writeExecutable(t, filepath.Join(extDir, "kodelet-extension-blocking"), fmt.Sprintf("#!/bin/sh\n: > %q\nsleep 60\n", startedPath))
	t.Setenv("KODELET_BASE_PATH", t.TempDir())

	var logOutput bytes.Buffer
	testLogger := logrus.New()
	testLogger.SetOutput(&logOutput)
	ctx, cancel := context.WithCancel(logger.WithLogger(context.Background(), logrus.NewEntry(testLogger)))
	t.Cleanup(cancel)

	type runtimeResult struct {
		runtime *Runtime
		err     error
	}
	done := make(chan runtimeResult, 1)
	go func() {
		runtime, err := NewRuntime(
			ctx,
			WithConfig(DefaultConfig()),
			WithWorkingDir(rootDir),
			WithRoots(Root{Dir: rootDir, Kind: SourceKindLocalStandalone}),
		)
		done <- runtimeResult{runtime: runtime, err: err}
	}()
	require.Eventually(t, func() bool {
		_, err := os.Stat(startedPath)
		return err == nil
	}, time.Second, 10*time.Millisecond)
	cancel()

	select {
	case result := <-done:
		assert.Nil(t, result.runtime)
		require.ErrorIs(t, result.err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("runtime initialization did not stop after cancellation")
	}
	assert.NotContains(t, logOutput.String(), "failed to initialize extension")
}

func TestRuntimeOwnsProcessContextLifetime(t *testing.T) {
	type contextKey string
	const key contextKey = "runtime-value"
	rootDir := t.TempDir()
	extDir := filepath.Join(rootDir, "runtime-context")
	writeExecutable(t, filepath.Join(extDir, "kodelet-extension-runtime-context"), helperEnvExtensionScript(t))
	t.Setenv("KODELET_BASE_PATH", t.TempDir())

	parentCtx, cancelParent := context.WithCancel(context.WithValue(context.Background(), key, "preserved"))
	runtime, err := NewRuntime(
		parentCtx,
		WithConfig(DefaultConfig()),
		WithWorkingDir(rootDir),
		WithRoots(Root{Dir: rootDir, Kind: SourceKindLocalStandalone}),
	)
	require.NoError(t, err)
	runtimeCtx := runtime.runtimeCtx
	require.Len(t, runtime.processes, 1)

	assert.Equal(t, "preserved", runtimeCtx.Value(key))
	assert.Equal(t, "preserved", runtime.processes[0].runtimeCtx.Value(key))
	cancelParent()
	assert.NoError(t, runtimeCtx.Err())

	require.NoError(t, runtime.Close())
	assert.ErrorIs(t, runtimeCtx.Err(), context.Canceled)
}

func TestRuntimeCloseClosesProcessesBeforeCancelingContext(t *testing.T) {
	runtimeCtx, cancelRuntime := context.WithCancel(context.Background())
	processes := []*Process{
		{client: &rpcClient{}},
		{client: &rpcClient{}},
	}
	contextCanceledAfterProcessClose := true
	runtime := &Runtime{
		runtimeCtx: runtimeCtx,
		cancelRuntime: func() {
			for _, process := range processes {
				process.mu.Lock()
				contextCanceledAfterProcessClose = contextCanceledAfterProcessClose && process.closed
				process.mu.Unlock()
			}
			cancelRuntime()
		},
		processes: processes,
	}

	require.NoError(t, runtime.Close())
	assert.True(t, contextCanceledAfterProcessClose)
	assert.ErrorIs(t, runtimeCtx.Err(), context.Canceled)
}

func TestSafeDataDirNamePreservesPluginIdentity(t *testing.T) {
	assert.Equal(t, "org@repo_weather", safeDataDirName("org@repo/weather"))
	assert.Equal(t, "extension", safeDataDirName("///"))
}

func TestExtensionProcessInheritsParentEnvironment(t *testing.T) {
	rootDir := t.TempDir()
	extDir := filepath.Join(rootDir, "weather")
	writeExecutable(t, filepath.Join(extDir, "kodelet-extension-weather"), helperExtensionScript(t))
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	t.Setenv("EXTENSION_VISIBLE_TOKEN", "parent-secret")

	runtime, err := NewRuntime(
		context.Background(),
		WithConfig(DefaultConfig()),
		WithWorkingDir(rootDir),
		WithRoots(Root{Dir: rootDir, Kind: SourceKindLocalStandalone}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, runtime.Close()) })

	tools := runtime.Tools()
	require.Len(t, tools, 1)

	ctx := kodelettools.ContextWithToolContext(context.Background(), kodelettools.ToolContext{WorkingDir: rootDir})
	result := tools[0].Execute(ctx, nil, `{"location":"Env"}`)

	assert.False(t, result.IsError())
	assert.Equal(t, "parent-secret", result.GetResult())
}

func TestRuntimeRejectsDuplicateToolRegistrations(t *testing.T) {
	rootDir := t.TempDir()
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	writeExecutable(t, filepath.Join(rootDir, "first", "kodelet-extension-first"), helperExtensionScript(t))
	writeExecutable(t, filepath.Join(rootDir, "second", "kodelet-extension-second"), helperExtensionScript(t))

	runtime, err := NewRuntime(
		context.Background(),
		WithConfig(DefaultConfig()),
		WithWorkingDir(rootDir),
		WithRoots(Root{Dir: rootDir, Kind: SourceKindLocalStandalone}),
	)
	if runtime != nil {
		_ = runtime.Close()
	}

	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate extension tool registration: get_weather")
}

func TestRuntimeRejectsDuplicateCommandRegistrations(t *testing.T) {
	runtime := EmptyRuntime()
	registration := CommandRegistration{Name: "doctor", Aliases: []string{"/doctor"}, Description: "Inspect extension runtime health"}
	require.NoError(t, runtime.register(context.Background(), &Process{Extension: Extension{ID: "first"}}, &InitializeResult{
		Commands: []CommandRegistration{registration},
	}))

	err := runtime.register(context.Background(), &Process{Extension: Extension{ID: "second"}}, &InitializeResult{
		Commands: []CommandRegistration{registration},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate extension command registration: doctor")
}

func TestExtensionHelperProcess(t *testing.T) {
	if os.Getenv("KODELET_TEST_EXTENSION_HELPER") != "1" {
		return
	}
	runExtensionHelperProcess()
	os.Exit(0)
}

func TestEnvExtensionHelperProcess(t *testing.T) {
	if os.Getenv("KODELET_TEST_ENV_EXTENSION_HELPER") != "1" {
		return
	}
	runEnvExtensionHelperProcess()
	os.Exit(0)
}

func helperExtensionScript(t *testing.T) string {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	return fmt.Sprintf("#!/bin/sh\nKODELET_TEST_EXTENSION_HELPER=1 exec %q -test.run TestExtensionHelperProcess --\n", executable)
}

func helperEnvExtensionScript(t *testing.T) string {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	return fmt.Sprintf("#!/bin/sh\nKODELET_TEST_ENV_EXTENSION_HELPER=1 exec %q -test.run TestEnvExtensionHelperProcess --\n", executable)
}

func runEnvExtensionHelperProcess() {
	workspaceCWD := os.Getenv(workspaceCWDEnvKey)
	if diagnostic := os.Getenv("KODELET_TEST_EXTENSION_STDERR"); diagnostic != "" {
		fmt.Fprintln(os.Stderr, diagnostic)
	}
	reader := bufio.NewReader(os.Stdin)
	for {
		payload, err := readFrame(reader)
		if err != nil {
			return
		}

		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal(payload, &request); err != nil {
			writeHelperResponse(request.ID, nil, &rpcError{Code: -32700, Message: err.Error()})
			continue
		}

		switch request.Method {
		case "extension.initialize":
			writeHelperResponse(request.ID, InitializeResult{
				Name: fmt.Sprintf("env;stderr_tty=%t", readerIsTerminal(os.Stderr)),
				Tools: []ToolRegistration{{
					Name:        "workspace_cwd",
					Description: "report workspace cwd",
					InputSchema: map[string]any{"type": "object"},
				}},
			}, nil)
		case "extension.tool.execute":
			writeHelperResponse(request.ID, ToolExecutionResult{Content: workspaceCWD}, nil)
		case "extension.event.handle":
			writeHelperResponse(request.ID, EventResult{}, nil)
		default:
			writeHelperResponse(request.ID, nil, &rpcError{Code: -32601, Message: "method not found"})
		}
	}
}

func runExtensionHelperProcess() {
	reader := bufio.NewReader(os.Stdin)
	for {
		payload, err := readFrame(reader)
		if err != nil {
			return
		}

		var request struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int64           `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(payload, &request); err != nil {
			writeHelperResponse(request.ID, nil, &rpcError{Code: -32700, Message: err.Error()})
			continue
		}

		switch request.Method {
		case "extension.initialize":
			result := InitializeResult{
				Name:    "weather",
				Version: "0.1.0",
				Tools: []ToolRegistration{{
					Name:        "get_weather",
					Description: "get the current weather",
					InputSchema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"location": map[string]any{"type": "string"},
						},
						"required": []any{"location"},
					},
				}},
				Commands: []CommandRegistration{{
					Name:        "doctor",
					Aliases:     []string{"/doctor"},
					Description: "Inspect extension runtime health",
				}, {
					Name:        "review",
					Aliases:     []string{"/review"},
					Description: "Run extension review",
					Kind:        "recipe",
				}},
				Shortcuts: []ShortcutRegistration{{
					Key:         "ctrl+alt+r",
					Description: "Refresh project context",
				}},
				Subscriptions: []Subscription{
					{Event: EventSessionStart, Priority: 10},
					{Event: EventResourcesDiscover, Priority: 10},
					{Event: EventToolCall, Priority: 10},
					{Event: EventToolUpdate, Priority: 10},
					{Event: EventToolResult, Priority: 10},
					{Event: EventUserMessage, Priority: 10},
					{Event: EventAgentInit, Priority: 10},
					{Event: EventTurnEnd, Priority: 10},
					{Event: EventAgentEnd, Priority: 10},
					{Event: EventSessionEnd, Priority: 10},
				},
			}
			writeHelperResponse(request.ID, result, nil)
		case "extension.tool.execute":
			var params executeToolParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				writeHelperResponse(request.ID, nil, &rpcError{Code: -32602, Message: err.Error()})
				continue
			}
			var input struct {
				Location string `json:"location"`
			}
			_ = json.Unmarshal(params.Input, &input)
			if input.Location == "Env" {
				writeHelperResponse(request.ID, ToolExecutionResult{Content: os.Getenv("EXTENSION_VISIBLE_TOKEN")}, nil)
				continue
			}
			if input.Location == "AskUI" {
				uiRequest := rpcRequest{JSONRPC: "2.0", ID: 99, Method: "kodelet.ui.input", Params: UIInputRequest{Title: "Choose option"}}
				uiPayload, _ := json.Marshal(uiRequest)
				_ = writeFrame(os.Stdout, uiPayload)
				uiResponsePayload, err := readFrame(reader)
				if err != nil {
					return
				}
				var uiResponse rpcResponse
				_ = json.Unmarshal(uiResponsePayload, &uiResponse)
				var uiResult UIInputResponse
				_ = json.Unmarshal(uiResponse.Result, &uiResult)
				writeHelperResponse(request.ID, ToolExecutionResult{Content: "User answered " + uiResult.Value}, nil)
				continue
			}
			if input.Location == "Stream" {
				updateRequest := rpcRequest{
					JSONRPC:  "2.0",
					ID:       98,
					ParentID: request.ID,
					Method:   "kodelet.tool.update",
					Params: ToolExecutionResult{
						Content: "Searching code",
						Data:    map[string]any{"revision": 1},
					},
				}
				updatePayload, _ := json.Marshal(updateRequest)
				_ = writeFrame(os.Stdout, updatePayload)
				if _, err := readFrame(reader); err != nil {
					return
				}
			}
			result := ToolExecutionResult{
				Content: fmt.Sprintf("Weather for %s from %s", input.Location, params.Context.ConversationID),
				Data:    map[string]any{"unit": "celsius"},
			}
			writeHelperResponse(request.ID, result, nil)
		case "extension.event.handle":
			var params eventParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				writeHelperResponse(request.ID, nil, &rpcError{Code: -32602, Message: err.Error()})
				continue
			}
			writeHelperResponse(request.ID, handleHelperEvent(params), nil)
		case "extension.command.execute":
			var params executeCommandParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				writeHelperResponse(request.ID, nil, &rpcError{Code: -32602, Message: err.Error()})
				continue
			}
			writeHelperResponse(request.ID, handleHelperCommand(params), nil)
		case "extension.shortcut.execute":
			var params executeShortcutParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				writeHelperResponse(request.ID, nil, &rpcError{Code: -32602, Message: err.Error()})
				continue
			}
			if params.Key != "ctrl+alt+r" || params.Context.ConversationID != "conv-shortcut" || params.Context.UIScopeID != "conversation-scope" {
				writeHelperResponse(request.ID, nil, &rpcError{Code: -32602, Message: "unexpected shortcut context"})
				continue
			}
			if os.Getenv("KODELET_TEST_SHORTCUT_NULL_RESULT") == "1" {
				writeHelperResponse(request.ID, nil, nil)
			} else {
				writeHelperResponse(request.ID, ShortcutResult{Action: ShortcutActionSubmit, Message: "/refresh"}, nil)
			}
		default:
			writeHelperResponse(request.ID, nil, &rpcError{Code: -32601, Message: "method not found"})
		}
	}
}

func handleHelperCommand(params executeCommandParams) CommandResult {
	switch params.Name {
	case "doctor":
		return CommandResult{Action: CommandActionRespond, Response: fmt.Sprintf("All extensions are healthy for %s.", params.Context.ConversationID)}
	case "review":
		target, _ := params.Input["target"].(string)
		if target == "" {
			target = "HEAD"
		}
		return CommandResult{Action: CommandActionRunAgent, Prompt: "Review " + target, RecipeName: "review", Display: "Please review " + target}
	default:
		return CommandResult{Action: CommandActionPass}
	}
}

func handleHelperEvent(params eventParams) EventResult {
	switch params.Event {
	case EventSessionStart, EventResourcesDiscover, EventSessionEnd:
		if path := filepath.Join(params.Context.CWD, "events.log"); params.Context.CWD != "" {
			file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if err == nil {
				_, _ = file.WriteString(params.Event + "\n")
				_ = file.Close()
			}
		}
		if params.Event == EventSessionStart && params.Context.ConversationID != "" {
			path := filepath.Join(params.Context.CWD, "session-start-context.json")
			encoded, _ := json.Marshal(params.Context)
			_ = os.WriteFile(path, encoded, 0o644)
		}
		return EventResult{}
	case EventToolCall:
		payload, _ := json.Marshal(params.Payload)
		var event toolCallPayload
		_ = json.Unmarshal(payload, &event)
		if event.Tool.Name == "bash" {
			return EventResult{Block: &EventBlock{Reason: "dangerous command denied"}}
		}
		return EventResult{Input: json.RawMessage(`{"location":"Paris"}`)}
	case EventToolUpdate, EventToolResult:
		payload, _ := json.Marshal(params.Payload)
		var event toolResultPayload
		_ = json.Unmarshal(payload, &event)
		var input struct {
			Location string `json:"location"`
		}
		_ = json.Unmarshal(event.Tool.Input, &input)
		if params.Event == EventToolUpdate && input.Location == "InvalidUpdate" {
			return EventResult{Output: json.RawMessage(`"invalid"`)}
		}
		outputText := "event modified output"
		if params.Event == EventToolUpdate {
			outputText = "event updated output"
		}
		metadata := tooltypes.ExtensionToolMetadata{ExtensionID: "events", ToolName: event.Tool.Name, Output: outputText}
		if input.Location == "Presentation" {
			metadata.Data = map[string]any{"presentation": map[string]any{
				"summary": "  Event\npresentation  ",
				"body":    string(bytes.Repeat([]byte("界"), 100)),
				"format":  "MARKDOWN",
			}}
		}
		modified := tooltypes.StructuredToolResult{
			ToolName:  event.Tool.Name,
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  metadata,
		}
		output, _ := json.Marshal(modified)
		return EventResult{Output: output}
	case EventUserMessage:
		payload, _ := json.Marshal(params.Payload)
		var event userMessagePayload
		_ = json.Unmarshal(payload, &event)
		if event.Message == "please block me" {
			return EventResult{Block: &EventBlock{Reason: "blocked by user.message"}}
		}
		message := event.Message + " [mutated]"
		return EventResult{Message: &message}
	case EventAgentInit:
		prepend := "preface"
		appendix := "appendix"
		return EventResult{
			SystemPrompt: &SystemPromptPatch{Prepend: &prepend, Append: &appendix},
			Tools:        &ToolListPatch{Disable: []string{"bash"}, Enable: []string{"get_weather"}},
		}
	case EventTurnEnd:
		return EventResult{}
	case EventAgentEnd:
		return EventResult{FollowUpMessages: []string{"inspect tests", "update docs"}}
	default:
		return EventResult{}
	}
}

func writeHelperResponse(id int64, result any, rpcErr *rpcError) {
	response := rpcResponse{JSONRPC: "2.0", ID: id, Error: rpcErr}
	if result != nil {
		payload, _ := json.Marshal(result)
		response.Result = payload
	}
	payload, _ := json.Marshal(response)
	_ = writeFrame(os.Stdout, payload)
}
