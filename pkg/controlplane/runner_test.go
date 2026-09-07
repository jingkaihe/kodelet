package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/jingkaihe/kodelet/pkg/agentenv"
	chat "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	runnerclient "github.com/jingkaihe/kodelet/pkg/runner/client"
	"github.com/jingkaihe/kodelet/pkg/runner/localstate"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/jingkaihe/kodelet/pkg/version"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type runnerAPITestLink struct {
	done chan struct{}
	call func(context.Context, string, any, any) error
}

func TestLocalServerStopRequiresIdentityAndIdleServer(t *testing.T) {
	for _, test := range []struct {
		name   string
		body   string
		active bool
		code   int
	}{
		{"invalid", "{", false, http.StatusBadRequest},
		{"wrong instance", `{"instanceId":"other"}`, false, http.StatusConflict},
		{"active", `{"instanceId":"same"}`, true, http.StatusConflict},
		{"idle", `{"instanceId":"same"}`, false, http.StatusOK},
		{"force", `{"instanceId":"same","force":true}`, true, http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			stopped := false
			server := &Server{config: &ServerConfig{InstanceID: "same", LocalShutdown: func() { stopped = true }}, activeChats: map[string]*activeChatRun{}}
			if test.active {
				server.activeChats["active"] = &activeChatRun{}
			}
			response := httptest.NewRecorder()
			server.handleLocalServerStop(response, httptest.NewRequest(http.MethodPost, "/api/server/stop", strings.NewReader(test.body)))
			assert.Equal(t, test.code, response.Code)
			assert.Equal(t, test.code == http.StatusOK, stopped)
			assert.Equal(t, stopped, server.stopping)
		})
	}
}

func newRunnerAPITestLink() *runnerAPITestLink {
	return &runnerAPITestLink{done: make(chan struct{})}
}

func (l *runnerAPITestLink) Call(ctx context.Context, method string, params any, result any) error {
	if l.call != nil {
		return l.call(ctx, method, params, result)
	}
	return nil
}

func (l *runnerAPITestLink) CallTracked(ctx context.Context, method string, params any, result any, onRequestID func(string)) error {
	if onRequestID != nil {
		onRequestID("web-test:request")
	}
	return l.Call(ctx, method, params, result)
}
func (*runnerAPITestLink) Notify(context.Context, string, any) error { return nil }
func (*runnerAPITestLink) Close() error                              { return nil }
func (l *runnerAPITestLink) Done() <-chan struct{}                   { return l.done }
func (*runnerAPITestLink) Err() error                                { return nil }

func TestEmbeddedRunnerTransportLifecycle(t *testing.T) {
	for _, mode := range []RunnerAuthMode{RunnerAuthModeToken, RunnerAuthModeEnrollment, RunnerAuthModeNone} {
		t.Run(string(mode), func(t *testing.T) {
			t.Setenv("KODELET_BASE_PATH", t.TempDir())
			require.NoError(t, db.RunMigrations(t.Context(), migrations.All()))
			workspace := t.TempDir()
			store, err := localstate.NewStoreAt(t.TempDir())
			require.NoError(t, err)
			config := &ServerConfig{
				Host: "127.0.0.1", Port: 0, CompactRatio: 0.8,
				WebAuthMode: WebAuthModeToken, AuthToken: "web-secret", RunnerAuthMode: mode,
				EmbeddedRunner: &EmbeddedRunnerConfig{Workspace: workspace, Store: store, Settings: map[string]any{
					"extensions": map[string]any{"enabled": false}, "skills": map[string]any{"enabled": false},
				}},
			}
			if mode == RunnerAuthModeToken {
				config.RunnerAuthToken = "runner-secret"
			}
			server, err := NewServer(t.Context(), config, testFrontendHandler())
			require.NoError(t, err)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			endpoint := "http://" + listener.Addr().String()
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			go func() { done <- server.Serve(ctx, listener) }()
			t.Cleanup(func() {
				cancel()
				select {
				case err := <-done:
					assert.NoError(t, err)
				case <-time.After(10 * time.Second):
					assert.Fail(t, "embedded service did not stop")
				}
				held, err := store.WorkspaceLockHeld(workspace)
				assert.NoError(t, err)
				assert.False(t, held, "shutdown must release the workspace lock")
				assert.NoError(t, server.Close())
			})
			require.Eventually(t, func() bool { return server.EmbeddedRunnerStatus().Ready }, 10*time.Second, 20*time.Millisecond,
				"embedded runner failed to become ready")
			status := server.EmbeddedRunnerStatus()
			assert.True(t, status.Enabled)
			assert.Empty(t, status.Error)
			runner, found := server.runnerRegistry.Runner(status.RunnerID)
			require.True(t, found)
			assert.NotEmpty(t, runner.ConnectionID, "embedding must register through the normal transport")
			assert.Positive(t, runner.Generation)
			assert.Equal(t, workspace, runner.Workspace.Path)
			credential, enrolled, err := store.LoadCredential(endpoint, workspace)
			require.NoError(t, err)
			assert.Equal(t, mode == RunnerAuthModeEnrollment, enrolled)
			if enrolled {
				assert.NotEmpty(t, credential.AccessToken)
				assert.NotEmpty(t, credential.PrivateKey)
			}
			request := httptest.NewRequest(http.MethodGet, "/api/status", nil)
			response := httptest.NewRecorder()
			server.router.ServeHTTP(response, request)
			assert.Equal(t, http.StatusUnauthorized, response.Code, "embedding must not bypass API authentication")
			request.Header.Set("Authorization", "Bearer web-secret")
			response = httptest.NewRecorder()
			server.router.ServeHTTP(response, request)
			assert.Equal(t, http.StatusOK, response.Code)
			assert.Contains(t, response.Body.String(), `"ready":true`)
		})
	}
}

func TestEmbeddedRunnerLockConflictKeepsAPIAvailable(t *testing.T) {
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	require.NoError(t, db.RunMigrations(t.Context(), migrations.All()))
	workspace := t.TempDir()
	store, err := localstate.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	lock, err := store.AcquireWorkspaceLock(workspace, localstate.LockMetadata{RunnerID: "existing-owner"})
	require.NoError(t, err)
	defer lock.Close()
	server, err := NewServer(t.Context(), &ServerConfig{
		Host: "127.0.0.1", Port: 0, CompactRatio: 0.8, WebAuthMode: WebAuthModeNone, RunnerAuthMode: RunnerAuthModeNone,
		EmbeddedRunner: &EmbeddedRunnerConfig{Workspace: workspace, Store: store},
	}, testFrontendHandler())
	require.NoError(t, err)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, listener) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(10 * time.Second):
			assert.Fail(t, "service did not stop after lock conflict")
		}
		assert.NoError(t, server.Close())
	})
	require.Eventually(t, func() bool { return server.EmbeddedRunnerStatus().Error != "" }, 5*time.Second, 20*time.Millisecond)
	status := server.EmbeddedRunnerStatus()
	assert.False(t, status.Ready)
	assert.Contains(t, status.Error, "existing-owner")
	assert.Contains(t, status.Error, "--embedded-runner=false")
	assert.Empty(t, server.runnerRegistry.Runners(), "lock conflict must not create a second enrolled owner")
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	assert.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"apiReady":true`)
	held, err := store.WorkspaceLockHeld(workspace)
	require.NoError(t, err)
	assert.True(t, held, "existing ownership must not be stolen")
}

func TestEmbeddedLoopbackEndpoint(t *testing.T) {
	for _, test := range []struct{ address, expected string }{
		{"0.0.0.0", "http://127.0.0.1:4321"},
		{"127.0.0.1", "http://127.0.0.1:4321"},
		{"::", "http://[::1]:4321"},
		{"::1", "http://[::1]:4321"},
	} {
		endpoint, err := embeddedLoopbackEndpoint(&net.TCPAddr{IP: net.ParseIP(test.address), Port: 4321})
		require.NoError(t, err)
		assert.Equal(t, test.expected, endpoint)
	}
	_, err := embeddedLoopbackEndpoint(&net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 4321})
	assert.ErrorContains(t, err, "must be able to connect locally")
}

// The returned stop function closes persistence too, allowing restart tests to
// reopen the same database and endpoint without retaining the previous registry.
func startEmbeddedRunnerTestServer(t *testing.T, config *ServerConfig, address string) (*Server, string, func()) {
	t.Helper()
	server, err := NewServer(t.Context(), config, testFrontendHandler())
	require.NoError(t, err)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		assert.NoError(t, server.Close())
		require.NoError(t, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, listener) }()
	stop := sync.OnceFunc(func() {
		cancel()
		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(10 * time.Second):
			assert.Fail(t, "embedded service did not stop")
		}
		assert.NoError(t, server.Close())
	})
	t.Cleanup(stop)
	return server, "http://" + listener.Addr().String(), stop
}

func embeddedRunnerTestConfig(t *testing.T) *ServerConfig {
	t.Helper()
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	require.NoError(t, db.RunMigrations(t.Context(), migrations.All()))
	store, err := localstate.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	return &ServerConfig{
		Host: "127.0.0.1", Port: 0, CompactRatio: 0.8,
		WebAuthMode: WebAuthModeToken, AuthToken: "web-secret", RunnerAuthMode: RunnerAuthModeNone,
		EmbeddedRunner: &EmbeddedRunnerConfig{Workspace: t.TempDir(), Store: store, Settings: map[string]any{
			"extensions": map[string]any{"enabled": false}, "skills": map[string]any{"enabled": false},
		}},
	}
}

func TestEmbeddedRunnerEnrollmentReuseAndRevocation(t *testing.T) {
	config := embeddedRunnerTestConfig(t)
	config.RunnerAuthMode = RunnerAuthModeEnrollment
	store, workspace := config.EmbeddedRunner.Store, config.EmbeddedRunner.Workspace
	server, endpoint, stop := startEmbeddedRunnerTestServer(t, config, "127.0.0.1:0")
	require.Eventually(t, func() bool { return server.EmbeddedRunnerStatus().Ready }, 5*time.Second, 10*time.Millisecond)
	runnerID := server.EmbeddedRunnerStatus().RunnerID
	credential, found, err := store.LoadCredential(endpoint, workspace)
	require.NoError(t, err)
	require.True(t, found)
	var approvedBy string
	require.NoError(t, server.authStore.db.GetContext(t.Context(), &approvedBy, "SELECT approved_by FROM runner_enrollments WHERE runner_id = ?", runnerID))
	assert.Equal(t, "daemon:embedded-runner", approvedBy)
	var enrolledVersion string
	require.NoError(t, server.authStore.db.GetContext(t.Context(), &enrolledVersion, "SELECT kodelet_version FROM runner_enrollments WHERE runner_id = ?", runnerID))
	assert.Equal(t, version.Get().Version, enrolledVersion)
	stop()

	server, _, stop = startEmbeddedRunnerTestServer(t, config, strings.TrimPrefix(endpoint, "http://"))
	require.Eventually(t, func() bool { return server.EmbeddedRunnerStatus().Ready }, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, runnerID, server.EmbeddedRunnerStatus().RunnerID)
	reused, found, err := store.LoadCredential(endpoint, workspace)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, credential, reused, "restart must reuse the approved identity and key")
	var credentials int
	require.NoError(t, server.authStore.db.GetContext(t.Context(), &credentials, "SELECT COUNT(*) FROM runner_credentials"))
	assert.Equal(t, 1, credentials)
	_, err = server.authStore.db.ExecContext(t.Context(), "UPDATE runner_credentials SET revoked_at = ?, revoke_reason = ? WHERE id = ?", time.Now().UTC(), "test revocation", credential.CredentialID)
	require.NoError(t, err)
	stop()

	server, _, _ = startEmbeddedRunnerTestServer(t, config, strings.TrimPrefix(endpoint, "http://"))
	require.Eventually(t, func() bool { return server.EmbeddedRunnerStatus().Error != "" }, 5*time.Second, 10*time.Millisecond)
	assert.False(t, server.EmbeddedRunnerStatus().Ready)
	assert.Contains(t, server.EmbeddedRunnerStatus().Error, "re-enroll")
	require.NoError(t, server.authStore.db.GetContext(t.Context(), &credentials, "SELECT COUNT(*) FROM runner_credentials"))
	assert.Equal(t, 1, credentials, "revocation must not trigger a replacement enrollment")
	assert.Len(t, server.runnerRegistry.Runners(), 1)
	held, err := store.WorkspaceLockHeld(workspace)
	require.NoError(t, err)
	assert.False(t, held, "provisioning failure must release the workspace lock")
	unchanged, found, err := store.LoadCredential(endpoint, workspace)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, credential, unchanged)
}

type embeddedTestEnvironment struct {
	agentenv.Environment
	open    func(context.Context, agentenv.RunSpec) (agentenv.Manifest, error)
	close   func(context.Context) error
	execute func(context.Context, agentenv.ToolRequest, agentenv.ToolUpdateSink) (agentenv.ToolExecution, error)
}

func (e *embeddedTestEnvironment) Open(ctx context.Context, spec agentenv.RunSpec) (agentenv.Manifest, error) {
	if e.open != nil {
		return e.open(ctx, spec)
	}
	return e.Environment.Open(ctx, spec)
}

func (e *embeddedTestEnvironment) Close(ctx context.Context) error {
	if e.close != nil {
		return e.close(ctx)
	}
	return e.Environment.Close(ctx)
}

func (e *embeddedTestEnvironment) ExecuteTool(ctx context.Context, request agentenv.ToolRequest, updates agentenv.ToolUpdateSink) (agentenv.ToolExecution, error) {
	if e.execute != nil {
		return e.execute(ctx, request, updates)
	}
	return e.Environment.ExecuteTool(ctx, request, updates)
}

func TestEmbeddedRunnerWaitsForEnvironmentReadiness(t *testing.T) {
	for _, cancelDuringStartup := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%v", cancelDuringStartup), func(t *testing.T) {
			config := embeddedRunnerTestConfig(t)
			started, release := make(chan struct{}), make(chan struct{})
			var closed atomic.Bool
			config.EmbeddedRunner.ServiceOptions.EnvironmentFactory = func(cwd string, runtime *extensions.Runtime) agentenv.Environment {
				local := agentenv.NewLocalEnvironment(cwd, runtime)
				return &embeddedTestEnvironment{
					Environment: local,
					open: func(ctx context.Context, spec agentenv.RunSpec) (agentenv.Manifest, error) {
						close(started)
						select {
						case <-release:
							return local.Open(ctx, spec)
						case <-ctx.Done():
							return agentenv.Manifest{}, ctx.Err()
						}
					},
					close: func(ctx context.Context) error {
						closed.Store(true)
						return local.Close(ctx)
					},
				}
			}
			server, _, stop := startEmbeddedRunnerTestServer(t, config, "127.0.0.1:0")
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				require.FailNow(t, "environment probe did not start")
			}
			assert.False(t, server.EmbeddedRunnerStatus().Ready)
			assert.Empty(t, server.runnerRegistry.Runners(), "environment probe must complete before registration")
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/api/status", nil)
			request.Header.Set("Authorization", "Bearer web-secret")
			server.router.ServeHTTP(response, request)
			assert.Equal(t, http.StatusOK, response.Code)
			assert.Contains(t, response.Body.String(), `"apiReady":true`)
			if !cancelDuringStartup {
				close(release)
				require.Eventually(t, func() bool { return server.EmbeddedRunnerStatus().Ready }, 5*time.Second, 10*time.Millisecond)
			}
			stop()
			assert.True(t, closed.Load(), "startup probe resources must be closed, including on cancellation")
			assert.Empty(t, server.EmbeddedRunnerStatus().Error, "shutdown cancellation is not a startup failure")
			held, err := config.EmbeddedRunner.Store.WorkspaceLockHeld(config.EmbeddedRunner.Workspace)
			require.NoError(t, err)
			assert.False(t, held)
		})
	}
}

func TestEmbeddedRunnerStartupFailureReleasesLock(t *testing.T) {
	config := embeddedRunnerTestConfig(t)
	var closed atomic.Bool
	config.EmbeddedRunner.ServiceOptions.EnvironmentFactory = func(cwd string, runtime *extensions.Runtime) agentenv.Environment {
		local := agentenv.NewLocalEnvironment(cwd, runtime)
		return &embeddedTestEnvironment{
			Environment: local,
			open: func(context.Context, agentenv.RunSpec) (agentenv.Manifest, error) {
				return agentenv.Manifest{}, errors.New("environment initialization failed")
			},
			close: func(ctx context.Context) error {
				closed.Store(true)
				return local.Close(ctx)
			},
		}
	}
	server, _, _ := startEmbeddedRunnerTestServer(t, config, "127.0.0.1:0")
	require.Eventually(t, func() bool { return server.EmbeddedRunnerStatus().Error != "" }, 5*time.Second, 10*time.Millisecond)
	assert.Contains(t, server.EmbeddedRunnerStatus().Error, "environment initialization failed")
	assert.False(t, server.EmbeddedRunnerStatus().Ready)
	assert.Empty(t, server.runnerRegistry.Runners())
	assert.True(t, closed.Load())
	held, err := config.EmbeddedRunner.Store.WorkspaceLockHeld(config.EmbeddedRunner.Workspace)
	require.NoError(t, err)
	assert.False(t, held)
}

func TestEmbeddedRunnerConcurrentCWDConfigIsolation(t *testing.T) {
	config := embeddedRunnerTestConfig(t)
	config.EmbeddedRunner.Settings["allowed_tools"] = []string{"bash", "file_read"}
	config.EmbeddedRunner.Settings["sysprompt_args"] = map[string]any{"origin": "runner"}
	config.EmbeddedRunner.Settings["environment_profiles"] = map[string]any{
		"review": map[string]any{"sysprompt_args": map[string]any{"origin": "profile"}},
	}
	processCWD, err := os.Getwd()
	require.NoError(t, err)
	directories := []string{t.TempDir(), t.TempDir()}
	for i, cwd := range directories {
		require.NoError(t, os.WriteFile(filepath.Join(cwd, "AGENTS.md"), fmt.Appendf(nil, "Project %d context", i), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(cwd, "kodelet-config.yaml"), []byte("sysprompt_args:\n  origin: workspace\n  directory: "+cwd+"\n"), 0o600))
	}
	server, _, _ := startEmbeddedRunnerTestServer(t, config, "127.0.0.1:0")
	require.Eventually(t, func() bool { return server.EmbeddedRunnerStatus().Ready }, 5*time.Second, 10*time.Millisecond)
	runnerID := server.EmbeddedRunnerStatus().RunnerID
	manifests := make([]runnerpayload.Manifest, len(directories))
	var wg sync.WaitGroup
	for i, cwd := range directories {
		wg.Go(func() {
			profile := ""
			if i == 1 {
				profile = "review"
			}
			manifest, err := server.runnerRegistry.OpenRun(t.Context(), runnerID, protocol.RunOpenParams{
				RunID: fmt.Sprintf("run-%d", i), ConversationID: fmt.Sprintf("conversation-%d", i), CWD: cwd,
				Agent: protocol.AgentDescriptor{EnvironmentProfile: profile},
			})
			if !assert.NoError(t, err) {
				return
			}
			manifests[i] = manifest
			assert.Equal(t, cwd, manifest.WorkingDirectory)
			assert.Equal(t, cwd, manifest.Config.SystemPromptArgs["directory"])
			contexts := make(map[string]string)
			for _, file := range manifest.ContextFiles {
				contexts[file.Path] = file.Content
			}
			assert.Equal(t, fmt.Sprintf("Project %d context", i), contexts[filepath.Join(cwd, "AGENTS.md")])
			assert.NotContains(t, contexts, filepath.Join(directories[1-i], "AGENTS.md"))
			result, err := server.runnerRegistry.ExecuteTool(t.Context(), runnerpayload.ToolExecuteParams{
				RunID: manifest.RunID, ToolCallID: "pwd", Name: "bash",
				Input: json.RawMessage(`{"command":"pwd","description":"Check effective execution working directory","timeout":10}`),
			}, nil)
			if assert.NoError(t, err) {
				assert.Empty(t, result.Result.Error)
				assert.Contains(t, result.Result.AssistantFacing, cwd)
			}
		})
	}
	wg.Wait()
	assert.Equal(t, "workspace", manifests[0].Config.SystemPromptArgs["origin"])
	assert.Equal(t, "profile", manifests[1].Config.SystemPromptArgs["origin"])
	assert.True(t, server.EmbeddedRunnerStatus().Ready, "a concurrent runner remains ready while busy")
	// YAML changes affect a later run, never an already-pinned manifest.
	require.NoError(t, os.WriteFile(filepath.Join(directories[0], "kodelet-config.yaml"), []byte("sysprompt_args:\n  origin: changed\n"), 0o600))
	for _, manifest := range manifests {
		require.NoError(t, server.runnerRegistry.CloseRun(t.Context(), manifest.RunID, runnerregistry.RunStatusSucceeded, nil))
	}
	later, err := server.runnerRegistry.OpenRun(t.Context(), runnerID, protocol.RunOpenParams{RunID: "later", ConversationID: "later", CWD: directories[0]})
	require.NoError(t, err)
	assert.Equal(t, "changed", later.Config.SystemPromptArgs["origin"])
	assert.Equal(t, "workspace", manifests[0].Config.SystemPromptArgs["origin"])
	require.NoError(t, server.runnerRegistry.CloseRun(t.Context(), later.RunID, runnerregistry.RunStatusSucceeded, nil))
	currentCWD, err := os.Getwd()
	require.NoError(t, err)
	assert.Equal(t, processCWD, currentCWD)
}

func TestEmbeddedRunnerShutdownDrainsBeforeDisconnect(t *testing.T) {
	config := embeddedRunnerTestConfig(t)
	config.EmbeddedRunner.Settings["allowed_tools"] = []string{"file_read"}
	closed := make(chan struct{})
	toolStarted, toolCanceled := make(chan struct{}), make(chan struct{})
	config.EmbeddedRunner.ServiceOptions.EnvironmentFactory = func(cwd string, runtime *extensions.Runtime) agentenv.Environment {
		local := agentenv.NewLocalEnvironment(cwd, runtime)
		var conversationID string
		return &embeddedTestEnvironment{
			Environment: local,
			open: func(ctx context.Context, spec agentenv.RunSpec) (agentenv.Manifest, error) {
				conversationID = spec.ConversationID
				return local.Open(ctx, spec)
			},
			close: func(ctx context.Context) error {
				if conversationID == "shutdown-conversation" {
					close(closed)
				}
				return local.Close(ctx)
			},
			execute: func(ctx context.Context, _ agentenv.ToolRequest, _ agentenv.ToolUpdateSink) (agentenv.ToolExecution, error) {
				close(toolStarted)
				<-ctx.Done()
				close(toolCanceled)
				return agentenv.ToolExecution{}, ctx.Err()
			},
		}
	}
	server, _, stop := startEmbeddedRunnerTestServer(t, config, "127.0.0.1:0")
	require.Eventually(t, func() bool { return server.EmbeddedRunnerStatus().Ready }, 5*time.Second, 10*time.Millisecond)
	runnerID := server.EmbeddedRunnerStatus().RunnerID
	// Mirror the agent-owned lease lifetime without involving a provider.
	runCtx, cancel := context.WithCancel(server.runCtx)
	t.Cleanup(cancel)
	_, err := server.runnerRegistry.OpenRun(runCtx, runnerID, protocol.RunOpenParams{RunID: "shutdown-run", ConversationID: "shutdown-conversation"})
	require.NoError(t, err)
	toolDone := make(chan error, 1)
	go func() {
		_, err := server.runnerRegistry.ExecuteTool(t.Context(), runnerpayload.ToolExecuteParams{
			RunID: "shutdown-run", ToolCallID: "blocked-tool", Name: "file_read", Input: json.RawMessage(`{"path":"AGENTS.md"}`),
		}, nil)
		toolDone <- err
	}()
	select {
	case <-toolStarted:
	case <-time.After(3 * time.Second):
		require.FailNow(t, "runner tool did not start")
	}
	run := &activeChatRun{cancel: cancel, done: make(chan struct{})}
	require.True(t, server.registerActiveChat("shutdown-conversation", run))
	go func() {
		defer close(run.done)
		<-runCtx.Done()
		assert.False(t, server.EmbeddedRunnerStatus().Ready, "shutdown must withdraw readiness immediately")
		runner, found := server.runnerRegistry.Runner(runnerID)
		assert.True(t, found)
		assert.True(t, runner.Connected, "execution cleanup must run before runner transport closes")
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cleanupCancel()
		assert.NoError(t, server.runnerRegistry.CancelRun(cleanupCtx, "shutdown-run", "daemon shutdown"))
		assert.NoError(t, server.runnerRegistry.CloseRun(cleanupCtx, "shutdown-run", runnerregistry.RunStatusCanceled, context.Canceled))
	}()
	stop()
	select {
	case err := <-toolDone:
		assert.Error(t, err, "active tool must report cancellation, not success")
	case <-time.After(time.Second):
		assert.Fail(t, "tool RPC did not finish during shutdown")
	}
	select {
	case <-toolCanceled:
	default:
		assert.Fail(t, "cancellation did not reach the runner tool")
	}
	select {
	case <-closed:
	default:
		assert.Fail(t, "runner environment was not closed before shutdown completed")
	}
	assert.False(t, server.registerActiveChat("late-conversation", &activeChatRun{}), "shutdown must reject new admission")
	held, err := config.EmbeddedRunner.Store.WorkspaceLockHeld(config.EmbeddedRunner.Workspace)
	require.NoError(t, err)
	assert.False(t, held)
}

func TestEmbeddedRunnerBoundedStartupCleanup(t *testing.T) {
	config := embeddedRunnerTestConfig(t)
	config.EmbeddedRunner.ServiceOptions.CleanupTimeout = 30 * time.Millisecond
	release, cleanupDone := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		close(release)
		select {
		case <-cleanupDone:
		case <-time.After(time.Second):
			assert.Fail(t, "test cleanup did not finish")
		}
	})
	config.EmbeddedRunner.ServiceOptions.EnvironmentFactory = func(cwd string, runtime *extensions.Runtime) agentenv.Environment {
		local := agentenv.NewLocalEnvironment(cwd, runtime)
		return &embeddedTestEnvironment{Environment: local, close: func(ctx context.Context) error {
			defer close(cleanupDone)
			<-release // Deliberately ignore cancellation to verify bounded cleanup.
			return local.Close(ctx)
		}}
	}
	server, _, stop := startEmbeddedRunnerTestServer(t, config, "127.0.0.1:0")
	require.Eventually(t, func() bool { return server.EmbeddedRunnerStatus().Error != "" }, 3*time.Second, 10*time.Millisecond)
	assert.Contains(t, server.EmbeddedRunnerStatus().Error, "timed out")
	assert.False(t, server.EmbeddedRunnerStatus().Ready)
	stop()
	held, err := config.EmbeddedRunner.Store.WorkspaceLockHeld(config.EmbeddedRunner.Workspace)
	require.NoError(t, err)
	assert.False(t, held, "bounded cleanup must release workspace ownership")
}

func TestEmbeddedRunnerReadinessRequiresHealthyHeartbeat(t *testing.T) {
	server := newRunnerTestServer(t, "")
	server.config.EmbeddedRunner = &EmbeddedRunnerConfig{Workspace: t.TempDir()}
	registration, err := server.runnerRegistry.Register(protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version},
		Host:             protocol.Host{InstanceID: "host-one", Hostname: "worker", OS: "linux", Arch: "amd64"},
		Workspace:        protocol.Workspace{Path: server.config.EmbeddedRunner.Workspace, Name: "workspace"},
	}, newRunnerAPITestLink())
	require.NoError(t, err)
	server.embeddedStatus.RunnerID = registration.RunnerID
	assert.False(t, server.EmbeddedRunnerStatus().Ready, "registration alone is not readiness")
	for _, state := range []protocol.RunnerState{protocol.RunnerStateError, protocol.RunnerStateIdle} {
		require.NoError(t, server.runnerRegistry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.HeartbeatParams{
			RunnerID: registration.RunnerID, Generation: registration.Generation, State: state,
		}))
		assert.Equal(t, state == protocol.RunnerStateIdle, server.EmbeddedRunnerStatus().Ready)
	}
	server.runnerRegistry.Detach(registration.RunnerID, registration.ConnectionID, registration.Generation, errors.New("connection lost"))
	status := server.EmbeddedRunnerStatus()
	assert.False(t, status.Ready)
	assert.Equal(t, registration.RunnerID, status.RunnerID, "offline default identity must remain pinned")
}

func TestEmbeddedRunnerShutdownBoundsExecutionDrain(t *testing.T) {
	config := embeddedRunnerTestConfig(t)
	server, err := NewServer(t.Context(), config, testFrontendHandler())
	require.NoError(t, err)
	server.shutdownTimeout = 50 * time.Millisecond
	t.Cleanup(func() { assert.NoError(t, server.Close()) })
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, listener) }()
	require.Eventually(t, func() bool { return server.EmbeddedRunnerStatus().Ready }, 5*time.Second, 10*time.Millisecond)
	var canceled atomic.Bool
	// An uncooperative execution never acknowledges cancellation.
	require.True(t, server.registerActiveChat("stuck", &activeChatRun{cancel: func() { canceled.Store(true) }, done: make(chan struct{})}))
	cancel()
	select {
	case err := <-done:
		require.ErrorContains(t, err, "active work did not finish stopping")
	case <-time.After(3 * time.Second):
		require.FailNow(t, "daemon shutdown exceeded its bound")
	}
	assert.True(t, canceled.Load())
	assert.False(t, server.EmbeddedRunnerStatus().Ready)
	require.Eventually(t, func() bool {
		held, err := config.EmbeddedRunner.Store.WorkspaceLockHeld(config.EmbeddedRunner.Workspace)
		return err == nil && !held
	}, time.Second, 10*time.Millisecond)
}

type embeddedSelectionResolver struct {
	requests []chat.ChatRequest
}

func (r *embeddedSelectionResolver) ResolveEnvironment(_ context.Context, request chat.ChatRequest, _ string, _ llmtypes.Config, _ string) (agentenv.Environment, error) {
	r.requests = append(r.requests, request)
	return nil, errors.New("selection captured before provider execution")
}

func TestEmbeddedRunnerDefaultSelectionPrecedence(t *testing.T) {
	for _, scenario := range []string{"default", "explicit", "affinity", "unavailable", "explicit while default offline", "affinity while default offline"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("KODELET_BASE_PATH", t.TempDir())
			require.NoError(t, db.RunMigrations(t.Context(), migrations.All()))
			server := newRunnerTestServer(t, "")
			server.config.EmbeddedRunner = &EmbeddedRunnerConfig{}
			registrations := make([]protocol.RegisterResult, 0, 2)
			for _, host := range []string{"embedded", "external"} {
				registration, err := server.runnerRegistry.Register(protocol.RegisterParams{
					ProtocolVersions: []int{protocol.Version}, Host: protocol.Host{InstanceID: host, Hostname: host, OS: "linux", Arch: "amd64"},
					Workspace: protocol.Workspace{Path: "/work/" + host, Name: host},
				}, newRunnerAPITestLink())
				require.NoError(t, err)
				require.NoError(t, server.runnerRegistry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.HeartbeatParams{
					RunnerID: registration.RunnerID, Generation: registration.Generation, State: protocol.RunnerStateIdle,
				}))
				registrations = append(registrations, registration)
			}
			server.embeddedStatus.RunnerID = registrations[0].RunnerID
			request := chat.ChatRequest{Message: "hello", CWD: "/requested/runner/directory"}
			wantRunner := registrations[0].RunnerID
			if strings.HasPrefix(scenario, "explicit") {
				request.RunnerID = registrations[1].RunnerID
				wantRunner = request.RunnerID
			}
			if strings.HasPrefix(scenario, "affinity") {
				request.ConversationID = "existing"
				require.NoError(t, server.runnerRegistry.BindConversationWithEnvironmentProfile(t.Context(), "existing", registrations[1].RunnerID, "gpu"))
				wantRunner = registrations[1].RunnerID
			}
			if scenario == "unavailable" || strings.HasSuffix(scenario, "offline") {
				registration := registrations[0]
				server.runnerRegistry.Detach(registration.RunnerID, registration.ConnectionID, registration.Generation, errors.New("offline"))
			}
			resolver := &embeddedSelectionResolver{}
			defaultRunner := NewExecutor("")
			defaultRunner.SetEnvironmentResolver(resolver)
			t.Cleanup(func() { assert.NoError(t, defaultRunner.Close()) })
			_, err := (&serverChatRunner{server: server, runner: defaultRunner}).Run(t.Context(), request, &recordingChatSink{})
			if scenario == "unavailable" {
				require.ErrorContains(t, err, "default runner is unavailable")
				assert.Empty(t, resolver.requests, "never silently select the available external runner")
				return
			}
			require.ErrorContains(t, err, "selection captured before provider execution")
			require.Len(t, resolver.requests, 1)
			assert.Equal(t, wantRunner, resolver.requests[0].RunnerID)
			assert.Equal(t, request.CWD, resolver.requests[0].CWD)
			if request.ConversationID != "" {
				assert.Equal(t, "gpu", resolver.requests[0].EnvironmentProfile)
			}
		})
	}
}

type embeddedCrashState struct {
	Address    string `json:"address"`
	RunnerID   string `json:"runnerId"`
	Generation int64  `json:"generation"`
}

func TestEmbeddedRunnerForcedCrashRestoresLostRunWithoutReplay(t *testing.T) {
	config := embeddedRunnerTestConfig(t)
	workspace := config.EmbeddedRunner.Workspace
	executable, err := os.Executable()
	require.NoError(t, err)
	logFile, err := os.Create(filepath.Join(t.TempDir(), "crash-process.log"))
	require.NoError(t, err)
	defer logFile.Close()
	process := exec.CommandContext(t.Context(), executable, "-test.run=^TestEmbeddedRunnerCrashProcess$", "-test.timeout=60s")
	process.Env = append(os.Environ(), "KODELET_EMBEDDED_CRASH_WORKSPACE="+workspace, "KODELET_EMBEDDED_CRASH_STORE="+config.EmbeddedRunner.Store.Root())
	process.Stdout, process.Stderr = logFile, logFile
	require.NoError(t, process.Start())
	var waited bool
	t.Cleanup(func() {
		if !waited {
			_ = process.Process.Kill()
			_ = process.Wait()
		}
		if t.Failed() {
			data, _ := os.ReadFile(logFile.Name())
			if len(data) > 4096 {
				data = data[len(data)-4096:]
			}
			t.Logf("crash helper output: %s", data)
		}
	})
	var crashed embeddedCrashState
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(filepath.Join(workspace, "crash-ready.json"))
		return err == nil && json.Unmarshal(data, &crashed) == nil && crashed.RunnerID != ""
	}, 10*time.Second, 20*time.Millisecond)
	held, err := config.EmbeddedRunner.Store.WorkspaceLockHeld(workspace)
	require.NoError(t, err)
	require.True(t, held)
	require.NoError(t, process.Process.Kill()) // No deferred Close, drain, or final database write.
	require.Error(t, process.Wait())
	waited = true
	held, err = config.EmbeddedRunner.Store.WorkspaceLockHeld(workspace)
	require.NoError(t, err)
	assert.False(t, held, "OS ownership lock must release even after SIGKILL")
	server, _, stop := startEmbeddedRunnerTestServer(t, config, crashed.Address)
	require.Eventually(t, func() bool { return server.EmbeddedRunnerStatus().Ready }, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, crashed.RunnerID, server.EmbeddedRunnerStatus().RunnerID)
	runner, found := server.runnerRegistry.Runner(crashed.RunnerID)
	require.True(t, found)
	assert.Greater(t, runner.Generation, crashed.Generation)
	run, found := server.runnerRegistry.Run("crash-run")
	require.True(t, found)
	assert.Equal(t, runnerregistry.RunStatusLost, run.Status)
	assert.Contains(t, run.Error, "server restarted")
	affinity, found, err := server.runnerRegistry.ResolveConversationAffinity(t.Context(), "crash-conversation")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, crashed.RunnerID, affinity.RunnerID)
	_, err = server.runnerRegistry.ExecuteTool(t.Context(), runnerpayload.ToolExecuteParams{RunID: "crash-run", ToolCallID: "replay", Name: "file_write", Input: json.RawMessage(`{}`)}, nil)
	require.Error(t, err, "a lost run must not dispatch uncertain work again")
	data, err := os.ReadFile(filepath.Join(workspace, "side-effects.log"))
	require.NoError(t, err)
	assert.Equal(t, "effect\n", string(data), "startup must not replay the interrupted tool")
	stop()
}

func TestEmbeddedRunnerCrashProcess(t *testing.T) {
	workspace := os.Getenv("KODELET_EMBEDDED_CRASH_WORKSPACE")
	if workspace == "" {
		return
	}
	store, err := localstate.NewStoreAt(os.Getenv("KODELET_EMBEDDED_CRASH_STORE"))
	require.NoError(t, err)
	toolStarted := make(chan struct{})
	config := &ServerConfig{
		Host: "127.0.0.1", Port: 0, CompactRatio: 0.8, WebAuthMode: WebAuthModeToken, AuthToken: "web-secret", RunnerAuthMode: RunnerAuthModeNone,
		EmbeddedRunner: &EmbeddedRunnerConfig{Workspace: workspace, Store: store, Settings: map[string]any{
			"extensions": map[string]any{"enabled": false}, "skills": map[string]any{"enabled": false}, "allowed_tools": []string{"file_write"},
		}},
	}
	config.EmbeddedRunner.ServiceOptions.EnvironmentFactory = func(cwd string, runtime *extensions.Runtime) agentenv.Environment {
		return &embeddedTestEnvironment{Environment: agentenv.NewLocalEnvironment(cwd, runtime), execute: func(ctx context.Context, _ agentenv.ToolRequest, _ agentenv.ToolUpdateSink) (agentenv.ToolExecution, error) {
			file, err := os.OpenFile(filepath.Join(cwd, "side-effects.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
			if err != nil {
				return agentenv.ToolExecution{}, err
			}
			_, writeErr := file.WriteString("effect\n")
			closeErr := file.Close()
			if writeErr != nil || closeErr != nil {
				return agentenv.ToolExecution{}, errors.New("failed to persist test side effect")
			}
			close(toolStarted)
			<-ctx.Done()
			return agentenv.ToolExecution{}, ctx.Err()
		}}
	}
	server, endpoint, _ := startEmbeddedRunnerTestServer(t, config, "127.0.0.1:0")
	require.Eventually(t, func() bool { return server.EmbeddedRunnerStatus().Ready }, 5*time.Second, 10*time.Millisecond)
	runnerID := server.EmbeddedRunnerStatus().RunnerID
	_, err = server.runnerRegistry.OpenRun(t.Context(), runnerID, protocol.RunOpenParams{RunID: "crash-run", ConversationID: "crash-conversation"})
	require.NoError(t, err)
	require.NoError(t, server.runnerRegistry.CommitConversationAffinity(t.Context(), "crash-conversation"))
	go func() {
		_, _ = server.runnerRegistry.ExecuteTool(t.Context(), runnerpayload.ToolExecuteParams{RunID: "crash-run", ToolCallID: "side-effect", Name: "file_write", Input: json.RawMessage(`{}`)}, nil)
	}()
	select {
	case <-toolStarted:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "crash test tool did not start")
	}
	runner, found := server.runnerRegistry.Runner(runnerID)
	require.True(t, found)
	data, err := json.Marshal(embeddedCrashState{Address: strings.TrimPrefix(endpoint, "http://"), RunnerID: runnerID, Generation: runner.Generation})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(workspace, "crash-ready.json"), data, 0o600))
	select {} // The parent deliberately kills this daemon without graceful cleanup.
}

func TestRunnerWebsocketRegistersAndDetachesRunner(t *testing.T) {
	server := newRunnerTestServer(t, "")
	httpServer := httptest.NewServer(http.HandlerFunc(server.handleRunnerWebsocket))
	t.Cleanup(httpServer.Close)

	peer := dialRunnerPeer(t, "ws"+strings.TrimPrefix(httpServer.URL, "http"), nil)
	var registration protocol.RegisterResult
	require.NoError(t, peer.Call(t.Context(), protocol.MethodRunnerRegister, protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version},
		Host: protocol.Host{
			InstanceID: "host-one",
			Hostname:   "runner-host",
			OS:         "linux",
			Arch:       "amd64",
		},
		Workspace:      protocol.Workspace{Path: "/work/project", Name: "project"},
		KodeletVersion: "test",
	}, &registration))
	assert.NotEmpty(t, registration.RunnerID)
	assert.Equal(t, protocol.Version, registration.ProtocolVersion)

	runner, ok := server.runnerRegistry.Runner(registration.RunnerID)
	require.True(t, ok)
	assert.True(t, runner.Connected)
	assert.Equal(t, "runner-host", runner.Host.Hostname)
	assert.Equal(t, "/work/project", runner.Workspace.Path)

	require.NoError(t, peer.Notify(t.Context(), protocol.MethodRunnerHeartbeat, protocol.HeartbeatParams{
		RunnerID:       registration.RunnerID,
		Generation:     registration.Generation,
		State:          protocol.RunnerStateIdle,
		ManifestDigest: "sha256:test",
	}))
	require.Eventually(t, func() bool {
		runner, ok := server.runnerRegistry.Runner(registration.RunnerID)
		return ok && runner.ManifestDigest == "sha256:test"
	}, time.Second, 10*time.Millisecond)

	require.NoError(t, peer.Close())
	require.Eventually(t, func() bool {
		runner, ok := server.runnerRegistry.Runner(registration.RunnerID)
		return ok && !runner.Connected && runner.Status == runnerregistry.RunnerStatusOffline
	}, time.Second, 10*time.Millisecond)
}

func TestRunnerWebsocketRequiresSubprotocol(t *testing.T) {
	server := newRunnerTestServer(t, "")
	httpServer := httptest.NewServer(http.HandlerFunc(server.handleRunnerWebsocket))
	t.Cleanup(httpServer.Close)

	_, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(httpServer.URL, "http"), nil)
	require.Error(t, err)
	require.NotNil(t, response)
	assert.Equal(t, http.StatusBadRequest, response.StatusCode)
	require.NoError(t, response.Body.Close())
}

func TestRunnerWebsocketUsesServerAuthentication(t *testing.T) {
	server := newRunnerTestServer(t, "secret-token")
	server.router = mux.NewRouter()
	server.setupRoutes()
	httpServer := httptest.NewServer(server.router)
	t.Cleanup(httpServer.Close)
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + protocol.Endpoint

	dialer := websocket.Dialer{Subprotocols: []string{protocol.Subprotocol}}
	_, response, err := dialer.Dial(wsURL, nil)
	require.Error(t, err)
	require.NotNil(t, response)
	assert.Equal(t, http.StatusUnauthorized, response.StatusCode)
	require.NoError(t, response.Body.Close())

	headers := http.Header{"Authorization": []string{"Bearer secret-token-runner"}}
	peer := dialRunnerPeer(t, wsURL, headers)
	require.NoError(t, peer.Close())

	webHeaders := http.Header{"Authorization": []string{"Bearer secret-token"}}
	_, response, err = dialer.Dial(wsURL, webHeaders)
	require.Error(t, err)
	require.NotNil(t, response)
	assert.Equal(t, http.StatusUnauthorized, response.StatusCode)
	require.NoError(t, response.Body.Close())
}

func TestRunnerRESTEndpointsExposeRegisteredStatus(t *testing.T) {
	server := newRunnerTestServer(t, "")
	link := newRunnerAPITestLink()
	registration, err := server.runnerRegistry.Register(protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version},
		DisplayName:      "project-runner",
		Host: protocol.Host{
			InstanceID: "host-one",
			Hostname:   "worker-one",
			OS:         "linux",
			Arch:       "amd64",
			PID:        1234,
		},
		Workspace: protocol.Workspace{Path: "/work/project", Name: "project"},
	}, link)
	require.NoError(t, err)
	require.NoError(t, server.runnerRegistry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.HeartbeatParams{
		RunnerID:       registration.RunnerID,
		Generation:     registration.Generation,
		State:          protocol.RunnerStateIdle,
		ManifestDigest: "sha256:manifest",
	}))

	listRecorder := httptest.NewRecorder()
	server.handleListRunners(listRecorder, httptest.NewRequest(http.MethodGet, "/api/runners", nil))
	require.Equal(t, http.StatusOK, listRecorder.Code)
	var list runnerListResponse
	require.NoError(t, json.Unmarshal(listRecorder.Body.Bytes(), &list))
	require.Len(t, list.Runners, 1)
	assert.Equal(t, registration.RunnerID, list.Runners[0].ID)
	assert.Equal(t, runnerregistry.RunnerStatusIdle, list.Runners[0].Status)
	assert.Equal(t, "worker-one", list.Runners[0].Host.Hostname)
	assert.Equal(t, 1234, list.Runners[0].Host.PID)

	getRecorder := httptest.NewRecorder()
	getRequest := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/api/runners/"+registration.RunnerID, nil), map[string]string{"id": registration.RunnerID})
	server.handleGetRunner(getRecorder, getRequest)
	require.Equal(t, http.StatusOK, getRecorder.Code)
	var runner runnerregistry.Runner
	require.NoError(t, json.Unmarshal(getRecorder.Body.Bytes(), &runner))
	assert.Equal(t, "sha256:manifest", runner.ManifestDigest)
	assert.True(t, runner.Connected)

	missingRecorder := httptest.NewRecorder()
	missingRequest := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/api/runners/missing", nil), map[string]string{"id": "missing"})
	server.handleGetRunner(missingRecorder, missingRequest)
	assert.Equal(t, http.StatusNotFound, missingRecorder.Code)
}

func TestRemoteWorkspaceGitDiffUsesConversationRunner(t *testing.T) {
	server := newRunnerTestServer(t, "")
	server.conversationService = &mockConversationService{getFunc: func(context.Context, string) (*conversations.GetConversationResponse, error) {
		return &conversations.GetConversationResponse{ID: "conversation-git", CWD: "/runner/selected"}, nil
	}}
	link := newRunnerAPITestLink()
	link.call = func(_ context.Context, method string, params any, result any) error {
		assert.Equal(t, protocol.MethodWorkspaceGitDiff, method)
		assert.Equal(t, protocol.WorkspaceGitDiffParams{CWD: "/runner/selected"}, params)
		output := result.(*protocol.WorkspaceGitDiffResult)
		*output = protocol.WorkspaceGitDiffResult{
			CWD:      "/runner/selected",
			GitRoot:  "/runner/selected",
			Diff:     "diff --git a/file.txt b/file.txt\n",
			HasDiff:  true,
			ExitCode: 0,
		}
		return nil
	}
	registration, err := server.runnerRegistry.Register(protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version},
		Capabilities: protocol.RunnerCapabilities{
			WorkspaceGitDiff: true,
			WorkspaceCWD:     true,
		},
		Host:      protocol.Host{InstanceID: "host-git", Hostname: "worker", OS: "linux", Arch: "amd64"},
		Workspace: protocol.Workspace{Path: "/runner/project", Name: "project"},
	}, link)
	require.NoError(t, err)
	require.NoError(t, server.runnerRegistry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.HeartbeatParams{
		RunnerID:   registration.RunnerID,
		Generation: registration.Generation,
		State:      protocol.RunnerStateIdle,
	}))
	require.NoError(t, server.runnerRegistry.BindConversation(t.Context(), "conversation-git", registration.RunnerID))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/git/diff?conversationId=conversation-git&runnerId="+registration.RunnerID, nil)
	server.handleGetGitDiff(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response gitDiffResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, "/runner/selected", response.CWD)
	assert.True(t, response.HasDiff)
	assert.Contains(t, response.Diff, "diff --git")
}

func TestRemoteWorkspaceTargetRejectsUnreservedConversation(t *testing.T) {
	server := newRunnerTestServer(t, "")
	link := newRunnerAPITestLink()
	called := false
	link.call = func(context.Context, string, any, any) error {
		called = true
		return nil
	}
	registration, err := server.runnerRegistry.Register(protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version},
		Capabilities:     protocol.RunnerCapabilities{WorkspaceGitDiff: true},
		Host:             protocol.Host{InstanceID: "host-preallocated-target", Hostname: "worker", OS: "linux", Arch: "amd64"},
		Workspace:        protocol.Workspace{Path: "/runner/project", Name: "project"},
	}, link)
	require.NoError(t, err)
	require.NoError(t, server.runnerRegistry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.HeartbeatParams{
		RunnerID:   registration.RunnerID,
		Generation: registration.Generation,
		State:      protocol.RunnerStateIdle,
	}))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/git/diff?conversationId=preallocated-conversation&runnerId="+registration.RunnerID, nil)
	server.handleGetGitDiff(recorder, request)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.False(t, called)
}

func TestRunnerDiscoveryRoutesDirectoryAndProfileWithoutLocalWorkspace(t *testing.T) {
	for _, method := range []string{protocol.MethodWorkspaceDiscover, protocol.MethodWorkspaceCWDHints} {
		t.Run(method, func(t *testing.T) {
			server := newRunnerTestServer(t, "")
			server.conversationService = &mockConversationService{getFunc: func(context.Context, string) (*conversations.GetConversationResponse, error) {
				return &conversations.GetConversationResponse{ID: "conversation-discovery", CWD: "/runner/selected"}, nil
			}}
			link := newRunnerAPITestLink()
			calls := 0
			var expectedOptions *llmtypes.ExecutionOptions
			link.call = func(ctx context.Context, gotMethod string, params, result any) error {
				calls++
				assert.Equal(t, method, gotMethod)
				_, bounded := ctx.Deadline()
				assert.True(t, bounded)
				if method == protocol.MethodWorkspaceDiscover {
					assert.Equal(t, protocol.WorkspaceDiscoverParams{CWD: "/runner/selected", EnvironmentProfile: "review", Options: expectedOptions}, params)
					*result.(*protocol.WorkspaceDiscoverResult) = protocol.WorkspaceDiscoverResult{CWD: "/runner/selected", EnvironmentProfile: "review", Digest: "sha256:selected"}
				} else {
					assert.Equal(t, protocol.WorkspaceCWDHintsParams{CWD: "/runner/selected", EnvironmentProfile: "review", Query: "project"}, params)
					*result.(*protocol.WorkspaceCWDHintsResult) = protocol.WorkspaceCWDHintsResult{Hints: []protocol.DirectoryHint{{Path: "/runner/selected/project"}}}
				}
				return nil
			}
			registration, err := server.runnerRegistry.Register(protocol.RegisterParams{
				ProtocolVersions: []int{protocol.Version},
				Capabilities:     protocol.RunnerCapabilities{WorkspaceDiscovery: true},
				Host:             protocol.Host{InstanceID: "host-discovery", Hostname: "worker", OS: "linux", Arch: "amd64"},
				Workspace:        protocol.Workspace{Path: "/runner/startup", Name: "startup"},
			}, link)
			require.NoError(t, err)
			require.NoError(t, server.runnerRegistry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.HeartbeatParams{RunnerID: registration.RunnerID, Generation: registration.Generation, State: protocol.RunnerStateIdle}))
			require.NoError(t, server.runnerRegistry.BindConversationWithEnvironmentProfile(t.Context(), "conversation-discovery", registration.RunnerID, "review"))
			for _, query := range []string{
				"runnerId=" + registration.RunnerID + "&cwd=/runner/selected&environmentProfile=review",
				"conversationId=conversation-discovery",
			} {
				recorder := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodGet, "/api/chat/discovery?"+query+"&q=project", nil)
				if method == protocol.MethodWorkspaceDiscover {
					server.handleGetSlashCommands(recorder, request)
				} else {
					server.handleGetCWDHints(recorder, request)
				}
				require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
				assert.Contains(t, recorder.Body.String(), "/runner/selected")
			}
			assert.Equal(t, 2, calls)
			if method == protocol.MethodWorkspaceDiscover {
				expectedOptions = &llmtypes.ExecutionOptions{NoExtensions: new(true), NoSkills: new(true), AllowedTools: &[]string{}}
				recorder := httptest.NewRecorder()
				query := url.Values{"conversationId": {"conversation-discovery"}, "options": {`{"noExtensions":true,"noSkills":true,"allowedTools":[]}`}}
				server.handleGetSlashCommands(recorder, httptest.NewRequest(http.MethodGet, "/api/chat/discovery?"+query.Encode(), nil))
				require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
				assert.Equal(t, 3, calls)
			}
			previousCalls := calls
			for _, options := range [][]string{{"null"}, {`{"noExtensions":null}`}, {`{"model":"gpt-4.1"}`}, {`{"maxTurns":2}`}, {`{"unknown":true}`}, {"{}", "{}"}, {strings.Repeat(" ", 16*1024) + "{}"}} {
				query := url.Values{"runnerId": {registration.RunnerID}, "options": options}
				recorder := httptest.NewRecorder()
				server.handleRunnerDiscovery(recorder, httptest.NewRequest(http.MethodGet, "/?"+query.Encode(), nil), method)
				assert.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
			}
			assert.Equal(t, previousCalls, calls, "invalid restrictions must not reach the runner")
		})
	}
}

func TestRunnerDiscoveryRoutesModelProfilesIndependentlyOfEnvironmentProfiles(t *testing.T) {
	snapshotMetadata := func(profile string) map[string]any {
		metadata, err := conversations.AddConfigSnapshot(map[string]any{"profile": "legacy-ignored"}, llmtypes.Config{Profile: profile, Provider: "openai", Model: "stored-model", ReasoningEffort: "medium"})
		require.NoError(t, err)
		return metadata
	}
	for _, method := range []string{protocol.MethodWorkspaceDiscover, protocol.MethodWorkspaceCWDHints} {
		t.Run(method, func(t *testing.T) {
			server := newRunnerTestServer(t, "")
			var metadata map[string]any
			server.conversationService = &mockConversationService{getFunc: func(context.Context, string) (*conversations.GetConversationResponse, error) {
				return &conversations.GetConversationResponse{ID: "saved", CWD: "/runner/selected", Metadata: metadata}, nil
			}}
			link := newRunnerAPITestLink()
			registration, err := server.runnerRegistry.Register(protocol.RegisterParams{
				ProtocolVersions: []int{protocol.Version}, Capabilities: protocol.RunnerCapabilities{WorkspaceDiscovery: true},
				Host: protocol.Host{InstanceID: "profile-host", Hostname: "worker", OS: "linux", Arch: "amd64"}, Workspace: protocol.Workspace{Path: "/runner/startup", Name: "startup"},
			}, link)
			require.NoError(t, err)
			require.NoError(t, server.runnerRegistry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.HeartbeatParams{RunnerID: registration.RunnerID, Generation: registration.Generation, State: protocol.RunnerStateIdle}))
			require.NoError(t, server.runnerRegistry.BindConversationWithEnvironmentProfile(t.Context(), "saved", registration.RunnerID, "environment"))
			for _, test := range []struct {
				name     string
				profile  string
				metadata map[string]any
				want     string
				status   int
			}{
				{name: "daemon default"},
				{name: "explicit default", profile: "default", want: "default"},
				{name: "named", profile: " model-profile ", want: "model-profile"},
				{name: "default spelling", profile: " DEFAULT ", want: "default"},
				{name: "snapshot wins", metadata: snapshotMetadata("stored-profile"), want: "stored-profile"},
				{name: "matching snapshot", profile: "stored-profile", metadata: snapshotMetadata("stored-profile"), want: "stored-profile"},
				{name: "snapshot default", metadata: snapshotMetadata("default"), want: "default"},
				{name: "snapshot empty pins base", metadata: snapshotMetadata(""), want: "default"},
				{name: "legacy named", metadata: map[string]any{"profile": "legacy-profile"}, want: "legacy-profile"},
				{name: "legacy default", metadata: map[string]any{"profile": "default"}, want: "default"},
				{name: "legacy empty pins base", metadata: map[string]any{"profile": ""}, want: "default"},
				{name: "legacy missing inherits daemon default", metadata: map[string]any{}},
				{name: "conflicting named", profile: "other", metadata: snapshotMetadata("stored-profile"), status: http.StatusBadRequest},
				{name: "conflicting default", profile: "default", metadata: snapshotMetadata("stored-profile"), status: http.StatusBadRequest},
				{name: "conflicting base", profile: "other", metadata: snapshotMetadata(""), status: http.StatusBadRequest},
				{name: "conflicting legacy", profile: "other", metadata: map[string]any{"profile": "legacy-profile"}, status: http.StatusBadRequest},
				{name: "invalid snapshot", metadata: map[string]any{conversations.ConfigSnapshotMetadataKey: map[string]any{"version": 99}, "profile": "legacy"}, status: http.StatusInternalServerError},
			} {
				t.Run(test.name, func(t *testing.T) {
					metadata = test.metadata
					calls := 0
					link.call = func(_ context.Context, gotMethod string, params, _ any) error {
						calls++
						assert.Equal(t, method, gotMethod, "discovery must not open a run or submit a model turn")
						if method == protocol.MethodWorkspaceDiscover {
							assert.Equal(t, protocol.WorkspaceDiscoverParams{CWD: "/runner/selected", Profile: test.want, EnvironmentProfile: "environment"}, params)
						} else {
							assert.Equal(t, protocol.WorkspaceCWDHintsParams{CWD: "/runner/selected", Profile: test.want, EnvironmentProfile: "environment", Query: "project"}, params)
						}
						return nil
					}
					query := url.Values{"runnerId": {registration.RunnerID}, "cwd": {"/runner/selected"}, "environmentProfile": {"environment"}, "q": {"project"}}
					if metadata != nil {
						query = url.Values{"conversationId": {"saved"}, "q": {"project"}}
					}
					if test.profile != "" {
						query.Set("profile", test.profile)
					}
					recorder := httptest.NewRecorder()
					server.handleRunnerDiscovery(recorder, httptest.NewRequest(http.MethodGet, "/?"+query.Encode(), nil), method)
					if test.status != 0 {
						assert.Equal(t, test.status, recorder.Code, recorder.Body.String())
						assert.Zero(t, calls)
					} else {
						assert.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
						assert.Equal(t, 1, calls)
					}
				})
			}
		})
	}
}

func TestRunnerWorkspaceScopeRejectsUnsupportedAndMismatchedTargets(t *testing.T) {
	server := newRunnerTestServer(t, "")
	server.conversationService = &mockConversationService{getFunc: func(context.Context, string) (*conversations.GetConversationResponse, error) {
		return &conversations.GetConversationResponse{ID: "conversation-scope", CWD: "/runner/selected"}, nil
	}}
	link := newRunnerAPITestLink()
	link.call = func(context.Context, string, any, any) error {
		t.Error("invalid or unsupported targets must not call the runner")
		return nil
	}
	registration, err := server.runnerRegistry.Register(protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version},
		Capabilities:     protocol.RunnerCapabilities{WorkspaceGitDiff: true, WorkspaceTerminal: true},
		Host:             protocol.Host{InstanceID: "host-scope", Hostname: "worker", OS: "linux", Arch: "amd64"},
		Workspace:        protocol.Workspace{Path: "/runner/startup", Name: "startup"},
	}, link)
	require.NoError(t, err)
	require.NoError(t, server.runnerRegistry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.HeartbeatParams{RunnerID: registration.RunnerID, Generation: registration.Generation, State: protocol.RunnerStateIdle}))
	require.NoError(t, server.runnerRegistry.BindConversationWithEnvironmentProfile(t.Context(), "conversation-scope", registration.RunnerID, "review"))
	for _, test := range []struct {
		name    string
		query   string
		handler http.HandlerFunc
		status  int
	}{
		{"old runner diff", "conversationId=conversation-scope", server.handleGetGitDiff, http.StatusNotImplemented},
		{"old runner terminal", "conversationId=conversation-scope", server.handleTerminalWebsocket, http.StatusNotImplemented},
		{"old runner discovery", "runnerId=" + registration.RunnerID, server.handleGetSlashCommands, http.StatusNotImplemented},
		{"wrong directory", "conversationId=conversation-scope&cwd=/runner/startup", server.handleGetSlashCommands, http.StatusBadRequest},
		{"wrong profile", "conversationId=conversation-scope&environmentProfile=default", server.handleGetSlashCommands, http.StatusBadRequest},
		{"wrong runner", "conversationId=conversation-scope&runnerId=other", server.handleGetSlashCommands, http.StatusBadRequest},
		{"runner-wide custom directory", "runnerId=" + registration.RunnerID + "&cwd=/runner/selected", server.handleGetGitDiff, http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			test.handler(recorder, httptest.NewRequest(http.MethodGet, "/?"+test.query, nil))
			assert.Equal(t, test.status, recorder.Code, recorder.Body.String())
		})
	}
}

func TestRemoteWorkspaceTargetRejectsConversationRunnerMismatch(t *testing.T) {
	server := newRunnerTestServer(t, "")
	firstLink := newRunnerAPITestLink()
	secondLink := newRunnerAPITestLink()
	called := false
	secondLink.call = func(context.Context, string, any, any) error {
		called = true
		return nil
	}
	register := func(instanceID, workspace string, link *runnerAPITestLink) protocol.RegisterResult {
		registration, err := server.runnerRegistry.Register(protocol.RegisterParams{
			ProtocolVersions: []int{protocol.Version},
			Capabilities:     protocol.RunnerCapabilities{WorkspaceGitDiff: true},
			Host:             protocol.Host{InstanceID: instanceID, Hostname: "worker", OS: "linux", Arch: "amd64"},
			Workspace:        protocol.Workspace{Path: workspace, Name: filepath.Base(workspace)},
		}, link)
		require.NoError(t, err)
		require.NoError(t, server.runnerRegistry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.HeartbeatParams{
			RunnerID:   registration.RunnerID,
			Generation: registration.Generation,
			State:      protocol.RunnerStateIdle,
		}))
		return registration
	}
	first := register("host-affinity-first", "/runner/first", firstLink)
	second := register("host-affinity-second", "/runner/second", secondLink)
	require.NoError(t, server.runnerRegistry.BindConversation(t.Context(), "conversation-affinity", first.RunnerID))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/git/diff?conversationId=conversation-affinity&runnerId="+second.RunnerID, nil)
	server.handleGetGitDiff(recorder, request)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.False(t, called)
}

func TestRemoteWorkspaceTerminalProxiesReplayAndExit(t *testing.T) {
	server := newRunnerTestServer(t, "")
	server.conversationService = &mockConversationService{getFunc: func(context.Context, string) (*conversations.GetConversationResponse, error) {
		return &conversations.GetConversationResponse{ID: "conversation-terminal", CWD: "/runner/selected"}, nil
	}}
	link := newRunnerAPITestLink()
	var readCount atomic.Int32
	inputReceived := make(chan struct{})
	link.call = func(_ context.Context, method string, params any, result any) error {
		switch method {
		case protocol.MethodWorkspaceTerminalOpen:
			assert.Equal(t, "/runner/selected", params.(protocol.WorkspaceTerminalOpenParams).CWD)
			output := result.(*protocol.WorkspaceTerminalOpenResult)
			*output = protocol.WorkspaceTerminalOpenResult{
				SessionID:    "terminal-1",
				CWD:          "/runner/selected",
				Name:         "bash",
				Git:          true,
				PID:          123,
				ReplayCursor: 0,
				WriteCursor:  5,
			}
		case protocol.MethodWorkspaceTerminalRead:
			currentRead := readCount.Add(1)
			output := result.(*protocol.WorkspaceTerminalReadResult)
			if currentRead == 1 {
				*output = protocol.WorkspaceTerminalReadResult{Data: []byte("hellolive"), NextCursor: 9}
			} else {
				<-inputReceived
				*output = protocol.WorkspaceTerminalReadResult{NextCursor: 9, Exited: true, ExitCode: 7}
			}
		case protocol.MethodWorkspaceTerminalInput:
			input := params.(protocol.WorkspaceTerminalInputParams)
			assert.Equal(t, "terminal-1", input.SessionID)
			assert.Equal(t, []byte("exit 7\n"), input.Data)
			close(inputReceived)
		default:
			return errors.Errorf("unexpected terminal method %s", method)
		}
		return nil
	}
	registration, err := server.runnerRegistry.Register(protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version},
		Capabilities: protocol.RunnerCapabilities{
			WorkspaceTerminal: true,
			WorkspaceCWD:      true,
		},
		Host:      protocol.Host{InstanceID: "host-terminal", Hostname: "worker", OS: "linux", Arch: "amd64"},
		Workspace: protocol.Workspace{Path: "/runner/project", Name: "project"},
	}, link)
	require.NoError(t, err)
	require.NoError(t, server.runnerRegistry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.HeartbeatParams{
		RunnerID:   registration.RunnerID,
		Generation: registration.Generation,
		State:      protocol.RunnerStateIdle,
	}))
	require.NoError(t, server.runnerRegistry.BindConversation(t.Context(), "conversation-terminal", registration.RunnerID))

	httpServer := httptest.NewServer(http.HandlerFunc(server.handleTerminalWebsocket))
	t.Cleanup(httpServer.Close)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(httpServer.URL, "http")+"?conversationId=conversation-terminal", nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	ready := readTerminalReady(t, conn)
	assert.Equal(t, "/runner/selected", ready.CWD)
	assert.Equal(t, "bash", ready.Name)
	messageType, payload, err := conn.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, websocket.BinaryMessage, messageType)
	assert.Equal(t, []byte("hello"), payload)

	messageType, payload, err = conn.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, websocket.TextMessage, messageType)
	var replayComplete terminalMessage
	require.NoError(t, json.Unmarshal(payload, &replayComplete))
	assert.Equal(t, "replay-complete", replayComplete.Type)

	messageType, payload, err = conn.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, websocket.BinaryMessage, messageType)
	assert.Equal(t, []byte("live"), payload)
	require.NoError(t, conn.WriteJSON(terminalMessage{Type: "input", Data: "exit 7\n"}))

	messageType, payload, err = conn.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, websocket.TextMessage, messageType)
	var exit terminalMessage
	require.NoError(t, json.Unmarshal(payload, &exit))
	assert.Equal(t, "exit", exit.Type)
	require.NotNil(t, exit.Code)
	assert.Equal(t, 7, *exit.Code)
}

func TestRemoteWorkspaceTerminalPersistsAcrossBrowserDetachAndReplaysOutput(t *testing.T) {
	server := newRunnerTestServer(t, "")
	link := newRunnerAPITestLink()
	var terminalMu sync.Mutex
	var terminalOutput []byte
	var openCount atomic.Int32
	readCanceled := make(chan struct{}, 2)
	link.call = func(ctx context.Context, method string, params any, result any) error {
		switch method {
		case protocol.MethodWorkspaceTerminalOpen:
			openCount.Add(1)
			terminalMu.Lock()
			writeCursor := uint64(len(terminalOutput))
			terminalMu.Unlock()
			output := result.(*protocol.WorkspaceTerminalOpenResult)
			*output = protocol.WorkspaceTerminalOpenResult{
				SessionID:    "terminal-persistent",
				CWD:          "/runner/project",
				Name:         "bash",
				ReplayCursor: 0,
				WriteCursor:  writeCursor,
			}
			return nil
		case protocol.MethodWorkspaceTerminalRead:
			readParams := params.(protocol.WorkspaceTerminalReadParams)
			assert.Equal(t, "terminal-persistent", readParams.SessionID)
			terminalMu.Lock()
			if readParams.Cursor < uint64(len(terminalOutput)) {
				data := append([]byte(nil), terminalOutput[readParams.Cursor:]...)
				nextCursor := uint64(len(terminalOutput))
				terminalMu.Unlock()
				output := result.(*protocol.WorkspaceTerminalReadResult)
				*output = protocol.WorkspaceTerminalReadResult{Data: data, NextCursor: nextCursor}
				return nil
			}
			terminalMu.Unlock()
			<-ctx.Done()
			select {
			case readCanceled <- struct{}{}:
			default:
			}
			return ctx.Err()
		default:
			return errors.Errorf("unexpected terminal method %s", method)
		}
	}
	registration, err := server.runnerRegistry.Register(protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version},
		Capabilities:     protocol.RunnerCapabilities{WorkspaceTerminal: true},
		Host:             protocol.Host{InstanceID: "host-terminal-cancel", Hostname: "worker", OS: "linux", Arch: "amd64"},
		Workspace:        protocol.Workspace{Path: "/runner/project", Name: "project"},
	}, link)
	require.NoError(t, err)
	require.NoError(t, server.runnerRegistry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.HeartbeatParams{
		RunnerID:   registration.RunnerID,
		Generation: registration.Generation,
		State:      protocol.RunnerStateIdle,
	}))

	httpServer := httptest.NewServer(http.HandlerFunc(server.handleTerminalWebsocket))
	t.Cleanup(httpServer.Close)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(httpServer.URL, "http")+"?runnerId="+registration.RunnerID, nil)
	require.NoError(t, err)
	readTerminalReady(t, conn)
	_, payload, err := conn.ReadMessage()
	require.NoError(t, err)
	var replayComplete terminalMessage
	require.NoError(t, json.Unmarshal(payload, &replayComplete))
	assert.Equal(t, "replay-complete", replayComplete.Type)
	require.NoError(t, conn.Close())

	select {
	case <-readCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("runner terminal read was not canceled after browser disconnect")
	}

	terminalMu.Lock()
	terminalOutput = append(terminalOutput, []byte("output while detached")...)
	terminalMu.Unlock()

	reconnected, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(httpServer.URL, "http")+"?runnerId="+registration.RunnerID, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = reconnected.Close() })
	ready := readTerminalReady(t, reconnected)
	assert.Equal(t, "/runner/project", ready.CWD)
	messageType, payload, err := reconnected.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, websocket.BinaryMessage, messageType)
	assert.Equal(t, []byte("output while detached"), payload)
	messageType, payload, err = reconnected.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, websocket.TextMessage, messageType)
	require.NoError(t, json.Unmarshal(payload, &replayComplete))
	assert.Equal(t, "replay-complete", replayComplete.Type)
	assert.Equal(t, int32(2), openCount.Load())
}

func TestRunnerRoutesAllowUserDiscoveryButRequireAdminForInspection(t *testing.T) {
	store, _ := newAuthStoreTest(t)
	userToken, _, err := store.CreateWebSession(t.Context(), "issuer", "user", "User", "user@example.com", []string{string(RoleUser)}, time.Hour)
	require.NoError(t, err)
	runnerAdminToken, _, err := store.CreateWebSession(t.Context(), "issuer", "runner-admin", "Runner admin", "runner-admin@example.com", []string{string(RoleUser), string(RoleRunnerAdmin)}, time.Hour)
	require.NoError(t, err)

	server := newRunnerTestServer(t, "")
	server.router = mux.NewRouter()
	server.config.WebAuthMode = WebAuthModeOIDC
	server.authStore = store
	server.setupRoutes()

	listRequest := httptest.NewRequest(http.MethodGet, "/api/runners", nil)
	listRequest.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: userToken})
	listResponse := httptest.NewRecorder()
	server.router.ServeHTTP(listResponse, listRequest)
	assert.Equal(t, http.StatusOK, listResponse.Code)

	inspectRequest := httptest.NewRequest(http.MethodGet, "/api/runners/missing", nil)
	inspectRequest.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: userToken})
	inspectResponse := httptest.NewRecorder()
	server.router.ServeHTTP(inspectResponse, inspectRequest)
	assert.Equal(t, http.StatusForbidden, inspectResponse.Code)

	adminInspectRequest := httptest.NewRequest(http.MethodGet, "/api/runners/missing", nil)
	adminInspectRequest.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: runnerAdminToken})
	adminInspectResponse := httptest.NewRecorder()
	server.router.ServeHTTP(adminInspectResponse, adminInspectRequest)
	assert.Equal(t, http.StatusNotFound, adminInspectResponse.Code)
}

func TestConversationResponseIncludesDurableRunnerEnvironmentProfile(t *testing.T) {
	server := newRunnerTestServer(t, "")
	link := newRunnerAPITestLink()
	registration, err := server.runnerRegistry.Register(protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version},
		Host: protocol.Host{
			InstanceID: "host-one",
			Hostname:   "worker-one",
			OS:         "linux",
			Arch:       "amd64",
		},
		Workspace: protocol.Workspace{Path: "/work/project", Name: "project"},
	}, link)
	require.NoError(t, err)
	require.NoError(t, server.runnerRegistry.BindConversationWithEnvironmentProfile(t.Context(), "conversation-profile", registration.RunnerID, "gpu"))
	server.conversationService = &mockConversationService{
		getFunc: func(context.Context, string) (*conversations.GetConversationResponse, error) {
			return &conversations.GetConversationResponse{
				ID:          "conversation-profile",
				Provider:    "openai",
				RawMessages: json.RawMessage(`[]`),
			}, nil
		},
	}

	request := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/api/conversations/conversation-profile", nil), map[string]string{"id": "conversation-profile"})
	recorder := httptest.NewRecorder()
	server.handleGetConversation(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)
	var response WebConversationResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, registration.RunnerID, response.RunnerID)
	assert.Equal(t, "gpu", response.EnvironmentProfile)
}

func TestDeleteRunnerRequiresOfflineAndClearsAffinity(t *testing.T) {
	server := newRunnerTestServer(t, "")
	link := newRunnerAPITestLink()
	registration, err := server.runnerRegistry.Register(protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version},
		Host:             protocol.Host{InstanceID: "host-delete", Hostname: "worker-delete", OS: "linux", Arch: "amd64"},
		Workspace:        protocol.Workspace{Path: "/work/delete", Name: "delete"},
	}, link)
	require.NoError(t, err)
	require.NoError(t, server.runnerRegistry.BindConversation(t.Context(), "conversation-delete", registration.RunnerID))

	connectedRecorder := httptest.NewRecorder()
	connectedRequest := mux.SetURLVars(httptest.NewRequest(http.MethodDelete, "/api/runners/"+registration.RunnerID, nil), map[string]string{"id": registration.RunnerID})
	server.handleDeleteRunner(connectedRecorder, connectedRequest)
	assert.Equal(t, http.StatusConflict, connectedRecorder.Code)

	server.runnerRegistry.Detach(registration.RunnerID, registration.ConnectionID, registration.Generation, nil)
	removeRecorder := httptest.NewRecorder()
	removeRequest := mux.SetURLVars(httptest.NewRequest(http.MethodDelete, "/api/runners/"+registration.RunnerID, nil), map[string]string{"id": registration.RunnerID})
	server.handleDeleteRunner(removeRecorder, removeRequest)
	require.Equal(t, http.StatusOK, removeRecorder.Code)
	var result runnerregistry.RemovalResult
	require.NoError(t, json.Unmarshal(removeRecorder.Body.Bytes(), &result))
	assert.Equal(t, registration.RunnerID, result.RunnerID)
	assert.Equal(t, 1, result.RemovedConversationAffinities)

	missingRecorder := httptest.NewRecorder()
	missingRequest := mux.SetURLVars(httptest.NewRequest(http.MethodDelete, "/api/runners/"+registration.RunnerID, nil), map[string]string{"id": registration.RunnerID})
	server.handleDeleteRunner(missingRecorder, missingRequest)
	assert.Equal(t, http.StatusNotFound, missingRecorder.Code)

	invalidForceRecorder := httptest.NewRecorder()
	invalidForceRequest := mux.SetURLVars(httptest.NewRequest(http.MethodDelete, "/api/runners/missing?force=perhaps", nil), map[string]string{"id": "missing"})
	server.handleDeleteRunner(invalidForceRecorder, invalidForceRequest)
	assert.Equal(t, http.StatusBadRequest, invalidForceRecorder.Code)
}

type runnerUIEventSink struct {
	events chan ChatEvent
}

func (s *runnerUIEventSink) Send(event ChatEvent) error {
	s.events <- event
	return nil
}

func TestHandleRunnerUIRequestRoutesInteractivePrompts(t *testing.T) {
	server := newRunnerTestServer(t, "")
	registration, sink, broker := openRunnerUIRun(t, server)
	identity := runnerUIRequestIdentity(registration)

	tests := []struct {
		name      string
		method    string
		params    any
		eventKind string
		requestID string
		response  extensions.UIInputResponse
	}{
		{
			name:      "input",
			method:    protocol.MethodUIInput,
			params:    runnerpayload.UIInputParams{RunID: "run-ui", Request: extensions.UIInputRequest{ID: "input-1", Title: "Input"}},
			eventKind: "ui-input-request",
			requestID: "input-1",
			response:  extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted, Value: "answer"},
		},
		{
			name:      "confirm",
			method:    protocol.MethodUIConfirm,
			params:    runnerpayload.UIConfirmParams{RunID: "run-ui", Request: extensions.UIConfirmRequest{ID: "confirm-1", Title: "Confirm"}},
			eventKind: "ui-confirm-request",
			requestID: "confirm-1",
			response:  extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted, Confirmed: true},
		},
		{
			name:      "select",
			method:    protocol.MethodUISelect,
			params:    runnerpayload.UISelectParams{RunID: "run-ui", Request: extensions.UISelectRequest{ID: "select-1", Title: "Select", Options: []string{"one"}}},
			eventKind: "ui-select-request",
			requestID: "select-1",
			response:  extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted, Value: "one"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			type result struct {
				value  any
				rpcErr *protocol.RPCError
			}
			resultCh := make(chan result, 1)
			go func() {
				value, rpcErr := server.HandleRunnerUIRequest(t.Context(), identity, test.method, mustRunnerJSON(t, test.params))
				resultCh <- result{value: value, rpcErr: rpcErr}
			}()

			select {
			case event := <-sink.events:
				assert.Equal(t, test.eventKind, event.Kind)
			case <-time.After(time.Second):
				t.Fatal("runner UI event was not emitted")
			}
			require.True(t, broker.Respond(test.requestID, test.response))
			select {
			case result := <-resultCh:
				require.Nil(t, result.rpcErr)
				response, ok := result.value.(extensions.UIInputResponse)
				require.True(t, ok)
				assert.Equal(t, test.response, response)
			case <-time.After(time.Second):
				t.Fatal("runner UI request did not resolve")
			}
		})
	}

	notify, rpcErr := server.HandleRunnerUIRequest(t.Context(), identity, protocol.MethodUINotify, mustRunnerJSON(t, runnerpayload.UINotifyParams{
		RunID: "run-ui", Request: extensions.UINotifyRequest{Title: "Notice", Message: "done"},
	}))
	require.Nil(t, rpcErr)
	assert.Equal(t, extensions.UIInputStatusSubmitted, notify.(extensions.UIInputResponse).Status)
	select {
	case event := <-sink.events:
		assert.Equal(t, "ui-notification", event.Kind)
		assert.Equal(t, "done", event.UINotify.Message)
	case <-time.After(time.Second):
		t.Fatal("runner UI notification was not emitted")
	}
}

func TestHandleRunnerUIRequestValidatesRunAndPersistentCapabilities(t *testing.T) {
	server := newRunnerTestServer(t, "")
	registration, _, _ := openRunnerUIRun(t, server)
	identity := runnerUIRequestIdentity(registration)

	server.activeChatsMu.Lock()
	delete(server.activeChats, "conversation-ui")
	server.activeChatsMu.Unlock()
	value, rpcErr := server.HandleRunnerUIRequest(t.Context(), identity, protocol.MethodUIInput, mustRunnerJSON(t, runnerpayload.UIInputParams{
		RunID: "run-ui", Request: extensions.UIInputRequest{ID: "input-1", Title: "Input"},
	}))
	require.Nil(t, rpcErr)
	response := value.(extensions.UIInputResponse)
	assert.Equal(t, extensions.UIInputStatusUnavailable, response.Status)

	owner := runnerpayload.ExtensionOwner{ExtensionID: "subagent", Generation: 1}
	value, rpcErr = server.HandleRunnerUIRequest(t.Context(), identity, protocol.MethodUITranscriptAppend, mustRunnerJSON(t, runnerpayload.UITranscriptAppendParams{RunID: "run-ui", Owner: owner}))
	require.Nil(t, rpcErr)
	assert.Contains(t, value.(extensions.UITranscriptAppendResponse).Reason, "no owning execution")
	value, rpcErr = server.HandleRunnerUIRequest(t.Context(), identity, protocol.MethodUIWidgetSet, mustRunnerJSON(t, runnerpayload.UIWidgetSetParams{
		RunID: "run-ui",
		Owner: owner,
		Request: extensions.UIWidgetSetRequest{
			ID:        "background-agents",
			Placement: extensions.UIWidgetPlacementAboveComposer,
			Frame: extensions.UIFrame{
				Sequence: 1,
				Lines:    []extensions.UIFrameLine{{Spans: []extensions.UIStyledSpan{{Text: "1 active"}}}},
			},
		},
	}))
	require.Nil(t, rpcErr)
	assert.True(t, value.(extensions.UIFrameResponse).Accepted)
	_, snapshot := server.extensionUI.Snapshot("conversation-ui")
	require.Len(t, snapshot, 1)
	assert.Equal(t, fmt.Sprintf("%d:1", identity.Generation), snapshot[0].Generation)

	value, rpcErr = server.HandleRunnerUIRequest(t.Context(), identity, protocol.MethodUIWidgetFrame, mustRunnerJSON(t, runnerpayload.UIWidgetFrameParams{
		RunID: "run-ui",
		Owner: owner,
		Request: extensions.UIWidgetFrameRequest{
			ID: "background-agents",
			Frame: extensions.UIFrame{
				Sequence: 2,
				Lines:    []extensions.UIFrameLine{{Spans: []extensions.UIStyledSpan{{Text: "1 completed"}}}},
			},
		},
	}))
	require.Nil(t, rpcErr)
	assert.True(t, value.(extensions.UIFrameResponse).Accepted)

	_, rpcErr = server.HandleRunnerUIRequest(t.Context(), identity, protocol.MethodUIWidgetSet, mustRunnerJSON(t, runnerpayload.UIWidgetSetParams{
		RunID: "run-ui",
		Owner: owner,
		Request: extensions.UIWidgetSetRequest{
			ScopeID:   "another-conversation",
			ID:        "other",
			Placement: extensions.UIWidgetPlacementAboveComposer,
			Frame:     extensions.UIFrame{Sequence: 1},
		},
	}))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeInvalidParams, rpcErr.Code)

	value, rpcErr = server.HandleRunnerUIRequest(t.Context(), identity, protocol.MethodUIWidgetRemove, mustRunnerJSON(t, runnerpayload.UIWidgetRemoveParams{
		RunID:   "run-ui",
		Owner:   owner,
		Request: extensions.UIWidgetRemoveRequest{ID: "background-agents", Sequence: 3},
	}))
	require.Nil(t, rpcErr)
	assert.True(t, value.(extensions.UIFrameResponse).Accepted)
	_, snapshot = server.extensionUI.Snapshot("conversation-ui")
	assert.Empty(t, snapshot)

	for _, method := range []string{
		protocol.MethodUISurfaceOpen,
		protocol.MethodUISurfaceFrame,
		protocol.MethodUISurfaceClose,
	} {
		value, rpcErr = server.HandleRunnerUIRequest(t.Context(), identity, method, json.RawMessage(`{"runId":"run-ui","owner":{"extensionId":"subagent","generation":1},"lifecycle":1,"request":{"id":"surface","frame":{"sequence":1},"sequence":2}}`))
		require.Nil(t, rpcErr)
		assert.Contains(t, value.(extensions.UIFrameResponse).Reason, "no owning execution")
	}

	_, rpcErr = server.HandleRunnerUIRequest(t.Context(), identity, protocol.MethodUIInput, json.RawMessage(`not-json`))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeInvalidParams, rpcErr.Code)
	_, rpcErr = server.HandleRunnerUIRequest(t.Context(), identity, protocol.MethodUIInput, mustRunnerJSON(t, runnerpayload.UIInputParams{}))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeInvalidParams, rpcErr.Code)
	_, rpcErr = server.HandleRunnerUIRequest(t.Context(), runnerregistry.UIRequestIdentity{
		RunnerID:     "another-runner",
		ConnectionID: identity.ConnectionID,
		Generation:   identity.Generation,
	}, protocol.MethodUIInput, mustRunnerJSON(t, runnerpayload.UIInputParams{RunID: "run-ui"}))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeStale, rpcErr.Code)
	_, rpcErr = server.HandleRunnerUIRequest(t.Context(), runnerregistry.UIRequestIdentity{
		RunnerID:     identity.RunnerID,
		ConnectionID: identity.ConnectionID,
		Generation:   identity.Generation + 1,
	}, protocol.MethodUIInput, mustRunnerJSON(t, runnerpayload.UIInputParams{RunID: "run-ui"}))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeStale, rpcErr.Code)
	_, rpcErr = server.HandleRunnerUIRequest(t.Context(), identity, "ui.unknown", json.RawMessage(`{}`))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeMethodNotFound, rpcErr.Code)

	require.NoError(t, server.runnerRegistry.CloseRun(t.Context(), "run-ui", runnerregistry.RunStatusSucceeded, nil))
	_, rpcErr = server.HandleRunnerUIRequest(t.Context(), identity, protocol.MethodUIInput, mustRunnerJSON(t, runnerpayload.UIInputParams{RunID: "run-ui"}))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeStale, rpcErr.Code)
	value, rpcErr = server.HandleRunnerUIRequest(t.Context(), identity, protocol.MethodUIWidgetSet, mustRunnerJSON(t, runnerpayload.UIWidgetSetParams{
		RunID: "run-ui",
		Owner: owner,
		Request: extensions.UIWidgetSetRequest{
			ScopeID:   "conversation-ui",
			ID:        "background-after-close",
			Placement: extensions.UIWidgetPlacementAboveComposer,
			Frame: extensions.UIFrame{
				Sequence: 1,
				Lines:    []extensions.UIFrameLine{{Spans: []extensions.UIStyledSpan{{Text: "still running"}}}},
			},
		},
	}))
	require.Nil(t, rpcErr)
	assert.True(t, value.(extensions.UIFrameResponse).Accepted)
	_, backgroundSnapshot := server.extensionUI.Snapshot("conversation-ui")
	require.Len(t, backgroundSnapshot, 1)
	assert.Equal(t, "background-after-close", backgroundSnapshot[0].ID)

	_, rpcErr = server.HandleRunnerUIRequest(t.Context(), identity, protocol.MethodUIWidgetSet, mustRunnerJSON(t, runnerpayload.UIWidgetSetParams{
		RunID: "run-ui",
		Owner: owner,
		Request: extensions.UIWidgetSetRequest{
			ID:        "missing-scope",
			Placement: extensions.UIWidgetPlacementAboveComposer,
			Frame:     extensions.UIFrame{Sequence: 1},
		},
	}))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeStale, rpcErr.Code)
	_, rpcErr = server.HandleRunnerUIRequest(t.Context(), identity, protocol.MethodUIWidgetSet, mustRunnerJSON(t, runnerpayload.UIWidgetSetParams{
		RunID: "run-ui",
		Owner: owner,
		Request: extensions.UIWidgetSetRequest{
			ScopeID:   "another-conversation",
			ID:        "wrong-background-scope",
			Placement: extensions.UIWidgetPlacementAboveComposer,
			Frame:     extensions.UIFrame{Sequence: 1},
		},
	}))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeInvalidParams, rpcErr.Code)

	nilServer := &Server{}
	_, rpcErr = nilServer.HandleRunnerUIRequest(t.Context(), identity, protocol.MethodUIInput, mustRunnerJSON(t, runnerpayload.UIInputParams{RunID: "run-ui"}))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeUnavailable, rpcErr.Code)
	assert.Nil(t, nilServer.runnerUIBroker("run-ui"))
}

func TestRunnerRESTEndpointsRequireRegistry(t *testing.T) {
	server := &Server{}
	listRecorder := httptest.NewRecorder()
	server.handleListRunners(listRecorder, httptest.NewRequest(http.MethodGet, "/api/runners", nil))
	assert.Equal(t, http.StatusServiceUnavailable, listRecorder.Code)

	getRecorder := httptest.NewRecorder()
	getRequest := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/api/runners/runner-1", nil), map[string]string{"id": "runner-1"})
	server.handleGetRunner(getRecorder, getRequest)
	assert.Equal(t, http.StatusServiceUnavailable, getRecorder.Code)

	deleteRecorder := httptest.NewRecorder()
	deleteRequest := mux.SetURLVars(httptest.NewRequest(http.MethodDelete, "/api/runners/runner-1", nil), map[string]string{"id": "runner-1"})
	server.handleDeleteRunner(deleteRecorder, deleteRequest)
	assert.Equal(t, http.StatusServiceUnavailable, deleteRecorder.Code)
}

func TestCommitRunnerAffinityOnlyReleasesPendingBindingWhenConversationIsMissing(t *testing.T) {
	server := newRunnerTestServer(t, "")
	registration, err := server.runnerRegistry.Register(protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version},
		Host:             protocol.Host{InstanceID: "host-affinity", Hostname: "host", OS: "linux", Arch: "amd64"},
		Workspace:        protocol.Workspace{Path: "/work/affinity", Name: "affinity"},
	}, newRunnerAPITestLink())
	require.NoError(t, err)

	require.NoError(t, server.runnerRegistry.BindConversation(t.Context(), "conversation-transient", registration.RunnerID))
	server.conversationService = &mockConversationService{getFunc: func(context.Context, string) (*conversations.GetConversationResponse, error) {
		return nil, errors.New("database temporarily unavailable")
	}}
	err = server.commitRunnerAffinity(t.Context(), "conversation-transient")
	require.ErrorContains(t, err, "temporarily unavailable")
	runnerID, found := server.runnerRegistry.RunnerForConversation("conversation-transient")
	assert.True(t, found)
	assert.Equal(t, registration.RunnerID, runnerID)

	require.NoError(t, server.runnerRegistry.BindConversation(t.Context(), "conversation-missing", registration.RunnerID))
	server.conversationService = &mockConversationService{getFunc: func(context.Context, string) (*conversations.GetConversationResponse, error) {
		return nil, convtypes.ErrConversationNotFound
	}}
	err = server.commitRunnerAffinity(t.Context(), "conversation-missing")
	require.ErrorIs(t, err, convtypes.ErrConversationNotFound)
	_, found = server.runnerRegistry.RunnerForConversation("conversation-missing")
	assert.False(t, found)
}

func TestServerChatRunnerResolvesAffinityBeforeChatValidation(t *testing.T) {
	server := newRunnerTestServer(t, "")
	registration, err := server.runnerRegistry.Register(protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version},
		Host:             protocol.Host{InstanceID: "host-chat", Hostname: "host", OS: "linux", Arch: "amd64"},
		Workspace:        protocol.Workspace{Path: "/work/chat", Name: "chat"},
	}, newRunnerAPITestLink())
	require.NoError(t, err)
	require.NoError(t, server.runnerRegistry.BindConversationWithEnvironmentProfile(t.Context(), "conversation-chat", registration.RunnerID, "gpu"))

	defaultRunner := NewExecutor("")
	runner := &serverChatRunner{runner: defaultRunner, server: server}
	defaultRunner.SetEnvironmentResolver(runner)
	active := newActiveChatRun(func() {})
	active.uiInput = newWebUIInputBroker("conversation-chat", &recordingChatSink{})
	server.activeChats["conversation-chat"] = active

	conversationID, err := runner.Run(t.Context(), ChatRequest{
		ConversationID: "conversation-chat",
		Message:        " ",
		ClientCapabilities: &chat.ChatClientCapabilities{
			InteractiveUI: true,
		},
	}, &recordingChatSink{})
	require.ErrorContains(t, err, "message cannot be empty")
	assert.Equal(t, "conversation-chat", conversationID)
	affinity, found, err := server.runnerRegistry.ResolveConversationAffinity(t.Context(), conversationID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, registration.RunnerID, affinity.RunnerID)
	assert.Equal(t, "gpu", affinity.EnvironmentProfile)

	conversationID, err = runner.Run(t.Context(), ChatRequest{
		ConversationID: "local-conversation",
		Message:        "hello",
	}, &recordingChatSink{})
	require.ErrorContains(t, err, "no runner is selected")
	assert.Equal(t, "local-conversation", conversationID)

	var nilRunner *serverChatRunner
	conversationID, err = nilRunner.Run(t.Context(), ChatRequest{ConversationID: "local-conversation", Message: " "}, &recordingChatSink{})
	require.ErrorContains(t, err, "cannot start work because no runner is configured")
	assert.Equal(t, "local-conversation", conversationID)

	assert.True(t, chatSupportsInteractiveUI(ChatRequest{ClientCapabilities: &chat.ChatClientCapabilities{InteractiveUI: true}}))
	assert.False(t, chatSupportsInteractiveUI(ChatRequest{}))
	require.NoError(t, runner.CloseConversation("conversation-chat"))
	require.NoError(t, runner.Close())
	require.NoError(t, runner.Close())
	require.NoError(t, (*serverChatRunner)(nil).CloseConversation("conversation-chat"))
}

func TestServerChatRunnerRejectsExistingLocalConversationRunnerMigration(t *testing.T) {
	server := newRunnerTestServer(t, "")
	registration, err := server.runnerRegistry.Register(protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version},
		Host:             protocol.Host{InstanceID: "host-chat-migration", Hostname: "host", OS: "linux", Arch: "amd64"},
		Workspace:        protocol.Workspace{Path: "/work/chat-migration", Name: "chat-migration"},
	}, newRunnerAPITestLink())
	require.NoError(t, err)
	require.NoError(t, server.runnerRegistry.BindConversation(t.Context(), "remote-conversation", registration.RunnerID))

	server.conversationService = &mockConversationService{getFunc: func(_ context.Context, id string) (*conversations.GetConversationResponse, error) {
		switch id {
		case "local-conversation":
			return &conversations.GetConversationResponse{ID: id}, nil
		case "new-conversation":
			return nil, convtypes.ErrConversationNotFound
		default:
			return nil, errors.Errorf("unexpected conversation lookup %q", id)
		}
	}}
	runner := &serverChatRunner{server: server, runner: NewExecutor("")}
	t.Cleanup(func() { assert.NoError(t, runner.Close()) })

	conversationID, err := runner.Run(t.Context(), ChatRequest{
		ConversationID: "local-conversation",
		RunnerID:       registration.RunnerID,
		Message:        " ",
	}, &recordingChatSink{})
	require.ErrorContains(t, err, "older conversation needs a runner")
	assert.Equal(t, "local-conversation", conversationID)
	_, found := server.runnerRegistry.RunnerForConversation(conversationID)
	assert.False(t, found)

	conversationID, err = runner.Run(t.Context(), ChatRequest{
		ConversationID: "new-conversation",
		RunnerID:       registration.RunnerID,
		Message:        " ",
	}, &recordingChatSink{})
	require.ErrorContains(t, err, "message cannot be empty")
	assert.Equal(t, "new-conversation", conversationID)

	conversationID, err = runner.Run(t.Context(), ChatRequest{
		ConversationID: "remote-conversation",
		RunnerID:       registration.RunnerID,
		Message:        " ",
	}, &recordingChatSink{})
	require.ErrorContains(t, err, "message cannot be empty")
	assert.Equal(t, "remote-conversation", conversationID)
}

func TestServerChatRunnerSavedProfileEnvironmentSelection(t *testing.T) {
	previous := viper.AllSettings()
	viper.Reset()
	t.Cleanup(func() { viper.Reset(); require.NoError(t, viper.MergeConfigMap(previous)) })
	viper.Set("provider", "openai")
	viper.Set("model", "base-model")
	viper.Set("profile", "deep")
	viper.Set("tool_mode", "full")
	viper.Set("extensions.enabled", false)
	viper.Set("profiles", map[string]any{
		"deep": map[string]any{"model": "active-model", "tool_mode": "patch"},
	})
	viper.Set("environment_profiles", map[string]any{"review": map[string]any{"sysprompt_args": map[string]any{"scope": "review"}}})
	for _, test := range []struct {
		name       string
		snapshot   *string
		legacy     map[string]any
		standalone bool
		identity   string
		selector   string
		mode       llmtypes.ToolMode
	}{
		{name: "removed snapshot", snapshot: new("removed"), identity: "removed", selector: "default", mode: llmtypes.ToolModeFull},
		{name: "existing snapshot", snapshot: new("deep"), identity: "deep", selector: "deep", mode: llmtypes.ToolModePatch},
		{name: "empty snapshot", snapshot: new(""), selector: "default", mode: llmtypes.ToolModeFull},
		{name: "legacy missing profile", identity: "deep", selector: "deep", mode: llmtypes.ToolModePatch},
		{name: "legacy empty profile", legacy: map[string]any{"profile": ""}, identity: "default", selector: "default", mode: llmtypes.ToolModeFull},
		{name: "standalone removed snapshot", standalone: true, snapshot: new("removed"), identity: "removed", selector: "removed", mode: llmtypes.ToolModePatch},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("HOME", root)
			server := newRunnerTestServer(t, "")
			server.config.EmbeddedRunner = &EmbeddedRunnerConfig{Workspace: root}
			options := runnerclient.ServiceOptions{}
			var err error
			if test.standalone {
				options.WorkspaceConfigLoader, err = runnerclient.NewWorkspaceConfigLoader(map[string]any{
					"tool_mode": "patch", "extensions": map[string]any{"enabled": false},
					"environment_profiles": viper.Get("environment_profiles"),
				})
			} else {
				options.ProfileConfigLoader, err = runnerclient.NewEmbeddedConfigLoader(viper.AllSettings(), nil)
			}
			require.NoError(t, err)
			service, err := runnerclient.NewService(t.Context(), root, options)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, service.Close()) })
			link := newRunnerAPITestLink()
			registration, err := server.runnerRegistry.Register(protocol.RegisterParams{
				ProtocolVersions: []int{protocol.Version}, Capabilities: protocol.RunnerCapabilities{WorkspaceDiscovery: true},
				Host:      protocol.Host{InstanceID: "resume-host", Hostname: "worker", OS: "linux", Arch: "amd64"},
				Workspace: protocol.Workspace{Path: root, Name: "resume"},
			}, link)
			require.NoError(t, err)
			require.NoError(t, service.SetRegistration(registration))
			require.NoError(t, server.runnerRegistry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.HeartbeatParams{
				RunnerID: registration.RunnerID, Generation: registration.Generation, State: protocol.RunnerStateIdle,
			}))
			server.embeddedStatus.RunnerID = registration.RunnerID
			if test.standalone {
				server.embeddedStatus.RunnerID = "other-embedded-runner"
			}
			var opened protocol.RunOpenParams
			var wire runnerpayload.Manifest
			link.call = func(ctx context.Context, method string, params, result any) error {
				response, rpcErr := service.HandleRequest(ctx, method, mustRunnerJSON(t, params))
				if rpcErr != nil {
					return rpcErr
				}
				if method == protocol.MethodRunOpen {
					opened = params.(protocol.RunOpenParams)
					wire = response.(runnerpayload.Manifest)
				}
				if result == nil {
					return nil
				}
				return json.Unmarshal(mustRunnerJSON(t, response), result)
			}
			record := &conversations.GetConversationResponse{ID: "saved", CWD: root, Provider: "openai", Metadata: test.legacy}
			if test.snapshot != nil {
				record.Metadata, err = conversations.AddConfigSnapshot(map[string]any{"profile": "ignored-legacy-profile"}, llmtypes.Config{
					Profile: *test.snapshot, Provider: "openai", Model: "saved-model", ReasoningEffort: "medium",
				})
				require.NoError(t, err)
			}
			before := mustRunnerJSON(t, record)
			server.conversationService = &mockConversationService{getFunc: func(_ context.Context, id string) (*conversations.GetConversationResponse, error) {
				if id != record.ID {
					return nil, convtypes.ErrConversationNotFound
				}
				return record, nil
			}}
			require.NoError(t, server.runnerRegistry.BindConversationWithEnvironmentProfile(t.Context(), record.ID, registration.RunnerID, "review"))
			var discovery protocol.WorkspaceDiscoverResult
			for _, method := range []string{protocol.MethodWorkspaceDiscover, protocol.MethodWorkspaceCWDHints} {
				recorder := httptest.NewRecorder()
				query := url.Values{"conversationId": {record.ID}}
				if test.snapshot != nil {
					query.Set("profile", *test.snapshot)
				}
				server.handleRunnerDiscovery(recorder, httptest.NewRequest(http.MethodGet, "/?"+query.Encode(), nil), method)
				require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
				if method == protocol.MethodWorkspaceDiscover {
					require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &discovery))
				}
			}
			config, err := chat.ResolveConfigForExistingConversation(record)
			require.NoError(t, err)
			config.WorkingDirectory = root
			runner := &serverChatRunner{server: server}
			environment, err := runner.ResolveEnvironment(t.Context(), ChatRequest{RunnerID: registration.RunnerID}, record.ID, config, root)
			require.NoError(t, err)
			manifest, err := environment.Open(t.Context(), agentenv.RunSpec{ConversationID: record.ID, Config: config, EnvironmentProfile: "review"})
			require.NoError(t, err)
			assert.Equal(t, test.selector, opened.Agent.Profile)
			assert.Equal(t, "review", opened.Agent.EnvironmentProfile)
			assert.Equal(t, config.Provider, opened.Agent.Provider)
			assert.Equal(t, config.Model, opened.Agent.Model)
			assert.Equal(t, test.mode, manifest.Config.ToolMode)
			digest, err := runnerpayload.ComputeDiscoveryDigest(wire)
			require.NoError(t, err)
			assert.Equal(t, discovery.Digest, digest, "saved discovery and execution must use the same environment")
			assert.Equal(t, test.identity, config.Profile)
			assert.Equal(t, before, mustRunnerJSON(t, record), "runner fallback must not rewrite the saved snapshot")
			require.NoError(t, environment.Close(t.Context()))

			if !test.standalone {
				_, err = chat.ResolveConfigForNewConversation("removed")
				require.ErrorContains(t, err, "not found")
				recorder := httptest.NewRecorder()
				query := url.Values{"runnerId": {registration.RunnerID}, "profile": {"removed"}}
				server.handleGetSlashCommands(recorder, httptest.NewRequest(http.MethodGet, "/?"+query.Encode(), nil))
				assert.Equal(t, http.StatusBadGateway, recorder.Code)
				assert.Contains(t, recorder.Body.String(), "could not load the runner's available commands and tools", "unknown new discovery must not receive snapshot fallback")
				unknown := config.Clone()
				unknown.Profile = "removed"
				environment, err = runner.ResolveEnvironment(t.Context(), ChatRequest{RunnerID: registration.RunnerID}, "new", unknown, root)
				require.NoError(t, err)
				_, err = environment.Open(t.Context(), agentenv.RunSpec{ConversationID: "new", Config: unknown, EnvironmentProfile: "review"})
				require.ErrorContains(t, err, "not found", "new executions without a snapshot must not receive fallback")
				if test.snapshot == nil || *test.snapshot != "removed" {
					environment, err = runner.ResolveEnvironment(t.Context(), ChatRequest{RunnerID: registration.RunnerID}, record.ID, unknown, root)
					require.NoError(t, err)
					_, err = environment.Open(t.Context(), agentenv.RunSpec{ConversationID: record.ID, Config: unknown, EnvironmentProfile: "review"})
					require.ErrorContains(t, err, "not found", "fallback requires a matching snapshot, not just an existing conversation")
				}
			}
			if test.snapshot != nil && *test.snapshot == "removed" {
				recorder := httptest.NewRecorder()
				server.handleGetSlashCommands(recorder, httptest.NewRequest(http.MethodGet, "/?conversationId=saved&profile=default", nil))
				assert.Equal(t, http.StatusBadRequest, recorder.Code, "fallback must not allow replacing the immutable model profile")
			}
		})
	}
}

func TestServerChatRunnerResolveEnvironmentValidatesRunnerState(t *testing.T) {
	runner := &serverChatRunner{}
	_, err := runner.ResolveEnvironment(t.Context(), ChatRequest{}, "conversation", llmtypes.Config{}, "")
	require.ErrorContains(t, err, "runner id is required")
	_, err = runner.ResolveEnvironment(t.Context(), ChatRequest{RunnerID: "runner"}, "conversation", llmtypes.Config{}, "")
	require.ErrorContains(t, err, "runner registry is unavailable")

	server := newRunnerTestServer(t, "")
	runner.server = server
	_, err = runner.ResolveEnvironment(t.Context(), ChatRequest{RunnerID: "missing"}, "conversation", llmtypes.Config{}, "")
	require.ErrorContains(t, err, "runner not found")

	link := newRunnerAPITestLink()
	registration, err := server.runnerRegistry.Register(protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version},
		Host:             protocol.Host{InstanceID: "host-environment", Hostname: "host", OS: "linux", Arch: "amd64"},
		Workspace:        protocol.Workspace{Path: "/work/environment", Name: "environment"},
	}, link)
	require.NoError(t, err)
	environment, err := runner.ResolveEnvironment(t.Context(), ChatRequest{
		RunnerID: registration.RunnerID,
		ClientCapabilities: &chat.ChatClientCapabilities{
			InteractiveUI:      true,
			PersistentSurfaces: true,
		},
	}, "conversation", llmtypes.Config{}, "")
	require.NoError(t, err)
	require.NotNil(t, environment)

	server.runnerRegistry.Detach(registration.RunnerID, registration.ConnectionID, registration.Generation, nil)
	_, err = runner.ResolveEnvironment(t.Context(), ChatRequest{RunnerID: registration.RunnerID}, "conversation", llmtypes.Config{}, "")
	require.ErrorContains(t, err, "runner is offline")

	_, err = server.runnerRegistry.Register(protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version + 1},
		Host:             protocol.Host{InstanceID: "host-incompatible", Hostname: "host", OS: "linux", Arch: "amd64"},
		Workspace:        protocol.Workspace{Path: "/work/incompatible", Name: "incompatible"},
	}, newRunnerAPITestLink())
	require.ErrorContains(t, err, "does not support protocol version")
	var incompatibleID string
	for _, registered := range server.runnerRegistry.Runners() {
		if registered.Status == runnerregistry.RunnerStatusIncompatible {
			incompatibleID = registered.ID
			break
		}
	}
	require.NotEmpty(t, incompatibleID)
	_, err = runner.ResolveEnvironment(t.Context(), ChatRequest{RunnerID: incompatibleID}, "conversation", llmtypes.Config{}, "")
	require.ErrorContains(t, err, "does not support protocol version")
}

func openRunnerUIRun(t *testing.T, server *Server) (protocol.RegisterResult, *runnerUIEventSink, *webUIInputBroker) {
	t.Helper()
	link := newRunnerAPITestLink()
	registration, err := server.runnerRegistry.Register(protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version},
		Host:             protocol.Host{InstanceID: "host-ui", Hostname: "ui-host", OS: "linux", Arch: "amd64"},
		Workspace:        protocol.Workspace{Path: "/work/ui", Name: "ui"},
	}, link)
	require.NoError(t, err)
	link.call = func(_ context.Context, method string, params any, result any) error {
		switch method {
		case protocol.MethodRunOpen:
			request := params.(protocol.RunOpenParams)
			manifest := runnerpayload.Manifest{
				ProtocolVersion:  protocol.Version,
				RunnerID:         registration.RunnerID,
				RunID:            request.RunID,
				Generation:       registration.Generation,
				WorkingDirectory: "/work/ui",
			}
			digest, digestErr := runnerpayload.ComputeManifestDigest(manifest)
			if digestErr != nil {
				return digestErr
			}
			manifest.Digest = digest
			*result.(*runnerpayload.Manifest) = manifest
		}
		return nil
	}
	require.NoError(t, server.runnerRegistry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.HeartbeatParams{
		RunnerID: registration.RunnerID, Generation: registration.Generation, State: protocol.RunnerStateIdle,
	}))
	_, err = server.runnerRegistry.OpenRun(t.Context(), registration.RunnerID, protocol.RunOpenParams{
		RunID: "run-ui", ConversationID: "conversation-ui",
	})
	require.NoError(t, err)
	sink := &runnerUIEventSink{events: make(chan ChatEvent, 8)}
	broker := newWebUIInputBroker("conversation-ui", sink)
	t.Cleanup(broker.setOwner(t.Context(), "", sink))
	active := newActiveChatRun(func() {})
	active.uiInput = broker
	server.activeChats["conversation-ui"] = active
	return registration, sink, broker
}

func mustRunnerJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(value)
	require.NoError(t, err)
	return payload
}

func runnerUIRequestIdentity(registration protocol.RegisterResult) runnerregistry.UIRequestIdentity {
	return runnerregistry.UIRequestIdentity{
		RunnerID:     registration.RunnerID,
		ConnectionID: registration.ConnectionID,
		Generation:   registration.Generation,
	}
}

func newRunnerTestServer(t *testing.T, authToken string) *Server {
	t.Helper()
	runCtx, runCancel := context.WithCancel(t.Context())
	registry, err := runnerregistry.New(runCtx, runnerregistry.Options{
		HeartbeatInterval: time.Hour,
		HeartbeatTimeout:  2 * time.Hour,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		runCancel()
		_ = registry.Close()
	})
	config := &ServerConfig{}
	if authToken != "" {
		config.AuthToken = authToken
		config.RunnerAuthToken = authToken + "-runner"
	}
	server := &Server{
		config:          config,
		runCtx:          runCtx,
		runCancel:       runCancel,
		runnerRegistry:  registry,
		activeChats:     make(map[string]*activeChatRun),
		chatSubscribers: make(map[string]map[*subscriberEventSink]struct{}),
	}
	server.extensionUI = newWebExtensionUIHost(server.emitExtensionUIEvent)
	registry.SetEnvironmentErrorHandler(func(conversationID string) {
		server.cancelActiveChat(conversationID)
	})
	return server
}

func dialRunnerPeer(t *testing.T, url string, headers http.Header) *protocol.Peer {
	t.Helper()
	dialer := websocket.Dialer{Subprotocols: []string{protocol.Subprotocol}}
	conn, response, err := dialer.DialContext(t.Context(), url, headers)
	if response != nil && response.Body != nil {
		t.Cleanup(func() { _ = response.Body.Close() })
	}
	require.NoError(t, err)
	require.Equal(t, protocol.Subprotocol, conn.Subprotocol())
	peer, err := protocol.NewPeer(conn, protocol.PeerConfig{RequestPrefix: "runner"})
	require.NoError(t, err)
	require.NoError(t, peer.Start(t.Context()))
	t.Cleanup(func() { _ = peer.Close() })
	return peer
}
