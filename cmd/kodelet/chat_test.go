package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	chatpkg "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/controlplane"
	"github.com/jingkaihe/kodelet/pkg/messagehistory"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	"github.com/jingkaihe/kodelet/pkg/tui"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetChatConfigFromFlags(t *testing.T) {
	cmd := &cobra.Command{Use: "chat"}
	defaults := NewChatConfig()
	cmd.Flags().StringP("resume", "r", defaults.ResumeConvID, "")
	cmd.Flags().String("cwd", defaults.CWD, "")
	cmd.Flags().String("theme", tui.AutoThemeName, "")
	cmd.Flags().BoolP("follow", "f", defaults.Follow, "")
	cmd.Flags().Bool("no-extensions", defaults.NoExtensions, "")
	cmd.Flags().Bool("no-tools", defaults.NoTools, "")
	cmd.Flags().String("runner", defaults.Runner, "")
	cmd.Flags().String("runner-profile", defaults.RunnerProfile, "")
	cmd.Flags().String("server", defaults.Server, "")
	cmd.Flags().String("auth-token", defaults.AuthToken, "")

	require.NoError(t, cmd.Flags().Set("resume", "conv-1"))
	require.NoError(t, cmd.Flags().Set("cwd", " /tmp/project "))
	require.NoError(t, cmd.Flags().Set("theme", " tokyo-night "))
	require.NoError(t, cmd.Flags().Set("no-extensions", "true"))
	require.NoError(t, cmd.Flags().Set("no-tools", "true"))
	require.NoError(t, cmd.Flags().Set("runner", " runner-1 "))
	require.NoError(t, cmd.Flags().Set("runner-profile", " workspace "))

	config := getChatConfigFromFlags(cmd)

	assert.Equal(t, "conv-1", config.ResumeConvID)
	assert.Equal(t, "/tmp/project", config.CWD)
	assert.Equal(t, "tokyo-night", config.Theme)
	assert.True(t, config.NoExtensions)
	assert.True(t, config.NoTools)
	assert.Equal(t, "runner-1", config.Runner)
	assert.Equal(t, "workspace", config.RunnerProfile)
}

func TestGetChatConfigFromFlagsLoadsAuthTokenFromEnvironment(t *testing.T) {
	t.Setenv(controlPlaneAuthTokenEnv, " control-plane-secret ")
	cmd := &cobra.Command{Use: "chat"}
	cmd.Flags().String("auth-token", "", "")

	config := getChatConfigFromFlags(cmd)

	assert.Equal(t, "control-plane-secret", config.AuthToken)
}

func TestGetChatConfigFromFlagsLoadsServerFromConfig(t *testing.T) {
	setServerConfigForTest(t, " https://kodelet.example/control ")
	t.Setenv(controlPlaneServerEnv, "")
	cmd := &cobra.Command{Use: "chat"}
	cmd.Flags().String("server", defaultRunnerServer, "")
	cmd.Flags().String("auth-token", "", "")

	config := getChatConfigFromFlags(cmd)

	assert.Equal(t, "https://kodelet.example/control", config.Server)
	assert.True(t, config.ServerConfigured)
}

func TestChatResumeShortFlag(t *testing.T) {
	cmd := &cobra.Command{Use: "chat"}
	defaults := NewChatConfig()
	cmd.Flags().StringP("resume", "r", defaults.ResumeConvID, "")
	cmd.Flags().String("cwd", defaults.CWD, "")
	cmd.Flags().String("theme", tui.AutoThemeName, "")
	cmd.Flags().BoolP("follow", "f", defaults.Follow, "")
	cmd.Flags().Bool("no-extensions", defaults.NoExtensions, "")
	cmd.Flags().Bool("no-tools", defaults.NoTools, "")

	require.NoError(t, cmd.ParseFlags([]string{"-r", "conv-short"}))

	config := getChatConfigFromFlags(cmd)
	assert.Equal(t, "conv-short", config.ResumeConvID)
}

func TestChatFollowShortFlag(t *testing.T) {
	cmd := &cobra.Command{Use: "chat"}
	defaults := NewChatConfig()
	cmd.Flags().StringP("resume", "r", defaults.ResumeConvID, "")
	cmd.Flags().String("cwd", defaults.CWD, "")
	cmd.Flags().String("theme", tui.AutoThemeName, "")
	cmd.Flags().BoolP("follow", "f", defaults.Follow, "")
	cmd.Flags().Bool("no-extensions", defaults.NoExtensions, "")
	cmd.Flags().Bool("no-tools", defaults.NoTools, "")

	require.NoError(t, cmd.ParseFlags([]string{"-f"}))

	config := getChatConfigFromFlags(cmd)
	assert.True(t, config.Follow)
}

func TestChatNoToolsFlag(t *testing.T) {
	cmd := &cobra.Command{Use: "chat"}
	defaults := NewChatConfig()
	cmd.Flags().StringP("resume", "r", defaults.ResumeConvID, "")
	cmd.Flags().String("cwd", defaults.CWD, "")
	cmd.Flags().String("theme", tui.AutoThemeName, "")
	cmd.Flags().BoolP("follow", "f", defaults.Follow, "")
	cmd.Flags().Bool("no-extensions", defaults.NoExtensions, "")
	cmd.Flags().Bool("no-tools", defaults.NoTools, "")

	require.NoError(t, cmd.Flags().Set("no-tools", "true"))

	config := getChatConfigFromFlags(cmd)
	assert.True(t, config.NoTools)
}

func TestChatRestrictionsBecomeTypedOptions(t *testing.T) {
	cmd := remoteRunCommandForTest()
	require.NoError(t, cmd.ParseFlags([]string{"--no-extensions", "--no-tools=false", "--allowed-tools=", "--max-turns=0", "--auth-token=client"}))
	config := getChatConfigFromFlags(cmd)
	require.NoError(t, config.ConfigError)
	assert.Equal(t, &llmtypes.ExecutionOptions{NoExtensions: new(true), NoTools: new(false), AllowedTools: new([]string{}), MaxTurns: new(0)}, config.Options)
}

func TestPrepareRemoteChatRunnerSelectsAvailableRunner(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "/api/runners", request.URL.Path)
		assert.Equal(t, "Bearer secret", request.Header.Get("Authorization"))
		require.NoError(t, json.NewEncoder(w).Encode(runnerListAPIResponse{Runners: []runnerregistry.Runner{{
			ID:             "runner-1",
			DisplayName:    "kodelet-gpu",
			Host:           protocol.Host{Hostname: "worker"},
			Workspace:      protocol.Workspace{Path: "/runner/kodelet", Name: "kodelet"},
			Status:         runnerregistry.RunnerStatusBusy,
			ConcurrentRuns: true,
			ActiveRunID:    "run-1",
			Connected:      true,
		}}}))
	}))
	defer server.Close()

	runner, workspace, err := prepareRemoteChatRunner(t.Context(), &ChatConfig{
		Runner:       "kodelet-gpu",
		Server:       server.URL,
		AuthToken:    "secret",
		ResumeConvID: "conversation-1",
		CWD:          "/runner/other-project",
		NoTools:      true,
		NoExtensions: true,
	})

	require.NoError(t, err)
	assert.NotNil(t, runner)
	assert.Equal(t, "/runner/kodelet", workspace)
}

func TestPrepareRemoteChatRunnerRejectsBusyLegacyRunner(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		require.NoError(t, json.NewEncoder(w).Encode(runnerListAPIResponse{Runners: []runnerregistry.Runner{{
			ID:        "runner-1",
			Workspace: protocol.Workspace{Path: "/runner/kodelet", Name: "kodelet"},
			Status:    runnerregistry.RunnerStatusBusy,
			Connected: true,
		}}}))
	}))
	defer server.Close()

	_, _, err := prepareRemoteChatRunner(t.Context(), &ChatConfig{Runner: "runner-1", Server: server.URL})
	require.ErrorContains(t, err, "does not support concurrent runs")
}

func TestPrepareServerChatRunner(t *testing.T) {
	setServerConfigForTest(t, "")
	t.Setenv(controlPlaneServerEnv, "")
	cmd := &cobra.Command{Use: "chat"}
	cmd.Flags().String("server", defaultRunnerServer, "")
	cmd.Flags().String("auth-token", "", "")
	config := getChatConfigFromFlags(cmd)
	runner, err := prepareServerChatRunner(config)
	require.NoError(t, err)
	assert.NotNil(t, runner)

	require.NoError(t, cmd.Flags().Set("server", "http://localhost:8080"))
	config = getChatConfigFromFlags(cmd)
	runner, err = prepareServerChatRunner(config)
	require.NoError(t, err)
	assert.NotNil(t, runner)
	runner, err = prepareServerChatRunner(&ChatConfig{Server: defaultRunnerServer, CWD: "/tmp/project"})
	require.NoError(t, err)
	assert.NotNil(t, runner)
	_, err = prepareServerChatRunner(&ChatConfig{Server: defaultRunnerServer, NoTools: true})
	require.NoError(t, err)
}

func TestPrepareRemoteChatSettingsUsesControlPlaneProfiles(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "/api/chat/settings", request.URL.Path)
		profile := request.URL.Query().Get("profile")
		response := chatpkg.ControlPlaneChatSettings{
			CurrentProfile: "work",
			Profiles: []chatpkg.ControlPlaneProfileOption{
				{Name: "default"},
				{Name: "work"},
			},
			ReasoningEffort:        "high",
			ReasoningEffortOptions: []string{"medium", "high"},
			DefaultCWD:             "/control-plane/workspace",
		}
		if profile == "default" {
			response.CurrentProfile = "default"
			response.ReasoningEffort = "medium"
			response.ReasoningEffortOptions = []string{"low", "medium"}
		}
		require.NoError(t, json.NewEncoder(w).Encode(response))
	}))
	defer server.Close()
	runner, err := chatpkg.NewClient(server.URL, "", "runner-1")
	require.NoError(t, err)

	profile, options, settings, defaultCWD, err := prepareRemoteChatSettings(t.Context(), runner, "work")
	require.NoError(t, err)
	assert.Equal(t, "work", profile)
	assert.Equal(t, []string{"default", "work"}, options)
	assert.Equal(t, "high", settings["work"].ReasoningEffort)
	assert.Equal(t, []string{"low", "medium"}, settings["default"].ReasoningEffortOptions)
	assert.Equal(t, "/control-plane/workspace", defaultCWD)
	require.NoError(t, validateRemoteReasoningEffort("high", settings["work"].ReasoningEffortOptions))
	require.ErrorContains(t, validateRemoteReasoningEffort("max", settings["work"].ReasoningEffortOptions), "not allowed")
}

func daemonChatCommandForTest(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	cmd := remoteRunCommandForTest()
	cmd.Use = "chat"
	cmd.Flags().String("theme", tui.AutoThemeName, "")
	require.NoError(t, cmd.ParseFlags(append([]string{"--auth-token=client"}, args...)))
	return cmd
}

func TestPrepareDaemonChatUsesConnectedServerURL(t *testing.T) {
	for _, test := range []struct {
		name, prefix string
		local        bool
	}{
		{name: "normalized explicit server with base path", prefix: "/kodelet"},
		{name: "discovered local daemon with assigned port", local: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := localServerTestState(t)
			forbidLocalServerSpawn(t)
			var statusCalls atomic.Int32
			daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "Bearer client", r.Header.Get("Authorization"))
				switch r.URL.Path {
				case test.prefix + "/api/status":
					statusCalls.Add(1)
					require.NoError(t, json.NewEncoder(w).Encode(localServerStatus{
						APIReady:   true,
						InstanceID: "chat",
						EmbeddedRunner: controlplane.EmbeddedRunnerStatus{
							Enabled: true,
							Ready:   true,
						},
					}))
				case test.prefix + "/api/chat/settings":
					require.NoError(t, json.NewEncoder(w).Encode(chatpkg.ControlPlaneChatSettings{
						DefaultRunnerID:    "runner",
						DefaultRunnerReady: true,
					}))
				case test.prefix + "/api/chat/cwd-suggestions":
					require.NoError(t, json.NewEncoder(w).Encode(protocol.WorkspaceCWDHintsResult{BaseDir: "/workspace"}))
				default:
					http.NotFound(w, r)
				}
			}))
			defer daemon.Close()
			var args []string
			if test.local {
				lock, err := tryLocalServerLock(directory, "server.lock")
				require.NoError(t, err)
				require.NotNil(t, lock)
				defer lock.Close()
				require.NoError(t, publishLocalServer(directory, daemon.URL, "client", "chat", true))
			} else {
				args = []string{"--server=" + daemon.URL + test.prefix + "//./"}
			}
			config, err := prepareDaemonChat(t.Context(), daemonChatCommandForTest(t, args...))
			require.NoError(t, err)
			assert.Equal(t, daemon.URL+test.prefix, config.ServerURL)
			assert.Equal(t, "/workspace", config.CWD)
			if test.local {
				assert.Positive(t, statusCalls.Load(), "chat must connect through local daemon discovery")
			} else {
				assert.Zero(t, statusCalls.Load(), "explicit servers must not use local daemon discovery")
			}
		})
	}
}

func TestPrepareDaemonChatUsesRunnerDirectoriesAndTypedRestrictions(t *testing.T) {
	var submissions []chatpkg.ChatRequest
	var discoveries atomic.Int32
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer client", r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/api/chat/settings":
			require.NoError(t, json.NewEncoder(w).Encode(chatpkg.ControlPlaneChatSettings{CurrentProfile: "daemon", DefaultRunnerID: "runner", DefaultRunnerReady: true, ReasoningEffort: "medium", ReasoningEffortOptions: []string{"medium"}}))
		case "/api/chat/cwd-suggestions":
			assert.Equal(t, "runner", r.URL.Query().Get("runnerId"))
			assert.Equal(t, "daemon", r.URL.Query().Get("profile"))
			assert.Equal(t, "environment", r.URL.Query().Get("environmentProfile"))
			assert.False(t, r.URL.Query().Has("options"))
			require.NoError(t, json.NewEncoder(w).Encode(protocol.WorkspaceCWDHintsResult{BaseDir: "/runner-only/selected"}))
		case "/api/chat/slash-commands":
			discoveries.Add(1)
			assert.Equal(t, "runner", r.URL.Query().Get("runnerId"))
			assert.Equal(t, "daemon", r.URL.Query().Get("profile"))
			var options llmtypes.ExecutionOptions
			require.NoError(t, json.Unmarshal([]byte(r.URL.Query().Get("options")), &options))
			assert.Equal(t, new(true), options.NoExtensions)
			assert.Equal(t, new(true), options.NoSkills)
			assert.Nil(t, options.Model)
			require.NoError(t, json.NewEncoder(w).Encode(protocol.WorkspaceDiscoverResult{CWD: "/runner-only/selected", EnvironmentProfile: "environment"}))
		case "/api/chat":
			var request chatpkg.ChatRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			submissions = append(submissions, request)
			w.Header().Set("Content-Type", "application/x-ndjson")
			require.NoError(t, json.NewEncoder(w).Encode(chatpkg.ChatEvent{Kind: "done", ConversationID: request.ConversationID}))
		default:
			http.NotFound(w, r)
		}
	}))
	defer daemon.Close()
	invalidStore := filepath.Join(t.TempDir(), "no-local-state")
	require.NoError(t, os.WriteFile(invalidStore, []byte("not a directory"), 0o600))
	t.Setenv("KODELET_BASE_PATH", invalidStore)
	cmd := daemonChatCommandForTest(t, "--server="+daemon.URL, "--cwd=~/selected", "--runner-profile=environment", "--model=central", "--no-extensions", "--no-skills", "--allowed-tools=", "--no-tools=false", "--max-turns=0")
	config, err := prepareDaemonChat(t.Context(), cmd)
	require.NoError(t, err)
	assert.True(t, config.Remote)
	assert.Equal(t, "/runner-only/selected", config.CWD)
	assert.Equal(t, "daemon", config.Profile)
	assert.Equal(t, "environment", config.EnvironmentProfile)
	assert.Empty(t, submissions, "preparing the TUI must not start a provider turn")
	assert.Zero(t, discoveries.Load(), "preparing chat must not initialize extensions for command discovery")
	_, err = config.Runner.Run(t.Context(), chatpkg.ChatRequest{ConversationID: "new", TurnID: "turn", Message: "work", CWD: config.CWD, Profile: config.Profile}, &remoteRunSink{output: io.Discard, diagnostics: io.Discard})
	require.NoError(t, err)
	require.Len(t, submissions, 1)
	assert.Equal(t, "runner", submissions[0].RunnerID)
	assert.Equal(t, "/runner-only/selected", submissions[0].CWD)
	assert.Equal(t, new("central"), submissions[0].Options.Model)
	assert.Equal(t, new(false), submissions[0].Options.NoTools)
	assert.Equal(t, new(0), submissions[0].Options.MaxTurns)
	assert.Equal(t, new([]string{}), submissions[0].Options.AllowedTools)
	assert.EqualValues(t, 1, discoveries.Load())
	_, ok := config.Runner.(chatpkg.ConversationStreamer)
	assert.True(t, ok, "wrapper promotes shared stream and UI transport methods")
}

func TestPrepareDaemonChatResumeDoesNotRequireCurrentDefaultRunner(t *testing.T) {
	var resolutions atomic.Int32
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/conversations/saved":
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"id": "saved", "cwd": "/runner-only/saved", "runnerId": "stored", "profile": "removed-profile", "environmentProfile": "stored-environment", "reasoningEffort": "high"}))
		case "/api/chat/cwd-suggestions":
			resolutions.Add(1)
			assert.Equal(t, "saved", r.URL.Query().Get("conversationId"))
			assert.False(t, r.URL.Query().Has("profile"), "saved discovery must use the server's pinned profile")
			assert.False(t, r.URL.Query().Has("options"))
			require.NoError(t, json.NewEncoder(w).Encode(protocol.WorkspaceCWDHintsResult{BaseDir: "/runner-only/saved"}))
		default:
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
			http.Error(w, "current default unavailable", http.StatusServiceUnavailable)
		}
	}))
	defer daemon.Close()
	config, err := prepareDaemonChat(t.Context(), daemonChatCommandForTest(t, "--server="+daemon.URL, "--resume=saved"))
	require.NoError(t, err)
	assert.Equal(t, "removed-profile", config.Profile)
	assert.Equal(t, "high", config.ReasoningEffort)
	assert.Equal(t, "/runner-only/saved", config.CWD)
	for _, flags := range [][]string{{"--cwd=/replacement"}, {"--profile=replacement"}, {"--runner-profile="}, {"--reasoning-effort=low"}} {
		_, err := prepareDaemonChat(t.Context(), daemonChatCommandForTest(t, append([]string{"--server=" + daemon.URL, "--resume=saved"}, flags...)...))
		require.Error(t, err)
	}
	assert.EqualValues(t, 1, resolutions.Load(), "resume replacements fail before directory resolution")
}

func TestPrepareDaemonChatFollowResolvesDirectoryWithoutLoadingExtensions(t *testing.T) {
	var resolutions atomic.Int32
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/chat/settings":
			require.NoError(t, json.NewEncoder(w).Encode(chatpkg.ControlPlaneChatSettings{DefaultRunnerID: "runner", DefaultRunnerReady: true}))
		case "/api/chat/cwd-suggestions":
			resolutions.Add(1)
			assert.False(t, r.URL.Query().Has("options"))
			if r.URL.Query().Get("conversationId") == "" {
				assert.Equal(t, "/runner-only/selected", r.URL.Query().Get("cwd"))
				assert.Equal(t, "runner", r.URL.Query().Get("runnerId"))
			} else {
				assert.Equal(t, "saved", r.URL.Query().Get("conversationId"))
			}
			require.NoError(t, json.NewEncoder(w).Encode(protocol.WorkspaceCWDHintsResult{BaseDir: "/runner-only/selected"}))
		case "/api/conversations":
			assert.Equal(t, "/runner-only/selected", r.URL.Query().Get("cwd"))
			assert.Equal(t, "1", r.URL.Query().Get("limit"))
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"conversations": []convtypes.ConversationSummary{{ID: "saved"}}}))
		case "/api/conversations/saved":
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"id": "saved", "cwd": "/runner-only/selected", "runnerId": "runner", "profile": "default", "reasoningEffort": "medium"}))
		default:
			assert.Fail(t, "unexpected endpoint during chat preparation", r.URL.Path)
			http.Error(w, "must not load extensions", http.StatusInternalServerError)
		}
	}))
	defer daemon.Close()

	config, err := prepareDaemonChat(t.Context(), daemonChatCommandForTest(t, "--server="+daemon.URL, "--follow", "--cwd=/runner-only/selected", "--no-extensions"))
	require.NoError(t, err)
	assert.Equal(t, "saved", config.ConversationID)
	assert.Equal(t, "/runner-only/selected", config.CWD)
	assert.EqualValues(t, 2, resolutions.Load())
}

func TestPrepareDaemonChatRejectsUnsupportedFlagsBeforeHTTP(t *testing.T) {
	var calls atomic.Int32
	daemon := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer daemon.Close()
	for _, flags := range [][]string{{"--sysprompt=/client/prompt"}, {"--max-turns=-1"}, {"--follow", "--resume=saved"}, {"--follow"}} {
		_, err := prepareDaemonChat(t.Context(), daemonChatCommandForTest(t, append([]string{"--server=" + daemon.URL}, flags...)...))
		require.Error(t, err)
	}
	assert.Zero(t, calls.Load())
}

func TestDaemonChatProcessFailsWithoutLocalFallback(t *testing.T) {
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "daemon unavailable", http.StatusServiceUnavailable)
	}))
	defer daemon.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	root := t.TempDir()
	invalidStore := filepath.Join(root, "no-local-state")
	require.NoError(t, os.WriteFile(invalidStore, []byte("not a directory"), 0o600))
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "KODELET_TEST_CLI_PROCESS=1", "KODELET_BASE_PATH=" + invalidStore, "KODELET_SERVER=" + daemon.URL}
	process := daemonCLIProcess(ctx, t, root, env, "chat", "--auth-token=client")
	output, err := process.CombinedOutput()
	require.Error(t, err)
	assert.Contains(t, string(output), "could not start chat")
	assert.NotContains(t, strings.ToLower(string(output)), "database")
}

func TestConfiguredChatRunnerMessageHistoryUsesSelectedTarget(t *testing.T) {
	for _, scenario := range []string{"configured", "explicit", "default", "saved", "unavailable-default"} {
		t.Run(scenario, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/chat/settings":
					assert.Contains(t, []string{"default", "unavailable-default"}, scenario)
					require.NoError(t, json.NewEncoder(w).Encode(chatpkg.ControlPlaneChatSettings{DefaultRunnerID: "default", DefaultRunnerReady: scenario == "default"}))
				case "/api/chat/message-history":
					calls.Add(1)
					assert.False(t, r.URL.Query().Has("options"), "history access must not execute extensions or carry run restrictions")
					if scenario == "saved" {
						assert.Equal(t, "saved", r.URL.Query().Get("conversationId"))
						assert.False(t, r.URL.Query().Has("runnerId"))
						assert.False(t, r.URL.Query().Has("cwd"))
						assert.False(t, r.URL.Query().Has("environmentProfile"))
					} else {
						assert.Equal(t, scenario, r.URL.Query().Get("runnerId"))
						assert.Equal(t, "/runner/project", r.URL.Query().Get("cwd"))
						assert.Equal(t, "environment", r.URL.Query().Get("environmentProfile"))
					}
					require.NoError(t, json.NewEncoder(w).Encode(protocol.WorkspaceMessageHistoryResult{CWD: "/runner/project", ScopeCWD: "/runner/project", Messages: []string{"previous raw message"}}))
				default:
					assert.Fail(t, "unexpected history endpoint", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client, err := chatpkg.NewClient(server.URL, "", "wrong-promoted-runner")
			require.NoError(t, err)
			runner := &configuredChatRunner{Client: client, runnerID: "configured", defaultCWD: "/runner/project", environmentProfile: "environment", options: &llmtypes.ExecutionOptions{NoExtensions: new(true)}}
			target := chatpkg.WorkspaceTarget{}
			switch scenario {
			case "explicit":
				target.RunnerID = "explicit"
			case "default", "unavailable-default":
				runner.runnerID = ""
			case "saved":
				target.ConversationID = "saved"
			}
			result, loadErr := runner.LoadMessageHistory(t.Context(), target)
			appendErr := runner.AppendMessageHistory(t.Context(), target, messagehistory.Entry{Text: "raw composer message"})
			if scenario == "unavailable-default" {
				require.ErrorContains(t, loadErr, "default runner is unavailable")
				require.ErrorContains(t, appendErr, "default runner is unavailable")
				assert.Zero(t, calls.Load())
			} else {
				require.NoError(t, loadErr)
				require.NoError(t, appendErr)
				assert.Equal(t, []string{"previous raw message"}, result.Messages)
				assert.EqualValues(t, 2, calls.Load())
			}
		})
	}
}
