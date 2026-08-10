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

	provider := newEmbeddedACPRemoteProvider(server.URL, "api-secret")
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

	provider := newEmbeddedACPRemoteProvider(server.URL, "")
	provider.registeredRunner(protocol.RegisterResult{RunnerID: "runner-1"})
	_, _, err := provider.WaitForRemoteChat(t.Context())
	require.ErrorContains(t, err, "protocol mismatch")
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
	provider := newEmbeddedACPRemoteProvider("http://localhost:8080", "")

	err := runACPServerWithEmbeddedRunner(t.Context(), server, runner, provider)
	require.ErrorIs(t, err, wantErr)
	assertClosed(t, runner.started)
	assertClosed(t, runner.stopped)
}

func TestRunACPServerWithEmbeddedRunnerReturnsRunnerFailure(t *testing.T) {
	wantErr := errors.New("runner authentication failed")
	server := newFakeACPServerLifecycle()
	runner := &fakeEmbeddedRunner{err: wantErr, started: make(chan struct{})}
	provider := newEmbeddedACPRemoteProvider("http://localhost:8080", "")

	err := runACPServerWithEmbeddedRunner(t.Context(), server, runner, provider)
	require.ErrorIs(t, err, wantErr)
	assertClosed(t, runner.started)
	assertClosed(t, server.shutdown)
}
