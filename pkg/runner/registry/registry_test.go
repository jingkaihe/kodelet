package registry

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	conversationsqlite "github.com/jingkaihe/kodelet/pkg/conversations/sqlite"
	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	conversationtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeLink struct {
	mu       sync.Mutex
	call     func(context.Context, string, any, any) error
	closed   bool
	done     chan struct{}
	doneOnce sync.Once
	err      error
}

type testPersistence struct {
	state               PersistedState
	saveRunnerErr       error
	saveRunnerAndRunErr error
	saveRunnerCalls     int
}

type fakeCredentialAuthorizer struct {
	bound                  bool
	err                    error
	credentialActive       bool
	credentialErr          error
	host                   string
	workspace              string
	credentialID           string
	runnerID               string
	credentialRequestCount int
	requestCount           int
}

func (a *fakeCredentialAuthorizer) HasActiveRunnerCredential(_ context.Context, hostInstanceID, workspacePath string) (bool, error) {
	a.requestCount++
	a.host = hostInstanceID
	a.workspace = workspacePath
	return a.bound, a.err
}

func (a *fakeCredentialAuthorizer) RunnerCredentialActive(_ context.Context, credentialID, runnerID, hostInstanceID, workspacePath string) (bool, error) {
	a.credentialRequestCount++
	a.credentialID = credentialID
	a.runnerID = runnerID
	a.host = hostInstanceID
	a.workspace = workspacePath
	return a.credentialActive, a.credentialErr
}

func (p *testPersistence) Load(context.Context) (PersistedState, error) {
	state := p.state
	state.Runners = append([]Runner(nil), state.Runners...)
	state.Runs = append([]Run(nil), state.Runs...)
	state.Affinities = make(map[string]ConversationAffinity, len(p.state.Affinities))
	for conversationID, affinity := range p.state.Affinities {
		state.Affinities[conversationID] = affinity
	}
	return state, nil
}

func (p *testPersistence) SaveRunner(_ context.Context, runner Runner) error {
	p.saveRunnerCalls++
	if p.saveRunnerErr != nil {
		return p.saveRunnerErr
	}
	p.upsertRunner(runner)
	return nil
}

func (p *testPersistence) SaveRun(_ context.Context, run Run) error {
	p.upsertRun(run)
	return nil
}

func (p *testPersistence) SaveRunnerAndRun(_ context.Context, runner Runner, run Run) error {
	return p.SaveRunnerAndRuns(context.Background(), runner, []Run{run})
}

func (p *testPersistence) SaveRunnerAndRuns(_ context.Context, runner Runner, runs []Run) error {
	if p.saveRunnerAndRunErr != nil {
		return p.saveRunnerAndRunErr
	}
	p.upsertRunner(runner)
	for _, run := range runs {
		p.upsertRun(run)
	}
	return nil
}

func (p *testPersistence) BindConversation(_ context.Context, conversationID, runnerID, environmentProfile string, _ time.Time) error {
	if p.state.Affinities == nil {
		p.state.Affinities = make(map[string]ConversationAffinity)
	}
	if existing, ok := p.state.Affinities[conversationID]; ok && (existing.RunnerID != runnerID || existing.EnvironmentProfile != environmentProfile) {
		return errors.New("conversation is already bound to another runner")
	}
	p.state.Affinities[conversationID] = ConversationAffinity{RunnerID: runnerID, EnvironmentProfile: environmentProfile}
	return nil
}

func (p *testPersistence) ConversationAffinity(_ context.Context, conversationID string) (ConversationAffinity, bool, error) {
	affinity, ok := p.state.Affinities[conversationID]
	return affinity, ok, nil
}

func (*testPersistence) RemoveRunner(context.Context, string, bool) (RemovalResult, error) {
	return RemovalResult{}, errors.New("not implemented")
}

func (*testPersistence) Close() error { return nil }

func (p *testPersistence) upsertRunner(runner Runner) {
	for i := range p.state.Runners {
		if p.state.Runners[i].ID == runner.ID {
			p.state.Runners[i] = runner
			return
		}
	}
	p.state.Runners = append(p.state.Runners, runner)
}

func (p *testPersistence) upsertRun(run Run) {
	for i := range p.state.Runs {
		if p.state.Runs[i].ID == run.ID {
			p.state.Runs[i] = run
			return
		}
	}
	p.state.Runs = append(p.state.Runs, run)
}

type fakeUIRequestRouter struct {
	runnerID string
	method   string
	params   json.RawMessage
}

func (r *fakeUIRequestRouter) HandleRunnerUIRequest(_ context.Context, runnerID string, method string, params json.RawMessage) (any, *protocol.RPCError) {
	r.runnerID = runnerID
	r.method = method
	r.params = append(json.RawMessage(nil), params...)
	return map[string]bool{"ok": true}, nil
}

func newFakeLink() *fakeLink {
	return &fakeLink{done: make(chan struct{})}
}

func (l *fakeLink) Call(ctx context.Context, method string, params any, result any) error {
	l.mu.Lock()
	call := l.call
	closed := l.closed
	l.mu.Unlock()
	if closed {
		return protocol.ErrPeerClosed
	}
	if call != nil {
		return call(ctx, method, params, result)
	}
	return nil
}

func (l *fakeLink) CallTracked(ctx context.Context, method string, params any, result any, onRequestID func(string)) error {
	if onRequestID != nil {
		onRequestID("fake:request")
	}
	return l.Call(ctx, method, params, result)
}

func (l *fakeLink) Notify(context.Context, string, any) error { return nil }

func (l *fakeLink) Close() error {
	l.mu.Lock()
	l.closed = true
	l.mu.Unlock()
	l.doneOnce.Do(func() { close(l.done) })
	return nil
}

func (l *fakeLink) Done() <-chan struct{} { return l.done }
func (l *fakeLink) Err() error            { return l.err }

func (l *fakeLink) isClosed() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closed
}

func TestRegisterUpsertsWorkspaceIdentityAndFencesStaleConnections(t *testing.T) {
	registry := newTestRegistry(t)
	firstLink := newFakeLink()
	params := testRegisterParams("host-one", "/work/project")

	first, err := registry.Register(params, firstLink)
	require.NoError(t, err)
	assert.Equal(t, int64(1), first.Generation)

	secondLink := newFakeLink()
	params.RunnerID = first.RunnerID
	params.DisplayName = "renamed"
	second, err := registry.Register(params, secondLink)
	require.NoError(t, err)
	assert.Equal(t, first.RunnerID, second.RunnerID)
	assert.Equal(t, int64(2), second.Generation)
	assert.True(t, firstLink.isClosed())

	registry.Detach(first.RunnerID, first.ConnectionID, first.Generation, context.Canceled)
	runner, ok := registry.Runner(first.RunnerID)
	require.True(t, ok)
	assert.True(t, runner.Connected)
	assert.Equal(t, second.ConnectionID, runner.ConnectionID)
	assert.Equal(t, "renamed", runner.DisplayName)

	params.Workspace.Path = "/work/other"
	_, err = registry.Register(params, newFakeLink())
	assert.ErrorContains(t, err, "another host workspace")
}

func TestRegisterAuthenticatedBindsConnectionToEnrolledRunnerIdentity(t *testing.T) {
	registry := newTestRegistry(t)
	enrollment := testEnrollmentStartRequest(t, "host-enrolled", "/work/enrolled")
	offline, err := registry.EnsureOfflineRegistration(enrollment)
	require.NoError(t, err)

	params := testRegisterParams("host-enrolled", "/work/enrolled")
	registration, err := registry.RegisterAuthenticated(params, newFakeLink(), RegistrationPrincipal{
		Mode:           RegistrationAuthKey,
		CredentialID:   "credential-one",
		RunnerID:       offline.ID,
		HostInstanceID: "host-enrolled",
		WorkspacePath:  "/work/enrolled",
	})
	require.NoError(t, err)
	assert.Equal(t, offline.ID, registration.RunnerID)

	params.RunnerID = "runner-other"
	_, err = registry.RegisterAuthenticated(params, newFakeLink(), RegistrationPrincipal{
		Mode:           RegistrationAuthKey,
		CredentialID:   "credential-one",
		RunnerID:       offline.ID,
		HostInstanceID: "host-enrolled",
		WorkspacePath:  "/work/enrolled",
	})
	require.ErrorContains(t, err, "runner id does not match")

	params.RunnerID = ""
	params.Workspace.Path = "/work/other"
	_, err = registry.RegisterAuthenticated(params, newFakeLink(), RegistrationPrincipal{
		Mode:           RegistrationAuthKey,
		CredentialID:   "credential-one",
		RunnerID:       offline.ID,
		HostInstanceID: "host-enrolled",
		WorkspacePath:  "/work/enrolled",
	})
	require.ErrorContains(t, err, "does not match its enrolled credential")

	_, err = registry.RegisterAuthenticated(testRegisterParams("host-enrolled", "/work/enrolled"), newFakeLink(), RegistrationPrincipal{Mode: RegistrationAuthKey})
	require.ErrorContains(t, err, "principal is incomplete")
}

func TestKeyRegistrationRevalidatesCredentialAndReplacementDisconnectsOldConnection(t *testing.T) {
	authorizer := &fakeCredentialAuthorizer{credentialActive: true}
	registry, err := New(t.Context(), Options{
		HeartbeatInterval: time.Hour,
		HeartbeatTimeout:  2 * time.Hour,
		NewID:             sequentialIDs(),
		Credentials:       authorizer,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = registry.Close() })

	offline, err := registry.EnsureOfflineRegistration(testEnrollmentStartRequest(t, "host-enrolled", "/work/enrolled"))
	require.NoError(t, err)
	oldLink := newFakeLink()
	oldRegistration, err := registry.RegisterAuthenticated(testRegisterParams("host-enrolled", "/work/enrolled"), oldLink, RegistrationPrincipal{
		Mode:           RegistrationAuthKey,
		CredentialID:   "credential-old",
		RunnerID:       offline.ID,
		HostInstanceID: "host-enrolled",
		WorkspacePath:  "/work/enrolled",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, authorizer.credentialRequestCount)
	assert.Equal(t, "credential-old", authorizer.credentialID)
	assert.Equal(t, offline.ID, authorizer.runnerID)

	registry.DisconnectRunnerExceptCredential(oldRegistration.RunnerID, "credential-new", errors.New("credential replaced"))
	assert.True(t, oldLink.isClosed())
	runner, ok := registry.Runner(oldRegistration.RunnerID)
	require.True(t, ok)
	assert.False(t, runner.Connected)

	newLink := newFakeLink()
	newRegistration, err := registry.RegisterAuthenticated(testRegisterParams("host-enrolled", "/work/enrolled"), newLink, RegistrationPrincipal{
		Mode:           RegistrationAuthKey,
		CredentialID:   "credential-new",
		RunnerID:       offline.ID,
		HostInstanceID: "host-enrolled",
		WorkspacePath:  "/work/enrolled",
	})
	require.NoError(t, err)
	registry.DisconnectRunnerExceptCredential(newRegistration.RunnerID, "credential-new", errors.New("credential replaced"))
	assert.False(t, newLink.isClosed())

	authorizer.credentialActive = false
	_, err = registry.RegisterAuthenticated(testRegisterParams("host-enrolled", "/work/enrolled"), newFakeLink(), RegistrationPrincipal{
		Mode:           RegistrationAuthKey,
		CredentialID:   "credential-revoked",
		RunnerID:       offline.ID,
		HostInstanceID: "host-enrolled",
		WorkspacePath:  "/work/enrolled",
	})
	require.ErrorContains(t, err, "invalid or revoked")
}

func TestLegacyRegistrationIsBlockedAfterKeyEnrollment(t *testing.T) {
	authorizer := &fakeCredentialAuthorizer{bound: true}
	registry, err := New(t.Context(), Options{
		HeartbeatInterval: time.Hour,
		HeartbeatTimeout:  2 * time.Hour,
		NewID:             sequentialIDs(),
		Credentials:       authorizer,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = registry.Close() })

	_, err = registry.Register(testRegisterParams("host-one", "/work/project"), newFakeLink())
	require.ErrorContains(t, err, "cannot use legacy authentication")
	assert.Equal(t, 1, authorizer.requestCount)
	assert.Equal(t, "host-one", authorizer.host)
	assert.Equal(t, "/work/project", authorizer.workspace)

	authorizer.bound = false
	registration, err := registry.Register(testRegisterParams("host-one", "/work/project"), newFakeLink())
	require.NoError(t, err)
	assert.NotEmpty(t, registration.RunnerID)

	authorizer.err = errors.New("credential lookup failed")
	_, err = registry.Register(testRegisterParams("host-two", "/work/other"), newFakeLink())
	require.ErrorContains(t, err, "failed to inspect runner credential binding")
}

func TestEnsureOfflineRegistrationPreservesIdentityAndRequiresStoppedRunner(t *testing.T) {
	registry := newTestRegistry(t)
	enrollment := testEnrollmentStartRequest(t, "host-one", "/work/project")
	enrollment.DisplayName = "first"
	offline, err := registry.EnsureOfflineRegistration(enrollment)
	require.NoError(t, err)
	assert.Equal(t, RunnerStatusOffline, offline.Status)

	registration, err := registry.RegisterAuthenticated(testRegisterParams("host-one", "/work/project"), newFakeLink(), RegistrationPrincipal{
		Mode:           RegistrationAuthKey,
		CredentialID:   "credential-one",
		RunnerID:       offline.ID,
		HostInstanceID: "host-one",
		WorkspacePath:  "/work/project",
	})
	require.NoError(t, err)
	live, found := registry.Runner(registration.RunnerID)
	require.True(t, found)
	enrollment.DisplayName = "second"
	_, err = registry.EnsureOfflineRegistration(enrollment)
	require.ErrorIs(t, err, ErrRunnerConnected)
	replacementTarget, err := registry.EnsureEnrollmentRegistration(enrollment, true)
	require.NoError(t, err)
	assert.Equal(t, offline.ID, replacementTarget.ID)
	assert.True(t, replacementTarget.Connected)
	assert.Equal(t, live.DisplayName, replacementTarget.DisplayName)

	registry.Detach(registration.RunnerID, registration.ConnectionID, registration.Generation, context.Canceled)
	updated, err := registry.EnsureOfflineRegistration(enrollment)
	require.NoError(t, err)
	assert.Equal(t, offline.ID, updated.ID)
	assert.Equal(t, "second", updated.DisplayName)
}

func TestRegisterPersistenceFailureDoesNotPublishRunner(t *testing.T) {
	persistence := &testPersistence{saveRunnerErr: errors.New("save failed")}
	registry, err := New(t.Context(), Options{
		HeartbeatInterval: time.Hour,
		HeartbeatTimeout:  2 * time.Hour,
		NewID:             sequentialIDs(),
		Persistence:       persistence,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = registry.Close() })

	params := testRegisterParams("host-one", "/work/project")
	_, err = registry.Register(params, newFakeLink())
	require.ErrorContains(t, err, "save failed")
	assert.Empty(t, registry.Runners())

	persistence.saveRunnerErr = nil
	registration, err := registry.Register(params, newFakeLink())
	require.NoError(t, err)
	assert.NotEmpty(t, registration.RunnerID)
	assert.Len(t, registry.Runners(), 1)
}

func TestReconnectPersistenceFailureKeepsPriorGenerationAndRun(t *testing.T) {
	persistence := &testPersistence{}
	registry, err := New(t.Context(), Options{
		HeartbeatInterval: time.Hour,
		HeartbeatTimeout:  2 * time.Hour,
		NewID:             sequentialIDs(),
		Persistence:       persistence,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		persistence.saveRunnerAndRunErr = nil
		_ = registry.Close()
	})

	firstLink := newFakeLink()
	registration, err := registry.Register(testRegisterParams("host-one", "/work/project"), firstLink)
	require.NoError(t, err)
	configureManifestLink(t, firstLink, registration)
	markRunnerReady(t, registry, registration)
	_, err = registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-one", "conversation-one"))
	require.NoError(t, err)

	persistence.saveRunnerAndRunErr = errors.New("transaction failed")
	_, err = registry.Register(testRegisterParams("host-one", "/work/project"), newFakeLink())
	require.ErrorContains(t, err, "transaction failed")

	runner, ok := registry.Runner(registration.RunnerID)
	require.True(t, ok)
	assert.Equal(t, registration.Generation, runner.Generation)
	assert.Equal(t, registration.ConnectionID, runner.ConnectionID)
	assert.Equal(t, "run-one", runner.ActiveRunID)
	assert.False(t, firstLink.isClosed())
	run, ok := registry.Run("run-one")
	require.True(t, ok)
	assert.Equal(t, RunStatusRunning, run.Status)
}

func TestHeartbeatStateGatesScheduling(t *testing.T) {
	registry := newTestRegistry(t)
	link := newFakeLink()
	registration, err := registry.Register(testRegisterParams("host-one", "/work/project"), link)
	require.NoError(t, err)

	err = registry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.HeartbeatParams{
		RunnerID:   registration.RunnerID,
		Generation: registration.Generation,
		State:      protocol.RunnerState("future"),
	})
	require.ErrorContains(t, err, "unsupported runner state")
	runner, ok := registry.Runner(registration.RunnerID)
	require.True(t, ok)
	assert.Equal(t, RunnerStatusConnecting, runner.Status)
	_, err = registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-one", "conversation-one"))
	require.ErrorContains(t, err, "initial heartbeat")

	require.NoError(t, registry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.HeartbeatParams{
		RunnerID:   registration.RunnerID,
		Generation: registration.Generation,
		State:      protocol.RunnerStateError,
	}))
	runner, ok = registry.Runner(registration.RunnerID)
	require.True(t, ok)
	assert.Equal(t, RunnerStatusError, runner.Status)
	_, err = registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-two", "conversation-two"))
	require.ErrorContains(t, err, "runner is not available")
}

func TestHeartbeatUpdatesLiveStateWithoutWritingSQLiteState(t *testing.T) {
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	persistence := &testPersistence{}
	registry, err := New(t.Context(), Options{
		HeartbeatInterval: time.Hour,
		HeartbeatTimeout:  2 * time.Hour,
		Now:               func() time.Time { return now },
		NewID:             sequentialIDs(),
		Persistence:       persistence,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = registry.Close() })
	registration, err := registry.Register(testRegisterParams("host-one", "/work/project"), newFakeLink())
	require.NoError(t, err)
	writesAfterRegistration := persistence.saveRunnerCalls
	persistedHeartbeat := persistence.state.Runners[0].LastHeartbeatAt

	now = now.Add(time.Minute)
	markRunnerReady(t, registry, registration)
	runner, ok := registry.Runner(registration.RunnerID)
	require.True(t, ok)
	assert.Equal(t, now, runner.LastHeartbeatAt)
	assert.Equal(t, writesAfterRegistration, persistence.saveRunnerCalls)
	assert.Equal(t, persistedHeartbeat, persistence.state.Runners[0].LastHeartbeatAt)
}

func TestHeartbeatAllowsOpeningRunNotYetReportedByRunner(t *testing.T) {
	registry := newTestRegistry(t)
	link := newFakeLink()
	registration, err := registry.Register(testRegisterParams("host-one", "/work/project"), link)
	require.NoError(t, err)
	configureManifestLink(t, link, registration)
	markRunnerReady(t, registry, registration)

	openStarted := make(chan struct{})
	releaseOpen := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseOpen) }) })
	link.mu.Lock()
	manifestCall := link.call
	link.call = func(ctx context.Context, method string, params any, result any) error {
		if method == protocol.MethodRunOpen {
			close(openStarted)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-releaseOpen:
			}
		}
		return manifestCall(ctx, method, params, result)
	}
	link.mu.Unlock()

	type openResult struct {
		manifest runnerpayload.Manifest
		err      error
	}
	opened := make(chan openResult, 1)
	go func() {
		manifest, openErr := registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-one", "conversation-one"))
		opened <- openResult{manifest: manifest, err: openErr}
	}()
	select {
	case <-openStarted:
	case <-time.After(time.Second):
		t.Fatal("run.open did not reach the runner")
	}

	require.NoError(t, registry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.HeartbeatParams{
		RunnerID:   registration.RunnerID,
		Generation: registration.Generation,
		State:      protocol.RunnerStateIdle,
	}))
	runner, ok := registry.Runner(registration.RunnerID)
	require.True(t, ok)
	assert.Equal(t, RunnerStatusBusy, runner.Status)
	assert.Equal(t, []string{"run-one"}, runner.ActiveRunIDs)

	releaseOnce.Do(func() { close(releaseOpen) })
	select {
	case result := <-opened:
		require.NoError(t, result.err)
		assert.Equal(t, "run-one", result.manifest.RunID)
	case <-time.After(time.Second):
		t.Fatal("run.open did not finish after the runner was released")
	}
	require.NoError(t, registry.CloseRun(t.Context(), "run-one", RunStatusSucceeded, nil))
}

func TestHeartbeatRunMismatchDoesNotSuppressConnectionLiveness(t *testing.T) {
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	registry, err := New(t.Context(), Options{
		HeartbeatInterval: time.Hour,
		HeartbeatTimeout:  2 * time.Hour,
		Now:               func() time.Time { return now },
		NewID:             sequentialIDs(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = registry.Close() })
	link := newFakeLink()
	registration, err := registry.Register(testRegisterParams("host-one", "/work/project"), link)
	require.NoError(t, err)
	configureManifestLink(t, link, registration)
	markRunnerReady(t, registry, registration)
	_, err = registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-one", "conversation-one"))
	require.NoError(t, err)

	now = now.Add(time.Minute)
	require.NoError(t, registry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.HeartbeatParams{
		RunnerID:   registration.RunnerID,
		Generation: registration.Generation,
		State:      protocol.RunnerStateIdle,
	}))
	runner, ok := registry.Runner(registration.RunnerID)
	require.True(t, ok)
	assert.Equal(t, now, runner.LastHeartbeatAt)
	assert.Equal(t, RunnerStatusBusy, runner.Status)
	assert.Equal(t, []string{"run-one"}, runner.ActiveRunIDs)
	run, ok := registry.Run("run-one")
	require.True(t, ok)
	assert.Equal(t, RunStatusRunning, run.Status)
	assert.False(t, link.isClosed())

	require.NoError(t, registry.CloseRun(t.Context(), "run-one", RunStatusSucceeded, nil))
	now = now.Add(time.Minute)
	require.NoError(t, registry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.HeartbeatParams{
		RunnerID:     registration.RunnerID,
		Generation:   registration.Generation,
		State:        protocol.RunnerStateRunning,
		ActiveRunIDs: []string{"stale-run"},
	}))
	runner, ok = registry.Runner(registration.RunnerID)
	require.True(t, ok)
	assert.Equal(t, now, runner.LastHeartbeatAt)
	assert.Equal(t, RunnerStatusIdle, runner.Status)
	assert.Empty(t, runner.ActiveRunIDs)
}

func TestMalformedHeartbeatRunSetStillRefreshesConnectionLiveness(t *testing.T) {
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	registry, err := New(t.Context(), Options{
		HeartbeatInterval: time.Hour,
		HeartbeatTimeout:  2 * time.Hour,
		Now:               func() time.Time { return now },
		NewID:             sequentialIDs(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = registry.Close() })
	registration, err := registry.Register(testRegisterParams("host-one", "/work/project"), newFakeLink())
	require.NoError(t, err)
	markRunnerReady(t, registry, registration)

	now = now.Add(time.Minute)
	err = registry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.HeartbeatParams{
		RunnerID:       registration.RunnerID,
		Generation:     registration.Generation,
		State:          protocol.RunnerStateRunning,
		ActiveRunIDs:   []string{"duplicate-run", "duplicate-run"},
		ManifestDigest: "digest-after-malformed-heartbeat",
	})
	require.ErrorContains(t, err, "duplicate run")
	runner, ok := registry.Runner(registration.RunnerID)
	require.True(t, ok)
	assert.Equal(t, now, runner.LastHeartbeatAt)
	assert.Equal(t, "digest-after-malformed-heartbeat", runner.ManifestDigest)
	assert.Equal(t, RunnerStatusIdle, runner.Status)
}

func TestHeartbeatReleasesRunAfterConsecutiveOmissions(t *testing.T) {
	registry := newTestRegistry(t)
	lostConversations := make(chan string, 1)
	registry.SetEnvironmentErrorHandler(func(conversationID string) {
		lostConversations <- conversationID
	})
	link := newFakeLink()
	registration, err := registry.Register(testRegisterParams("host-one", "/work/project"), link)
	require.NoError(t, err)
	configureManifestLink(t, link, registration)
	markRunnerReady(t, registry, registration)
	_, err = registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-one", "conversation-one"))
	require.NoError(t, err)

	missingHeartbeat := protocol.HeartbeatParams{
		RunnerID:   registration.RunnerID,
		Generation: registration.Generation,
		State:      protocol.RunnerStateIdle,
	}
	require.NoError(t, registry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, missingHeartbeat))
	run, ok := registry.Run("run-one")
	require.True(t, ok)
	assert.Equal(t, RunStatusRunning, run.Status)
	runner, ok := registry.Runner(registration.RunnerID)
	require.True(t, ok)
	assert.Equal(t, RunnerStatusBusy, runner.Status)
	assert.Equal(t, []string{"run-one"}, runner.ActiveRunIDs)

	require.NoError(t, registry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, missingHeartbeat))
	run, ok = registry.Run("run-one")
	require.True(t, ok)
	assert.Equal(t, RunStatusRunning, run.Status)

	require.NoError(t, registry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, missingHeartbeat))
	run, ok = registry.Run("run-one")
	require.True(t, ok)
	assert.Equal(t, RunStatusLost, run.Status)
	assert.Contains(t, run.Error, "3 consecutive heartbeats")
	runner, ok = registry.Runner(registration.RunnerID)
	require.True(t, ok)
	assert.Equal(t, RunnerStatusIdle, runner.Status)
	assert.Empty(t, runner.ActiveRunIDs)
	select {
	case conversationID := <-lostConversations:
		assert.Equal(t, "conversation-one", conversationID)
	case <-time.After(time.Second):
		t.Fatal("missing run reconciliation did not cancel the owning conversation")
	}

	_, err = registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-replacement", "conversation-one"))
	require.NoError(t, err)
	require.NoError(t, registry.CloseRun(t.Context(), "run-replacement", RunStatusSucceeded, nil))
}

func TestOpenRunReservationFailureRollsBackPendingAffinityAndCapacity(t *testing.T) {
	persistence := &testPersistence{}
	registry, err := New(t.Context(), Options{
		HeartbeatInterval: time.Hour,
		HeartbeatTimeout:  2 * time.Hour,
		NewID:             sequentialIDs(),
		Persistence:       persistence,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		persistence.saveRunnerAndRunErr = nil
		_ = registry.Close()
	})
	link := newFakeLink()
	registration, err := registry.Register(testRegisterParams("host-one", "/work/project"), link)
	require.NoError(t, err)
	markRunnerReady(t, registry, registration)

	persistence.saveRunnerAndRunErr = errors.New("reservation failed")
	_, err = registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-one", "conversation-one"))
	require.ErrorContains(t, err, "reservation failed")
	_, ok := registry.RunnerForConversation("conversation-one")
	assert.False(t, ok)
	_, ok = registry.Run("run-one")
	assert.False(t, ok)
	runner, ok := registry.Runner(registration.RunnerID)
	require.True(t, ok)
	assert.Equal(t, RunnerStatusIdle, runner.Status)
	assert.Empty(t, runner.ActiveRunID)
}

func TestOpenRunSupportsRunnerConcurrencyAndEnforcesConversationAffinity(t *testing.T) {
	registry := newTestRegistry(t)
	firstLink := newFakeLink()
	firstRegistration, err := registry.Register(testRegisterParams("host-one", "/work/one"), firstLink)
	require.NoError(t, err)
	configureManifestLink(t, firstLink, firstRegistration)
	markRunnerReady(t, registry, firstRegistration)

	params := testRunOpenParams("run-one", "conversation-one")
	manifest, err := registry.OpenRun(t.Context(), firstRegistration.RunnerID, params)
	require.NoError(t, err)
	assert.Equal(t, "run-one", manifest.RunID)

	secondManifest, err := registry.OpenRun(t.Context(), firstRegistration.RunnerID, testRunOpenParams("run-two", "conversation-two"))
	require.NoError(t, err)
	assert.Equal(t, "run-two", secondManifest.RunID)
	runner, ok := registry.Runner(firstRegistration.RunnerID)
	require.True(t, ok)
	assert.Equal(t, RunnerStatusBusy, runner.Status)
	assert.Empty(t, runner.ActiveRunID)
	assert.Equal(t, []string{"run-one", "run-two"}, runner.ActiveRunIDs)
	require.NoError(t, registry.Heartbeat(firstRegistration.RunnerID, firstRegistration.ConnectionID, firstRegistration.Generation, protocol.HeartbeatParams{
		RunnerID:     firstRegistration.RunnerID,
		Generation:   firstRegistration.Generation,
		State:        protocol.RunnerStateRunning,
		ActiveRunIDs: []string{"run-two", "run-one"},
	}))

	secondLink := newFakeLink()
	secondRegistration, err := registry.Register(testRegisterParams("host-two", "/work/two"), secondLink)
	require.NoError(t, err)
	configureManifestLink(t, secondLink, secondRegistration)
	markRunnerReady(t, registry, secondRegistration)
	_, err = registry.OpenRun(t.Context(), secondRegistration.RunnerID, testRunOpenParams("run-three", "conversation-one"))
	assert.ErrorContains(t, err, "conversation is bound to runner")

	require.NoError(t, registry.CloseRun(t.Context(), "run-one", RunStatusSucceeded, nil))
	run, ok := registry.Run("run-one")
	require.True(t, ok)
	assert.Equal(t, RunStatusSucceeded, run.Status)
	var persistedManifest runnerpayload.Manifest
	require.NoError(t, json.Unmarshal([]byte(run.ManifestJSON), &persistedManifest))
	assert.Equal(t, manifest, persistedManifest)
	runner, ok = registry.Runner(firstRegistration.RunnerID)
	require.True(t, ok)
	assert.Equal(t, RunnerStatusBusy, runner.Status)
	assert.Equal(t, "run-two", runner.ActiveRunID)
	assert.Equal(t, []string{"run-two"}, runner.ActiveRunIDs)
	require.NoError(t, registry.CloseRun(t.Context(), "run-two", RunStatusSucceeded, nil))
	runner, ok = registry.Runner(firstRegistration.RunnerID)
	require.True(t, ok)
	assert.Equal(t, RunnerStatusIdle, runner.Status)
	assert.Empty(t, runner.ActiveRunID)
	assert.Empty(t, runner.ActiveRunIDs)
}

func TestOpenRunKeepsLegacyRunnerCapacityOne(t *testing.T) {
	registry := newTestRegistry(t)
	link := newFakeLink()
	params := testRegisterParams("host-one", "/work/project")
	params.Capabilities.ConcurrentRuns = false
	registration, err := registry.Register(params, link)
	require.NoError(t, err)
	configureManifestLink(t, link, registration)
	markRunnerReady(t, registry, registration)
	_, err = registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-one", "conversation-one"))
	require.NoError(t, err)

	_, err = registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-two", "conversation-two"))
	require.ErrorContains(t, err, "does not support concurrent runs")
	require.NoError(t, registry.CloseRun(t.Context(), "run-one", RunStatusSucceeded, nil))
}

func TestClosingConcurrentRunPreservesRunnerErrorState(t *testing.T) {
	registry := newTestRegistry(t)
	link := newFakeLink()
	registration, err := registry.Register(testRegisterParams("host-one", "/work/project"), link)
	require.NoError(t, err)
	configureManifestLink(t, link, registration)
	markRunnerReady(t, registry, registration)
	_, err = registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-one", "conversation-one"))
	require.NoError(t, err)
	_, err = registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-two", "conversation-two"))
	require.NoError(t, err)
	require.NoError(t, registry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.HeartbeatParams{
		RunnerID:     registration.RunnerID,
		Generation:   registration.Generation,
		State:        protocol.RunnerStateError,
		ActiveRunIDs: []string{"run-one", "run-two"},
	}))

	require.NoError(t, registry.CloseRun(t.Context(), "run-one", RunStatusSucceeded, nil))
	runner, ok := registry.Runner(registration.RunnerID)
	require.True(t, ok)
	assert.Equal(t, RunnerStatusError, runner.Status)
	assert.Equal(t, []string{"run-two"}, runner.ActiveRunIDs)
	_, err = registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-three", "conversation-three"))
	require.ErrorContains(t, err, "runner is not available: error")

	require.NoError(t, registry.CloseRun(t.Context(), "run-two", RunStatusSucceeded, nil))
	runner, ok = registry.Runner(registration.RunnerID)
	require.True(t, ok)
	assert.Equal(t, RunnerStatusError, runner.Status)
	assert.Empty(t, runner.ActiveRunIDs)
}

func TestReconnectMarksAllActiveRunsLost(t *testing.T) {
	registry := newTestRegistry(t)
	firstLink := newFakeLink()
	registration, err := registry.Register(testRegisterParams("host-one", "/work/project"), firstLink)
	require.NoError(t, err)
	configureManifestLink(t, firstLink, registration)
	markRunnerReady(t, registry, registration)
	_, err = registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-one", "conversation-one"))
	require.NoError(t, err)
	_, err = registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-two", "conversation-two"))
	require.NoError(t, err)

	params := testRegisterParams("host-one", "/work/project")
	params.RunnerID = registration.RunnerID
	second, err := registry.Register(params, newFakeLink())
	require.NoError(t, err)
	assert.Equal(t, int64(2), second.Generation)

	for _, runID := range []string{"run-one", "run-two"} {
		run, ok := registry.Run(runID)
		require.True(t, ok)
		assert.Equal(t, RunStatusLost, run.Status)
		assert.Contains(t, run.Error, "replaced")
	}
}

func TestConnectionLossCancelsTheActiveConversation(t *testing.T) {
	registry := newTestRegistry(t)
	canceled := make(chan string, 1)
	registry.SetEnvironmentErrorHandler(func(conversationID string) {
		canceled <- conversationID
	})
	link := newFakeLink()
	registration, err := registry.Register(testRegisterParams("host-one", "/work/project"), link)
	require.NoError(t, err)
	configureManifestLink(t, link, registration)
	markRunnerReady(t, registry, registration)
	_, err = registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-one", "conversation-one"))
	require.NoError(t, err)

	link.mu.Lock()
	link.err = errors.New("connection reset")
	link.mu.Unlock()
	require.NoError(t, link.Close())

	select {
	case conversationID := <-canceled:
		assert.Equal(t, "conversation-one", conversationID)
	case <-time.After(time.Second):
		t.Fatal("connection loss did not cancel the active conversation")
	}
	require.Eventually(t, func() bool {
		run, ok := registry.Run("run-one")
		return ok && run.Status == RunStatusLost
	}, time.Second, time.Millisecond)
}

func TestRunLeaseWatchdogReleasesCapacityAfterOwnerContextEnds(t *testing.T) {
	registry, err := New(t.Context(), Options{
		HeartbeatInterval: time.Hour,
		HeartbeatTimeout:  2 * time.Hour,
		RunLeaseGrace:     5 * time.Millisecond,
		NewID:             sequentialIDs(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = registry.Close() })
	link := newFakeLink()
	registration, err := registry.Register(testRegisterParams("host-one", "/work/project"), link)
	require.NoError(t, err)
	configureManifestLink(t, link, registration)
	markRunnerReady(t, registry, registration)
	statusAtCancellation := make(chan RunStatus, 1)
	registry.SetEnvironmentErrorHandler(func(string) {
		run, _ := registry.Run("run-one")
		statusAtCancellation <- run.Status
	})

	runCtx, cancel := context.WithCancel(t.Context())
	_, err = registry.OpenRun(runCtx, registration.RunnerID, testRunOpenParams("run-one", "conversation-one"))
	require.NoError(t, err)
	cancel()
	select {
	case status := <-statusAtCancellation:
		assert.Equal(t, RunStatusCanceled, status)
	case <-time.After(time.Second):
		t.Fatal("run lease expiry did not cancel the owning conversation")
	}

	require.Eventually(t, func() bool {
		run, runOK := registry.Run("run-one")
		runner, runnerOK := registry.Runner(registration.RunnerID)
		return runOK && runnerOK && run.Status == RunStatusCanceled && runner.Status == RunnerStatusIdle && runner.ActiveRunID == ""
	}, time.Second, time.Millisecond)
}

func TestRunLeaseCleanupFailureDoesNotDetachOtherRuns(t *testing.T) {
	registry, err := New(t.Context(), Options{
		HeartbeatInterval: time.Hour,
		HeartbeatTimeout:  2 * time.Hour,
		RunLeaseGrace:     5 * time.Millisecond,
		NewID:             sequentialIDs(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = registry.Close() })
	link := newFakeLink()
	registration, err := registry.Register(testRegisterParams("host-one", "/work/project"), link)
	require.NoError(t, err)
	configureManifestLink(t, link, registration)
	markRunnerReady(t, registry, registration)

	expiringCtx, cancelExpiring := context.WithCancel(t.Context())
	_, err = registry.OpenRun(expiringCtx, registration.RunnerID, testRunOpenParams("run-expiring", "conversation-expiring"))
	require.NoError(t, err)
	_, err = registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-healthy", "conversation-healthy"))
	require.NoError(t, err)

	link.mu.Lock()
	previousCall := link.call
	link.call = func(ctx context.Context, method string, params any, result any) error {
		if method == protocol.MethodRunClose && params.(protocol.RunCloseParams).RunID == "run-expiring" {
			return errors.New("run cleanup timed out")
		}
		return previousCall(ctx, method, params, result)
	}
	link.mu.Unlock()
	cancelExpiring()

	require.Eventually(t, func() bool {
		expiring, expiringOK := registry.Run("run-expiring")
		healthy, healthyOK := registry.Run("run-healthy")
		runner, runnerOK := registry.Runner(registration.RunnerID)
		return expiringOK && healthyOK && runnerOK &&
			expiring.Status == RunStatusLost &&
			healthy.Status == RunStatusRunning &&
			runner.Connected && runner.Status == RunnerStatusBusy &&
			len(runner.ActiveRunIDs) == 1 && runner.ActiveRunIDs[0] == "run-healthy"
	}, time.Second, time.Millisecond)
	assert.False(t, link.isClosed())
	_, err = registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-replacement", "conversation-expiring"))
	require.NoError(t, err)
	require.NoError(t, registry.CloseRun(t.Context(), "run-replacement", RunStatusSucceeded, nil))
	require.NoError(t, registry.CloseRun(t.Context(), "run-healthy", RunStatusSucceeded, nil))
}

func TestCloseRunCleanupFailureDoesNotPoisonOtherRuns(t *testing.T) {
	registry := newTestRegistry(t)
	link := newFakeLink()
	registration, err := registry.Register(testRegisterParams("host-one", "/work/project"), link)
	require.NoError(t, err)
	configureManifestLink(t, link, registration)
	markRunnerReady(t, registry, registration)
	_, err = registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-failing", "conversation-failing"))
	require.NoError(t, err)
	_, err = registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-healthy", "conversation-healthy"))
	require.NoError(t, err)

	link.mu.Lock()
	previousCall := link.call
	link.call = func(ctx context.Context, method string, params any, result any) error {
		if method == protocol.MethodRunClose && params.(protocol.RunCloseParams).RunID == "run-failing" {
			return errors.New("run cleanup failed")
		}
		return previousCall(ctx, method, params, result)
	}
	link.mu.Unlock()

	require.ErrorContains(t, registry.CloseRun(t.Context(), "run-failing", RunStatusFailed, errors.New("agent failed")), "run cleanup failed")
	failing, ok := registry.Run("run-failing")
	require.True(t, ok)
	assert.Equal(t, RunStatusLost, failing.Status)
	assert.Contains(t, failing.Error, "agent failed")
	assert.Contains(t, failing.Error, "runner cleanup was not confirmed: run cleanup failed")
	healthy, ok := registry.Run("run-healthy")
	require.True(t, ok)
	assert.Equal(t, RunStatusRunning, healthy.Status)
	runner, ok := registry.Runner(registration.RunnerID)
	require.True(t, ok)
	assert.True(t, runner.Connected)
	assert.Equal(t, RunnerStatusBusy, runner.Status)
	assert.Equal(t, []string{"run-healthy"}, runner.ActiveRunIDs)
	assert.False(t, link.isClosed())
	_, err = registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-replacement", "conversation-failing"))
	require.NoError(t, err)
	require.NoError(t, registry.CloseRun(t.Context(), "run-replacement", RunStatusSucceeded, nil))
	require.NoError(t, registry.CloseRun(t.Context(), "run-healthy", RunStatusSucceeded, nil))
}

func TestStaleOpenFailureCannotPoisonReplacementGeneration(t *testing.T) {
	registry := newTestRegistry(t)
	firstLink := newFakeLink()
	registration, err := registry.Register(testRegisterParams("host-one", "/work/project"), firstLink)
	require.NoError(t, err)
	markRunnerReady(t, registry, registration)
	openStarted := make(chan struct{})
	releaseOpen := make(chan struct{})
	firstLink.mu.Lock()
	firstLink.call = func(_ context.Context, method string, _ any, _ any) error {
		if method == protocol.MethodRunOpen {
			close(openStarted)
			<-releaseOpen
		}
		return protocol.ErrPeerClosed
	}
	firstLink.mu.Unlock()

	openDone := make(chan error, 1)
	go func() {
		_, openErr := registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-one", "conversation-one"))
		openDone <- openErr
	}()
	select {
	case <-openStarted:
	case <-time.After(time.Second):
		t.Fatal("run.open did not start")
	}

	params := testRegisterParams("host-one", "/work/project")
	params.RunnerID = registration.RunnerID
	secondLink := newFakeLink()
	second, err := registry.Register(params, secondLink)
	require.NoError(t, err)
	markRunnerReady(t, registry, second)
	close(releaseOpen)
	require.ErrorContains(t, <-openDone, "open state is uncertain")

	runner, ok := registry.Runner(registration.RunnerID)
	require.True(t, ok)
	assert.True(t, runner.Connected)
	assert.Equal(t, second.Generation, runner.Generation)
	assert.Equal(t, RunnerStatusIdle, runner.Status)
	run, ok := registry.Run("run-one")
	require.True(t, ok)
	assert.Equal(t, RunStatusLost, run.Status)
	assert.Contains(t, run.Error, "replaced")
}

func TestExecuteToolRoutesMonotonicUpdates(t *testing.T) {
	registry := newTestRegistry(t)
	link := newFakeLink()
	registration, err := registry.Register(testRegisterParams("host-one", "/work/project"), link)
	require.NoError(t, err)
	configureManifestLink(t, link, registration)
	markRunnerReady(t, registry, registration)
	_, err = registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-one", "conversation-one"))
	require.NoError(t, err)

	link.mu.Lock()
	link.call = func(_ context.Context, method string, params any, result any) error {
		require.Equal(t, protocol.MethodToolExecute, method)
		request := params.(runnerpayload.ToolExecuteParams)
		assert.True(t, request.WantUpdates)
		wrongRequest := runnerpayload.ToolUpdateParams{RunID: request.RunID, RequestID: "fake:old-request", ToolCallID: request.ToolCallID, Sequence: 10}
		assert.ErrorContains(t, registry.DeliverToolUpdate(registration.RunnerID, registration.ConnectionID, registration.Generation, wrongRequest), "request id is stale")
		update := runnerpayload.ToolUpdateParams{RunID: request.RunID, RequestID: "fake:request", ToolCallID: request.ToolCallID, Sequence: 1}
		require.NoError(t, registry.DeliverToolUpdate(registration.RunnerID, registration.ConnectionID, registration.Generation, update))
		assert.ErrorContains(t, registry.DeliverToolUpdate(registration.RunnerID, registration.ConnectionID, registration.Generation, update), "sequence is stale")
		*result.(*runnerpayload.ToolExecuteResult) = runnerpayload.ToolExecuteResult{Input: request.Input}
		return nil
	}
	link.mu.Unlock()

	var updates []uint64
	_, err = registry.ExecuteTool(t.Context(), runnerpayload.ToolExecuteParams{
		RunID:      "run-one",
		ToolCallID: "call-one",
		Name:       "bash",
		Input:      json.RawMessage(`{"command":"true"}`),
	}, func(update runnerpayload.ToolUpdateParams) {
		updates = append(updates, update.Sequence)
	})
	require.NoError(t, err)
	assert.Equal(t, []uint64{1}, updates)

	link.mu.Lock()
	link.call = func(context.Context, string, any, any) error {
		return protocol.ErrPeerClosed
	}
	link.mu.Unlock()
	_, err = registry.ExecuteTool(t.Context(), runnerpayload.ToolExecuteParams{
		RunID:      "run-one",
		ToolCallID: "call-two",
		Name:       "bash",
		Input:      json.RawMessage(`{"command":"touch result"}`),
	}, nil)
	require.ErrorContains(t, err, "side effects are uncertain")

	link.mu.Lock()
	link.call = func(context.Context, string, any, any) error {
		return &protocol.RPCError{Code: protocol.ErrorCodeInvalidParams, Message: "tool rejected"}
	}
	link.mu.Unlock()
	_, err = registry.ExecuteTool(t.Context(), runnerpayload.ToolExecuteParams{
		RunID:      "run-one",
		ToolCallID: "call-three",
		Name:       "bash",
		Input:      json.RawMessage(`{"command":"false"}`),
	}, nil)
	require.ErrorContains(t, err, "tool rejected")
	assert.NotContains(t, err.Error(), "side effects are uncertain")
}

func TestHeartbeatTimeoutMarksRunnerOffline(t *testing.T) {
	registry, err := New(t.Context(), Options{
		HeartbeatInterval: 5 * time.Millisecond,
		HeartbeatTimeout:  15 * time.Millisecond,
		NewID:             sequentialIDs(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = registry.Close() })
	link := newFakeLink()
	registration, err := registry.Register(testRegisterParams("host-one", "/work/project"), link)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		runner, ok := registry.Runner(registration.RunnerID)
		return ok && !runner.Connected && runner.Status == RunnerStatusOffline
	}, time.Second, 5*time.Millisecond)
	assert.True(t, link.isClosed())
}

func TestManifestChangedIsExposedUntilTheNextRunPinsIt(t *testing.T) {
	registry := newTestRegistry(t)
	link := newFakeLink()
	registration, err := registry.Register(testRegisterParams("host-one", "/work/project"), link)
	require.NoError(t, err)
	configureManifestLink(t, link, registration)
	markRunnerReady(t, registry, registration)

	require.NoError(t, registry.ManifestChanged(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.ManifestChangedParams{
		RunnerID:       registration.RunnerID,
		Generation:     registration.Generation,
		ManifestDigest: "sha256:changed",
	}))
	runner, ok := registry.Runner(registration.RunnerID)
	require.True(t, ok)
	assert.True(t, runner.ManifestChanged)
	assert.Equal(t, "sha256:changed", runner.ManifestDigest)

	_, err = registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-one", "conversation-one"))
	require.NoError(t, err)
	runner, ok = registry.Runner(registration.RunnerID)
	require.True(t, ok)
	assert.False(t, runner.ManifestChanged)
}

func TestSessionRequiresRegistrationBeforeOtherRequests(t *testing.T) {
	registry := newTestRegistry(t)
	session := NewSession(registry, nil)
	link := newFakeLink()
	session.Attach(link)

	_, rpcErr := session.HandleRequest(t.Context(), protocol.MethodUIInput, json.RawMessage(`{}`))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeInvalidRequest, rpcErr.Code)

	params := testRegisterParams("host-one", "/work/project")
	payload, err := json.Marshal(params)
	require.NoError(t, err)
	result, rpcErr := session.HandleRequest(t.Context(), protocol.MethodRunnerRegister, payload)
	require.Nil(t, rpcErr)
	registration := result.(protocol.RegisterResult)
	assert.NotEmpty(t, registration.RunnerID)

	heartbeatPayload, err := json.Marshal(protocol.HeartbeatParams{
		RunnerID:   registration.RunnerID,
		Generation: registration.Generation,
		State:      protocol.RunnerStateIdle,
	})
	require.NoError(t, err)
	session.HandleNotification(t.Context(), protocol.MethodRunnerHeartbeat, heartbeatPayload)
	runner, ok := registry.Runner(registration.RunnerID)
	require.True(t, ok)
	assert.Equal(t, RunnerStatusIdle, runner.Status)
}

func TestRegistryRestoresStableIdentityAffinityAndLostRuns(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "storage.db")
	database, err := db.Open(t.Context(), dbPath)
	require.NoError(t, err)
	require.NoError(t, db.NewMigrationRunner(database).Run(t.Context(), migrations.All()))
	require.NoError(t, database.Close())

	firstPersistence, err := NewSQLitePersistence(t.Context(), dbPath, "owner-one")
	require.NoError(t, err)
	firstRegistry, err := New(t.Context(), Options{
		HeartbeatInterval: time.Hour,
		HeartbeatTimeout:  2 * time.Hour,
		NewID:             sequentialIDs(),
		Persistence:       firstPersistence,
	})
	require.NoError(t, err)

	firstLink := newFakeLink()
	registration, err := firstRegistry.Register(testRegisterParams("host-one", "/work/project"), firstLink)
	require.NoError(t, err)
	configureManifestLink(t, firstLink, registration)
	markRunnerReady(t, firstRegistry, registration)
	_, err = firstRegistry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-one", "conversation-one"))
	require.NoError(t, err)
	require.NoError(t, firstRegistry.CommitConversationAffinity(t.Context(), "conversation-one"))

	secondPersistence, err := NewSQLitePersistence(t.Context(), dbPath, "owner-one")
	require.NoError(t, err)
	secondRegistry, err := New(t.Context(), Options{
		HeartbeatInterval: time.Hour,
		HeartbeatTimeout:  2 * time.Hour,
		NewID:             sequentialIDs(),
		Persistence:       secondPersistence,
	})
	require.NoError(t, err)

	restoredRunner, ok := secondRegistry.Runner(registration.RunnerID)
	require.True(t, ok)
	assert.False(t, restoredRunner.Connected)
	assert.Equal(t, RunnerStatusOffline, restoredRunner.Status)
	assert.Equal(t, int64(1), restoredRunner.Generation)

	restoredRun, ok := secondRegistry.Run("run-one")
	require.True(t, ok)
	assert.Equal(t, RunStatusLost, restoredRun.Status)
	assert.Contains(t, restoredRun.Error, "control plane restarted")
	var restoredManifest runnerpayload.Manifest
	require.NoError(t, json.Unmarshal([]byte(restoredRun.ManifestJSON), &restoredManifest))
	assert.Equal(t, "run-one", restoredManifest.RunID)
	assert.Equal(t, restoredRun.ManifestDigest, restoredManifest.Digest)

	affinity, ok := secondRegistry.RunnerForConversation("conversation-one")
	require.True(t, ok)
	assert.Equal(t, registration.RunnerID, affinity)

	reconnectedLink := newFakeLink()
	reconnected, err := secondRegistry.Register(testRegisterParams("host-one", "/work/project"), reconnectedLink)
	require.NoError(t, err)
	assert.Equal(t, registration.RunnerID, reconnected.RunnerID)
	assert.Equal(t, int64(2), reconnected.Generation)

	require.NoError(t, secondRegistry.Close())
	require.NoError(t, firstRegistry.Close())
}

func TestSQLitePersistenceBoundsRestoredHistoryAndPrunesTerminalOrphans(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "storage.db")
	database, err := db.Open(t.Context(), dbPath)
	require.NoError(t, err)
	require.NoError(t, db.NewMigrationRunner(database).Run(t.Context(), migrations.All()))
	require.NoError(t, database.Close())
	persistence, err := NewSQLitePersistence(t.Context(), dbPath, "owner-one")
	require.NoError(t, err)
	t.Cleanup(func() { _ = persistence.Close() })
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	require.NoError(t, persistence.SaveRunner(t.Context(), Runner{
		ID:        "runner-one",
		Host:      protocol.Host{InstanceID: "host-one"},
		Workspace: protocol.Workspace{Path: "/work/project", Name: "project"},
		Status:    RunnerStatusOffline,
		CreatedAt: now,
		UpdatedAt: now,
	}))
	_, err = persistence.db.ExecContext(t.Context(), `
		INSERT INTO conversations (id, raw_messages, provider, usage, created_at, updated_at, metadata, tool_results)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, "conversation-one", `[]`, "openai", `{}`, now, now, `{}`, `{}`)
	require.NoError(t, err)
	tx, err := persistence.db.BeginTxx(t.Context(), nil)
	require.NoError(t, err)
	for i := range defaultLoadedRunHistory + 5 {
		runID := fmt.Sprintf("run-%04d", i)
		require.NoError(t, persistence.saveRun(t.Context(), tx, Run{
			ID:             runID,
			ConversationID: "conversation-one",
			RunnerID:       "runner-one",
			Status:         RunStatusSucceeded,
			CreatedAt:      now.Add(time.Duration(i) * time.Second),
			UpdatedAt:      now.Add(time.Duration(i) * time.Second),
		}))
	}
	require.NoError(t, persistence.saveRun(t.Context(), tx, Run{
		ID:             "orphan-terminal",
		ConversationID: "deleted-conversation",
		RunnerID:       "runner-one",
		Status:         RunStatusFailed,
		CreatedAt:      now.Add(time.Hour),
		UpdatedAt:      now.Add(time.Hour),
	}))
	require.NoError(t, persistence.saveRun(t.Context(), tx, Run{
		ID:             "orphan-active",
		ConversationID: "not-yet-persisted-conversation",
		RunnerID:       "runner-one",
		Status:         RunStatusRunning,
		CreatedAt:      now.Add(2 * time.Hour),
		UpdatedAt:      now.Add(2 * time.Hour),
	}))
	require.NoError(t, tx.Commit())

	state, err := persistence.Load(t.Context())
	require.NoError(t, err)
	require.Len(t, state.Runs, defaultLoadedRunHistory+1)
	loaded := make(map[string]struct{}, len(state.Runs))
	for _, run := range state.Runs {
		loaded[run.ID] = struct{}{}
	}
	assert.NotContains(t, loaded, "run-0000")
	assert.Contains(t, loaded, fmt.Sprintf("run-%04d", defaultLoadedRunHistory+4))
	assert.NotContains(t, loaded, "orphan-terminal")
	assert.Contains(t, loaded, "orphan-active")
	var orphanCount int
	require.NoError(t, persistence.db.GetContext(t.Context(), &orphanCount, "SELECT COUNT(*) FROM runner_runs WHERE id = ?", "orphan-terminal"))
	assert.Zero(t, orphanCount)
}

func TestRemoveRunnerRequiresOfflineAndClearsConversationAffinity(t *testing.T) {
	registry := newTestRegistry(t)
	link := newFakeLink()
	registration, err := registry.Register(testRegisterParams("host-one", "/work/project"), link)
	require.NoError(t, err)
	configureManifestLink(t, link, registration)
	markRunnerReady(t, registry, registration)

	_, err = registry.RemoveRunner(t.Context(), registration.RunnerID, false)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrRunnerConnected))

	_, err = registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-one", "conversation-one"))
	require.NoError(t, err)
	require.NoError(t, registry.CloseRun(t.Context(), "run-one", RunStatusSucceeded, nil))
	registry.Detach(registration.RunnerID, registration.ConnectionID, registration.Generation, nil)

	result, err := registry.RemoveRunner(t.Context(), registration.RunnerID, false)
	require.NoError(t, err)
	assert.Equal(t, RemovalResult{
		RunnerID:                      registration.RunnerID,
		RemovedRuns:                   1,
		RemovedConversationAffinities: 1,
	}, result)
	_, ok := registry.Runner(registration.RunnerID)
	assert.False(t, ok)
	_, ok = registry.Run("run-one")
	assert.False(t, ok)
	_, ok = registry.RunnerForConversation("conversation-one")
	assert.False(t, ok)

	_, err = registry.RemoveRunner(t.Context(), registration.RunnerID, true)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrRunnerNotFound))
}

func TestRemoveRunnerDeletesDurableState(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "storage.db")
	database, err := db.Open(t.Context(), dbPath)
	require.NoError(t, err)
	require.NoError(t, db.NewMigrationRunner(database).Run(t.Context(), migrations.All()))
	require.NoError(t, database.Close())
	conversationStore, err := conversationsqlite.NewStore(t.Context(), dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conversationStore.Close()) })
	conversation := conversationtypes.ConversationRecord{
		ID:          "conversation-one",
		CWD:         "/work/project",
		RawMessages: json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"preserve this transcript"}]}]`),
		Provider:    "openai",
		Summary:     "Preserved conversation",
		Metadata: map[string]any{
			conversationtypes.RunnerIDMetadataKey:                 "runner-stale",
			conversationtypes.RunnerEnvironmentProfileMetadataKey: "gpu",
			"preserve": "metadata",
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	require.NoError(t, conversationStore.Save(t.Context(), conversation))

	firstPersistence, err := NewSQLitePersistence(t.Context(), dbPath, "owner-one")
	require.NoError(t, err)
	firstRegistry, err := New(t.Context(), Options{
		HeartbeatInterval: time.Hour,
		HeartbeatTimeout:  2 * time.Hour,
		NewID:             sequentialIDs(),
		Persistence:       firstPersistence,
	})
	require.NoError(t, err)

	link := newFakeLink()
	registration, err := firstRegistry.Register(testRegisterParams("host-one", "/work/project"), link)
	require.NoError(t, err)
	configureManifestLink(t, link, registration)
	markRunnerReady(t, firstRegistry, registration)
	_, err = firstRegistry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-one", "conversation-one"))
	require.NoError(t, err)
	require.NoError(t, firstRegistry.CommitConversationAffinity(t.Context(), "conversation-one"))
	require.NoError(t, firstRegistry.CloseRun(t.Context(), "run-one", RunStatusSucceeded, nil))
	firstRegistry.Detach(registration.RunnerID, registration.ConnectionID, registration.Generation, nil)

	result, err := firstRegistry.RemoveRunner(t.Context(), registration.RunnerID, false)
	require.NoError(t, err)
	assert.Equal(t, 1, result.RemovedRuns)
	assert.Equal(t, 1, result.RemovedConversationAffinities)
	require.NoError(t, firstRegistry.Close())

	secondPersistence, err := NewSQLitePersistence(t.Context(), dbPath, "owner-one")
	require.NoError(t, err)
	secondRegistry, err := New(t.Context(), Options{
		HeartbeatInterval: time.Hour,
		HeartbeatTimeout:  2 * time.Hour,
		NewID:             sequentialIDs(),
		Persistence:       secondPersistence,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = secondRegistry.Close() })
	assert.Empty(t, secondRegistry.Runners())
	_, ok := secondRegistry.Run("run-one")
	assert.False(t, ok)
	_, ok = secondRegistry.RunnerForConversation("conversation-one")
	assert.False(t, ok)
	loadedConversation, err := conversationStore.Load(t.Context(), "conversation-one")
	require.NoError(t, err)
	assert.Equal(t, string(conversation.RawMessages), string(loadedConversation.RawMessages))
	assert.Equal(t, conversation.Summary, loadedConversation.Summary)
	assert.NotContains(t, loadedConversation.Metadata, conversationtypes.RunnerIDMetadataKey)
	assert.NotContains(t, loadedConversation.Metadata, conversationtypes.RunnerEnvironmentProfileMetadataKey)
	assert.Equal(t, "metadata", loadedConversation.Metadata["preserve"])
	queryResult, err := conversationStore.Query(t.Context(), conversationtypes.QueryOptions{})
	require.NoError(t, err)
	require.Len(t, queryResult.ConversationSummaries, 1)
	assert.Equal(t, conversation.ID, queryResult.ConversationSummaries[0].ID)
	assert.Equal(t, conversation.Summary, queryResult.ConversationSummaries[0].Summary)
	assert.NotContains(t, queryResult.ConversationSummaries[0].Metadata, conversationtypes.RunnerIDMetadataKey)
	assert.NotContains(t, queryResult.ConversationSummaries[0].Metadata, conversationtypes.RunnerEnvironmentProfileMetadataKey)
	assert.Equal(t, "metadata", queryResult.ConversationSummaries[0].Metadata["preserve"])
}

func TestRemoveRunnerClearsPersistedMetadataWithoutDurableAffinity(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "storage.db")
	database, err := db.Open(t.Context(), dbPath)
	require.NoError(t, err)
	require.NoError(t, db.NewMigrationRunner(database).Run(t.Context(), migrations.All()))
	require.NoError(t, database.Close())
	conversationStore, err := conversationsqlite.NewStore(t.Context(), dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conversationStore.Close()) })
	persistence, err := NewSQLitePersistence(t.Context(), dbPath, "owner-one")
	require.NoError(t, err)
	registry, err := New(t.Context(), Options{
		HeartbeatInterval: time.Hour,
		HeartbeatTimeout:  2 * time.Hour,
		NewID:             sequentialIDs(),
		Persistence:       persistence,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = registry.Close() })

	registration, err := registry.Register(testRegisterParams("host-one", "/work/project"), newFakeLink())
	require.NoError(t, err)
	registry.Detach(registration.RunnerID, registration.ConnectionID, registration.Generation, nil)
	conversation := conversationtypes.ConversationRecord{
		ID:          "conversation-before-affinity-commit",
		CWD:         "/work/project",
		RawMessages: json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"preserve this transcript"}]}]`),
		Provider:    "openai",
		Summary:     "Preserved before affinity commit",
		Metadata: map[string]any{
			conversationtypes.RunnerIDMetadataKey:                 registration.RunnerID,
			conversationtypes.RunnerEnvironmentProfileMetadataKey: "gpu",
			"preserve": "metadata",
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	require.NoError(t, conversationStore.Save(t.Context(), conversation))

	result, err := registry.RemoveRunner(t.Context(), registration.RunnerID, false)

	require.NoError(t, err)
	assert.Zero(t, result.RemovedConversationAffinities)
	loaded, err := conversationStore.Load(t.Context(), conversation.ID)
	require.NoError(t, err)
	assert.Equal(t, string(conversation.RawMessages), string(loaded.RawMessages))
	assert.NotContains(t, loaded.Metadata, conversationtypes.RunnerIDMetadataKey)
	assert.NotContains(t, loaded.Metadata, conversationtypes.RunnerEnvironmentProfileMetadataKey)
	assert.Equal(t, "metadata", loaded.Metadata["preserve"])
	queryResult, err := conversationStore.Query(t.Context(), conversationtypes.QueryOptions{})
	require.NoError(t, err)
	require.Len(t, queryResult.ConversationSummaries, 1)
	assert.NotContains(t, queryResult.ConversationSummaries[0].Metadata, conversationtypes.RunnerIDMetadataKey)
	assert.NotContains(t, queryResult.ConversationSummaries[0].Metadata, conversationtypes.RunnerEnvironmentProfileMetadataKey)
}

func TestRemoveRunnerClearsAffinityWithoutConversationRecord(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "storage.db")
	database, err := db.Open(t.Context(), dbPath)
	require.NoError(t, err)
	require.NoError(t, db.NewMigrationRunner(database).Run(t.Context(), migrations.All()))
	require.NoError(t, database.Close())

	persistence, err := NewSQLitePersistence(t.Context(), dbPath, "owner-one")
	require.NoError(t, err)
	registry, err := New(t.Context(), Options{
		HeartbeatInterval: time.Hour,
		HeartbeatTimeout:  2 * time.Hour,
		NewID:             sequentialIDs(),
		Persistence:       persistence,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = registry.Close() })

	registration, err := registry.Register(testRegisterParams("host-one", "/work/project"), newFakeLink())
	require.NoError(t, err)
	require.NoError(t, registry.BindConversation(t.Context(), "conversation-one", registration.RunnerID))
	registry.Detach(registration.RunnerID, registration.ConnectionID, registration.Generation, nil)

	result, err := registry.RemoveRunner(t.Context(), registration.RunnerID, false)
	require.NoError(t, err)
	assert.Equal(t, 1, result.RemovedConversationAffinities)
	_, ok := registry.RunnerForConversation("conversation-one")
	assert.False(t, ok)
}

func TestRegisterExposesIncompatibleRunner(t *testing.T) {
	registry := newTestRegistry(t)
	params := testRegisterParams("host-one", "/work/project")
	params.ProtocolVersions = []int{protocol.Version + 1}

	_, err := registry.Register(params, newFakeLink())
	require.ErrorContains(t, err, "does not support protocol version")

	runners := registry.Runners()
	require.Len(t, runners, 1)
	assert.Equal(t, RunnerStatusIncompatible, runners[0].Status)
	assert.False(t, runners[0].Connected)
	assert.Contains(t, runners[0].CompatibilityError, "does not support protocol version")
}

func TestCancelEnvironmentErrorAndConversationAffinity(t *testing.T) {
	registry := newTestRegistry(t)
	var failedConversation string
	registry.SetEnvironmentErrorHandler(func(conversationID string) {
		failedConversation = conversationID
	})
	firstLink := newFakeLink()
	first, err := registry.Register(testRegisterParams("host-one", "/work/one"), firstLink)
	require.NoError(t, err)
	configureManifestLink(t, firstLink, first)
	markRunnerReady(t, registry, first)
	second, err := registry.Register(testRegisterParams("host-two", "/work/two"), newFakeLink())
	require.NoError(t, err)

	require.ErrorContains(t, registry.BindConversation(t.Context(), "", first.RunnerID), "conversation id")
	require.ErrorContains(t, registry.BindConversation(t.Context(), "conversation-one", ""), "runner id")
	require.ErrorContains(t, registry.BindConversation(t.Context(), "conversation-one", "missing"), "runner not found")
	require.NoError(t, registry.BindConversation(t.Context(), "conversation-one", first.RunnerID))
	require.NoError(t, registry.BindConversation(t.Context(), "conversation-one", first.RunnerID))
	require.ErrorContains(t, registry.BindConversation(t.Context(), "conversation-one", second.RunnerID), "bound to runner")
	affinity, ok := registry.RunnerForConversation(" conversation-one ")
	require.True(t, ok)
	assert.Equal(t, first.RunnerID, affinity)

	_, err = registry.OpenRun(t.Context(), first.RunnerID, testRunOpenParams("run-one", "conversation-one"))
	require.NoError(t, err)
	require.NoError(t, registry.EnvironmentError(first.RunnerID, first.ConnectionID, first.Generation, protocol.EnvironmentErrorParams{
		RunID: "run-one", Message: "extension failed",
	}))
	run, ok := registry.Run("run-one")
	require.True(t, ok)
	assert.Equal(t, RunStatusFailed, run.Status)
	assert.Equal(t, "extension failed", run.Error)
	assert.Equal(t, "conversation-one", failedConversation)
	require.ErrorContains(t, registry.EnvironmentError(first.RunnerID, first.ConnectionID, first.Generation, protocol.EnvironmentErrorParams{
		RunID: "another-run", Message: "stale",
	}), "another run")
	require.NoError(t, registry.CloseRun(t.Context(), "run-one", RunStatusFailed, errors.New("extension failed")))
	run, ok = registry.Run("run-one")
	require.True(t, ok)
	assert.Equal(t, RunStatusFailed, run.Status)
	assert.Equal(t, "extension failed", run.Error)

	_, err = registry.OpenRun(t.Context(), first.RunnerID, testRunOpenParams("run-two", "conversation-one"))
	require.NoError(t, err)
	require.NoError(t, registry.CancelRun(t.Context(), "run-two", "user stopped"))
	run, ok = registry.Run("run-two")
	require.True(t, ok)
	assert.Equal(t, RunStatusCanceled, run.Status)
	require.NoError(t, registry.CloseRun(t.Context(), "run-two", RunStatusCanceled, context.Canceled))
	require.ErrorContains(t, registry.CancelRun(t.Context(), "missing", "no run"), "not found")
	require.ErrorContains(t, registry.CloseRun(t.Context(), "missing", RunStatus("invalid"), nil), "invalid terminal")
}

func TestOpenRunClosesLeaseWhenEnvironmentFailsBeforeRunningCommit(t *testing.T) {
	registry := newTestRegistry(t)
	link := newFakeLink()
	registration, err := registry.Register(testRegisterParams("host-one", "/work/project"), link)
	require.NoError(t, err)
	markRunnerReady(t, registry, registration)
	var methods []string
	link.mu.Lock()
	link.call = func(_ context.Context, method string, params any, result any) error {
		methods = append(methods, method)
		switch method {
		case protocol.MethodRunOpen:
			request := params.(protocol.RunOpenParams)
			manifest := runnerpayload.Manifest{
				ProtocolVersion:  protocol.Version,
				RunnerID:         registration.RunnerID,
				RunID:            request.RunID,
				Generation:       registration.Generation,
				WorkingDirectory: "/work/project",
			}
			digest, digestErr := runnerpayload.ComputeManifestDigest(manifest)
			require.NoError(t, digestErr)
			manifest.Digest = digest
			*result.(*runnerpayload.Manifest) = manifest
			require.NoError(t, registry.EnvironmentError(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.EnvironmentErrorParams{
				RunID: request.RunID, Message: "extension failed during open",
			}))
		case protocol.MethodRunClose:
			return nil
		default:
			return fmt.Errorf("unexpected method %s", method)
		}
		return nil
	}
	link.mu.Unlock()

	_, err = registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-one", "conversation-one"))
	require.ErrorContains(t, err, "run failed while opening")
	require.ErrorContains(t, err, "extension failed during open")
	assert.Equal(t, []string{protocol.MethodRunOpen, protocol.MethodRunClose}, methods)
	run, ok := registry.Run("run-one")
	require.True(t, ok)
	assert.Equal(t, RunStatusFailed, run.Status)
	assert.Equal(t, "extension failed during open", run.Error)
	runner, ok := registry.Runner(registration.RunnerID)
	require.True(t, ok)
	assert.Equal(t, RunnerStatusIdle, runner.Status)
	assert.Empty(t, runner.ActiveRunID)
}

func TestCloseRunTreatsMissingRemoteLeaseAsAlreadyClosed(t *testing.T) {
	registry := newTestRegistry(t)
	link := newFakeLink()
	registration, err := registry.Register(testRegisterParams("host-one", "/work/project"), link)
	require.NoError(t, err)
	configureManifestLink(t, link, registration)
	markRunnerReady(t, registry, registration)
	_, err = registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-one", "conversation-one"))
	require.NoError(t, err)
	link.mu.Lock()
	link.call = func(_ context.Context, method string, _ any, _ any) error {
		if method == protocol.MethodRunClose {
			return &protocol.RPCError{
				Code:    protocol.ErrorCodeStale,
				Message: "runner has no active run",
				Data:    protocol.RPCErrorData{Reason: protocol.ErrorReasonRunNotActive},
			}
		}
		return fmt.Errorf("unexpected method %s", method)
	}
	link.mu.Unlock()

	require.NoError(t, registry.CloseRun(t.Context(), "run-one", RunStatusSucceeded, nil))
	run, ok := registry.Run("run-one")
	require.True(t, ok)
	assert.Equal(t, RunStatusSucceeded, run.Status)
	runner, ok := registry.Runner(registration.RunnerID)
	require.True(t, ok)
	assert.Equal(t, RunnerStatusIdle, runner.Status)
	assert.Empty(t, runner.ActiveRunID)
}

func TestOpenRunFailureReleasesRunnerCapacity(t *testing.T) {
	registry := newTestRegistry(t)
	link := newFakeLink()
	registration, err := registry.Register(testRegisterParams("host-one", "/work/project"), link)
	require.NoError(t, err)
	markRunnerReady(t, registry, registration)
	link.mu.Lock()
	link.call = func(_ context.Context, method string, _ any, _ any) error {
		if method == protocol.MethodRunOpen {
			return errors.New("runner open failed")
		}
		return nil
	}
	link.mu.Unlock()

	_, err = registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-one", "conversation-one"))
	require.ErrorContains(t, err, "runner open failed")
	run, ok := registry.Run("run-one")
	require.True(t, ok)
	assert.Equal(t, RunStatusFailed, run.Status)
	runner, ok := registry.Runner(registration.RunnerID)
	require.True(t, ok)
	assert.Equal(t, RunnerStatusIdle, runner.Status)
	assert.Empty(t, runner.ActiveRunID)

	_, err = registry.OpenRun(t.Context(), "missing", testRunOpenParams("run-two", "conversation-two"))
	require.ErrorContains(t, err, "runner not found")

	offlineLink := newFakeLink()
	offline, err := registry.Register(testRegisterParams("host-two", "/work/offline"), offlineLink)
	require.NoError(t, err)
	registry.Detach(offline.RunnerID, offline.ConnectionID, offline.Generation, nil)
	_, err = registry.OpenRun(t.Context(), offline.RunnerID, testRunOpenParams("run-three", "conversation-three"))
	require.ErrorContains(t, err, "offline")

	notReady, err := registry.Register(testRegisterParams("host-three", "/work/not-ready"), newFakeLink())
	require.NoError(t, err)
	_, err = registry.OpenRun(t.Context(), notReady.RunnerID, testRunOpenParams("run-four", "conversation-four"))
	require.ErrorContains(t, err, "initial heartbeat")
}

func TestOpenRunReconcilesAmbiguousTransportFailure(t *testing.T) {
	registry := newTestRegistry(t)
	link := newFakeLink()
	registration, err := registry.Register(testRegisterParams("host-one", "/work/project"), link)
	require.NoError(t, err)
	markRunnerReady(t, registry, registration)
	var methods []string
	link.mu.Lock()
	link.call = func(_ context.Context, method string, _ any, _ any) error {
		methods = append(methods, method)
		if method == protocol.MethodRunOpen {
			return protocol.ErrPeerClosed
		}
		return nil
	}
	link.mu.Unlock()

	_, err = registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-one", "conversation-one"))
	require.ErrorIs(t, err, protocol.ErrPeerClosed)
	assert.Equal(t, []string{protocol.MethodRunOpen, protocol.MethodRunClose}, methods)
	run, ok := registry.Run("run-one")
	require.True(t, ok)
	assert.Equal(t, RunStatusFailed, run.Status)
	runner, ok := registry.Runner(registration.RunnerID)
	require.True(t, ok)
	assert.Equal(t, RunnerStatusIdle, runner.Status)
	assert.False(t, link.isClosed())
}

func TestOpenRunCleanupFailureDoesNotCloseSharedConnection(t *testing.T) {
	registry := newTestRegistry(t)
	link := newFakeLink()
	registration, err := registry.Register(testRegisterParams("host-one", "/work/project"), link)
	require.NoError(t, err)
	configureManifestLink(t, link, registration)
	markRunnerReady(t, registry, registration)
	_, err = registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-healthy", "conversation-healthy"))
	require.NoError(t, err)
	link.mu.Lock()
	previousCall := link.call
	link.call = func(ctx context.Context, method string, params any, result any) error {
		switch method {
		case protocol.MethodRunOpen:
			if params.(protocol.RunOpenParams).RunID == "run-failing" {
				return protocol.ErrPeerClosed
			}
		case protocol.MethodRunClose:
			if params.(protocol.RunCloseParams).RunID == "run-failing" {
				return protocol.ErrPeerClosed
			}
		}
		return previousCall(ctx, method, params, result)
	}
	link.mu.Unlock()

	_, err = registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-failing", "conversation-failing"))
	require.ErrorContains(t, err, "open state is uncertain")
	run, ok := registry.Run("run-failing")
	require.True(t, ok)
	assert.Equal(t, RunStatusLost, run.Status)
	assert.Contains(t, run.Error, "cleanup was not confirmed")
	runner, ok := registry.Runner(registration.RunnerID)
	require.True(t, ok)
	assert.True(t, runner.Connected)
	assert.Equal(t, RunnerStatusBusy, runner.Status)
	assert.Equal(t, []string{"run-healthy"}, runner.ActiveRunIDs)
	healthy, ok := registry.Run("run-healthy")
	require.True(t, ok)
	assert.Equal(t, RunStatusRunning, healthy.Status)
	assert.False(t, link.isClosed())
	_, err = registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-replacement", "conversation-failing"))
	require.NoError(t, err)
	require.NoError(t, registry.CloseRun(t.Context(), "run-replacement", RunStatusSucceeded, nil))
	require.NoError(t, registry.CloseRun(t.Context(), "run-healthy", RunStatusSucceeded, nil))
}

func TestOpenRunRPCRejectionDoesNotAttemptCleanup(t *testing.T) {
	registry := newTestRegistry(t)
	link := newFakeLink()
	registration, err := registry.Register(testRegisterParams("host-one", "/work/project"), link)
	require.NoError(t, err)
	markRunnerReady(t, registry, registration)
	var methods []string
	link.mu.Lock()
	link.call = func(_ context.Context, method string, _ any, _ any) error {
		methods = append(methods, method)
		return &protocol.RPCError{Code: protocol.ErrorCodeInvalidParams, Message: "open rejected"}
	}
	link.mu.Unlock()

	_, err = registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-one", "conversation-one"))
	require.ErrorContains(t, err, "open rejected")
	assert.Equal(t, []string{protocol.MethodRunOpen}, methods)
	run, ok := registry.Run("run-one")
	require.True(t, ok)
	assert.Equal(t, RunStatusFailed, run.Status)
	assert.False(t, link.isClosed())
}

func TestPendingAffinityCanBeReleasedWhenFirstRunCreatesNoConversation(t *testing.T) {
	registry := newTestRegistry(t)
	link := newFakeLink()
	registration, err := registry.Register(testRegisterParams("host-one", "/work/project"), link)
	require.NoError(t, err)
	markRunnerReady(t, registry, registration)
	link.mu.Lock()
	link.call = func(context.Context, string, any, any) error {
		return &protocol.RPCError{Code: protocol.ErrorCodeInvalidParams, Message: "open rejected"}
	}
	link.mu.Unlock()

	_, err = registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-one", "conversation-one"))
	require.Error(t, err)
	_, ok := registry.RunnerForConversation("conversation-one")
	require.True(t, ok)
	assert.True(t, registry.ReleasePendingConversationAffinity("conversation-one"))
	_, ok = registry.RunnerForConversation("conversation-one")
	assert.False(t, ok)
	assert.False(t, registry.ReleasePendingConversationAffinity("conversation-one"))
}

func TestOpenRunDoesNotReserveAffinityBeforeRunnerAvailability(t *testing.T) {
	registry := newTestRegistry(t)
	link := newFakeLink()
	registration, err := registry.Register(testRegisterParams("host-one", "/work/project"), link)
	require.NoError(t, err)
	registry.Detach(registration.RunnerID, registration.ConnectionID, registration.Generation, nil)

	params := testRunOpenParams("run-one", "conversation-one")
	params.Agent.EnvironmentProfile = "runner-work"
	_, err = registry.OpenRun(t.Context(), registration.RunnerID, params)
	require.ErrorContains(t, err, "offline")

	params.RunID = "run-two"
	params.Agent.EnvironmentProfile = "runner-ci"
	_, err = registry.OpenRun(t.Context(), registration.RunnerID, params)
	require.ErrorContains(t, err, "offline")
	_, ok := registry.RunnerForConversation("conversation-one")
	assert.False(t, ok)
}

func TestResolveConversationAffinityObservesExternalDeletion(t *testing.T) {
	persistence := &testPersistence{}
	registry, err := New(t.Context(), Options{
		HeartbeatInterval: time.Hour,
		HeartbeatTimeout:  2 * time.Hour,
		NewID:             sequentialIDs(),
		Persistence:       persistence,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = registry.Close() })
	registration, err := registry.Register(testRegisterParams("host-one", "/work/project"), newFakeLink())
	require.NoError(t, err)
	require.NoError(t, registry.BindConversationWithEnvironmentProfile(t.Context(), "conversation-one", registration.RunnerID, "runner-work"))

	affinity, ok, err := registry.ResolveConversationAffinity(t.Context(), "conversation-one")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, ConversationAffinity{RunnerID: registration.RunnerID, EnvironmentProfile: "runner-work"}, affinity)

	delete(persistence.state.Affinities, "conversation-one")
	_, ok, err = registry.ResolveConversationAffinity(t.Context(), "conversation-one")
	require.NoError(t, err)
	assert.False(t, ok)
	_, ok = registry.RunnerForConversation("conversation-one")
	assert.False(t, ok)
}

func TestRegistryCallRunRoutesOnlyToActiveRunAndForgetsAffinity(t *testing.T) {
	registry := newTestRegistry(t)
	link := newFakeLink()
	registration, err := registry.Register(testRegisterParams("host-one", "/work/project"), link)
	require.NoError(t, err)
	configureManifestLink(t, link, registration)
	markRunnerReady(t, registry, registration)
	_, err = registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-one", "conversation-one"))
	require.NoError(t, err)

	link.mu.Lock()
	previousCall := link.call
	link.call = func(ctx context.Context, method string, params any, result any) error {
		if method == "runner.custom" {
			assert.Equal(t, map[string]string{"key": "value"}, params)
			(*result.(*map[string]bool))["ok"] = true
			return nil
		}
		return previousCall(ctx, method, params, result)
	}
	link.mu.Unlock()
	result := map[string]bool{}
	require.NoError(t, registry.CallRun(t.Context(), "run-one", "runner.custom", map[string]string{"key": "value"}, &result))
	assert.True(t, result["ok"])

	require.NoError(t, registry.CloseRun(t.Context(), "run-one", RunStatusSucceeded, nil))
	require.ErrorContains(t, registry.CallRun(t.Context(), "run-one", "runner.custom", nil, nil), "not active")

	require.NoError(t, registry.BindConversation(t.Context(), "conversation-forget", registration.RunnerID))
	_, found := registry.RunnerForConversation("conversation-forget")
	require.True(t, found)
	registry.ForgetConversation("conversation-forget")
	_, found = registry.RunnerForConversation("conversation-forget")
	assert.False(t, found)
	registry.ForgetConversation(" ")
	(*Registry)(nil).ForgetConversation("conversation")
}

func TestConversationAffinityProfileLock(t *testing.T) {
	registry := newTestRegistry(t)
	registration, err := registry.Register(testRegisterParams("host-one", "/work/project"), newFakeLink())
	require.NoError(t, err)
	require.NoError(t, registry.BindConversationWithEnvironmentProfile(t.Context(), "conversation-one", registration.RunnerID, "default"))
	err = registry.BindConversationWithEnvironmentProfile(t.Context(), "conversation-one", registration.RunnerID, "gpu")
	require.ErrorContains(t, err, `locked to "default"`)
	require.ErrorContains(t, err, `resume with "gpu"`)
}

func TestSQLitePersistenceReadsConversationAffinity(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "storage.db")
	database, err := db.Open(t.Context(), dbPath)
	require.NoError(t, err)
	require.NoError(t, db.NewMigrationRunner(database).Run(t.Context(), migrations.All()))
	require.NoError(t, database.Close())

	persistence, err := NewSQLitePersistence(t.Context(), dbPath, "owner-one")
	require.NoError(t, err)
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	require.NoError(t, persistence.SaveRunner(t.Context(), Runner{
		ID:        "runner-one",
		Host:      protocol.Host{InstanceID: "host-one"},
		Workspace: protocol.Workspace{Path: "/work/project", Name: "project"},
		Status:    RunnerStatusOffline,
		CreatedAt: now,
		UpdatedAt: now,
	}))
	require.NoError(t, persistence.BindConversation(t.Context(), "conversation-one", "runner-one", "gpu", now))

	affinity, found, err := persistence.ConversationAffinity(t.Context(), " conversation-one ")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, ConversationAffinity{RunnerID: "runner-one", EnvironmentProfile: "gpu"}, affinity)
	_, found, err = persistence.ConversationAffinity(t.Context(), "missing")
	require.NoError(t, err)
	assert.False(t, found)

	require.NoError(t, persistence.Close())
	_, _, err = persistence.ConversationAffinity(t.Context(), "conversation-one")
	require.ErrorContains(t, err, "runner persistence is closed")
	_, _, err = (*SQLitePersistence)(nil).ConversationAffinity(t.Context(), "conversation-one")
	require.ErrorContains(t, err, "runner persistence is closed")
}

func TestValidateManifestRejectsInvalidRunnerContracts(t *testing.T) {
	params := testRunOpenParams("run-one", "conversation-one")
	base := runnerpayload.Manifest{
		ProtocolVersion:  protocol.Version,
		RunnerID:         "runner-one",
		RunID:            params.RunID,
		Generation:       2,
		WorkingDirectory: "/work/project",
		Tools: []runnerpayload.ToolDefinition{{
			Name: "bash", Placement: "environment", InputSchema: map[string]any{"type": "object"},
		}},
	}
	withDigest := func(manifest runnerpayload.Manifest) runnerpayload.Manifest {
		digest, err := runnerpayload.ComputeManifestDigest(manifest)
		require.NoError(t, err)
		manifest.Digest = digest
		return manifest
	}
	require.NoError(t, validateManifest(withDigest(base), "runner-one", params, 2))

	tests := []struct {
		name      string
		manifest  runnerpayload.Manifest
		wantError string
	}{
		{name: "protocol", manifest: func() runnerpayload.Manifest { value := base; value.ProtocolVersion++; return withDigest(value) }(), wantError: "protocol version"},
		{name: "identity", manifest: func() runnerpayload.Manifest { value := base; value.RunnerID = "other"; return withDigest(value) }(), wantError: "identity"},
		{name: "unnamed tool", manifest: func() runnerpayload.Manifest {
			value := base
			value.Tools = []runnerpayload.ToolDefinition{{Placement: "environment"}}
			return withDigest(value)
		}(), wantError: "without a name"},
		{name: "reserved collision", manifest: func() runnerpayload.Manifest {
			value := base
			value.Tools = []runnerpayload.ToolDefinition{{Name: "get_goal", Placement: "environment"}}
			return withDigest(value)
		}(), wantError: "reserved"},
		{name: "placement", manifest: func() runnerpayload.Manifest {
			value := base
			value.Tools = []runnerpayload.ToolDefinition{{Name: "bash", Placement: "control_plane"}}
			return withDigest(value)
		}(), wantError: "invalid placement"},
		{name: "duplicate", manifest: func() runnerpayload.Manifest {
			value := base
			value.Tools = append(value.Tools, value.Tools[0])
			return withDigest(value)
		}(), wantError: "duplicate tool"},
		{name: "system platform", manifest: func() runnerpayload.Manifest {
			value := base
			value.Config.SystemInformation = &llmtypes.SystemInformation{OSVersion: "macOS 26.0", Date: "2026-08-09"}
			return withDigest(value)
		}(), wantError: "missing its platform"},
		{name: "system OS version", manifest: func() runnerpayload.Manifest {
			value := base
			value.Config.SystemInformation = &llmtypes.SystemInformation{Platform: "darwin", Date: "2026-08-09"}
			return withDigest(value)
		}(), wantError: "missing its OS version"},
		{name: "system date", manifest: func() runnerpayload.Manifest {
			value := base
			value.Config.SystemInformation = &llmtypes.SystemInformation{Platform: "darwin", OSVersion: "macOS 26.0", Date: "not-a-date"}
			return withDigest(value)
		}(), wantError: "invalid date"},
		{name: "missing digest", manifest: base, wantError: "digest is required"},
		{name: "wrong digest", manifest: func() runnerpayload.Manifest { value := base; value.Digest = "sha256:wrong"; return value }(), wantError: "does not match"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.ErrorContains(t, validateManifest(test.manifest, "runner-one", params, 2), test.wantError)
		})
	}

	id, err := randomID("runner")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(id, "runner_"))
	assert.Empty(t, errorString(nil))
	assert.Equal(t, "failure", errorString(errors.New("failure")))
}

func TestSessionRoutesUIAndRunnerNotifications(t *testing.T) {
	registry := newTestRegistry(t)
	link := newFakeLink()
	router := &fakeUIRequestRouter{}
	session := NewSession(registry, router)
	session.Attach(link)
	params := testRegisterParams("host-one", "/work/project")
	registrationValue, rpcErr := session.HandleRequest(t.Context(), protocol.MethodRunnerRegister, mustRegistryJSON(t, params))
	require.Nil(t, rpcErr)
	registration := registrationValue.(protocol.RegisterResult)

	_, rpcErr = session.HandleRequest(t.Context(), protocol.MethodRunnerRegister, mustRegistryJSON(t, params))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeInvalidRequest, rpcErr.Code)
	for _, method := range []string{
		protocol.MethodUIInput,
		protocol.MethodUIConfirm,
		protocol.MethodUISelect,
		protocol.MethodUINotify,
		protocol.MethodUIWidgetSet,
		protocol.MethodUIWidgetFrame,
		protocol.MethodUIWidgetRemove,
		protocol.MethodUITranscriptAppend,
		protocol.MethodUISurfaceOpen,
		protocol.MethodUISurfaceFrame,
		protocol.MethodUISurfaceClose,
	} {
		result, rpcErr := session.HandleRequest(t.Context(), method, json.RawMessage(`{"runId":"run-one"}`))
		require.Nil(t, rpcErr)
		assert.Equal(t, map[string]bool{"ok": true}, result)
		assert.Equal(t, registration.RunnerID, router.runnerID)
		assert.Equal(t, method, router.method)
	}
	_, rpcErr = session.HandleRequest(t.Context(), "runner.unknown", json.RawMessage(`{}`))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeMethodNotFound, rpcErr.Code)

	session.HandleNotification(t.Context(), protocol.MethodRunnerHeartbeat, mustRegistryJSON(t, protocol.HeartbeatParams{
		RunnerID: registration.RunnerID, Generation: registration.Generation, State: protocol.RunnerStateIdle, ManifestDigest: "sha256:one",
	}))
	runner, ok := registry.Runner(registration.RunnerID)
	require.True(t, ok)
	assert.Equal(t, RunnerStatusIdle, runner.Status)
	session.HandleNotification(t.Context(), protocol.MethodRunnerManifestChanged, mustRegistryJSON(t, protocol.ManifestChangedParams{
		RunnerID: registration.RunnerID, Generation: registration.Generation, ManifestDigest: "sha256:two",
	}))
	runner, ok = registry.Runner(registration.RunnerID)
	require.True(t, ok)
	assert.True(t, runner.ManifestChanged)

	configureManifestLink(t, link, registration)
	_, err := registry.OpenRun(t.Context(), registration.RunnerID, testRunOpenParams("run-one", "conversation-one"))
	require.NoError(t, err)
	session.HandleNotification(t.Context(), protocol.MethodRunEnvironmentError, mustRegistryJSON(t, protocol.EnvironmentErrorParams{
		RunID: "run-one", Message: "runner extension failed",
	}))
	run, ok := registry.Run("run-one")
	require.True(t, ok)
	assert.Equal(t, RunStatusFailed, run.Status)
	session.HandleNotification(t.Context(), protocol.MethodToolUpdate, mustRegistryJSON(t, runnerpayload.ToolUpdateParams{
		RunID: "run-one", ToolCallID: "tool-one", Sequence: 1,
	}))
	session.HandleNotification(t.Context(), protocol.MethodToolUpdate, json.RawMessage(`not-json`))
	session.HandleNotification(t.Context(), protocol.MethodRunnerGoodbye, json.RawMessage(`{}`))
	runner, ok = registry.Runner(registration.RunnerID)
	require.True(t, ok)
	assert.False(t, runner.Connected)
	session.Detach(errors.New("already detached"))
}

func TestSessionValidationAndRPCErrorMapping(t *testing.T) {
	session := NewSession(nil, nil)
	_, rpcErr := session.HandleRequest(t.Context(), protocol.MethodRunnerRegister, json.RawMessage(`{}`))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeInternal, rpcErr.Code)

	registry := newTestRegistry(t)
	invalidSession := NewSession(registry, nil)
	invalidSession.Attach(newFakeLink())
	_, rpcErr = invalidSession.HandleRequest(t.Context(), protocol.MethodRunnerRegister, json.RawMessage(`not-json`))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeInvalidParams, rpcErr.Code)

	withoutUI := NewSession(registry, nil)
	withoutUI.Attach(newFakeLink())
	value, rpcErr := withoutUI.HandleRequest(t.Context(), protocol.MethodRunnerRegister, mustRegistryJSON(t, testRegisterParams("host-two", "/work/two")))
	require.Nil(t, rpcErr)
	assert.NotNil(t, value)
	_, rpcErr = withoutUI.HandleRequest(t.Context(), protocol.MethodUIInput, json.RawMessage(`{}`))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeUnavailable, rpcErr.Code)

	assert.Nil(t, rpcErrorFor(nil))
	assert.Equal(t, protocol.ErrorCodeBusy, rpcErrorFor(errors.New("runner is busy")).Code)
	assert.Equal(t, protocol.ErrorCodeStale, rpcErrorFor(errors.New("stale generation")).Code)
	assert.Equal(t, protocol.ErrorCodeConflict, rpcErrorFor(errors.New("already registered")).Code)
	assert.Equal(t, protocol.ErrorCodeInvalidParams, rpcErrorFor(errors.New("invalid input")).Code)
	legacyAuthError := rpcErrorFor(ErrLegacyAuthKeyEnrolled)
	assert.Equal(t, protocol.ErrorCodeConflict, legacyAuthError.Code)
	assert.Equal(t, protocol.ErrorReasonLegacyAuthKeyEnrolled, legacyAuthError.Reason())
	assert.False(t, isUIRequest("runner.unknown"))
}

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()
	registry, err := New(t.Context(), Options{
		HeartbeatInterval: time.Hour,
		HeartbeatTimeout:  2 * time.Hour,
		NewID:             sequentialIDs(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = registry.Close() })
	return registry
}

func sequentialIDs() func(string) (string, error) {
	var mu sync.Mutex
	count := 0
	return func(prefix string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		count++
		return fmt.Sprintf("%s-%d", prefix, count), nil
	}
}

func testRegisterParams(hostInstanceID, workspace string) protocol.RegisterParams {
	return protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version},
		Capabilities:     protocol.RunnerCapabilities{ConcurrentRuns: true},
		Host: protocol.Host{
			InstanceID: hostInstanceID,
			Hostname:   "host",
			OS:         "linux",
			Arch:       "amd64",
		},
		Workspace: protocol.Workspace{Path: workspace, Name: "project"},
	}
}

func testEnrollmentStartRequest(t *testing.T, hostInstanceID, workspace string) protocol.EnrollmentStartRequest {
	t.Helper()
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x42}, ed25519.SeedSize))
	publicKey := privateKey.Public().(ed25519.PublicKey)
	encodedPublicKey, err := protocol.EncodePublicKey(publicKey)
	require.NoError(t, err)
	fingerprint, err := protocol.CredentialFingerprint(publicKey)
	require.NoError(t, err)
	return protocol.EnrollmentStartRequest{
		ProtocolVersions: []int{protocol.Version},
		PublicKey:        encodedPublicKey,
		Fingerprint:      fingerprint,
		Host: protocol.Host{
			InstanceID: hostInstanceID,
			Hostname:   "host",
			OS:         "linux",
			Arch:       "amd64",
		},
		Workspace:      protocol.Workspace{Path: workspace, Name: "project"},
		KodeletVersion: "v-test",
	}
}

func testRunOpenParams(runID, conversationID string) protocol.RunOpenParams {
	return protocol.RunOpenParams{
		RunID:             runID,
		ConversationID:    conversationID,
		ReservedToolNames: []string{"get_goal", "update_goal", "read_conversation"},
	}
}

func configureManifestLink(t *testing.T, link *fakeLink, registration protocol.RegisterResult) {
	t.Helper()
	link.mu.Lock()
	defer link.mu.Unlock()
	link.call = func(_ context.Context, method string, params any, result any) error {
		switch method {
		case protocol.MethodRunOpen:
			request := params.(protocol.RunOpenParams)
			manifest := runnerpayload.Manifest{
				ProtocolVersion:  protocol.Version,
				RunnerID:         registration.RunnerID,
				RunID:            request.RunID,
				Generation:       registration.Generation,
				WorkingDirectory: "/work/project",
				Tools: []runnerpayload.ToolDefinition{{
					Name:        "bash",
					Description: "execute commands",
					InputSchema: map[string]any{"type": "object"},
					Placement:   "environment",
				}},
			}
			digest, err := runnerpayload.ComputeManifestDigest(manifest)
			require.NoError(t, err)
			manifest.Digest = digest
			*result.(*runnerpayload.Manifest) = manifest
		case protocol.MethodRunClose, protocol.MethodRunCancel:
			return nil
		default:
			return fmt.Errorf("unexpected method %s", method)
		}
		return nil
	}
}

func markRunnerReady(t *testing.T, registry *Registry, registration protocol.RegisterResult) {
	t.Helper()
	require.NoError(t, registry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.HeartbeatParams{
		RunnerID:   registration.RunnerID,
		Generation: registration.Generation,
		State:      protocol.RunnerStateIdle,
	}))
}

func mustRegistryJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(value)
	require.NoError(t, err)
	return payload
}
