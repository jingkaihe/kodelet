package extension_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/sdk/go/extension"
	"github.com/jingkaihe/kodelet/sdk/go/extension/extensiontest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type weatherInput struct {
	Location string   `json:"location" jsonschema:"Location to fetch weather for"`
	Units    string   `json:"units,omitempty"`
	Tags     []string `json:"tags,omitempty"`
}

func TestRegistersToolsCommandsEventsAndExecutesHandlers(t *testing.T) {
	ctx := t.Context()
	ext := extension.New(extension.Metadata{Name: "weather", Version: "0.1.0"})
	extension.RegisterTool(ext, extension.Tool[weatherInput]{
		Name:        "get_weather",
		Description: "Get the current weather",
		Group:       "forecast",
		Short:       "Weather lookup",
		Timeout:     10 * time.Second,
		Execute: func(_ context.Context, in weatherInput, _ *extension.ToolContext) (extension.ToolResult, error) {
			return extension.ToolResult{Content: "Weather for " + in.Location, Data: map[string]any{"location": in.Location}}, nil
		},
	})
	extension.RegisterCommand(ext, extension.Command[map[string]any]{
		Name:        "/hello",
		Aliases:     []string{"hi"},
		Description: "Say hello",
		Execute: func(_ context.Context, _ map[string]any, cc *extension.CommandContext) (extension.CommandResult, error) {
			return extension.Respond("hello " + cc.Invocation.Args[0]), nil
		},
	})
	extension.On(ext, extension.EventToolCall, func(_ context.Context, event extension.ToolCallEvent, _ *extension.EventContext) (extension.EventResult, error) {
		var input struct {
			Command string `json:"command"`
		}
		_ = event.Tool.DecodeInput(&input)
		if event.Tool.Name == "bash" && strings.Contains(input.Command, "rm -rf /") {
			return extension.EventResult{Block: &extension.EventBlock{Reason: "Refusing dangerous shell command"}}, nil
		}
		return extension.EventResult{}, nil
	}, extension.WithPriority(5), extension.WithTimeout(5*time.Second))
	h := newHarness(t, ext, nil)

	initialized, err := h.Initialize(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, "weather", initialized.Name)
	assert.Equal(t, "0.1.0", initialized.Version)
	require.Len(t, initialized.Tools, 1)
	tool := initialized.Tools[0]
	assert.Equal(t, "get_weather", tool.Name)
	assert.Equal(t, "forecast", tool.Group)
	assert.Equal(t, "Weather lookup", tool.Short)
	require.NotNil(t, tool.TimeoutInSec)
	assert.InDelta(t, 10, *tool.TimeoutInSec, 0)
	assert.Equal(t, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"location": map[string]any{"type": "string", "description": "Location to fetch weather for"},
			"units":    map[string]any{"type": "string"},
			"tags":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
		"required":             []any{"location"},
		"additionalProperties": false,
	}, tool.InputSchema)
	require.Len(t, initialized.Commands, 1)
	assert.Equal(t, "/hello", initialized.Commands[0].Name)
	assert.Equal(t, []string{"hi"}, initialized.Commands[0].Aliases)
	assert.Nil(t, initialized.Commands[0].InputSchema)
	require.Len(t, initialized.Subscriptions, 1)
	assert.Equal(t, "tool.call", initialized.Subscriptions[0].Event)
	assert.Equal(t, 5, initialized.Subscriptions[0].Priority)
	assert.InDelta(t, 5, *initialized.Subscriptions[0].TimeoutInSec, 0)

	result, err := h.ExecuteTool(ctx, "get_weather", map[string]any{"location": "London"})
	require.NoError(t, err)
	assert.Equal(t, "Weather for London", result.Content)
	assert.Equal(t, map[string]any{"location": "London"}, result.Data)

	_, err = h.ExecuteTool(ctx, "get_weather", map[string]any{})
	require.ErrorContains(t, err, "invalid input for tool get_weather")
	_, err = h.ExecuteTool(ctx, "get_weather", map[string]any{"location": "London", "extra": true})
	require.ErrorContains(t, err, "invalid input for tool get_weather")
	_, err = h.ExecuteTool(ctx, "missing", map[string]any{})
	require.ErrorContains(t, err, "unknown extension tool: missing")

	command, err := h.ExecuteCommand(ctx, "hi", nil, extension.CommandInvocation{Raw: "/hi bob", CommandName: "hi", Args: []string{"bob"}})
	require.NoError(t, err)
	assert.Equal(t, extension.Respond("hello bob"), command)

	blocked, err := h.HandleEvent(ctx, "tool.call", map[string]any{"tool": map[string]any{"name": "bash", "input": map[string]any{"command": "rm -rf /"}}})
	require.NoError(t, err)
	require.NotNil(t, blocked.Block)
	assert.Equal(t, "Refusing dangerous shell command", blocked.Block.Reason)
	allowed, err := h.HandleEvent(ctx, "tool.call", map[string]any{"tool": map[string]any{"name": "bash", "input": map[string]any{"command": "ls"}}})
	require.NoError(t, err)
	assert.Equal(t, extension.EventResult{}, allowed)
}

func TestMetadataDefaultsToExtensionID(t *testing.T) {
	ext := extension.New(extension.Metadata{})
	ext.SetMetadata(extension.Metadata{Version: "2.0.0"})
	initialized, err := newHarness(t, ext, nil).Initialize(t.Context(), nil)
	require.NoError(t, err)
	assert.Equal(t, "test-extension", initialized.Name)
	assert.Equal(t, "2.0.0", initialized.Version)
	assert.Empty(t, initialized.Tools)
	assert.NotNil(t, initialized.Tools, "registration arrays are always present")
}

func TestToolResultsPreserveAttachmentsPresentationAndFalsyStructuredContent(t *testing.T) {
	ext := extension.New(extension.Metadata{})
	outputSchema := map[string]any{"type": "boolean"}
	extension.RegisterTool(ext, extension.Tool[struct{}]{
		Name:         "chart",
		Description:  "Draw a chart",
		OutputSchema: outputSchema,
		Execute: func(context.Context, struct{}, *extension.ToolContext) (extension.ToolResult, error) {
			return extension.ToolResult{
				Content:           "chart",
				StructuredContent: false,
				Data:              map[string]any{"presentation": extension.Presentation{Summary: "Chart", Format: "markdown"}},
				Attachments:       []extension.Attachment{extension.ImageFile("chart.png"), extension.ImageData([]byte{1, 2, 3}, "image/png")},
			}, nil
		},
	})
	outputSchema["type"] = "mutated"
	h := newHarness(t, ext, nil)
	initialized, err := h.Initialize(t.Context(), nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"type": "boolean"}, initialized.Tools[0].OutputSchema)
	assert.Equal(t, map[string]any{"type": "object", "additionalProperties": false}, initialized.Tools[0].InputSchema)

	result, err := h.ExecuteTool(t.Context(), "chart", map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, false, result.StructuredContent)
	assert.Equal(t, map[string]any{"presentation": map[string]any{"summary": "Chart", "format": "markdown"}}, result.Data)
	assert.Equal(t, []extension.Attachment{
		{Type: "image", Path: "chart.png"},
		{Type: "image", Data: "AQID", MimeType: "image/png"},
	}, result.Attachments)
}

func TestRawSchemasArePassedThroughWithoutValidation(t *testing.T) {
	ext := extension.New(extension.Metadata{})
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"query": map[string]any{"type": "string"}},
		"required":   []any{"query"},
	}
	extension.RegisterTool(ext, extension.Tool[map[string]any]{
		Name:        "raw",
		Description: "Raw schema",
		InputSchema: schema,
		Execute: func(_ context.Context, in map[string]any, _ *extension.ToolContext) (extension.ToolResult, error) {
			return extension.Text(fmt.Sprint(in["query"], " ", in["extra"])), nil
		},
	})
	extension.RegisterTool(ext, extension.Tool[json.RawMessage]{
		Name:        "passthrough",
		Description: "Any input",
		Execute: func(_ context.Context, in json.RawMessage, _ *extension.ToolContext) (extension.ToolResult, error) {
			return extension.Text(string(in)), nil
		},
	})
	h := newHarness(t, ext, nil)
	initialized, err := h.Initialize(t.Context(), nil)
	require.NoError(t, err)
	assert.Equal(t, schema, initialized.Tools[0].InputSchema)
	assert.Equal(t, map[string]any{"type": "object", "additionalProperties": true}, initialized.Tools[1].InputSchema)

	result, err := h.ExecuteTool(t.Context(), "raw", map[string]any{"query": "q", "extra": 1})
	require.NoError(t, err)
	assert.Equal(t, "q 1", result.Content)
	result, err = h.ExecuteTool(t.Context(), "raw", map[string]any{})
	require.NoError(t, err, "raw schemas are left for the handler to validate")
	assert.Equal(t, "<nil> <nil>", result.Content)
	result, err = h.ExecuteTool(t.Context(), "passthrough", map[string]any{"a": []int{1}})
	require.NoError(t, err)
	assert.JSONEq(t, `{"a":[1]}`, result.Content)
}

func TestEventHandlersRunByPriorityAndMergeResults(t *testing.T) {
	ext := extension.New(extension.Metadata{})
	var order []string
	extension.On(ext, extension.EventToolCall, func(_ context.Context, event extension.ToolCallEvent, _ *extension.EventContext) (extension.EventResult, error) {
		order = append(order, "low:"+string(event.Tool.Input))
		return extension.EventResult{FollowUpMessages: []string{"low"}}, nil
	})
	extension.On(ext, extension.EventToolCall, func(_ context.Context, event extension.ToolCallEvent, _ *extension.EventContext) (extension.EventResult, error) {
		order = append(order, "high:"+event.Tool.Name+":"+event.Event+":"+event.ID)
		return extension.EventResult{Input: map[string]any{"command": "ls"}, FollowUpMessages: []string{"high"}}, nil
	}, extension.WithPriority(10))
	h := newHarness(t, ext, nil)

	result, err := h.HandleEvent(t.Context(), "tool.call", map[string]any{"tool": map[string]any{"name": "bash", "input": map[string]any{"command": "rm"}}})
	require.NoError(t, err)
	assert.Equal(t, []string{"high:bash:tool.call:event-1", `low:{"command":"ls"}`}, order)
	assert.Equal(t, map[string]any{"command": "ls"}, result.Input)
	assert.Equal(t, []string{"high", "low"}, result.FollowUpMessages)
}

func TestFollowUpsAggregateAtTurnAndAgentEnd(t *testing.T) {
	for _, name := range []string{"turn.end", "agent.end"} {
		t.Run(name, func(t *testing.T) {
			ext := extension.New(extension.Metadata{})
			high := extension.FollowUp{Text: "new mail", Visibility: "hidden", Key: "mailbox:owner:contact", Revision: 42}
			low := extension.FollowUp{Text: "visible notification"}
			register := func(result extension.EventResult, priority int) {
				if name == "turn.end" {
					extension.On(ext, extension.EventTurnEnd, func(context.Context, extension.TurnEndEvent, *extension.EventContext) (extension.EventResult, error) {
						return result, nil
					}, extension.WithPriority(priority))
				} else {
					extension.On(ext, extension.EventAgentEnd, func(context.Context, extension.AgentEndEvent, *extension.EventContext) (extension.EventResult, error) {
						return result, nil
					}, extension.WithPriority(priority))
				}
			}
			register(extension.EventResult{FollowUps: []extension.FollowUp{low}, FollowUpMessages: []string{"legacy low"}}, 0)
			register(extension.EventResult{FollowUps: []extension.FollowUp{high}, FollowUpMessages: []string{"legacy high"}}, 10)
			result, err := newHarness(t, ext, nil).HandleEvent(t.Context(), name, map[string]any{})
			require.NoError(t, err)
			assert.Equal(t, []extension.FollowUp{high, low}, result.FollowUps)
			assert.Equal(t, []string{"legacy high", "legacy low"}, result.FollowUpMessages)
			raw, err := json.Marshal(result)
			require.NoError(t, err)
			var wire map[string]any
			require.NoError(t, json.Unmarshal(raw, &wire))
			assert.Contains(t, wire, "followUps")
			assert.Contains(t, wire, "followUpMessages")
		})
	}
}

func TestBlockStopsTheChainAndAgentInitPatchesMerge(t *testing.T) {
	ext := extension.New(extension.Metadata{})
	calledAfterBlock := false
	extension.On(ext, extension.EventToolCall, func(context.Context, extension.ToolCallEvent, *extension.EventContext) (extension.EventResult, error) {
		return extension.EventResult{Block: &extension.EventBlock{Reason: "no"}}, nil
	})
	extension.On(ext, extension.EventToolCall, func(context.Context, extension.ToolCallEvent, *extension.EventContext) (extension.EventResult, error) {
		calledAfterBlock = true
		return extension.EventResult{}, nil
	})
	extension.On(ext, extension.EventAgentInit, func(_ context.Context, event extension.AgentInitEvent, _ *extension.EventContext) (extension.EventResult, error) {
		return extension.EventResult{
			SystemPrompt: &extension.SystemPromptPatch{Append: extension.Ptr("Prefer concise answers. " + strings.Join(event.AllowedTools, ","))},
			Tools:        &extension.ToolListPatch{Disable: []string{"bash"}},
		}, nil
	})
	extension.On(ext, extension.EventAgentInit, func(context.Context, extension.AgentInitEvent, *extension.EventContext) (extension.EventResult, error) {
		return extension.EventResult{Tools: &extension.ToolListPatch{Enable: []string{"file_read"}}}, nil
	})
	h := newHarness(t, ext, nil)

	blocked, err := h.HandleEvent(t.Context(), "tool.call", map[string]any{"tool": map[string]any{"name": "bash", "input": map[string]any{}}})
	require.NoError(t, err)
	assert.Equal(t, "no", blocked.Block.Reason)
	assert.False(t, calledAfterBlock)

	init, err := h.HandleEvent(t.Context(), "agent.init", map[string]any{"allowedTools": []string{"bash", "grep"}})
	require.NoError(t, err)
	assert.Equal(t, "Prefer concise answers. bash,grep", *init.SystemPrompt.Append)
	assert.Nil(t, init.SystemPrompt.Replace)
	assert.Equal(t, []string{"bash"}, init.Tools.Disable)
	assert.Equal(t, []string{"file_read"}, init.Tools.Enable)
}

func TestSystemPromptPatchesCanSetEmptyValues(t *testing.T) {
	data, err := json.Marshal(extension.SystemPromptPatch{Replace: extension.Ptr(""), Append: extension.Ptr("Only this.")})
	require.NoError(t, err)
	assert.JSONEq(t, `{"replace":"","append":"Only this."}`, string(data), "an empty replacement must reach the host")
	data, err = json.Marshal(extension.SystemPromptPatch{})
	require.NoError(t, err)
	assert.JSONEq(t, `{}`, string(data))

	ext := extension.New(extension.Metadata{})
	extension.On(ext, extension.EventAgentInit, func(context.Context, extension.AgentInitEvent, *extension.EventContext) (extension.EventResult, error) {
		return extension.EventResult{SystemPrompt: &extension.SystemPromptPatch{Replace: extension.Ptr("")}}, nil
	})
	result, err := newHarness(t, ext, nil).HandleEvent(t.Context(), "agent.init", map[string]any{"systemPrompt": "original"})
	require.NoError(t, err)
	require.NotNil(t, result.SystemPrompt.Replace)
	assert.Empty(t, *result.SystemPrompt.Replace)
	assert.Nil(t, result.SystemPrompt.Append)
}

func TestOutputHooksClearMachineDataBeforeLaterHandlers(t *testing.T) {
	ext := extension.New(extension.Metadata{})
	extension.On(ext, extension.EventToolResult, func(_ context.Context, event extension.ToolResultEvent, _ *extension.EventContext) (extension.EventResult, error) {
		var output map[string]any
		require.NoError(t, event.Tool.DecodeOutput(&output))
		output["content"] = "[redacted]"
		return extension.EventResult{Output: output}, nil
	}, extension.WithPriority(1))
	var observed map[string]any
	var input json.RawMessage
	extension.On(ext, extension.EventToolResult, func(_ context.Context, event extension.ToolResultEvent, _ *extension.EventContext) (extension.EventResult, error) {
		input = event.Tool.Input
		return extension.EventResult{}, event.Tool.DecodeOutput(&observed)
	})
	h := newHarness(t, ext, nil)

	result, err := h.HandleEvent(t.Context(), "tool.result", map[string]any{"tool": map[string]any{
		"name":   "lookup",
		"input":  map[string]any{"id": 1},
		"output": map[string]any{"content": "secret", "data": map[string]any{"token": "abc"}},
	}})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"content": "[redacted]"}, result.Output)
	assert.Equal(t, map[string]any{"content": "[redacted]"}, observed)
	assert.JSONEq(t, `{"id":1}`, string(input))
}

func TestObservationalOutputHooksPreserveMachineData(t *testing.T) {
	ext := extension.New(extension.Metadata{})
	var observed map[string]any
	extension.On(ext, extension.EventToolUpdate, noop[extension.ToolUpdateEvent], extension.WithPriority(1))
	extension.On(ext, extension.EventToolUpdate, func(_ context.Context, event extension.ToolUpdateEvent, _ *extension.EventContext) (extension.EventResult, error) {
		return extension.EventResult{}, event.Tool.DecodeOutput(&observed)
	})
	result, err := newHarness(t, ext, nil).HandleEvent(t.Context(), "tool.update", map[string]any{"tool": map[string]any{
		"name": "lookup", "input": map[string]any{}, "output": map[string]any{"content": "x", "data": map[string]any{"k": "v"}},
	}})
	require.NoError(t, err)
	assert.Nil(t, result.Output)
	assert.Equal(t, map[string]any{"content": "x", "data": map[string]any{"k": "v"}}, observed)
}

func TestTypedEventsAndCustomEvents(t *testing.T) {
	ext := extension.New(extension.Metadata{})
	var finished extension.AgentFinishedEvent
	var custom map[string]any
	extension.On(ext, extension.EventAgentFinished, func(_ context.Context, event extension.AgentFinishedEvent, ec *extension.EventContext) (extension.EventResult, error) {
		finished = event
		assert.Equal(t, "conv-1", ec.ConversationID)
		return extension.EventResult{}, nil
	})
	extension.On(ext, extension.CustomEvent("future.event"), func(_ context.Context, event map[string]any, _ *extension.EventContext) (extension.EventResult, error) {
		custom = event
		message := "custom"
		return extension.EventResult{Message: &message}, nil
	})
	h := newHarness(t, ext, nil)
	_, err := h.HandleEvent(t.Context(), "agent.finished", map[string]any{"status": "failed", "reason": "turn_limit", "error": "too many turns"},
		extensiontest.WithCall(extension.CallInfo{ConversationID: "conv-1"}))
	require.NoError(t, err)
	assert.Equal(t, "failed", finished.Status)
	assert.Equal(t, "turn_limit", finished.Reason)
	assert.Equal(t, "too many turns", finished.Error)
	assert.Equal(t, "agent.finished", finished.Event)

	result, err := h.HandleEvent(t.Context(), "future.event", map[string]any{"value": 1})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"id": "event-2", "event": "future.event", "value": float64(1)}, custom)
	require.NotNil(t, result.Message)
	assert.Equal(t, "custom", *result.Message)
}

type deployInput struct {
	Env string `json:"env"`
}

func TestCommandValidationCanPassToTheNextRoute(t *testing.T) {
	ext := extension.New(extension.Metadata{})
	extension.RegisterCommand(ext, extension.Command[deployInput]{
		Name:        "deploy",
		Description: "Deploy",
		Kind:        extension.CommandKindRecipe,
		Execute: func(_ context.Context, in deployInput, _ *extension.CommandContext) (extension.CommandResult, error) {
			if in.Env == "noop" {
				return extension.CommandResult{}, nil
			}
			return extension.RunAgent("deploy to " + in.Env), nil
		},
	})
	h := newHarness(t, ext, nil)
	initialized, err := h.Initialize(t.Context(), nil)
	require.NoError(t, err)
	assert.Equal(t, extension.CommandKindRecipe, initialized.Commands[0].Kind)
	assert.Contains(t, initialized.Commands[0].InputSchema["properties"], "env")

	invocation := extension.CommandInvocation{Raw: "/deploy", CommandName: "deploy", Args: []string{}, Flags: map[string]any{}}
	result, err := h.ExecuteCommand(t.Context(), "/deploy", map[string]any{"env": "prod"}, invocation)
	require.NoError(t, err)
	assert.Equal(t, extension.RunAgent("deploy to prod"), result)
	for _, input := range []any{nil, map[string]any{}, map[string]any{"env": 1}} {
		result, err = h.ExecuteCommand(t.Context(), "deploy", input, invocation)
		require.NoError(t, err)
		assert.Equal(t, extension.Pass(), result)
	}
	result, err = h.ExecuteCommand(t.Context(), "deploy", map[string]any{"env": "noop"}, invocation)
	require.NoError(t, err)
	assert.Equal(t, extension.Pass(), result, "a zero result passes")
}

func TestInvalidAndDuplicateRegistrationsPanic(t *testing.T) {
	okTool := func(context.Context, *extension.ToolContext) (extension.ToolResult, error) {
		return extension.ToolResult{}, nil
	}
	noopCommand := func(context.Context, map[string]any, *extension.CommandContext) (extension.CommandResult, error) {
		return extension.Pass(), nil
	}
	ext := extension.New(extension.Metadata{})
	addTool(ext, "tool", okTool)
	assert.PanicsWithValue(t, "extension: duplicate tool registration: tool", func() { addTool(ext, "tool", okTool) })
	assert.Panics(t, func() { extension.RegisterTool(ext, extension.Tool[struct{}]{Name: "no-handler"}) })
	assert.Panics(t, func() {
		extension.RegisterTool(ext, extension.Tool[struct{ C chan int }]{Name: "unsupported", Execute: func(context.Context, struct{ C chan int }, *extension.ToolContext) (extension.ToolResult, error) {
			return extension.ToolResult{}, nil
		}})
	})
	extension.RegisterCommand(ext, extension.Command[map[string]any]{Name: "/review", Aliases: []string{"r"}, Execute: noopCommand})
	assert.PanicsWithValue(t, "extension: duplicate command registration: r", func() {
		extension.RegisterCommand(ext, extension.Command[map[string]any]{Name: "other", Aliases: []string{"/r"}, Execute: noopCommand})
	})
	assert.Panics(t, func() {
		extension.RegisterCommand(ext, extension.Command[map[string]any]{Name: "dup", Aliases: []string{"x", "x"}, Execute: noopCommand})
	})
	shortcut := extension.Shortcut{Handler: func(context.Context, *extension.ShortcutContext) (extension.ShortcutResult, error) {
		return extension.ShortcutResult{}, nil
	}}
	ext.RegisterShortcut("ctrl+g", shortcut)
	assert.PanicsWithValue(t, "extension: duplicate shortcut registration: ctrl+g", func() { ext.RegisterShortcut("Control+G", shortcut) })
	assert.Panics(t, func() { ext.RegisterShortcut("shift+a", shortcut) })
}

func TestProfilesPreserveConfigurationAndRequireHostSupport(t *testing.T) {
	ext := extension.New(extension.Metadata{})
	settings := map[string]any{"max_tokens": 512, "openai": map[string]any{"api_mode": "responses"}}
	name := ext.RegisterProfile(extension.ProfileRegistration{
		Name: "code-search", Provider: "openai", Model: "gpt-5", Hidden: true, Settings: settings,
	})
	assert.Equal(t, "code-search", name)
	settings["max_tokens"] = 1
	h := newHarness(t, ext, nil)

	_, err := h.Initialize(t.Context(), nil)
	require.ErrorContains(t, err, "capabilities.profiles.remote")
	params := extensiontest.DefaultInitializeParams()
	params.Capabilities["profiles"] = map[string]any{"remote": true}
	initialized, err := h.Initialize(t.Context(), &params)
	require.NoError(t, err)
	assert.Equal(t, []extension.ProfileDeclaration{{
		Name:    "code-search",
		Hidden:  true,
		Options: map[string]any{"provider": "openai", "model": "gpt-5", "max_tokens": float64(512), "openai": map[string]any{"api_mode": "responses"}},
	}}, initialized.Profiles)

	for _, invalid := range []extension.ProfileRegistration{
		{Name: "-bad", Provider: "openai", Model: "m"},
		{Name: "ok", Provider: "gemini", Model: "m"},
		{Name: "ok", Provider: "openai", Model: " "},
		{Name: "ok", Provider: "openai", Model: "m", Settings: map[string]any{"model": "other"}},
		{Name: "ok", Provider: "openai", Model: "m", Settings: map[string]any{"bad": func() {}}},
		{Name: "code-search", Provider: "openai", Model: "m"},
	} {
		assert.Panics(t, func() { ext.RegisterProfile(invalid) }, "%+v", invalid)
	}
}

func TestShortcutsNormalizeAndRequireSubmitSupport(t *testing.T) {
	ext := extension.New(extension.Metadata{})
	ext.RegisterShortcut(" Control+Option+K ", extension.Shortcut{
		Description: "Kick off",
		Handler: func(context.Context, *extension.ShortcutContext) (extension.ShortcutResult, error) {
			return extension.Submit("hello"), nil
		},
	})
	ext.RegisterShortcut("f5", extension.Shortcut{Handler: func(context.Context, *extension.ShortcutContext) (extension.ShortcutResult, error) {
		return extension.ShortcutResult{}, nil
	}})
	h := newHarness(t, ext, nil)
	initialized, err := h.Initialize(t.Context(), nil)
	require.NoError(t, err)
	assert.Equal(t, []extension.ShortcutDeclaration{{Key: "ctrl+alt+k", Description: "Kick off"}, {Key: "f5"}}, initialized.Shortcuts)

	result, err := h.ExecuteShortcut(t.Context(), "alt+ctrl+k")
	require.NoError(t, err)
	assert.Equal(t, extension.Submit("hello"), result)
	result, err = h.ExecuteShortcut(t.Context(), "F5")
	require.NoError(t, err)
	assert.Equal(t, extension.ShortcutResult{}, result, "a zero result means no follow-up")

	params := extensiontest.DefaultInitializeParams()
	params.Capabilities = map[string]any{}
	_, err = h.Initialize(t.Context(), &params)
	require.NoError(t, err)
	_, err = h.ExecuteShortcut(t.Context(), "ctrl+alt+k")
	require.ErrorContains(t, err, "shortcut submit results are not supported by this host")
}

func TestTimeoutsPreserveExplicitZeroAndMergeEventOptions(t *testing.T) {
	ext := extension.New(extension.Metadata{})
	extension.RegisterTool(ext, extension.Tool[struct{}]{Name: "slow", Timeout: extension.NoTimeout, Execute: func(context.Context, struct{}, *extension.ToolContext) (extension.ToolResult, error) {
		return extension.ToolResult{}, nil
	}})
	extension.RegisterCommand(ext, extension.Command[map[string]any]{Name: "wait", Timeout: extension.NoTimeout, Execute: func(context.Context, map[string]any, *extension.CommandContext) (extension.CommandResult, error) {
		return extension.Pass(), nil
	}})
	extension.On(ext, extension.EventToolCall, noop[extension.ToolCallEvent], extension.WithTimeout(5*time.Second))
	extension.On(ext, extension.EventToolCall, noop[extension.ToolCallEvent], extension.WithTimeout(10*time.Second), extension.WithPriority(-1))
	extension.On(ext, extension.EventToolUpdate, noop[extension.ToolUpdateEvent], extension.WithTimeout(5*time.Second))
	extension.On(ext, extension.EventToolUpdate, noop[extension.ToolUpdateEvent], extension.WithTimeout(extension.NoTimeout))
	extension.On(ext, extension.EventTurnEnd, noop[extension.TurnEndEvent])
	initialized, err := newHarness(t, ext, nil).Initialize(t.Context(), nil)
	require.NoError(t, err)
	assert.InDelta(t, 0, *initialized.Tools[0].TimeoutInSec, 0)
	assert.InDelta(t, 0, *initialized.Commands[0].TimeoutInSec, 0)
	require.Len(t, initialized.Subscriptions, 3)
	assert.Equal(t, 0, initialized.Subscriptions[0].Priority)
	assert.InDelta(t, 10, *initialized.Subscriptions[0].TimeoutInSec, 0)
	assert.InDelta(t, 0, *initialized.Subscriptions[1].TimeoutInSec, 0)
	assert.Nil(t, initialized.Subscriptions[2].TimeoutInSec)
}
