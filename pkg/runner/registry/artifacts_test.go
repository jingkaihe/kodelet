package registry

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArtifactToolOwnership(t *testing.T) {
	for _, name := range []string{
		"matching owner", "wrong runner", "wrong connection", "wrong generation", "wrong run", "wrong tool",
		"stale run connection", "stale run generation", "unowned run", "canceled tool", "completed tool", "opening run",
	} {
		t.Run(name, func(t *testing.T) {
			registry, _, session := newModelHelperRegistry(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			params := runnerpayload.ToolExecuteParams{RunID: "run-one", ToolCallID: "tool-one", Name: "generate_image"}
			cleanup, err := registry.registerArtifactTool(ctx, params)
			require.NoError(t, err)
			defer cleanup()
			runnerID, connectionID, generation, _ := session.connectionIdentity()
			identity := UIRequestIdentity{RunnerID: runnerID, ConnectionID: connectionID, Generation: generation}
			switch name {
			case "wrong runner":
				other, err := registry.Register(testRegisterParams("other-host", "/work/other"), newFakeLink())
				require.NoError(t, err)
				identity = UIRequestIdentity{RunnerID: other.RunnerID, ConnectionID: other.ConnectionID, Generation: other.Generation}
			case "wrong connection":
				identity.ConnectionID = "another-connection"
			case "wrong generation":
				identity.Generation++
			case "wrong run":
				params.RunID = "another-run"
			case "wrong tool":
				params.ToolCallID = "another-tool"
			case "canceled tool":
				cancel()
			case "completed tool":
				cleanup()
			case "stale run connection", "stale run generation", "unowned run", "opening run":
				registry.mu.Lock()
				switch name {
				case "stale run connection":
					registry.runs[params.RunID].connectionID = "old-connection"
				case "stale run generation":
					registry.runs[params.RunID].generation--
				case "unowned run":
					removeRunnerActiveRun(registry.runners[runnerID], params.RunID)
				case "opening run":
					registry.runs[params.RunID].Status = RunStatusOpening
				}
				registry.mu.Unlock()
			}
			owner, conversationID, err := registry.ArtifactToolContext(identity, params.RunID, params.ToolCallID)
			if name == "matching owner" {
				require.NoError(t, err)
				require.NotNil(t, owner)
				assert.NoError(t, owner.Err())
				assert.Equal(t, "conversation-one", conversationID)
			} else {
				require.Error(t, err)
				assert.Nil(t, owner)
				assert.Empty(t, conversationID)
			}
		})
	}
}

func TestArtifactToolExecuteLifetime(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
	}{
		{name: "success"},
		{name: "RPC rejection", err: &protocol.RPCError{Code: protocol.ErrorCodeInvalidParams, Message: "tool rejected"}},
		{name: "transport failure", err: protocol.ErrPeerClosed},
	} {
		t.Run(tt.name, func(t *testing.T) {
			registry, link, session := newModelHelperRegistry(t)
			runnerID, connectionID, generation, _ := session.connectionIdentity()
			identity := UIRequestIdentity{RunnerID: runnerID, ConnectionID: connectionID, Generation: generation}
			params := runnerpayload.ToolExecuteParams{RunID: "run-one", ToolCallID: "tool-one", Name: "generate_image"}
			type ownerKey struct{}
			ctx := context.WithValue(t.Context(), ownerKey{}, "server-owned-context")
			var owner context.Context
			link.mu.Lock()
			link.call = func(_ context.Context, method string, _, _ any) error {
				require.Equal(t, protocol.MethodToolExecute, method)
				var conversationID string
				var err error
				owner, conversationID, err = registry.ArtifactToolContext(identity, params.RunID, params.ToolCallID)
				require.NoError(t, err)
				assert.Equal(t, "conversation-one", conversationID)
				assert.Equal(t, "server-owned-context", owner.Value(ownerKey{}))
				_, err = registry.ExecuteTool(ctx, params, nil)
				assert.ErrorContains(t, err, "already active")
				return tt.err
			}
			link.mu.Unlock()
			_, err := registry.ExecuteTool(ctx, params, nil)
			if tt.err == nil {
				require.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, tt.err)
			}
			require.NotNil(t, owner)
			assert.ErrorIs(t, owner.Err(), context.Canceled)
			_, _, err = registry.ArtifactToolContext(identity, params.RunID, params.ToolCallID)
			assert.Error(t, err)
			registry.mu.RLock()
			assert.Empty(t, registry.artifactTools)
			registry.mu.RUnlock()
		})
	}
}

func TestArtifactToolRunRevocation(t *testing.T) {
	for _, action := range []string{"cancel", "close", "environment failure", "disconnect", "reconnect", "registry close"} {
		t.Run(action, func(t *testing.T) {
			registry, link, session := newModelHelperRegistry(t)
			cleanup, err := registry.registerArtifactTool(t.Context(), runnerpayload.ToolExecuteParams{RunID: "run-one", ToolCallID: "tool-one"})
			require.NoError(t, err)
			defer cleanup()
			runnerID, connectionID, generation, _ := session.connectionIdentity()
			identity := UIRequestIdentity{RunnerID: runnerID, ConnectionID: connectionID, Generation: generation}
			owner, _, err := registry.ArtifactToolContext(identity, "run-one", "tool-one")
			require.NoError(t, err)
			if action == "cancel" || action == "close" {
				link.mu.Lock()
				link.call = func(context.Context, string, any, any) error {
					assert.ErrorIs(t, owner.Err(), context.Canceled, "revoke uploads before waiting for the runner's cleanup RPC")
					_, _, err := registry.ArtifactToolContext(identity, "run-one", "tool-one")
					assert.Error(t, err)
					return nil
				}
				link.mu.Unlock()
			}
			switch action {
			case "cancel":
				require.NoError(t, registry.CancelRun(t.Context(), "run-one", "user stopped"))
			case "close":
				require.NoError(t, registry.CloseRun(t.Context(), "run-one", RunStatusSucceeded, nil))
			case "environment failure":
				require.NoError(t, registry.EnvironmentError(runnerID, connectionID, generation, protocol.EnvironmentErrorParams{RunID: "run-one", Message: "failed"}))
			case "disconnect":
				registry.Detach(runnerID, connectionID, generation, protocol.ErrPeerClosed)
			case "reconnect":
				_, err := registry.Register(testRegisterParams("host-one", "/work/project"), newFakeLink())
				require.NoError(t, err)
			case "registry close":
				require.NoError(t, registry.Close())
			}
			assert.ErrorIs(t, owner.Err(), context.Canceled)
			_, _, err = registry.ArtifactToolContext(identity, "run-one", "tool-one")
			assert.Error(t, err)
			registry.mu.RLock()
			assert.Empty(t, registry.artifactTools)
			registry.mu.RUnlock()
		})
	}
}

func TestArtifactToolRegistrationValidation(t *testing.T) {
	for _, name := range []string{"missing run", "missing tool", "unknown run", "opening run", "canceled run", "completed run", "canceled context"} {
		t.Run(name, func(t *testing.T) {
			registry, _, _ := newModelHelperRegistry(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			params := runnerpayload.ToolExecuteParams{RunID: "run-one", ToolCallID: "tool-one"}
			switch name {
			case "missing run":
				params.RunID = " "
			case "missing tool":
				params.ToolCallID = " "
			case "unknown run":
				params.RunID = "unknown"
			case "opening run":
				registry.mu.Lock()
				registry.runs[params.RunID].Status = RunStatusOpening
				registry.mu.Unlock()
			case "canceled run":
				require.NoError(t, registry.CancelRun(t.Context(), params.RunID, "user stopped"))
			case "completed run":
				require.NoError(t, registry.CloseRun(t.Context(), params.RunID, RunStatusSucceeded, nil))
			case "canceled context":
				cancel()
			}
			cleanup, err := registry.registerArtifactTool(ctx, params)
			if cleanup != nil {
				defer cleanup()
			}
			require.Error(t, err)
			assert.Nil(t, cleanup)
		})
	}
}

func TestArtifactToolCleanupIsolation(t *testing.T) {
	registry, _, session := newModelHelperRegistry(t)
	runnerID, connectionID, generation, _ := session.connectionIdentity()
	identity := UIRequestIdentity{RunnerID: runnerID, ConnectionID: connectionID, Generation: generation}
	params := runnerpayload.ToolExecuteParams{RunID: "run-one", ToolCallID: "tool-one"}
	cleanup, err := registry.registerArtifactTool(t.Context(), params)
	require.NoError(t, err)
	cleanup()
	replacementCleanup, err := registry.registerArtifactTool(t.Context(), params)
	require.NoError(t, err)
	defer replacementCleanup()
	cleanup() // An old deferred cleanup must not revoke a replacement registration.
	_, _, err = registry.ArtifactToolContext(identity, params.RunID, params.ToolCallID)
	require.NoError(t, err)

	_, err = registry.OpenRun(t.Context(), runnerID, testRunOpenParams("run-two", "conversation-two"))
	require.NoError(t, err)
	otherCleanup, err := registry.registerArtifactTool(t.Context(), runnerpayload.ToolExecuteParams{RunID: "run-two", ToolCallID: "tool-one"})
	require.NoError(t, err)
	defer otherCleanup()
	require.NoError(t, registry.CancelRun(t.Context(), "run-one", "cancel first run"))
	owner, conversationID, err := registry.ArtifactToolContext(identity, "run-two", "tool-one")
	require.NoError(t, err)
	assert.NoError(t, owner.Err())
	assert.Equal(t, "conversation-two", conversationID)
}

type artifactRequestRouter struct {
	fakeUIRequestRouter
}

func (r *artifactRequestRouter) HandleRunnerArtifactRequest(ctx context.Context, identity UIRequestIdentity, method string, params json.RawMessage) (any, *protocol.RPCError) {
	return r.HandleRunnerUIRequest(ctx, identity, method, params)
}

func TestArtifactSessionReverseRPCRouting(t *testing.T) {
	registry := newTestRegistry(t)
	router := &artifactRequestRouter{}
	session := NewSession(registry, router)
	session.Attach(newFakeLink())
	request := json.RawMessage(`{"runId":"run-one","toolCallId":"tool-one","runnerId":"forged","connectionId":"forged","generation":999}`)
	for _, method := range []string{runnerpayload.MethodArtifactUpload, runnerpayload.MethodArtifactResolve} {
		value, rpcErr := session.HandleRequest(t.Context(), method, request)
		assert.Nil(t, value)
		require.NotNil(t, rpcErr)
		assert.Equal(t, protocol.ErrorCodeInvalidRequest, rpcErr.Code)
		assert.Empty(t, router.method)
	}
	value, rpcErr := session.HandleRequest(t.Context(), protocol.MethodRunnerRegister, mustRegistryJSON(t, testRegisterParams("host-one", "/work/project")))
	require.Nil(t, rpcErr)
	registered := value.(protocol.RegisterResult)
	for _, method := range []string{runnerpayload.MethodArtifactUpload, runnerpayload.MethodArtifactResolve} {
		value, rpcErr = session.HandleRequest(t.Context(), method, request)
		require.Nil(t, rpcErr)
		assert.Equal(t, map[string]bool{"ok": true}, value)
		assert.Equal(t, UIRequestIdentity{RunnerID: registered.RunnerID, ConnectionID: registered.ConnectionID, Generation: registered.Generation}, router.identity)
		assert.Equal(t, method, router.method)
		assert.Equal(t, request, router.params)
	}
	session.ui = nil
	for _, method := range []string{runnerpayload.MethodArtifactUpload, runnerpayload.MethodArtifactResolve} {
		value, rpcErr = session.HandleRequest(t.Context(), method, request)
		assert.Nil(t, value)
		require.NotNil(t, rpcErr)
		assert.Equal(t, protocol.ErrorCodeUnavailable, rpcErr.Code)
	}
}
