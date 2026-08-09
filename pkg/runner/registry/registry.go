// Package registry owns live runner registrations and run-capacity coordination
// in the control plane.
package registry

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	"github.com/pkg/errors"
)

const (
	defaultHeartbeatInterval = 15 * time.Second
	defaultHeartbeatTimeout  = 45 * time.Second
	defaultRunLeaseGrace     = 10 * time.Second
	openingCleanupTimeout    = 10 * time.Second
)

var (
	// ErrRunnerNotFound indicates that a stable runner registration does not exist.
	ErrRunnerNotFound = errors.New("runner not found")
	// ErrRunnerConnected indicates that a live runner must be stopped before removal.
	ErrRunnerConnected = errors.New("runner is connected")
	// ErrRunnerActiveRun indicates inconsistent state that still references an active run.
	ErrRunnerActiveRun = errors.New("runner has an active run")
)

// Link is the live symmetric RPC connection retained for one runner generation.
type Link interface {
	Call(ctx context.Context, method string, params any, result any) error
	CallTracked(ctx context.Context, method string, params any, result any, onRequestID func(string)) error
	Notify(ctx context.Context, method string, params any) error
	Close() error
	Done() <-chan struct{}
	Err() error
}

// RunnerStatus is the scheduler-facing state of a runner registration.
type RunnerStatus string

const (
	RunnerStatusOffline      RunnerStatus = "offline"
	RunnerStatusConnecting   RunnerStatus = "connecting"
	RunnerStatusIdle         RunnerStatus = "idle"
	RunnerStatusBusy         RunnerStatus = "busy"
	RunnerStatusError        RunnerStatus = "error"
	RunnerStatusIncompatible RunnerStatus = "incompatible"
)

// RunStatus is retained as an API alias for the shared runner lease status.
type RunStatus = protocol.RunStatus

const (
	RunStatusOpening   = protocol.RunStatusOpening
	RunStatusRunning   = protocol.RunStatusRunning
	RunStatusSucceeded = protocol.RunStatusSucceeded
	RunStatusFailed    = protocol.RunStatusFailed
	RunStatusCanceled  = protocol.RunStatusCanceled
	RunStatusLost      = protocol.RunStatusLost
)

// Runner is a safe snapshot of one stable runner registration.
type Runner struct {
	ID                 string             `json:"id"`
	DisplayName        string             `json:"displayName,omitempty"`
	Host               protocol.Host      `json:"host"`
	Workspace          protocol.Workspace `json:"workspace"`
	KodeletVersion     string             `json:"kodeletVersion"`
	ManifestDigest     string             `json:"manifestDigest,omitempty"`
	ManifestChanged    bool               `json:"manifestChanged"`
	CompatibilityError string             `json:"compatibilityError,omitempty"`
	Status             RunnerStatus       `json:"status"`
	Connected          bool               `json:"connected"`
	ActiveRunID        string             `json:"activeRunId,omitempty"`
	ConnectionID       string             `json:"connectionId,omitempty"`
	Generation         int64              `json:"generation"`
	ConnectedAt        time.Time          `json:"connectedAt,omitempty"`
	LastHeartbeatAt    time.Time          `json:"lastHeartbeatAt,omitempty"`
	CreatedAt          time.Time          `json:"createdAt"`
	UpdatedAt          time.Time          `json:"updatedAt"`
}

// Run is a snapshot of one top-level runner environment lease.
type Run struct {
	ID             string    `db:"id" json:"id"`
	ConversationID string    `db:"conversation_id" json:"conversationId"`
	RunnerID       string    `db:"runner_id" json:"runnerId"`
	Status         RunStatus `db:"status" json:"status"`
	ManifestDigest string    `db:"manifest_digest" json:"manifestDigest,omitempty"`
	ManifestJSON   string    `db:"manifest_json" json:"-"`
	Error          string    `db:"error" json:"error,omitempty"`
	CreatedAt      time.Time `db:"created_at" json:"createdAt"`
	UpdatedAt      time.Time `db:"updated_at" json:"updatedAt"`
}

// RemovalResult describes durable state removed with one runner registration.
type RemovalResult struct {
	RunnerID                      string `json:"runnerId"`
	RemovedRuns                   int    `json:"removedRuns"`
	RemovedConversationAffinities int    `json:"removedConversationAffinities"`
}

// RunnerReferencedError prevents an ordinary removal from abandoning conversation affinity.
type RunnerReferencedError struct {
	RunnerID        string
	ConversationIDs []string
}

func (e *RunnerReferencedError) Error() string {
	if e == nil {
		return "runner is referenced by conversations"
	}
	ids := append([]string(nil), e.ConversationIDs...)
	sort.Strings(ids)
	detail := strings.Join(ids, ", ")
	if len(ids) > 5 {
		detail = strings.Join(ids[:5], ", ") + fmt.Sprintf(", and %d more", len(ids)-5)
	}
	return fmt.Sprintf("runner %s is bound to %d conversation(s): %s; remove those conversations or retry with --force", e.RunnerID, len(ids), detail)
}

type runnerEntry struct {
	Runner
	identity string
	link     Link
	ready    bool
}

type runEntry struct {
	Run
	connectionID string
	generation   int64
	leaseCancel  context.CancelFunc
}

type runFence struct {
	runnerID     string
	runID        string
	connectionID string
	generation   int64
	link         Link
}

// Options configures registry liveness and test seams.
type Options struct {
	HeartbeatInterval time.Duration
	HeartbeatTimeout  time.Duration
	RunLeaseGrace     time.Duration
	Now               func() time.Time
	NewID             func(prefix string) (string, error)
	Persistence       Persistence
}

// Registry coordinates stable runner identity, live connection generations, and capacity-one runs.
type Registry struct {
	mu                sync.RWMutex
	runners           map[string]*runnerEntry
	byIdentity        map[string]string
	runs              map[string]*runEntry
	affinities        *affinityIndex
	toolUpdates       *toolUpdateRouter
	onRunFailure      func(string)
	heartbeatInterval time.Duration
	heartbeatTimeout  time.Duration
	runLeaseGrace     time.Duration
	now               func() time.Time
	newID             func(string) (string, error)
	persistence       Persistence
	ctx               context.Context
	cancel            context.CancelFunc
	closed            bool
}

// New creates a live registry and restores any configured durable state.
func New(parent context.Context, options Options) (*Registry, error) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	if options.HeartbeatInterval <= 0 {
		options.HeartbeatInterval = defaultHeartbeatInterval
	}
	if options.HeartbeatTimeout <= 0 {
		options.HeartbeatTimeout = defaultHeartbeatTimeout
	}
	if options.RunLeaseGrace <= 0 {
		options.RunLeaseGrace = defaultRunLeaseGrace
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.NewID == nil {
		options.NewID = randomID
	}
	r := &Registry{
		runners:           make(map[string]*runnerEntry),
		byIdentity:        make(map[string]string),
		runs:              make(map[string]*runEntry),
		affinities:        newAffinityIndex(),
		toolUpdates:       newToolUpdateRouter(),
		heartbeatInterval: options.HeartbeatInterval,
		heartbeatTimeout:  options.HeartbeatTimeout,
		runLeaseGrace:     options.RunLeaseGrace,
		now:               options.Now,
		newID:             options.NewID,
		persistence:       options.Persistence,
		ctx:               ctx,
		cancel:            cancel,
	}
	if r.persistence != nil {
		state, err := r.persistence.Load(ctx)
		if err != nil {
			cancel()
			_ = r.persistence.Close()
			return nil, errors.Wrap(err, "failed to restore runner registry")
		}
		if err := r.restore(state); err != nil {
			cancel()
			_ = r.persistence.Close()
			return nil, err
		}
	}
	go r.monitorLiveness()
	return r, nil
}

func (r *Registry) restore(state PersistedState) error {
	now := r.now().UTC()
	for _, runner := range state.Runners {
		if strings.TrimSpace(runner.ID) == "" || strings.TrimSpace(runner.Host.InstanceID) == "" || strings.TrimSpace(runner.Workspace.Path) == "" {
			return errors.New("persisted runner registration has incomplete identity")
		}
		identity := runnerIdentity(runner.Host.InstanceID, runner.Workspace.Path)
		if _, exists := r.runners[runner.ID]; exists {
			return errors.Errorf("duplicate persisted runner id %s", runner.ID)
		}
		if _, exists := r.byIdentity[identity]; exists {
			return errors.New("duplicate persisted runner workspace identity")
		}
		runner.Connected = false
		runner.ConnectionID = ""
		runner.ActiveRunID = ""
		if runner.Status != RunnerStatusIncompatible {
			runner.Status = RunnerStatusOffline
		}
		if runner.CreatedAt.IsZero() {
			runner.CreatedAt = now
		}
		runner.UpdatedAt = now
		entry := &runnerEntry{Runner: runner, identity: identity}
		r.runners[runner.ID] = entry
		r.byIdentity[identity] = runner.ID
	}

	for _, run := range state.Runs {
		if strings.TrimSpace(run.ID) == "" || r.runners[run.RunnerID] == nil {
			return errors.New("persisted runner run has incomplete identity")
		}
		if run.Status == RunStatusOpening || run.Status == RunStatusRunning {
			run.Status = RunStatusLost
			run.Error = "control plane restarted while run was active"
			run.UpdatedAt = now
			if err := r.persistence.SaveRun(r.ctx, run); err != nil {
				return errors.Wrap(err, "failed to mark restored runner run lost")
			}
		}
		r.runs[run.ID] = &runEntry{Run: run}
	}

	for conversationID, affinity := range state.Affinities {
		if strings.TrimSpace(conversationID) == "" || r.runners[affinity.RunnerID] == nil {
			return errors.New("persisted conversation runner affinity is invalid")
		}
		affinity.EnvironmentProfile = normalizeEnvironmentProfile(affinity.EnvironmentProfile)
		r.affinities.put(conversationID, affinity, true)
	}
	for _, entry := range r.runners {
		if err := r.persistence.SaveRunner(r.ctx, entry.Runner); err != nil {
			return errors.Wrap(err, "failed to mark restored runner offline")
		}
	}
	return nil
}

// Register upserts one stable workspace identity and atomically replaces its live generation.
func (r *Registry) Register(params protocol.RegisterParams, link Link) (protocol.RegisterResult, error) {
	if r == nil {
		return protocol.RegisterResult{}, errors.New("runner registry is required")
	}
	if link == nil {
		return protocol.RegisterResult{}, errors.New("runner link is required")
	}
	if strings.TrimSpace(params.Host.InstanceID) == "" || strings.TrimSpace(params.Workspace.Path) == "" || strings.TrimSpace(params.Workspace.Name) == "" {
		return protocol.RegisterResult{}, params.Validate()
	}
	if !protocol.SupportsVersion(params.ProtocolVersions, protocol.Version) {
		return protocol.RegisterResult{}, r.recordIncompatible(params)
	}

	identity := runnerIdentity(params.Host.InstanceID, params.Workspace.Path)
	connectionID, err := r.newID("conn")
	if err != nil {
		return protocol.RegisterResult{}, errors.Wrap(err, "failed to generate runner connection id")
	}
	now := r.now().UTC()

	var oldLink Link
	var lostConversationID string
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return protocol.RegisterResult{}, errors.New("runner registry is closed")
	}
	entry, isNew, err := r.registrationCandidateLocked(params, identity, now)
	if err != nil {
		r.mu.Unlock()
		return protocol.RegisterResult{}, err
	}
	oldLink = entry.link
	var lostRun *runEntry
	if entry.ActiveRunID != "" {
		currentRun := r.runs[entry.ActiveRunID]
		if currentRun == nil {
			r.mu.Unlock()
			return protocol.RegisterResult{}, errors.New("runner active run is missing")
		}
		candidate := *currentRun
		candidate.Status = RunStatusLost
		candidate.Error = "runner connection was replaced"
		candidate.UpdatedAt = now
		lostRun = &candidate
	}
	entry.Generation++
	entry.ConnectionID = connectionID
	entry.link = link
	entry.Host = params.Host
	entry.Workspace = params.Workspace
	entry.DisplayName = strings.TrimSpace(params.DisplayName)
	entry.KodeletVersion = strings.TrimSpace(params.KodeletVersion)
	entry.ManifestDigest = strings.TrimSpace(params.ManifestDigest)
	entry.ManifestChanged = false
	entry.CompatibilityError = ""
	entry.Connected = true
	entry.Status = RunnerStatusConnecting
	entry.ready = false
	entry.ActiveRunID = ""
	entry.ConnectedAt = now
	entry.LastHeartbeatAt = now
	entry.UpdatedAt = now
	if r.persistence != nil {
		if lostRun != nil {
			err = r.persistence.SaveRunnerAndRun(r.ctx, entry.Runner, lostRun.Run)
		} else {
			err = r.persistence.SaveRunner(r.ctx, entry.Runner)
		}
		if err != nil {
			r.mu.Unlock()
			return protocol.RegisterResult{}, err
		}
	}
	if isNew {
		r.byIdentity[identity] = entry.ID
	}
	r.runners[entry.ID] = entry
	if lostRun != nil {
		if lostRun.leaseCancel != nil {
			lostRun.leaseCancel()
			lostRun.leaseCancel = nil
		}
		r.runs[lostRun.ID] = lostRun
		r.affinities.deactivate(lostRun.ConversationID)
		r.clearRunTransientStateLocked(lostRun.ID)
		lostConversationID = lostRun.ConversationID
	}
	result := protocol.RegisterResult{
		RunnerID:            entry.ID,
		ProtocolVersion:     protocol.Version,
		ConnectionID:        entry.ConnectionID,
		Generation:          entry.Generation,
		HeartbeatIntervalMS: r.heartbeatInterval.Milliseconds(),
	}
	r.mu.Unlock()

	if lostConversationID != "" {
		r.notifyRunFailure(lostConversationID)
	}
	if oldLink != nil && oldLink != link {
		_ = oldLink.Close()
	}
	go r.watchConnection(result, link)
	return result, nil
}

func (r *Registry) watchConnection(registration protocol.RegisterResult, link Link) {
	select {
	case <-linkTransportDone(link):
		cause := link.Err()
		if cause == nil {
			cause = errors.New("runner connection closed")
		}
		r.Detach(registration.RunnerID, registration.ConnectionID, registration.Generation, cause)
	case <-r.ctx.Done():
	}
}

func linkTransportDone(link Link) <-chan struct{} {
	if transport, ok := link.(interface{ TransportDone() <-chan struct{} }); ok {
		return transport.TransportDone()
	}
	return link.Done()
}

func (r *Registry) registrationCandidateLocked(params protocol.RegisterParams, identity string, now time.Time) (*runnerEntry, bool, error) {
	existingID := r.byIdentity[identity]
	if requestedID := strings.TrimSpace(params.RunnerID); requestedID != "" {
		if entry := r.runners[requestedID]; entry != nil && entry.identity != identity {
			return nil, false, errors.New("runner id belongs to another host workspace")
		}
		if existingID != "" && existingID != requestedID {
			return nil, false, errors.New("host workspace is registered under another runner id")
		}
		if existingID == "" && r.runners[requestedID] == nil {
			return nil, false, errors.Wrap(ErrRunnerNotFound, "runner id is not known to this control plane")
		}
	}

	entry := r.runners[existingID]
	if entry != nil {
		candidate := *entry
		return &candidate, false, nil
	}
	runnerID, err := r.newID("runner")
	if err != nil {
		return nil, false, errors.Wrap(err, "failed to generate runner id")
	}
	return &runnerEntry{
		Runner: Runner{
			ID:        runnerID,
			CreatedAt: now,
			UpdatedAt: now,
		},
		identity: identity,
	}, true, nil
}

func (r *Registry) recordIncompatible(params protocol.RegisterParams) error {
	message := errors.Errorf("runner does not support protocol version %d", protocol.Version)
	identity := runnerIdentity(params.Host.InstanceID, params.Workspace.Path)
	now := r.now().UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errors.New("runner registry is closed")
	}
	entry, isNew, err := r.registrationCandidateLocked(params, identity, now)
	if err != nil {
		return err
	}
	entry.Host = params.Host
	entry.Workspace = params.Workspace
	entry.DisplayName = strings.TrimSpace(params.DisplayName)
	entry.KodeletVersion = strings.TrimSpace(params.KodeletVersion)
	entry.ManifestDigest = strings.TrimSpace(params.ManifestDigest)
	entry.CompatibilityError = message.Error()
	entry.UpdatedAt = now
	if !entry.Connected {
		entry.Status = RunnerStatusIncompatible
	}
	if err := r.persistRunnerLocked(entry); err != nil {
		return err
	}
	if isNew {
		r.byIdentity[identity] = entry.ID
	}
	r.runners[entry.ID] = entry
	return message
}

// Detach marks a connection offline only when it is still the current fenced generation.
func (r *Registry) Detach(runnerID, connectionID string, generation int64, cause error) {
	if r == nil {
		return
	}
	now := r.now().UTC()
	r.mu.Lock()
	entry := r.runners[strings.TrimSpace(runnerID)]
	if entry == nil || entry.ConnectionID != connectionID || entry.Generation != generation {
		r.mu.Unlock()
		return
	}
	entry.link = nil
	entry.Connected = false
	entry.Status = RunnerStatusOffline
	entry.ConnectionID = ""
	entry.UpdatedAt = now
	var lostRun *runEntry
	if entry.ActiveRunID != "" {
		lostRun = r.runs[entry.ActiveRunID]
		r.finishRunLocked(entry.ActiveRunID, RunStatusLost, errorString(cause), now)
	}
	if lostRun != nil {
		r.logPersistenceError("failed to persist detached runner and lost run", r.persistRunnerAndRunLocked(entry, lostRun))
	} else {
		r.logPersistenceError("failed to persist detached runner", r.persistRunnerLocked(entry))
	}
	conversationID := ""
	if lostRun != nil {
		conversationID = lostRun.ConversationID
	}
	r.mu.Unlock()
	if conversationID != "" {
		r.notifyRunFailure(conversationID)
	}
}

// Heartbeat applies application health from the current connection generation.
func (r *Registry) Heartbeat(runnerID, connectionID string, generation int64, params protocol.HeartbeatParams) error {
	if err := params.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(params.RunnerID) != strings.TrimSpace(runnerID) || params.Generation != generation {
		return errors.New("heartbeat identity does not match registered connection")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, err := r.currentRunnerLocked(runnerID, connectionID, generation)
	if err != nil {
		return err
	}
	if strings.TrimSpace(params.ActiveRunID) != entry.ActiveRunID {
		return errors.New("heartbeat active run does not match control-plane lease")
	}
	switch params.State {
	case protocol.RunnerStateIdle:
		if entry.ActiveRunID != "" {
			return errors.New("idle heartbeat cannot report an active run")
		}
	case protocol.RunnerStateRunning, protocol.RunnerStateStopping:
		if entry.ActiveRunID == "" {
			return errors.New("active heartbeat state requires a control-plane run lease")
		}
	case protocol.RunnerStateError:
	}
	now := r.now().UTC()
	entry.LastHeartbeatAt = now
	entry.ManifestDigest = strings.TrimSpace(params.ManifestDigest)
	entry.ready = true
	if params.State == protocol.RunnerStateError {
		entry.Status = RunnerStatusError
	} else if entry.ActiveRunID != "" {
		entry.Status = RunnerStatusBusy
	} else {
		entry.Status = RunnerStatusIdle
	}
	entry.UpdatedAt = now
	return nil
}

// ManifestChanged updates the idle manifest digest for the current generation.
func (r *Registry) ManifestChanged(runnerID, connectionID string, generation int64, params protocol.ManifestChangedParams) error {
	if params.RunnerID != runnerID || params.Generation != generation {
		return errors.New("manifest notification identity does not match registered connection")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, err := r.currentRunnerLocked(runnerID, connectionID, generation)
	if err != nil {
		return err
	}
	digest := strings.TrimSpace(params.ManifestDigest)
	if digest != entry.ManifestDigest {
		entry.ManifestChanged = true
	}
	entry.ManifestDigest = digest
	entry.UpdatedAt = r.now().UTC()
	return r.persistRunnerLocked(entry)
}

// OpenRun reserves capacity, opens the remote environment, validates its manifest, and marks the run active.
func (r *Registry) OpenRun(ctx context.Context, runnerID string, params protocol.RunOpenParams) (runnerpayload.Manifest, error) {
	if err := params.Validate(); err != nil {
		return runnerpayload.Manifest{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runnerID = strings.TrimSpace(runnerID)
	now := r.now().UTC()

	r.mu.Lock()
	entry := r.runners[runnerID]
	if entry == nil {
		r.mu.Unlock()
		return runnerpayload.Manifest{}, errors.New("runner not found")
	}
	environmentProfile := normalizeEnvironmentProfile(params.Agent.EnvironmentProfile)
	reservedAffinity := false
	if affinity, exists := r.affinities.get(params.ConversationID); exists && affinity.RunnerID != runnerID {
		r.mu.Unlock()
		return runnerpayload.Manifest{}, errors.Errorf("conversation is bound to runner %s", affinity.RunnerID)
	}
	if affinity, exists := r.affinities.get(params.ConversationID); exists && affinity.EnvironmentProfile != environmentProfile {
		r.mu.Unlock()
		return runnerpayload.Manifest{}, environmentProfileLockedError(affinity.EnvironmentProfile, environmentProfile)
	}
	if !entry.Connected || entry.link == nil {
		r.mu.Unlock()
		return runnerpayload.Manifest{}, errors.New("runner is offline")
	}
	if !entry.ready {
		r.mu.Unlock()
		return runnerpayload.Manifest{}, errors.New("runner has not completed its initial heartbeat")
	}
	if entry.ActiveRunID != "" {
		r.mu.Unlock()
		return runnerpayload.Manifest{}, errors.Errorf("runner is busy with run %s", entry.ActiveRunID)
	}
	if entry.Status != RunnerStatusIdle {
		r.mu.Unlock()
		return runnerpayload.Manifest{}, errors.Errorf("runner is not idle: %s", entry.Status)
	}
	if activeRunID := r.affinities.activeRun(params.ConversationID); activeRunID != "" {
		r.mu.Unlock()
		return runnerpayload.Manifest{}, errors.Errorf("conversation already has active run %s", activeRunID)
	}
	if _, exists := r.runs[params.RunID]; exists {
		r.mu.Unlock()
		return runnerpayload.Manifest{}, errors.New("run id already exists")
	}
	if _, exists := r.affinities.get(params.ConversationID); !exists {
		if err := r.reserveConversationAffinityLocked(params.ConversationID, runnerID, environmentProfile); err != nil {
			r.mu.Unlock()
			return runnerpayload.Manifest{}, err
		}
		reservedAffinity = true
	}

	link := entry.link
	connectionID := entry.ConnectionID
	generation := entry.Generation
	fence := runFence{
		runnerID:     runnerID,
		runID:        params.RunID,
		connectionID: connectionID,
		generation:   generation,
		link:         link,
	}
	leaseCtx, leaseCancel := context.WithCancel(context.Background())
	runnerCandidate := *entry
	runnerCandidate.ActiveRunID = params.RunID
	runnerCandidate.Status = RunnerStatusBusy
	runnerCandidate.UpdatedAt = now
	run := &runEntry{
		Run: Run{
			ID:             params.RunID,
			ConversationID: params.ConversationID,
			RunnerID:       runnerID,
			Status:         RunStatusOpening,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
		connectionID: connectionID,
		generation:   generation,
		leaseCancel:  leaseCancel,
	}
	if err := r.persistRunnerAndRunLocked(&runnerCandidate, run); err != nil {
		leaseCancel()
		if reservedAffinity {
			r.affinities.releasePending(params.ConversationID)
		}
		r.mu.Unlock()
		return runnerpayload.Manifest{}, err
	}
	r.runners[runnerID] = &runnerCandidate
	r.affinities.activate(params.ConversationID, params.RunID)
	r.runs[params.RunID] = run
	r.mu.Unlock()
	go r.watchRunLease(ctx, leaseCtx.Done(), fence)

	var manifest runnerpayload.Manifest
	if err := link.Call(ctx, protocol.MethodRunOpen, params, &manifest); err != nil {
		var rpcErr *protocol.RPCError
		return runnerpayload.Manifest{}, r.reconcileOpeningFailure(fence, err, errors.As(err, &rpcErr))
	}
	if err := validateManifest(manifest, runnerID, params, generation); err != nil {
		return runnerpayload.Manifest{}, r.reconcileOpeningFailure(fence, err, false)
	}
	manifestPayload, err := json.Marshal(manifest)
	if err != nil {
		wrapped := errors.Wrap(err, "failed to encode runner manifest snapshot")
		return runnerpayload.Manifest{}, r.reconcileOpeningFailure(fence, wrapped, false)
	}

	r.mu.Lock()
	current, err := r.currentRunnerLocked(runnerID, connectionID, generation)
	if err != nil || current.link != link {
		r.mu.Unlock()
		return runnerpayload.Manifest{}, errors.New("runner connection changed while opening run")
	}
	run = r.runs[params.RunID]
	if run == nil {
		r.mu.Unlock()
		return runnerpayload.Manifest{}, errors.New("run is no longer opening")
	}
	if run.Status != RunStatusOpening {
		status := run.Status
		message := run.Error
		r.mu.Unlock()
		return runnerpayload.Manifest{}, r.closeRunInterruptedDuringOpen(params.RunID, status, message)
	}
	runCandidate := *run
	runCandidate.Status = RunStatusRunning
	runCandidate.ManifestDigest = manifest.Digest
	runCandidate.ManifestJSON = string(manifestPayload)
	now = r.now().UTC()
	runCandidate.UpdatedAt = now
	runnerCandidate = *current
	runnerCandidate.ManifestDigest = manifest.Digest
	runnerCandidate.ManifestChanged = false
	runnerCandidate.UpdatedAt = now
	if err := r.persistRunnerAndRunLocked(&runnerCandidate, &runCandidate); err != nil {
		r.mu.Unlock()
		return runnerpayload.Manifest{}, r.reconcileOpeningFailure(fence, err, false)
	}
	r.runners[runnerID] = &runnerCandidate
	r.runs[params.RunID] = &runCandidate
	r.mu.Unlock()
	return manifest, nil
}

// CallRun invokes a run-scoped runner method after generation and active-run validation.
func (r *Registry) CallRun(ctx context.Context, runID, method string, params any, result any) error {
	link, err := r.activeRunLink(runID)
	if err != nil {
		return err
	}
	return link.Call(ctx, method, params, result)
}

// ExecuteTool invokes tool.execute and routes replaceable transient updates to the supplied sink.
func (r *Registry) ExecuteTool(ctx context.Context, params runnerpayload.ToolExecuteParams, updates func(runnerpayload.ToolUpdateParams)) (runnerpayload.ToolExecuteResult, error) {
	params.WantUpdates = updates != nil
	cleanup := func() {}
	if updates != nil {
		var err error
		cleanup, err = r.toolUpdates.subscribe(params.RunID, params.ToolCallID, updates)
		if err != nil {
			return runnerpayload.ToolExecuteResult{}, err
		}
	}
	defer cleanup()

	link, err := r.activeRunLink(params.RunID)
	if err != nil {
		return runnerpayload.ToolExecuteResult{}, err
	}
	var result runnerpayload.ToolExecuteResult
	if err := link.CallTracked(ctx, protocol.MethodToolExecute, params, &result, func(requestID string) {
		r.toolUpdates.setRequestID(params.RunID, params.ToolCallID, requestID)
	}); err != nil {
		var rpcErr *protocol.RPCError
		if errors.As(err, &rpcErr) {
			return runnerpayload.ToolExecuteResult{}, err
		}
		return runnerpayload.ToolExecuteResult{}, errors.Wrap(err, "runner tool response was not received; execution may have started and side effects are uncertain")
	}
	return result, nil
}

// DeliverToolUpdate forwards a generation-fenced transient tool update.
func (r *Registry) DeliverToolUpdate(runnerID, connectionID string, generation int64, params runnerpayload.ToolUpdateParams) error {
	r.mu.Lock()
	entry := r.runners[runnerID]
	run := r.runs[params.RunID]
	valid := entry != nil && entry.ConnectionID == connectionID && entry.Generation == generation && entry.ActiveRunID == params.RunID && run != nil && run.Status == RunStatusRunning
	if !valid {
		r.mu.Unlock()
		return errors.New("tool update belongs to a stale or inactive run")
	}
	r.mu.Unlock()
	return r.toolUpdates.deliver(params)
}

// CancelRun cancels active runner operations but leaves run.close responsible for releasing the lease.
func (r *Registry) CancelRun(ctx context.Context, runID, reason string) error {
	link, err := r.activeRunLinkAllowCanceled(runID)
	if err != nil {
		return err
	}
	if err := link.Call(ctx, protocol.MethodRunCancel, protocol.RunCancelParams{RunID: runID, Reason: reason}, nil); err != nil {
		return err
	}
	r.mu.Lock()
	if run := r.runs[runID]; run != nil && (run.Status == RunStatusOpening || run.Status == RunStatusRunning) {
		run.Status = RunStatusCanceled
		run.UpdatedAt = r.now().UTC()
		if err := r.persistRunLocked(run); err != nil {
			r.mu.Unlock()
			return err
		}
	}
	r.mu.Unlock()
	return nil
}

// CloseRun releases one runner lease and records its terminal status.
func (r *Registry) CloseRun(ctx context.Context, runID string, status RunStatus, runErr error) error {
	if status != RunStatusSucceeded && status != RunStatusFailed && status != RunStatusCanceled {
		return errors.Errorf("invalid terminal run status %q", status)
	}
	link, err := r.activeRunLinkAllowCanceled(runID)
	if err != nil {
		return err
	}
	callErr := link.Call(ctx, protocol.MethodRunClose, protocol.RunCloseParams{RunID: runID}, nil)
	if remoteRunAlreadyClosed(callErr) {
		callErr = nil
	}
	r.mu.Lock()
	message := errorString(runErr)
	run := r.runs[runID]
	if run == nil {
		r.mu.Unlock()
		return nil
	}
	runner := r.runners[run.RunnerID]
	if runner == nil || runner.ActiveRunID != runID {
		r.mu.Unlock()
		return nil
	}
	if run.Status == RunStatusFailed && callErr == nil {
		status = RunStatusFailed
		message = run.Error
	} else if message == "" {
		message = run.Error
	}
	if callErr != nil {
		status = RunStatusLost
		if message == "" {
			message = callErr.Error()
		}
	}
	r.finishRunLocked(runID, status, message, r.now().UTC())
	run = r.runs[runID]
	var persistenceErr error
	if run != nil {
		if callErr != nil {
			if runner := r.runners[run.RunnerID]; runner != nil && runner.Connected {
				runner.Status = RunnerStatusError
				runner.UpdatedAt = r.now().UTC()
			}
		}
		persistenceErr = r.persistRunnerAndRunLocked(r.runners[run.RunnerID], run)
	}
	r.mu.Unlock()
	if persistenceErr != nil {
		return persistenceErr
	}
	return callErr
}

func (r *Registry) closeRunInterruptedDuringOpen(runID string, status RunStatus, message string) error {
	if status != RunStatusFailed && status != RunStatusCanceled {
		return errors.Errorf("run is no longer opening: %s", status)
	}
	summary := fmt.Sprintf("run %s while opening", status)
	cause := errors.New(summary)
	if strings.TrimSpace(message) != "" {
		cause = errors.Wrap(errors.New(message), summary)
	}
	ctx, cancel := context.WithTimeout(context.Background(), openingCleanupTimeout)
	defer cancel()
	if err := r.CloseRun(ctx, runID, status, cause); err != nil {
		return errors.Wrapf(cause, "remote cleanup failed: %v", err)
	}
	return cause
}

// EnvironmentError marks an active run failed after an asynchronous runner notification.
func (r *Registry) EnvironmentError(runnerID, connectionID string, generation int64, params protocol.EnvironmentErrorParams) error {
	r.mu.Lock()
	entry, err := r.currentRunnerLocked(runnerID, connectionID, generation)
	if err != nil {
		r.mu.Unlock()
		return err
	}
	if entry.ActiveRunID != params.RunID {
		r.mu.Unlock()
		return errors.New("environment error belongs to another run")
	}
	run := r.runs[params.RunID]
	if run == nil {
		r.mu.Unlock()
		return errors.New("run not found")
	}
	run.Status = RunStatusFailed
	run.Error = params.Message
	now := r.now().UTC()
	run.UpdatedAt = now
	entry.Status = RunnerStatusBusy
	entry.UpdatedAt = now
	persistErr := r.persistRunnerAndRunLocked(entry, run)
	conversationID := run.ConversationID
	handler := r.onRunFailure
	r.mu.Unlock()
	if handler != nil {
		handler(conversationID)
	}
	return persistErr
}

// SetEnvironmentErrorHandler installs the control-plane cancellation hook for asynchronous runner failures.
func (r *Registry) SetEnvironmentErrorHandler(handler func(conversationID string)) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.onRunFailure = handler
	r.mu.Unlock()
}

func (r *Registry) notifyRunFailure(conversationID string) {
	r.mu.RLock()
	handler := r.onRunFailure
	r.mu.RUnlock()
	if handler != nil {
		handler(conversationID)
	}
}

// Runner returns one stable runner snapshot.
func (r *Registry) Runner(id string) (Runner, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry := r.runners[strings.TrimSpace(id)]
	if entry == nil {
		return Runner{}, false
	}
	return entry.Runner, true
}

// Runners returns deterministic runner snapshots ordered by stable ID.
func (r *Registry) Runners() []Runner {
	r.mu.RLock()
	result := make([]Runner, 0, len(r.runners))
	for _, entry := range r.runners {
		result = append(result, entry.Runner)
	}
	r.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

// Run returns one run snapshot.
func (r *Registry) Run(id string) (Run, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry := r.runs[strings.TrimSpace(id)]
	if entry == nil {
		return Run{}, false
	}
	return entry.Run, true
}

// RemoveRunner deletes an offline stable registration and its run history.
// Force explicitly abandons any conversations pinned to the runner.
func (r *Registry) RemoveRunner(ctx context.Context, runnerID string, force bool) (RemovalResult, error) {
	if r == nil {
		return RemovalResult{}, errors.New("runner registry is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runnerID = strings.TrimSpace(runnerID)
	if runnerID == "" {
		return RemovalResult{}, errors.New("runner id is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return RemovalResult{}, errors.New("runner registry is closed")
	}
	entry := r.runners[runnerID]
	if entry == nil {
		return RemovalResult{}, errors.Wrapf(ErrRunnerNotFound, "runner %s", runnerID)
	}
	if entry.Connected || entry.link != nil {
		return RemovalResult{}, errors.Wrapf(ErrRunnerConnected, "runner %s must be stopped before removal", runnerID)
	}
	if entry.ActiveRunID != "" {
		return RemovalResult{}, errors.Wrapf(ErrRunnerActiveRun, "runner %s", runnerID)
	}

	conversationIDs := make([]string, 0)
	conversationIDs = append(conversationIDs, r.affinities.conversationsForRunner(runnerID)...)
	if len(conversationIDs) > 0 && !force && r.persistence == nil {
		return RemovalResult{}, &RunnerReferencedError{RunnerID: runnerID, ConversationIDs: conversationIDs}
	}

	result := RemovalResult{RunnerID: runnerID}
	if r.persistence != nil {
		persisted, err := r.persistence.RemoveRunner(ctx, runnerID, force)
		if err != nil {
			return RemovalResult{}, err
		}
		result = persisted
	}

	for runID, run := range r.runs {
		if run.RunnerID != runnerID {
			continue
		}
		if r.persistence == nil {
			result.RemovedRuns++
		}
		r.affinities.deactivate(run.ConversationID)
		r.clearRunTransientStateLocked(runID)
		delete(r.runs, runID)
	}
	if r.persistence == nil {
		result.RemovedConversationAffinities += r.affinities.removeRunner(runnerID)
	} else {
		r.affinities.removeRunner(runnerID)
	}
	delete(r.byIdentity, entry.identity)
	delete(r.runners, runnerID)
	return result, nil
}

// Close disconnects all live runner generations and stops liveness monitoring.
func (r *Registry) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	now := r.now().UTC()
	links := make([]Link, 0, len(r.runners))
	for _, entry := range r.runners {
		if entry.link != nil {
			links = append(links, entry.link)
			entry.link = nil
		}
		if entry.ActiveRunID != "" {
			r.finishRunLocked(entry.ActiveRunID, RunStatusLost, "control plane stopped while run was active", now)
		}
		entry.Connected = false
		if entry.Status != RunnerStatusIncompatible {
			entry.Status = RunnerStatusOffline
		}
		entry.ConnectionID = ""
		entry.UpdatedAt = now
		r.logPersistenceError("failed to persist runner shutdown", r.persistRunnerLocked(entry))
	}
	for _, run := range r.runs {
		if run.UpdatedAt.Equal(now) && run.Status == RunStatusLost {
			r.logPersistenceError("failed to persist lost shutdown run", r.persistRunLocked(run))
		}
	}
	r.mu.Unlock()
	r.cancel()

	var firstErr error
	for _, link := range links {
		if err := link.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if r.persistence != nil {
		if err := r.persistence.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (r *Registry) finishOpeningFailure(fence runFence, status RunStatus, message string, runnerError bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	run := r.runs[fence.runID]
	if !runMatchesFence(run, fence) || run.Status != RunStatusOpening {
		return
	}
	r.finishRunLocked(fence.runID, status, message, r.now().UTC())
	if run = r.runs[fence.runID]; run != nil {
		if runnerError {
			if runner := r.runners[run.RunnerID]; runnerMatchesFence(runner, fence) {
				runner.Status = RunnerStatusError
				runner.UpdatedAt = r.now().UTC()
			}
		}
		r.logPersistenceError("failed to persist failed opening run", r.persistRunnerAndRunLocked(r.runners[run.RunnerID], run))
	}
}

func (r *Registry) reconcileOpeningFailure(fence runFence, cause error, openRejected bool) error {
	if openRejected {
		r.finishOpeningFailure(fence, RunStatusFailed, errorString(cause), false)
		return cause
	}

	cleanupConfirmed, cleanupErr := closeRemoteOpeningRun(fence.link, fence.runID)
	if cleanupConfirmed {
		r.finishOpeningFailure(fence, RunStatusFailed, errorString(cause), false)
		return cause
	}

	message := fmt.Sprintf("%v; runner cleanup was not confirmed: %v", cause, cleanupErr)
	r.finishOpeningFailure(fence, RunStatusLost, message, true)
	_ = fence.link.Close()
	return errors.Wrapf(cause, "runner open state is uncertain because cleanup failed: %v", cleanupErr)
}

func runMatchesFence(run *runEntry, fence runFence) bool {
	return run != nil && run.ID == fence.runID && run.RunnerID == fence.runnerID && run.connectionID == fence.connectionID && run.generation == fence.generation
}

func runnerMatchesFence(runner *runnerEntry, fence runFence) bool {
	return runner != nil && runner.ID == fence.runnerID && runner.ConnectionID == fence.connectionID && runner.Generation == fence.generation && runner.link == fence.link
}

func (r *Registry) watchRunLease(owner context.Context, leaseDone <-chan struct{}, fence runFence) {
	select {
	case <-owner.Done():
	case <-leaseDone:
		return
	case <-r.ctx.Done():
		return
	}

	timer := time.NewTimer(r.runLeaseGrace)
	defer timer.Stop()
	select {
	case <-timer.C:
		r.expireRunLease(fence, owner.Err())
	case <-leaseDone:
	case <-r.ctx.Done():
	}
}

func (r *Registry) expireRunLease(fence runFence, cause error) {
	message := "control-plane run context ended without run.close"
	if cause != nil {
		message += ": " + cause.Error()
	}
	r.mu.Lock()
	run := r.runs[fence.runID]
	runner := r.runners[fence.runnerID]
	active := runMatchesFence(run, fence) && runnerMatchesFence(runner, fence) && runner.ActiveRunID == fence.runID && (run.Status == RunStatusOpening || run.Status == RunStatusRunning || run.Status == RunStatusCanceled || run.Status == RunStatusFailed)
	conversationID := ""
	var persistenceErr error
	if active {
		conversationID = run.ConversationID
		run.Status = RunStatusCanceled
		run.Error = message
		run.UpdatedAt = r.now().UTC()
		persistenceErr = r.persistRunLocked(run)
	}
	r.mu.Unlock()
	if !active {
		return
	}
	r.logPersistenceError("failed to persist expired run lease", persistenceErr)

	r.notifyRunFailure(conversationID)
	cleanupCtx, cancel := context.WithTimeout(context.Background(), openingCleanupTimeout)
	_ = fence.link.Call(cleanupCtx, protocol.MethodRunCancel, protocol.RunCancelParams{RunID: fence.runID, Reason: "control-plane run lease expired"}, nil)
	cleanupErr := fence.link.Call(cleanupCtx, protocol.MethodRunClose, protocol.RunCloseParams{RunID: fence.runID}, nil)
	cancel()
	if cleanupErr == nil || remoteRunAlreadyClosed(cleanupErr) {
		r.finishRunForFence(fence, RunStatusCanceled, message, false)
		return
	}

	_ = fence.link.Close()
	r.Detach(fence.runnerID, fence.connectionID, fence.generation, errors.Wrap(cleanupErr, "control-plane run lease cleanup failed"))
}

func (r *Registry) finishRunForFence(fence runFence, status RunStatus, message string, runnerError bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	run := r.runs[fence.runID]
	if !runMatchesFence(run, fence) {
		return false
	}
	runner := r.runners[fence.runnerID]
	if !runnerMatchesFence(runner, fence) || runner.ActiveRunID != fence.runID {
		return false
	}
	r.finishRunLocked(fence.runID, status, message, r.now().UTC())
	if runnerError && runner.Connected {
		runner.Status = RunnerStatusError
		runner.UpdatedAt = r.now().UTC()
	}
	r.logPersistenceError("failed to persist terminal fenced run", r.persistRunnerAndRunLocked(runner, run))
	return true
}

func closeRemoteOpeningRun(link Link, runID string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), openingCleanupTimeout)
	defer cancel()
	err := link.Call(ctx, protocol.MethodRunClose, protocol.RunCloseParams{RunID: runID}, nil)
	if err == nil {
		return true, nil
	}
	if remoteRunAlreadyClosed(err) {
		return true, err
	}
	return false, err
}

func remoteRunAlreadyClosed(err error) bool {
	var rpcErr *protocol.RPCError
	return errors.As(err, &rpcErr) && rpcErr.Code == protocol.ErrorCodeStale && (rpcErr.Reason() == "" || rpcErr.Reason() == protocol.ErrorReasonRunNotActive)
}

func (r *Registry) finishRunLocked(runID string, status RunStatus, message string, now time.Time) {
	run := r.runs[runID]
	if run == nil {
		return
	}
	if run.leaseCancel != nil {
		run.leaseCancel()
		run.leaseCancel = nil
	}
	run.Status = status
	run.Error = message
	run.UpdatedAt = now
	r.affinities.deactivate(run.ConversationID)
	r.clearRunTransientStateLocked(run.ID)
	if runner := r.runners[run.RunnerID]; runner != nil && runner.ActiveRunID == runID {
		runner.ActiveRunID = ""
		if runner.Connected {
			runner.Status = RunnerStatusIdle
		} else {
			runner.Status = RunnerStatusOffline
		}
		runner.UpdatedAt = now
	}
}

func (r *Registry) clearRunTransientStateLocked(runID string) {
	r.toolUpdates.clearRun(runID)
}

func (r *Registry) persistRunnerLocked(entry *runnerEntry) error {
	if r.persistence == nil || entry == nil {
		return nil
	}
	return r.persistence.SaveRunner(r.ctx, entry.Runner)
}

func (r *Registry) persistRunLocked(entry *runEntry) error {
	if r.persistence == nil || entry == nil {
		return nil
	}
	return r.persistence.SaveRun(r.ctx, entry.Run)
}

func (r *Registry) persistRunnerAndRunLocked(runner *runnerEntry, run *runEntry) error {
	if r.persistence == nil || runner == nil || run == nil {
		return nil
	}
	return r.persistence.SaveRunnerAndRun(r.ctx, runner.Runner, run.Run)
}

func (r *Registry) logPersistenceError(message string, err error) {
	if err != nil {
		logger.G(r.ctx).WithError(err).Error(message)
	}
}

func (r *Registry) activeRunLink(runID string) (Link, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.activeRunLinkLocked(runID, false)
}

func (r *Registry) activeRunLinkAllowCanceled(runID string) (Link, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.activeRunLinkLocked(runID, true)
}

func (r *Registry) activeRunLinkLocked(runID string, allowCanceled bool) (Link, error) {
	run := r.runs[strings.TrimSpace(runID)]
	if run == nil {
		return nil, errors.New("run not found")
	}
	if run.Status != RunStatusRunning && run.Status != RunStatusOpening && (!allowCanceled || (run.Status != RunStatusCanceled && run.Status != RunStatusFailed)) {
		return nil, errors.Errorf("run is not active: %s", run.Status)
	}
	runner := r.runners[run.RunnerID]
	if runner == nil || !runner.Connected || runner.link == nil || runner.ActiveRunID != run.ID {
		return nil, errors.New("runner connection for run is unavailable")
	}
	if runner.ConnectionID != run.connectionID || runner.Generation != run.generation {
		return nil, errors.New("runner connection generation for run is stale")
	}
	return runner.link, nil
}

func (r *Registry) currentRunnerLocked(runnerID, connectionID string, generation int64) (*runnerEntry, error) {
	entry := r.runners[strings.TrimSpace(runnerID)]
	if entry == nil {
		return nil, errors.New("runner not found")
	}
	if !entry.Connected || entry.link == nil || entry.ConnectionID != connectionID || entry.Generation != generation {
		return nil, errors.New("runner connection generation is stale")
	}
	return entry, nil
}

func (r *Registry) monitorLiveness() {
	interval := r.heartbeatInterval
	if interval > r.heartbeatTimeout/2 {
		interval = r.heartbeatTimeout / 2
	}
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			r.expireStaleConnections()
		case <-r.ctx.Done():
			return
		}
	}
}

func (r *Registry) expireStaleConnections() {
	now := r.now().UTC()
	type staleConnection struct {
		runnerID     string
		connectionID string
		generation   int64
		link         Link
	}
	var stale []staleConnection
	r.mu.RLock()
	for _, entry := range r.runners {
		if entry.Connected && entry.link != nil && now.Sub(entry.LastHeartbeatAt) > r.heartbeatTimeout {
			stale = append(stale, staleConnection{entry.ID, entry.ConnectionID, entry.Generation, entry.link})
		}
	}
	r.mu.RUnlock()
	for _, connection := range stale {
		r.Detach(connection.runnerID, connection.connectionID, connection.generation, errors.New("runner heartbeat timed out"))
		_ = connection.link.Close()
	}
}

func validateManifest(manifest runnerpayload.Manifest, runnerID string, params protocol.RunOpenParams, generation int64) error {
	if manifest.ProtocolVersion != protocol.Version {
		return errors.Errorf("runner manifest uses protocol version %d", manifest.ProtocolVersion)
	}
	if manifest.RunnerID != runnerID || manifest.RunID != params.RunID || manifest.Generation != generation {
		return errors.New("runner manifest identity does not match opened run")
	}
	reserved := make(map[string]struct{}, len(params.ReservedToolNames))
	for _, name := range params.ReservedToolNames {
		reserved[strings.TrimSpace(name)] = struct{}{}
	}
	seen := make(map[string]struct{}, len(manifest.Tools))
	for _, definition := range manifest.Tools {
		name := strings.TrimSpace(definition.Name)
		if name == "" {
			return errors.New("runner manifest contains a tool without a name")
		}
		if _, exists := reserved[name]; exists {
			return errors.Errorf("runner tool %s collides with a reserved control-plane tool", name)
		}
		if definition.Placement != "environment" {
			return errors.Errorf("runner tool %s has invalid placement %q", name, definition.Placement)
		}
		if _, exists := seen[name]; exists {
			return errors.Errorf("runner manifest contains duplicate tool %s", name)
		}
		seen[name] = struct{}{}
	}
	if information := manifest.Config.SystemInformation; information != nil {
		if strings.TrimSpace(information.Platform) == "" {
			return errors.New("runner manifest system information is missing its platform")
		}
		if strings.TrimSpace(information.OSVersion) == "" {
			return errors.New("runner manifest system information is missing its OS version")
		}
		if _, err := time.Parse("2006-01-02", strings.TrimSpace(information.Date)); err != nil {
			return errors.New("runner manifest system information has an invalid date")
		}
	}
	digest, err := runnerpayload.ComputeManifestDigest(manifest)
	if err != nil {
		return err
	}
	if strings.TrimSpace(manifest.Digest) == "" {
		return errors.New("runner manifest digest is required")
	}
	if manifest.Digest != digest {
		return errors.New("runner manifest digest does not match its contents")
	}
	return nil
}

func runnerIdentity(hostInstanceID, workspacePath string) string {
	return strings.TrimSpace(hostInstanceID) + "\x00" + strings.TrimSpace(workspacePath)
}

func randomID(prefix string) (string, error) {
	data := make([]byte, 12)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return strings.TrimSpace(prefix) + "_" + base64.RawURLEncoding.EncodeToString(data), nil
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func decodeParams[T any](params json.RawMessage) (T, error) {
	var value T
	if len(params) == 0 {
		return value, errors.New("rpc params are required")
	}
	if err := json.Unmarshal(params, &value); err != nil {
		return value, err
	}
	return value, nil
}
