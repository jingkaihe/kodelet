package main

import (
	"bytes"
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
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func remoteACPCommandForTest() *cobra.Command {
	cmd := remoteRunCommandForTest()
	cmd.Use = "acp"
	cmd.Flags().String("runner-auth-token", "", "")
	return cmd
}

func TestRemoteACPRejectsRunnerCredentialsAndOwnerConfig(t *testing.T) {
	for _, flag := range []string{"--runner-auth-token=runner-only", "--sysprompt=/client/prompt", "--allowed-domains-file=/client/domains", "--compact-ratio=0.5", "--enable-openai-search=false"} {
		cmd := remoteACPCommandForTest()
		require.NoError(t, cmd.ParseFlags([]string{flag}))
		require.ErrorContains(t, validateRemoteACPFlags(cmd), "owning daemon or runner")
	}
}

func TestRemoteACPUsesTypedOptionsWithoutLoadingClientConfig(t *testing.T) {
	base := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(base, []byte("no local state"), 0o600))
	t.Setenv("KODELET_BASE_PATH", base)
	t.Setenv("KODELET_SERVER", "https://unchanged.example")
	cmd := remoteACPCommandForTest()
	require.NoError(t, cmd.ParseFlags([]string{"--auth-token=client-only", "--profile=daemon-profile", "--runner-profile=environment", "--model=daemon-model", "--allowed-tools=", "--no-tools=false", "--max-turns=0"}))
	config, err := remoteACPSessionConfig(t.Context(), cmd, "https://daemon.example")
	require.NoError(t, err)
	assert.Equal(t, "daemon-profile", config.Profile)
	assert.Equal(t, "environment", config.EnvironmentProfile)
	assert.True(t, config.EnvironmentProfileExplicit)
	assert.Equal(t, &llmtypes.ExecutionOptions{Model: new("daemon-model"), AllowedTools: new([]string{}), NoTools: new(false), MaxTurns: new(0)}, config.Options)
	_, runnerID, err := config.Provider.WaitForRemoteChat(t.Context())
	require.NoError(t, err)
	assert.Empty(t, runnerID, "default runner is selected for each new session, not substituted on resume")
	assert.Equal(t, "https://unchanged.example", os.Getenv("KODELET_SERVER"), "ACP does not mutate the process environment")
}

func TestRemoteACPSelectsRegisteredRunnerAndUsesDiscoveryClient(t *testing.T) {
	var targets []chat.WorkspaceTarget
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer client-token", r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/api/runners":
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"runners": []runnerregistry.Runner{{ID: "runner-123", DisplayName: "workstation", Connected: true, Status: runnerregistry.RunnerStatusIdle}}}))
		case "/api/chat/slash-commands":
			q := r.URL.Query()
			var options *llmtypes.ExecutionOptions
			if q.Has("options") {
				require.NoError(t, json.Unmarshal([]byte(q.Get("options")), &options))
			}
			targets = append(targets, chat.WorkspaceTarget{RunnerID: q.Get("runnerId"), CWD: q.Get("cwd"), EnvironmentProfile: q.Get("environmentProfile"), ConversationID: q.Get("conversationId"), Options: options})
			require.NoError(t, json.NewEncoder(w).Encode(protocol.WorkspaceDiscoverResult{CWD: "/runner-only/repo", EnvironmentProfile: "gpu"}))
		case "/api/chat/cwd-suggestions":
			assert.Equal(t, "../oth", r.URL.Query().Get("q"))
			assert.Equal(t, "saved", r.URL.Query().Get("conversationId"))
			require.NoError(t, json.NewEncoder(w).Encode(protocol.WorkspaceCWDHintsResult{BaseDir: "/runner-only", Hints: []protocol.DirectoryHint{{Path: "/runner-only/other"}}}))
		default:
			http.Error(w, "unexpected endpoint", http.StatusNotFound)
		}
	}))
	defer server.Close()
	cmd := remoteACPCommandForTest()
	require.NoError(t, cmd.ParseFlags([]string{"--runner=workstation", "--auth-token=client-token"}))
	config, err := remoteACPSessionConfig(t.Context(), cmd, server.URL)
	require.NoError(t, err)
	client, runnerID, err := config.Provider.WaitForRemoteChat(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "runner-123", runnerID)
	target := chat.WorkspaceTarget{RunnerID: runnerID, CWD: "~/repo with spaces", EnvironmentProfile: "gpu", Options: &llmtypes.ExecutionOptions{NoExtensions: new(true), NoSkills: new(true), AllowedTools: new([]string{})}}
	result, err := client.DiscoverWorkspace(t.Context(), target)
	require.NoError(t, err)
	assert.Equal(t, "/runner-only/repo", result.CWD)
	assert.Equal(t, []chat.WorkspaceTarget{target}, targets)
	hints, err := client.(*chat.ControlPlaneChatRunner).WorkspaceCWDSuggestions(t.Context(), chat.WorkspaceTarget{ConversationID: "saved"}, "../oth")
	require.NoError(t, err)
	assert.Equal(t, "/runner-only/other", hints.Hints[0].Path)
}

func TestRemoteACPDoesNotFallbackWhenDaemonIsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer server.Close()
	cmd := remoteACPCommandForTest()
	require.NoError(t, cmd.ParseFlags([]string{"--runner=missing", "--auth-token=client-token"}))
	err := runRemoteACP(context.Background(), cmd, server.URL)
	require.ErrorContains(t, err, "no local fallback")
}

func TestRemoteACPInitializesWithoutLocalRunnerOrDatabase(t *testing.T) {
	base := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(base, []byte("no local store"), 0o600))
	t.Setenv("KODELET_BASE_PATH", base)
	cmd := remoteACPCommandForTest()
	require.NoError(t, cmd.ParseFlags([]string{"--auth-token=client-token"}))
	cmd.SetIn(strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"protocolVersion\":1}}\n"))
	var output bytes.Buffer
	cmd.SetOut(&output)
	require.NoError(t, runRemoteACP(t.Context(), cmd, "https://daemon.example"))
	assert.Contains(t, output.String(), `"protocolVersion":1`)
}

func TestRemoteACPDiscoveryRejectsInvalidOptionsBeforeHTTP(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	client, err := chat.NewControlPlaneChatRunner(server.URL, "client-token", "")
	require.NoError(t, err)
	for _, options := range []*llmtypes.ExecutionOptions{
		{Model: new("model")},
		{MaxTurns: new(0)},
		{AllowedCommands: new([]string{""})},
		{AllowedCommands: new([]string{strings.Repeat("x", 17*1024)})},
	} {
		_, err := client.DiscoverWorkspace(t.Context(), chat.WorkspaceTarget{RunnerID: "runner", Options: options})
		require.Error(t, err)
	}
	_, err = client.DiscoverWorkspace(t.Context(), chat.WorkspaceTarget{})
	require.Error(t, err)
	assert.Zero(t, calls.Load())
}

func TestRemoteACPProcessHasNoLocalExecutionOwnership(t *testing.T) {
	for _, outcome := range []string{"complete", "detach", "cancel"} {
		t.Run(outcome, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			requests := make(chan chat.ChatRequest, 1)
			stops := make(chan string, 1)
			var discoveryCalls atomic.Int32
			daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "Bearer client-only", r.Header.Get("Authorization"))
				switch r.URL.Path {
				case "/api/runners":
					assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"runners": []runnerregistry.Runner{{ID: "registered", DisplayName: "workstation", Connected: true, Status: runnerregistry.RunnerStatusIdle}}}))
				case "/api/chat/slash-commands":
					discoveryCalls.Add(1)
					assert.Equal(t, "registered", r.URL.Query().Get("runnerId"))
					assert.Equal(t, "/runner-only/outside-client-workspace", r.URL.Query().Get("cwd"))
					var options llmtypes.ExecutionOptions
					assert.NoError(t, json.Unmarshal([]byte(r.URL.Query().Get("options")), &options))
					assert.Equal(t, new(true), options.NoExtensions)
					assert.Equal(t, new(true), options.NoSkills)
					assert.Nil(t, options.Model)
					assert.NoError(t, json.NewEncoder(w).Encode(protocol.WorkspaceDiscoverResult{CWD: "/runner-only/outside-client-workspace", EnvironmentProfile: "environment"}))
				case "/api/chat":
					var request chat.ChatRequest
					if err := json.NewDecoder(r.Body).Decode(&request); !assert.NoError(t, err) {
						return
					}
					w.Header().Set("Content-Type", "application/x-ndjson")
					assert.NoError(t, json.NewEncoder(w).Encode(chat.ChatEvent{Kind: "conversation", ConversationID: request.ConversationID}))
					w.(http.Flusher).Flush()
					requests <- request
					if outcome == "complete" {
						assert.NoError(t, json.NewEncoder(w).Encode(chat.ChatEvent{Kind: "text-delta", Delta: "daemon-only answer"}))
						assert.NoError(t, json.NewEncoder(w).Encode(chat.ChatEvent{Kind: "done"}))
					} else {
						select {
						case <-r.Context().Done():
						case <-ctx.Done():
						}
					}
				default:
					if strings.HasSuffix(r.URL.Path, "/stop") {
						stops <- r.URL.Query().Get("turnId")
						assert.NoError(t, json.NewEncoder(w).Encode(map[string]bool{"stopped": true}))
						return
					}
					http.NotFound(w, r)
				}
			}))
			defer daemon.Close()
			root := t.TempDir()
			invalidStore := filepath.Join(root, "no-client-database")
			require.NoError(t, os.WriteFile(invalidStore, []byte("not a directory"), 0o600))
			// This child receives neither provider nor runner credentials. A file
			// at the state path makes accidental local database/runner setup fail.
			environment := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "KODELET_TEST_CLI_PROCESS=1", "KODELET_BASE_PATH=" + invalidStore, "KODELET_SERVER=" + daemon.URL}
			process := daemonCLIProcess(ctx, t, root, environment, "acp", "--auth-token=client-only", "--runner=workstation", "--model=central-model", "--no-extensions", "--no-skills", "--allowed-tools=", "--no-tools=false", "--max-turns=0")
			input, err := process.StdinPipe()
			require.NoError(t, err)
			output, err := process.StdoutPipe()
			require.NoError(t, err)
			var stderr bytes.Buffer
			process.Stderr = &stderr
			require.NoError(t, process.Start())
			waited := false
			defer func() {
				if !waited {
					cancel()
					_ = process.Wait()
				}
				if t.Failed() {
					t.Log(stderr.String())
				}
			}()
			encoder, decoder := json.NewEncoder(input), json.NewDecoder(output)
			readResponse := func(id int) map[string]any {
				t.Helper()
				for {
					var response map[string]any
					require.NoError(t, decoder.Decode(&response))
					if response["id"] == float64(id) {
						require.Nil(t, response["error"], "%v", response["error"])
						return response["result"].(map[string]any)
					}
				}
			}
			require.NoError(t, encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": 1}}))
			assert.Equal(t, float64(1), readResponse(1)["protocolVersion"])
			require.NoError(t, encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "session/new", "params": map[string]any{"cwd": "/runner-only/outside-client-workspace"}}))
			sessionID := readResponse(2)["sessionId"].(string)
			require.NoError(t, encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": 3, "method": "session/prompt", "params": map[string]any{"sessionId": sessionID, "prompt": []map[string]string{{"type": "text", "text": "work"}}}}))
			var request chat.ChatRequest
			select {
			case request = <-requests:
			case <-ctx.Done():
				require.FailNow(t, "ACP did not submit a daemon request")
			}
			assert.Equal(t, sessionID, request.ConversationID)
			assert.Equal(t, "registered", request.RunnerID)
			assert.Equal(t, "/runner-only/outside-client-workspace", request.CWD)
			assert.Equal(t, "environment", request.EnvironmentProfile)
			require.NotNil(t, request.Options)
			assert.Equal(t, new("central-model"), request.Options.Model)
			assert.Equal(t, new([]string{}), request.Options.AllowedTools)
			assert.Equal(t, new(false), request.Options.NoTools)
			assert.Equal(t, new(0), request.Options.MaxTurns)
			switch outcome {
			case "cancel":
				require.NoError(t, encoder.Encode(map[string]any{"jsonrpc": "2.0", "method": "session/cancel", "params": map[string]string{"sessionId": sessionID}}))
				assert.Equal(t, "cancelled", readResponse(3)["stopReason"])
				select {
				case turnID := <-stops:
					assert.NotEmpty(t, turnID)
					assert.Equal(t, request.TurnID, turnID)
				case <-ctx.Done():
					require.FailNow(t, "ACP did not send scoped cancellation")
				}
			case "complete":
				assert.Equal(t, "end_turn", readResponse(3)["stopReason"])
			}
			require.NoError(t, input.Close())
			err = process.Wait()
			waited = true
			require.NoError(t, err, "stderr: %s", stderr.String())
			assert.Empty(t, stops, "EOF/detach must not issue an additional stop")
			assert.GreaterOrEqual(t, discoveryCalls.Load(), int32(1))
		})
	}
}
