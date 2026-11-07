package conversations

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/conversations/sqlite"
	convdb "github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/dbtest"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfigUsesKODELETBasePath(t *testing.T) {
	basePath := filepath.Join(t.TempDir(), "kodelet-home")
	t.Setenv("KODELET_BASE_PATH", basePath)

	config, err := DefaultConfig()

	require.NoError(t, err)
	require.NotNil(t, config)
	assert.Equal(t, "sqlite", config.StoreType)
	assert.Equal(t, basePath, config.BasePath)
}

func TestDefaultStoreHelpersReturnBasePathErrors(t *testing.T) {
	basePathFile := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(basePathFile, []byte("file"), 0o644))
	t.Setenv("KODELET_BASE_PATH", basePathFile)

	config, err := DefaultConfig()
	assert.Nil(t, config)
	require.Error(t, err)

	service, err := GetDefaultConversationService(context.Background())
	assert.Nil(t, service)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get conversation store")

	conversationID, err := GetMostRecentConversationID(context.Background())
	assert.Empty(t, conversationID)
	require.Error(t, err)
}

func TestNewConversationStoreUsesCallersDatabase(t *testing.T) {
	database := dbtest.OpenTemp(t)
	store := NewConversationStore(database)
	require.IsType(t, &sqlite.Store{}, store)
	require.NoError(t, store.Save(t.Context(), convtypes.NewConversationRecord("shared")))

	// The caller owns the handle: closing the store must leave it usable.
	require.NoError(t, store.Close())
	var count int
	require.NoError(t, database.Reader.GetContext(t.Context(), &count, "SELECT COUNT(*) FROM conversations"))
	assert.Equal(t, 1, count)
}

func TestGetConversationStoreHonorsStoreTypeOverride(t *testing.T) {
	basePath := t.TempDir()
	t.Setenv("KODELET_BASE_PATH", basePath)
	t.Setenv("KODELET_CONVERSATION_STORE_TYPE", "unknown")

	store, err := GetConversationStore(context.Background())

	require.NoError(t, err)
	require.NotNil(t, store)
	require.NoError(t, store.Close())
	assert.FileExists(t, filepath.Join(basePath, "storage.db"))
}

func TestGetMostRecentConversationID(t *testing.T) {
	ctx := context.Background()
	basePath := t.TempDir()
	t.Setenv("KODELET_BASE_PATH", basePath)

	database := dbtest.Open(t, filepath.Join(basePath, "storage.db"))
	store := NewConversationStore(database)
	now := time.Now().UTC().Add(-time.Hour)
	records := []convtypes.ConversationRecord{
		{
			ID:          "older",
			RawMessages: json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"older"}]}]`),
			Provider:    "anthropic",
			CreatedAt:   now,
			UpdatedAt:   now,
			Metadata:    map[string]any{},
		},
		{
			ID:          "newer",
			RawMessages: json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"newer"}]}]`),
			Provider:    "anthropic",
			CreatedAt:   now.Add(time.Minute),
			UpdatedAt:   now.Add(time.Minute),
			Metadata:    map[string]any{},
		},
	}
	for _, record := range records {
		require.NoError(t, store.Save(ctx, record))
	}
	_, err := database.Writer.ExecContext(ctx, `UPDATE conversation_summaries SET updated_at = ? WHERE id = ?`, now, "older")
	require.NoError(t, err)
	_, err = database.Writer.ExecContext(ctx, `UPDATE conversation_summaries SET updated_at = ? WHERE id = ?`, now.Add(time.Minute), "newer")
	require.NoError(t, err)

	conversationID, err := GetMostRecentConversationID(ctx)

	require.NoError(t, err)
	assert.Equal(t, "newer", conversationID)
}

func TestGetMostRecentConversationIDReturnsErrorWhenEmpty(t *testing.T) {
	ctx := context.Background()
	basePath := t.TempDir()
	t.Setenv("KODELET_BASE_PATH", basePath)

	database, err := convdb.Open(ctx, filepath.Join(basePath, "storage.db"))
	require.NoError(t, err)
	runner := convdb.NewMigrationRunner(database)
	require.NoError(t, runner.Run(ctx, migrations.All()))
	require.NoError(t, database.Close())

	conversationID, err := GetMostRecentConversationID(ctx)

	assert.Empty(t, conversationID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no conversations found")
}

func TestGetMostRecentConversationIDReturnsQueryError(t *testing.T) {
	ctx := context.Background()
	basePath := t.TempDir()
	t.Setenv("KODELET_BASE_PATH", basePath)

	database, err := convdb.Open(ctx, filepath.Join(basePath, "storage.db"))
	require.NoError(t, err)
	require.NoError(t, database.Close())

	conversationID, err := GetMostRecentConversationID(ctx)

	assert.Empty(t, conversationID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to execute query")
}
