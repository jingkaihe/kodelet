package registry

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newCheckpointRegistry(t *testing.T) (*Registry, *fakeLink, *Session, protocol.RegisterResult) {
	t.Helper()
	r := newTestRegistry(t)
	link := newFakeLink()
	session := NewSession(r, nil)
	session.Attach(link)
	params := testRegisterParams("checkpoint-host", "/work/project")
	params.Capabilities.RunCheckpoint = true
	value, rpcErr := session.HandleRequest(t.Context(), protocol.MethodRunnerRegister, mustRegistryJSON(t, params))
	require.Nil(t, rpcErr)
	registration := value.(protocol.RegisterResult)
	configureManifestLink(t, link, registration)
	markRunnerReady(t, r, registration)
	return r, link, session, registration
}

func TestRunCheckpointOwnershipAndReplay(t *testing.T) {
	r, link, session, registration := newCheckpointRegistry(t)
	request := protocol.RunCheckpointParams{RunID: "run", CWD: "/work/project"}
	_, rpcErr := session.HandleRequest(t.Context(), protocol.MethodRunCheckpoint, mustRegistryJSON(t, request))
	require.NotNil(t, rpcErr, "saved IDs alone must not grant checkpoint authority")
	saves := 0
	baseCall := link.call
	link.call = func(ctx context.Context, method string, params, result any) error {
		if method != protocol.MethodRunOpen {
			return baseCall(ctx, method, params, result)
		}
		assert.True(t, params.(protocol.RunOpenParams).RequireCheckpoint)
		for _, test := range []struct {
			name, runner, connection string
			generation               int64
			request                  protocol.RunCheckpointParams
		}{
			{"run", registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.RunCheckpointParams{RunID: "other", CWD: request.CWD}},
			{"runner", "other", registration.ConnectionID, registration.Generation, request},
			{"connection", registration.RunnerID, "other", registration.Generation, request},
			{"generation", registration.RunnerID, registration.ConnectionID, registration.Generation + 1, request},
			{"cwd", registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.RunCheckpointParams{RunID: "run", CWD: "/work/other"}},
			{"relative", registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.RunCheckpointParams{RunID: "run", CWD: "relative"}},
		} {
			_, rpcErr := r.checkpointRun(ctx, test.runner, test.connection, test.generation, test.request)
			assert.NotNil(t, rpcErr, test.name)
			assert.Zero(t, saves)
		}
		_, rpcErr := session.HandleRequest(ctx, protocol.MethodRunCheckpoint, mustRegistryJSON(t, request))
		require.Nil(t, rpcErr)
		_, rpcErr = session.HandleRequest(ctx, protocol.MethodRunCheckpoint, mustRegistryJSON(t, request))
		require.NotNil(t, rpcErr)
		assert.Equal(t, protocol.ErrorCodeConflict, rpcErr.Code)
		return baseCall(ctx, method, params, result)
	}
	params := testRunOpenParams("run", "conversation")
	params.ExpectedCWD = request.CWD
	_, err := r.OpenRunWithCheckpoint(t.Context(), registration.RunnerID, params, func(ctx context.Context, cwd string) error {
		saves++
		assert.Equal(t, request.CWD, cwd)
		admission, found := convtypes.TurnAdmissionFromContext(ctx)
		assert.True(t, found)
		assert.Equal(t, convtypes.TurnAdmission{ConversationID: "conversation", RunID: "run", RunnerID: registration.RunnerID}, admission)
		_, found = r.Run("run") // callback must not hold the registry lock
		assert.True(t, found)
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, 1, saves)
	_, rpcErr = session.HandleRequest(t.Context(), protocol.MethodRunCheckpoint, mustRegistryJSON(t, request))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeStale, rpcErr.Code)
	r.mu.RLock()
	assert.Nil(t, r.runs["run"].checkpoint, "run history must not retain the callback/thread")
	r.mu.RUnlock()
}

func TestRunCheckpointFailsClosed(t *testing.T) {
	for _, name := range []string{"old runner", "missing callback", "missing acknowledgement", "save failure", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			r, link, session, registration := newCheckpointRegistry(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls, saves := 0, 0
			baseCall := link.call
			link.call = func(ctx context.Context, method string, params, result any) error {
				if method == protocol.MethodRunOpen {
					calls++
					if name == "cancelled" {
						cancel()
					}
					if name != "missing acknowledgement" {
						_, rpcErr := session.HandleRequest(ctx, protocol.MethodRunCheckpoint, mustRegistryJSON(t, protocol.RunCheckpointParams{RunID: "run", CWD: "/work/project"}))
						if rpcErr != nil {
							return rpcErr
						}
					}
				}
				return baseCall(ctx, method, params, result)
			}
			save := func(context.Context, string) error { saves++; return errors.New("disk unavailable") }
			if name == "missing callback" {
				save = nil
			}
			if name == "old runner" {
				r.mu.Lock()
				r.runners[registration.RunnerID].RunCheckpoint = false
				r.mu.Unlock()
			}
			_, err := r.OpenRunWithCheckpoint(ctx, registration.RunnerID, testRunOpenParams("run", "conversation"), save)
			require.Error(t, err)
			if name == "old runner" || name == "missing callback" {
				assert.Zero(t, calls, "unsupported execution must fail before run.open")
			}
			if name == "save failure" {
				assert.Equal(t, 1, saves)
			} else {
				assert.Zero(t, saves)
			}
		})
	}
}

func TestRunCheckpointCancellationDrainsCallback(t *testing.T) {
	r, link, session, registration := newCheckpointRegistry(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started, finish := make(chan struct{}), make(chan struct{})
	callbackDone := make(chan struct{})
	var saves atomic.Int32
	baseCall := link.call
	link.call = func(ctx context.Context, method string, params, result any) error {
		if method != protocol.MethodRunOpen {
			return baseCall(ctx, method, params, result)
		}
		go func() {
			defer close(callbackDone)
			_, _ = session.HandleRequest(context.Background(), protocol.MethodRunCheckpoint, mustRegistryJSON(t, protocol.RunCheckpointParams{RunID: "run", CWD: "/work/project"}))
		}()
		<-ctx.Done()
		return ctx.Err()
	}
	done := make(chan error, 1)
	go func() {
		_, err := r.OpenRunWithCheckpoint(ctx, registration.RunnerID, testRunOpenParams("run", "conversation"), func(ctx context.Context, _ string) error {
			saves.Add(1)
			close(started)
			<-ctx.Done()
			<-finish
			return ctx.Err()
		})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		require.FailNow(t, "checkpoint did not start")
	}
	cancel()
	select {
	case <-done:
		assert.Fail(t, "run opening released a thread still used by its checkpoint")
	case <-time.After(20 * time.Millisecond):
	}
	close(finish)
	select {
	case err := <-done:
		assert.Error(t, err)
	case <-time.After(time.Second):
		require.FailNow(t, "checkpoint callback did not drain")
	}
	<-callbackDone
	assert.EqualValues(t, 1, saves.Load())
}
