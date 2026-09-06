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
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/controlplane"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	"github.com/jingkaihe/kodelet/pkg/runner/localstate"
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
		AuthToken: "history-token", RunnerAuthToken: "runner-token", DisableControlPlaneWorkspace: true,
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

	client, err := chat.NewControlPlaneChatRunner(endpoint, "history-token", "")
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
	assert.Contains(t, string(output), "no local database fallback")
	assert.NotContains(t, string(output), "failed to run database migrations")
	data, err := os.ReadFile(invalidStore)
	require.NoError(t, err)
	assert.Equal(t, "unchanged", string(data))
}

func TestDaemonConversationAdoptionCLI(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	previous := viper.AllSettings()
	viper.Reset()
	t.Cleanup(func() { viper.Reset(); require.NoError(t, viper.MergeConfigMap(previous)) })
	viper.Set("provider", "anthropic")
	viper.Set("model", "adoption-model")
	viper.Set("weak_model", "adoption-weak")
	viper.Set("anthropic_api_access", "api-key")
	root, workspace := t.TempDir(), t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("ANTHROPIC_API_KEY", "daemon-only")
	t.Setenv("KODELET_BASE_PATH", filepath.Join(root, "daemon-state"))
	t.Setenv("KODELET_CONVERSATION_STORE_TYPE", "sqlite")
	require.NoError(t, db.RunMigrations(ctx, migrations.All()))
	store, err := conversations.GetConversationStore(ctx)
	require.NoError(t, err)
	defer store.Close()
	record := convtypes.NewConversationRecord("cli-legacy-adoption")
	record.Provider, record.CWD = "anthropic", workspace
	record.RawMessages = json.RawMessage(`[{"role":"user","content":"preserved history"}]`)
	require.NoError(t, store.Save(ctx, record))
	record, err = store.Load(ctx, record.ID)
	require.NoError(t, err)
	runnerStore, err := localstate.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	daemon, err := controlplane.NewServer(ctx, &controlplane.ServerConfig{
		Host: "127.0.0.1", Port: 0, CompactRatio: 0.8,
		AuthToken: "adoption-token", WebAuthMode: controlplane.WebAuthModeToken, RunnerAuthMode: controlplane.RunnerAuthModeNone,
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
			assert.Fail(t, "adoption CLI daemon did not stop")
		}
		assert.NoError(t, daemon.Close())
	})
	require.Eventually(t, func() bool { return daemon.EmbeddedRunnerStatus().Ready }, 5*time.Second, 10*time.Millisecond)
	runnerID := daemon.EmbeddedRunnerStatus().RunnerID
	invalidStore := filepath.Join(root, "client-store-is-a-file")
	require.NoError(t, os.WriteFile(invalidStore, []byte("untouched"), 0o600))
	environment := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "KODELET_BASE_PATH=" + invalidStore, "KODELET_TEST_CLI_PROCESS=1", "KODELET_AUTH_TOKEN=adoption-token", "KODELET_SERVER=http://" + listener.Addr().String()}
	run := func(stdin string, args ...string) (string, error) {
		process := daemonCLIProcess(ctx, t, root, environment, append([]string{"conversation", "adopt", record.ID}, args...)...)
		process.Stdin = strings.NewReader(stdin)
		output, err := process.CombinedOutput()
		return string(output), err
	}
	output, err := run("", "--preview")
	require.Error(t, err)
	assert.Contains(t, output, "runner")
	output, err = run("", "--runner="+runnerID, "--preview")
	require.NoError(t, err, "%s", output)
	assert.Contains(t, output, "Preview only")
	assert.Contains(t, output, "Host:")
	assert.Contains(t, output, workspace)
	assert.Contains(t, output, runnerID)
	output, err = run("n\n", "--runner="+runnerID)
	require.NoError(t, err, "%s", output)
	assert.Contains(t, output, "Adoption cancelled")
	unchanged, err := store.Load(ctx, record.ID)
	require.NoError(t, err)
	assert.Equal(t, record, unchanged)
	output, err = run("", "--runner="+runnerID, "--yes")
	require.NoError(t, err, "%s", output)
	assert.Contains(t, output, "adopted; history and model configuration preserved")
	updated, err := store.Load(ctx, record.ID)
	require.NoError(t, err)
	assert.Equal(t, runnerID, updated.Metadata[convtypes.RunnerIDMetadataKey])
	delete(updated.Metadata, convtypes.RunnerIDMetadataKey)
	delete(updated.Metadata, convtypes.RunnerEnvironmentProfileMetadataKey)
	assert.Equal(t, record, updated)
	_, err = run("", "--runner="+runnerID, "--yes")
	require.Error(t, err, "adoption cannot silently rebind existing affinity")
	data, err := os.ReadFile(invalidStore)
	require.NoError(t, err)
	assert.Equal(t, "untouched", string(data), "CLI must not initialize a local store or provider")
}
