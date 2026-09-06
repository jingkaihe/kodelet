package registry

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	"github.com/jingkaihe/kodelet/pkg/delegation"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	"github.com/jingkaihe/kodelet/pkg/steer"
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

func TestChildReleaseDrainsUncertainAdmissionAndFencesDelayedStart(t *testing.T) {
	for _, preparing := range []bool{true, false} {
		t.Run(map[bool]string{true: "preparing", false: "admitted lost response"}[preparing], func(t *testing.T) {
			started, cancelled, drain := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var effects atomic.Int32
			r, s, _ := childTestRegistry(t, func(ctx context.Context, _ delegation.Request, _ delegation.Preset, _ delegation.Identity) (delegation.Run, error) {
				if preparing {
					close(started)
					<-ctx.Done()
					close(cancelled)
					<-drain
					return nil, ctx.Err()
				}
				return func(ctx context.Context, _ func(delegation.Event)) error {
					if err := delegation.Admit(ctx); err != nil {
						return err
					}
					close(started)
					<-ctx.Done()
					close(cancelled)
					<-drain
					effects.Add(1)
					return ctx.Err()
				}, nil
			})
			runner, connection, generation, _ := s.connectionIdentity()
			p := childTestParams()
			p.LeaseID = "retained"
			p.Request.LeaseID = p.LeaseID
			caller, cancel := context.WithCancel(t.Context())
			startDone := make(chan struct{})
			go func() {
				defer close(startDone)
				_, _ = r.executeChildRequest(caller, runner, connection, generation, delegation.StartMethod, p)
			}()
			<-started
			cancel()
			<-startDone // caller intentionally has no acknowledged handle
			releaseCtx, releaseCancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
			_, err := r.executeChildRequest(releaseCtx, runner, connection, generation, delegation.ReleaseMethod, p)
			releaseCancel()
			require.ErrorContains(t, err, "cleanup is not yet confirmed")
			<-cancelled
			late := p
			late.Request.RequestID = "delayed"
			_, err = r.executeChildRequest(t.Context(), runner, connection, generation, delegation.StartMethod, late)
			require.Error(t, err)
			close(drain)
			result, err := r.executeChildRequest(t.Context(), runner, connection, generation, delegation.ReleaseMethod, p)
			require.NoError(t, err)
			require.True(t, result.Done)
			r.mu.RLock()
			tombstone := r.childLeases[childLeaseKey{runner, p.LeaseID}]
			assert.Nil(t, tombstone.prepare)
			assert.Empty(t, tombstone.children)
			assert.Equal(t, map[string]delegation.Preset{"": {Generation: p.Generation}}, tombstone.profiles)
			r.mu.RUnlock()
			after := effects.Load()
			_, err = r.executeChildRequest(t.Context(), runner, connection, generation, delegation.StartMethod, p)
			require.Error(t, err)
			assert.Equal(t, after, effects.Load(), "release acknowledgement joined all admitted/in-flight execution")
		})
	}
}

func TestChildReleaseBeforeStartLeavesConnectionFencedTombstone(t *testing.T) {
	var calls atomic.Int32
	r, s, _ := childTestRegistry(t, func(context.Context, delegation.Request, delegation.Preset, delegation.Identity) (delegation.Run, error) {
		calls.Add(1)
		return nil, errors.New("must not prepare")
	})
	runner, connection, generation, _ := s.connectionIdentity()
	p := childTestParams()
	p.LeaseID = "lease"
	p.Request.LeaseID = p.LeaseID
	_, err := r.executeChildRequest(t.Context(), runner, connection, generation, delegation.ReleaseMethod, p)
	require.NoError(t, err)
	_, err = r.executeChildRequest(t.Context(), runner, connection, generation, delegation.StartMethod, p)
	require.Error(t, err)
	assert.Zero(t, calls.Load())
}

func TestChildRetainedInitialAdmissionCannotOutliveOriginatingTool(t *testing.T) {
	for _, mode := range []string{"fresh", "fork"} {
		t.Run(mode, func(t *testing.T) {
			preparing, continueAdmission := make(chan struct{}), make(chan struct{})
			var effects atomic.Int32
			r, s, cleanup := childTestRegistry(t, func(context.Context, delegation.Request, delegation.Preset, delegation.Identity) (delegation.Run, error) {
				close(preparing)
				<-continueAdmission
				return func(ctx context.Context, _ func(delegation.Event)) error {
					if err := delegation.Admit(ctx); err != nil {
						return err
					}
					effects.Add(1)
					return nil
				}, nil
			})
			runner, connection, generation, _ := s.connectionIdentity()
			p := childTestParams()
			p.Request.ContextMode = mode
			p.LeaseID = "lease"
			p.Request.LeaseID = "lease"
			done := make(chan error, 1)
			go func() {
				_, err := r.executeChildRequest(t.Context(), runner, connection, generation, delegation.StartMethod, p)
				done <- err
			}()
			<-preparing
			cleanup()
			close(continueAdmission)
			require.Error(t, <-done)
			assert.Zero(t, effects.Load())
			p.Request.ContextMode = "fresh"
			p.Request.RequestID = "late-start"
			_, err := r.executeChildRequest(t.Context(), runner, connection, generation, delegation.StartMethod, p)
			require.ErrorContains(t, err, "successful initial admission")
		})
	}
}

func TestChildDurableReceiptCancelDrainsOnlyExactReservedExecution(t *testing.T) {
	r, s, _ := childTestRegistry(t, func(context.Context, delegation.Request, delegation.Preset, delegation.Identity) (delegation.Run, error) {
		return func(ctx context.Context, _ func(delegation.Event)) error {
			if err := delegation.Admit(ctx); err != nil {
				return err
			}
			<-ctx.Done()
			return ctx.Err()
		}, nil
	})
	runner, connection, generation, _ := s.connectionIdentity()
	p := childTestParams()
	child, err := r.executeChildRequest(t.Context(), runner, connection, generation, delegation.StartMethod, p)
	require.NoError(t, err)
	for _, ids := range [][2]string{{"other", child.RunID}, {child.ConversationID, "old-run"}} {
		cancelled, err := r.CancelChildTurn(t.Context(), ids[0], ids[1])
		require.NoError(t, err)
		assert.False(t, cancelled)
	}
	cancelled, err := r.CancelChildTurn(t.Context(), child.ConversationID, child.RunID)
	require.NoError(t, err)
	assert.True(t, cancelled)
	p.ChildID, p.ChildRunID = child.ConversationID, child.RunID
	result, err := r.executeChildRequest(t.Context(), runner, connection, generation, delegation.ReadMethod, p)
	require.NoError(t, err)
	assert.True(t, result.Done)
	assert.True(t, result.Cancelled)
}

func TestChildForkRequiresActiveParentAndResumeHasExactHandles(t *testing.T) {
	r, s, cleanup := childTestRegistry(t, func(_ context.Context, _ delegation.Request, _ delegation.Preset, _ delegation.Identity) (delegation.Run, error) {
		return func(ctx context.Context, _ func(delegation.Event)) error { return delegation.Admit(ctx) }, nil
	})
	runner, connection, generation, _ := s.connectionIdentity()
	p := childTestParams()
	p.LeaseID = "lease"
	p.Request.LeaseID = p.LeaseID
	p.Request.ContextMode = "fork"
	first, err := r.executeChildRequest(t.Context(), runner, connection, generation, delegation.StartMethod, p)
	require.NoError(t, err)
	cleanup()
	replay, err := r.executeChildRequest(t.Context(), runner, connection, generation, delegation.StartMethod, p)
	require.NoError(t, err)
	assert.Equal(t, first.Identity, replay.Identity, "an admitted fork replay does not require another live snapshot")
	p.Request.RequestID = "no-active-fork"
	_, err = r.executeChildRequest(t.Context(), runner, connection, generation, delegation.StartMethod, p)
	require.ErrorContains(t, err, "active parent tool")
	read := p
	read.ChildID = first.ConversationID
	read.ChildRunID = first.RunID
	require.Eventually(t, func() bool {
		result, err := r.executeChildRequest(t.Context(), runner, connection, generation, delegation.ReadMethod, read)
		return err == nil && result.Done
	}, time.Second, time.Millisecond)
	p.Request.ContextMode = ""
	p.Request.Resume = first.ConversationID
	p.Request.RequestID = "followup"
	second, err := r.executeChildRequest(t.Context(), runner, connection, generation, delegation.StartMethod, p)
	require.NoError(t, err)
	assert.Equal(t, first.ConversationID, second.ConversationID)
	assert.NotEqual(t, first.RunID, second.RunID)
	read.ChildRunID = "wrong"
	_, err = r.executeChildRequest(t.Context(), runner, connection, generation, delegation.CancelMethod, read)
	require.Error(t, err)
	read.ChildRunID = ""
	_, err = r.executeChildRequest(t.Context(), runner, connection, generation, delegation.ReadMethod, read)
	require.ErrorContains(t, err, "ambiguous")
	read.ChildRunID = first.RunID
	old, err := r.executeChildRequest(t.Context(), runner, connection, generation, delegation.CancelMethod, read)
	require.NoError(t, err)
	assert.Equal(t, first.RunID, old.RunID)
	assert.True(t, old.Done)
}

func TestChildSteerIsOwnedExactIdempotentAndTerminalSafe(t *testing.T) {
	t.Setenv("KODELET_BASE_PATH", filepath.Join(t.TempDir(), "state"))
	require.NoError(t, db.RunMigrations(t.Context(), migrations.All()))
	path, err := db.DefaultDBPath()
	require.NoError(t, err)
	database, err := db.Open(t.Context(), path)
	require.NoError(t, err)
	defer database.Close()
	r, s, _ := childTestRegistry(t, func(_ context.Context, _ delegation.Request, _ delegation.Preset, id delegation.Identity) (delegation.Run, error) {
		return func(ctx context.Context, _ func(delegation.Event)) error {
			_, err := database.Exec(`INSERT INTO chat_turns(conversation_id,turn_id,run_id,status,created_at,updated_at) VALUES (?,?,?,'running',?,?)`, id.ConversationID, id.RunID, id.RunID, time.Now().UTC(), time.Now().UTC())
			if err != nil {
				return err
			}
			if err := delegation.Admit(ctx); err != nil {
				return err
			}
			<-ctx.Done()
			return ctx.Err()
		}, nil
	})
	runner, connection, generation, _ := s.connectionIdentity()
	p := childTestParams()
	child, err := r.executeChildRequest(t.Context(), runner, connection, generation, delegation.StartMethod, p)
	require.NoError(t, err)
	p.ChildID, p.ChildRunID, p.RequestID, p.Message = child.ConversationID, child.RunID, "steer-one", "do this once"
	for range 2 {
		result, err := r.executeChildSteer(t.Context(), runner, connection, generation, p)
		require.NoError(t, err)
		assert.Equal(t, "injected", result.Outcome)
	}
	wrong := p
	wrong.Message = "conflict"
	_, err = r.executeChildSteer(t.Context(), runner, connection, generation, wrong)
	require.Error(t, err)
	for _, change := range []func(*delegation.Params){func(p *delegation.Params) { p.ChildRunID = "other" }, func(p *delegation.Params) { p.ExtensionID = "other" }, func(p *delegation.Params) { p.Generation++ }, func(p *delegation.Params) { p.RunID = "other" }, func(p *delegation.Params) { p.ToolCallID = "other" }} {
		wrong = p
		change(&wrong)
		_, err = r.executeChildSteer(t.Context(), runner, connection, generation, wrong)
		require.Error(t, err)
	}
	queue, err := steer.NewSteerStore(t.Context())
	require.NoError(t, err)
	defer queue.Close()
	pending, err := queue.Consume(steer.WithChildRun(t.Context(), child.RunID), child.ConversationID)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	_, err = r.executeChildRequest(t.Context(), runner, connection, generation, delegation.CancelMethod, p)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		result, err := r.executeChildRequest(t.Context(), runner, connection, generation, delegation.ReadMethod, p)
		return err == nil && result.Done
	}, time.Second, time.Millisecond)
	p.RequestID = "terminal"
	result, err := r.executeChildSteer(t.Context(), runner, connection, generation, p)
	require.NoError(t, err)
	assert.Equal(t, delegation.SteerResult{Outcome: "promptRequired", Reason: "noRunningTurn"}, result)
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
			require.Eventually(t, func() bool {
				r.mu.RLock()
				defer r.mu.RUnlock()
				for _, grant := range r.childLeases {
					if !grant.revoked || grant.ctx.Err() == nil {
						return false
					}
				}
				return true
			}, time.Second, time.Millisecond)
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
