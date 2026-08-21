package client

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/localstate"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type blockingRefreshInstanceProvider struct {
	workspace string
	probes    atomic.Int32
	started   atomic.Bool
	startedCh chan struct{}
}

type delayedInitialProbeInstanceProvider struct {
	workspace string
	delay     time.Duration
}

func saveTestRunnerCredential(t *testing.T, store *localstate.Store, server, workspace, credentialID string) localstate.Credential {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	fingerprint, err := protocol.CredentialFingerprint(publicKey)
	require.NoError(t, err)
	credential := localstate.Credential{
		Server:       server,
		Workspace:    workspace,
		CredentialID: credentialID,
		AccessToken:  mustRunnerAccessToken(t),
		Fingerprint:  fingerprint,
		PublicKey:    publicKey,
		PrivateKey:   privateKey,
	}
	require.NoError(t, store.SaveCredential(credential))
	return credential
}

func (p *delayedInitialProbeInstanceProvider) Create(ctx context.Context, spec ExecutionInstanceSpec) (ExecutionInstance, error) {
	if spec.Probe {
		timer := time.NewTimer(p.delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &directWorkspaceInstance{workspace: p.workspace}, nil
}

func (p *blockingRefreshInstanceProvider) Create(ctx context.Context, spec ExecutionInstanceSpec) (ExecutionInstance, error) {
	if spec.Probe && p.probes.Add(1) > 1 {
		if p.started.CompareAndSwap(false, true) {
			close(p.startedCh)
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &directWorkspaceInstance{workspace: p.workspace}, nil
}

func TestNormalizeServerURL(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		wantServer    string
		wantWebsocket string
		wantError     string
	}{
		{
			name:          "loopback http",
			input:         " http://localhost:8080/base/ ",
			wantServer:    "http://localhost:8080/base",
			wantWebsocket: "ws://localhost:8080/base/api/runner/v1/connect",
		},
		{
			name:          "loopback ipv4",
			input:         "http://127.0.0.1:9090",
			wantServer:    "http://127.0.0.1:9090",
			wantWebsocket: "ws://127.0.0.1:9090/api/runner/v1/connect",
		},
		{
			name:          "remote https",
			input:         "https://kodelet.example/control",
			wantServer:    "https://kodelet.example/control",
			wantWebsocket: "wss://kodelet.example/control/api/runner/v1/connect",
		},
		{name: "missing", input: "", wantError: "required"},
		{name: "invalid scheme", input: "ftp://localhost", wantError: "http or https"},
		{name: "remote plaintext", input: "http://kodelet.example", wantError: "require https"},
		{name: "missing host", input: "http:///path", wantError: "only scheme, host"},
		{name: "userinfo", input: "https://user@kodelet.example", wantError: "only scheme, host"},
		{name: "query", input: "https://kodelet.example?token=secret", wantError: "only scheme, host"},
		{name: "fragment", input: "https://kodelet.example/#fragment", wantError: "only scheme, host"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, websocketURL, err := normalizeServerURL(test.input)
			if test.wantError != "" {
				require.ErrorContains(t, err, test.wantError)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.wantServer, server)
			assert.Equal(t, test.wantWebsocket, websocketURL)
		})
	}

	assert.True(t, isLoopbackHostname("LOCALHOST"))
	assert.True(t, isLoopbackHostname("::1"))
	assert.False(t, isLoopbackHostname("kodelet.example"))
}

func TestNewRunnerValidatesConfigurationAndAppliesDefaults(t *testing.T) {
	workspace := t.TempDir()
	store, err := localstate.NewStoreAt(t.TempDir())
	require.NoError(t, err)

	runner, err := NewRunner(t.Context(), RunnerConfig{
		Server:    "http://localhost:8080",
		Workspace: workspace,
		Store:     store,
	})
	require.NoError(t, err)
	assert.Equal(t, defaultReconnectMin, runner.config.ReconnectMin)
	assert.Equal(t, defaultReconnectMax, runner.config.ReconnectMax)
	assert.Equal(t, defaultManifestInterval, runner.config.ManifestInterval)
	assert.Equal(t, defaultManifestProbeTimeout, runner.config.ManifestProbeTimeout)
	assert.Equal(t, filepath.Base(workspace), filepath.Base(runner.workspace))
	assert.NotEmpty(t, runner.host.InstanceID)
	assert.NotEmpty(t, runner.host.Hostname)
	assert.Positive(t, runner.host.PID)
	require.NoError(t, runner.service.Close())

	_, err = NewRunner(t.Context(), RunnerConfig{
		Server:       "http://localhost:8080",
		Workspace:    workspace,
		Store:        store,
		ReconnectMin: 2 * time.Second,
		ReconnectMax: time.Second,
	})
	require.ErrorContains(t, err, "maximum")

	_, err = NewRunner(t.Context(), RunnerConfig{
		Server:    "http://localhost:8080",
		Workspace: filepath.Join(workspace, "missing"),
		Store:     store,
	})
	require.Error(t, err)
}

func TestRunnerCloseReleasesWorkspaceLockAfterRunCleanupError(t *testing.T) {
	workspace := t.TempDir()
	store, err := localstate.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	runner, err := NewRunner(t.Context(), RunnerConfig{
		Server:    "http://localhost:8080",
		Workspace: workspace,
		Store:     store,
	})
	require.NoError(t, err)
	runtime := extensions.EmptyRuntime()
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	runner.service.runtimeProvider = staticRuntimeProvider{runtime: runtime}
	runner.service.configLoader = func(string) (llmtypes.Config, error) { return llmtypes.Config{}, nil }
	runner.service.instanceProvider = &recordingExecutionInstanceProvider{
		workspace: workspace,
		closeErr:  errors.New("instance close failed"),
	}
	runner.service.Attach(&recordingPeer{})
	require.NoError(t, runner.service.SetRegistration(protocol.RegisterResult{RunnerID: "runner-1", Generation: 1}))
	callService[runnerpayload.Manifest](t, runner.service, protocol.MethodRunOpen, protocol.RunOpenParams{
		RunID: "run-1", ConversationID: "conversation-1",
	})
	require.NoError(t, runner.AcquireWorkspaceLock())

	err = runner.Close()
	require.ErrorContains(t, err, "instance close failed")
	held, err := store.WorkspaceLockHeld(workspace)
	require.NoError(t, err)
	assert.False(t, held)
	assert.Nil(t, runner.lock)
}

func TestRunnerCloseReleasesWorkspaceLockAfterBoundedTerminalCleanup(t *testing.T) {
	workspace := t.TempDir()
	store, err := localstate.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	runner, err := NewRunner(t.Context(), RunnerConfig{
		Server:    "http://localhost:8080",
		Workspace: workspace,
		Store:     store,
	})
	require.NoError(t, err)
	require.NoError(t, runner.AcquireWorkspaceLock())

	shell := filepath.Join(t.TempDir(), "ignore-hup.sh")
	require.NoError(t, os.WriteFile(shell, []byte("#!/bin/bash\ntrap '' HUP TERM\nwhile :; do sleep 60; done\n"), 0o700))
	t.Setenv("SHELL", shell)
	opened, err := runner.service.workspaceTerminals.Open(t.Context(), 24, 80)
	require.NoError(t, err)
	require.NotEmpty(t, opened.SessionID)

	started := time.Now()
	require.NoError(t, runner.Close())
	assert.Less(t, time.Since(started), workspaceTerminalShutdownWait)
	held, err := store.WorkspaceLockHeld(workspace)
	require.NoError(t, err)
	assert.False(t, held)
	assert.Nil(t, runner.lock)
	require.NoError(t, runner.Close())
}

func TestRunnerRegistersHeartbeatsAndReleasesWorkspaceLock(t *testing.T) {
	workspace := t.TempDir()
	store, err := localstate.NewStoreAt(t.TempDir())
	require.NoError(t, err)

	registry, err := runnerregistry.New(t.Context(), runnerregistry.Options{
		HeartbeatInterval: time.Hour,
		HeartbeatTimeout:  2 * time.Hour,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, registry.Close()) })

	upgrader := websocket.Upgrader{
		Subprotocols: []string{protocol.Subprotocol},
		CheckOrigin:  func(*http.Request) bool { return true },
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		assert.Equal(t, protocol.Endpoint, request.URL.Path)
		assert.Equal(t, "Bearer runner-secret", request.Header.Get("Authorization"))
		assert.Empty(t, request.Header.Get(protocol.DPoPHeader))
		conn, upgradeErr := upgrader.Upgrade(w, request, nil)
		if upgradeErr != nil {
			return
		}
		session := runnerregistry.NewSession(registry, nil)
		peer, peerErr := protocol.NewPeer(conn, protocol.PeerConfig{
			RequestPrefix: "server",
			Handler:       session,
			Notifications: session,
		})
		if peerErr != nil {
			_ = conn.Close()
			return
		}
		session.Attach(peer)
		if peerErr = peer.Start(request.Context()); peerErr != nil {
			_ = peer.Close()
			return
		}
		<-peer.Done()
		session.Detach(peer.Err())
	}))
	t.Cleanup(server.Close)
	saveTestRunnerCredential(t, store, server.URL, workspace, "credential-ignored")

	require.NoError(t, store.SaveRegistration(localstate.Registration{
		Server:    server.URL,
		Workspace: workspace,
		RunnerID:  "runner-stale",
	}))

	registered := make(chan protocol.RegisterResult, 1)
	runner, err := NewRunner(t.Context(), RunnerConfig{
		Server:               server.URL,
		AuthToken:            "runner-secret",
		Workspace:            workspace,
		DisplayName:          "project runner",
		Store:                store,
		ReconnectMin:         5 * time.Millisecond,
		ReconnectMax:         10 * time.Millisecond,
		ManifestInterval:     time.Hour,
		ManifestProbeTimeout: 5 * time.Millisecond,
		OnRegistered: func(result protocol.RegisterResult) {
			registered <- result
		},
	})
	require.NoError(t, err)
	assert.Nil(t, runner.credential)
	runtime := extensions.EmptyRuntime()
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	runner.service.runtimeProvider = staticRuntimeProvider{runtime: runtime}
	runner.service.configLoader = func(string) (llmtypes.Config, error) {
		return llmtypes.Config{AllowedTools: []string{"file_read"}}, nil
	}
	runner.service.instanceProvider = &delayedInitialProbeInstanceProvider{workspace: workspace, delay: 25 * time.Millisecond}

	runCtx, cancel := context.WithCancel(t.Context())
	runDone := make(chan error, 1)
	go func() { runDone <- runner.Run(runCtx) }()

	var registration protocol.RegisterResult
	select {
	case registration = <-registered:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not register")
	}
	assert.NotEqual(t, "runner-stale", registration.RunnerID)
	require.Eventually(t, func() bool {
		entry, ok := registry.Runner(registration.RunnerID)
		return ok && entry.Status == runnerregistry.RunnerStatusIdle && entry.ManifestDigest != ""
	}, 5*time.Second, 10*time.Millisecond)

	metadata, found, err := store.ReadWorkspaceLockMetadata(workspace)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, registration.RunnerID, metadata.RunnerID)
	assert.Equal(t, "project runner", metadata.DisplayName)
	assert.Equal(t, runner.host.PID, metadata.PID)
	assert.Nil(t, metadata.StoppedAt)

	cached, found, err := store.LoadRegistration(server.URL, workspace)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, registration.RunnerID, cached.RunnerID)

	cancel()
	select {
	case err = <-runDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not stop after cancellation")
	}

	metadata, found, err = store.ReadWorkspaceLockMetadata(workspace)
	require.NoError(t, err)
	require.True(t, found)
	assert.NotNil(t, metadata.StoppedAt)
}

func TestManifestRefreshDoesNotBlockRunnerHeartbeats(t *testing.T) {
	workspace := t.TempDir()
	store, err := localstate.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	registry, err := runnerregistry.New(t.Context(), runnerregistry.Options{
		HeartbeatInterval: 5 * time.Millisecond,
		HeartbeatTimeout:  30 * time.Millisecond,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, registry.Close()) })

	upgrader := websocket.Upgrader{Subprotocols: []string{protocol.Subprotocol}, CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		conn, upgradeErr := upgrader.Upgrade(w, request, nil)
		if upgradeErr != nil {
			return
		}
		session := runnerregistry.NewSession(registry, nil)
		peer, peerErr := protocol.NewPeer(conn, protocol.PeerConfig{RequestPrefix: "server", Handler: session, Notifications: session})
		if peerErr != nil {
			_ = conn.Close()
			return
		}
		session.Attach(peer)
		if peerErr = peer.Start(request.Context()); peerErr != nil {
			_ = peer.Close()
			return
		}
		<-peer.TransportDone()
		session.Detach(peer.Err())
	}))
	t.Cleanup(server.Close)

	registered := make(chan protocol.RegisterResult, 2)
	runner, err := NewRunner(t.Context(), RunnerConfig{
		Server:               server.URL,
		Workspace:            workspace,
		Store:                store,
		ReconnectMin:         5 * time.Millisecond,
		ReconnectMax:         10 * time.Millisecond,
		ManifestInterval:     5 * time.Millisecond,
		ManifestProbeTimeout: 200 * time.Millisecond,
		OnRegistered: func(result protocol.RegisterResult) {
			registered <- result
		},
	})
	require.NoError(t, err)
	runtime := extensions.EmptyRuntime()
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	runner.service.runtimeProvider = staticRuntimeProvider{runtime: runtime}
	runner.service.configLoader = func(string) (llmtypes.Config, error) { return llmtypes.Config{}, nil }
	provider := &blockingRefreshInstanceProvider{workspace: workspace, startedCh: make(chan struct{})}
	runner.service.instanceProvider = provider

	runCtx, cancel := context.WithCancel(t.Context())
	runDone := make(chan error, 1)
	go func() { runDone <- runner.Run(runCtx) }()
	registration := <-registered
	select {
	case <-provider.startedCh:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("periodic manifest refresh did not start")
	}
	time.Sleep(80 * time.Millisecond)
	entry, ok := registry.Runner(registration.RunnerID)
	require.True(t, ok)
	assert.True(t, entry.Connected)
	assert.Equal(t, registration.Generation, entry.Generation)

	cancel()
	select {
	case err = <-runDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not stop")
	}
}

func TestRunnerTreatsAuthenticationFailureAsPermanent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	store, err := localstate.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	runner, err := NewRunner(t.Context(), RunnerConfig{
		Server:       server.URL,
		Workspace:    t.TempDir(),
		Store:        store,
		ReconnectMin: time.Millisecond,
		ReconnectMax: time.Millisecond,
	})
	require.NoError(t, err)
	runtime := extensions.EmptyRuntime()
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	runner.service.runtimeProvider = staticRuntimeProvider{runtime: runtime}
	runner.service.configLoader = func(string) (llmtypes.Config, error) { return llmtypes.Config{}, nil }

	err = runner.Run(t.Context())
	require.ErrorContains(t, err, "authentication failed")
	assert.True(t, isPermanentConnectionError(err))

	wrapped := &permanentConnectionError{err: errors.New("permanent")}
	assert.Equal(t, "permanent", wrapped.Error())
	assert.EqualError(t, wrapped.Unwrap(), "permanent")
}

func TestRunnerTreatsUnknownOrRevokedCredentialAsPermanent(t *testing.T) {
	workspace := t.TempDir()
	store, err := localstate.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	var connectionRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != protocol.Endpoint {
			t.Errorf("unexpected request path %q", request.URL.Path)
			http.NotFound(w, request)
			return
		}
		connectionRequests.Add(1)
		assert.Contains(t, request.Header.Get("Authorization"), protocol.DPoPAuthorizationScheme+" ")
		assert.NotEmpty(t, request.Header.Get(protocol.DPoPHeader))
		http.Error(w, "runner credential is invalid or revoked", http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	saveTestRunnerCredential(t, store, server.URL, workspace, "credential-revoked")

	runner, err := NewRunner(t.Context(), RunnerConfig{
		Server:       server.URL,
		Workspace:    workspace,
		Store:        store,
		ReconnectMin: time.Millisecond,
		ReconnectMax: time.Millisecond,
	})
	require.NoError(t, err)
	runtime := extensions.EmptyRuntime()
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	runner.service.runtimeProvider = staticRuntimeProvider{runtime: runtime}
	runner.service.configLoader = func(string) (llmtypes.Config, error) { return llmtypes.Config{}, nil }

	err = runner.Run(t.Context())
	require.ErrorContains(t, err, "unknown or revoked")
	require.ErrorContains(t, err, "re-enroll")
	assert.True(t, isPermanentConnectionError(err))
	assert.Equal(t, int32(1), connectionRequests.Load())
}

func TestRunnerReloadsCredentialReplacedDuringAuthenticationFailure(t *testing.T) {
	workspace := t.TempDir()
	store, err := localstate.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	var current localstate.Credential
	var replacement localstate.Credential
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		verifyRunnerDPoPRequest(t, request, current)
		require.NoError(t, store.SaveCredential(replacement))
		http.Error(w, "runner credential is invalid or revoked", http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	current = saveTestRunnerCredential(t, store, server.URL, workspace, "credential-old")
	replacementPublicKey, replacementPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	replacementFingerprint, err := protocol.CredentialFingerprint(replacementPublicKey)
	require.NoError(t, err)
	replacement = localstate.Credential{
		Server:       server.URL,
		Workspace:    workspace,
		CredentialID: "credential-new",
		AccessToken:  mustRunnerAccessToken(t),
		Fingerprint:  replacementFingerprint,
		PublicKey:    replacementPublicKey,
		PrivateKey:   replacementPrivateKey,
	}

	runner, err := NewRunner(t.Context(), RunnerConfig{Server: server.URL, Workspace: workspace, Store: store})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runner.service.Close()) })

	connected, err := runner.runConnection(t.Context(), "manifest-digest")

	assert.False(t, connected)
	require.ErrorContains(t, err, "credential was replaced")
	assert.False(t, isPermanentConnectionError(err))
	require.NotNil(t, runner.credential)
	assert.Equal(t, replacement.CredentialID, runner.credential.CredentialID)
	headers, keyAuthenticated, err := runner.connectionHeaders()
	require.NoError(t, err)
	assert.True(t, keyAuthenticated)
	assert.Equal(t, protocol.DPoPAuthorizationScheme+" "+replacement.AccessToken, headers.Get("Authorization"))
}

func TestKeyAuthenticatedRunnerDoesNotDiscardStaleRunnerID(t *testing.T) {
	workspace := t.TempDir()
	store, err := localstate.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	var credential localstate.Credential
	var registerCalls atomic.Int32
	requestedRunnerIDs := make([]string, 0, 1)
	var requestedRunnerIDsMu sync.Mutex
	upgrader := websocket.Upgrader{
		Subprotocols: []string{protocol.Subprotocol},
		CheckOrigin:  func(*http.Request) bool { return true },
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != protocol.Endpoint {
			http.NotFound(w, request)
			return
		}
		verifyRunnerDPoPRequest(t, request, credential)
		conn, upgradeErr := upgrader.Upgrade(w, request, nil)
		if upgradeErr != nil {
			return
		}
		peer, peerErr := protocol.NewPeer(conn, protocol.PeerConfig{
			RequestPrefix: "server",
			Handler: protocol.RequestHandlerFunc(func(_ context.Context, method string, raw json.RawMessage) (any, *protocol.RPCError) {
				if method != protocol.MethodRunnerRegister {
					return nil, &protocol.RPCError{Code: protocol.ErrorCodeMethodNotFound, Message: "unknown method"}
				}
				var params protocol.RegisterParams
				if err := json.Unmarshal(raw, &params); err != nil {
					return nil, &protocol.RPCError{Code: protocol.ErrorCodeInvalidParams, Message: err.Error()}
				}
				registerCalls.Add(1)
				requestedRunnerIDsMu.Lock()
				requestedRunnerIDs = append(requestedRunnerIDs, params.RunnerID)
				requestedRunnerIDsMu.Unlock()
				return nil, &protocol.RPCError{
					Code:    protocol.ErrorCodeStale,
					Message: "runner not found",
					Data:    protocol.RPCErrorData{Reason: protocol.ErrorReasonRunnerNotFound},
				}
			}),
		})
		if peerErr != nil {
			_ = conn.Close()
			return
		}
		if peerErr = peer.Start(request.Context()); peerErr != nil {
			_ = peer.Close()
			return
		}
		<-peer.Done()
	}))
	t.Cleanup(server.Close)
	credential = saveTestRunnerCredential(t, store, server.URL, workspace, "credential-stale")
	require.NoError(t, store.SaveRegistration(localstate.Registration{
		Server:    server.URL,
		Workspace: workspace,
		RunnerID:  "runner-stale",
	}))

	runner, err := NewRunner(t.Context(), RunnerConfig{Server: server.URL, Workspace: workspace, Store: store})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runner.service.Close()) })
	connected, err := runner.runConnection(t.Context(), "manifest-digest")
	require.ErrorContains(t, err, "runner not found")
	assert.False(t, connected)
	assert.Equal(t, int32(1), registerCalls.Load())
	requestedRunnerIDsMu.Lock()
	assert.Equal(t, []string{"runner-stale"}, requestedRunnerIDs)
	requestedRunnerIDsMu.Unlock()
}

func TestRunnerReconnectBackoffResetsAfterSuccessfulRegistration(t *testing.T) {
	workspace := t.TempDir()
	store, err := localstate.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	registry, err := runnerregistry.New(t.Context(), runnerregistry.Options{
		HeartbeatInterval: time.Hour,
		HeartbeatTimeout:  2 * time.Hour,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, registry.Close()) })

	var attempts atomic.Int32
	var credential localstate.Credential
	seenProofs := map[string]struct{}{}
	var seenProofsMu sync.Mutex
	upgrader := websocket.Upgrader{
		Subprotocols: []string{protocol.Subprotocol},
		CheckOrigin:  func(*http.Request) bool { return true },
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != protocol.Endpoint {
			http.NotFound(w, request)
			return
		}
		verified := verifyRunnerDPoPRequest(t, request, credential)
		seenProofsMu.Lock()
		_, replayed := seenProofs[verified.JTI]
		seenProofs[verified.JTI] = struct{}{}
		seenProofsMu.Unlock()
		if replayed {
			http.Error(w, "replayed DPoP proof", http.StatusUnauthorized)
			return
		}
		if attempts.Add(1) <= 2 {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		conn, upgradeErr := upgrader.Upgrade(w, request, nil)
		if upgradeErr != nil {
			return
		}
		session := runnerregistry.NewSession(registry, nil)
		peer, peerErr := protocol.NewPeer(conn, protocol.PeerConfig{
			RequestPrefix: "server",
			Handler:       session,
			Notifications: session,
		})
		if peerErr != nil {
			_ = conn.Close()
			return
		}
		session.Attach(peer)
		if peerErr = peer.Start(request.Context()); peerErr != nil {
			_ = peer.Close()
			return
		}
		go func() {
			deadline := time.NewTimer(2 * time.Second)
			ticker := time.NewTicker(time.Millisecond)
			defer deadline.Stop()
			defer ticker.Stop()
			for {
				select {
				case <-deadline.C:
					_ = peer.Close()
					return
				case <-ticker.C:
					for _, registered := range registry.Runners() {
						if registered.Connected {
							_ = peer.Close()
							return
						}
					}
				}
			}
		}()
		<-peer.Done()
		session.Detach(peer.Err())
	}))
	t.Cleanup(server.Close)
	credential = saveTestRunnerCredential(t, store, server.URL, workspace, "credential-reconnect")

	retryDelays := make(chan time.Duration, 4)
	runner, err := NewRunner(t.Context(), RunnerConfig{
		Server:           server.URL + "/",
		Workspace:        workspace + string(filepath.Separator) + ".",
		Store:            store,
		ReconnectMin:     10 * time.Millisecond,
		ReconnectMax:     20 * time.Millisecond,
		ManifestInterval: time.Hour,
		OnRetry: func(_ error, delay time.Duration) {
			retryDelays <- delay
		},
	})
	require.NoError(t, err)
	require.NotNil(t, runner.credential)
	assert.Equal(t, credential.CredentialID, runner.credential.CredentialID)
	runtime := extensions.EmptyRuntime()
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	runner.service.runtimeProvider = staticRuntimeProvider{runtime: runtime}
	runner.service.configLoader = func(string) (llmtypes.Config, error) { return llmtypes.Config{}, nil }

	runCtx, cancel := context.WithCancel(t.Context())
	runDone := make(chan error, 1)
	go func() { runDone <- runner.Run(runCtx) }()

	delays := make([]time.Duration, 0, 3)
	for len(delays) < 3 {
		select {
		case delay := <-retryDelays:
			delays = append(delays, delay)
		case <-time.After(5 * time.Second):
			t.Fatal("runner did not report expected reconnect attempts")
		}
	}
	assert.Equal(t, []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 10 * time.Millisecond}, delays)
	cancel()
	select {
	case err = <-runDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not stop after cancellation")
	}
	assert.Equal(t, int32(3), attempts.Load())
	seenProofsMu.Lock()
	assert.Len(t, seenProofs, int(attempts.Load()))
	seenProofsMu.Unlock()
}

func verifyRunnerDPoPRequest(t *testing.T, request *http.Request, credential localstate.Credential) protocol.VerifiedDPoPProof {
	t.Helper()
	scheme, accessToken, found := strings.Cut(request.Header.Get("Authorization"), " ")
	require.True(t, found)
	assert.Equal(t, protocol.DPoPAuthorizationScheme, scheme)
	assert.Equal(t, credential.AccessToken, accessToken)
	targetScheme := "http"
	if request.TLS != nil {
		targetScheme = "https"
	}
	verified, err := protocol.VerifyDPoPProof(request.Header.Get(protocol.DPoPHeader), protocol.DPoPVerificationOptions{
		Method:      request.Method,
		TargetURL:   targetScheme + "://" + request.Host + request.URL.EscapedPath(),
		AccessToken: accessToken,
		PublicKey:   credential.PublicKey,
		Now:         time.Now(),
		MaxAge:      5 * time.Minute,
		FutureSkew:  time.Minute,
	})
	require.NoError(t, err)
	return verified
}

func mustRunnerAccessToken(t *testing.T) string {
	t.Helper()
	token, err := protocol.NewRunnerAccessToken()
	require.NoError(t, err)
	return token
}

func TestRunnerRunRequiresInitialization(t *testing.T) {
	var runner *Runner
	require.ErrorContains(t, runner.Run(t.Context()), "not initialized")
	require.ErrorContains(t, (&Runner{}).Run(t.Context()), "not initialized")
}
