package registry

import (
	"context"
	"path"
	"sync"

	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/pkg/errors"
)

type runCheckpointKey struct{}

type runCheckpoint struct {
	ctx         context.Context
	cancel      context.CancelFunc
	save        func(context.Context, string) error
	expectedCWD string
	used        bool
	complete    bool
	closed      bool
	cwd         string
	ops         sync.WaitGroup
}

// OpenRunWithCheckpoint requires the registered runner to validate its CWD and
// environment policy, then wait for central persistence before extension startup.
// It is only for admitted ordinary turns, not discovery or delegated utilities.
func (r *Registry) OpenRunWithCheckpoint(ctx context.Context, runnerID string, params protocol.RunOpenParams, save func(context.Context, string) error) (runnerpayload.Manifest, error) {
	if save == nil {
		return runnerpayload.Manifest{}, errors.New("cannot save the conversation before starting work")
	}
	checkpointCtx, cancel := context.WithCancel(ctx)
	checkpoint := &runCheckpoint{ctx: checkpointCtx, cancel: cancel, save: save, expectedCWD: params.ExpectedCWD}
	defer func() {
		cancel()
		r.mu.Lock()
		checkpoint.closed = true
		if run := r.runs[params.RunID]; run != nil && run.checkpoint == checkpoint {
			run.checkpoint = nil
		}
		r.mu.Unlock()
		// A cancelled reverse RPC can outlive its caller. Never release the
		// central thread while a checkpoint callback is still using it.
		checkpoint.ops.Wait()
	}()
	params.RequireCheckpoint = true
	return r.OpenRun(context.WithValue(ctx, runCheckpointKey{}, checkpoint), runnerID, params)
}

func (r *Registry) checkpointRun(ctx context.Context, runnerID, connectionID string, generation int64, params protocol.RunCheckpointParams) (any, *protocol.RPCError) {
	if params.RunID == "" || !path.IsAbs(params.CWD) || path.Clean(params.CWD) != params.CWD {
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeInvalidParams, Message: "saving the conversation checkpoint requires a run ID and the runner's resolved working directory"}
	}
	r.mu.Lock()
	run := r.runs[params.RunID]
	runner, connectionErr := r.currentRunnerLocked(runnerID, connectionID, generation)
	if connectionErr != nil || run == nil || run.Status != RunStatusOpening || run.RunnerID != runnerID || run.connectionID != connectionID || run.generation != generation || !runnerHasActiveRun(runner, params.RunID) || run.checkpoint == nil {
		r.mu.Unlock()
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeStale, Message: "only the runner starting this accepted run can save its conversation checkpoint"}
	}
	checkpoint := run.checkpoint
	if checkpoint.closed || checkpoint.ctx.Err() != nil || ctx.Err() != nil {
		r.mu.Unlock()
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeStale, Message: "conversation checkpoint is no longer active"}
	}
	if checkpoint.used {
		r.mu.Unlock()
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeConflict, Message: "conversation checkpoint was already invoked; do not replay"}
	}
	if checkpoint.expectedCWD != "" && checkpoint.expectedCWD != params.CWD {
		r.mu.Unlock()
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeConflict, Message: "the working directory differs from the conversation's saved directory"}
	}
	checkpoint.used = true
	checkpoint.ops.Add(1)
	defer checkpoint.ops.Done()
	affinity, _ := r.affinities.get(run.ConversationID)
	admission := convtypes.TurnAdmission{ConversationID: run.ConversationID, RunID: run.ID, RunnerID: runnerID, EnvironmentProfile: affinity.EnvironmentProfile}
	r.mu.Unlock()

	saveCtx, cancel := context.WithCancel(checkpoint.ctx)
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	defer cancel()
	if err := checkpoint.save(convtypes.ContextWithTurnAdmission(saveCtx, admission), params.CWD); err != nil {
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeUnavailable, Message: "conversation checkpoint failed: " + err.Error()}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	run = r.runs[params.RunID]
	if saveCtx.Err() != nil || run == nil || run.Status != RunStatusOpening || run.checkpoint != checkpoint {
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeStale, Message: "conversation checkpoint was cancelled"}
	}
	// The ordinary store committed the history, summary and affinity together.
	r.affinities.put(run.ConversationID, affinity.ConversationAffinity, true)
	checkpoint.complete, checkpoint.cwd = true, params.CWD
	return struct{}{}, nil
}
