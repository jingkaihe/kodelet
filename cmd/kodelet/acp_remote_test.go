package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	runnerclient "github.com/jingkaihe/kodelet/pkg/runner/client"
	"github.com/jingkaihe/kodelet/pkg/runner/localstate"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbeddedACPRemoteProviderWaitsForControlPlaneReadiness(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "/api/runners", request.URL.Path)
		assert.Equal(t, "Bearer api-secret", request.Header.Get("Authorization"))
		status := runnerregistry.RunnerStatusConnecting
		if requests.Add(1) > 1 {
			status = runnerregistry.RunnerStatusIdle
		}
		require.NoError(t, json.NewEncoder(w).Encode(runnerListAPIResponse{Runners: []runnerregistry.Runner{{
			ID:        "runner-1",
			Connected: true,
			Status:    status,
		}}}))
	}))
	t.Cleanup(server.Close)

	provider := newEmbeddedACPRemoteProvider(server.URL, "api-secret", "", nil)
	provider.registeredRunner(protocol.RegisterResult{RunnerID: "runner-1"})
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	client, runnerID, err := provider.WaitForRemoteChat(ctx)
	require.NoError(t, err)
	require.NotNil(t, client)
	assert.Equal(t, "runner-1", runnerID)
	assert.GreaterOrEqual(t, requests.Load(), int32(2))
}

func TestEmbeddedACPRemoteProviderRejectsIncompatibleRunner(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		require.NoError(t, json.NewEncoder(w).Encode(runnerListAPIResponse{Runners: []runnerregistry.Runner{{
			ID:                 "runner-1",
			Connected:          true,
			Status:             runnerregistry.RunnerStatusIncompatible,
			CompatibilityError: "runner protocol mismatch",
		}}}))
	}))
	t.Cleanup(server.Close)

	provider := newEmbeddedACPRemoteProvider(server.URL, "", "", nil)
	provider.registeredRunner(protocol.RegisterResult{RunnerID: "runner-1"})
	_, _, err := provider.WaitForRemoteChat(t.Context())
	require.ErrorContains(t, err, "protocol mismatch")
}

func TestAcquireOrReuseACPRunnerOwnsUnlockedWorkspace(t *testing.T) {
	store, err := localstate.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	runner, err := runnerclient.NewRunner(t.Context(), runnerclient.RunnerConfig{
		Server:    "http://localhost:8080",
		Workspace: t.TempDir(),
		Store:     store,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runner.Close()) })

	owned, runnerID, err := acquireOrReuseACPRunner(t.Context(), runner, "http://localhost:8080")
	require.NoError(t, err)
	assert.True(t, owned)
	assert.Empty(t, runnerID)
}

func TestAcquireOrReuseACPRunnerUsesLiveLockRunnerID(t *testing.T) {
	store, err := localstate.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	workspace := t.TempDir()
	lock, err := store.AcquireWorkspaceLock(workspace, localstate.LockMetadata{
		Server:   "http://localhost:8080",
		RunnerID: "runner-existing",
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, lock.Close()) })
	runner, err := runnerclient.NewRunner(t.Context(), runnerclient.RunnerConfig{
		Server:    "http://localhost:8080",
		Workspace: workspace,
		Store:     store,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runner.Close()) })

	owned, runnerID, err := acquireOrReuseACPRunner(t.Context(), runner, "http://localhost:8080")
	require.NoError(t, err)
	assert.False(t, owned)
	assert.Equal(t, "runner-existing", runnerID)
}

func TestAcquireOrReuseACPRunnerServesWhileExistingRunnerRegisters(t *testing.T) {
	store, err := localstate.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	workspace := t.TempDir()
	lock, err := store.AcquireWorkspaceLock(workspace, localstate.LockMetadata{Server: "http://localhost:8080"})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, lock.Close()) })
	runner, err := runnerclient.NewRunner(t.Context(), runnerclient.RunnerConfig{
		Server:    "http://localhost:8080",
		Workspace: workspace,
		Store:     store,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runner.Close()) })

	startedAt := time.Now()
	owned, runnerID, err := acquireOrReuseACPRunner(t.Context(), runner, "http://localhost:8080")
	require.NoError(t, err)
	assert.False(t, owned)
	assert.Empty(t, runnerID)
	assert.Less(t, time.Since(startedAt), time.Second)
}

func TestAcquireOrReuseACPRunnerRejectsAnotherServer(t *testing.T) {
	store, err := localstate.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	workspace := t.TempDir()
	lock, err := store.AcquireWorkspaceLock(workspace, localstate.LockMetadata{
		Server:   "http://localhost:9090",
		RunnerID: "runner-existing",
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, lock.Close()) })
	runner, err := runnerclient.NewRunner(t.Context(), runnerclient.RunnerConfig{
		Server:    "http://localhost:8080",
		Workspace: workspace,
		Store:     store,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runner.Close()) })

	_, _, err = acquireOrReuseACPRunner(t.Context(), runner, "http://localhost:8080")
	require.ErrorContains(t, err, "already registered with http://localhost:9090")
}

func TestAcquireOrReuseACPRunnerIgnoresStoppingLockMetadata(t *testing.T) {
	store, err := localstate.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	workspace := t.TempDir()
	lock, err := store.AcquireWorkspaceLock(workspace, localstate.LockMetadata{
		Server:   "http://localhost:9090",
		RunnerID: "runner-stopping",
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, lock.Close()) })
	metadata := lock.Metadata()
	stoppedAt := time.Now().UTC()
	metadata.StoppedAt = &stoppedAt
	require.NoError(t, lock.WriteMetadata(metadata))
	runner, err := runnerclient.NewRunner(t.Context(), runnerclient.RunnerConfig{
		Server:    "http://localhost:8080",
		Workspace: workspace,
		Store:     store,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runner.Close()) })

	type result struct {
		owned    bool
		runnerID string
		err      error
	}
	done := make(chan result, 1)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	go func() {
		owned, runnerID, acquireErr := acquireOrReuseACPRunner(ctx, runner, "http://localhost:8080")
		done <- result{owned: owned, runnerID: runnerID, err: acquireErr}
	}()
	select {
	case result := <-done:
		t.Fatalf("stopping lock metadata was reused before release: %+v", result)
	case <-time.After(30 * time.Millisecond):
	}
	require.NoError(t, lock.Close())
	select {
	case result := <-done:
		require.NoError(t, result.err)
		assert.True(t, result.owned)
		assert.Empty(t, result.runnerID)
	case <-time.After(time.Second):
		t.Fatal("ACP did not acquire the workspace after the stopping owner released it")
	}
}

func TestEmbeddedACPRemoteProviderRefreshesRunnerIDFromLiveLock(t *testing.T) {
	workspace := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		require.NoError(t, json.NewEncoder(w).Encode(runnerListAPIResponse{Runners: []runnerregistry.Runner{{
			ID:        "runner-new",
			Connected: true,
			Status:    runnerregistry.RunnerStatusIdle,
			Workspace: protocol.Workspace{Path: workspace},
		}}}))
	}))
	t.Cleanup(server.Close)
	store, err := localstate.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	lock, err := store.AcquireWorkspaceLock(workspace, localstate.LockMetadata{Server: server.URL, RunnerID: "runner-old"})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, lock.Close()) })
	provider := newEmbeddedACPRemoteProvider(server.URL, "", workspace, store)
	provider.registeredRunnerID("runner-old")
	metadata := lock.Metadata()
	metadata.RunnerID = "runner-new"
	require.NoError(t, lock.WriteMetadata(metadata))

	client, runnerID, err := provider.WaitForRemoteChat(t.Context())
	require.NoError(t, err)
	require.NotNil(t, client)
	assert.Equal(t, "runner-new", runnerID)
}

func TestEmbeddedACPRemoteProviderReportsReusedRunnerExit(t *testing.T) {
	workspace := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		require.NoError(t, json.NewEncoder(w).Encode(runnerListAPIResponse{Runners: []runnerregistry.Runner{{
			ID:        "runner-1",
			Connected: true,
			Status:    runnerregistry.RunnerStatusIdle,
			Workspace: protocol.Workspace{Path: workspace},
		}}}))
	}))
	t.Cleanup(server.Close)
	store, err := localstate.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	lock, err := store.AcquireWorkspaceLock(workspace, localstate.LockMetadata{Server: server.URL, RunnerID: "runner-1"})
	require.NoError(t, err)
	provider := newEmbeddedACPRemoteProvider(server.URL, "", workspace, store)
	provider.reuseRunner("runner-1")

	client, runnerID, err := provider.WaitForRemoteChat(t.Context())
	require.NoError(t, err)
	require.NotNil(t, client)
	assert.Equal(t, "runner-1", runnerID)
	require.NoError(t, lock.Close())

	_, _, err = provider.WaitForRemoteChat(t.Context())
	require.ErrorContains(t, err, "reused workspace runner stopped")
}

func TestEmbeddedACPRemoteProviderRecoversSameRunnerIDAfterHandoff(t *testing.T) {
	workspace := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		require.NoError(t, json.NewEncoder(w).Encode(runnerListAPIResponse{Runners: []runnerregistry.Runner{{
			ID:        "runner-1",
			Connected: true,
			Status:    runnerregistry.RunnerStatusIdle,
			Workspace: protocol.Workspace{Path: workspace},
		}}}))
	}))
	t.Cleanup(server.Close)
	store, err := localstate.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	lock, err := store.AcquireWorkspaceLock(workspace, localstate.LockMetadata{Server: server.URL, RunnerID: "runner-1"})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, lock.Close()) })
	provider := newEmbeddedACPRemoteProvider(server.URL, "", workspace, store)
	provider.reuseRunner("runner-1")

	_, _, err = provider.WaitForRemoteChat(t.Context())
	require.NoError(t, err)
	metadata := lock.Metadata()
	stoppedAt := time.Now().UTC()
	metadata.StoppedAt = &stoppedAt
	require.NoError(t, lock.WriteMetadata(metadata))
	require.NoError(t, provider.refreshAdvertisedRunner())
	provider.mu.Lock()
	assert.False(t, provider.ready)
	provider.mu.Unlock()

	metadata.StoppedAt = nil
	require.NoError(t, lock.WriteMetadata(metadata))
	client, runnerID, err := provider.WaitForRemoteChat(t.Context())
	require.NoError(t, err)
	require.NotNil(t, client)
	assert.Equal(t, "runner-1", runnerID)
}

func TestEmbeddedACPRemoteProviderCanonicalizesServerURL(t *testing.T) {
	provider := newEmbeddedACPRemoteProvider(" HTTP://LOCALHOST:8080/ ", "", "", nil)
	assert.Equal(t, "http://localhost:8080", provider.server)
}

func TestValidateRemoteACPFlagsRejectsLocalLoopOverrides(t *testing.T) {
	cmd := &cobra.Command{}
	for _, name := range []string{
		"provider",
		"model",
		"max-tokens",
		"max-turns",
		"thinking-budget-tokens",
		"weak-model",
		"weak-model-max-tokens",
		"weak-reasoning-effort",
		"compact-ratio",
		"anthropic-api-access",
		"enable-openai-search",
		"profile",
	} {
		cmd.Flags().String(name, "", "")
	}
	require.NoError(t, cmd.Flags().Set("model", "local-model"))
	require.ErrorContains(t, validateRemoteACPFlags(cmd), "--model")

	cmd = &cobra.Command{}
	cmd.Flags().String("profile", "", "")
	require.NoError(t, cmd.Flags().Set("profile", "server-profile"))
	require.NoError(t, validateRemoteACPFlags(cmd))
}

func TestValidateReusedACPFlagsRejectsEmbeddedRunnerOverrides(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().Bool("no-skills", false, "")
	cmd.Flags().Bool("no-extensions", false, "")
	cmd.Flags().Bool("enable-fs-search-tools", false, "")
	require.NoError(t, cmd.Flags().Set("no-extensions", "true"))
	require.ErrorContains(t, validateReusedACPFlags(cmd), "already-running workspace runner")
}

func TestRemoteACPCommandSourceUsesOnlyLockOwner(t *testing.T) {
	runner := &runnerclient.Runner{}
	assert.Same(t, runner, remoteACPCommandSource(runner, true))
	assert.Nil(t, remoteACPCommandSource(runner, false))
}

func TestRemoteACPServiceOptionsKeepCLIRestrictionsAfterEnvironmentProfile(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("environment_profiles", map[string]any{
		"workspace": map[string]any{
			"skills":                 map[string]any{"enabled": true},
			"extensions":             map[string]any{"enabled": true},
			"enable_fs_search_tools": false,
		},
	})

	cmd := &cobra.Command{}
	cmd.Flags().Bool("no-skills", false, "")
	cmd.Flags().Bool("no-extensions", false, "")
	cmd.Flags().Bool("enable-fs-search-tools", false, "")
	require.NoError(t, cmd.Flags().Set("no-skills", "true"))
	require.NoError(t, cmd.Flags().Set("no-extensions", "true"))
	require.NoError(t, cmd.Flags().Set("enable-fs-search-tools", "true"))

	options := remoteACPServiceOptions(cmd)
	require.NotNil(t, options.ConfigLoader)
	config, err := options.ConfigLoader("workspace")
	require.NoError(t, err)
	require.NotNil(t, config.Skills)
	assert.False(t, config.Skills.Enabled)
	assert.Equal(t, false, config.ExtensionSettings["enabled"])
	assert.True(t, config.EnableFSSearchTools)
}

func TestConsumeRemoteACPAuthTokensRemovesCredentialsFromChildEnvironment(t *testing.T) {
	t.Setenv(controlPlaneAuthTokenEnv, "api-secret")
	t.Setenv(runnerAuthTokenEnv, "runner-secret")
	cmd := &cobra.Command{}
	cmd.Flags().String("auth-token", "", "")
	cmd.Flags().String("runner-auth-token", "", "")

	apiToken, runnerToken, err := consumeRemoteACPAuthTokens(cmd)

	require.NoError(t, err)
	assert.Equal(t, "api-secret", apiToken)
	assert.Equal(t, "runner-secret", runnerToken)
	assert.Empty(t, os.Getenv(controlPlaneAuthTokenEnv))
	assert.Empty(t, os.Getenv(runnerAuthTokenEnv))
}

type fakeEmbeddedRunner struct {
	err     error
	started chan struct{}
	stopped chan struct{}
	once    sync.Once
}

func (r *fakeEmbeddedRunner) Run(ctx context.Context) error {
	if r.started != nil {
		r.once.Do(func() { close(r.started) })
	}
	if r.err != nil {
		return r.err
	}
	<-ctx.Done()
	if r.stopped != nil {
		close(r.stopped)
	}
	return nil
}

func TestRunACPServerWithEmbeddedRunnerStopsRunnerWhenACPStops(t *testing.T) {
	wantErr := errors.New("ACP input closed")
	server := newFakeACPServerLifecycle()
	server.runResult <- wantErr
	runner := &fakeEmbeddedRunner{started: make(chan struct{}), stopped: make(chan struct{})}
	provider := newEmbeddedACPRemoteProvider("http://localhost:8080", "", "", nil)

	err := runACPServerWithEmbeddedRunner(t.Context(), server, runner, provider)
	require.ErrorIs(t, err, wantErr)
	assertClosed(t, runner.started)
	assertClosed(t, runner.stopped)
}

func TestRunACPServerWithEmbeddedRunnerReturnsRunnerFailure(t *testing.T) {
	wantErr := errors.New("runner authentication failed")
	server := newFakeACPServerLifecycle()
	runner := &fakeEmbeddedRunner{err: wantErr, started: make(chan struct{})}
	provider := newEmbeddedACPRemoteProvider("http://localhost:8080", "", "", nil)

	err := runACPServerWithEmbeddedRunner(t.Context(), server, runner, provider)
	require.ErrorIs(t, err, wantErr)
	assertClosed(t, runner.started)
	assertClosed(t, server.shutdown)
}
