package registry

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jingkaihe/kodelet/pkg/delegation"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	"github.com/jingkaihe/kodelet/pkg/steer"
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
	revoked                                                    bool
	activated                                                  bool
}

type childExecution struct {
	request  delegation.Request
	result   delegation.Result
	cancel   context.CancelFunc
	sequence uint64
	ready    chan struct{}
	admitted bool
	done     chan struct{}
	steers   map[string]childSteerReceipt
}

type childSteerReceipt struct {
	message string
	result  delegation.SteerResult
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
	var result any
	var err error
	if method == delegation.SteerMethod {
		result, err = r.executeChildSteer(ctx, runnerID, connectionID, generation, p)
	} else {
		result, err = r.executeChildRequest(ctx, runnerID, connectionID, generation, method, p)
	}
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
		if p.LeaseID == "" || p.ExtensionID == "" || p.Generation == 0 {
			r.mu.Unlock()
			return delegation.Result{}, errors.New("child lease release requires its exact owner")
		}
		// Release is connection/process scoped, not tied to lastRunID: a
		// retained process may have reattached since starting its children.
		ownerParams := p
		if grant != nil {
			ownerParams.RunID = grant.runID
		}
		if grant != nil && !grant.owns(connectionID, generation, ownerParams) {
			r.mu.Unlock()
			return delegation.Result{}, errors.New("child lease owner mismatch")
		}
		if grant == nil {
			if len(r.childLeases) >= 1024 {
				r.mu.Unlock()
				return delegation.Result{}, errors.New("child lease revocation capacity exceeded; reconnect runner")
			}
			leaseCtx, cancel := context.WithCancel(r.ctx)
			grant = &childGrant{
				ctx: leaseCtx, cancel: cancel, runnerID: runnerID, connectionID: connectionID, generation: generation, extensionID: p.ExtensionID, runID: p.RunID,
				profiles: map[string]delegation.Preset{"": {Generation: p.Generation}},
			}
			if r.childLeases == nil {
				r.childLeases = make(map[childLeaseKey]*childGrant)
			}
			r.childLeases[leaseKey] = grant
		}
		// Keep a bounded connection-lifetime tombstone. A delayed start that
		// passed the runner's local check before release must not recreate it.
		grant.revoked = true
		if grant.stop != nil {
			grant.stop()
		}
		grant.cancel()
		done := make([]<-chan struct{}, 0, len(grant.children))
		for _, child := range grant.children {
			done = append(done, child.done)
		}
		r.mu.Unlock()
		drainCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		for _, wait := range done {
			select {
			case <-wait:
			case <-drainCtx.Done():
				return delegation.Result{}, errors.Wrap(drainCtx.Err(), "child lease revoked but cleanup is not yet confirmed; retry release")
			}
		}
		r.mu.Lock()
		if r.childLeases[leaseKey] == grant {
			// A revocation tombstone needs ownership proof, not the parent
			// thread, prompts or completed child history. Keep payloads only
			// while cleanup remains unconfirmed and retry must still drain.
			grant.prepare, grant.children, grant.stop = nil, nil, nil
			grant.profiles = map[string]delegation.Preset{"": {Generation: p.Generation}}
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
		child, err := r.ownedChildLocked(grant, p)
		if err != nil {
			r.mu.Unlock()
			return delegation.Result{}, err
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
	if child := grant.children[p.Request.RequestID]; child != nil {
		if !reflect.DeepEqual(child.request, p.Request) {
			r.mu.Unlock()
			return delegation.Result{}, errors.New("requestId was already used with different child input")
		}
		r.mu.Unlock()
		return r.waitChildAdmission(ctx, child, p.After)
	}
	toolGrant := r.childTools[toolForkKey{p.RunID, p.ToolCallID}]
	if grant.leaseID != "" && !grant.activated && (toolGrant == nil || toolGrant.ctx.Err() != nil || !toolGrant.owns(connectionID, generation, p)) {
		r.mu.Unlock()
		return delegation.Result{}, errors.New("child retained authority requires successful initial admission from an active tool")
	}
	if p.Request.ContextMode == "fork" && (toolGrant == nil || toolGrant.ctx.Err() != nil || !toolGrant.owns(connectionID, generation, p)) {
		r.mu.Unlock()
		return delegation.Result{}, errors.New("child fork requires the current active parent tool")
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
	if len(grant.children) >= maxChildrenPerGrant {
		r.mu.Unlock()
		return delegation.Result{}, errors.New("child submission limit exceeded")
	}
	identity := delegation.Identity{
		ConversationID: convtypes.GenerateID(), RunID: convtypes.GenerateID(), ParentConversationID: grant.conversationID,
		ParentRunID: grant.runID, ExtensionID: p.ExtensionID, Profile: p.Request.Profile,
		RunnerID: runnerID, HostInstanceID: r.runners[runnerID].Host.InstanceID,
	}
	if p.Request.Resume != "" {
		identity.ConversationID = p.Request.Resume
		if active := r.childExecutions[identity.ConversationID]; active != nil && !active.result.Done {
			r.mu.Unlock()
			return delegation.Result{}, errors.New("child conversation already has an active submission")
		}
	}
	childCtx, cancel := context.WithCancel(grant.ctx)
	child := &childExecution{request: p.Request, result: delegation.Result{Identity: identity}, cancel: cancel, ready: make(chan struct{}), done: make(chan struct{})}
	grant.children[p.Request.RequestID] = child
	if r.childExecutions == nil {
		r.childExecutions = make(map[string]*childExecution)
	}
	r.childExecutions[identity.ConversationID] = child
	var stopOrigin func() bool
	if toolGrant != nil {
		// Initial retained admission must still complete while its originating
		// tool lives. Once admitted, the retained child owns an independent
		// lifetime; a late context callback must not cancel it.
		stopOrigin = context.AfterFunc(toolGrant.ctx, func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			if !child.admitted {
				cancel()
			}
		})
	}
	r.mu.Unlock()
	go func() {
		defer cancel()
		defer close(child.done)
		if stopOrigin != nil {
			defer stopOrigin()
		}
		// Reserve before preparation so duplicate/cancel/release observe even
		// an in-flight snapshot. No registry lock is held during provider work.
		run, prepareErr := grant.prepare(childCtx, p.Request, preset, identity)
		childCtx = delegation.WithAdmission(childCtx, func() error {
			if err := childCtx.Err(); err != nil {
				return err
			}
			if err := r.BindConversationWithEnvironmentProfile(childCtx, identity.ConversationID, runnerID, grant.environmentProfile); err != nil {
				return err
			}
			r.mu.Lock()
			defer r.mu.Unlock()
			if err := childCtx.Err(); err != nil {
				return err
			}
			if toolGrant != nil && toolGrant.ctx.Err() != nil {
				cancel()
				return errors.New("child originating tool ended before admission")
			}
			child.admitted = true
			grant.activated = true
			if stopOrigin != nil {
				stopOrigin()
			}
			close(child.ready)
			return nil
		})
		err := prepareErr
		if err == nil {
			err = childCtx.Err()
		}
		if err == nil {
			err = run(childCtx, func(event delegation.Event) {
				r.mu.Lock()
				defer r.mu.Unlock()
				if event.Kind == "result" {
					child.result.Output = event.Text
				}
				child.sequence++
				event.Sequence = child.sequence
				for _, field := range []*string{&event.Text, &event.Input, &event.ToolOutput, &event.Error} {
					if len(*field) > 32*1024 {
						limit := 32 * 1024
						for limit > 0 && !utf8.RuneStart((*field)[limit]) {
							limit--
						}
						*field = strings.Clone((*field)[:limit])
					}
				}
				child.result.Events = append(child.result.Events, event)
				if len(child.result.Events) > 128 {
					child.result.Events = child.result.Events[len(child.result.Events)-128:]
				}
			})
		}
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
		if r.childExecutions[identity.ConversationID] == child {
			delete(r.childExecutions, identity.ConversationID)
		}
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
	if !grant.revoked {
		delete(r.childLeases, key)
	}
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
			delete(r.childLeases, key)
		}
	}
}

func (r *Registry) ownedChildLocked(grant *childGrant, p delegation.Params) (*childExecution, error) {
	var child *childExecution
	for _, candidate := range grant.children {
		if candidate.result.ConversationID != p.ChildID || (p.ChildRunID != "" && candidate.result.RunID != p.ChildRunID) {
			continue
		}
		if child != nil {
			return nil, errors.New("childRunId is required for an ambiguous child handle")
		}
		child = candidate
	}
	if child == nil {
		return nil, errors.New("child run is not owned by this capability")
	}
	if p.ChildRunID == "" && child.request.Resume != "" {
		return nil, errors.New("childRunId is required after a child followup")
	}
	return child, nil
}

// CancelChildTurn is the daemon client's exact durable-receipt cancellation
// seam. It cannot cancel a later followup sharing the same conversation ID.
func (r *Registry) CancelChildTurn(ctx context.Context, conversationID, runID string) (bool, error) {
	r.mu.Lock()
	child := r.childExecutions[conversationID]
	if child == nil || child.result.RunID != runID {
		r.mu.Unlock()
		return false, nil
	}
	child.cancel()
	r.mu.Unlock()
	select {
	case <-child.done:
		return true, nil
	case <-ctx.Done():
		return true, errors.Wrap(ctx.Err(), "child cancellation cleanup is unconfirmed")
	}
}

func (r *Registry) executeChildSteer(ctx context.Context, runnerID, connectionID string, generation int64, p delegation.Params) (delegation.SteerResult, error) {
	if !delegation.ValidID(p.RequestID) || strings.TrimSpace(p.Message) == "" || len(p.Message) > steer.MaxMessageLength {
		return delegation.SteerResult{}, errors.New("child steer requires requestId and a nonempty bounded message")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.currentRunnerLocked(runnerID, connectionID, generation); err != nil {
		return delegation.SteerResult{}, err
	}
	grant := r.childTools[toolForkKey{p.RunID, p.ToolCallID}]
	if p.LeaseID != "" && r.childLeases[childLeaseKey{runnerID, p.LeaseID}] != nil {
		grant = r.childLeases[childLeaseKey{runnerID, p.LeaseID}]
	}
	if grant == nil || !grant.owns(connectionID, generation, p) || grant.ctx.Err() != nil {
		return delegation.SteerResult{}, errors.New("child steering requires current owned authority")
	}
	child, err := r.ownedChildLocked(grant, p)
	if err != nil {
		return delegation.SteerResult{}, err
	}
	if prior, ok := child.steers[p.RequestID]; ok {
		if prior.message != p.Message {
			return delegation.SteerResult{}, errors.New("steer requestId already used with different input")
		}
		return prior.result, nil
	}
	if len(child.steers) >= 128 {
		return delegation.SteerResult{}, errors.New("child steer request limit exceeded")
	}
	result := delegation.SteerResult{Outcome: "promptRequired", Reason: "noRunningTurn"}
	if child.admitted && !child.result.Done {
		store, err := steer.NewSteerStore(ctx)
		if err != nil {
			return delegation.SteerResult{}, err
		}
		defer func() { _ = store.Close() }()
		injected, err := store.EnqueueChild(ctx, p.ChildID, child.result.RunID, p.Message)
		if err != nil {
			return delegation.SteerResult{}, err
		}
		if injected {
			result = delegation.SteerResult{Outcome: "injected"}
		}
	}
	if child.steers == nil {
		child.steers = make(map[string]childSteerReceipt)
	}
	child.steers[p.RequestID] = childSteerReceipt{message: p.Message, result: result}
	return result, nil
}
