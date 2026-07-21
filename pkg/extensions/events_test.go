package extensions

import (
	"context"
	"testing"
	"time"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestObservationalEventsAndSubscriptions(t *testing.T) {
	runtime := EmptyRuntime()
	runtime.subs = []Subscription{{Event: EventTurnStart, Priority: 1}}
	runtime.eventHandlersByName[EventAgentStart] = []eventHandler{{sub: Subscription{Event: EventAgentStart}}}
	runtime.eventHandlersByName[EventTurnStart] = []eventHandler{{sub: Subscription{Event: EventTurnStart}}}

	runtime.DispatchAgentStart(context.Background(), ExtensionCallContext{})
	runtime.DispatchTurnStart(context.Background(), ExtensionCallContext{}, 2)
	subs := runtime.Subscriptions()

	require.Len(t, subs, 1)
	assert.Equal(t, EventTurnStart, subs[0].Event)
}

func TestApplySystemPromptPatchVariants(t *testing.T) {
	replacement := "replacement"
	prepend := "pre"
	appendix := "post"

	assert.Equal(t, "base", applySystemPromptPatch("base", nil))
	assert.Equal(t, "pre\nreplacement\npost", applySystemPromptPatch("base", &SystemPromptPatch{Replace: &replacement, Prepend: &prepend, Append: &appendix}))
	assert.Equal(t, "only", joinPromptParts("", "only"))
	assert.Equal(t, "only", joinPromptParts("only", ""))
}

func TestApplyToolListPatchTrimsDeduplicatesAndPreservesOrder(t *testing.T) {
	patched := applyToolListPatch([]string{"bash", "file_read", "grep_tool"}, &ToolListPatch{
		Disable: []string{" bash ", ""},
		Enable:  []string{"get_weather", "file_read", " "},
	})

	assert.Equal(t, []string{"file_read", "grep_tool", "get_weather"}, patched)
}

func TestEventHandlersSortByPriorityThenRegistrationOrder(t *testing.T) {
	runtime := EmptyRuntime()
	runtime.eventHandlersByName[EventToolCall] = []eventHandler{
		{sub: Subscription{Event: EventToolCall, Priority: 1}, order: 2},
		{sub: Subscription{Event: EventToolCall, Priority: 3}, order: 3},
		{sub: Subscription{Event: EventToolCall, Priority: 3}, order: 1},
	}

	handlers := runtime.eventHandlers(EventToolCall)

	require.Len(t, handlers, 3)
	assert.Equal(t, 1, handlers[0].order)
	assert.Equal(t, 3, handlers[1].order)
	assert.Equal(t, 2, handlers[2].order)
}

func TestToolUpdateHandlersExcludeTheProvidingProcess(t *testing.T) {
	provider := &Process{Extension: Extension{ID: "provider"}}
	observer := &Process{Extension: Extension{ID: "observer"}}
	runtime := EmptyRuntime()
	runtime.eventHandlersByName[EventToolUpdate] = []eventHandler{
		{process: provider},
		{process: observer},
	}
	extensionOutput := tooltypes.StructuredToolResult{
		Metadata: &tooltypes.ExtensionToolMetadata{ExtensionID: "provider"},
	}

	handlers := runtime.toolOutputEventHandlers(EventToolUpdate, extensionOutput)

	require.Len(t, handlers, 1)
	assert.Same(t, observer, handlers[0].process)
	require.Len(t, runtime.toolOutputEventHandlers(EventToolResult, extensionOutput), 0)
}

func TestToolUpdateHandlersDoNotInferProviderFromCollidingToolName(t *testing.T) {
	shadowed := &Process{Extension: Extension{ID: "shadowed-bash"}}
	observer := &Process{Extension: Extension{ID: "observer"}}
	runtime := EmptyRuntime()
	runtime.tools["bash"] = &Tool{process: shadowed}
	runtime.eventHandlersByName[EventToolUpdate] = []eventHandler{
		{process: shadowed},
		{process: observer},
	}
	builtinOutput := tooltypes.StructuredToolResult{Metadata: &tooltypes.BashMetadata{}}

	handlers := runtime.toolOutputEventHandlers(EventToolUpdate, builtinOutput)

	require.Len(t, handlers, 2)
	assert.Same(t, shadowed, handlers[0].process)
	assert.Same(t, observer, handlers[1].process)
}

func TestEventTimeoutUsesSpecificAndDefaultTimeouts(t *testing.T) {
	sdkTimeoutInSec := 3.0

	assert.Equal(t, 3*time.Second, eventTimeout(eventHandler{sub: Subscription{TimeoutInSec: &sdkTimeoutInSec}}))
	assert.Equal(t, 30*time.Second, eventTimeout(eventHandler{}))
}

func TestNilRuntimeDispatchersReturnDefaults(t *testing.T) {
	var runtime *Runtime
	toolResult := tooltypes.StructuredToolResult{ToolName: "tool", Success: true}

	assert.Equal(t, UserMessageDecision{Message: "hello"}, runtime.DispatchUserMessage(context.Background(), ExtensionCallContext{}, "hello"))
	assert.Equal(t, AgentInitDecision{SystemPrompt: "base", AllowedTools: []string{"bash"}}, runtime.DispatchAgentInitDecision(context.Background(), ExtensionCallContext{}, "base", []string{"bash"}))
	assert.Equal(t, ToolCallDecision{Input: `{"x":1}`}, runtime.DispatchToolCall(context.Background(), ExtensionCallContext{}, "tool", `{"x":1}`, "call"))
	modifiedResult, changed := runtime.DispatchToolResult(context.Background(), ExtensionCallContext{}, "tool", `{"x":1}`, "call", toolResult)
	assert.False(t, changed)
	assert.Equal(t, toolResult, modifiedResult)
	modifiedUpdate, changed, accepted := runtime.DispatchToolUpdate(context.Background(), ExtensionCallContext{}, "tool", `{"x":1}`, "call", toolResult)
	assert.False(t, changed)
	assert.True(t, accepted)
	assert.Equal(t, toolResult, modifiedUpdate)
}

func TestCanStreamToolUpdatesRequiresMatchingResultExtensionSubscription(t *testing.T) {
	resultProcess := &Process{}
	otherProcess := &Process{}

	runtime := EmptyRuntime()
	runtime.eventHandlersByName[EventToolResult] = []eventHandler{{process: resultProcess}}
	assert.False(t, runtime.CanStreamToolUpdates())

	runtime.eventHandlersByName[EventToolUpdate] = []eventHandler{{process: otherProcess}}
	assert.False(t, runtime.CanStreamToolUpdates())

	runtime.eventHandlersByName[EventToolUpdate] = append(runtime.eventHandlersByName[EventToolUpdate], eventHandler{process: resultProcess})
	assert.True(t, runtime.CanStreamToolUpdates())
}
