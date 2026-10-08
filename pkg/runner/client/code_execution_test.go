package client

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type codeTestEnvironment struct {
	agentenv.Environment
	execute func(context.Context, agentenv.ToolRequest, agentenv.ToolUpdateSink) (agentenv.ToolExecution, error)
}

func (e *codeTestEnvironment) ExecuteTool(ctx context.Context, request agentenv.ToolRequest, updates agentenv.ToolUpdateSink) (agentenv.ToolExecution, error) {
	return e.execute(ctx, request, updates)
}

func newCodeService(t *testing.T, environment agentenv.Environment) (*Service, *activeRun, *recordingPeer, runnerpayload.ToolExecuteParams) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	peer := &recordingPeer{}
	run := &activeRun{
		id:             "code-run",
		conversationID: "conversation",
		ctx:            ctx,
		cancel:         cancel,
		codeExecution:  true,
		environment:    environment,
		manifest: runnerpayload.Manifest{
			Digest:           "manifest",
			WorkingDirectory: t.TempDir(),
			Capabilities:     runnerpayload.EnvironmentCapabilities{CodeExecution: true},
			Tools: []runnerpayload.ToolDefinition{
				{Name: "code_execute", Placement: "environment"},
				{Name: "test_tool", Placement: "environment", InputSchema: map[string]any{"type": "object"}, Short: "Test things."},
			},
		},
	}
	service := &Service{runs: map[string]*activeRun{run.id: run}, peer: peer}
	return service, run, peer, runnerpayload.ToolExecuteParams{
		RunID:          run.id,
		ToolCallID:     "parent",
		Name:           "code_execute",
		ManifestDigest: "manifest",
		CallableTools:  new([]string{"test_tool"}),
	}
}

func TestRunnerCodeChildUsesEffectiveResultAndLocalExecution(t *testing.T) {
	var calls int
	environment := &codeTestEnvironment{execute: func(ctx context.Context, request agentenv.ToolRequest, updates agentenv.ToolUpdateSink) (agentenv.ToolExecution, error) {
		calls++
		assert.Equal(t, "test_tool", request.Name)
		assert.Equal(t, "child", request.ToolCallID)
		assert.Equal(t, `{"input":"private input"}`, request.Input)
		assert.Nil(t, updates)
		assert.NotNil(t, tooltypes.ModelHelperFromContext(ctx))
		assert.NotNil(t, tooltypes.ArtifactResolverFromContext(ctx))
		assert.NotNil(t, tools.ToolContextFromContext(ctx).MetadataStore)
		return agentenv.ToolExecution{
			Input: request.Input, Modified: true,
			Result: tooltypes.BaseToolResult{Result: "raw secret output"},
			StructuredResult: tooltypes.StructuredToolResult{
				ToolName: request.Name, Success: true, Data: map[string]any{"secret": true},
				Metadata:    tooltypes.ExtensionToolMetadata{Output: "redacted"},
				Attachments: []tooltypes.ToolAttachment{{Type: "image", ArtifactID: "saved-image"}},
			},
		}, nil
	}}
	service, run, peer, params := newCodeService(t, environment)
	service.peer = &modelHelperPeer{call: func(ctx context.Context, method string, params, result any) error {
		if method == runnerpayload.MethodArtifactResolve {
			request := params.(runnerpayload.ArtifactRequest)
			assert.Equal(t, "child", request.ToolCallID)
			*result.(*tooltypes.ToolAttachment) = tooltypes.ToolAttachment{Type: "image", ArtifactID: "saved-image"}
		}
		return peer.Call(ctx, method, params, result)
	}}
	authority, err := service.codeExecutionContext(t.Context(), run, params)
	require.NoError(t, err)
	require.Len(t, authority.Definitions, 1)
	reply, err := authority.Call(t.Context(), "test_tool", `{"input":"private input"}`, "child")
	require.NoError(t, err)
	assert.Equal(t, 1, calls)
	assert.Nil(t, reply.Data)
	assert.Equal(t, "redacted", reply.Text)
	assert.Nil(t, reply.Input, "a result hook's redaction must not be bypassed through arguments")
	require.NotNil(t, reply.Result)
	assert.Equal(t, tooltypes.ExtensionToolMetadata{Output: "redacted"}, reply.Result.Metadata)
	require.Len(t, reply.Attachments, 1)
	assert.Equal(t, "saved-image", reply.Attachments[0].ArtifactID)
	assert.Equal(t, []string{protocol.MethodToolChildBegin, runnerpayload.MethodArtifactResolve, protocol.MethodToolChildEnd}, peer.calls)
	assert.Empty(t, peer.updates)
	for _, params := range peer.callParams {
		payload, err := json.Marshal(params)
		require.NoError(t, err)
		assert.NotContains(t, string(payload), "private input")
		assert.NotContains(t, string(payload), "secret")
		assert.NotContains(t, string(payload), "redacted")
	}
}

func TestRunnerMachineDataStaysRunnerLocal(t *testing.T) {
	machineData := map[string]any{"rows": []any{"machine-row"}}
	environment := &codeTestEnvironment{execute: func(_ context.Context, request agentenv.ToolRequest, _ agentenv.ToolUpdateSink) (agentenv.ToolExecution, error) {
		return agentenv.ToolExecution{
			Input:  request.Input,
			Result: tooltypes.BaseToolResult{Result: "one row"},
			StructuredResult: tooltypes.StructuredToolResult{
				ToolName: request.Name,
				Success:  true,
				Data:     machineData,
				Metadata: tooltypes.ExtensionToolMetadata{Output: "one row"},
			},
		}, nil
	}}
	service, run, _, params := newCodeService(t, environment)
	authority, err := service.codeExecutionContext(t.Context(), run, params)
	require.NoError(t, err)
	reply, err := authority.Call(t.Context(), "test_tool", `{}`, "child")
	require.NoError(t, err)
	assert.Equal(t, machineData, reply.Data, "code-mode children keep machine data")

	direct, err := service.executeRunTool(t.Context(), run, runnerpayload.ToolExecuteParams{
		RunID:      run.id,
		ToolCallID: "direct",
		Name:       "test_tool",
		Input:      json.RawMessage(`{}`),
	}, false)
	require.NoError(t, err)
	assert.Nil(t, direct.Result.Structured.Data)
	encoded, err := json.Marshal(direct)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "machine-row", "direct results must not carry machine data over the runner link")
	assert.Contains(t, string(encoded), "one row")
}

func TestRunnerCodeAuthorization(t *testing.T) {
	for _, name := range []string{
		"missing metadata", "wrong digest", "old central", "old runner", "missing parent", "missing child",
		"recursive", "model only", "empty", "agent restriction", "no tools", "command restriction", "no peer",
	} {
		t.Run(name, func(t *testing.T) {
			service, run, _, params := newCodeService(t, &codeTestEnvironment{})
			switch name {
			case "missing metadata":
				params.CallableTools = nil
			case "wrong digest":
				params.ManifestDigest = "stale"
			case "old central":
				run.codeExecution = false
			case "old runner":
				run.manifest.Capabilities.CodeExecution = false
			case "missing parent":
				run.manifest.Tools = run.manifest.Tools[1:]
			case "missing child":
				params.CallableTools = new([]string{"hidden"})
			case "recursive":
				params.CallableTools = new([]string{"code_execute"})
			case "model only":
				run.manifest.Tools = append(run.manifest.Tools, runnerpayload.ToolDefinition{
					Name:      "skill",
					Placement: "environment",
					ModelOnly: true,
				})
				params.CallableTools = new([]string{"test_tool", "skill"})
			case "empty":
				params.CallableTools = new([]string{})
			case "agent restriction":
				run.codeAllowedTools = new([]string{"code_execute"})
			case "no tools":
				run.config.ExecutionOptions = &llmtypes.ExecutionOptions{NoTools: new(true)}
			case "command restriction":
				run.config.AllowedTools = []string{"code_execute"}
			case "no peer":
				service.peer = nil
			}
			authority, err := service.codeExecutionContext(t.Context(), run, params)
			if name == "empty" || name == "agent restriction" || name == "command restriction" {
				require.NoError(t, err)
				assert.Empty(t, authority.Definitions)
				_, err = authority.Call(t.Context(), "test_tool", `{}`, "child")
				var denied *tools.CodeToolError
				require.ErrorAs(t, err, &denied)
				assert.Equal(t, "not_started", denied.Outcome)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestRunnerCodeCatalogCarriesSummaries(t *testing.T) {
	service, run, _, params := newCodeService(t, &codeTestEnvironment{})
	authority, err := service.codeExecutionContext(t.Context(), run, params)
	require.NoError(t, err)
	require.Len(t, authority.Definitions, 1)
	assert.Equal(t, "Test things.", authority.Definitions[0].Short)
}

func TestRunnerCodeRegistrationFailureDoesNotExecute(t *testing.T) {
	var executed atomic.Bool
	service, run, _, params := newCodeService(t, &codeTestEnvironment{execute: func(context.Context, agentenv.ToolRequest, agentenv.ToolUpdateSink) (agentenv.ToolExecution, error) {
		executed.Store(true)
		return agentenv.ToolExecution{}, nil
	}})
	service.peer = &modelHelperPeer{call: func(context.Context, string, any, any) error { return errors.New("lost acknowledgement") }}
	authority, err := service.codeExecutionContext(t.Context(), run, params)
	require.NoError(t, err)
	_, err = authority.Call(t.Context(), "test_tool", `{}`, "child")
	var failure *tools.CodeToolError
	require.ErrorAs(t, err, &failure)
	assert.Equal(t, "not_started", failure.Outcome)
	assert.False(t, executed.Load())
}

func TestRunnerCodeChildCancellation(t *testing.T) {
	started := make(chan struct{})
	service, run, peer, params := newCodeService(t, &codeTestEnvironment{execute: func(ctx context.Context, _ agentenv.ToolRequest, _ agentenv.ToolUpdateSink) (agentenv.ToolExecution, error) {
		close(started)
		<-ctx.Done()
		return agentenv.ToolExecution{}, ctx.Err()
	}})
	parent, cancel := context.WithCancel(t.Context())
	defer cancel()
	authority, err := service.codeExecutionContext(parent, run, params)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		_, err := authority.Call(t.Context(), "test_tool", `{}`, "child")
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("child did not start")
	}
	cancel()
	select {
	case err := <-done:
		var failure *tools.CodeToolError
		require.ErrorAs(t, err, &failure)
		assert.Equal(t, "cancelled", failure.Kind)
		assert.Equal(t, "unknown", failure.Outcome)
	case <-time.After(time.Second):
		t.Fatal("child did not stop")
	}
	run.ops.Wait()
	assert.Equal(t, []string{protocol.MethodToolChildBegin, protocol.MethodToolChildEnd}, peer.calls)
}

func TestRunnerCodeModeNegotiation(t *testing.T) {
	for _, test := range []struct {
		name, mode                    string
		supported, noTools, wantError bool
	}{
		{name: "on supported", mode: "on", supported: true},
		{name: "on legacy direct tools", mode: "on"},
		{name: "only supported", mode: "only", supported: true},
		{name: "only rejects legacy daemon", mode: "only", wantError: true},
		{name: "only permits explicit no-tools", mode: "only", noTools: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime := extensions.EmptyRuntime()
			t.Cleanup(func() { require.NoError(t, runtime.Close()) })
			service, err := NewService(t.Context(), t.TempDir(), ServiceOptions{
				RuntimeProvider: staticRuntimeProvider{runtime: runtime},
				ConfigLoader:    func(string) (llmtypes.Config, error) { return llmtypes.Config{CodeMode: test.mode}, nil },
			})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, service.Close()) })
			require.NoError(t, service.SetRegistration(protocol.RegisterResult{
				RunnerID:      "runner-1",
				Generation:    1,
				CodeExecution: test.supported,
			}))
			var options *llmtypes.ExecutionOptions
			if test.noTools {
				options = &llmtypes.ExecutionOptions{NoTools: new(true)}
			}
			probe, probeErr := service.ProbeManifestForCWDWithOptions(t.Context(), "", "", options)
			manifest, err := service.openRun(t.Context(), protocol.RunOpenParams{
				RunID:          "run",
				ConversationID: "conversation",
				CodeExecution:  test.supported,
				Options:        options,
			})
			if test.wantError {
				require.ErrorContains(t, probeErr, "code_mode only requires code execution support")
				require.ErrorContains(t, err, "code_mode only requires code execution support")
				assert.Empty(t, service.runs, "failed negotiation must release the run")
				return
			}
			require.NoError(t, probeErr)
			require.NoError(t, err)
			assert.Equal(t, probe.Digest, manifest.Digest, "idle discovery and run.open must negotiate the same fields")
			assert.Equal(t, test.supported, manifest.Capabilities.CodeExecution)
			if test.supported {
				assert.Contains(t, manifestToolNames(manifest), "code_execute")
				assert.Equal(t, test.mode, manifest.Config.CodeMode)
			} else {
				assert.NotContains(t, manifestToolNames(manifest), "code_execute")
				assert.Empty(t, manifest.Config.CodeMode)
				for _, tool := range manifest.Tools {
					assert.Nil(t, tool.OutputSchema)
					assert.Empty(t, tool.Group)
				}
			}
		})
	}
}

func TestRunnerCodeReplyMetadata(t *testing.T) {
	for _, metadata := range []tooltypes.ToolMetadata{
		&tooltypes.BashMetadata{Output: "shell", Truncation: &tooltypes.BashOutputTruncation{Truncated: true}},
		tooltypes.FileReadMetadata{Lines: []string{"first", "last"}, Truncated: true},
		&tooltypes.ExtensionToolMetadata{Output: "extension", Truncated: true},
		tooltypes.WebFetchMetadata{Content: "web"},
	} {
		t.Run(metadata.ToolType(), func(t *testing.T) {
			reply := codeReply(runnerpayload.ToolExecuteResult{Result: runnerpayload.ToolResult{
				Structured: tooltypes.StructuredToolResult{ToolName: metadata.ToolType(), Success: true, Metadata: metadata},
			}})
			assert.Equal(t, []tooltypes.ToolAttachment{}, reply.Attachments, "the public envelope always has an attachment array")
			switch metadata.ToolType() {
			case "bash":
				assert.Equal(t, "shell", reply.Text)
				assert.True(t, reply.Truncated)
			case "file_read":
				assert.Equal(t, "first\nlast", reply.Text)
				assert.True(t, reply.Truncated)
			case "extension_tool":
				assert.Equal(t, "extension", reply.Text)
				assert.True(t, reply.Truncated)
			case "web_fetch":
				assert.Equal(t, "web", reply.Text)
			}
		})
	}
}

func TestRunnerCodeFailureProvenanceSurvivesHooks(t *testing.T) {
	for _, failure := range []struct{ kind, outcome string }{
		{"invalid_input", "not_started"}, {"blocked", "not_started"}, {"transport", "unknown"}, {"tool_error", "completed"},
	} {
		t.Run(failure.kind, func(t *testing.T) {
			service, run, _, params := newCodeService(t, &codeTestEnvironment{execute: func(context.Context, agentenv.ToolRequest, agentenv.ToolUpdateSink) (agentenv.ToolExecution, error) {
				return agentenv.ToolExecution{
					Input: "{}", Modified: true, FailureKind: failure.kind, FailureOutcome: failure.outcome,
					Result: tooltypes.BaseToolResult{Error: "private error"},
					StructuredResult: tooltypes.StructuredToolResult{
						ToolName: "test_tool", Success: true, Metadata: &tooltypes.ExtensionToolMetadata{Output: "redacted"},
					},
				}, nil
			}})
			authority, err := service.codeExecutionContext(t.Context(), run, params)
			require.NoError(t, err)
			_, err = authority.Call(t.Context(), "test_tool", `{}`, "child")
			var childError *tools.CodeToolError
			require.ErrorAs(t, err, &childError)
			assert.Equal(t, failure.kind, childError.Kind)
			assert.Equal(t, failure.outcome, childError.Outcome)
			assert.Equal(t, "redacted", childError.Result.Text)
			assert.NotContains(t, childError.Message, "private error")
		})
	}
}

func TestRunnerCodeModifiedParentSerialization(t *testing.T) {
	result := serializeToolResult(tooltypes.BaseToolResult{Result: "raw secret", Error: "raw secret"}, tooltypes.StructuredToolResult{
		ToolName: "code_execute", Success: true, Data: "raw secret",
		Metadata: tooltypes.CodeExecutionMetadata{Status: "completed", Items: []tooltypes.CodeExecutionOutput{
			{Type: "json", Value: json.RawMessage(`"redacted"`)},
			{Type: "image", ArtifactID: "kept"},
		}},
		Attachments: []tooltypes.ToolAttachment{{Type: "image", ArtifactID: "kept", MimeType: "image/png"}},
	}, true)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "raw secret")
	assert.Contains(t, string(encoded), "redacted")
	require.Len(t, result.ContentParts, 4)
	assert.Equal(t, "kept", result.ContentParts[3].ArtifactID)
}

func TestRunnerCodeImagePermissionAndDetail(t *testing.T) {
	service, run, _, params := newCodeService(t, &codeTestEnvironment{})
	run.manifest.Tools = append(run.manifest.Tools, runnerpayload.ToolDefinition{Name: "view_image", Placement: "environment"})
	authority, err := service.codeExecutionContext(t.Context(), run, params)
	require.NoError(t, err)
	assert.ErrorContains(t, authority.ValidateImage(""), "view_image permission", "manifest membership alone does not grant access")
	params.CallableTools = new([]string{"view_image"})
	authority, err = service.codeExecutionContext(t.Context(), run, params)
	require.NoError(t, err)
	assert.NoError(t, authority.ValidateImage(""))
	run.config.Model = "gpt-4.1"
	assert.ErrorContains(t, authority.ValidateImage("original"), "compatible models")
	run.config.Model = "gpt-5.4"
	assert.NoError(t, authority.ValidateImage("original"))
	run.codeAllowedTools = new([]string{"code_execute"})
	assert.ErrorContains(t, authority.ValidateImage(""), "view_image permission", "check current restrictions at emission time")
}
