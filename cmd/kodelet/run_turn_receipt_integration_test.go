package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/chat"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These are actual daemon and runner processes. SIGKILL leaves no opportunity
// for an in-memory admission map or graceful shutdown to manufacture a receipt.
func TestDaemonTurnReceiptSurvivesProcessCrashWithoutReplay(t *testing.T) {
	for _, scenario := range []string{"standalone", "embedded", "standalone-checkpoint", "embedded-checkpoint"} {
		t.Run(scenario, func(t *testing.T) {
			placement := strings.TrimSuffix(scenario, "-checkpoint")
			checkpointOnly := strings.HasSuffix(scenario, "-checkpoint")
			ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
			defer cancel()
			root := t.TempDir()
			workspace := filepath.Join(root, "workspace")
			require.NoError(t, os.MkdirAll(workspace, 0o700))
			var calls atomic.Int32
			effectReceived := make(chan struct{})
			var signalEffect sync.Once
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				assert.Equal(t, "Bearer daemon-only-key", r.Header.Get("Authorization"))
				var request struct {
					Messages []struct {
						Role string `json:"role"`
					} `json:"messages"`
				}
				if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&request)) {
					return
				}
				if checkpointOnly {
					signalEffect.Do(func() { close(effectReceived) })
					<-r.Context().Done() // Crash before any provider response or normal loop save.
					return
				}
				if len(request.Messages) > 0 && request.Messages[len(request.Messages)-1].Role == "tool" {
					data, err := os.ReadFile(filepath.Join(workspace, "effects.log"))
					assert.NoError(t, err, "provider must receive a completed real tool, not a validation error")
					assert.Equal(t, "once\n", string(data))
					signalEffect.Do(func() { close(effectReceived) })
					<-r.Context().Done() // Tool completed; kill before terminal provider outcome.
					return
				}
				arguments, _ := json.Marshal(map[string]any{"command": "printf 'once\\n' >> effects.log", "description": "Append one durable test side effect", "timeout": 10})
				delta := map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "effect-once", "type": "function", "function": map[string]any{"name": "bash", "arguments": string(arguments)}}}}
				chunk := map[string]any{"id": "completion", "object": "chat.completion.chunk", "model": "gpt-4o", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": "tool_calls"}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}}
				encoded, _ := json.Marshal(chunk)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", encoded)
			}))
			t.Cleanup(provider.Close)
			settings := map[string]any{"tool_mode": "full", "allowed_tools": []string{"bash"}, "extensions": map[string]any{"enabled": false}, "skills": map[string]any{"enabled": false}}
			daemonSettings := map[string]any{"provider": "openai", "model": "gpt-4o", "weak_model": "gpt-4o", "max_tokens": 256, "reasoning_effort": "medium", "openai": map[string]any{"platform": "openai", "base_url": provider.URL, "api_key_env_var": "KODELET_TEST_PROVIDER_KEY", "api_mode": "chat_completions"}, "serve": map[string]any{"runner_settings": settings}}
			for key, value := range settings {
				daemonSettings[key] = value
			}
			configPath := filepath.Join(root, "daemon.yaml")
			encoded, err := json.Marshal(daemonSettings)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(configPath, encoded, 0o600))
			encoded, err = json.Marshal(settings)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(workspace, "kodelet-config.yaml"), encoded, 0o600))
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			port := listener.Addr().(*net.TCPAddr).Port
			require.NoError(t, listener.Close())
			serverURL := fmt.Sprintf("http://127.0.0.1:%d", port)
			baseEnv := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "KODELET_TEST_CLI_PROCESS=1"}
			daemonEnv := append(append([]string{}, baseEnv...), "KODELET_BASE_PATH="+filepath.Join(root, "daemon-store"), "KODELET_CONFIG_FILE="+configPath, "KODELET_CONFIG_FILE_MODE=isolated", "KODELET_TEST_PROVIDER_KEY=daemon-only-key")
			startDaemon := func() func() {
				process := daemonCLIProcess(ctx, t, root, daemonEnv, "serve", "--host=127.0.0.1", "--port="+strconv.Itoa(port), "--auth-token=client-secret", "--runner-auth-token=runner-secret", "--disable-control-plane-workspace", "--embedded-runner="+strconv.FormatBool(placement == "embedded"), "--runner-workspace="+workspace)
				return startReceiptProcess(t, process)
			}
			crashDaemon := startDaemon()
			if placement == "standalone" {
				runnerEnv := append(append([]string{}, baseEnv...), "KODELET_BASE_PATH="+filepath.Join(root, "runner-state"))
				startReceiptProcess(t, daemonCLIProcess(ctx, t, workspace, runnerEnv, "runner", "start", "--server="+serverURL, "--auth-token=runner-secret", "--name=receipt-runner"))
			}
			var runnerID string
			require.Eventually(t, func() bool {
				runners, _, err := fetchRunners(ctx, serverURL, "client-secret")
				if err != nil {
					return false
				}
				for _, runner := range runners {
					if runner.Connected && runner.Status == runnerregistry.RunnerStatusIdle {
						runnerID = runner.ID
						return true
					}
				}
				return false
			}, 20*time.Second, 20*time.Millisecond)
			client, err := chat.NewControlPlaneChatRunner(serverURL, "client-secret", runnerID)
			require.NoError(t, err)
			req := chat.ChatRequest{ConversationID: "receipt-crash-conversation", TurnID: "receipt-crash-turn", RunnerID: runnerID, CWD: workspace, Message: "perform effect once"}
			data, err := json.Marshal(req)
			require.NoError(t, err)
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/api/chat", strings.NewReader(string(data)))
			require.NoError(t, err)
			request.Header.Set("Authorization", "Bearer client-secret")
			response, err := http.DefaultClient.Do(request)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, response.StatusCode)
			require.NoError(t, response.Body.Close()) // Submission observer is gone.
			select {
			case <-effectReceived:
			case <-ctx.Done():
				require.FailNow(t, "provider/tool checkpoint was not reached")
			}
			if checkpointOnly {
				history, err := client.LoadConversation(ctx, req.ConversationID)
				require.NoError(t, err)
				require.Len(t, history.Messages, 1, "ordinary input is durable before the initial provider response")
				assert.Equal(t, runnerID, history.RunnerID)
				assert.Equal(t, workspace, history.CWD)
			}
			receipt, err := client.GetTurnReceipt(ctx, req.ConversationID, req.TurnID)
			require.NoError(t, err)
			require.Equal(t, "running", receipt.Status)
			require.NotEmpty(t, receipt.RunID)
			_, err = client.Run(ctx, req, &receiptDiscardSink{})
			var pending *chat.TurnPendingError
			require.ErrorAs(t, err, &pending)
			require.NoError(t, client.StopConversationTurn(ctx, req.ConversationID, "cancel-before-admission"))
			crashDaemon()
			stopRestarted := startDaemon()
			require.Eventually(t, func() bool {
				recovered, err := client.GetTurnReceipt(ctx, req.ConversationID, req.TurnID)
				return err == nil && recovered.Status == "interrupted" && recovered.RunID == receipt.RunID
			}, 15*time.Second, 20*time.Millisecond)
			_, err = client.Run(ctx, req, &receiptDiscardSink{})
			require.ErrorContains(t, err, "daemon stopped")
			late := req
			late.TurnID = "cancel-before-admission"
			sink := &receiptDiscardSink{}
			_, err = client.Run(ctx, late, sink)
			require.NoError(t, err)
			assert.True(t, sink.cancelled)
			history, err := client.LoadConversation(ctx, req.ConversationID)
			require.NoError(t, err)
			assert.Equal(t, runnerID, history.RunnerID)
			assert.NotEmpty(t, history.Messages, "receipt is attached to ordinary persisted conversation history")
			assert.Equal(t, workspace, history.CWD)
			if checkpointOnly {
				assert.Len(t, history.Messages, 1, "restart must neither drop nor replay admitted input")
			}
			invalidState := filepath.Join(root, "client-state-is-not-directory")
			require.NoError(t, os.WriteFile(invalidState, []byte("no client persistence"), 0o600))
			clientEnv := append(append([]string{}, baseEnv...), "KODELET_BASE_PATH="+invalidState)
			query := daemonCLIProcess(ctx, t, root, clientEnv, "conversation", "turn", req.ConversationID, req.TurnID, "--server="+serverURL, "--auth-token=client-secret")
			output, err := query.CombinedOutput()
			require.NoError(t, err, "%s", output)
			var queried chat.TurnReceipt
			require.NoError(t, json.Unmarshal(output, &queried))
			assert.Equal(t, "interrupted", queried.Status)
			stopRestarted()
			effects, err := os.ReadFile(filepath.Join(workspace, "effects.log"))
			if checkpointOnly {
				assert.True(t, os.IsNotExist(err), "checkpoint-only execution never reaches a tool")
				assert.EqualValues(t, 1, calls.Load(), "restart does not replay the admitted initial input")
			} else {
				require.NoError(t, err)
				assert.Equal(t, "once\n", string(effects))
				assert.EqualValues(t, 2, calls.Load(), "neither duplicate submission nor restart replays provider/tool work")
			}
		})
	}
}

type receiptDiscardSink struct{ cancelled bool }

func (s *receiptDiscardSink) Send(event chat.ChatEvent) error {
	s.cancelled = s.cancelled || event.Cancelled
	return nil
}

func startReceiptProcess(t *testing.T, process *exec.Cmd) func() {
	t.Helper()
	log, err := os.CreateTemp(t.TempDir(), "process-*.log")
	require.NoError(t, err)
	process.Stdout, process.Stderr = log, log
	require.NoError(t, process.Start())
	stop := sync.OnceFunc(func() {
		_ = process.Process.Kill()
		_ = process.Wait()
		_ = log.Close()
		if t.Failed() {
			data, _ := os.ReadFile(log.Name())
			t.Logf("process output (tail): %s", data[max(0, len(data)-12000):])
		}
	})
	t.Cleanup(stop)
	return stop
}
