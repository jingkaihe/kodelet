package registry

import (
	"context"
	"strings"

	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	"github.com/pkg/errors"
)

type artifactToolRegistration struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func (r *Registry) registerArtifactTool(ctx context.Context, params runnerpayload.ToolExecuteParams) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := modelHelperKey{strings.TrimSpace(params.RunID), strings.TrimSpace(params.ToolCallID)}
	if key.runID == "" || key.toolCallID == "" {
		return nil, errors.New("tool run and call IDs are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.activeRunLinkLocked(key.runID, false); err != nil {
		return nil, err
	}
	if r.runs[key.runID].Status != RunStatusRunning {
		return nil, errors.New("artifact access requires a running tool owner")
	}
	if r.artifactTools == nil {
		r.artifactTools = make(map[modelHelperKey]*artifactToolRegistration)
	}
	if _, exists := r.artifactTools[key]; exists {
		return nil, errors.New("tool call is already active")
	}
	ctx, cancel := context.WithCancel(ctx)
	registration := &artifactToolRegistration{ctx: ctx, cancel: cancel}
	r.artifactTools[key] = registration
	return func() {
		cancel()
		r.mu.Lock()
		if r.artifactTools[key] == registration {
			delete(r.artifactTools, key)
		}
		r.mu.Unlock()
	}, nil
}

// ArtifactToolContext authorizes artifact operations against an active tool and
// its connection generation, returning server-owned conversation identity.
func (r *Registry) ArtifactToolContext(identity UIRequestIdentity, runID, toolCallID string) (context.Context, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	run := r.runs[runID]
	owner := r.artifactTools[modelHelperKey{runID, toolCallID}]
	runner, err := r.currentRunnerLocked(identity.RunnerID, identity.ConnectionID, identity.Generation)
	if err != nil || run == nil || owner == nil || owner.ctx.Err() != nil ||
		run.Status != RunStatusRunning || run.RunnerID != identity.RunnerID ||
		run.connectionID != identity.ConnectionID || run.generation != identity.Generation || !runnerHasActiveRun(runner, runID) {
		return nil, "", errors.New("artifact access requires active tool and runner ownership")
	}
	return owner.ctx, run.ConversationID, nil
}

func (r *Registry) clearRunArtifactToolsLocked(runID string) {
	for key, registration := range r.artifactTools {
		if key.runID == runID {
			registration.cancel()
			delete(r.artifactTools, key)
		}
	}
}
