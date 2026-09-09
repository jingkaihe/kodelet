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

	"github.com/charmbracelet/x/ansi"
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
	t.Run("complete", func(t *testing.T) { testRemoteCommitProcess(t, false, "") })
	t.Run("truncated", func(t *testing.T) { testRemoteCommitProcess(t, true, "") })
	t.Run("color", func(t *testing.T) { testRemoteCommitProcess(t, false, "", "KODELET_COLOR=always") })
	t.Run("no-color", func(t *testing.T) { testRemoteCommitProcess(t, false, "", "KODELET_COLOR=always", "NO_COLOR=1") })
	t.Run("cleanup-failure", func(t *testing.T) { testRemoteCommitProcess(t, false, "cleanup") })
	t.Run("commit-failure", func(t *testing.T) { testRemoteCommitProcess(t, false, "commit") })
}

func testRemoteCommitProcess(t *testing.T, truncated bool, failure string, colorEnv ...string) {
	t.Helper()
	snapshot := protocol.WorkspaceGitCommitSnapshot{CWD: "/runner-only/repo", GitRoot: "/runner-only/repo", Head: strings.Repeat("a", 40), HeadRef: "refs/heads/main", Tree: strings.Repeat("b", 40), RunnerID: "registered", Generation: 7, Diff: "approved staged diff"}
	if truncated {
		snapshot.Truncated, snapshot.DiffStat = true, "summary of all staged changes"
	}
	var preparations, submissions, approvals, deletions atomic.Int32
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
			if failure == "commit" {
				w.WriteHeader(http.StatusBadGateway)
				require.NoError(t, json.NewEncoder(w).Encode(map[string]string{"error": "runner commit was not acknowledged; inspect Git history before retrying"}))
				return
			}
			require.NoError(t, json.NewEncoder(w).Encode(protocol.WorkspaceGitCommitResult{Commit: strings.Repeat("c", 40), Output: "[main ccccccc] TICKET feat: runner change\n 1 file changed, 1 insertion(+)"}))
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
			require.NoError(t, json.NewEncoder(w).Encode(chat.ChatEvent{Kind: "usage", Usage: &llmtypes.Usage{
				InputTokens: 10, OutputTokens: 5, CacheCreationInputTokens: 2, CacheReadInputTokens: 3,
				InputCost: 0.001, OutputCost: 0.002, CacheCreationCost: 0.003, CacheReadCost: 0.004,
				CurrentContextWindow: 20, MaxContextWindow: 100,
			}}))
			require.NoError(t, json.NewEncoder(w).Encode(chat.ChatEvent{Kind: "done"}))
		case "/api/conversations/" + conversationID:
			deletions.Add(1)
			assert.Equal(t, http.MethodDelete, r.Method)
			assert.NotEmpty(t, conversationID)
			assert.EqualValues(t, 1, approvals.Load(), "cleanup must follow the commit")
			if failure == "cleanup" {
				w.WriteHeader(http.StatusInternalServerError)
				require.NoError(t, json.NewEncoder(w).Encode(map[string]string{"error": "failed to delete conversation"}))
				return
			}
			w.WriteHeader(http.StatusNoContent)
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
	env = append(env, colorEnv...)
	process := daemonCLIProcess(ctx, t, root, env, "commit", "--server="+daemon.URL, "--auth-token=client", "--cwd=/runner-only/repo", "--prefix=TICKET", "--no-confirm")
	output, err := process.CombinedOutput()
	assert.EqualValues(t, 1, preparations.Load())
	assert.EqualValues(t, 1, submissions.Load())
	assert.EqualValues(t, 1, approvals.Load())
	if failure == "commit" {
		require.Error(t, err)
		assert.Contains(t, string(output), "check 'git log'")
		assert.NotContains(t, string(output), "Commit created successfully!")
		assert.Zero(t, deletions.Load(), "uncertain commits must retain their conversation")
		return
	}
	require.NoError(t, err, "%s", output)
	assert.EqualValues(t, 1, deletions.Load())
	if len(colorEnv) == 1 {
		assert.Contains(t, string(output), "\x1b[", "explicit color mode should style terminal output")
	} else {
		assert.NotContains(t, string(output), "\x1b[", "pipes and NO_COLOR should use plain text")
	}
	text := ansi.Strip(string(output))
	assert.Contains(t, text, "Analyzing staged changes and generating commit message...\n")
	assert.Contains(t, text, "Generated Commit Message\n------------------------\nTICKET feat: runner change\n")
	assert.Contains(t, text, "[Usage Stats] Input tokens: 10 | Output tokens: 5 | Cache write: 2 | Cache read: 3 | Total: 20")
	assert.Contains(t, text, "[Context Window] Current: 20 | Max: 100 | Usage: 20.0%")
	assert.Contains(t, text, "[Cost Stats] Input: $0.0010 | Output: $0.0020 | Cache write: $0.0030 | Cache read: $0.0040 | Total: $0.0100")
	assert.Contains(t, text, "[main ccccccc] TICKET feat: runner change\n 1 file changed, 1 insertion(+)\n")
	assert.Contains(t, text, "✓ Commit created successfully!\n")
	if truncated {
		assert.Contains(t, text, "⚠ Staged changes are too large to preview in full")
		assert.Contains(t, text, "commit will include all staged changes")
	} else if failure == "cleanup" {
		assert.Contains(t, text, "⚠ The commit was created, but its temporary conversation could not be deleted.")
	} else {
		assert.NotContains(t, text, "⚠")
	}
}

func TestRemoteCommitPreparationErrorsAreActionable(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"no staged changes", http.StatusBadGateway, `{"error":"runner commit preparation failed: no staged changes; stage changes on the selected runner first"}`, "no staged changes; stage changes on the selected runner first"},
		{"git error", http.StatusBadGateway, `{"error":"runner commit preparation failed: runner Git write-tree failed: unmerged index"}`, "runner Git write-tree failed: unmerged index"},
		{"runner offline", http.StatusConflict, `{"error":"runner is offline; reconnect it before trying again"}`, "runner is offline; reconnect it before trying again"},
		{"unknown gateway error", http.StatusBadGateway, "Bad Gateway", "server returned HTTP 502"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var preparations, unexpectedCalls atomic.Int32
			daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/api/chat/settings":
					require.NoError(t, json.NewEncoder(w).Encode(chat.ControlPlaneChatSettings{DefaultRunnerID: "registered", DefaultRunnerReady: true}))
				case r.URL.Path == "/api/git/commit" && r.Method == http.MethodGet:
					preparations.Add(1)
					w.WriteHeader(scenario.status)
					_, _ = w.Write([]byte(scenario.body))
				default:
					unexpectedCalls.Add(1)
					http.NotFound(w, r)
				}
			}))
			defer daemon.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			root := t.TempDir()
			env := []string{"PATH=" + root, "HOME=" + root, "KODELET_TEST_CLI_PROCESS=1", "NO_COLOR=1"}
			process := daemonCLIProcess(ctx, t, root, env, "commit", "--server="+daemon.URL, "--auth-token=client", "--cwd=/runner-only/repo", "--no-confirm")
			output, err := process.CombinedOutput()
			require.Error(t, err)
			assert.Equal(t, 1, process.ProcessState.ExitCode())
			assert.Equal(t, "Error: "+scenario.want+"\n", string(output), "show only actionable guidance, without usage or transport wrappers")
			assert.EqualValues(t, 1, preparations.Load())
			assert.Zero(t, unexpectedCalls.Load(), "preparation errors must not generate messages, create commits, or retry")
		})
	}
}

func TestRemoteCommitConfirmationDismissalAndCancellation(t *testing.T) {
	for _, value := range []string{"n\n", "y\n", ""} {
		var output strings.Builder
		broker := extensions.NewTerminalUIInputBroker(strings.NewReader(value), &output)
		broker.Interactive = true
		confirmed, message, err := confirmRemoteCommit(t.Context(), broker, "message")
		require.NoError(t, err)
		assert.Equal(t, value == "y\n", confirmed)
		assert.Equal(t, "message", message)
		assert.Contains(t, output.String(), "Create commit with this message?")
		assert.Contains(t, output.String(), "Y/n/e (edit)> ")
		assert.NotContains(t, output.String(), "Submit>")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	broker := extensions.NewTerminalUIInputBroker(strings.NewReader("y\n"), &strings.Builder{})
	broker.Interactive = true
	confirmed, _, err := confirmRemoteCommit(ctx, broker, "message")
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, confirmed)
}
