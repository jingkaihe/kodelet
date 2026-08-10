package main

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/jingkaihe/kodelet/pkg/acp"
	"github.com/jingkaihe/kodelet/pkg/chat"
	runnerclient "github.com/jingkaihe/kodelet/pkg/runner/client"
	"github.com/jingkaihe/kodelet/pkg/runner/localstate"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	"github.com/pkg/errors"
)

type embeddedACPRemoteProvider struct {
	server    string
	authToken string
	workspace string
	store     *localstate.Store

	mu          sync.Mutex
	ready       bool
	runnerID    string
	client      acp.RemoteChatClient
	terminalErr error
	changed     chan struct{}
}

func newEmbeddedACPRemoteProvider(server, authToken, workspace string, store *localstate.Store) *embeddedACPRemoteProvider {
	if normalized, err := normalizeRunnerAPIBaseURL(server); err == nil {
		server = normalized
	}
	return &embeddedACPRemoteProvider{
		server:    server,
		authToken: authToken,
		workspace: strings.TrimSpace(workspace),
		store:     store,
		changed:   make(chan struct{}),
	}
}

func (p *embeddedACPRemoteProvider) registeredRunner(result protocol.RegisterResult) {
	p.registeredRunnerID(result.RunnerID)
}

func (p *embeddedACPRemoteProvider) registeredRunnerID(runnerID string) {
	client, err := chat.NewControlPlaneChatRunner(p.server, p.authToken, runnerID)
	p.mu.Lock()
	defer p.mu.Unlock()
	if err != nil {
		p.terminalErr = err
		p.ready = false
		p.signalLocked()
		return
	}
	p.client = client
	p.runnerID = strings.TrimSpace(runnerID)
	p.ready = true
	p.terminalErr = nil
	p.signalLocked()
}

func (p *embeddedACPRemoteProvider) unavailable() {
	p.mu.Lock()
	p.ready = false
	p.signalLocked()
	p.mu.Unlock()
}

func (p *embeddedACPRemoteProvider) fail(err error) {
	p.mu.Lock()
	if err == nil {
		err = errors.New("embedded workspace runner stopped")
	}
	p.terminalErr = err
	p.ready = false
	p.signalLocked()
	p.mu.Unlock()
}

func (p *embeddedACPRemoteProvider) WaitForRemoteChat(ctx context.Context) (acp.RemoteChatClient, string, error) {
	for {
		if err := p.refreshAdvertisedRunner(); err != nil {
			return nil, "", err
		}
		p.mu.Lock()
		if p.ready && p.client != nil && p.runnerID != "" {
			client := p.client
			runnerID := p.runnerID
			changed := p.changed
			p.mu.Unlock()
			ready, err := p.controlPlaneRunnerReady(ctx, runnerID)
			if err != nil {
				return nil, "", err
			}
			if ready {
				p.mu.Lock()
				stillCurrent := p.ready && p.client == client && p.runnerID == runnerID
				p.mu.Unlock()
				if stillCurrent {
					return client, runnerID, nil
				}
				continue
			}
			select {
			case <-ctx.Done():
				return nil, "", ctx.Err()
			case <-changed:
			case <-time.After(50 * time.Millisecond):
			}
			continue
		}
		if p.terminalErr != nil {
			err := p.terminalErr
			p.mu.Unlock()
			return nil, "", err
		}
		changed := p.changed
		p.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-changed:
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (p *embeddedACPRemoteProvider) refreshAdvertisedRunner() error {
	if p == nil || p.store == nil || strings.TrimSpace(p.workspace) == "" {
		return nil
	}
	metadata, found, err := p.store.ReadWorkspaceLockMetadata(p.workspace)
	if err != nil || !found {
		// The lock owner rewrites this file in place. A concurrent read can see
		// incomplete JSON, so treat read failures as transient and retry later.
		return nil
	}
	if metadata.StoppedAt != nil {
		p.markUnavailable()
		return nil
	}
	runnerID := strings.TrimSpace(metadata.RunnerID)
	advertisedServer := strings.TrimSpace(metadata.Server)
	if runnerID == "" || advertisedServer == "" {
		p.markUnavailable()
		return nil
	}
	normalizedServer, err := normalizeRunnerAPIBaseURL(advertisedServer)
	if err != nil {
		return errors.Wrap(err, "workspace lock advertises an invalid control-plane server")
	}
	if normalizedServer != p.server {
		return errors.Errorf("workspace runner is now registered with %s, not %s", normalizedServer, p.server)
	}
	p.mu.Lock()
	currentRunnerID := p.runnerID
	p.mu.Unlock()
	if currentRunnerID != runnerID {
		p.registeredRunnerID(runnerID)
	}
	return nil
}

func (p *embeddedACPRemoteProvider) markUnavailable() {
	p.mu.Lock()
	if p.ready {
		p.ready = false
		p.signalLocked()
	}
	p.mu.Unlock()
}

func (p *embeddedACPRemoteProvider) controlPlaneRunnerReady(ctx context.Context, runnerID string) (bool, error) {
	runners, _, err := fetchRunners(ctx, p.server, p.authToken)
	if err != nil {
		return false, err
	}
	for _, runner := range runners {
		if runner.ID != runnerID {
			continue
		}
		if runner.Status == runnerregistry.RunnerStatusIncompatible {
			if strings.TrimSpace(runner.CompatibilityError) != "" {
				return false, errors.New(runner.CompatibilityError)
			}
			return false, errors.New("workspace runner is incompatible with the control plane")
		}
		if runner.Status == runnerregistry.RunnerStatusError {
			return false, errors.New("workspace runner entered an error state")
		}
		if p.workspace != "" && strings.TrimSpace(runner.Workspace.Path) != p.workspace {
			return false, errors.Errorf("runner %s is bound to workspace %s, not %s", runnerID, runner.Workspace.Path, p.workspace)
		}
		return runner.Connected && (runner.Status == runnerregistry.RunnerStatusIdle || (runner.Status == runnerregistry.RunnerStatusBusy && runner.ConcurrentRuns)), nil
	}
	return false, nil
}

func (p *embeddedACPRemoteProvider) signalLocked() {
	close(p.changed)
	p.changed = make(chan struct{})
}

type acpEmbeddedRunner interface {
	Run(ctx context.Context) error
}

func acquireOrReuseACPRunner(ctx context.Context, runner *runnerclient.Runner, expectedServer string) (bool, string, error) {
	if runner == nil {
		return false, "", errors.New("workspace runner is required")
	}
	expectedServer, err := normalizeRunnerAPIBaseURL(expectedServer)
	if err != nil {
		return false, "", err
	}
	for {
		err := runner.AcquireWorkspaceLock()
		if err == nil {
			return true, "", nil
		}
		var held *localstate.LockHeldError
		if !errors.As(err, &held) {
			return false, "", err
		}
		metadata := held.Metadata
		if metadata.StoppedAt != nil {
			timer := time.NewTimer(50 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return false, "", ctx.Err()
			case <-timer.C:
			}
			continue
		}
		if advertisedServer := strings.TrimSpace(metadata.Server); advertisedServer != "" {
			normalizedServer, normalizeErr := normalizeRunnerAPIBaseURL(advertisedServer)
			if normalizeErr != nil {
				return false, "", errors.Wrap(normalizeErr, "workspace lock advertises an invalid control-plane server")
			}
			if normalizedServer != expectedServer {
				return false, "", errors.Errorf("workspace runner is already registered with %s, not %s", normalizedServer, expectedServer)
			}
		}
		if runnerID := strings.TrimSpace(metadata.RunnerID); runnerID != "" {
			if strings.TrimSpace(metadata.Server) == "" {
				return false, "", errors.New("workspace lock advertises a runner id without a control-plane server")
			}
			return false, runnerID, nil
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false, "", ctx.Err()
		case <-timer.C:
		}
	}
}

func runACPServerWithEmbeddedRunner(
	ctx context.Context,
	server acpServerLifecycle,
	runner acpEmbeddedRunner,
	provider *embeddedACPRemoteProvider,
) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	runnerDone := make(chan error, 1)
	go func() {
		err := runner.Run(runCtx)
		provider.fail(err)
		runnerDone <- err
		cancel()
	}()

	serverErr := runACPServer(runCtx, server)
	cancel()
	runnerErr := <-runnerDone
	if ctx.Err() != nil {
		return nil
	}
	if runnerErr != nil {
		return runnerErr
	}
	return serverErr
}
