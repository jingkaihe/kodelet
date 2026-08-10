package main

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/jingkaihe/kodelet/pkg/acp"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	"github.com/pkg/errors"
)

type embeddedACPRemoteProvider struct {
	server    string
	authToken string

	mu          sync.Mutex
	ready       bool
	runnerID    string
	client      acp.RemoteChatClient
	terminalErr error
	changed     chan struct{}
}

func newEmbeddedACPRemoteProvider(server, authToken string) *embeddedACPRemoteProvider {
	return &embeddedACPRemoteProvider{
		server:    server,
		authToken: authToken,
		changed:   make(chan struct{}),
	}
}

func (p *embeddedACPRemoteProvider) registeredRunner(result protocol.RegisterResult) {
	client, err := chat.NewControlPlaneChatRunner(p.server, p.authToken, result.RunnerID)
	p.mu.Lock()
	defer p.mu.Unlock()
	if err != nil {
		p.terminalErr = err
		p.ready = false
		p.signalLocked()
		return
	}
	p.client = client
	p.runnerID = result.RunnerID
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
		}
	}
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
			return false, errors.New("embedded workspace runner is incompatible with the control plane")
		}
		if runner.Status == runnerregistry.RunnerStatusError {
			return false, errors.New("embedded workspace runner entered an error state")
		}
		return runner.Connected && (runner.Status == runnerregistry.RunnerStatusIdle || runner.Status == runnerregistry.RunnerStatusBusy), nil
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
