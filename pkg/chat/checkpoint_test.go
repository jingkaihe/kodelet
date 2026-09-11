package chat

import (
	"context"
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
			thread.EnablePersistence(t.Context(), true)
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
			reloaded.EnablePersistence(t.Context(), true)
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
			reloaded.EnablePersistence(t.Context(), true)
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
