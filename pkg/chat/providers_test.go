package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/auth"
	conversationservice "github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/tools"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestControlPlaneProviderValidationBeforeTransport(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "secret", "")
	require.NoError(t, err)
	for _, mutation := range []AnthropicAccountMutation{{}, {Action: "unknown"}, {Action: "logout", Alias: "work"}, {Action: "rename", Alias: "work"}, {Action: "remove", Alias: "bad name"}, {Action: "default", Alias: "work", NewAlias: "bad"}} {
		require.Error(t, client.MutateAnthropicAccount(t.Context(), mutation))
	}
	_, err = client.CompleteAnthropicLogin(t.Context(), "id", strings.Repeat("x", 8193), "")
	require.Error(t, err)
	_, err = client.AnthropicAccountUsage(t.Context(), "../work")
	require.Error(t, err)
	_, err = client.StartProviderDeviceLogin(t.Context(), "anthropic")
	require.Error(t, err)
	_, err = client.ProviderConnection(t.Context(), "../unknown")
	require.Error(t, err)
	require.Error(t, client.CancelProviderDeviceLogin(t.Context(), "codex", ""))
	assert.Zero(t, calls.Load())
	require.Error(t, client.MutateAnthropicAccount(t.Context(), AnthropicAccountMutation{Action: "logout"}))
	assert.EqualValues(t, 1, calls.Load(), "mutations must never be retried automatically")
}

func TestExtensionProfileSubscriptionRequests(t *testing.T) {
	previous := viper.AllSettings()
	viper.Reset()
	t.Cleanup(func() { viper.Reset(); require.NoError(t, viper.MergeConfigMap(previous)) })
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(home)
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	require.NoError(t, db.RunMigrations(t.Context(), migrations.All()))
	t.Setenv("OPENAI_API_KEY", "unused-api-key")
	t.Setenv("ANTHROPIC_API_KEY", "unused-api-key")
	viper.Set("provider", "openai")
	viper.Set("anthropic_account", "work")
	viper.Set("anthropic_api_access", "api-key")
	viper.Set("allowed_tools", []string{tools.NoToolsMarker})
	viper.Set("skills.enabled", false)
	viper.Set("extensions.enabled", false)
	viper.Set("retry.attempts", 1)
	_, err := auth.SaveCodexCredentials(&auth.CodexCredentials{
		AccessToken: "fake-codex-token",
		AccountID:   "test-account",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	})
	require.NoError(t, err)
	_, err = auth.SaveAnthropicCredentialsWithAlias("work", &auth.AnthropicCredentials{
		Email:       "work@example.com",
		AccessToken: "fake-claude-token",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	})
	require.NoError(t, err)

	for _, test := range []struct {
		name    string
		path    string
		token   string
		options *llmtypes.ExtensionProfileOptions
		stream  string
	}{
		{
			name:  "codex",
			path:  "/responses",
			token: "fake-codex-token",
			options: &llmtypes.ExtensionProfileOptions{
				Provider:        new("openai"),
				Model:           new("gpt-5.6-luna"),
				ReasoningEffort: new("none"),
				OpenAI: map[string]any{
					"platform":       "codex",
					"api_mode":       "responses",
					"service_tier":   "fast",
					"websocket_mode": false,
				},
			},
			stream: `data: {"type":"response.output_text.delta","delta":"ok"}

data: {"type":"response.output_item.done","output_index":0,"item":{"id":"msg_test","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok","annotations":[]}]}}

data: {"type":"response.completed","response":{"id":"resp_test","status":"completed","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}

`,
		},
		{
			name:  "claude",
			path:  "/v1/messages",
			token: "fake-claude-token",
			options: &llmtypes.ExtensionProfileOptions{
				Provider:           new("anthropic"),
				Model:              new("claude-sonnet-4-6"),
				ReasoningEffort:    new("medium"),
				AnthropicAPIAccess: new(llmtypes.AnthropicAPIAccessSubscription),
				Anthropic:          map[string]any{"platform": "anthropic"},
			},
			stream: `event: message_start
data: {"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}

event: message_stop
data: {"type":"message_stop"}

`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls.Add(1)
				assert.Equal(t, http.MethodPost, req.Method)
				assert.Equal(t, test.path, req.URL.Path)
				assert.Equal(t, "Bearer "+test.token, req.Header.Get("Authorization"))
				assert.Empty(t, req.Header.Get("X-Api-Key"))
				var body map[string]any
				if !assert.NoError(t, json.NewDecoder(req.Body).Decode(&body)) {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				assert.Equal(t, *test.options.Model, body["model"])
				assert.Equal(t, true, body["stream"])
				assert.Empty(t, body["tools"])
				if test.name == "codex" {
					assert.Equal(t, "test-account", req.Header.Get("ChatGPT-Account-ID"))
					assert.Equal(t, "priority", body["service_tier"])
					assert.NotEmpty(t, body["input"])
				} else {
					assert.NotEmpty(t, body["messages"])
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, err := w.Write([]byte(test.stream))
				assert.NoError(t, err)
			}))
			defer server.Close()
			viper.Set(*test.options.Provider+".base_url", server.URL+"/daemon")
			if test.options.OpenAI != nil {
				viper.Set("openai.platform", "codex")
				viper.Set("openai.websocket_mode", false)
			}
			if test.options.OpenAI != nil {
				test.options.OpenAI["base_url"] = server.URL
			} else {
				test.options.Anthropic["base_url"] = server.URL
			}
			profile := extensions.Profile{
				Name:        "subscription-test",
				ExtensionID: "test-extension",
				Options:     test.options.Clone(),
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			ctx = ContextWithProfileResolver(ctx, func(name, effort string) (llmtypes.Config, error) {
				assert.Equal(t, profile.Name, name)
				return ResolveExtensionProfile(profile, effort)
			})
			store, err := conversationservice.GetConversationStore(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, store.Close()) })
			conversationID := ""
			for turn := range 2 {
				config, err := ResolveRemoteConfigWithReasoning(ctx, conversationID, profile.Name, "")
				require.NoError(t, err)
				thread, err := llm.NewThread(config)
				require.NoError(t, err)
				t.Cleanup(func() { assert.NoError(t, llm.CloseThread(thread)) })
				thread.SetState(tools.NewBasicState(ctx, tools.WithLLMConfig(config)))
				output, err := thread.SendMessage(ctx, "say ok", &llmtypes.StringCollectorHandler{Silent: true}, llmtypes.MessageOpt{
					NoToolUse:          true,
					NoSaveConversation: true,
					MaxTurns:           1,
					DisableUsageLog:    true,
				})
				require.NoError(t, err)
				assert.Equal(t, "ok", output)
				if turn == 0 {
					record := convtypes.NewConversationRecord("subscription-" + test.name)
					record.Provider = config.Provider
					record.Metadata, err = conversationservice.AddConfigSnapshot(nil, config)
					require.NoError(t, err)
					require.NoError(t, store.Save(ctx, record))
					conversationID = record.ID
					profile.Options.ReasoningEffort = new("high")
					if test.name == "codex" {
						profile.Options.Model = new("gpt-5.5")
						profile.Options.OpenAI["service_tier"] = "default"
					} else {
						profile.Options.Model = new("claude-opus-4-6")
						profile.Options.AnthropicAPIAccess = new(llmtypes.AnthropicAPIAccessAPIKey)
					}
				}
			}
			assert.EqualValues(t, 2, calls.Load())
		})
	}
}
