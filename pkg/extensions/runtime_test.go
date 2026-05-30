package extensions

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	kodelettools "github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuntimeInitializesExtensionAndExecutesRegisteredTool(t *testing.T) {
	rootDir := t.TempDir()
	extDir := filepath.Join(rootDir, "weather")
	writeExecutable(t, filepath.Join(extDir, "kodelet-extension-weather"), helperExtensionScript(t))

	config := DefaultConfig()
	config.Timeout = 5 * time.Second
	config.ToolTimeout = 5 * time.Second
	runtime, err := NewRuntime(
		context.Background(),
		WithConfig(config),
		WithWorkingDir(rootDir),
		WithRoots(Root{Dir: rootDir, Kind: SourceKindLocalStandalone}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, runtime.Close()) })

	tools := runtime.Tools()
	require.Len(t, tools, 1)
	assert.Equal(t, "get_weather", tools[0].Name())
	assert.Equal(t, "get the current weather", tools[0].Description())

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

func TestRuntimeDispatchesToolCallAndToolResultEvents(t *testing.T) {
	rootDir := t.TempDir()
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
	modified := runtime.DispatchToolResult(context.Background(), callContext, "get_weather", decision.Input, "call-1", original)

	require.True(t, modified.Success)
	require.NotNil(t, modified.Metadata)
	var metadata tooltypes.ExtensionToolMetadata
	require.True(t, tooltypes.ExtractMetadata(modified.Metadata, &metadata))
	assert.Equal(t, "event modified output", metadata.Output)
}

func TestRuntimeDispatchToolCallCanBlock(t *testing.T) {
	rootDir := t.TempDir()
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

	runtime.DispatchTurnEnd(context.Background(), callContext, "final response", 3)

	followUps := runtime.DispatchAgentEnd(context.Background(), callContext, []llmtypes.Message{{Role: "assistant", Content: "done"}})
	assert.Equal(t, []string{"inspect tests", "update docs"}, followUps)
}

func TestRuntimeDispatchesSessionLifecycleEvents(t *testing.T) {
	rootDir := t.TempDir()
	statePath := filepath.Join(rootDir, "events.log")
	extDir := filepath.Join(rootDir, "events")
	writeExecutable(t, filepath.Join(extDir, "kodelet-extension-events"), helperExtensionScript(t))
	t.Setenv("KODELET_TEST_EXTENSION_STATE", statePath)

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
}

func TestRuntimeTryCommandReturnsRunAgent(t *testing.T) {
	rootDir := t.TempDir()
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
}

func TestRuntimeRejectsDuplicateToolRegistrations(t *testing.T) {
	rootDir := t.TempDir()
	writeExecutable(t, filepath.Join(rootDir, "first", "kodelet-extension-first"), helperExtensionScript(t))
	writeExecutable(t, filepath.Join(rootDir, "second", "kodelet-extension-second"), helperExtensionScript(t))

	runtime, err := NewRuntime(
		context.Background(),
		WithConfig(DefaultConfig()),
		WithRoots(Root{Dir: rootDir, Kind: SourceKindLocalStandalone}),
	)
	if runtime != nil {
		_ = runtime.Close()
	}

	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate extension tool registration: get_weather")
}

func TestExtensionHelperProcess(t *testing.T) {
	if os.Getenv("KODELET_TEST_EXTENSION_HELPER") != "1" {
		return
	}
	runExtensionHelperProcess()
	os.Exit(0)
}

func helperExtensionScript(t *testing.T) string {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	return fmt.Sprintf("#!/bin/sh\nKODELET_TEST_EXTENSION_HELPER=1 exec %q -test.run TestExtensionHelperProcess --\n", executable)
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
				Subscriptions: []Subscription{
					{Event: EventSessionStart, Priority: 10},
					{Event: EventResourcesDiscover, Priority: 10},
					{Event: EventToolCall, Priority: 10},
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
		return CommandResult{Action: CommandActionRunAgent, Prompt: "Review " + target, RecipeName: "review"}
	default:
		return CommandResult{Action: CommandActionPass}
	}
}

func handleHelperEvent(params eventParams) EventResult {
	switch params.Event {
	case EventSessionStart, EventResourcesDiscover, EventSessionEnd:
		if path := os.Getenv("KODELET_TEST_EXTENSION_STATE"); path != "" {
			file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if err == nil {
				_, _ = file.WriteString(params.Event + "\n")
				_ = file.Close()
			}
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
	case EventToolResult:
		payload, _ := json.Marshal(params.Payload)
		var event toolResultPayload
		_ = json.Unmarshal(payload, &event)
		metadata := tooltypes.ExtensionToolMetadata{ExtensionID: "events", ToolName: event.Tool.Name, Output: "event modified output"}
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
		return EventResult{SystemPrompt: &SystemPromptPatch{Prepend: &prepend, Append: &appendix}}
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
