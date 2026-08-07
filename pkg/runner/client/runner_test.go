package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/localstate"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	require.NoError(t, store.SaveRegistration(localstate.Registration{
		Server:    server.URL,
		Workspace: workspace,
		RunnerID:  "runner-stale",
	}))

	registered := make(chan protocol.RegisterResult, 1)
	runner, err := NewRunner(t.Context(), RunnerConfig{
		Server:           server.URL,
		AuthToken:        "runner-secret",
		Workspace:        workspace,
		DisplayName:      "project runner",
		Store:            store,
		ReconnectMin:     5 * time.Millisecond,
		ReconnectMax:     10 * time.Millisecond,
		ManifestInterval: time.Hour,
		OnRegistered: func(result protocol.RegisterResult) {
			registered <- result
		},
	})
	require.NoError(t, err)
	runtime := extensions.EmptyRuntime()
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	runner.service.runtimeProvider = staticRuntimeProvider{runtime: runtime}
	runner.service.configLoader = func(string) (llmtypes.Config, error) {
		return llmtypes.Config{AllowedTools: []string{"file_read"}}, nil
	}

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
	upgrader := websocket.Upgrader{
		Subprotocols: []string{protocol.Subprotocol},
		CheckOrigin:  func(*http.Request) bool { return true },
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
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

	retryDelays := make(chan time.Duration, 4)
	runner, err := NewRunner(t.Context(), RunnerConfig{
		Server:           server.URL,
		Workspace:        workspace,
		Store:            store,
		ReconnectMin:     10 * time.Millisecond,
		ReconnectMax:     20 * time.Millisecond,
		ManifestInterval: time.Hour,
		OnRetry: func(_ error, delay time.Duration) {
			retryDelays <- delay
		},
	})
	require.NoError(t, err)
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
}

func TestRunnerRunRequiresInitialization(t *testing.T) {
	var runner *Runner
	require.ErrorContains(t, runner.Run(t.Context()), "not initialized")
	require.ErrorContains(t, (&Runner{}).Run(t.Context()), "not initialized")
}
