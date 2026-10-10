package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/codemode"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodeExecuteToolDiscoveryAndCalls(t *testing.T) {
	tool := &CodeExecuteTool{}
	ctx := ContextWithCodeExecution(t.Context(), CodeExecutionContext{
		Definitions: []codemode.Definition{{Name: "lookup", Description: "Find issue details", Group: "extension/test"}},
		Call: func(_ context.Context, name, input, callID string) (CodeToolReply, error) {
			assert.Equal(t, "lookup", name)
			assert.JSONEq(t, `{"id":42}`, input)
			assert.NotEmpty(t, callID)
			return CodeToolReply{
				Data:  map[string]any{"id": 42, "secret": "omit me"},
				Input: json.RawMessage(input),
				Result: &tooltypes.StructuredToolResult{
					ToolName:    name,
					Success:     true,
					Data:        "do not duplicate machine data",
					Metadata:    tooltypes.ExtensionToolMetadata{Output: "UI-only detail"},
					Attachments: []tooltypes.ToolAttachment{{Type: "image", ArtifactID: "unselected"}},
				},
			}, nil
		},
	})
	params, err := json.Marshal(codeExecuteInput{Code: `
const [listing, matches] = await Promise.all([catalog.list(), catalog.search("issue")]);
if (listing.tools[0].name !== "lookup" || matches.tools[0].name !== "lookup") throw new Error("discovery failed");
const result = await tools.lookup({id: 42});
if ("result" in result || "input" in result) throw new Error("UI details entered the VM");
return {id: result.data.id};
`})
	require.NoError(t, err)
	updates := 0
	result := tool.ExecuteStreaming(ctx, nil, string(params), func(snapshot tooltypes.ToolResult) {
		updates++
		assert.Equal(t, "code_execute", snapshot.StructuredData().ToolName)
		for _, call := range snapshot.(CodeExecuteResult).Metadata.Calls {
			assert.Nil(t, call.Input)
			assert.Nil(t, call.Result, "progress must not stream child bodies")
		}
	})
	require.False(t, result.IsError(), result.GetError())
	assert.Positive(t, updates)
	assert.Contains(t, result.AssistantFacing(), `"id":42`)
	assert.NotContains(t, result.AssistantFacing(), "omit me")
	assert.NotContains(t, result.AssistantFacing(), "UI-only detail")
	var metadata tooltypes.CodeExecutionMetadata
	require.True(t, tooltypes.ExtractMetadata(result.StructuredData().Metadata, &metadata))
	require.Len(t, metadata.Calls, 1)
	assert.Equal(t, "completed", metadata.Calls[0].Status)
	assert.JSONEq(t, `{"id":42}`, string(metadata.Calls[0].Input))
	require.NotNil(t, metadata.Calls[0].Result)
	assert.Equal(t, tooltypes.ExtensionToolMetadata{Output: "UI-only detail"}, metadata.Calls[0].Result.Metadata)
	assert.Nil(t, metadata.Calls[0].Result.Data)
	assert.Empty(t, metadata.Calls[0].Result.Attachments)
	// UI details must survive history reload without entering selected output.
	encoded, err := json.Marshal(result.StructuredData())
	require.NoError(t, err)
	var restored tooltypes.StructuredToolResult
	require.NoError(t, json.Unmarshal(encoded, &restored))
	assert.Equal(t, result.StructuredData().Metadata, restored.Metadata)
}

func TestCodeExecuteToolBoundsChildDetails(t *testing.T) {
	ctx := ContextWithCodeExecution(t.Context(), CodeExecutionContext{
		Definitions: []codemode.Definition{{Name: "lookup"}},
		Call: func(context.Context, string, string, string) (CodeToolReply, error) {
			return CodeToolReply{Result: &tooltypes.StructuredToolResult{
				ToolName: "lookup", Success: true,
				Metadata: tooltypes.ExtensionToolMetadata{Output: strings.Repeat("x", 300*1024)},
			}}, nil
		},
	})
	result := (&CodeExecuteTool{}).Execute(ctx, nil, `{"code":"await tools.lookup({}); await tools.lookup({}); return 42;"}`)
	require.False(t, result.IsError(), result.GetError())
	calls := result.(CodeExecuteResult).Metadata.Calls
	require.Len(t, calls, 2)
	require.NotNil(t, calls[0].Result)
	assert.Nil(t, calls[1].Result)
	assert.True(t, calls[1].DetailsOmitted, "the budget applies across children, not per child")
	assert.Contains(t, result.AssistantFacing(), "42")
}

func TestCodeExecuteToolCaughtFailure(t *testing.T) {
	ctx := ContextWithCodeExecution(t.Context(), CodeExecutionContext{
		Definitions: []codemode.Definition{},
		Call: func(context.Context, string, string, string) (CodeToolReply, error) {
			t.Fatal("a forbidden tool must not be dispatched")
			return CodeToolReply{}, nil
		},
	})
	params, err := json.Marshal(codeExecuteInput{Code: `
try {
  await tools.forbidden({});
} catch (e) {
  return {kind: e.kind, outcome: e.outcome};
}
`})
	require.NoError(t, err)
	result := (&CodeExecuteTool{}).Execute(ctx, nil, string(params))
	require.False(t, result.IsError(), result.GetError())
	assert.Contains(t, result.AssistantFacing(), `"blocked"`)
	assert.Contains(t, result.AssistantFacing(), "did not succeed")
	var metadata tooltypes.CodeExecutionMetadata
	require.True(t, tooltypes.ExtractMetadata(result.StructuredData().Metadata, &metadata))
	require.Len(t, metadata.Calls, 1)
	assert.Equal(t, "blocked", metadata.Calls[0].Status)
	assert.Nil(t, metadata.Calls[0].Result)
	assert.Nil(t, metadata.Calls[0].Input)
}

func TestCodeExecuteToolRequiresHostAuthority(t *testing.T) {
	result := (&CodeExecuteTool{}).Execute(t.Context(), nil, `{"code":"return 1"}`)
	assert.True(t, result.IsError())
	assert.Contains(t, result.GetError(), "authorized runner")
}

func TestCodeExecuteToolDescriptionMatchesLimits(t *testing.T) {
	description := (&CodeExecuteTool{}).Description()
	assert.Contains(t, description, "Limits: 15 minutes,")
	assert.Contains(t, description, fmt.Sprintf("up to %d tool calls run at once", codemode.MaxConcurrentToolCalls))
	assert.Contains(t, description, fmt.Sprintf("Tool replies and individual return/emit values are limited to %d MiB", codemode.MaxHostResponseBytes>>20))
	assert.Contains(t, description, "Total selected output above ~40 KB is truncated")
	assert.Contains(t, description, "Output-limit failures do not roll back completed tool calls; check for side effects before retrying")
	assert.NotContains(t, description, "code_search", "examples use a generic tool name")
	assert.Contains(t, description, codemode.RuntimeDeclaration, "the shared runtime contract is embedded verbatim")
	assert.Equal(t, 1, strings.Count(description, "interface ToolReply"), "the description has no hand-written copy")
	assert.Contains(t, description, "never guess field names")
	assert.Contains(t, description, "Do as much as you can in one code_execute call")
	assert.Contains(t, description, "run independent calls in parallel with Promise.all, including catalog lookups")
	assert.Contains(t, description, "return or emit e.result?.text || e.message")
	assert.Contains(t, description, "throws invalid_output")
	assert.LessOrEqual(t, len(description), 4<<10, "keep the bootstrap description compact")
}

func TestCodeExecuteToolInputValidation(t *testing.T) {
	tool := &CodeExecuteTool{}
	for _, input := range []string{
		`{`, `null`, `{}`, `{"code":" "}`, `{"code":4}`,
		`{"code":"return 1", "callableTools":["bash"]}`,
		`{"code":"return 1"} {}`,
	} {
		assert.Error(t, tool.ValidateInput(nil, input), input)
	}
	input, err := json.Marshal(codeExecuteInput{Code: strings.Repeat("x", 128*1024+1)})
	require.NoError(t, err)
	assert.Error(t, tool.ValidateInput(nil, string(input)))
	assert.NoError(t, tool.ValidateInput(nil, `{"code":"return 1"}`))
}

func TestCodeExecuteToolRetainsArtifactsFromEffectiveErrorReply(t *testing.T) {
	attachment := tooltypes.ToolAttachment{Type: "image", ArtifactID: "partial-image"}
	ctx := ContextWithCodeExecution(t.Context(), CodeExecutionContext{
		Definitions: []codemode.Definition{{Name: "images"}},
		Call: func(_ context.Context, name, _, callID string) (CodeToolReply, error) {
			return CodeToolReply{Attachments: []tooltypes.ToolAttachment{{Type: "image", ArtifactID: "raw-image"}}}, &CodeToolError{
				Kind: "tool_error", Tool: name, CallID: callID, Outcome: "completed", Message: "partial failure",
				Result: &CodeToolReply{
					Attachments: []tooltypes.ToolAttachment{attachment},
					Result:      &tooltypes.StructuredToolResult{ToolName: name, Error: "effective failure"},
				},
			}
		},
	})
	for _, test := range []struct {
		name string
		code string
		fail bool
	}{
		{
			name: "caught",
			code: `
try {
  await tools.images({});
} catch (e) {
  emit.artifact(e.result.attachments[0]);
  return "raw-image";
}`,
		},
		{
			name: "emitted before failure",
			code: `
try {
  await tools.images({});
} catch (e) {
  emit.artifact(e.result.attachments[0].artifactId);
  throw e;
}`,
			fail: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			params, err := json.Marshal(codeExecuteInput{Code: test.code})
			require.NoError(t, err)
			result := (&CodeExecuteTool{}).Execute(ctx, nil, string(params))
			assert.Equal(t, test.fail, result.IsError(), result.GetError())
			assert.Equal(t, []tooltypes.ToolAttachment{attachment}, result.StructuredData().Attachments)
			assert.Equal(t, "effective failure", result.(CodeExecuteResult).Metadata.Calls[0].Result.Error)
		})
	}
}

func TestCodeExecuteResultSendsSelectedStringsAsPlainText(t *testing.T) {
	result := CodeExecuteResult{
		Metadata: tooltypes.CodeExecutionMetadata{
			Status:  "completed",
			Outputs: []json.RawMessage{json.RawMessage(`"legacy\tvalue"`)},
			Items: []tooltypes.CodeExecutionOutput{
				{Type: "json", Value: json.RawMessage(`"first line\nsecond \"quoted\" line"`)},
				{Type: "json", Value: json.RawMessage(`{"count":2,"text":"a\nb"}`)},
				{Type: "json", Value: json.RawMessage(`42`)},
				{Type: "json", Value: json.RawMessage(`null`)},
				{Type: "image", ArtifactID: "image-1"},
			},
		},
		Attachments: []tooltypes.ToolAttachment{{Type: "image", ArtifactID: "image-1", MimeType: "image/png"}},
	}

	facing := result.AssistantFacing()
	assert.Contains(t, facing, "legacy\tvalue\n")
	assert.Contains(t, facing, "first line\nsecond \"quoted\" line\n")
	assert.NotContains(t, facing, `"first line`, "strings must not stay JSON-quoted")
	assert.Contains(t, facing, `{"count":2,"text":"a\nb"}`, "non-string values stay compact JSON")
	assert.Contains(t, facing, "\n42\n")
	assert.Contains(t, facing, "\nnull\n")

	parts := result.ContentParts()
	require.Len(t, parts, 7)
	assert.Equal(t, "first line\nsecond \"quoted\" line", parts[1].Text)
	assert.JSONEq(t, `{"count":2,"text":"a\nb"}`, parts[2].Text)
	assert.Equal(t, "42", parts[3].Text)
	assert.Equal(t, "null", parts[4].Text)
	assert.Equal(t, "Image artifact: image-1", parts[5].Text)
	assert.Equal(t, tooltypes.ToolResultContentPartTypeImage, parts[6].Type)
}

func TestCodeExecuteToolConsoleLogReachesModelAsText(t *testing.T) {
	ctx := ContextWithCodeExecution(t.Context(), CodeExecutionContext{
		Call: func(context.Context, string, string, string) (CodeToolReply, error) {
			return CodeToolReply{}, nil
		},
	})
	params, err := json.Marshal(codeExecuteInput{Code: `
console.log("files:\n" + ["a.go", "b.go"].join("\n"));
return "done";
`})
	require.NoError(t, err)
	result := (&CodeExecuteTool{}).Execute(ctx, nil, string(params))
	require.False(t, result.IsError(), result.GetError())
	assert.Contains(t, result.AssistantFacing(), "files:\na.go\nb.go\ndone\n")
	assert.NotContains(t, result.AssistantFacing(), `\n`)
}

func TestCodeExecuteToolExplicitMediaSelection(t *testing.T) {
	for _, test := range []struct {
		name, code, failure string
		allowImages         bool
		retained            int
	}{
		{name: "ordinary JSON is inert", code: `return {type: "image", artifactId: r.attachments[0].artifactId};`},
		{name: "retain without viewing", code: `emit.artifact(r.attachments[0]);`, retained: 1},
		{
			name: "mixed ordered output",
			code: `
emit("before");
emit.image(r.attachments[0], {detail: "original"});
emit({after: true});
emit.artifact(r.attachments[1]);`,
			allowImages: true,
			retained:    2,
		},
		{name: "foreign reference", code: `emit.image("foreign");`, allowImages: true, failure: "effective child reply"},
		{name: "image permission required", code: `emit.image(r.attachments[0]);`, failure: "view_image permission"},
	} {
		t.Run(test.name, func(t *testing.T) {
			authority := CodeExecutionContext{
				Definitions: []codemode.Definition{{Name: "images"}},
				ValidateImage: func(detail string) error {
					assert.Equal(t, "original", detail)
					return nil
				},
				Call: func(context.Context, string, string, string) (CodeToolReply, error) {
					return CodeToolReply{Attachments: []tooltypes.ToolAttachment{
						{Type: "image", ArtifactID: "image-1", MimeType: "image/png"},
						{Type: "image", ArtifactID: "image-2", MimeType: "image/png"},
					}}, nil
				},
			}
			if test.allowImages {
				authority.Definitions = append(authority.Definitions, codemode.Definition{Name: "view_image"})
			}
			ctx := ContextWithCodeExecution(t.Context(), authority)
			params, err := json.Marshal(codeExecuteInput{Code: `const r = await tools.images({}); ` + test.code})
			require.NoError(t, err)
			result := (&CodeExecuteTool{}).Execute(ctx, nil, string(params))
			assert.Equal(t, test.failure != "", result.IsError(), result.GetError())
			if test.failure != "" {
				assert.Contains(t, result.GetError(), test.failure)
			}
			require.Len(t, result.StructuredData().Attachments, test.retained)
			parts := result.(tooltypes.MultiModalToolResult).ContentParts()
			if test.name != "mixed ordered output" {
				assert.Empty(t, parts)
				return
			}
			require.Len(t, parts, 6)
			assert.Equal(t, "before", parts[1].Text, "selected strings reach the model as plain text")
			assert.Equal(t, tooltypes.ToolResultContentPart{
				Type:       tooltypes.ToolResultContentPartTypeImage,
				ArtifactID: "image-1",
				MimeType:   "image/png",
				Detail:     "original",
			}, parts[3])
			assert.JSONEq(t, `{"after":true}`, parts[4].Text)
			assert.Contains(t, parts[5].Text, "pixels not sent")
		})
	}
}

func TestCodeExecuteToolRejectedReplyPreservesFailureSummaryAndOutcome(t *testing.T) {
	for _, test := range []struct {
		name    string
		data    any
		outcome string
	}{
		{name: "non-JSON result", data: make(chan int)},
		{name: "oversized result", data: strings.Repeat("x", 2<<20)},
		{name: "non-JSON error with unknown outcome", data: make(chan int), outcome: "unknown"},
		{name: "oversized error not started", data: strings.Repeat("x", 2<<20), outcome: "not_started"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := ContextWithCodeExecution(t.Context(), CodeExecutionContext{
				Definitions: []codemode.Definition{{Name: "lookup", OutputSchema: map[string]any{"type": "invalid"}}},
				Call: func(context.Context, string, string, string) (CodeToolReply, error) {
					reply := CodeToolReply{Data: test.data}
					if test.outcome != "" {
						return reply, &CodeToolError{Kind: "tool_error", Outcome: test.outcome, Result: &reply}
					}
					return reply, nil
				},
			})
			params, err := json.Marshal(codeExecuteInput{Code: `
try {
  await tools.lookup({});
} catch (e) {
  return {kind: e.kind, outcome: e.outcome, message: e.message};
}
`})
			require.NoError(t, err)
			result := (&CodeExecuteTool{}).Execute(ctx, nil, string(params))
			require.False(t, result.IsError(), result.GetError())
			outcome, status := test.outcome, "failed"
			switch outcome {
			case "":
				outcome = "completed"
			case "unknown":
				status = "unknown"
			}
			assert.Contains(t, result.AssistantFacing(), `"kind":"invalid_output"`)
			assert.Contains(t, result.AssistantFacing(), `"outcome":"`+outcome+`"`)
			assert.Contains(t, result.AssistantFacing(), "not valid JSON or exceeds the 2 MiB limit")
			assert.NotContains(t, result.AssistantFacing(), "outputSchema", "JSON and size checks must run before schema validation")
			var metadata tooltypes.CodeExecutionMetadata
			require.True(t, tooltypes.ExtractMetadata(result.StructuredData().Metadata, &metadata))
			require.Len(t, metadata.Calls, 1)
			assert.Equal(t, status, metadata.Calls[0].Status)
			assert.Equal(t, "invalid_output", metadata.Calls[0].ErrorKind)
			assert.Contains(t, result.AssistantFacing(), "did not succeed")
		})
	}
}

func TestCodeExecuteToolOutputSchemaValidation(t *testing.T) {
	const objectSchema = `{
  "type": "object",
  "properties": {"items": {"type": "array", "items": {"type": "integer"}}},
  "required": ["items"]
}`
	for _, test := range []struct {
		name    string
		schema  string
		data    string
		outcome string
		invalid bool
	}{
		{name: "structured data", schema: objectSchema, data: `{"items":[42]}`},
		{name: "type mismatch", schema: objectSchema, data: `{"items":["private-output"]}`, invalid: true},
		{name: "policy removed data", schema: objectSchema, data: `null`},
		{name: "no schema", data: `[1,false,"text"]`},
		{name: "falsy scalar", schema: `{"type":"boolean"}`, data: `false`},
		{name: "invalid schema", schema: `{"type":"private-output"}`, data: `{}`, invalid: true},
		{
			name: "local reference", data: `[1,2]`,
			schema: `{"type":"array","items":{"$ref":"#/$defs/id"},"$defs":{"id":{"type":"integer"}}}`,
		},
		{name: "partial failure", schema: objectSchema, data: `{"items":[]}`, outcome: "completed"},
		{name: "mismatched partial failure passes through", schema: objectSchema, data: `"partial-output"`, outcome: "unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var schema map[string]any
			if test.schema != "" {
				require.NoError(t, json.Unmarshal([]byte(test.schema), &schema))
			}
			calls := 0
			ctx := ContextWithCodeExecution(t.Context(), CodeExecutionContext{
				Definitions: []codemode.Definition{{Name: "lookup", OutputSchema: schema}},
				Call: func(_ context.Context, name, _, callID string) (CodeToolReply, error) {
					calls++
					reply := CodeToolReply{Data: json.RawMessage(test.data)}
					if test.outcome != "" {
						return CodeToolReply{Data: "raw-data-must-not-be-used"}, &CodeToolError{
							Kind: "tool_error", Tool: name, CallID: callID, Outcome: test.outcome,
							Message: "tool failed", Result: &reply,
						}
					}
					return reply, nil
				},
			})
			params, err := json.Marshal(codeExecuteInput{Code: `
try {
  return (await tools.lookup({})).data;
} catch (e) {
  if (e.kind === "tool_error") return e.result.data;
  return e;
}
`})
			require.NoError(t, err)
			result := (&CodeExecuteTool{}).Execute(ctx, nil, string(params))
			require.False(t, result.IsError(), result.GetError())
			assert.Equal(t, 1, calls, "schema errors must never retry the tool")
			metadata := result.(CodeExecuteResult).Metadata
			require.Len(t, metadata.Items, 1)
			if !test.invalid {
				assert.JSONEq(t, test.data, string(metadata.Items[0].Value))
				return
			}
			var failure CodeToolError
			require.NoError(t, json.Unmarshal(metadata.Items[0].Value, &failure))
			assert.Equal(t, "invalid_output", failure.Kind)
			outcome := test.outcome
			if outcome == "" {
				outcome = "completed"
			}
			assert.Equal(t, outcome, failure.Outcome)
			assert.Nil(t, failure.Result)
			assert.Contains(t, failure.Message, "outputSchema")
			assert.NotContains(t, result.AssistantFacing(), "private-output")
			assert.Equal(t, "invalid_output", metadata.Calls[0].ErrorKind)
		})
	}
}

func TestCodeExecuteToolMismatchedPartialDataKeepsFailure(t *testing.T) {
	calls := 0
	ctx := ContextWithCodeExecution(t.Context(), CodeExecutionContext{
		Definitions: []codemode.Definition{{
			Name:         "lookup",
			OutputSchema: map[string]any{"type": "object", "required": []any{"items"}},
		}},
		Call: func(_ context.Context, name, _, callID string) (CodeToolReply, error) {
			calls++
			partial := CodeToolReply{
				Data:        map[string]any{"error": "rate_limited"},
				Text:        "rate limited; retry after 30s",
				Attachments: []tooltypes.ToolAttachment{},
			}
			return CodeToolReply{}, &CodeToolError{
				Kind:    "tool_error",
				Tool:    name,
				CallID:  callID,
				Outcome: "completed",
				Message: "lookup failed: 403",
				Result:  &partial,
			}
		},
	})
	params, err := json.Marshal(codeExecuteInput{Code: `
try {
  await tools.lookup({});
} catch (e) {
  return {kind: e.kind, outcome: e.outcome, message: e.message, result: e.result};
}
`})
	require.NoError(t, err)
	result := (&CodeExecuteTool{}).Execute(ctx, nil, string(params))
	require.False(t, result.IsError(), result.GetError())
	assert.Equal(t, 1, calls, "a failed call is never retried")
	metadata := result.(CodeExecuteResult).Metadata
	require.Len(t, metadata.Items, 1)
	assert.JSONEq(t, `{
  "kind": "tool_error",
  "outcome": "completed",
  "message": "lookup failed: 403",
  "result": {
    "data": {"error": "rate_limited"},
    "text": "rate limited; retry after 30s",
    "attachments": [],
    "truncated": false
  }
}`, string(metadata.Items[0].Value), "schema checks never replace a failure's own diagnostics")
	assert.Equal(t, "tool_error", metadata.Calls[0].ErrorKind)
}

func TestCodeExecuteToolOutputSchemaCannotLoadExternalResources(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"type":"object"}`))
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "private-schema.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"type":"object"}`), 0o600))
	fileURL := (&url.URL{Scheme: "file", Path: path}).String()
	for _, schema := range []map[string]any{
		{"$ref": server.URL + "/private-schema"},
		{"$schema": server.URL + "/private-schema"},
		{"$ref": fileURL},
		{"$schema": fileURL},
	} {
		ctx := ContextWithCodeExecution(t.Context(), CodeExecutionContext{
			Definitions: []codemode.Definition{{Name: "lookup", OutputSchema: schema}},
			Call: func(context.Context, string, string, string) (CodeToolReply, error) {
				return CodeToolReply{Data: map[string]any{}}, nil
			},
		})
		result := (&CodeExecuteTool{}).Execute(ctx, nil, `{"code":"await tools.lookup({});"}`)
		require.True(t, result.IsError(), schema)
		assert.Contains(t, result.GetError(), "outputSchema is invalid or requires an external resource")
		assert.NotContains(t, result.GetError(), server.URL)
		assert.NotContains(t, result.GetError(), path)
		assert.Equal(t, "invalid_output", result.(CodeExecuteResult).Metadata.Calls[0].ErrorKind)
	}
	assert.Zero(t, requests.Load(), "external refs must not initiate network requests")
}

type changingCodeToolData struct {
	marshals int
}

func (d *changingCodeToolData) MarshalJSON() ([]byte, error) {
	d.marshals++
	if d.marshals == 1 {
		return []byte(`42`), nil
	}
	return []byte(`"unvalidated-private-output"`), nil
}

func TestCodeExecuteToolValidatesAndReturnsSameSnapshot(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(fmt.Sprintf("failure=%t", failure), func(t *testing.T) {
			data := &changingCodeToolData{}
			ctx := ContextWithCodeExecution(t.Context(), CodeExecutionContext{
				Definitions: []codemode.Definition{{Name: "lookup", OutputSchema: map[string]any{"type": "integer"}}},
				Call: func(context.Context, string, string, string) (CodeToolReply, error) {
					reply := CodeToolReply{Data: data}
					if failure {
						return reply, &CodeToolError{Kind: "tool_error", Message: "failed", Outcome: "completed", Result: &reply}
					}
					return reply, nil
				},
			})
			params, err := json.Marshal(codeExecuteInput{Code: `
try {
  return (await tools.lookup({})).data;
} catch (e) {
  return e.result.data;
}
`})
			require.NoError(t, err)
			result := (&CodeExecuteTool{}).Execute(ctx, nil, string(params))
			require.False(t, result.IsError(), result.GetError())
			assert.Equal(t, 1, data.marshals, "validation and delivery must share one serialized snapshot")
			require.Len(t, result.(CodeExecuteResult).Metadata.Items, 1)
			assert.JSONEq(t, `42`, string(result.(CodeExecuteResult).Metadata.Items[0].Value))
			assert.NotContains(t, result.AssistantFacing(), "unvalidated-private-output")
		})
	}
}

func TestCodeExecuteToolCancellationDoesNotWaitForProgressHook(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx = ContextWithCodeExecution(ctx, CodeExecutionContext{
		Definitions: []codemode.Definition{{Name: "lookup"}},
		Call: func(ctx context.Context, _, _, _ string) (CodeToolReply, error) {
			return CodeToolReply{}, ctx.Err()
		},
	})
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	result := make(chan tooltypes.ToolResult, 1)
	go func() {
		result <- (&CodeExecuteTool{}).ExecuteStreaming(ctx, nil, `{"code":"return await tools.lookup({});"}`, func(tooltypes.ToolResult) {
			once.Do(func() { close(entered) })
			<-release
		})
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("progress callback did not begin")
	}
	cancel()
	select {
	case final := <-result:
		close(release)
		assert.True(t, final.IsError())
	case <-time.After(500 * time.Millisecond):
		close(release)
		select {
		case <-result:
		case <-time.After(5 * time.Second):
			t.Fatal("execution did not finish even after progress hook was released")
		}
		t.Error("cancelled execution waited for a progress callback instead of respecting parent cancellation")
	}
}
