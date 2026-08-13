package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
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
	"github.com/spf13/cobra"
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

	server, err = normalizeRunnerAPIBaseURL("http://localhost:8080/%62ase")
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:8080/base", server)

	_, err = normalizeRunnerAPIBaseURL("http://kodelet.example")
	require.ErrorContains(t, err, "require https")

	_, err = normalizeRunnerAPIBaseURL("https://kodelet.example?token=secret")
	require.ErrorContains(t, err, "only scheme, host")
}

func TestRunnerConfigsLoadAuthTokensFromEnvironment(t *testing.T) {
	setServerConfigForTest(t, "")
	t.Setenv(controlPlaneServerEnv, "")
	t.Setenv(runnerAuthTokenEnv, "runner-secret")
	t.Setenv(controlPlaneAuthTokenEnv, "control-plane-secret")

	startCmd := &cobra.Command{Use: "start"}
	startCmd.Flags().String("server", defaultRunnerServer, "")
	startCmd.Flags().String("auth-token", "", "")
	startCmd.Flags().String("name", "workspace", "")
	assert.Equal(t, runnerStartConfig{
		Server:      defaultRunnerServer,
		AuthToken:   "runner-secret",
		DisplayName: "workspace",
	}, runnerStartConfigFromFlags(startCmd))

	enrollCmd := &cobra.Command{Use: "enroll"}
	enrollCmd.Flags().String("server", defaultRunnerServer, "")
	enrollCmd.Flags().String("name", " workspace ", "")
	enrollCmd.Flags().Bool("replace", true, "")
	enrollCmd.Flags().Bool("no-browser", true, "")
	assert.Equal(t, runnerEnrollConfig{
		Server:                 defaultRunnerServer,
		DisplayName:            "workspace",
		ReplaceLocalCredential: true,
		NoBrowser:              true,
	}, runnerEnrollConfigFromFlags(enrollCmd))

	queryCmd := &cobra.Command{Use: "list"}
	queryCmd.Flags().String("server", defaultRunnerServer, "")
	queryCmd.Flags().String("auth-token", "", "")
	queryCmd.Flags().Bool("json", true, "")
	assert.Equal(t, runnerQueryConfig{
		Server:     defaultRunnerServer,
		AuthToken:  "control-plane-secret",
		JSONOutput: true,
	}, runnerQueryConfigFromFlags(queryCmd))
}

func TestConsumeRunnerStartConfigRemovesCredentialFromChildEnvironment(t *testing.T) {
	setServerConfigForTest(t, "")
	t.Setenv(controlPlaneServerEnv, "")
	t.Setenv(runnerAuthTokenEnv, "runner-secret")
	cmd := &cobra.Command{Use: "start"}
	cmd.Flags().String("server", defaultRunnerServer, "")
	cmd.Flags().String("auth-token", "", "")
	cmd.Flags().String("name", "workspace", "")

	config, err := consumeRunnerStartConfig(cmd)

	require.NoError(t, err)
	assert.Equal(t, "runner-secret", config.AuthToken)
	assert.Empty(t, os.Getenv(runnerAuthTokenEnv))
}

func TestRunRunnerEnrollStartsBrowserFlowAndSavesCredential(t *testing.T) {
	workspace := t.TempDir()
	t.Chdir(workspace)
	store, err := localstate.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	var fingerprint string
	var openedURL string
	accessToken := mustRunnerAuthAccessToken(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case protocol.EnrollmentStartPath:
			var start protocol.EnrollmentStartRequest
			require.NoError(t, json.NewDecoder(request.Body).Decode(&start))
			require.NoError(t, start.Validate())
			assert.Equal(t, "project runner", start.DisplayName)
			fingerprint = start.Fingerprint
			require.NoError(t, json.NewEncoder(w).Encode(protocol.EnrollmentStartResponse{
				EnrollmentID:            "enrollment-cli",
				DeviceCode:              accessToken,
				UserCode:                "ABCD-EFGH",
				VerificationURL:         "https://kodelet.example/runner/enroll",
				VerificationURLComplete: "https://kodelet.example/runner/enroll?user_code=ABCD-EFGH",
				ExpiresAt:               time.Now().Add(10 * time.Minute),
				PollIntervalMS:          0,
			}))
		case protocol.EnrollmentPollPath:
			require.NoError(t, json.NewEncoder(w).Encode(protocol.EnrollmentPollResponse{
				Status:       protocol.EnrollmentStatusApproved,
				CredentialID: "credential-cli",
				AccessToken:  accessToken,
				TokenType:    protocol.DPoPAuthorizationScheme,
				RunnerID:     "runner-cli",
				Fingerprint:  fingerprint,
			}))
		default:
			t.Fatalf("unexpected request path %q", request.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	var output bytes.Buffer
	require.NoError(t, runRunnerEnroll(t.Context(), runnerEnrollConfig{
		Server:      server.URL,
		DisplayName: "project runner",
		Store:       store,
		HTTPClient:  server.Client(),
		OpenBrowser: func(target string) error {
			openedURL = target
			return nil
		},
	}, &output))

	assert.Equal(t, "https://kodelet.example/runner/enroll?user_code=ABCD-EFGH", openedURL)
	rendered := output.String()
	assert.Contains(t, rendered, "Enrollment code: ABCD-EFGH")
	assert.Contains(t, rendered, "Public-key fingerprint: "+fingerprint)
	assert.Contains(t, rendered, "Runner ID: runner-cli")
	credential, found, err := store.LoadCredential(server.URL, workspace)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "credential-cli", credential.CredentialID)
	registration, found, err := store.LoadRegistration(server.URL, workspace)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "runner-cli", registration.RunnerID)
}

func TestRunnerConfigsLoadServerFromConfigAndAllowFlagOverride(t *testing.T) {
	setServerConfigForTest(t, " https://config.example/control ")
	t.Setenv(controlPlaneServerEnv, "")

	startCmd := &cobra.Command{Use: "start"}
	startCmd.Flags().String("server", defaultRunnerServer, "")
	startCmd.Flags().String("auth-token", "", "")
	startCmd.Flags().String("name", "", "")
	assert.Equal(t, "https://config.example/control", runnerStartConfigFromFlags(startCmd).Server)

	queryCmd := &cobra.Command{Use: "list"}
	queryCmd.Flags().String("server", defaultRunnerServer, "")
	queryCmd.Flags().String("auth-token", "", "")
	queryCmd.Flags().Bool("json", false, "")
	require.NoError(t, queryCmd.Flags().Set("server", " https://flag.example "))
	assert.Equal(t, "https://flag.example", runnerQueryConfigFromFlags(queryCmd).Server)
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
				ActiveRunIDs:    []string{"run-1", "run-2"},
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
	assert.Contains(t, rendered, "run-1, run-2")
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

func TestRunRunnerRemoveUsesResolvedIDAndDeletesLocalCache(t *testing.T) {
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	workspace := t.TempDir()
	runner := runnerregistry.Runner{
		ID:          "runner_remove",
		DisplayName: "project",
		Host:        protocol.Host{Hostname: "worker"},
		Workspace:   protocol.Workspace{Path: workspace, Name: "project"},
		Status:      runnerregistry.RunnerStatusOffline,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "Bearer web-secret", request.Header.Get("Authorization"))
		switch request.Method {
		case http.MethodGet:
			assert.Equal(t, "/base/api/runners", request.URL.Path)
			require.NoError(t, json.NewEncoder(w).Encode(runnerListAPIResponse{Runners: []runnerregistry.Runner{runner}}))
		case http.MethodDelete:
			assert.Equal(t, "/base/api/runners/runner_remove", request.URL.Path)
			assert.Equal(t, "true", request.URL.Query().Get("force"))
			require.NoError(t, json.NewEncoder(w).Encode(runnerregistry.RemovalResult{
				RunnerID:                      runner.ID,
				RemovedRuns:                   2,
				RemovedConversationAffinities: 1,
			}))
		default:
			t.Fatalf("unexpected method %s", request.Method)
		}
	}))
	defer server.Close()

	store, err := localstate.NewStore()
	require.NoError(t, err)
	require.NoError(t, store.SaveRegistration(localstate.Registration{
		Server:    server.URL + "/base",
		Workspace: runner.Workspace.Path,
		RunnerID:  runner.ID,
	}))
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	fingerprint, err := protocol.CredentialFingerprint(publicKey)
	require.NoError(t, err)
	require.NoError(t, store.SaveCredential(localstate.Credential{
		Server:       server.URL + "/base",
		Workspace:    runner.Workspace.Path,
		CredentialID: "credential-remove",
		AccessToken:  mustRunnerAuthAccessToken(t),
		Fingerprint:  fingerprint,
		PublicKey:    publicKey,
		PrivateKey:   privateKey,
	}))
	require.NoError(t, store.SavePendingEnrollment(localstate.PendingEnrollment{
		Server:       server.URL + "/base",
		Workspace:    runner.Workspace.Path,
		EnrollmentID: "enrollment-remove",
		DeviceCode:   mustRunnerAuthAccessToken(t),
		Fingerprint:  fingerprint,
		PublicKey:    publicKey,
		PrivateKey:   privateKey,
		ExpiresAt:    time.Now().Add(10 * time.Minute),
	}))

	var output bytes.Buffer
	require.NoError(t, runRunnerRemove(t.Context(), "project", runnerRemoveConfig{
		runnerQueryConfig: runnerQueryConfig{Server: server.URL + "/base", AuthToken: "web-secret", JSONOutput: true},
		Force:             true,
		NoConfirm:         true,
	}, strings.NewReader(""), &output))

	var result runnerregistry.RemovalResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	assert.Equal(t, 2, result.RemovedRuns)
	_, found, err := store.LoadRegistration(server.URL+"/base", runner.Workspace.Path)
	require.NoError(t, err)
	assert.False(t, found)
	_, found, err = store.LoadCredential(server.URL+"/base", runner.Workspace.Path)
	require.NoError(t, err)
	assert.False(t, found)
	_, found, err = store.LoadPendingEnrollment(server.URL+"/base", runner.Workspace.Path)
	require.NoError(t, err)
	assert.False(t, found)
}

func mustRunnerAuthAccessToken(t *testing.T) string {
	t.Helper()
	token, err := protocol.NewRunnerAccessToken()
	require.NoError(t, err)
	return token
}

func TestRunRunnerRemoveRejectsConnectedAndSurfacesControlPlaneError(t *testing.T) {
	connected := runnerregistry.Runner{
		ID:        "runner_connected",
		Connected: true,
		Host:      protocol.Host{Hostname: "worker"},
		Workspace: protocol.Workspace{Path: "/work/connected", Name: "connected"},
	}
	connectedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		require.NoError(t, json.NewEncoder(w).Encode(runnerListAPIResponse{Runners: []runnerregistry.Runner{connected}}))
	}))
	defer connectedServer.Close()

	err := runRunnerRemove(t.Context(), connected.ID, runnerRemoveConfig{
		runnerQueryConfig: runnerQueryConfig{Server: connectedServer.URL},
		NoConfirm:         true,
	}, strings.NewReader(""), &bytes.Buffer{})
	require.ErrorContains(t, err, "stop it before removal")

	offline := connected
	offline.Connected = false
	offline.ID = "runner_offline"
	conflictServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet {
			require.NoError(t, json.NewEncoder(w).Encode(runnerListAPIResponse{Runners: []runnerregistry.Runner{offline}}))
			return
		}
		w.WriteHeader(http.StatusConflict)
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"error": "runner is bound to a conversation"}))
	}))
	defer conflictServer.Close()
	err = runRunnerRemove(t.Context(), offline.ID, runnerRemoveConfig{
		runnerQueryConfig: runnerQueryConfig{Server: conflictServer.URL},
		NoConfirm:         true,
	}, strings.NewReader(""), &bytes.Buffer{})
	require.ErrorContains(t, err, "runner is bound to a conversation")
}

func TestConfirmRunnerRemovalDefaultsToNoAndExplainsTranscriptPreservation(t *testing.T) {
	runner := runnerregistry.Runner{
		ID:        "runner_confirm",
		Host:      protocol.Host{Hostname: "worker"},
		Workspace: protocol.Workspace{Path: "/work/project"},
	}
	var output bytes.Buffer
	assert.False(t, confirmRunnerRemoval(strings.NewReader("\n"), &output, runner, true))
	assert.Contains(t, output.String(), "runner run history")
	assert.Contains(t, output.String(), "Conversation transcripts are preserved")
	assert.Contains(t, output.String(), "selecting a compatible runner")
	assert.True(t, confirmRunnerRemoval(strings.NewReader("yes\n"), io.Discard, runner, false))

	err := runRunnerRemove(t.Context(), runner.ID, runnerRemoveConfig{
		runnerQueryConfig: runnerQueryConfig{JSONOutput: true},
	}, strings.NewReader("yes\n"), io.Discard)
	require.ErrorContains(t, err, "--json requires --no-confirm")
}

func TestRunRunnerStartRejectsInvalidServerBeforeConnecting(t *testing.T) {
	t.Chdir(t.TempDir())

	err := runRunnerStart(t.Context(), runnerStartConfig{Server: "://invalid"})
	require.Error(t, err)
}

func TestRenderRunnerInspectIncludesCompatibilityAndLocalLifecycle(t *testing.T) {
	connectedAt := time.Date(2026, time.August, 8, 10, 0, 0, 0, time.FixedZone("test", 2*60*60))
	stoppedAt := connectedAt.Add(time.Hour)
	result := runnerInspectOutput{
		Runner: runnerregistry.Runner{
			ID:                 "runner-inspect",
			DisplayName:        "project",
			Status:             runnerregistry.RunnerStatusIncompatible,
			Connected:          true,
			Workspace:          protocol.Workspace{Path: "/work/project"},
			Host:               protocol.Host{InstanceID: "host-1", Hostname: "worker", PID: 1234, OS: "linux", Arch: "amd64"},
			KodeletVersion:     "v1.2.3",
			ManifestDigest:     "sha256:manifest",
			ManifestChanged:    true,
			CompatibilityError: "protocol mismatch",
			ActiveRunIDs:       []string{"run-1", "run-2"},
			ConnectionID:       "connection-1",
			Generation:         7,
			ConnectedAt:        connectedAt,
			LastHeartbeatAt:    connectedAt.Add(30 * time.Second),
		},
		Local: &runnerLocalOutput{
			LockPath: "/state/runner.lock",
			Metadata: &localstate.LockMetadata{
				StartedAt: connectedAt,
				StoppedAt: &stoppedAt,
			},
		},
	}

	var output bytes.Buffer
	require.NoError(t, renderRunnerInspect(&output, result))
	rendered := output.String()
	for _, expected := range []string{
		"runner-inspect", "project", "incompatible", "protocol mismatch", "/work/project",
		"worker", "host-1", "1234", "linux/amd64", "v1.2.3", "sha256:manifest",
		"run-1", "connection-1", "7", "/state/runner.lock", "2026-08-08T08:00:00Z", "2026-08-08T09:00:00Z",
	} {
		assert.Contains(t, rendered, expected)
	}
	assert.Equal(t, "", formatRunnerTime(time.Time{}))
}

func TestRunnerListAndRemovalHTTPFailurePaths(t *testing.T) {
	t.Run("empty list", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			require.NoError(t, json.NewEncoder(w).Encode(runnerListAPIResponse{}))
		}))
		defer server.Close()

		require.NoError(t, runRunnerList(t.Context(), runnerQueryConfig{Server: server.URL}, io.Discard))
	})

	t.Run("list HTTP status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		}))
		defer server.Close()

		_, _, err := fetchRunners(t.Context(), server.URL, "")
		require.ErrorContains(t, err, "HTTP 502")
	})

	t.Run("list malformed JSON", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "not-json")
		}))
		defer server.Close()

		_, _, err := fetchRunners(t.Context(), server.URL, "")
		require.ErrorContains(t, err, "failed to decode runner list response")
	})

	t.Run("remove HTTP status without JSON error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		_, err := deleteRunner(t.Context(), server.URL, "", "runner-1", false)
		require.ErrorContains(t, err, "HTTP 500")
	})

	t.Run("remove malformed success response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "not-json")
		}))
		defer server.Close()

		_, err := deleteRunner(t.Context(), server.URL, "", "runner-1", false)
		require.ErrorContains(t, err, "failed to decode runner removal response")
	})
}

func TestSelectRunnerRejectsEmptyAndUnknownSelectors(t *testing.T) {
	_, err := selectRunner(nil, " ")
	require.ErrorContains(t, err, "runner selector is required")
	_, err = selectRunner(nil, "missing")
	require.ErrorContains(t, err, "runner not found: missing")

	assert.Equal(t, "workspace", runnerDisplayName(runnerregistry.Runner{Workspace: protocol.Workspace{Name: "workspace"}}))
}
