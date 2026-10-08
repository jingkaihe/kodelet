package tools

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
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
			assert.Equal(t, `"before"`, parts[1].Text)
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
				Definitions: []codemode.Definition{{Name: "lookup"}},
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
  return {kind: e.kind, outcome: e.outcome};
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
			var metadata tooltypes.CodeExecutionMetadata
			require.True(t, tooltypes.ExtractMetadata(result.StructuredData().Metadata, &metadata))
			require.Len(t, metadata.Calls, 1)
			assert.Equal(t, status, metadata.Calls[0].Status)
			assert.Equal(t, "invalid_output", metadata.Calls[0].ErrorKind)
			assert.Contains(t, result.AssistantFacing(), "did not succeed")
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
