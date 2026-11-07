package chat

import (
	"path/filepath"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/db/dbtest"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/steer"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecutorThreadsPersistThroughExecutorStorage(t *testing.T) {
	// Nothing may be opened at the default location.
	basePath := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KODELET_BASE_PATH", basePath)
	t.Setenv("OPENAI_API_KEY", "executor-storage-test")
	database := dbtest.OpenTemp(t)
	storage := llm.Storage{
		Conversations: conversations.NewConversationStore(database),
		Steering:      steer.NewStore(database),
	}
	executor := NewExecutor("", storage)
	t.Cleanup(func() { require.NoError(t, executor.Close()) })

	thread, created, release, err := acquireChatThread(executor, "executor-storage", llmtypes.Config{Provider: "openai", Model: "gpt-4.1"})
	require.NoError(t, err)
	defer release()
	require.True(t, created)
	thread.SetConversationID("executor-storage")
	require.NoError(t, thread.EnablePersistence(t.Context(), true))
	thread.AddUserMessage(t.Context(), "hello")
	require.NoError(t, thread.SaveConversation(t.Context()))

	_, err = storage.Conversations.Load(t.Context(), "executor-storage")
	require.NoError(t, err, "the thread must persist through the executor's store")
	assert.NoFileExists(t, filepath.Join(basePath, "storage.db"), "the thread must not open a database of its own")
}
