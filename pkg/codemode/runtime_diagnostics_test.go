package codemode

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type runtimeTestLargeToolError struct {
	output string
}

func (runtimeTestLargeToolError) Error() string { return "command failed" }

func (e runtimeTestLargeToolError) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{
		"kind":    "tool_error",
		"tool":    "bash",
		"callId":  "child-1",
		"outcome": "completed",
		"message": "command failed",
		"result": map[string]any{
			"data": map[string]any{"output": e.output},
			"text": e.output,
		},
	})
}

func TestRuntimeUncaughtLargeToolErrorKeepsProvenance(t *testing.T) {
	// The reply alone exceeds the 32 KiB selected-output budget.
	output := strings.Repeat("x", 20_000)
	handler := func(context.Context, Request) (any, error) {
		return nil, runtimeTestLargeToolError{output: output}
	}
	_, err := Execute(t.Context(), `await tools.bash({command: "make"});`, handler)
	var failure *Error
	require.ErrorAs(t, err, &failure)
	assert.Equal(t, "tool_error", failure.Kind)
	assert.Equal(t, "bash", failure.Tool)
	assert.Equal(t, "child-1", failure.CallID)
	assert.Equal(t, "completed", failure.Outcome)
	assert.Equal(t, "command failed", failure.Message)
	assert.Empty(t, failure.Result, "uncaught diagnostics never carry the child reply")
	assert.Equal(t, "tool bash failed (tool_error, outcome completed): command failed", err.Error())

	result, err := Execute(t.Context(), `
try {
  await tools.bash({command: "make"});
} catch (e) {
  return {text: e.result.text.length, data: e.result.data.output.length};
}
`, handler)
	require.NoError(t, err)
	require.Len(t, result.Outputs, 1)
	assert.JSONEq(t, `{"text":20000,"data":20000}`, string(result.Outputs[0].Value))
}

func TestRuntimeFailureFormattingCannotStartHostWork(t *testing.T) {
	for name, code := range map[string]string{
		"rejected toString": `
Promise.reject({toString() { tools.side_effect({}); return "fake"; }});
await new Promise(() => {});
`,
		"message getter": `
const error = new Error("real");
Object.defineProperty(error, "message", {get() { tools.side_effect({}); return "fake"; }});
throw error;
`,
		"stack getter": `
class Sneaky extends Error {
  get stack() { tools.side_effect({}); return "fake stack"; }
}
throw new Sneaky("sneaky");
`,
		"proxy trap": `
throw new Proxy(new Error("proxied"), {
  getOwnPropertyDescriptor(target, key) {
    tools.side_effect({});
    emit("late output");
    return Reflect.getOwnPropertyDescriptor(target, key);
  },
});
`,
		"unhandled proxy": `
Promise.reject(new Proxy({}, {getPrototypeOf() { tools.side_effect({}); return null; }}));
return 1;
`,
	} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			result, err := Execute(t.Context(), code, func(context.Context, Request) (any, error) {
				calls.Add(1)
				return true, nil
			})
			require.Error(t, err)
			assert.Zero(t, calls.Load(), "failure formatting must not dispatch host work")
			for _, output := range result.Outputs {
				assert.NotContains(t, string(output.Value), "late output")
			}
			assert.NotContains(t, err.Error(), "fake")
		})
	}
}

func TestRuntimeFailureDiagnosticsAreBounded(t *testing.T) {
	// Control characters are escaped as six JSON bytes each, the worst case.
	_, err := Execute(t.Context(), `
const error = new Error("\u0001".repeat(20000));
Object.defineProperty(error, "stack", {value: "\u0002".repeat(20000)});
error.kind = "k".repeat(1000);
error.tool = "\u0003".repeat(1000);
error.callId = "\u0004".repeat(1000);
error.outcome = "\u0005".repeat(1000);
throw error;
`, nil)
	var failure *Error
	require.ErrorAs(t, err, &failure)
	marker := "... [truncated]"
	assert.Equal(t, strings.Repeat("\x01", 4096)+marker+"\n"+strings.Repeat("\x02", 2048)+marker, failure.Message)
	assert.Equal(t, strings.Repeat("k", 256)+marker, failure.Kind)
	assert.Equal(t, strings.Repeat("\x03", 256)+marker, failure.Tool)
	assert.Equal(t, strings.Repeat("\x04", 256)+marker, failure.CallID)
	assert.Equal(t, strings.Repeat("\x05", 256)+marker, failure.Outcome)
}

func TestRuntimeUsesHostClockAndRandomness(t *testing.T) {
	before := time.Now().UnixMilli()
	result, err := Execute(t.Context(), `return {now: Date.now(), random: Math.random()};`, nil)
	after := time.Now().UnixMilli()
	require.NoError(t, err)
	require.Len(t, result.Outputs, 1)
	var first struct {
		Now    int64   `json:"now"`
		Random float64 `json:"random"`
	}
	require.NoError(t, json.Unmarshal(result.Outputs[0].Value, &first))
	assert.GreaterOrEqual(t, first.Now, before-1000)
	assert.LessOrEqual(t, first.Now, after+1000)

	result, err = Execute(t.Context(), `return Math.random();`, nil)
	require.NoError(t, err)
	var second float64
	require.NoError(t, json.Unmarshal(result.Outputs[0].Value, &second))
	assert.NotEqual(t, first.Random, second, "each invocation needs a fresh random seed")
}

func TestRuntimeOmitsUndefinedLikeJSON(t *testing.T) {
	var input json.RawMessage
	result, err := Execute(t.Context(), `
await tools.echo({a: undefined, b: 1});
emit({a: undefined});
console.log(undefined, "logged");
return {a: 1, b: undefined, list: [undefined, 2]};
`, func(_ context.Context, request Request) (any, error) {
		input = request.Input
		return true, nil
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"b":1}`, string(input))
	require.Len(t, result.Outputs, 3)
	assert.JSONEq(t, `{}`, string(result.Outputs[0].Value))
	assert.JSONEq(t, `"undefined logged"`, string(result.Outputs[1].Value))
	assert.JSONEq(t, `{"a":1,"list":[null,2]}`, string(result.Outputs[2].Value))

	for name, code := range map[string]string{
		"root emit":      `emit(undefined);`,
		"root toJSON":    `emit({toJSON() { return undefined; }});`,
		"function field": `emit({f() {}});`,
		"symbol field":   `emit({s: Symbol("x")});`,
		"bigint input":   `await tools.echo({n: 1n});`,
	} {
		t.Run(name, func(t *testing.T) {
			var called atomic.Bool
			_, err := Execute(t.Context(), code, func(context.Context, Request) (any, error) {
				called.Store(true)
				return true, nil
			})
			require.ErrorContains(t, err, "Only JSON values can cross the host bridge")
			assert.False(t, called.Load())
		})
	}
}

func TestRuntimeCatalogHasDedicatedResponseSlot(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	catalogStarted := make(chan struct{})
	bridge := newRuntimeBridge(ctx, func(_ context.Context, request Request) (any, error) {
		if request.Operation != "tool.call" {
			close(catalogStarted)
		}
		return true, nil
	}, nil, defaultRuntimeLimits(), &Result{})
	bridge.startWorkers()
	// Nothing consumes completions, so finished tool work keeps every tool
	// response reservation occupied while more tool requests wait for one.
	requests := 2 * MaxConcurrentToolCalls
	for id := 1; id <= requests; id++ {
		require.NoError(t, bridge.submit([]byte(fmt.Sprintf(
			`{"id":%d,"request":{"operation":"tool.call","name":"echo","input":{}}}`, id,
		))))
	}
	require.Eventually(t, func() bool {
		return len(bridge.toolSlots) == cap(bridge.toolSlots)
	}, 2*time.Second, time.Millisecond)
	require.NoError(t, bridge.submit([]byte(fmt.Sprintf(
		`{"id":%d,"request":{"operation":"catalog.list"}}`, requests+1,
	))))
	select {
	case <-catalogStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("catalog work waited for tool responses to be consumed")
	}
}

func TestRuntimeReportsEarliestUnhandledRejection(t *testing.T) {
	// Map iteration order is randomized; repeat to catch nondeterminism.
	for range 20 {
		_, err := Execute(t.Context(), `
for (let i = 0; i < 8; i++) Promise.reject(new Error("rejection " + i));
return 1;
`, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "rejection 0")
	}
}

func TestRuntimeOversizedToolInputKeepsProvenance(t *testing.T) {
	var called atomic.Bool
	result, err := Execute(t.Context(), `
try {
  await tools.echo({blob: "x".repeat(2 << 20)});
} catch (e) {
  return {kind: e.kind, tool: e.tool, outcome: e.outcome, message: e.message};
}
`, func(context.Context, Request) (any, error) {
		called.Store(true)
		return true, nil
	})
	require.NoError(t, err)
	assert.False(t, called.Load())
	require.Len(t, result.Outputs, 1)
	assert.JSONEq(t, `{
  "kind": "limit",
  "tool": "echo",
  "outcome": "not_started",
  "message": "host request exceeds the byte limit"
}`, string(result.Outputs[0].Value))
}
