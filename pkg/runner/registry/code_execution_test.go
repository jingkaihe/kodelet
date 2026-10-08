package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newCodeRegistry(t *testing.T) (*Registry, *Session, runnerpayload.ToolExecuteParams) {
	t.Helper()
	registry, _, session := newModelHelperRegistry(t)
	registry.mu.Lock()
	run := registry.runs["run-one"]
	var manifest runnerpayload.Manifest
	require.NoError(t, json.Unmarshal([]byte(run.ManifestJSON), &manifest))
	manifest.Capabilities.CodeExecution = true
	manifest.Tools = append(manifest.Tools,
		runnerpayload.ToolDefinition{Name: "code_execute", Placement: "environment"},
		runnerpayload.ToolDefinition{Name: "web_fetch", Placement: "environment"},
	)
	digest, err := runnerpayload.ComputeManifestDigest(manifest)
	require.NoError(t, err)
	manifest.Digest = digest
	payload, err := json.Marshal(manifest)
	require.NoError(t, err)
	run.codeExecution, run.ManifestDigest, run.ManifestJSON = true, digest, string(payload)
	registry.mu.Unlock()
	callable := []string{"bash", "web_fetch"}
	return registry, session, runnerpayload.ToolExecuteParams{
		RunID: "run-one", ToolCallID: "parent", Name: "code_execute", Input: json.RawMessage(`{"code":"return 1"}`),
		ManifestDigest: digest, CallableTools: &callable,
	}
}

func codeRegistryIdentity(session *Session) UIRequestIdentity {
	runnerID, connectionID, generation, _ := session.connectionIdentity()
	return UIRequestIdentity{RunnerID: runnerID, ConnectionID: connectionID, Generation: generation}
}

func TestCodeParentAuthorization(t *testing.T) {
	for _, name := range []string{"missing tools", "wrong digest", "unnegotiated", "unsupported runner", "no parent", "unknown tool", "recursive tool", "empty allowlist", "no tools"} {
		t.Run(name, func(t *testing.T) {
			registry, _, params := newCodeRegistry(t)
			run := registry.runs[params.RunID]
			switch name {
			case "missing tools":
				params.CallableTools = nil
			case "wrong digest":
				params.ManifestDigest = "different"
			case "unnegotiated":
				run.codeExecution = false
			case "unknown tool":
				params.CallableTools = new([]string{"missing"})
			case "recursive tool":
				params.CallableTools = new([]string{"code_execute"})
			case "empty allowlist":
				params.CallableTools = new([]string{})
			default:
				var manifest runnerpayload.Manifest
				require.NoError(t, json.Unmarshal([]byte(run.ManifestJSON), &manifest))
				switch name {
				case "unsupported runner":
					manifest.Capabilities.CodeExecution = false
				case "no parent":
					manifest.Tools = manifest.Tools[:1]
				default:
					manifest.Config.Options = &llmtypes.ExecutionOptions{NoTools: new(true)}
				}
				payload, err := json.Marshal(manifest)
				require.NoError(t, err)
				run.ManifestJSON = string(payload)
			}
			cleanup, err := registry.registerCodeParent(t.Context(), params)
			if name == "empty allowlist" {
				require.NoError(t, err)
				cleanup()
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestCodeChildrenAuthorityAndTombstones(t *testing.T) {
	registry, session, params := newCodeRegistry(t)
	ctx := tooltypes.ContextWithModelHelper(t.Context(), func(context.Context, tooltypes.ModelHelperRequest) (string, error) {
		return "extracted", nil
	})
	forker := &codeRegistryForker{}
	ctx = tools.ContextWithToolContext(ctx, tools.ToolContext{MetadataStore: forker})
	cleanup, err := registry.registerCodeParent(ctx, params)
	require.NoError(t, err)
	defer cleanup()
	identity := codeRegistryIdentity(session)
	child := runnerpayload.ToolChildParams{RunID: "run-one", ParentToolCallID: "parent", ToolCallID: "child", Name: "web_fetch"}
	_, rpcErr := session.HandleRequest(t.Context(), protocol.MethodToolChildBegin, mustRegistryJSON(t, child))
	require.Nil(t, rpcErr)
	artifactCtx, conversationID, err := registry.ArtifactToolContext(identity, child.RunID, child.ToolCallID)
	require.NoError(t, err)
	assert.Equal(t, "conversation-one", conversationID)
	helper := testModelHelperParams()
	helper.ToolCallID = child.ToolCallID
	value, rpcErr := session.HandleRequest(t.Context(), runnerpayload.MethodModelHelperExecute, mustRegistryJSON(t, helper))
	require.Nil(t, rpcErr)
	assert.Equal(t, "extracted", value.(runnerpayload.ModelHelperResult).Text)
	_, rpcErr = session.HandleRequest(t.Context(), runnerpayload.MethodModelHelperExecute, mustRegistryJSON(t, helper))
	require.NotNil(t, rpcErr, "helper grant must retain one-use semantics")
	assert.Equal(t, "web_fetch", registry.toolForkers[toolForkKey{child.RunID, child.ToolCallID}].toolName)
	_, rpcErr = session.HandleRequest(t.Context(), protocol.MethodToolChildBegin, mustRegistryJSON(t, child))
	require.NotNil(t, rpcErr)
	for range 2 {
		_, rpcErr = session.HandleRequest(t.Context(), protocol.MethodToolChildEnd, mustRegistryJSON(t, child))
		require.Nil(t, rpcErr, "end cleanup is idempotent for the same owner")
	}
	assert.ErrorIs(t, artifactCtx.Err(), context.Canceled)
	assert.NotContains(t, registry.toolForkers, toolForkKey{child.RunID, child.ToolCallID})
	_, rpcErr = session.HandleRequest(t.Context(), protocol.MethodToolChildBegin, mustRegistryJSON(t, child))
	require.NotNil(t, rpcErr, "completed IDs cannot be replayed")
	_, err = registry.registerArtifactTool(t.Context(), runnerpayload.ToolExecuteParams{RunID: child.RunID, ToolCallID: child.ToolCallID})
	require.Error(t, err, "completed child IDs cannot be reused for direct calls")
	child.ToolCallID, child.Name = "shell-child", "bash"
	_, rpcErr = session.HandleRequest(t.Context(), protocol.MethodToolChildBegin, mustRegistryJSON(t, child))
	require.Nil(t, rpcErr)
	helper.ToolCallID = child.ToolCallID
	_, rpcErr = session.HandleRequest(t.Context(), runnerpayload.MethodModelHelperExecute, mustRegistryJSON(t, helper))
	require.NotNil(t, rpcErr, "bash must not inherit web_fetch authority")
	cleanup()
	_, _, err = registry.ArtifactToolContext(identity, child.RunID, child.ToolCallID)
	require.Error(t, err)
	assert.Empty(t, registry.codeParents)
}

func TestCodeChildRegistrationFences(t *testing.T) {
	for _, name := range []string{"wrong runner", "wrong generation", "wrong connection", "wrong parent", "wrong run", "recursive", "forbidden", "same ID", "bad ID", "cancelled request"} {
		t.Run(name, func(t *testing.T) {
			registry, session, params := newCodeRegistry(t)
			cleanup, err := registry.registerCodeParent(t.Context(), params)
			require.NoError(t, err)
			defer cleanup()
			identity := codeRegistryIdentity(session)
			child := runnerpayload.ToolChildParams{RunID: "run-one", ParentToolCallID: "parent", ToolCallID: "child", Name: "bash"}
			ctx := t.Context()
			switch name {
			case "wrong runner":
				identity.RunnerID = "other"
			case "wrong generation":
				identity.Generation++
			case "wrong connection":
				identity.ConnectionID = "other"
			case "wrong parent":
				child.ParentToolCallID = "other"
			case "wrong run":
				child.RunID = "other"
			case "recursive":
				child.Name = "code_execute"
			case "forbidden":
				child.Name = "hidden"
			case "same ID":
				child.ToolCallID = "parent"
			case "bad ID":
				child.ToolCallID = " "
			case "cancelled request":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, rpcErr := registry.codeChild(ctx, identity, child, true)
			require.NotNil(t, rpcErr)
			assert.Empty(t, registry.artifactTools)
		})
	}
}

func TestCodeChildBudgetsAndCancellation(t *testing.T) {
	registry, session, params := newCodeRegistry(t)
	ctx, cancel := context.WithCancel(t.Context())
	cleanup, err := registry.registerCodeParent(ctx, params)
	require.NoError(t, err)
	defer cleanup()
	defer cancel()
	identity := codeRegistryIdentity(session)
	children := make([]runnerpayload.ToolChildParams, 4)
	for i := range children {
		children[i] = runnerpayload.ToolChildParams{RunID: "run-one", ParentToolCallID: "parent", ToolCallID: fmt.Sprint(i), Name: "bash"}
		_, rpcErr := registry.codeChild(t.Context(), identity, children[i], true)
		require.Nil(t, rpcErr)
	}
	extra := runnerpayload.ToolChildParams{RunID: "run-one", ParentToolCallID: "parent", ToolCallID: "extra", Name: "bash"}
	_, rpcErr := registry.codeChild(t.Context(), identity, extra, true)
	require.NotNil(t, rpcErr)
	for _, child := range children {
		_, rpcErr := registry.codeChild(t.Context(), identity, child, false)
		require.Nil(t, rpcErr)
	}
	for i := 4; i < 128; i++ {
		extra.ToolCallID = fmt.Sprint(i)
		_, rpcErr := registry.codeChild(t.Context(), identity, extra, true)
		require.Nil(t, rpcErr)
		_, rpcErr = registry.codeChild(t.Context(), identity, extra, false)
		require.Nil(t, rpcErr)
	}
	extra.ToolCallID = "over-limit"
	_, rpcErr = registry.codeChild(t.Context(), identity, extra, true)
	require.NotNil(t, rpcErr)
	cancel()
	require.Eventually(t, func() bool {
		registry.mu.RLock()
		defer registry.mu.RUnlock()
		return len(registry.codeParents) == 0 && len(registry.artifactTools) == 0
	}, time.Second, time.Millisecond)
}

type codeRegistryForker struct{}

func (*codeRegistryForker) GetMetadata() map[string]any  { return nil }
func (*codeRegistryForker) SetMetadataValue(string, any) {}
func (*codeRegistryForker) ForkConversation(context.Context) (string, error) {
	return "child-conversation", nil
}
