package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/runner/localstate"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeRunnerAPIBaseURL(t *testing.T) {
	server, err := normalizeRunnerAPIBaseURL(" http://localhost:8080/base/ ")
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:8080/base", server)

	server, err = normalizeRunnerAPIBaseURL("https://kodelet.example/control")
	require.NoError(t, err)
	assert.Equal(t, "https://kodelet.example/control", server)

	_, err = normalizeRunnerAPIBaseURL("http://kodelet.example")
	require.ErrorContains(t, err, "require https")

	_, err = normalizeRunnerAPIBaseURL("https://kodelet.example?token=secret")
	require.ErrorContains(t, err, "only scheme, host")
}

func TestScrubRunnerCredentialEnvironment(t *testing.T) {
	t.Setenv("KODELET_RUNNER_AUTH_TOKEN", "runner-secret")
	t.Setenv("KODELET_AUTH_TOKEN", "client-secret")

	scrubRunnerCredentialEnvironment()

	assert.Empty(t, os.Getenv("KODELET_RUNNER_AUTH_TOKEN"))
	assert.Empty(t, os.Getenv("KODELET_AUTH_TOKEN"))
}

func TestSelectRunnerSupportsStableIDPrefixAndDisplayName(t *testing.T) {
	runners := []runnerregistry.Runner{
		{
			ID:          "runner_alpha",
			DisplayName: "project",
			Host:        protocol.Host{Hostname: "host-a"},
			Workspace:   protocol.Workspace{Path: "/work/a", Name: "a"},
		},
		{
			ID:          "runner_beta",
			DisplayName: "project",
			Host:        protocol.Host{Hostname: "host-b"},
			Workspace:   protocol.Workspace{Path: "/work/b", Name: "b"},
		},
	}

	selected, err := selectRunner(runners, "runner_alpha")
	require.NoError(t, err)
	assert.Equal(t, "runner_alpha", selected.ID)

	selected, err = selectRunner(runners, "runner_b")
	require.NoError(t, err)
	assert.Equal(t, "runner_beta", selected.ID)

	_, err = selectRunner(runners, "project")
	require.ErrorContains(t, err, "ambiguous")
	assert.ErrorContains(t, err, "host-a")
	assert.ErrorContains(t, err, "/work/b")
}

func TestRunRunnerListQueriesControlPlaneAndRendersStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "/base/api/runners", request.URL.Path)
		assert.Equal(t, "Bearer web-secret", request.Header.Get("Authorization"))
		require.NoError(t, json.NewEncoder(w).Encode(runnerListAPIResponse{Runners: []runnerregistry.Runner{
			{
				ID:             "runner_b",
				Host:           protocol.Host{Hostname: "host-b"},
				Workspace:      protocol.Workspace{Path: "/work/b", Name: "b"},
				KodeletVersion: "v2",
				Status:         runnerregistry.RunnerStatusOffline,
			},
			{
				ID:              "runner_a",
				DisplayName:     "primary",
				Host:            protocol.Host{Hostname: "host-a"},
				Workspace:       protocol.Workspace{Path: "/work/a", Name: "a"},
				KodeletVersion:  "v1",
				Status:          runnerregistry.RunnerStatusBusy,
				ActiveRunID:     "run-1",
				LastHeartbeatAt: time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC),
			},
		}}))
	}))
	defer server.Close()

	var output bytes.Buffer
	err := runRunnerList(t.Context(), runnerQueryConfig{
		Server:    server.URL + "/base",
		AuthToken: "web-secret",
	}, &output)

	require.NoError(t, err)
	rendered := output.String()
	assert.Contains(t, rendered, "runner_a")
	assert.Contains(t, rendered, "primary")
	assert.Contains(t, rendered, "busy")
	assert.Contains(t, rendered, "run-1")
	assert.Less(t, strings.Index(rendered, "runner_a"), strings.Index(rendered, "runner_b"))
}

func TestRunRunnerInspectIncludesLocalLockDiagnostics(t *testing.T) {
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	workspace := filepath.Join(t.TempDir(), "workspace")
	require.NoError(t, os.MkdirAll(workspace, 0o755))
	workspace, err := localstate.CanonicalWorkspace(workspace)
	require.NoError(t, err)

	runner := runnerregistry.Runner{
		ID:          "runner_local",
		DisplayName: "local-project",
		Host:        protocol.Host{InstanceID: "host-1", Hostname: "localhost", OS: "linux", Arch: "amd64", PID: 4242},
		Workspace:   protocol.Workspace{Path: workspace, Name: "workspace"},
		Status:      runnerregistry.RunnerStatusIdle,
		Connected:   true,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "/api/runners", request.URL.Path)
		require.NoError(t, json.NewEncoder(w).Encode(runnerListAPIResponse{Runners: []runnerregistry.Runner{runner}}))
	}))
	defer server.Close()

	store, err := localstate.NewStore()
	require.NoError(t, err)
	require.NoError(t, store.SaveRegistration(localstate.Registration{
		Server:    server.URL,
		Workspace: workspace,
		RunnerID:  runner.ID,
	}))
	lock, err := store.AcquireWorkspaceLock(workspace, localstate.LockMetadata{
		PID:       runner.Host.PID,
		Hostname:  runner.Host.Hostname,
		Workspace: workspace,
		Server:    server.URL,
		RunnerID:  runner.ID,
		StartedAt: time.Date(2026, time.August, 6, 11, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, lock.Close()) })

	var output bytes.Buffer
	require.NoError(t, runRunnerInspect(t.Context(), runner.ID, runnerQueryConfig{
		Server:     server.URL,
		JSONOutput: true,
	}, &output))

	var result runnerInspectOutput
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	assert.Equal(t, runner.ID, result.Runner.ID)
	require.NotNil(t, result.Local)
	assert.Equal(t, store.WorkspaceLockPath(workspace), result.Local.LockPath)
	require.NotNil(t, result.Local.Metadata)
	assert.Equal(t, 4242, result.Local.Metadata.PID)
	assert.Equal(t, runner.ID, result.Local.Metadata.RunnerID)
}
