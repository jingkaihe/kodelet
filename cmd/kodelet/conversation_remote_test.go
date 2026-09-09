package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/controlplane"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	"github.com/jingkaihe/kodelet/pkg/runner/localstate"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/jingkaihe/kodelet/pkg/runner/registry"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDaemonConversationCLIWithoutLocalStoreOrRunner(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	root := t.TempDir()
	t.Setenv("KODELET_BASE_PATH", filepath.Join(root, "daemon-state"))
	t.Setenv("KODELET_CONVERSATION_STORE_TYPE", "sqlite")
	require.NoError(t, db.RunMigrations(ctx, migrations.All()))
	store, err := conversations.GetConversationStore(ctx)
	require.NoError(t, err)
	defer store.Close()
	record := convtypes.NewConversationRecord("history-source")
	record.Provider = "anthropic"
	record.CWD = "/runner/does-not-exist-on-client"
	record.Summary = "retained history"
	record.CreatedAt = time.Date(2026, 9, 5, 12, 34, 56, 0, time.UTC)
	record.RawMessages = json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"history-only content"}]}]`)
	record.Metadata["custom"] = "preserved"
	record.Usage = llmtypes.Usage{InputTokens: 42, OutputTokens: 7, InputCost: 0.1}
	require.NoError(t, store.Save(ctx, record))
	record, err = store.Load(ctx, record.ID)
	require.NoError(t, err)
	other := convtypes.NewConversationRecord("other-provider")
	other.Provider = "openai"
	other.CreatedAt = record.CreatedAt
	other.RawMessages = json.RawMessage(`[{"role":"user","content":"other provider"}]`)
	require.NoError(t, store.Save(ctx, other))
	daemon, err := controlplane.NewServer(ctx, &controlplane.ServerConfig{
		Host: "127.0.0.1", Port: 0, CompactRatio: 0.8,
		AuthToken: "history-token", RunnerAuthToken: "runner-token",
	}, nil)
	require.NoError(t, err)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	endpoint := "http://" + listener.Addr().String()
	serverCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- daemon.Serve(serverCtx, listener) }()
	t.Cleanup(func() {
		stop()
		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(5 * time.Second):
			assert.Fail(t, "history-only daemon did not stop")
		}
		assert.NoError(t, daemon.Close())
	})
	invalidStore := filepath.Join(root, "client-store-is-a-file")
	require.NoError(t, os.WriteFile(invalidStore, []byte("not a database directory"), 0o600))
	environment := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "KODELET_BASE_PATH=" + invalidStore, "KODELET_TEST_CLI_PROCESS=1", "KODELET_AUTH_TOKEN=history-token", "KODELET_SERVER=" + endpoint}
	run := func(args ...string) (string, string, error) {
		process := daemonCLIProcess(ctx, t, root, environment, append([]string{"conversation"}, args...)...)
		var stdout, stderr bytes.Buffer
		process.Stdout, process.Stderr = &stdout, &stderr
		err := process.Run()
		return stdout.String(), stderr.String(), err
	}
	output, stderr, err := run("list", "--json", "--provider=anthropic", "--start=2026-09-05", "--end=2026-09-05", "--sort-by=created_at", "--cwd="+record.CWD)
	require.NoError(t, err, "%s", stderr)
	var list struct {
		Conversations []ConversationSummaryOutput `json:"conversations"`
	}
	require.NoError(t, json.Unmarshal([]byte(output), &list))
	require.Len(t, list.Conversations, 1)
	assert.Equal(t, record.ID, list.Conversations[0].ID)
	assert.Equal(t, record.Usage.TotalCost(), list.Conversations[0].TotalCost)
	for _, format := range []string{"raw", "json", "text", "markdown"} {
		output, stderr, err = run("show", record.ID, "--format="+format)
		require.NoError(t, err, "%s", stderr)
		assert.Contains(t, output, "history-only content")
	}
	exported := filepath.Join(root, "client-export.json")
	_, stderr, err = run("export", record.ID, exported)
	require.NoError(t, err, "%s", stderr)
	data, err := os.ReadFile(exported)
	require.NoError(t, err)
	want, err := json.Marshal(record)
	require.NoError(t, err)
	assert.JSONEq(t, string(want), string(data))

	output, stderr, err = run("fork", "--cwd="+record.CWD)
	require.NoError(t, err, "%s", stderr)
	forkID := strings.TrimSpace(strings.TrimPrefix(output, "Conversation forked successfully. New ID: "))
	require.NotEmpty(t, forkID)
	forked, err := store.Load(ctx, forkID)
	require.NoError(t, err)
	assert.NotEqual(t, record.ID, forked.ID)
	assert.Equal(t, record.CWD, forked.CWD)
	assert.JSONEq(t, string(record.RawMessages), string(forked.RawMessages))
	assert.Equal(t, 0, forked.Usage.InputTokens)
	assert.Contains(t, forked.Metadata, convtypes.ConversationForkMetadataKey)
	_, stderr, err = run("delete", record.ID, "--no-confirm")
	require.NoError(t, err, "%s", stderr)
	_, stderr, err = run("show", record.ID)
	require.Error(t, err)
	assert.Contains(t, stderr, "HTTP 404")

	client, err := chat.NewClient(endpoint, "history-token", "")
	require.NoError(t, err)
	history, err := client.QueryConversations(ctx, conversations.ListConversationsRequest{})
	require.NoError(t, err)
	assert.Len(t, history.Conversations, 2)
	runners, _, err := fetchRunners(ctx, endpoint, "history-token")
	require.NoError(t, err)
	assert.Empty(t, runners, "all history operations must work with no registered runner")
	unchanged, err := os.ReadFile(invalidStore)
	require.NoError(t, err)
	assert.Equal(t, "not a database directory", string(unchanged))

	for _, args := range [][]string{{"import", "/missing"}, {"edit", forkID}, {"fork"}, {"fork", forkID, "--cwd=/other"}, {"list", "--start=invalid"}} {
		_, _, err = run(args...)
		require.Error(t, err, "unsupported or ambiguous operation %v must fail", args)
	}
}

func TestDaemonConversationCLINeverFallsBack(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	endpoint := server.URL
	server.Close()
	root := t.TempDir()
	invalidStore := filepath.Join(root, "not-a-store")
	require.NoError(t, os.WriteFile(invalidStore, []byte("unchanged"), 0o600))
	process := daemonCLIProcess(t.Context(), t, root, []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "KODELET_TEST_CLI_PROCESS=1", "KODELET_BASE_PATH=" + invalidStore}, "conversation", "list", "--server="+endpoint, "--auth-token=token")
	output, err := process.CombinedOutput()
	require.Error(t, err)
	assert.Contains(t, string(output), "could not complete the conversation command")
	assert.NotContains(t, string(output), "failed to run database migrations")
	data, err := os.ReadFile(invalidStore)
	require.NoError(t, err)
	assert.Equal(t, "unchanged", string(data))
}

func TestParseConversationMoveTarget(t *testing.T) {
	for _, test := range []struct {
		name     string
		target   string
		runnerID string
		cwd      string
		wantErr  string
	}{
		{name: "saved directory", target: "runner-one", runnerID: "runner-one"},
		{name: "absolute directory", target: "runner-one:/srv/project", runnerID: "runner-one", cwd: "/srv/project"},
		{name: "relative directory", target: "runner-one:../project", runnerID: "runner-one", cwd: "../project"},
		{name: "noncanonical directory", target: "runner-one:/srv/../project//", runnerID: "runner-one", cwd: "/srv/../project//"},
		{name: "spaces and colons", target: "runner-one: /srv/my project:branch:copy ", runnerID: "runner-one", cwd: " /srv/my project:branch:copy "},
		{name: "colon directory", target: "runner-one::", runnerID: "runner-one", cwd: ":"},
		{name: "empty runner", target: "", wantErr: "runner ID is required"},
		{name: "whitespace runner", target: " \t", wantErr: "runner ID is required"},
		{name: "missing runner before path", target: ":/srv/project", wantErr: "runner ID is required"},
		{name: "whitespace runner before path", target: " \t:/srv/project", wantErr: "runner ID is required"},
		{name: "trailing colon", target: "runner-one:", wantErr: "directory after ':' must not be empty"},
		{name: "blank directory", target: "runner-one: \t", wantErr: "directory after ':' must not be empty"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runnerID, cwd, err := parseConversationMoveTarget(test.target)
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.runnerID, runnerID)
			assert.Equal(t, test.cwd, cwd)
		})
	}
}

func TestConversationMoveCLIRejectsInvalidArguments(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.NotFound(w, r)
	}))
	defer server.Close()
	root := t.TempDir()
	environment := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "KODELET_TEST_CLI_PROCESS=1", "KODELET_SERVER=" + server.URL, "KODELET_AUTH_TOKEN=move-token"}
	for _, test := range []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "missing arguments", args: []string{"move"}, wantErr: "accepts 2 arg(s)"},
		{name: "missing target", args: []string{"move", "conversation-one"}, wantErr: "accepts 2 arg(s)"},
		{name: "extra argument", args: []string{"move", "conversation-one", "runner-one", "/srv/project"}, wantErr: "accepts 2 arg(s)"},
		{name: "empty conversation", args: []string{"move", "", "runner-one"}, wantErr: "conversation ID is required"},
		{name: "blank conversation", args: []string{"move", " \t", "runner-one"}, wantErr: "conversation ID is required"},
		{name: "empty runner", args: []string{"move", "conversation-one", ""}, wantErr: "runner ID is required"},
		{name: "missing runner", args: []string{"move", "conversation-one", ":/srv/project"}, wantErr: "runner ID is required"},
		{name: "trailing colon", args: []string{"move", "conversation-one", "runner-one:"}, wantErr: "directory after ':' must not be empty"},
		{name: "blank directory", args: []string{"move", "conversation-one", "runner-one: \t"}, wantErr: "directory after ':' must not be empty"},
		{name: "removed command", args: []string{"adopt", "conversation-one", "runner-one"}, wantErr: "unknown command"},
	} {
		t.Run(test.name, func(t *testing.T) {
			process := daemonCLIProcess(ctx, t, root, environment, append([]string{"conversation"}, test.args...)...)
			output, err := process.CombinedOutput()
			require.Error(t, err, "%s", output)
			assert.Contains(t, string(output), test.wantErr)
		})
	}
	for _, flag := range []string{"runner=runner-one", "runner-profile=work", "cwd=/srv/project", "preview", "yes"} {
		t.Run("removed flag "+flag, func(t *testing.T) {
			process := daemonCLIProcess(ctx, t, root, environment, "conversation", "move", "conversation-one", "runner-one", "--"+flag)
			output, err := process.CombinedOutput()
			require.Error(t, err, "%s", output)
			assert.Contains(t, string(output), "unknown flag: --"+strings.SplitN(flag, "=", 2)[0])
		})
	}
	assert.Zero(t, requests.Load(), "invalid invocations must fail without querying the daemon")
}

func TestConversationMoveCLIConfirmationRoundTrip(t *testing.T) {
	for _, test := range []struct {
		name   string
		target string
		cwd    string
	}{
		{name: "saved directory", target: "runner-one"},
		{name: "literal directory", target: "runner-one: ../my project:branch:copy ", cwd: " ../my project:branch:copy "},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			requests := make(chan chat.ConversationMoveRequest, 4)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "/api/conversations/conversation-one/move", r.URL.Path)
				assert.Equal(t, "Bearer move-token", r.Header.Get("Authorization"))
				var params chat.ConversationMoveRequest
				decoder := json.NewDecoder(r.Body)
				decoder.DisallowUnknownFields()
				if err := decoder.Decode(&params); !assert.NoError(t, err) {
					http.Error(w, "invalid request", http.StatusBadRequest)
					return
				}
				requests <- params
				cwd := params.CWD
				if cwd == "" {
					cwd = "/server/saved"
				}
				result := chat.ConversationMoveResult{
					ConversationID: "conversation-one", SourceCWD: "/server/saved",
					RunnerID: params.RunnerID, CWD: cwd, Confirmation: "reviewed-plan",
					Moved: params.Confirmation == "reviewed-plan",
				}
				w.Header().Set("Content-Type", "application/json")
				assert.NoError(t, json.NewEncoder(w).Encode(result))
			}))
			defer server.Close()
			root := t.TempDir()
			environment := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "KODELET_TEST_CLI_PROCESS=1", "KODELET_SERVER=" + server.URL, "KODELET_AUTH_TOKEN=move-token"}
			process := daemonCLIProcess(ctx, t, root, environment, "conversation", "move", "conversation-one", test.target, "--no-confirm")
			output, err := process.CombinedOutput()
			require.NoError(t, err, "%s", output)
			require.Len(t, requests, 2, "even --no-confirm must fetch a plan before submitting its digest")
			assert.Equal(t, chat.ConversationMoveRequest{RunnerID: "runner-one", CWD: test.cwd}, <-requests)
			assert.Equal(t, chat.ConversationMoveRequest{RunnerID: "runner-one", CWD: test.cwd, Confirmation: "reviewed-plan"}, <-requests)
			assert.Contains(t, string(output), "From directory: /server/saved")
			assert.Contains(t, string(output), "To runner: runner-one\n")
			assert.Contains(t, string(output), "Runner profile: default (unchanged)")
			assert.NotContains(t, string(output), "Move this conversation?")
			if test.cwd != "" {
				assert.Contains(t, string(output), "To directory: "+test.cwd+"\n")
			} else {
				assert.Contains(t, string(output), "To directory: /server/saved")
			}
		})
	}
}

func TestDaemonConversationMoveCLI(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	previous := viper.AllSettings()
	viper.Reset()
	t.Cleanup(func() { viper.Reset(); require.NoError(t, viper.MergeConfigMap(previous)) })
	viper.Set("provider", "anthropic")
	viper.Set("model", "move-model")
	viper.Set("weak_model", "move-weak")
	viper.Set("anthropic_api_access", "api-key")
	root, workspace := t.TempDir(), t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("KODELET_BASE_PATH", filepath.Join(root, "daemon-state"))
	t.Setenv("KODELET_CONVERSATION_STORE_TYPE", "sqlite")
	require.NoError(t, db.RunMigrations(ctx, migrations.All()))
	store, err := conversations.GetConversationStore(ctx)
	require.NoError(t, err)
	defer store.Close()
	record := convtypes.NewConversationRecord("cli-legacy-move")
	record.Provider, record.CWD = "anthropic", "saved/missing directory:history"
	record.RawMessages = json.RawMessage(`[{"role":"user","content":"preserved history"}]`)
	record.Metadata[convtypes.RunnerEnvironmentProfileMetadataKey] = "unavailable-profile"
	record.Metadata["model"] = "retired-model"
	require.NoError(t, store.Save(ctx, record))
	record, err = store.Load(ctx, record.ID)
	require.NoError(t, err)
	path, err := db.DefaultDBPath()
	require.NoError(t, err)
	persistence, err := registry.NewSQLitePersistence(ctx, path, "")
	require.NoError(t, err)
	defer persistence.Close()
	for _, id := range []string{"offline-one", "offline-two"} {
		require.NoError(t, persistence.SaveRunner(ctx, registry.Runner{
			ID: id, DisplayName: id + " name", Status: registry.RunnerStatusOffline,
			Host: protocol.Host{InstanceID: id + "-host"}, Workspace: protocol.Workspace{Path: "/offline/workspace", Name: "workspace"},
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}))
	}
	runnerStore, err := localstate.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	daemon, err := controlplane.NewServer(ctx, &controlplane.ServerConfig{
		Host: "127.0.0.1", Port: 0, CompactRatio: 0.8,
		AuthToken: "move-token", WebAuthMode: controlplane.WebAuthModeToken, RunnerAuthMode: controlplane.RunnerAuthModeNone,
		EmbeddedRunner: &controlplane.EmbeddedRunnerConfig{Workspace: workspace, Store: runnerStore, Settings: map[string]any{
			"extensions": map[string]any{"enabled": false}, "skills": map[string]any{"enabled": false},
		}},
	}, nil)
	require.NoError(t, err)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	serverCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- daemon.Serve(serverCtx, listener) }()
	t.Cleanup(func() {
		stop()
		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(10 * time.Second):
			assert.Fail(t, "move CLI daemon did not stop")
		}
		assert.NoError(t, daemon.Close())
	})
	require.Eventually(t, func() bool { return daemon.EmbeddedRunnerStatus().Ready }, 5*time.Second, 10*time.Millisecond)
	runnerID := daemon.EmbeddedRunnerStatus().RunnerID
	invalidStore := filepath.Join(root, "client-store-is-a-file")
	require.NoError(t, os.WriteFile(invalidStore, []byte("untouched"), 0o600))
	environment := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "KODELET_BASE_PATH=" + invalidStore, "KODELET_TEST_CLI_PROCESS=1", "KODELET_AUTH_TOKEN=move-token", "KODELET_SERVER=http://" + listener.Addr().String()}
	run := func(stdin string, args ...string) (string, error) {
		process := daemonCLIProcess(ctx, t, root, environment, append([]string{"conversation", "move", record.ID}, args...)...)
		process.Stdin = strings.NewReader(stdin)
		output, err := process.CombinedOutput()
		return string(output), err
	}
	for _, test := range []struct{ name, stdin string }{{"decline", "n\n"}, {"EOF", ""}, {"default no", "\n"}} {
		t.Run(test.name, func(t *testing.T) {
			output, err := run(test.stdin, runnerID)
			require.NoError(t, err, "%s", output)
			assert.Contains(t, output, "From runner: unassigned")
			assert.Contains(t, output, "From directory: "+record.CWD)
			assert.Contains(t, output, "To runner: "+runnerID)
			assert.Contains(t, output, "To directory: "+record.CWD)
			assert.Contains(t, output, "Runner profile: unavailable-profile (unchanged)")
			assert.Contains(t, output, "No files are copied; the destination directory and runner readiness are not checked.")
			assert.Contains(t, output, "Move this conversation?")
			assert.Contains(t, output, "Move cancelled; the conversation is unchanged.")
			assert.Less(t, strings.Index(output, "To directory:"), strings.Index(output, "Move this conversation?"))
			unchanged, err := store.Load(ctx, record.ID)
			require.NoError(t, err)
			assert.Equal(t, record, unchanged)
		})
	}
	output, err := run("", runnerID, "--no-confirm")
	require.NoError(t, err, "%s", output)
	assert.Contains(t, output, "From runner: unassigned")
	assert.Contains(t, output, "To runner: "+runnerID)
	assert.Contains(t, output, "moved; its history and model settings are unchanged")
	assert.NotContains(t, output, "Move this conversation?")
	assert.NotContains(t, output, "ready to continue")
	updated, err := store.Load(ctx, record.ID)
	require.NoError(t, err)
	record.Metadata[convtypes.RunnerIDMetadataKey] = runnerID
	assert.Equal(t, record, updated)

	destination := filepath.Join(workspace, "missing directory:branch:copy")
	output, err = run("yes\n", "offline-one:"+destination)
	require.NoError(t, err, "%s", output)
	assert.Contains(t, output, "From runner: "+runnerID)
	assert.Contains(t, output, "From directory: "+record.CWD)
	assert.Contains(t, output, "To runner: offline-one (offline-one name)")
	assert.Contains(t, output, "To directory: "+destination)
	assert.Contains(t, output, "Move this conversation?")
	assert.Contains(t, output, "moved; its history and model settings are unchanged")
	record.CWD = destination
	record.Metadata[convtypes.RunnerIDMetadataKey] = "offline-one"
	updated, err = store.Load(ctx, record.ID)
	require.NoError(t, err)
	assert.Equal(t, record, updated)

	output, err = run("y\n", "offline-two")
	require.NoError(t, err, "%s", output)
	assert.Contains(t, output, "From runner: offline-one")
	assert.Contains(t, output, "To runner: offline-two (offline-two name)")
	assert.Contains(t, output, "To directory: "+destination)
	record.Metadata[convtypes.RunnerIDMetadataKey] = "offline-two"
	updated, err = store.Load(ctx, record.ID)
	require.NoError(t, err)
	assert.Equal(t, record, updated)

	output, err = run("", "offline-two:"+destination, "--no-confirm")
	require.NoError(t, err, "moving to the same runner and directory is idempotent: %s", output)
	updated, err = store.Load(ctx, record.ID)
	require.NoError(t, err)
	assert.Equal(t, record, updated)
	affinity, found, err := persistence.ConversationAffinity(ctx, record.ID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "offline-two", affinity.RunnerID)
	assert.Equal(t, "unavailable-profile", affinity.EnvironmentProfile)
	runners, _, err := fetchRunners(ctx, "http://"+listener.Addr().String(), "move-token")
	require.NoError(t, err)
	for _, runner := range runners {
		if strings.HasPrefix(runner.ID, "offline-") {
			assert.False(t, runner.Connected, "moves must not require bringing a runner online")
		}
	}
	_, err = os.Stat(destination)
	assert.True(t, os.IsNotExist(err), "moves must not create or copy a directory")
	data, err := os.ReadFile(invalidStore)
	require.NoError(t, err)
	assert.Equal(t, "untouched", string(data), "CLI must not initialize a local store or provider")
}
