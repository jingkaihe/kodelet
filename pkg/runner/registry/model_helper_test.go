package registry

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newModelHelperRegistry(t *testing.T) (*Registry, *fakeLink, *Session) {
	t.Helper()
	registry := newTestRegistry(t)
	link := newFakeLink()
	session := NewSession(registry, nil)
	session.Attach(link)
	value, rpcErr := session.HandleRequest(t.Context(), protocol.MethodRunnerRegister, mustRegistryJSON(t, testRegisterParams("host-one", "/work/project")))
	require.Nil(t, rpcErr)
	registration := value.(protocol.RegisterResult)
	configureManifestLink(t, link, registration)
	markRunnerReady(t, registry, registration)
	_, err := registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-one", "conversation-one"))
	require.NoError(t, err)
	return registry, link, session
}

func testModelHelperParams() runnerpayload.ModelHelperParams {
	return runnerpayload.ModelHelperParams{
		RunID: "run-one", ToolCallID: "tool-one",
		Request: tooltypes.ModelHelperRequest{Operation: tooltypes.ModelHelperWebFetchExtract, URL: "https://example.com", Content: "document", Prompt: "Extract title"},
	}
}

func TestModelHelperRequiresRegistration(t *testing.T) {
	session := NewSession(newTestRegistry(t), nil)
	value, rpcErr := session.HandleRequest(t.Context(), runnerpayload.MethodModelHelperExecute, mustRegistryJSON(t, testModelHelperParams()))
	assert.Nil(t, value)
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeInvalidRequest, rpcErr.Code)
}

func TestModelHelperAuthorization(t *testing.T) {
	for _, name := range []string{"absent capability", "wrong tool type", "wrong run", "wrong tool", "wrong runner", "wrong connection", "wrong generation", "stale run connection", "stale run generation", "inactive run", "unowned run", "canceled tool", "unsupported operation", "missing prompt"} {
		t.Run(name, func(t *testing.T) {
			registry, _, session := newModelHelperRegistry(t)
			calls := 0
			ctx, cancel := context.WithCancel(tooltypes.ContextWithModelHelper(t.Context(), func(context.Context, tooltypes.ModelHelperRequest) (string, error) {
				calls++
				return "extracted", nil
			}))
			defer cancel()
			tool := runnerpayload.ToolExecuteParams{RunID: "run-one", ToolCallID: "tool-one", Name: "web_fetch"}
			if name == "absent capability" {
				ctx = tooltypes.ContextWithModelHelper(ctx, nil)
			}
			if name == "wrong tool type" {
				tool.Name = "bash"
			}
			cleanup, err := registry.registerToolModelHelper(ctx, tool)
			require.NoError(t, err)
			defer cleanup()
			params := testModelHelperParams()
			runnerID, connectionID, generation, _ := session.connectionIdentity()
			wantCode := protocol.ErrorCodeStale
			switch name {
			case "wrong run":
				params.RunID = "run-other"
			case "wrong tool":
				params.ToolCallID = "tool-other"
			case "wrong runner":
				other, err := registry.Register(testRegisterParams("host-other", "/work/other"), newFakeLink())
				require.NoError(t, err)
				runnerID, connectionID, generation = other.RunnerID, other.ConnectionID, other.Generation
			case "wrong connection":
				connectionID = "connection-other"
			case "wrong generation":
				generation++
			case "stale run connection", "stale run generation", "inactive run", "unowned run":
				registry.mu.Lock()
				switch name {
				case "stale run connection":
					registry.runs[params.RunID].connectionID = "old-connection"
				case "stale run generation":
					registry.runs[params.RunID].generation--
				case "inactive run":
					registry.runs[params.RunID].Status = RunStatusCanceled
				case "unowned run":
					removeRunnerActiveRun(registry.runners[runnerID], params.RunID)
				}
				registry.mu.Unlock()
			case "canceled tool":
				cancel()
			case "unsupported operation":
				params.Request.Operation = "agent.run"
				wantCode = protocol.ErrorCodeInvalidParams
			case "missing prompt":
				params.Request.Prompt = ""
				wantCode = protocol.ErrorCodeInvalidParams
			}
			value, rpcErr := registry.executeModelHelper(t.Context(), runnerID, connectionID, generation, params)
			assert.Nil(t, value)
			require.NotNil(t, rpcErr)
			assert.Equal(t, wantCode, rpcErr.Code)
			assert.Zero(t, calls)
			registry.mu.RLock()
			if registration := registry.modelHelpers[modelHelperKey{"run-one", "tool-one"}]; registration != nil {
				assert.False(t, registration.used, "rejected requests must not consume another tool's capability")
			}
			registry.mu.RUnlock()
		})
	}
}

func TestModelHelperToolLifetimeAndReplay(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
	}{{name: "success"}, {name: "helper failure", err: errors.New("provider failed")}} {
		t.Run(tt.name, func(t *testing.T) {
			helperErr := tt.err
			registry, link, session := newModelHelperRegistry(t)
			calls := 0
			var helperCtx context.Context
			ctx := tooltypes.ContextWithModelHelper(t.Context(), func(ctx context.Context, request tooltypes.ModelHelperRequest) (string, error) {
				calls++
				helperCtx = ctx
				assert.Equal(t, testModelHelperParams().Request, request)
				// Reenter the registry to prove no registry lock is held by the helper.
				_, found := registry.Run("run-one")
				assert.True(t, found)
				return "extracted", helperErr
			})
			request := mustRegistryJSON(t, testModelHelperParams())
			_, rpcErr := session.HandleRequest(ctx, runnerpayload.MethodModelHelperExecute, request)
			require.NotNil(t, rpcErr)
			assert.Equal(t, protocol.ErrorCodeStale, rpcErr.Code)
			link.mu.Lock()
			link.call = func(_ context.Context, method string, _, _ any) error {
				assert.Equal(t, protocol.MethodToolExecute, method)
				value, rpcErr := session.HandleRequest(t.Context(), runnerpayload.MethodModelHelperExecute, request)
				if helperErr != nil {
					assert.Nil(t, value)
					if assert.NotNil(t, rpcErr) {
						assert.Equal(t, protocol.ErrorCodeUnavailable, rpcErr.Code)
					}
				} else {
					assert.Nil(t, rpcErr)
					assert.Equal(t, runnerpayload.ModelHelperResult{Text: "extracted"}, value)
				}
				_, rpcErr = session.HandleRequest(t.Context(), runnerpayload.MethodModelHelperExecute, request)
				if assert.NotNil(t, rpcErr) {
					assert.Equal(t, protocol.ErrorCodeConflict, rpcErr.Code)
				}
				return helperErr
			}
			link.mu.Unlock()
			_, err := registry.ExecuteTool(ctx, runnerpayload.ToolExecuteParams{RunID: "run-one", ToolCallID: "tool-one", Name: "web_fetch"}, nil)
			if helperErr != nil {
				assert.ErrorIs(t, err, helperErr)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, 1, calls)
			require.NotNil(t, helperCtx)
			assert.ErrorIs(t, helperCtx.Err(), context.Canceled)
			assert.Empty(t, registry.modelHelpers)
			_, rpcErr = session.HandleRequest(t.Context(), runnerpayload.MethodModelHelperExecute, request)
			require.NotNil(t, rpcErr)
			assert.Equal(t, protocol.ErrorCodeStale, rpcErr.Code)
		})
	}
}

func TestModelHelperCancellationAndCleanup(t *testing.T) {
	for _, action := range []string{"RPC cancellation", "parent cancellation", "tool completion", "run cancellation", "run close", "disconnect", "reconnect", "registry close"} {
		t.Run(action, func(t *testing.T) {
			registry, _, session := newModelHelperRegistry(t)
			started := make(chan struct{})
			var calls atomic.Int32
			parentCtx, parentCancel := context.WithCancel(tooltypes.ContextWithModelHelper(t.Context(), func(ctx context.Context, _ tooltypes.ModelHelperRequest) (string, error) {
				calls.Add(1)
				close(started)
				<-ctx.Done()
				// Even a provider that returns success after cancellation cannot
				// turn a revoked helper into a successful response.
				return "late result", nil
			}))
			defer parentCancel()
			cleanup, err := registry.registerToolModelHelper(parentCtx, runnerpayload.ToolExecuteParams{RunID: "run-one", ToolCallID: "tool-one", Name: "web_fetch"})
			require.NoError(t, err)
			defer cleanup()
			rpcCtx, rpcCancel := context.WithCancel(t.Context())
			defer rpcCancel()
			request := mustRegistryJSON(t, testModelHelperParams())
			done := make(chan *protocol.RPCError, 1)
			go func() {
				_, rpcErr := session.HandleRequest(rpcCtx, runnerpayload.MethodModelHelperExecute, request)
				done <- rpcErr
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				require.FailNow(t, "helper did not start")
			}
			// Concurrent replay is rejected while the first request is in flight.
			_, rpcErr := session.HandleRequest(t.Context(), runnerpayload.MethodModelHelperExecute, request)
			require.NotNil(t, rpcErr)
			assert.Equal(t, protocol.ErrorCodeConflict, rpcErr.Code)
			runnerID, connectionID, generation, _ := session.connectionIdentity()
			switch action {
			case "RPC cancellation":
				rpcCancel()
			case "parent cancellation":
				parentCancel()
			case "tool completion":
				cleanup()
			case "run cancellation":
				require.NoError(t, registry.CancelRun(t.Context(), "run-one", "test cancellation"))
				require.NoError(t, registry.CancelRun(t.Context(), "run-one", "repeated cancellation"))
			case "run close":
				require.NoError(t, registry.CloseRun(t.Context(), "run-one", RunStatusSucceeded, nil))
			case "disconnect":
				registry.Detach(runnerID, connectionID, generation, protocol.ErrPeerClosed)
			case "reconnect":
				_, err := registry.Register(testRegisterParams("host-one", "/work/project"), newFakeLink())
				require.NoError(t, err)
			case "registry close":
				require.NoError(t, registry.Close())
			}
			select {
			case rpcErr := <-done:
				require.NotNil(t, rpcErr)
				assert.Equal(t, protocol.ErrorCodeUnavailable, rpcErr.Code)
				assert.Contains(t, rpcErr.Message, context.Canceled.Error())
			case <-time.After(3 * time.Second):
				require.FailNow(t, "helper cancellation did not propagate")
			}
			_, rpcErr = session.HandleRequest(t.Context(), runnerpayload.MethodModelHelperExecute, request)
			require.NotNil(t, rpcErr)
			if action == "RPC cancellation" {
				assert.Equal(t, protocol.ErrorCodeConflict, rpcErr.Code, "RPC cancellation must not reset one-use authority")
			} else {
				assert.Equal(t, protocol.ErrorCodeStale, rpcErr.Code)
			}
			assert.Equal(t, int32(1), calls.Load())
			if action == "RPC cancellation" || action == "parent cancellation" {
				cleanup() // ExecuteTool owns final map cleanup when the tool returns.
			}
			registry.mu.RLock()
			assert.Empty(t, registry.modelHelpers)
			registry.mu.RUnlock()
		})
	}
}

func TestModelHelperRegistrationValidation(t *testing.T) {
	for _, name := range []string{"missing run", "missing tool", "unknown run", "opening run", "canceled context", "duplicate"} {
		t.Run(name, func(t *testing.T) {
			registry, _, _ := newModelHelperRegistry(t)
			ctx, cancel := context.WithCancel(tooltypes.ContextWithModelHelper(t.Context(), func(context.Context, tooltypes.ModelHelperRequest) (string, error) { return "", nil }))
			defer cancel()
			params := runnerpayload.ToolExecuteParams{RunID: "run-one", ToolCallID: "tool-one", Name: "web_fetch"}
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
			case "canceled context":
				cancel()
			case "duplicate":
				cleanup, err := registry.registerToolModelHelper(ctx, params)
				require.NoError(t, err)
				defer cleanup()
			}
			cleanup, err := registry.registerToolModelHelper(ctx, params)
			require.Error(t, err)
			assert.Nil(t, cleanup)
		})
	}
}

func TestModelHelperCleanupDoesNotRevokeAnotherTool(t *testing.T) {
	registry, _, session := newModelHelperRegistry(t)
	ctx := tooltypes.ContextWithModelHelper(t.Context(), func(context.Context, tooltypes.ModelHelperRequest) (string, error) { return "extracted", nil })
	tool := runnerpayload.ToolExecuteParams{RunID: "run-one", ToolCallID: "tool-one", Name: "web_fetch"}
	cleanup, err := registry.registerToolModelHelper(ctx, tool)
	require.NoError(t, err)
	cleanup()

	// Deferred cleanup of an old call cannot revoke a replacement registration.
	replacementCleanup, err := registry.registerToolModelHelper(ctx, tool)
	require.NoError(t, err)
	defer replacementCleanup()
	cleanup()
	value, rpcErr := session.HandleRequest(t.Context(), runnerpayload.MethodModelHelperExecute, mustRegistryJSON(t, testModelHelperParams()))
	require.Nil(t, rpcErr)
	assert.Equal(t, runnerpayload.ModelHelperResult{Text: "extracted"}, value)

	// Run-level cleanup must not affect another active run on the same runner.
	runnerID, _, _, _ := session.connectionIdentity()
	_, err = registry.OpenRun(t.Context(), runnerID, testRunOpenParams("run-two", "conversation-two"))
	require.NoError(t, err)
	tool.RunID = "run-two"
	otherCleanup, err := registry.registerToolModelHelper(ctx, tool)
	require.NoError(t, err)
	defer otherCleanup()
	require.NoError(t, registry.CancelRun(t.Context(), "run-one", "cancel only this run"))
	request := testModelHelperParams()
	request.RunID = "run-two"
	value, rpcErr = session.HandleRequest(t.Context(), runnerpayload.MethodModelHelperExecute, mustRegistryJSON(t, request))
	require.Nil(t, rpcErr)
	assert.Equal(t, runnerpayload.ModelHelperResult{Text: "extracted"}, value)
}

func TestModelHelperExpiredRPCCannotInvokeProvider(t *testing.T) {
	registry, _, session := newModelHelperRegistry(t)
	ctx := tooltypes.ContextWithModelHelper(t.Context(), func(context.Context, tooltypes.ModelHelperRequest) (string, error) {
		assert.Fail(t, "expired RPC invoked provider")
		return "", nil
	})
	cleanup, err := registry.registerToolModelHelper(ctx, runnerpayload.ToolExecuteParams{RunID: "run-one", ToolCallID: "tool-one", Name: "web_fetch"})
	require.NoError(t, err)
	defer cleanup()
	expired, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	request := mustRegistryJSON(t, testModelHelperParams())
	value, rpcErr := session.HandleRequest(expired, runnerpayload.MethodModelHelperExecute, request)
	assert.Nil(t, value)
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeUnavailable, rpcErr.Code)
	_, rpcErr = session.HandleRequest(t.Context(), runnerpayload.MethodModelHelperExecute, request)
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeConflict, rpcErr.Code)
}
