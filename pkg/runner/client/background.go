package client

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"sync"

	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/pkg/errors"
)

const (
	maxRunnerBackgroundTasks         = 64
	maxRunnerBackgroundTaskDescBytes = 1024
)

type runnerBackgroundResources struct {
	conversationID     string
	variant            string
	workingDirectory   string
	runtime            *extensions.Runtime
	runtimeRelease     func() error
	runtimeReleaseOnce sync.Once
	runtimeReleaseErr  error
	runtimeLeaseCtx    context.Context
	runtimeLeaseCancel context.CancelFunc
	instance           ExecutionInstance
	clientCaps         protocol.ClientCapabilities
	attachedRunID      string
	lastRunID          string
	runIDs             map[string]struct{}
	leases             map[string]runnerBackgroundLease
	cleanupOnce        sync.Once
	cleanupDone        chan struct{}
	cleanupErr         error
}

type runnerBackgroundLease struct {
	owner       extensions.UIExtensionOwner
	description string
}

func (s *Service) AcquireBackgroundTask(ctx context.Context, source extensions.UIExtensionSource, request extensions.BackgroundTaskAcquireRequest) (extensions.BackgroundTaskAcquireResponse, error) {
	owner, err := runnerBackgroundTaskOwner(source)
	if err != nil {
		return extensions.BackgroundTaskAcquireResponse{}, err
	}
	description := strings.TrimSpace(request.Description)
	if len(description) > maxRunnerBackgroundTaskDescBytes {
		return extensions.BackgroundTaskAcquireResponse{}, errors.Errorf("background task description exceeds %d bytes", maxRunnerBackgroundTaskDescBytes)
	}
	runID := runnerRunIDFromContext(ctx)
	if runID == "" {
		return extensions.BackgroundTaskAcquireResponse{}, errors.New("background task lease requires an active runner run")
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return extensions.BackgroundTaskAcquireResponse{}, errors.New("runner service is closed")
	}
	run := s.runs[runID]
	if run == nil || run.opening || run.closing || run.resources == nil {
		s.mu.Unlock()
		return extensions.BackgroundTaskAcquireResponse{}, errors.New("background task lease requires a ready runner run")
	}
	if len(s.backgroundLeases) >= maxRunnerBackgroundTasks {
		s.mu.Unlock()
		return extensions.BackgroundTaskAcquireResponse{}, errors.Errorf("runner supports at most %d background tasks", maxRunnerBackgroundTasks)
	}
	leaseID, err := newRunnerBackgroundLeaseID()
	if err != nil {
		s.mu.Unlock()
		return extensions.BackgroundTaskAcquireResponse{}, err
	}
	resources := run.resources
	if resources.leases == nil {
		resources.leases = make(map[string]runnerBackgroundLease)
	}
	resources.leases[leaseID] = runnerBackgroundLease{owner: owner, description: description}
	resources.runIDs[run.id] = struct{}{}
	resources.lastRunID = run.id
	resources.clientCaps = mergePersistentClientCapabilities(resources.clientCaps, run.clientCaps)
	s.backgrounds[resources.conversationID] = resources
	s.backgroundRunIDs[run.id] = resources
	s.backgroundLeases[leaseID] = resources
	s.mu.Unlock()

	return extensions.BackgroundTaskAcquireResponse{LeaseID: leaseID}, nil
}

func (s *Service) ReleaseBackgroundTask(_ context.Context, source extensions.UIExtensionSource, request extensions.BackgroundTaskReleaseRequest) (extensions.BackgroundTaskReleaseResponse, error) {
	owner, err := runnerBackgroundTaskOwner(source)
	if err != nil {
		return extensions.BackgroundTaskReleaseResponse{}, err
	}
	leaseID := strings.TrimSpace(request.LeaseID)
	if leaseID == "" {
		return extensions.BackgroundTaskReleaseResponse{}, errors.New("background task lease ID is required")
	}

	s.mu.Lock()
	resources := s.backgroundLeases[leaseID]
	if resources == nil {
		s.mu.Unlock()
		return extensions.BackgroundTaskReleaseResponse{Released: false}, nil
	}
	lease, ok := resources.leases[leaseID]
	if !ok || lease.owner != owner {
		s.mu.Unlock()
		return extensions.BackgroundTaskReleaseResponse{}, errors.New("background task lease is owned by another extension process")
	}
	delete(resources.leases, leaseID)
	delete(s.backgroundLeases, leaseID)
	cleanup := s.detachBackgroundResourcesIfUnusedLocked(resources)
	s.mu.Unlock()
	response := extensions.BackgroundTaskReleaseResponse{Released: true}
	if cleanup {
		response.AfterResponse = func() {
			s.cleanupBackgroundResourcesAsync(resources)
		}
	}
	return response, nil
}

func (s *Service) CleanupBackgroundTasks(owner extensions.UIExtensionOwner) {
	if s == nil || strings.TrimSpace(owner.ExtensionID) == "" || owner.Generation == 0 {
		return
	}
	s.mu.Lock()
	cleanup := make([]*runnerBackgroundResources, 0)
	seen := make(map[*runnerBackgroundResources]struct{})
	for leaseID, resources := range s.backgroundLeases {
		lease, ok := resources.leases[leaseID]
		if !ok || lease.owner != owner {
			continue
		}
		delete(resources.leases, leaseID)
		delete(s.backgroundLeases, leaseID)
		if s.detachBackgroundResourcesIfUnusedLocked(resources) {
			if _, exists := seen[resources]; !exists {
				seen[resources] = struct{}{}
				cleanup = append(cleanup, resources)
			}
		}
	}
	s.mu.Unlock()
	for _, resources := range cleanup {
		s.cleanupBackgroundResourcesAsync(resources)
	}
}

func (s *Service) detachBackgroundResourcesIfUnusedLocked(resources *runnerBackgroundResources) bool {
	if resources == nil || resources.attachedRunID != "" || len(resources.leases) > 0 {
		return false
	}
	if s.backgrounds[resources.conversationID] == resources {
		delete(s.backgrounds, resources.conversationID)
	}
	for runID := range resources.runIDs {
		if s.backgroundRunIDs[runID] == resources {
			delete(s.backgroundRunIDs, runID)
		}
	}
	s.backgroundCleanup[resources] = struct{}{}
	return true
}

func (s *Service) cleanupBackgroundResourcesAsync(resources *runnerBackgroundResources) {
	if resources == nil {
		return
	}
	go func() {
		ctx := context.Background()
		if s.ctx != nil {
			ctx = context.WithoutCancel(s.ctx)
		}
		if err := s.closeBackgroundResources(ctx, resources); err != nil {
			logger.G(ctx).WithError(err).WithField("conversation_id", resources.conversationID).Warn("failed to close runner background resources")
		}
	}()
}

func (s *Service) closeBackgroundResources(ctx context.Context, resources *runnerBackgroundResources) error {
	if resources == nil {
		return nil
	}
	resources.cleanupOnce.Do(func() {
		defer close(resources.cleanupDone)
		if resources.instance != nil {
			resources.cleanupErr = runBoundedCleanup(ctx, s.cleanupTimeout, "runner execution instance", resources.instance.Close)
		}
		if resources.runtimeRelease != nil {
			runtimeErr := runBoundedCleanup(ctx, s.cleanupTimeout, "runner extension runtime", func(context.Context) error {
				resources.runtimeReleaseOnce.Do(func() {
					resources.runtimeReleaseErr = resources.runtimeRelease()
				})
				return resources.runtimeReleaseErr
			})
			resources.cleanupErr = combineCleanupErrors(resources.cleanupErr, runtimeErr)
		}
		if resources.runtimeLeaseCancel != nil {
			resources.runtimeLeaseCancel()
		}
	})
	<-resources.cleanupDone
	s.mu.Lock()
	delete(s.backgroundCleanup, resources)
	s.mu.Unlock()
	return resources.cleanupErr
}

func (s *Service) closeAllBackgroundResources(ctx context.Context) error {
	s.mu.Lock()
	resources := make([]*runnerBackgroundResources, 0, len(s.backgrounds))
	seen := make(map[*runnerBackgroundResources]struct{})
	for _, candidate := range s.backgrounds {
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		resources = append(resources, candidate)
	}
	for candidate := range s.backgroundCleanup {
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		resources = append(resources, candidate)
	}
	s.backgrounds = make(map[string]*runnerBackgroundResources)
	s.backgroundRunIDs = make(map[string]*runnerBackgroundResources)
	s.backgroundLeases = make(map[string]*runnerBackgroundResources)
	s.backgroundCleanup = make(map[*runnerBackgroundResources]struct{})
	s.mu.Unlock()

	var firstErr error
	for _, candidate := range resources {
		if err := s.closeBackgroundResources(ctx, candidate); firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func runnerBackgroundTaskOwner(source extensions.UIExtensionSource) (extensions.UIExtensionOwner, error) {
	if source == nil {
		return extensions.UIExtensionOwner{}, errors.New("extension background task source is required")
	}
	owner := source.ExtensionUIOwner()
	owner.ExtensionID = strings.TrimSpace(owner.ExtensionID)
	if owner.ExtensionID == "" || owner.Generation == 0 {
		return extensions.UIExtensionOwner{}, errors.New("extension background task owner is invalid")
	}
	return owner, nil
}

func runnerRunIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	runID, _ := ctx.Value(runnerRunIDContextKey{}).(string)
	return strings.TrimSpace(runID)
}

func mergePersistentClientCapabilities(current, next protocol.ClientCapabilities) protocol.ClientCapabilities {
	current.PersistentWidgets = current.PersistentWidgets || next.PersistentWidgets
	current.PersistentSurfaces = current.PersistentSurfaces || next.PersistentSurfaces
	return current
}

func newRunnerBackgroundLeaseID() (string, error) {
	random := make([]byte, 24)
	if _, err := rand.Read(random); err != nil {
		return "", errors.Wrap(err, "failed to generate background task lease ID")
	}
	return "background_" + base64.RawURLEncoding.EncodeToString(random), nil
}
