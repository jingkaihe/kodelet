package extensions

import (
	"context"
	"testing"
	"time"

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

func TestEventTimeoutUsesSpecificAndDefaultTimeouts(t *testing.T) {
	runtime := EmptyRuntime()
	runtime.config.Timeout = 11 * time.Second
	runtime.config.Events = map[string]EventConfig{EventToolCall: {Timeout: 2 * time.Second}}

	assert.Equal(t, 2*time.Second, runtime.eventTimeout(EventToolCall))
	assert.Equal(t, 11*time.Second, runtime.eventTimeout(EventToolResult))
}

func TestNilRuntimeDispatchersReturnDefaults(t *testing.T) {
	var runtime *Runtime

	assert.Equal(t, UserMessageDecision{Message: "hello"}, runtime.DispatchUserMessage(context.Background(), ExtensionCallContext{}, "hello"))
	assert.Equal(t, AgentInitDecision{SystemPrompt: "base", AllowedTools: []string{"bash"}}, runtime.DispatchAgentInitDecision(context.Background(), ExtensionCallContext{}, "base", []string{"bash"}))
	assert.Equal(t, ToolCallDecision{Input: `{"x":1}`}, runtime.DispatchToolCall(context.Background(), ExtensionCallContext{}, "tool", `{"x":1}`, "call"))
}
