package extensions

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/telemetry"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
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

func TestEventTracingSkipsNoopDispatch(t *testing.T) {
	recorder := recordExtensionEventSpans(t, false)
	t.Run("nil runtime", func(t *testing.T) {
		var runtime *Runtime
		runtime.DispatchAgentStart(t.Context(), ExtensionCallContext{})
		assert.Empty(t, recorder.Ended())
	})
	t.Run("no subscribers", func(t *testing.T) {
		runtime := EmptyRuntime()
		runtime.DispatchAgentStart(t.Context(), ExtensionCallContext{})
		assert.Empty(t, recorder.Ended())
	})
	t.Run("nil handler", func(t *testing.T) {
		runtime := EmptyRuntime()
		runtime.eventHandlersByName[EventAgentStart] = []eventHandler{{}}
		runtime.DispatchAgentStart(t.Context(), ExtensionCallContext{})
		assert.Empty(t, recorder.Ended())
	})
	t.Run("session end does not restart stopped handler", func(t *testing.T) {
		process := &Process{Extension: Extension{ID: "stopped"}, closed: true}
		runtime := EmptyRuntime()
		runtime.eventHandlersByName[EventSessionEnd] = []eventHandler{{process: process}}
		runtime.DispatchSessionEnd(t.Context(), ExtensionCallContext{})
		assert.Empty(t, recorder.Ended())
		assert.Zero(t, process.failures)
		assert.Nil(t, process.cmd)
	})
}

func TestEventTracingHandlerSetupFailures(t *testing.T) {
	for _, name := range []string{"disconnected session", "failed restart", "missing rpc session", "startup cancellation", "startup deadline"} {
		t.Run(name, func(t *testing.T) {
			recorder := recordExtensionEventSpans(t, false)
			ctx, parent := telemetry.Tracer("test").Start(t.Context(), "invoke_agent kodelet")
			defer parent.End()
			process := &Process{Extension: Extension{ID: "unavailable"}}
			errorType := ""
			switch name {
			case "disconnected session":
				process, _, _ = newEventTraceProcess(t)
				process.failClientGeneration(process.client)
			case "failed restart":
				process.closed = true
				process.Extension.ExecPath = filepath.Join(t.TempDir(), "private-missing-executable")
			case "startup cancellation", "startup deadline":
				process.closed = true
				completionErr := context.Canceled
				errorType = "cancelled"
				if name == "startup deadline" {
					completionErr = context.DeadlineExceeded
					errorType = "timeout"
				}
				done := make(chan struct{})
				close(done)
				ctx = completedCallContext{Context: ctx, done: done, err: completionErr}
			}
			runtime := EmptyRuntime()
			runtime.eventHandlersByName[EventAgentStart] = []eventHandler{{process: process}}
			// Observational dispatch logs and swallows handler errors. The failed
			// attempt must remain visible even though the lifecycle RPC succeeds.
			runtime.DispatchAgentStart(ctx, ExtensionCallContext{})
			spans := recorder.Ended()
			require.Len(t, spans, 1)
			assert.Equal(t, "extension "+process.Extension.ID+" agent.start", spans[0].Name())
			assert.Equal(t, parent.SpanContext().SpanID(), spans[0].Parent().SpanID())
			assert.Equal(t, codes.Error, spans[0].Status().Code)
			assert.Empty(t, spans[0].Events())
			assert.NotContains(t, fmt.Sprint(spans[0].Attributes(), spans[0].Status()), "private")
			if errorType != "" {
				assert.Contains(t, spans[0].Attributes(), attribute.String("error.type", errorType))
				assert.Nil(t, process.cmd, "cancelled startup must not launch a process")
			}
		})
	}
}

func TestEventTracingHandlerParentageAndReverseCalls(t *testing.T) {
	recorder := recordExtensionEventSpans(t, false)
	process, sdk, reader := newEventTraceProcess(t)
	runtime := EmptyRuntime()
	runtime.eventHandlersByName[EventUserMessage] = []eventHandler{{process: process}}
	ctx, parent := telemetry.Tracer("test").Start(t.Context(), "invoke_agent kodelet")
	ctx = ContextWithUIInputBroker(ctx, eventTraceInputBroker{})
	completed := make(chan UserMessageDecision, 1)
	go func() {
		completed <- runtime.DispatchUserMessage(ctx, ExtensionCallContext{CWD: "/private/workspace"}, "private prompt")
	}()
	request, err := readIncomingMessage(reader)
	require.NoError(t, err)
	assert.Equal(t, "extension.event.handle", request.Method)
	sendEventTraceMessage(t, sdk, map[string]any{
		"jsonrpc":  "2.0",
		"id":       100,
		"parentId": request.ID,
		"method":   "kodelet.ui.input",
		"params":   UIInputRequest{Title: "private question"},
	})
	response, err := readIncomingMessage(reader)
	require.NoError(t, err)
	require.Nil(t, response.Error)
	sendEventTraceMessage(t, sdk, map[string]any{
		"jsonrpc": "2.0",
		"id":      request.ID,
		"result":  map[string]any{"message": "private replacement"},
	})
	assert.Equal(t, UserMessageDecision{Message: "private replacement"}, <-completed)
	parent.End()

	spans := recorder.Ended()
	require.Len(t, spans, 3)
	assert.Equal(t, "reverse request", spans[0].Name())
	handler := spans[1]
	assert.Equal(t, "extension session:traced user.message", handler.Name())
	assert.Equal(t, trace.SpanKindInternal, handler.SpanKind())
	assert.Equal(t, parent.SpanContext().SpanID(), handler.Parent().SpanID())
	assert.Equal(t, parent.SpanContext().TraceID(), handler.SpanContext().TraceID())
	assert.Equal(t, handler.SpanContext().SpanID(), spans[0].Parent().SpanID())
	assert.Equal(t, handler.SpanContext().TraceID(), spans[0].SpanContext().TraceID())
	assert.ElementsMatch(t, []attribute.KeyValue{
		attribute.String("kodelet.extension.id", "session:traced"),
		attribute.String("kodelet.extension.event", EventUserMessage),
	}, handler.Attributes())
	assert.NotEqual(t, codes.Error, handler.Status().Code)
	assert.Empty(t, handler.Events())
	assert.NotContains(t, fmt.Sprint(handler.Attributes(), handler.Events(), handler.Status()), "private")
}

func TestEventTracingFailuresPreservePolicyAndPrivacy(t *testing.T) {
	for _, test := range []struct {
		name           string
		event          string
		result         any
		rpcErr         *rpcError
		captureContent bool
	}{
		{
			name:   "rpc error",
			event:  EventToolUpdate,
			rpcErr: &rpcError{Code: -32000, Message: "private failure"},
		},
		{
			name:           "rpc error with content opt-in",
			event:          EventToolUpdate,
			rpcErr:         &rpcError{Code: -32000, Message: "private failure"},
			captureContent: true,
		},
		{
			name:   "invalid response",
			event:  EventToolUpdate,
			result: "private invalid response",
		},
		{
			name:   "invalid update output fails closed",
			event:  EventToolUpdate,
			result: EventResult{Output: json.RawMessage(`"private invalid output"`)},
		},
		{
			name:   "invalid result output remains best effort",
			event:  EventToolResult,
			result: EventResult{Output: json.RawMessage(`"private invalid output"`)},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := recordExtensionEventSpans(t, test.captureContent)
			process, sdk, reader := newEventTraceProcess(t)
			runtime := EmptyRuntime()
			runtime.eventHandlersByName[test.event] = []eventHandler{{process: process}}
			original := tooltypes.StructuredToolResult{ToolName: "bash", Success: true}
			completed := make(chan bool, 1)
			go func() {
				output, modified, accepted := runtime.dispatchToolOutput(
					t.Context(), test.event, ExtensionCallContext{},
					"bash", `{"command":"private input"}`, "call-1",
					original, test.event == EventToolUpdate,
				)
				assert.Equal(t, original, output)
				assert.False(t, modified)
				completed <- accepted
			}()
			request, err := readIncomingMessage(reader)
			require.NoError(t, err)
			sendEventTraceMessage(t, sdk, map[string]any{
				"jsonrpc": "2.0",
				"id":      request.ID,
				"result":  test.result,
				"error":   test.rpcErr,
			})
			assert.Equal(t, test.event == EventToolResult, <-completed)
			spans := recorder.Ended()
			require.Len(t, spans, 1)
			assert.Equal(t, "extension session:traced "+test.event, spans[0].Name())
			assert.Equal(t, codes.Error, spans[0].Status().Code)
			if test.captureContent {
				assert.Contains(t, spans[0].Status().Description, "private failure")
				assert.NotEmpty(t, spans[0].Events())
			} else {
				assert.Empty(t, spans[0].Events())
				assert.NotContains(t, fmt.Sprint(spans[0].Attributes(), spans[0].Status()), "private")
			}
		})
	}
}

func TestEventTracingCancellation(t *testing.T) {
	for _, completionErr := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(completionErr.Error(), func(t *testing.T) {
			recorder := recordExtensionEventSpans(t, false)
			process, _, reader := newEventTraceProcess(t)
			done := make(chan struct{})
			ctx := completedCallContext{Context: t.Context(), done: done, err: completionErr}
			completed := make(chan error, 1)
			go func() {
				_, err := process.HandleEvent(ctx, "event-1", EventAgentStart, agentStartPayload{}, ExtensionCallContext{})
				completed <- err
			}()
			request, err := readIncomingMessage(reader)
			require.NoError(t, err)
			assert.Equal(t, "extension.event.handle", request.Method)
			close(done)
			notification, err := readIncomingMessage(reader)
			require.NoError(t, err)
			assert.Equal(t, "$/cancelRequest", notification.Method)
			require.ErrorIs(t, <-completed, completionErr)
			spans := recorder.Ended()
			require.Len(t, spans, 1)
			assert.Equal(t, codes.Error, spans[0].Status().Code)
			errorType := "cancelled"
			if completionErr == context.DeadlineExceeded {
				errorType = "timeout"
			}
			assert.Contains(t, spans[0].Attributes(), attribute.String("error.type", errorType))
			assert.Empty(t, spans[0].Events())
		})
	}
}

type eventTraceInputBroker struct{}

func (eventTraceInputBroker) Input(ctx context.Context, _ UIInputRequest) (UIInputResponse, error) {
	_, span := telemetry.Tracer("test").Start(ctx, "reverse request")
	defer span.End()
	return UIInputResponse{Status: UIInputStatusSubmitted, Value: "private answer"}, nil
}

func recordExtensionEventSpans(t *testing.T, captureContent bool) *tracetest.SpanRecorder {
	t.Helper()
	previousContent := telemetry.ContentEnabled()
	previousInternalRPCSpans := telemetry.InternalRPCSpansEnabled()
	_, err := telemetry.InitTracer(t.Context(), telemetry.Config{
		CaptureContent:   captureContent,
		InternalRPCSpans: previousInternalRPCSpans,
	})
	require.NoError(t, err)
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previousProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		require.NoError(t, provider.Shutdown(context.Background()))
		otel.SetTracerProvider(previousProvider)
		_, err := telemetry.InitTracer(context.Background(), telemetry.Config{
			CaptureContent:   previousContent,
			InternalRPCSpans: previousInternalRPCSpans,
		})
		require.NoError(t, err)
	})
	return recorder
}

func newEventTraceProcess(t *testing.T) (*Process, net.Conn, *bufio.Reader) {
	t.Helper()
	local, sdk := net.Pipe()
	t.Cleanup(func() { _ = sdk.Close() })
	require.NoError(t, sdk.SetDeadline(time.Now().Add(5*time.Second)))
	process, err := AttachProcess(
		t.Context(), Extension{ID: "session:traced", Kind: SourceKindSession},
		DefaultConfig(), t.TempDir(), local,
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, process.Close()) })
	return process, sdk, bufio.NewReader(sdk)
}

func sendEventTraceMessage(t *testing.T, sdk net.Conn, message any) {
	t.Helper()
	payload, err := json.Marshal(message)
	require.NoError(t, err)
	require.NoError(t, writeFrame(sdk, payload))
}
