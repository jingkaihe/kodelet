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
	assert.True(t, usesControlPlaneChat(config))
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

func TestPrepareServerChatRunnerAndModeSelection(t *testing.T) {
	setServerConfigForTest(t, "")
	t.Setenv(controlPlaneServerEnv, "")
	cmd := &cobra.Command{Use: "chat"}
	cmd.Flags().String("server", defaultRunnerServer, "")
	cmd.Flags().String("auth-token", "", "")
	config := getChatConfigFromFlags(cmd)
	assert.True(t, usesControlPlaneChat(config), "even the implicit default endpoint is daemon-backed")
	require.NoError(t, cmd.Flags().Set("server", "http://localhost:8080"))
	config = getChatConfigFromFlags(cmd)
	assert.True(t, usesControlPlaneChat(config))

	runner, err := prepareServerChatRunner(config)
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
	runner, err := chatpkg.NewControlPlaneChatRunner(server.URL, "", "runner-1")
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

type staticChatConversationSource struct {
	summaries []convtypes.ConversationSummary
	err       error
}

func (s staticChatConversationSource) ListConversations(context.Context, int) ([]convtypes.ConversationSummary, error) {
	return s.summaries, s.err
}

func (staticChatConversationSource) LoadConversation(context.Context, string) (chatpkg.ConversationHistory, error) {
	return chatpkg.ConversationHistory{}, nil
}

func TestResolveFollowConversationUsesSelectedSource(t *testing.T) {
	id, err := resolveFollowConversation(t.Context(), staticChatConversationSource{summaries: []convtypes.ConversationSummary{{ID: "conversation-latest"}}})
	require.NoError(t, err)
	assert.Equal(t, "conversation-latest", id)

	_, err = resolveFollowConversation(t.Context(), staticChatConversationSource{})
	require.ErrorContains(t, err, "no conversations")
}

func TestResolveFollowConversationRejectsNilSourceWithoutLocalStore(t *testing.T) {
	var runner *chatpkg.ControlPlaneChatRunner
	_, err := resolveFollowConversation(t.Context(), runner)
	require.ErrorContains(t, err, "conversation history is unavailable")
}

func daemonChatCommandForTest(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	cmd := remoteRunCommandForTest()
	cmd.Use = "chat"
	cmd.Flags().String("theme", tui.AutoThemeName, "")
	require.NoError(t, cmd.ParseFlags(append([]string{"--auth-token=client"}, args...)))
	return cmd
}

func TestPrepareDaemonChatUsesRunnerDirectoriesAndTypedRestrictions(t *testing.T) {
	var submissions []chatpkg.ChatRequest
	var discoveries atomic.Int32
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer client", r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/api/chat/settings":
			require.NoError(t, json.NewEncoder(w).Encode(chatpkg.ControlPlaneChatSettings{CurrentProfile: "daemon", DefaultRunnerID: "runner", DefaultRunnerReady: true, ReasoningEffort: "medium", ReasoningEffortOptions: []string{"medium"}}))
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
	cmd := daemonChatCommandForTest(t, "--server="+daemon.URL, "--cwd=~/selected", "--model=central", "--no-extensions", "--no-skills", "--allowed-tools=", "--no-tools=false", "--max-turns=0")
	config, err := prepareDaemonChat(t.Context(), cmd)
	require.NoError(t, err)
	assert.True(t, config.Remote)
	assert.Equal(t, "/runner-only/selected", config.CWD)
	assert.Equal(t, "daemon", config.Profile)
	assert.Equal(t, "environment", config.EnvironmentProfile)
	assert.Empty(t, submissions, "preparing the TUI must not start a provider turn")
	_, err = config.Runner.Run(t.Context(), chatpkg.ChatRequest{ConversationID: "new", TurnID: "turn", Message: "work", CWD: config.CWD, Profile: config.Profile}, &remoteRunSink{output: io.Discard, diagnostics: io.Discard})
	require.NoError(t, err)
	require.Len(t, submissions, 1)
	assert.Equal(t, "runner", submissions[0].RunnerID)
	assert.Equal(t, "/runner-only/selected", submissions[0].CWD)
	assert.Equal(t, new("central"), submissions[0].Options.Model)
	assert.Equal(t, new(false), submissions[0].Options.NoTools)
	assert.Equal(t, new(0), submissions[0].Options.MaxTurns)
	assert.Equal(t, new([]string{}), submissions[0].Options.AllowedTools)
	assert.EqualValues(t, 2, discoveries.Load())
	_, ok := config.Runner.(chatpkg.ConversationStreamer)
	assert.True(t, ok, "wrapper promotes shared stream and UI transport methods")
}

func TestPrepareDaemonChatResumeDoesNotRequireCurrentDefaultRunner(t *testing.T) {
	var discoveries atomic.Int32
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/conversations/saved":
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"id": "saved", "cwd": "/runner-only/saved", "runnerId": "stored", "profile": "removed-profile", "environmentProfile": "stored-environment", "reasoningEffort": "high"}))
		case "/api/chat/slash-commands":
			discoveries.Add(1)
			assert.Equal(t, "saved", r.URL.Query().Get("conversationId"))
			assert.False(t, r.URL.Query().Has("profile"), "saved discovery must use the server's pinned profile")
			require.NoError(t, json.NewEncoder(w).Encode(protocol.WorkspaceDiscoverResult{CWD: "/runner-only/saved", EnvironmentProfile: "stored-environment"}))
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
	assert.EqualValues(t, 1, discoveries.Load(), "resume replacements fail before discovery")
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
