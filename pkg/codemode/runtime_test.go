package codemode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuntimeArtifactIntegrity(t *testing.T) {
	digest := sha256.Sum256(runtimeWASM)
	assert.Equal(t, "d4c9375f2b1ca4dc95f72c8aa2982a7a9951ac8011490d79c6582df732b4bbd9", hex.EncodeToString(digest[:]))
}

func TestRuntimeReturnAndEmit(t *testing.T) {
	result, err := Execute(context.Background(), `
emit({started: true});
console.log("answer", 42);
return await Promise.resolve({answer: 42});
`, nil)
	require.NoError(t, err)
	require.Len(t, result.Outputs, 3)
	for _, item := range result.Outputs {
		assert.Equal(t, "json", item.Type)
	}
	assert.JSONEq(t, `{"started":true}`, string(result.Outputs[0].Value))
	assert.JSONEq(t, `"answer 42"`, string(result.Outputs[1].Value))
	assert.JSONEq(t, `{"answer":42}`, string(result.Outputs[2].Value))
}

func TestRuntimeTypedEmissions(t *testing.T) {
	var validated []OutputItem
	result, err := ExecuteWithOutputValidator(t.Context(), `
emit({type: "image", artifactId: "fake"});
const ref = await tools.image({});
if (emit.image(ref, {detail: "original"}) !== undefined) throw new Error("emit.image is not synchronous");
console.log("selected");
if (emit.artifact(ref.artifactId) !== undefined) throw new Error("emit.artifact is not synchronous");
emit.image(ref.artifactId, {});
emit.artifact({...ref, type: "json"});
return {type: "artifact", artifactId: "fake"};
`, func(_ context.Context, request Request) (any, error) {
		assert.Equal(t, "image", request.Name)
		return map[string]any{
			"artifactId": "art_image", "type": "image", "filename": "test.png", "size": 1 << 40,
		}, nil
	}, func(item OutputItem) error {
		validated = append(validated, item)
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, result.Outputs, validated, "every output is validated in emission order")
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	assert.JSONEq(t, `{"outputs":[
  {"type":"json","value":{"type":"image","artifactId":"fake"}},
  {"type":"image","artifactId":"art_image","detail":"original"},
  {"type":"json","value":"selected"},
  {"type":"artifact","artifactId":"art_image"},
  {"type":"image","artifactId":"art_image"},
  {"type":"artifact","artifactId":"art_image"},
  {"type":"json","value":{"type":"artifact","artifactId":"fake"}}
]}`, string(encoded))
}

func TestRuntimeMalformedMediaIsImmediatelyCatchable(t *testing.T) {
	for name, code := range map[string]string{
		"missing":              `emit.image()`,
		"extra image argument": `emit.image("art_image", {}, true)`,
		"artifact options":     `emit.artifact("art_image", {})`,
		"null reference":       `emit.image(null)`,
		"empty ID":             `emit.image("")`,
		"missing ID":           `emit.artifact({id: "art_image"})`,
		"non-string ID":        `emit.image({artifactId: 1})`,
		"path":                 `emit.image("/tmp/image.png")`,
		"base64":               `emit.image("YWJjZA==")`,
		"disguised promise": `
emit.image(Object.assign(Promise.resolve("art_image"), {
  artifactId: "art_image",
  toJSON: () => ({artifactId: "art_image"})
}))`,
		"thenable":          `emit.image({artifactId: "art_image", then() {}})`,
		"null options":      `emit.image("art_image", null)`,
		"array options":     `emit.image("art_image", [])`,
		"non-plain options": `emit.image("art_image", new Date())`,
		"unknown option":    `emit.image("art_image", {quality: "high"})`,
		"symbol option":     `emit.image("art_image", {[Symbol()]: true})`,
		"toJSON options":    `emit.image("art_image", {quality: "high", toJSON: () => ({detail: "original"})})`,
		"invalid detail":    `emit.image("art_image", {detail: "high"})`,
		"null detail":       `emit.image("art_image", {detail: null})`,
	} {
		t.Run(name, func(t *testing.T) {
			var validated []OutputItem
			result, err := ExecuteWithOutputValidator(t.Context(), `
let caught = false;
try { `+code+`; } catch (_) { caught = true; }
return caught;
`, nil, func(item OutputItem) error {
				validated = append(validated, item)
				return nil
			})
			require.NoError(t, err)
			want := []OutputItem{{Type: "json", Value: json.RawMessage(`true`)}}
			assert.Equal(t, want, result.Outputs)
			assert.Equal(t, want, validated, "invalid media must not reach authorization")
		})
	}
}

func TestRuntimeMediaAuthorization(t *testing.T) {
	validator := func(item OutputItem) error {
		if item.Type != "json" && item.ArtifactID != "art_allowed" {
			return &Error{Kind: "blocked", Message: "artifact is not authorized"}
		}
		return nil
	}
	result, err := ExecuteWithOutputValidator(t.Context(), `
try { emit.image("art_denied"); } catch (error) { emit({kind: error.kind, message: error.message}); }
emit.artifact("art_allowed");
emit.image("art_denied");
`, nil, validator)
	require.ErrorContains(t, err, "artifact is not authorized")
	require.Len(t, result.Outputs, 2, "uncaught denial preserves earlier outputs")
	assert.Equal(t, "json", result.Outputs[0].Type)
	assert.JSONEq(t, `{"kind":"blocked","message":"artifact is not authorized"}`, string(result.Outputs[0].Value))
	assert.Equal(t, OutputItem{Type: "artifact", ArtifactID: "art_allowed"}, result.Outputs[1])

	for _, operation := range []string{"image", "artifact"} {
		t.Run("no validator "+operation, func(t *testing.T) {
			result, err := Execute(t.Context(), `
emit({type: "image", artifactId: "art_allowed"});
emit.`+operation+`("art_allowed");
`, nil)
			require.ErrorContains(t, err, "requires an output validator")
			var failure *Error
			require.ErrorAs(t, err, &failure)
			assert.Equal(t, "blocked", failure.Kind)
			require.Len(t, result.Outputs, 1)
			assert.Equal(t, "json", result.Outputs[0].Type, "fake media JSON does not need authority")
		})
	}
}

func TestRuntimeTypedOutputLimits(t *testing.T) {
	for _, test := range []struct {
		name  string
		code  string
		limit int
	}{
		{
			name: "media including repeats",
			code: `
emit(0);
for (let i = 0; i < 8; i++) emit[i % 2 ? "image" : "artifact"]("art_same");
emit.image("art_same");
`,
			limit: 9,
		},
		{
			name: "overall including media",
			code: `
emit.image("art_same");
for (let i = 0; i < 1024; i++) emit(0);
`,
			limit: 1024,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls int
			result, err := ExecuteWithOutputValidator(t.Context(), test.code, nil, func(OutputItem) error {
				calls++
				return nil
			})
			var failure *Error
			require.ErrorAs(t, err, &failure)
			assert.Equal(t, "limit", failure.Kind)
			assert.Len(t, result.Outputs, test.limit)
			assert.Equal(t, test.limit, calls, "over-budget outputs must not reach authorization")
		})
	}

	t.Run("descriptor bytes and validation before append", func(t *testing.T) {
		item := OutputItem{Type: "image", ArtifactID: "art_image", Detail: "original"}
		descriptor, err := json.Marshal(item)
		require.NoError(t, err)
		limits := defaultRuntimeLimits()
		limits.outputBytes = len(descriptor) + 1
		var result Result
		var calls int
		bridge := newRuntimeBridge(t.Context(), nil, func(OutputItem) error {
			assert.Len(t, result.Outputs, calls, "authorization must happen before append")
			calls++
			return nil
		}, limits, &result)
		require.NoError(t, bridge.emit(json.RawMessage(`0`)))
		require.NoError(t, bridge.emitMedia("image", json.RawMessage(`{"artifactId":"art_image","detail":"original"}`)))
		assert.Equal(t, limits.outputBytes, bridge.outputBytes)
		require.ErrorContains(t, bridge.emitMedia("artifact", json.RawMessage(`{"artifactId":"art_image"}`)), "limit")
		require.ErrorContains(t, bridge.emit(json.RawMessage(`0`)), "limit")
		assert.Equal(t, []OutputItem{{Type: "json", Value: json.RawMessage(`0`)}, item}, result.Outputs)
		assert.Equal(t, 2, calls)
	})
}

func TestRuntimeMediaBridgeValidation(t *testing.T) {
	for _, payload := range []string{
		`null`, `{`, `[]`, `{} {}`, `{"artifactId":"art_image"} {}`,
		`{"artifactId":"art_image","type":"json"}`,
		`{"artifactId":"art_image","value":"fake"}`,
		`{"artifactId":"art_image","detail":null}`,
		`{"artifactId":"art_image","detail":""}`,
		`{"artifactId":"art_image","detail":"high"}`,
	} {
		var result Result
		bridge := newRuntimeBridge(t.Context(), nil, func(OutputItem) error {
			t.Error("malformed descriptor reached authorization")
			return nil
		}, defaultRuntimeLimits(), &result)
		require.Error(t, bridge.emitMedia("image", json.RawMessage(payload)), payload)
		assert.Empty(t, result.Outputs)
	}
	bridge := newRuntimeBridge(t.Context(), nil, nil, defaultRuntimeLimits(), &Result{})
	require.ErrorContains(t, bridge.emitMedia("artifact", json.RawMessage(`{"artifactId":"art_image","detail":"original"}`)), "do not accept options")
}

func TestRuntimeAsyncHostCalls(t *testing.T) {
	result, err := Execute(context.Background(), `
const pages = await Promise.all([catalog.search("one"), catalog.list(), catalog.describe("bash")]);
const response = await tools.bash({command:"git status"});
return {pages, response};
`, func(_ context.Context, request Request) (any, error) {
		if request.Operation == "tool.call" {
			assert.Equal(t, "bash", request.Name)
			assert.JSONEq(t, `{"command":"git status"}`, string(request.Input))
		}
		return map[string]any{"operation": request.Operation}, nil
	})
	require.NoError(t, err)
	require.Len(t, result.Outputs, 1)
	var output map[string]any
	require.NoError(t, json.Unmarshal(result.Outputs[0].Value, &output))
	assert.Len(t, output["pages"], 3)
	assert.Equal(t, map[string]any{"operation": "tool.call"}, output["response"])
}

func TestRuntimeExactToolNames(t *testing.T) {
	names := []string{"then", "toString", "constructor", "__proto__", "hyphen-name"}
	var mu sync.Mutex
	var called []string
	result, err := Execute(t.Context(), `
return await Promise.all(["then", "toString", "constructor", "__proto__", "hyphen-name"].map(
  name => tools[name]({name})
));
`, func(_ context.Context, request Request) (any, error) {
		assert.Equal(t, "tool.call", request.Operation)
		var input struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(request.Input, &input); err != nil {
			return nil, err
		}
		assert.Equal(t, request.Name, input.Name)
		mu.Lock()
		called = append(called, request.Name)
		mu.Unlock()
		return request.Name, nil
	})
	require.NoError(t, err)
	require.Len(t, result.Outputs, 1)
	assert.JSONEq(t, `["then","toString","constructor","__proto__","hyphen-name"]`, string(result.Outputs[0].Value))
	assert.ElementsMatch(t, names, called)
}

func TestRuntimeToolsNamespaceIsNotAPromise(t *testing.T) {
	for _, code := range []string{`await tools;`, `return tools;`, `return Promise.resolve(tools);`} {
		t.Run(code, func(t *testing.T) {
			var called atomic.Bool
			result, err := Execute(t.Context(), code, func(context.Context, Request) (any, error) {
				called.Store(true)
				return "unexpected host call", nil
			})
			require.ErrorContains(t, err, "the tools namespace is not a promise")
			var failure *Error
			require.ErrorAs(t, err, &failure)
			assert.Equal(t, "invalid_input", failure.Kind)
			assert.Equal(t, "then", failure.Tool)
			assert.Equal(t, "not_started", failure.Outcome)
			assert.Empty(t, result.Outputs)
			assert.False(t, called.Load(), "Promise assimilation must not dispatch a tool")
		})
	}
}

type runtimeTestHostError struct{}

func (runtimeTestHostError) Error() string { return "not permitted" }

func (runtimeTestHostError) MarshalJSON() ([]byte, error) {
	return []byte(`{"kind":"blocked","tool":"delete","callId":"child-7","outcome":"not_started","message":"not permitted","result":{"text":"redacted"}}`), nil
}

func TestRuntimePreservesHostErrors(t *testing.T) {
	result, err := Execute(context.Background(), `
return await Promise.allSettled([tools.delete({})]);
`, func(context.Context, Request) (any, error) {
		return nil, errors.Wrap(runtimeTestHostError{}, "host context")
	})
	require.NoError(t, err)
	require.Len(t, result.Outputs, 1)
	assert.JSONEq(t, `[{
"status":"rejected",
"reason":{"kind":"blocked","tool":"delete","callId":"child-7","outcome":"not_started","message":"not permitted","result":{"text":"redacted"}}
}]`, string(result.Outputs[0].Value))

	_, err = Execute(context.Background(), `return await tools.delete({});`, func(context.Context, Request) (any, error) {
		return nil, runtimeTestHostError{}
	})
	var failure *Error
	require.ErrorAs(t, err, &failure)
	assert.Equal(t, "blocked", failure.Kind)
	assert.Equal(t, "child-7", failure.CallID)
	assert.Equal(t, "not_started", failure.Outcome)
	assert.Equal(t, "not permitted", failure.Message)
	assert.Equal(t, "tool delete failed (blocked, outcome not_started): not permitted", err.Error())
}

func TestRuntimeParallelToolsAndIndependentCatalog(t *testing.T) {
	var active atomic.Int32
	var maxActive atomic.Int32
	var entered atomic.Int32
	release := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := Execute(ctx, `
const jobs = Array.from({length: 16}, (_, i) => tools.echo({i}));
const discovery = await catalog.search("release tools");
return {discovery, values: await Promise.all(jobs)};
`, func(ctx context.Context, request Request) (any, error) {
		if request.Operation == "catalog.search" {
			// Catalog must still run with all eight tool slots occupied.
			for entered.Load() < 8 {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(time.Millisecond):
				}
			}
			close(release)
			return "ready", nil
		}
		count := active.Add(1)
		defer active.Add(-1)
		for previous := maxActive.Load(); count > previous; previous = maxActive.Load() {
			if maxActive.CompareAndSwap(previous, count) {
				break
			}
		}
		entered.Add(1)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
		}
		var input struct {
			I int `json:"i"`
		}
		if err := json.Unmarshal(request.Input, &input); err != nil {
			return nil, err
		}
		return input.I, nil
	})
	require.NoError(t, err)
	assert.Equal(t, int32(8), maxActive.Load())
	require.Len(t, result.Outputs, 1)
	assert.JSONEq(t, `{
  "discovery": "ready",
  "values": [0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15]
}`, string(result.Outputs[0].Value))
}

func TestRuntimeOutOfOrderCompletionAndCopiedInputs(t *testing.T) {
	secondDone := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := Execute(ctx, `
const input = {value:"original"};
const first = tools.first(input);
input.value = "changed";
return await Promise.all([first, tools.second({})]);
`, func(ctx context.Context, request Request) (any, error) {
		if request.Name == "second" {
			close(secondDone)
			return "second", nil
		}
		select {
		case <-secondDone:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return request.Input, nil
	})
	require.NoError(t, err)
	assert.JSONEq(t, `[{"value":"original"},"second"]`, string(result.Outputs[0].Value))
}

func TestRuntimeCancellation(t *testing.T) {
	// Warm the immutable compilation cache so these deadlines exercise the VM,
	// not its first compilation on a slow CI machine.
	_, err := Execute(context.Background(), `return 1;`, nil)
	require.NoError(t, err)
	for name, code := range map[string]string{
		"CPU":        `while (true) {}`,
		"microtasks": `await new Promise(() => { const spin = () => Promise.resolve().then(spin); spin(); });`,
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			started := time.Now()
			_, err := Execute(ctx, code, nil)
			require.ErrorIs(t, err, context.DeadlineExceeded)
			assert.Less(t, time.Since(started), 2*time.Second)
		})
	}
	t.Run("already cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := Execute(ctx, `return 1`, nil)
		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestRuntimeDoesNotWaitForUncooperativeHandler(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := Execute(ctx, `return await tools.wait({});`, func(context.Context, Request) (any, error) {
			close(entered)
			<-release // Deliberately ignore context to exercise bounded VM cleanup.
			defer close(finished)
			return "late", nil
		})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not start")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("runtime waited for a handler that ignored cancellation")
	}
	close(release)
	<-finished
}

func TestRuntimeIsolationAndNoAmbientCapabilities(t *testing.T) {
	_, err := Execute(context.Background(), `globalThis.leaked = 1; return 1;`, nil)
	require.NoError(t, err)
	result, err := Execute(context.Background(), `
return [typeof leaked, typeof process, typeof require, typeof fetch,
        typeof setTimeout, typeof std, typeof os, typeof __kodelet_host];
`, nil)
	require.NoError(t, err)
	assert.JSONEq(t, `["undefined","undefined","undefined","undefined","undefined","undefined","undefined","undefined"]`, string(result.Outputs[0].Value))
	_, err = Execute(context.Background(), `return await import("std");`, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "std")
}

func TestRuntimeErrorsAndUnhandledRejections(t *testing.T) {
	for name, code := range map[string]string{
		"syntax":         `const = ;`,
		"throw":          `throw new Error("broken");`,
		"unhandled":      `Promise.reject(new Error("unhandled")); return 1;`,
		"host unhandled": `tools.nope({});`,
		"nested promise": `return {value:Promise.resolve(1)};`,
		"emit promise":   `emit(Promise.resolve(1));`,
		"non-finite":     `return NaN;`,
		"function":       `return () => 1;`,
		"cycle":          `const value = {}; value.self = value; return value;`,
		"unresolved":     `await new Promise(() => {});`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Execute(context.Background(), code, nil)
			require.Error(t, err)
		})
	}
	result, err := Execute(context.Background(), `
const error = Promise.reject(new Error("handled"));
await Promise.resolve();
return await error.catch(() => "okay");
`, nil)
	require.NoError(t, err)
	assert.JSONEq(t, `"okay"`, string(result.Outputs[0].Value))

	result, err = Execute(context.Background(), "emit(1);\nthrow new Error('line two');", nil)
	require.Error(t, err)
	require.Len(t, result.Outputs, 1)
	assert.JSONEq(t, "1", string(result.Outputs[0].Value))
	assert.Contains(t, err.Error(), "code_execute.js:2")
}

func TestRuntimeHostValidationAndLimits(t *testing.T) {
	for name, code := range map[string]string{
		"null input":        `return await tools.echo(null);`,
		"array input":       `return await tools.echo([]);`,
		"undefined input":   `return await tools.echo();`,
		"recursive":         `return await tools.code_execute({code:"return 1"});`,
		"catalog options":   `return await catalog.list("wrong");`,
		"catalog name":      `return await catalog.describe(7);`,
		"catalog query":     `return await catalog.search({wrong:true});`,
		"non-JSON argument": `return await tools.echo({value:Infinity});`,
	} {
		t.Run(name, func(t *testing.T) {
			var called atomic.Bool
			_, err := Execute(context.Background(), code, func(context.Context, Request) (any, error) {
				called.Store(true)
				return true, nil
			})
			require.Error(t, err)
			assert.False(t, called.Load())
		})
	}

	t.Run("output", func(t *testing.T) {
		_, err := Execute(context.Background(), `return "x".repeat(33000);`, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "limit")
	})
	t.Run("script", func(t *testing.T) {
		_, err := Execute(context.Background(), strings.Repeat(" ", MaxScriptBytes+1), nil)
		require.ErrorContains(t, err, "script byte limit")
		_, err = Execute(context.Background(), strings.Repeat(" ", MaxScriptBytes), nil)
		require.NoError(t, err)
	})
	t.Run("host response", func(t *testing.T) {
		limits := defaultRuntimeLimits()
		limits.responseBytes = 64
		result, err := executeWithLimits(context.Background(), `
try { await tools.echo({}); } catch (error) { return {kind:error.kind, tool:error.tool, outcome:error.outcome}; }
`, func(context.Context, Request) (any, error) {
			return strings.Repeat("x", 65), nil
		}, nil, limits)
		require.NoError(t, err)
		assert.JSONEq(t, `{"kind":"invalid_output","tool":"echo","outcome":"completed"}`, string(result.Outputs[0].Value))
	})
	t.Run("admission", func(t *testing.T) {
		limits := defaultRuntimeLimits()
		limits.toolCalls = 2
		result, err := executeWithLimits(context.Background(), `
return await Promise.allSettled([tools.echo({}), tools.echo({}), tools.echo({})]);
`, func(context.Context, Request) (any, error) { return 1, nil }, nil, limits)
		require.NoError(t, err)
		assert.Contains(t, string(result.Outputs[0].Value), `"kind":"limit"`)
		assert.Contains(t, string(result.Outputs[0].Value), `"outcome":"not_started"`)
	})
}

func TestRuntimeMemoryLimit(t *testing.T) {
	limits := defaultRuntimeLimits()
	limits.memoryBytes = 16 << 20
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := executeWithLimits(ctx, `const values = []; while(true) values.push(new Uint8Array(1024 * 1024));`, nil, nil, limits)
	require.Error(t, err)
	assert.NotErrorIs(t, err, context.DeadlineExceeded)
	assert.Contains(t, strings.ToLower(err.Error()), "memory")
}

func TestRuntimeConcurrentInvocations(t *testing.T) {
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			result, err := Execute(context.Background(), `return await tools.echo({});`, func(context.Context, Request) (any, error) {
				return true, nil
			})
			if assert.NoError(t, err) && assert.Len(t, result.Outputs, 1) {
				assert.JSONEq(t, "true", string(result.Outputs[0].Value))
			}
		})
	}
	wg.Wait()
}

func TestRuntimeHostPanicAndInvalidResults(t *testing.T) {
	for name, handler := range map[string]Handler{
		"panic": func(context.Context, Request) (any, error) { panic("oops") },
		"invalid JSON": func(context.Context, Request) (any, error) {
			return make(chan bool), nil
		},
		"plain error": func(context.Context, Request) (any, error) { return nil, errors.New("oops") },
	} {
		t.Run(name, func(t *testing.T) {
			result, err := Execute(context.Background(), `return await Promise.allSettled([tools.echo({})]);`, handler)
			require.NoError(t, err)
			assert.Contains(t, string(result.Outputs[0].Value), `"status":"rejected"`)
		})
	}
}

func TestRuntimeBridgeValidation(t *testing.T) {
	for _, payload := range []string{
		`{`,
		`{} {}`,
		`{"id":1,"extra":true,"request":{"operation":"catalog.list"}}`,
		`{"id":1,"request":{"operation":"unknown"}}`,
		`{"id":0,"request":{"operation":"catalog.list"}}`,
		`{"id":1,"request":{"operation":"catalog.list","query":"unexpected"}}`,
		`{"id":1,"request":{"operation":"catalog.describe","name":""}}`,
		`{"id":1,"request":{"operation":"tool.call","name":"bad\u0000name","input":{}}}`,
	} {
		bridge := newRuntimeBridge(context.Background(), nil, nil, defaultRuntimeLimits(), &Result{})
		require.Error(t, bridge.submit([]byte(payload)), payload)
		assert.Empty(t, bridge.pending)
	}

	limits := defaultRuntimeLimits()
	limits.pendingCalls = 1
	bridge := newRuntimeBridge(context.Background(), nil, nil, limits, &Result{})
	require.NoError(t, bridge.submit([]byte(`{"id":1,"request":{"operation":"catalog.list"}}`)))
	require.ErrorContains(t, bridge.submit([]byte(`{"id":1,"request":{"operation":"catalog.list"}}`)), "reused")
	require.ErrorContains(t, bridge.submit([]byte(`{"id":2,"request":{"operation":"catalog.list"}}`)), "pending")
	assert.False(t, bridge.consume(runtimeCompletion{ID: 7}))
	assert.True(t, bridge.consume(runtimeCompletion{ID: 1}))
	assert.False(t, bridge.consume(runtimeCompletion{ID: 1}))
	assert.Zero(t, bridge.requestBytes)

	bridge.closed = true
	require.ErrorContains(t, bridge.submit([]byte(`{"id":3,"request":{"operation":"catalog.list"}}`)), "no longer")
}

func TestRuntimeCatalogLimit(t *testing.T) {
	limits := defaultRuntimeLimits()
	limits.catalogCalls = 1
	result, err := executeWithLimits(t.Context(), `
return await Promise.allSettled([catalog.list(), catalog.list()]);
`, func(context.Context, Request) (any, error) {
		return true, nil
	}, nil, limits)
	require.NoError(t, err)
	assert.Contains(t, string(result.Outputs[0].Value), `"kind":"limit"`)
}

func TestRuntimeOversizedErrorPreservesUnknownOutcome(t *testing.T) {
	limits := defaultRuntimeLimits()
	limits.responseBytes = 256
	result, err := executeWithLimits(context.Background(), `
try { await tools.write({}); } catch (error) { return error; }
`, func(context.Context, Request) (any, error) {
		return nil, &Error{
			Kind: "transport", Message: strings.Repeat("x", 300),
			Tool: "write", CallID: "child-1", Outcome: "unknown",
		}
	}, nil, limits)
	require.NoError(t, err)
	require.Len(t, result.Outputs, 1)
	var failure Error
	require.NoError(t, json.Unmarshal(result.Outputs[0].Value, &failure))
	assert.Equal(t, "invalid_output", failure.Kind)
	assert.Equal(t, "unknown", failure.Outcome)
	assert.Equal(t, "child-1", failure.CallID)
}

func TestRuntimeSettlesUnawaitedWorkBeforeReturning(t *testing.T) {
	var completed atomic.Bool
	result, err := Execute(context.Background(), `tools.echo({}).then(value => emit(value));`, func(context.Context, Request) (any, error) {
		completed.Store(true)
		return "completed", nil
	})
	require.NoError(t, err)
	assert.True(t, completed.Load())
	require.Len(t, result.Outputs, 1)
	assert.JSONEq(t, `"completed"`, string(result.Outputs[0].Value))
}

func TestRuntimeCompletionBudgetAppliesBackpressure(t *testing.T) {
	limits := defaultRuntimeLimits()
	limits.retainedBytes = 256
	limits.responseBytes = 128
	// The tiny request budget permits two requests, each response reservation
	// occupies half the result budget, and the VM must drain completions before
	// accepting the next pair. Successful host work must not turn into overflow.
	var calls atomic.Int32
	result, err := executeWithLimits(context.Background(), `
let total = 0;
for (let i = 0; i < 4; i++) {
  const values = await Promise.all([tools.echo({}), tools.echo({})]);
  total += values[0] + values[1];
}
return total;
`, func(context.Context, Request) (any, error) {
		calls.Add(1)
		return 1, nil
	}, nil, limits)
	require.NoError(t, err)
	assert.Equal(t, int32(8), calls.Load())
	assert.JSONEq(t, `8`, string(result.Outputs[0].Value))
}
