package registry

import (
	"context"
	"encoding/json"
	"reflect"
	"time"

	"github.com/jingkaihe/kodelet/pkg/delegation"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/pkg/errors"
)

// A retained lease can delegate for at most one hour. Admission must originate
// in an active extension tool; registration alone and initialization hooks do
// not grant model authority. Result caches are bounded by the same lease.
const (
	childLeaseLifetime  = time.Hour
	maxChildrenPerGrant = 64
)

type childGrant struct {
	ctx                                                        context.Context
	cancel                                                     context.CancelFunc
	prepare                                                    delegation.Prepare
	runnerID, connectionID, runID, conversationID, extensionID string
	generation                                                 int64
	profiles                                                   map[string]delegation.Preset
	children                                                   map[string]*childExecution
	leaseID                                                    string
	environmentProfile                                         string
	stop                                                       func() bool
}

type childExecution struct {
	request  delegation.Request
	result   delegation.Result
	cancel   context.CancelFunc
	sequence uint64
	ready    chan struct{}
	admitted bool
}

type childLeaseKey struct{ runnerID, leaseID string }

func (r *Registry) registerToolChildren(ctx context.Context, params runnerpayload.ToolExecuteParams) (func(), error) {
	prepare := delegation.PrepareFromContext(ctx)
	if prepare == nil {
		return func() {}, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.activeRunLinkLocked(params.RunID, false); err != nil {
		return nil, err
	}
	run := r.runs[params.RunID]
	var manifest runnerpayload.Manifest
	if err := json.Unmarshal([]byte(run.ManifestJSON), &manifest); err != nil {
		return nil, err
	}
	var extensionID string
	for _, tool := range manifest.Tools {
		if tool.Name == params.Name {
			extensionID = tool.ExtensionID
			break
		}
	}
	if extensionID == "" {
		return func() {}, nil
	}
	if run.Status != RunStatusRunning || params.ToolCallID == "" || ctx.Err() != nil {
		return nil, errors.New("child authority requires an active extension tool")
	}
	key := toolForkKey{params.RunID, params.ToolCallID}
	if r.childTools[key] != nil {
		return nil, errors.New("child tool authority already registered")
	}
	grantCtx, cancel := context.WithCancel(ctx)
	affinity, _ := r.affinities.get(run.ConversationID)
	grant := &childGrant{
		ctx: grantCtx, cancel: cancel, prepare: prepare, runnerID: run.RunnerID, connectionID: run.connectionID,
		runID: run.ID, conversationID: run.ConversationID, generation: run.generation, extensionID: extensionID,
		profiles: make(map[string]delegation.Preset), children: make(map[string]*childExecution),
		environmentProfile: affinity.EnvironmentProfile,
	}
	for _, profile := range manifest.Profiles {
		if profile.ExtensionID == extensionID {
			profile.Options = profile.Options.Clone()
			grant.profiles[profile.Name] = profile
		}
	}
	if r.childTools == nil {
		r.childTools = make(map[toolForkKey]*childGrant)
	}
	r.childTools[key] = grant
	return func() {
		cancel()
		r.mu.Lock()
		if r.childTools[key] == grant {
			delete(r.childTools, key)
		}
		r.mu.Unlock()
	}, nil
}

func (r *Registry) childRequest(ctx context.Context, runnerID, connectionID string, generation int64, method string, p delegation.Params) (any, *protocol.RPCError) {
	result, err := r.executeChildRequest(ctx, runnerID, connectionID, generation, method, p)
	if err != nil {
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeInvalidParams, Message: err.Error()}
	}
	return result, nil
}

func (r *Registry) executeChildRequest(ctx context.Context, runnerID, connectionID string, generation int64, method string, p delegation.Params) (delegation.Result, error) {
	if err := ctx.Err(); err != nil {
		return delegation.Result{}, err
	}
	r.mu.Lock()
	if _, err := r.currentRunnerLocked(runnerID, connectionID, generation); err != nil {
		r.mu.Unlock()
		return delegation.Result{}, err
	}
	leaseKey := childLeaseKey{runnerID, p.LeaseID}
	if method == delegation.ReleaseMethod {
		grant := r.childLeases[leaseKey]
		if grant != nil && !grant.owns(connectionID, generation, p) {
			r.mu.Unlock()
			return delegation.Result{}, errors.New("child lease owner mismatch")
		}
		if grant != nil {
			r.dropChildLeaseLocked(leaseKey, grant)
		}
		r.mu.Unlock()
		return delegation.Result{Done: true}, nil
	}
	grant := r.childTools[toolForkKey{p.RunID, p.ToolCallID}]
	if p.LeaseID != "" && r.childLeases[leaseKey] != nil {
		grant = r.childLeases[leaseKey]
	}
	if grant == nil || !grant.owns(connectionID, generation, p) || grant.ctx.Err() != nil {
		r.mu.Unlock()
		return delegation.Result{}, errors.New("child requires active tool ownership or an unexpired retained lease")
	}
	if grant.leaseID == "" {
		run := r.runs[p.RunID]
		runner := r.runners[runnerID]
		if run == nil || run.Status != RunStatusRunning || run.RunnerID != runnerID || run.connectionID != connectionID || run.generation != generation || !runnerHasActiveRun(runner, p.RunID) {
			r.mu.Unlock()
			return delegation.Result{}, errors.New("child parent is not running")
		}
	}
	if method != delegation.StartMethod {
		var child *childExecution
		for _, candidate := range grant.children {
			if candidate.result.ConversationID == p.ChildID {
				child = candidate
				break
			}
		}
		if child == nil {
			r.mu.Unlock()
			return delegation.Result{}, errors.New("child is not owned by this capability")
		}
		if method == delegation.CancelMethod {
			child.cancel()
		} else if method != delegation.ReadMethod {
			r.mu.Unlock()
			return delegation.Result{}, errors.New("unsupported child operation")
		}
		r.mu.Unlock()
		return r.waitChildAdmission(ctx, child, p.After)
	}
	if err := p.Request.Validate(); err != nil {
		r.mu.Unlock()
		return delegation.Result{}, err
	}
	preset, exists := grant.profiles[p.Request.Profile]
	if !exists || preset.Generation != p.Generation {
		r.mu.Unlock()
		return delegation.Result{}, errors.New("execution preset absent or stale")
	}
	if p.Request.LeaseID != p.LeaseID {
		r.mu.Unlock()
		return delegation.Result{}, errors.New("child lease mismatch")
	}
	if p.LeaseID != "" && grant.leaseID == "" {
		if len(r.childLeases) >= 1024 {
			r.mu.Unlock()
			return delegation.Result{}, errors.New("child lease capacity exceeded")
		}
		leaseCtx, cancel := context.WithTimeout(r.ctx, childLeaseLifetime)
		retained := *grant
		retained.ctx, retained.cancel, retained.leaseID = leaseCtx, cancel, p.LeaseID
		retained.children = make(map[string]*childExecution)
		grant = &retained
		if r.childLeases == nil {
			r.childLeases = make(map[childLeaseKey]*childGrant)
		}
		r.childLeases[leaseKey] = grant
		grant.stop = context.AfterFunc(leaseCtx, func() { r.mu.Lock(); defer r.mu.Unlock(); r.dropChildLeaseLocked(leaseKey, grant) })
	}
	if child := grant.children[p.Request.RequestID]; child != nil {
		if !reflect.DeepEqual(child.request, p.Request) {
			r.mu.Unlock()
			return delegation.Result{}, errors.New("requestId was already used with different child input")
		}
		r.mu.Unlock()
		return r.waitChildAdmission(ctx, child, p.After)
	}
	if len(grant.children) >= maxChildrenPerGrant {
		r.mu.Unlock()
		return delegation.Result{}, errors.New("child submission limit exceeded")
	}
	identity := delegation.Identity{
		ConversationID: convtypes.GenerateID(), RunID: convtypes.GenerateID(), ParentConversationID: grant.conversationID,
		ParentRunID: grant.runID, ExtensionID: p.ExtensionID, Profile: p.Request.Profile,
	}
	r.mu.Unlock()
	// Preparation is side-effect free. Never hold the registry mutex, parent's
	// conversation lock, or runner snapshot gate during nested execution.
	run, err := grant.prepare(ctx, p.Request, preset, identity)
	if err != nil {
		return delegation.Result{}, err
	}
	r.mu.Lock()
	if grant.ctx.Err() != nil {
		r.mu.Unlock()
		return delegation.Result{}, errors.New("child authority expired during preparation")
	}
	if child := grant.children[p.Request.RequestID]; child != nil {
		if !reflect.DeepEqual(child.request, p.Request) {
			r.mu.Unlock()
			return delegation.Result{}, errors.New("requestId was already used with different child input")
		}
		r.mu.Unlock()
		return r.waitChildAdmission(ctx, child, p.After)
	}
	if len(grant.children) >= maxChildrenPerGrant {
		r.mu.Unlock()
		return delegation.Result{}, errors.New("child submission limit exceeded")
	}
	childCtx, cancel := context.WithCancel(grant.ctx)
	child := &childExecution{request: p.Request, result: delegation.Result{Identity: identity}, cancel: cancel, ready: make(chan struct{})}
	grant.children[p.Request.RequestID] = child
	r.mu.Unlock()
	go func() {
		defer cancel()
		childCtx = delegation.WithAdmission(childCtx, func() error {
			if err := childCtx.Err(); err != nil {
				return err
			}
			if err := r.BindConversationWithEnvironmentProfile(childCtx, identity.ConversationID, runnerID, grant.environmentProfile); err != nil {
				return err
			}
			r.mu.Lock()
			defer r.mu.Unlock()
			child.admitted = true
			close(child.ready)
			return nil
		})
		err := run(childCtx, func(event delegation.Event) {
			r.mu.Lock()
			defer r.mu.Unlock()
			if event.Kind == "result" {
				child.result.Output = event.Text
			}
			child.sequence++
			event.Sequence = child.sequence
			if len(event.Text) > 32*1024 {
				event.Text = event.Text[:32*1024]
			}
			child.result.Events = append(child.result.Events, event)
			if len(child.result.Events) > 128 {
				child.result.Events = child.result.Events[len(child.result.Events)-128:]
			}
		})
		if _, opened := r.Run(identity.RunID); opened {
			commitCtx, commitCancel := context.WithTimeout(context.WithoutCancel(childCtx), 10*time.Second)
			commitErr := r.CommitConversationAffinity(commitCtx, identity.ConversationID)
			commitCancel()
			if err == nil {
				err = commitErr
			}
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		child.result.Done = true
		if !child.admitted {
			close(child.ready)
		}
		child.result.Cancelled = childCtx.Err() != nil
		if err != nil {
			child.result.Error = err.Error()
		}
	}()
	return r.waitChildAdmission(ctx, child, 0)
}

func (r *Registry) waitChildAdmission(ctx context.Context, child *childExecution, after uint64) (delegation.Result, error) {
	select {
	case <-ctx.Done():
		return delegation.Result{}, ctx.Err()
	case <-child.ready:
		r.mu.RLock()
		defer r.mu.RUnlock()
		if !child.admitted {
			return delegation.Result{}, errors.Errorf("child admission failed: %s", child.result.Error)
		}
		return child.snapshot(after), nil
	}
}

func (g *childGrant) owns(connectionID string, generation int64, p delegation.Params) bool {
	if g.connectionID != connectionID || g.generation != generation || g.runID != p.RunID || g.extensionID != p.ExtensionID || p.Generation == 0 {
		return false
	}
	for _, preset := range g.profiles {
		if preset.Generation == p.Generation {
			return true
		}
	}
	return false
}

func (c *childExecution) snapshot(after uint64) delegation.Result {
	result := c.result
	result.Events = nil
	for _, event := range c.result.Events {
		if event.Sequence > after {
			result.Events = append(result.Events, event)
		}
	}
	return result
}

func (r *Registry) dropChildLeaseLocked(key childLeaseKey, grant *childGrant) {
	if r.childLeases[key] != grant {
		return
	}
	delete(r.childLeases, key)
	if grant.stop != nil {
		grant.stop()
	}
	grant.cancel()
}

func (r *Registry) clearRunChildrenLocked(runID string, retainBackground bool) {
	for key, grant := range r.childTools {
		if !retainBackground {
			cancelOwnedChildRun(grant, runID)
		}
		if key.runID == runID {
			grant.cancel()
			delete(r.childTools, key)
		}
	}
	if !retainBackground {
		for key, grant := range r.childLeases {
			cancelOwnedChildRun(grant, runID)
			if grant.runID == runID {
				r.dropChildLeaseLocked(key, grant)
			}
		}
	}
}

func cancelOwnedChildRun(grant *childGrant, runID string) {
	for _, child := range grant.children {
		if child.result.RunID == runID {
			child.cancel()
		}
	}
}

func (r *Registry) clearRunnerChildrenLocked(runnerID string) {
	for key, grant := range r.childLeases {
		if grant.runnerID == runnerID {
			r.dropChildLeaseLocked(key, grant)
		}
	}
}
