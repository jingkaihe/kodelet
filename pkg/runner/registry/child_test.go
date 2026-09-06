package registry

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/delegation"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func childTestParams() delegation.Params {
	return delegation.Params{
		RunID: "run-one", ToolCallID: "tool-one", ExtensionID: "search", Generation: 7,
		Request: delegation.Request{RequestID: "request-one", Profile: "code_search", Message: "find the parser"},
	}
}

func childTestRegistry(t *testing.T, prepare delegation.Prepare) (*Registry, *Session, func()) {
	t.Helper()
	r, _, session := newModelHelperRegistry(t)
	r.mu.Lock()
	manifest := runnerpayload.Manifest{
		Tools:    []runnerpayload.ToolDefinition{{Name: "search", ExtensionID: "search"}},
		Profiles: []delegation.Preset{{ExtensionID: "search", Generation: 7, Profile: delegation.Profile{Name: "code_search", SystemPrompt: "pinned runner prompt"}}},
	}
	data, err := json.Marshal(manifest)
	r.runs["run-one"].ManifestJSON = string(data)
	r.mu.Unlock()
	require.NoError(t, err)
	cleanup, err := r.registerToolChildren(delegation.WithPrepare(t.Context(), prepare), runnerpayload.ToolExecuteParams{RunID: "run-one", ToolCallID: "tool-one", Name: "search"})
	require.NoError(t, err)
	t.Cleanup(cleanup)
	return r, session, cleanup
}

func TestChildAuthority(t *testing.T) {
	for _, scenario := range []string{"unregistered", "absent", "run", "tool", "runner", "connection", "generation", "extension", "process generation", "preset", "stale run connection", "stale run generation", "unowned run", "inactive", "cleaned", "unknown fields", "null options", "trailing"} {
		t.Run(scenario, func(t *testing.T) {
			var calls atomic.Int32
			r, s, cleanup := childTestRegistry(t, func(context.Context, delegation.Request, delegation.Preset, delegation.Identity) (delegation.Run, error) {
				calls.Add(1)
				return func(context.Context, func(delegation.Event)) error { return nil }, nil
			})
			p := childTestParams()
			runnerID, connectionID, generation, _ := s.connectionIdentity()
			switch scenario {
			case "unregistered":
				s = NewSession(r, nil)
			case "absent", "cleaned":
				cleanup()
			case "run":
				p.RunID = "other"
			case "tool":
				p.ToolCallID = "other"
			case "runner":
				runnerID = "other"
			case "connection":
				connectionID = "other"
			case "generation":
				generation++
			case "extension":
				p.ExtensionID = "other"
			case "process generation":
				p.Generation++
			case "preset":
				p.Request.Profile = "other"
			case "stale run connection":
				r.runs[p.RunID].connectionID = "old"
			case "stale run generation":
				r.runs[p.RunID].generation--
			case "unowned run":
				removeRunnerActiveRun(r.runners[runnerID], p.RunID)
			case "inactive":
				r.runs[p.RunID].Status = RunStatusCanceled
			}
			if scenario == "unregistered" || scenario == "unknown fields" || scenario == "null options" || scenario == "trailing" {
				raw := mustRegistryJSON(t, p)
				switch scenario {
				case "unknown fields":
					raw = []byte(`{"providerKey":"no"}`)
				case "null options":
					raw = []byte(`{"request":{"options":null}}`)
				case "trailing":
					raw = append(raw, []byte(`{}`)...)
				}
				_, err := s.HandleRequest(t.Context(), delegation.StartMethod, raw)
				require.NotNil(t, err)
			} else {
				_, err := r.executeChildRequest(t.Context(), runnerID, connectionID, generation, delegation.StartMethod, p)
				require.Error(t, err)
			}
			assert.Zero(t, calls.Load())
		})
	}
}

func TestChildReplayProgressAndExactCancellation(t *testing.T) {
	var runs atomic.Int32
	started := make(chan struct{}, 2)
	r, s, _ := childTestRegistry(t, func(_ context.Context, request delegation.Request, preset delegation.Preset, id delegation.Identity) (delegation.Run, error) {
		assert.Equal(t, "pinned runner prompt", preset.SystemPrompt)
		assert.NotEqual(t, id.ConversationID, id.ParentConversationID)
		assert.NotEqual(t, id.RunID, id.ParentRunID)
		return func(ctx context.Context, emit func(delegation.Event)) error {
			if err := delegation.Admit(ctx); err != nil {
				return err
			}
			runs.Add(1)
			emit(delegation.Event{Kind: "text", Text: request.Message})
			started <- struct{}{}
			<-ctx.Done()
			return ctx.Err()
		}, nil
	})
	runner, connection, generation, _ := s.connectionIdentity()
	p := childTestParams()
	first, err := r.executeChildRequest(t.Context(), runner, connection, generation, delegation.StartMethod, p)
	require.NoError(t, err)
	<-started
	replay, err := r.executeChildRequest(t.Context(), runner, connection, generation, delegation.StartMethod, p)
	require.NoError(t, err)
	assert.Equal(t, first.Identity, replay.Identity)
	assert.Equal(t, int32(1), runs.Load())
	assert.Equal(t, "find the parser", replay.Events[0].Text)
	p.Request.Message = "different"
	_, err = r.executeChildRequest(t.Context(), runner, connection, generation, delegation.StartMethod, p)
	require.ErrorContains(t, err, "different child input")
	p.Request.RequestID = "second"
	second, err := r.executeChildRequest(t.Context(), runner, connection, generation, delegation.StartMethod, p)
	require.NoError(t, err)
	<-started
	p.ChildID = first.ConversationID
	_, err = r.executeChildRequest(t.Context(), runner, connection, generation, delegation.CancelMethod, p)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		result, err := r.executeChildRequest(t.Context(), runner, connection, generation, delegation.ReadMethod, p)
		return err == nil && result.Done && result.Cancelled
	}, time.Second, time.Millisecond)
	p.ChildID = second.ConversationID
	result, err := r.executeChildRequest(t.Context(), runner, connection, generation, delegation.ReadMethod, p)
	require.NoError(t, err)
	assert.False(t, result.Done)
}

func TestChildLifetime(t *testing.T) {
	for _, scenario := range []string{"foreground tool return", "background parent success", "parent cancel", "disconnect", "reconnect", "release", "expiry", "shutdown"} {
		t.Run(scenario, func(t *testing.T) {
			started := make(chan struct{})
			stopped := make(chan struct{})
			r, s, cleanup := childTestRegistry(t, func(context.Context, delegation.Request, delegation.Preset, delegation.Identity) (delegation.Run, error) {
				return func(ctx context.Context, _ func(delegation.Event)) error {
					if err := delegation.Admit(ctx); err != nil {
						return err
					}
					close(started)
					<-ctx.Done()
					close(stopped)
					return ctx.Err()
				}, nil
			})
			p := childTestParams()
			if scenario != "foreground tool return" {
				p.LeaseID = "lease"
				p.Request.LeaseID = "lease"
			}
			runner, connection, generation, _ := s.connectionIdentity()
			_, err := r.executeChildRequest(t.Context(), runner, connection, generation, delegation.StartMethod, p)
			require.NoError(t, err)
			<-started
			cleanup()
			if p.LeaseID != "" {
				select {
				case <-stopped:
					t.Fatal("retained child stopped with tool")
				default:
				}
			}
			switch scenario {
			case "background parent success":
				r.mu.Lock()
				r.finishRunLocked(p.RunID, RunStatusSucceeded, "", time.Now())
				r.mu.Unlock()
				select {
				case <-stopped:
					t.Fatal("retained child stopped with successful parent")
				default:
				}
				_, err = r.executeChildRequest(t.Context(), runner, connection, generation, delegation.ReleaseMethod, p)
				require.NoError(t, err)
			case "parent cancel":
				require.NoError(t, r.CancelRun(t.Context(), p.RunID, "stop"))
			case "disconnect":
				s.Detach(nil)
			case "reconnect":
				_, err = r.Register(testRegisterParams("host-one", "/work/project"), newFakeLink())
				require.NoError(t, err)
			case "release":
				_, err = r.executeChildRequest(t.Context(), runner, connection, generation, delegation.ReleaseMethod, p)
				require.NoError(t, err)
			case "expiry":
				r.mu.Lock()
				r.childLeases[childLeaseKey{runner, "lease"}].cancel()
				r.mu.Unlock()
			case "shutdown":
				require.NoError(t, r.Close())
			}
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("child cancellation did not propagate")
			}
			require.Eventually(t, func() bool { r.mu.RLock(); defer r.mu.RUnlock(); return len(r.childLeases) == 0 }, time.Second, time.Millisecond)
		})
	}
}

func TestChildAdmissionWaitsForPersistenceAndReplaysWithoutResubmission(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "persisted", true: "persistence failure"}[fail], func(t *testing.T) {
			started, persist := make(chan struct{}), make(chan struct{})
			var runs atomic.Int32
			r, s, _ := childTestRegistry(t, func(context.Context, delegation.Request, delegation.Preset, delegation.Identity) (delegation.Run, error) {
				return func(ctx context.Context, _ func(delegation.Event)) error {
					runs.Add(1)
					close(started)
					<-persist
					if fail {
						return errors.New("storage unavailable")
					}
					return delegation.Admit(ctx)
				}, nil
			})
			runner, conn, gen, _ := s.connectionIdentity()
			requestCtx, cancel := context.WithCancel(t.Context())
			first := make(chan error, 1)
			go func() {
				_, err := r.executeChildRequest(requestCtx, runner, conn, gen, delegation.StartMethod, childTestParams())
				first <- err
			}()
			<-started
			select {
			case <-first:
				t.Fatal("acknowledged before persistence")
			default:
			}
			cancel()
			require.ErrorIs(t, <-first, context.Canceled, "request disconnect does not resubmit or cancel the scoped child")
			close(persist)
			var wg sync.WaitGroup
			for range 2 {
				wg.Go(func() {
					result, err := r.executeChildRequest(t.Context(), runner, conn, gen, delegation.StartMethod, childTestParams())
					if fail {
						assert.ErrorContains(t, err, "storage unavailable")
						return
					}
					assert.NoError(t, err)
					affinity, ok, err := r.ResolveConversationAffinity(t.Context(), result.ConversationID)
					assert.NoError(t, err)
					assert.True(t, ok)
					assert.Equal(t, runner, affinity.RunnerID)
				})
			}
			wg.Wait()
			assert.EqualValues(t, 1, runs.Load())
		})
	}
}

func TestDaemonChildRunCancellationCancelsProviderContext(t *testing.T) {
	stopped := make(chan struct{})
	r, s, _ := childTestRegistry(t, func(context.Context, delegation.Request, delegation.Preset, delegation.Identity) (delegation.Run, error) {
		return func(ctx context.Context, _ func(delegation.Event)) error {
			if err := delegation.Admit(ctx); err != nil {
				return err
			}
			<-ctx.Done()
			close(stopped)
			return ctx.Err()
		}, nil
	})
	runner, conn, gen, _ := s.connectionIdentity()
	result, err := r.executeChildRequest(t.Context(), runner, conn, gen, delegation.StartMethod, childTestParams())
	require.NoError(t, err)
	r.mu.Lock()
	r.clearRunChildrenLocked(result.RunID, false)
	r.mu.Unlock()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("child provider context was not cancelled")
	}
}
