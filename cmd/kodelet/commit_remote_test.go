package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func remoteCommitCommandForTest(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	cmd := remoteRunCommandForTest()
	cmd.Use = "commit"
	cmd.SetContext(t.Context())
	cmd.Flags().String("template", "", "")
	cmd.Flags().String("prefix", "", "")
	cmd.Flags().Bool("short", true, "")
	cmd.Flags().Bool("no-sign", false, "")
	cmd.Flags().Bool("no-confirm", false, "")
	cmd.Flags().Bool("save", false, "")
	require.NoError(t, cmd.ParseFlags(args))
	return cmd
}

func TestRemoteCommitRequestRestrictionAndPrompt(t *testing.T) {
	cmd := remoteCommitCommandForTest(t, "--model=central-model", "--profile=review", "--cwd=/runner-only/repo", "--runner-profile=restricted")
	request, err := remoteCommitRequest(cmd)
	require.NoError(t, err)
	assert.Equal(t, "review", request.Profile)
	assert.Equal(t, "restricted", request.EnvironmentProfile)
	assert.Equal(t, "/runner-only/repo", request.CWD)
	assert.Equal(t, new("central-model"), request.Options.Model)
	assert.Equal(t, new(true), request.Options.NoTools)
	assert.Equal(t, new(true), request.Options.NoExtensions)
	assert.Equal(t, new(true), request.Options.NoSkills)
	assert.Equal(t, new(true), request.Options.UseWeakModel)
	snapshot := protocol.WorkspaceGitCommitSnapshot{Diff: "staged diff"}
	assert.Contains(t, remoteCommitPrompt(snapshot, NewCommitConfig()), "single-line")
	assert.Contains(t, remoteCommitPrompt(snapshot, &CommitConfig{}), "bullet points")
	assert.Contains(t, remoteCommitPrompt(snapshot, &CommitConfig{Template: "TICKET: description"}), "TICKET: description")
	assert.NotContains(t, remoteCommitPrompt(snapshot, NewCommitConfig()), "truncated")
	snapshot.Truncated, snapshot.DiffStat = true, "summary of staged changes"
	prompt := remoteCommitPrompt(snapshot, NewCommitConfig())
	assert.Contains(t, prompt, "patch preview is truncated")
	assert.Contains(t, prompt, "entire staged tree")
	assert.Contains(t, prompt, "do not infer details of omitted changes")
	assert.Contains(t, prompt, "<git_diff_stat>\nsummary of staged changes\n</git_diff_stat>")
	assert.Contains(t, prompt, "<git_diff>\nstaged diff\n</git_diff>")
}

func TestRemoteCommitRejectsInvalidOptionsBeforeHTTP(t *testing.T) {
	var calls atomic.Int32
	daemon := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer daemon.Close()
	for _, flag := range []string{"--save", "--save=false", "--no-tools=false", "--no-extensions=false", "--no-skills=false", "--max-tokens=0", "--sysprompt=/client/prompt", "--allowed-domains-file=/client/policy"} {
		cmd := remoteCommitCommandForTest(t, "--server="+daemon.URL, "--auth-token=client", "--no-confirm", flag)
		require.Error(t, runRemoteCommit(cmd), flag)
	}
	assert.Zero(t, calls.Load())
}

func TestRemoteCommitProcessDoesNotReadClientGitOrProviderState(t *testing.T) {
	t.Run("complete", func(t *testing.T) { testRemoteCommitProcess(t, false) })
	t.Run("truncated", func(t *testing.T) { testRemoteCommitProcess(t, true) })
}

func testRemoteCommitProcess(t *testing.T, truncated bool) {
	t.Helper()
	snapshot := protocol.WorkspaceGitCommitSnapshot{CWD: "/runner-only/repo", GitRoot: "/runner-only/repo", Head: strings.Repeat("a", 40), HeadRef: "refs/heads/main", Tree: strings.Repeat("b", 40), RunnerID: "registered", Generation: 7, Diff: "approved staged diff"}
	if truncated {
		snapshot.Truncated, snapshot.DiffStat = true, "summary of all staged changes"
	}
	var preparations, submissions, approvals atomic.Int32
	var conversationID string
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer client", r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/api/chat/settings":
			require.NoError(t, json.NewEncoder(w).Encode(chat.ControlPlaneChatSettings{DefaultRunnerID: "registered", DefaultRunnerReady: true}))
		case "/api/git/commit":
			if r.Method == http.MethodGet {
				preparations.Add(1)
				assert.Equal(t, "registered", r.URL.Query().Get("runnerId"))
				assert.Equal(t, "/runner-only/repo", r.URL.Query().Get("cwd"))
				require.NoError(t, json.NewEncoder(w).Encode(snapshot))
				return
			}
			approvals.Add(1)
			assert.Equal(t, conversationID, r.URL.Query().Get("conversationId"))
			var approval protocol.WorkspaceGitCommitParams
			require.NoError(t, json.NewDecoder(r.Body).Decode(&approval))
			assert.Equal(t, "TICKET feat: runner change", approval.Message)
			assert.Equal(t, strings.Repeat("b", 40), approval.Tree)
			assert.Equal(t, int64(7), approval.Generation)
			assert.Equal(t, "/runner-only/repo", approval.CWD)
			assert.True(t, approval.SignOff)
			require.NoError(t, json.NewEncoder(w).Encode(protocol.WorkspaceGitCommitResult{Commit: strings.Repeat("c", 40)}))
		case "/api/chat":
			submissions.Add(1)
			var request chat.ChatRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			assert.Equal(t, "registered", request.RunnerID)
			assert.Equal(t, "/runner-only/repo", request.CWD)
			assert.Contains(t, request.Message, "approved staged diff")
			if truncated {
				assert.Contains(t, request.Message, "summary of all staged changes")
				assert.Contains(t, request.Message, "patch preview is truncated")
			}
			assert.Equal(t, new(true), request.Options.NoTools)
			assert.Equal(t, new(true), request.Options.NoExtensions)
			assert.Equal(t, new(true), request.Options.NoSkills)
			assert.NotEmpty(t, request.ConversationID)
			assert.NotEmpty(t, request.TurnID)
			conversationID = request.ConversationID
			w.Header().Set("Content-Type", "application/x-ndjson")
			require.NoError(t, json.NewEncoder(w).Encode(chat.ChatEvent{Kind: "result", Result: new("feat: runner change")}))
			require.NoError(t, json.NewEncoder(w).Encode(chat.ChatEvent{Kind: "usage", Usage: &llmtypes.Usage{InputTokens: 10, OutputTokens: 5}}))
			require.NoError(t, json.NewEncoder(w).Encode(chat.ChatEvent{Kind: "done"}))
		default:
			http.NotFound(w, r)
		}
	}))
	defer daemon.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	root := t.TempDir()
	invalidStore := filepath.Join(root, "no-client-state")
	require.NoError(t, os.WriteFile(invalidStore, []byte("not a directory"), 0o600))
	env := []string{"PATH=" + root, "HOME=" + root, "KODELET_TEST_CLI_PROCESS=1", "KODELET_BASE_PATH=" + invalidStore}
	process := daemonCLIProcess(ctx, t, root, env, "commit", "--server="+daemon.URL, "--auth-token=client", "--cwd=/runner-only/repo", "--prefix=TICKET", "--no-confirm")
	output, err := process.CombinedOutput()
	require.NoError(t, err, "%s", output)
	assert.Contains(t, string(output), "TICKET feat: runner change")
	assert.Contains(t, string(output), "Usage: 10 input tokens, 5 output tokens")
	assert.Contains(t, string(output), "Commit created:")
	if truncated {
		assert.Contains(t, string(output), "Warning: staged changes are too large to preview in full")
		assert.Contains(t, string(output), "commit will include all staged changes")
	} else {
		assert.NotContains(t, string(output), "Warning: staged diff")
	}
	assert.EqualValues(t, 1, preparations.Load())
	assert.EqualValues(t, 1, submissions.Load())
	assert.EqualValues(t, 1, approvals.Load())
}

func TestRemoteCommitConfirmationDismissalAndCancellation(t *testing.T) {
	for _, value := range []string{"n\n", "y\n", ""} {
		broker := extensions.NewTerminalUIInputBroker(strings.NewReader(value), &strings.Builder{})
		broker.Interactive = true
		confirmed, message, err := confirmRemoteCommit(t.Context(), broker, "message")
		require.NoError(t, err)
		assert.Equal(t, value == "y\n", confirmed)
		assert.Equal(t, "message", message)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	broker := extensions.NewTerminalUIInputBroker(strings.NewReader("y\n"), &strings.Builder{})
	broker.Interactive = true
	confirmed, _, err := confirmRemoteCommit(ctx, broker, "message")
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, confirmed)
}
