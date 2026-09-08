package controlplane

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The earliest active extension hook is deliberately held before run.open
// returns. A second client must already see ordinary durable user history.
func TestCheckpointExtensionProcess(_ *testing.T) {
	if os.Getenv("KODELET_CHECKPOINT_EXTENSION") != "1" {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	for {
		request, err := readNativeReleaseMessage(reader)
		if err != nil {
			os.Exit(0)
		}
		var result any = map[string]any{}
		switch request.Method {
		case "extension.initialize":
			result = extensions.InitializeResult{Name: "checkpoint", Version: "1", Subscriptions: []extensions.Subscription{{Event: extensions.EventSessionStart}}}
		case "extension.event.handle":
			var params struct {
				Event   string                          `json:"event"`
				Context extensions.ExtensionCallContext `json:"context"`
			}
			_ = json.Unmarshal(request.Params, &params)
			if params.Event == extensions.EventSessionStart {
				_ = os.WriteFile(filepath.Join(params.Context.CWD, params.Context.ConversationID+".started"), []byte("started"), 0o600)
				for {
					if _, err := os.Stat(filepath.Join(params.Context.CWD, params.Context.ConversationID+".release")); err == nil {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
		}
		if len(request.ID) > 0 {
			data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
			_, _ = fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(data), data)
		}
	}
}

type checkpointDiscardSink struct{}

func (*checkpointDiscardSink) Send(chat.ChatEvent) error { return nil }

func checkpointTestModelPolicy(t *testing.T) {
	t.Helper()
	previous := viper.AllSettings()
	viper.Reset()
	t.Cleanup(func() { viper.Reset(); require.NoError(t, viper.MergeConfigMap(previous)) })
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "checkpoint-local-presence-only")
	viper.Set("provider", "anthropic")
	viper.Set("model", "checkpoint-main")
	viper.Set("weak_model", "checkpoint-weak")
	viper.Set("anthropic_api_access", "api-key")
}

func TestFirstTurnCheckpointVisibleBeforeSessionStartAcrossPlacements(t *testing.T) {
	for _, placement := range []string{"embedded", "standalone"} {
		t.Run(placement, func(t *testing.T) {
			checkpointTestModelPolicy(t)
			config := embeddedRunnerTestConfig(t)
			t.Setenv("OPENAI_API_KEY", "central-checkpoint-key")
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				body, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				assert.Equal(t, 1, strings.Count(string(body), "admitted checkpoint input"), "provider receives incoming input exactly once")
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, "data: "+`{"id":"reply","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"checkpoint complete"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`+"\n\ndata: [DONE]\n\n")
			}))
			defer provider.Close()
			viper.Set("provider", "openai")
			viper.Set("model", "gpt-4o")
			viper.Set("weak_model", "gpt-4o")
			viper.Set("openai", map[string]any{"base_url": provider.URL, "api_mode": "chat_completions", "api_key_env_var": "OPENAI_API_KEY"})
			workspace := config.EmbeddedRunner.Workspace
			config.EmbeddedRunner.Settings["extensions"] = map[string]any{"enabled": true}
			directory := filepath.Join(workspace, ".kodelet", "extensions")
			require.NoError(t, os.MkdirAll(directory, 0o700))
			executable, err := os.Executable()
			require.NoError(t, err)
			script := fmt.Sprintf("#!/bin/sh\nKODELET_CHECKPOINT_EXTENSION=1 exec %q -test.run '^TestCheckpointExtensionProcess$'\n", executable)
			require.NoError(t, os.WriteFile(filepath.Join(directory, "kodelet-extension-checkpoint"), []byte(script), 0o700))
			if placement == "standalone" {
				config.EmbeddedRunner = nil
			}
			server, endpoint, _ := startEmbeddedRunnerTestServer(t, config, "127.0.0.1:0")
			var runnerID string
			if placement == "embedded" {
				require.Eventually(t, func() bool { return server.EmbeddedRunnerStatus().Ready }, 5*time.Second, 10*time.Millisecond)
				runnerID = server.EmbeddedRunnerStatus().RunnerID
			} else {
				runnerID = startNativeReleaseRunnerProcess(t, server, endpoint, workspace)
			}
			client, err := chat.NewClient(endpoint, "web-secret", runnerID)
			require.NoError(t, err)
			observer, err := chat.NewClient(endpoint, "web-secret", "")
			require.NoError(t, err)
			for _, outcome := range []string{"success", "cancel", "save-failure"} {
				t.Run(outcome, func(t *testing.T) {
					before := calls.Load()
					req := chat.ChatRequest{ConversationID: "checkpoint-" + outcome, TurnID: "turn", RunnerID: runnerID, CWD: workspace, Message: "admitted checkpoint input"}
					if outcome == "save-failure" {
						_, err := server.turns.db.Exec(`CREATE TRIGGER fail_checkpoint BEFORE INSERT ON conversation_summaries WHEN NEW.id = 'checkpoint-save-failure' BEGIN SELECT RAISE(ABORT, 'disk failure'); END`)
						require.NoError(t, err)
						_, err = client.Run(t.Context(), req, &checkpointDiscardSink{})
						require.ErrorContains(t, err, "checkpoint failed")
						_, err = observer.LoadConversation(t.Context(), req.ConversationID)
						require.Error(t, err)
						_, err = os.Stat(filepath.Join(workspace, req.ConversationID+".started"))
						assert.True(t, os.IsNotExist(err), "no session.start effects on failed save")
						assert.Equal(t, before, calls.Load())
						return
					}
					done := make(chan error, 1)
					go func() { _, err := client.Run(t.Context(), req, &checkpointDiscardSink{}); done <- err }()
					release := func() {
						assert.NoError(t, os.WriteFile(filepath.Join(workspace, req.ConversationID+".release"), nil, 0o600))
					}
					t.Cleanup(release)
					require.Eventually(t, func() bool {
						_, err := os.Stat(filepath.Join(workspace, req.ConversationID+".started"))
						return err == nil
					}, 5*time.Second, 10*time.Millisecond)
					history, err := observer.LoadConversation(t.Context(), req.ConversationID)
					require.NoError(t, err, "second-client GET must work before first run.open returns")
					assert.Equal(t, workspace, history.CWD)
					assert.Equal(t, runnerID, history.RunnerID)
					require.Len(t, history.Messages, 1)
					assert.Equal(t, "user", history.Messages[0].Role)
					assert.Equal(t, before, calls.Load(), "no provider effect yet")
					receipt, err := observer.GetTurnReceipt(t.Context(), req.ConversationID, req.TurnID)
					require.NoError(t, err)
					assert.Equal(t, "running", receipt.Status)
					_, err = observer.Run(t.Context(), req, &checkpointDiscardSink{})
					var pending *chat.TurnPendingError
					require.ErrorAs(t, err, &pending)
					if outcome == "cancel" {
						require.NoError(t, observer.StopConversationTurn(t.Context(), req.ConversationID, req.TurnID))
					} else {
						release()
					}
					select {
					case err := <-done:
						if outcome == "success" {
							require.NoError(t, err)
						}
					case <-time.After(7 * time.Second):
						require.FailNow(t, "checkpoint turn did not finish")
					}
					receipt, err = observer.GetTurnReceipt(t.Context(), req.ConversationID, req.TurnID)
					require.NoError(t, err)
					history, err = observer.LoadConversation(t.Context(), req.ConversationID)
					require.NoError(t, err)
					if outcome == "cancel" {
						assert.Equal(t, "cancelled", receipt.Status)
						require.Len(t, history.Messages, 1, "cancelled initial input remains durable")
						assert.Equal(t, before, calls.Load())
					} else {
						assert.Equal(t, "succeeded", receipt.Status)
						assert.Equal(t, before+1, calls.Load())
					}
					_, err = observer.Run(context.Background(), req, &checkpointDiscardSink{})
					require.NoError(t, err, "terminal receipt reconciles without reexecution")
				})
			}
		})
	}
}
