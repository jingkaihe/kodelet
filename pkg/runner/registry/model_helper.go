package registry

import (
	"context"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
)

type modelHelperKey struct {
	runID      string
	toolCallID string
}

type modelHelperRegistration struct {
	ctx    context.Context
	cancel context.CancelFunc
	used   bool
}

func (r *Registry) registerToolModelHelper(ctx context.Context, params runnerpayload.ToolExecuteParams) (func(), error) {
	if params.Name != "web_fetch" || tooltypes.ModelHelperFromContext(ctx) == nil {
		return func() {}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := modelHelperKey{strings.TrimSpace(params.RunID), strings.TrimSpace(params.ToolCallID)}
	if key.runID == "" || key.toolCallID == "" {
		return nil, errors.New("model helper run and tool call IDs are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.activeRunLinkLocked(key.runID, false); err != nil {
		return nil, err
	}
	if r.runs[key.runID].Status != RunStatusRunning {
		return nil, errors.New("model helper requires a running tool owner")
	}
	if _, exists := r.modelHelpers[key]; exists {
		return nil, errors.New("model helper tool ownership is already registered")
	}
	if r.modelHelpers == nil {
		r.modelHelpers = make(map[modelHelperKey]*modelHelperRegistration)
	}
	ctx, cancel := context.WithCancel(ctx)
	registration := &modelHelperRegistration{ctx: ctx, cancel: cancel}
	r.modelHelpers[key] = registration
	return func() {
		cancel()
		r.mu.Lock()
		if r.modelHelpers[key] == registration {
			delete(r.modelHelpers, key)
		}
		r.mu.Unlock()
	}, nil
}

// executeModelHelper is reachable only through an authenticated runner Session.
// Neither a runner credential alone nor a saved conversation grants authority.
func (r *Registry) executeModelHelper(ctx context.Context, runnerID, connectionID string, generation int64, params runnerpayload.ModelHelperParams) (any, *protocol.RPCError) {
	if err := params.Request.Validate(); err != nil {
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeInvalidParams, Message: err.Error()}
	}
	key := modelHelperKey{strings.TrimSpace(params.RunID), strings.TrimSpace(params.ToolCallID)}
	r.mu.Lock()
	run := r.runs[key.runID]
	registration := r.modelHelpers[key]
	runner, connectionErr := r.currentRunnerLocked(runnerID, connectionID, generation)
	valid := connectionErr == nil && run != nil && run.Status == RunStatusRunning && run.RunnerID == runnerID && run.connectionID == connectionID && run.generation == generation && runnerHasActiveRun(runner, key.runID)
	if !valid || registration == nil || registration.ctx.Err() != nil {
		r.mu.Unlock()
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeStale, Message: "model helper requires active run and web_fetch tool ownership"}
	}
	if registration.used {
		r.mu.Unlock()
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeConflict, Message: "model helper was already invoked for this tool call; do not replay"}
	}
	registration.used = true
	r.mu.Unlock()

	// Join RPC cancellation with the parent tool's lifetime. No registry lock,
	// conversation slot, or runner snapshot admission is held during the call.
	helperCtx, cancel := context.WithCancel(registration.ctx)
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	defer cancel()
	if ctx.Err() != nil {
		cancel()
	}
	text, err := tooltypes.RunModelHelper(helperCtx, params.Request)
	if err == nil {
		err = helperCtx.Err()
	}
	if err != nil {
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeUnavailable, Message: "internal model helper failed: " + err.Error()}
	}
	return runnerpayload.ModelHelperResult{Text: text}, nil
}

func (r *Registry) clearRunModelHelpersLocked(runID string) {
	for key, registration := range r.modelHelpers {
		if key.runID == runID {
			registration.cancel()
			delete(r.modelHelpers, key)
		}
	}
}
