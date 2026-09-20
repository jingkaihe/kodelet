package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	"github.com/jingkaihe/kodelet/pkg/llm"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPendingUserCheckpointPreservesLiveContextAcrossProviders(t *testing.T) {
	for _, provider := range []string{"anthropic", "chat_completions", "responses"} {
		t.Run(provider, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("KODELET_BASE_PATH", t.TempDir())
			t.Setenv("ANTHROPIC_API_KEY", "checkpoint-test")
			t.Setenv("OPENAI_API_KEY", "checkpoint-test")
			require.NoError(t, db.RunMigrations(t.Context(), migrations.All()))
			var calls atomic.Int32
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer api.Close()
			config := llmtypes.Config{
				Provider: "openai", Model: "gpt-4.1", WorkingDirectory: filepath.Join(t.TempDir(), "runner-only"),
				OpenAI: &llmtypes.OpenAIConfig{BaseURL: api.URL, APIMode: llmtypes.OpenAIAPIMode(provider)},
			}
			if provider == "anthropic" {
				config.Provider, config.Model, config.OpenAI = "anthropic", "claude-sonnet-4-6", nil
				config.AnthropicAPIAccess = llmtypes.AnthropicAPIAccessAPIKey
				config.Anthropic = &llmtypes.AnthropicConfig{BaseURL: api.URL}
			}
			thread, err := llm.NewThread(config)
			require.NoError(t, err)
			defer llm.CloseThread(thread)
			thread.SetConversationID("checkpoint")
			saver, ok := thread.(llmtypes.PendingUserMessageSaver)
			require.True(t, ok)
			require.Error(t, saver.SavePendingUserMessage(t.Context(), "not persisted"))
			require.NoError(t, thread.EnablePersistence(t.Context(), true))
			thread.SetMetadataValue(RunnerIDMetadataKey, "runner")
			thread.SetMetadataValue(convtypes.ParentConversationIDMetadataKey, "parent")
			thread.AddUserMessage(t.Context(), "previous input")
			require.NoError(t, thread.SaveConversation(t.Context()))
			before, err := thread.GetMessages()
			require.NoError(t, err)
			require.NoError(t, saver.SavePendingUserMessage(t.Context(), "admitted input"))
			after, err := thread.GetMessages()
			require.NoError(t, err)
			assert.Equal(t, before, after, "checkpoint must not enter live pre-turn compaction/user.message context")
			store, err := conversations.GetConversationStore(t.Context())
			require.NoError(t, err)
			defer store.Close()
			record, err := store.Load(t.Context(), "checkpoint")
			require.NoError(t, err)
			assert.Equal(t, config.WorkingDirectory, record.CWD)
			assert.Equal(t, "runner", record.Metadata[RunnerIDMetadataKey])
			assert.Equal(t, "parent", convtypes.ParentConversationIDFromMetadata(record.Metadata))
			assert.Contains(t, record.Metadata, "config_snapshot")
			reloaded, err := llm.NewThread(config)
			require.NoError(t, err)
			defer llm.CloseThread(reloaded)
			reloaded.SetConversationID("checkpoint")
			require.NoError(t, reloaded.EnablePersistence(t.Context(), true))
			assert.Equal(t, "parent", convtypes.ParentConversationIDFromMetadata(reloaded.GetMetadata()))
			messages, err := reloaded.GetMessages()
			require.NoError(t, err)
			require.Len(t, messages, 2)
			messages, err = llm.ExtractMessages(record.Provider, record.RawMessages, record.Metadata, record.ToolResults)
			require.NoError(t, err)
			assert.Equal(t, "admitted input", messages[1].Content)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			require.Error(t, saver.SavePendingUserMessage(ctx, "cancelled checkpoint"))
			after, err = thread.GetMessages()
			require.NoError(t, err)
			assert.Equal(t, before, after)
			// Normal SendMessage appends the transformed input, not the checkpoint.
			thread.AddUserMessage(t.Context(), "transformed input")
			require.NoError(t, thread.SaveConversation(t.Context()))
			require.NoError(t, reloaded.EnablePersistence(t.Context(), true))
			messages, err = reloaded.GetMessages()
			require.NoError(t, err)
			require.Len(t, messages, 2)
			record, err = store.Load(t.Context(), "checkpoint")
			require.NoError(t, err)
			messages, err = llm.ExtractMessages(record.Provider, record.RawMessages, record.Metadata, record.ToolResults)
			require.NoError(t, err)
			assert.Equal(t, "transformed input", messages[1].Content)
			listed, err := store.Query(t.Context(), convtypes.QueryOptions{})
			require.NoError(t, err)
			assert.Len(t, listed.ConversationSummaries, 1)
			assert.Zero(t, calls.Load(), "checkpointing must not invoke a model or discover a workspace")
		})
	}
}

func TestResumeLoadFailurePreservesDurableTranscript(t *testing.T) {
	originalSettings := viper.AllSettings()
	t.Cleanup(func() {
		viper.Reset()
		for key, value := range originalSettings {
			viper.Set(key, value)
		}
	})
	viper.Reset()
	viper.Set("extensions.enabled", false)
	var calls atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer api.Close()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("OPENAI_API_KEY", "test-key")
	require.NoError(t, db.RunMigrations(t.Context(), migrations.All()))
	store, err := conversations.GetConversationStore(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	dbPath, err := db.DefaultDBPath()
	require.NoError(t, err)
	sqlDB, err := db.Open(t.Context(), dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })

	for _, provider := range []string{"anthropic", "chat_completions", "responses"} {
		for _, failure := range []string{"invalid boundary", "malformed messages"} {
			t.Run(provider+"/"+failure, func(t *testing.T) {
				config := llmtypes.Config{
					Provider:         "openai",
					Model:            "gpt-4.1",
					WorkingDirectory: t.TempDir(),
					Retry:            llmtypes.RetryConfig{Attempts: 1},
					OpenAI:           &llmtypes.OpenAIConfig{BaseURL: api.URL, APIMode: llmtypes.OpenAIAPIMode(provider)},
				}
				raw := `[{"role":"user","content":"seed"}]`
				switch provider {
				case "anthropic":
					config.Provider, config.Model, config.OpenAI = "anthropic", "claude-sonnet-4-6", nil
					config.AnthropicAPIAccess = llmtypes.AnthropicAPIAccessAPIKey
					config.Anthropic = &llmtypes.AnthropicConfig{BaseURL: api.URL}
					raw = `[{"role":"user","content":[{"type":"text","text":"seed"}]}]`
				case "responses":
					raw = `[{"type":"message","role":"user","content":"seed"}]`
				}
				record := convtypes.NewConversationRecord(provider + "-" + failure)
				record.Provider, record.CWD = config.Provider, config.WorkingDirectory
				record.RawMessages = json.RawMessage(raw)
				record.Metadata, err = conversations.AddConfigSnapshot(map[string]any{"api_mode": provider}, config)
				require.NoError(t, err)
				record.CompactionHistory = &convtypes.CompactionHistory{
					ActiveDisplayStart: 1,
					Segments: []convtypes.CompactedSegment{{
						RawMessages: json.RawMessage(raw),
						Marker:      llmtypes.CompactionMarker{ID: "compact-1", Method: "summary", Summary: "seed"},
					}},
				}
				require.NoError(t, store.Save(t.Context(), record))
				// Simulate an older/corrupt stored record, bypassing save-time validation.
				if failure == "invalid boundary" {
					record.CompactionHistory.ActiveDisplayStart = 99
				} else {
					raw = `{"not":"a message array"}`
				}
				history, err := json.Marshal(record.CompactionHistory)
				require.NoError(t, err)
				_, err = sqlDB.ExecContext(t.Context(),
					`UPDATE conversations SET raw_messages = ?, compaction_history = ? WHERE id = ?`,
					raw, string(history), record.ID,
				)
				require.NoError(t, err)

				thread, err := llm.NewThread(config)
				require.NoError(t, err)
				defer llm.CloseThread(thread)
				thread.SetConversationID(record.ID)
				require.ErrorContains(t, thread.EnablePersistence(t.Context(), true), "failed to load conversation")
				assert.False(t, thread.IsPersisted())
				thread.AddUserMessage(t.Context(), "must not overwrite the archive")
				require.NoError(t, thread.SaveConversation(t.Context()))

				runner := NewExecutor(config.WorkingDirectory)
				defer runner.Close()
				for range 2 {
					_, err = runner.Run(t.Context(),
						ChatRequest{ConversationID: record.ID, Message: "continue"}, &recordingChatSink{},
					)
					require.ErrorContains(t, err, "failed to load conversation")
					assert.NotContains(t, runner.sessions, record.ID, "failed thread must not remain in the live cache")
				}
				err = persistDirectCommandResponse(t.Context(), runner, record.ID, config, "", "", "/command", "reply", nil)
				require.ErrorContains(t, err, "failed to load conversation")
				assert.NotContains(t, runner.sessions, record.ID)
				var storedRaw, storedHistory string
				require.NoError(t, sqlDB.QueryRowContext(t.Context(),
					`SELECT raw_messages, compaction_history FROM conversations WHERE id = ?`, record.ID,
				).Scan(&storedRaw, &storedHistory))
				assert.Equal(t, raw, storedRaw)
				assert.Equal(t, string(history), storedHistory)
				assert.Zero(t, calls.Load(), "failed loads must not reach inference")
			})
		}
	}
}
