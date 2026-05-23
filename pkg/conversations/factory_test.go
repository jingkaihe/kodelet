package conversations

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/conversations/sqlite"
	convdb "github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
)

func lockSQLiteDatabase(t *testing.T, dbPath string) func() {
	t.Helper()

	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)

	_, err = db.Exec("CREATE TABLE IF NOT EXISTS t (id INTEGER PRIMARY KEY)")
	require.NoError(t, err)

	_, err = db.Exec("BEGIN EXCLUSIVE")
	require.NoError(t, err)

	return func() {
		_, _ = db.Exec("ROLLBACK")
		_ = db.Close()
	}
}

func TestDirectSQLiteReturnProducesTypedNilInterfaceOnError(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "storage.db")
	unlock := lockSQLiteDatabase(t, dbPath)
	defer unlock()

	directReturn := func(ctx context.Context, dbPath string) (ConversationStore, error) {
		return sqlite.NewStore(ctx, dbPath)
	}

	store, err := directReturn(context.Background(), dbPath)

	require.Error(t, err)
	assert.False(t, store == nil, "direct interface return should preserve typed-nil and reproduce the bug")
}

func TestNewConversationStore_ReturnsNilInterfaceOnError(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "storage.db")
	unlock := lockSQLiteDatabase(t, dbPath)
	defer unlock()

	store, err := NewConversationStore(context.Background(), &Config{
		StoreType: "sqlite",
		BasePath:  tmpDir,
	})

	require.Error(t, err)
	assert.True(t, store == nil, "store interface should be nil when initialization fails")
}

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

	store, err := NewConversationStore(context.Background(), nil)
	assert.Nil(t, store)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create default config")

	service, err := GetDefaultConversationService(context.Background())
	assert.Nil(t, service)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get conversation store")

	conversationID, err := GetMostRecentConversationID(context.Background())
	assert.Empty(t, conversationID)
	require.Error(t, err)
}

func TestNewConversationStoreCreatesSQLiteStorageForDefaultAndUnknownStoreType(t *testing.T) {
	ctx := context.Background()

	for _, tt := range []struct {
		name   string
		config *Config
	}{
		{
			name:   "nil config uses defaults",
			config: nil,
		},
		{
			name: "unknown store type defaults to sqlite",
			config: &Config{
				StoreType: "unknown",
				BasePath:  t.TempDir(),
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			basePath := t.TempDir()
			config := tt.config
			if config == nil {
				t.Setenv("KODELET_BASE_PATH", basePath)
			}

			store, err := NewConversationStore(ctx, config)

			require.NoError(t, err)
			require.NotNil(t, store)
			require.NoError(t, store.Close())
			if config == nil {
				assert.FileExists(t, filepath.Join(basePath, "storage.db"))
			} else {
				assert.FileExists(t, filepath.Join(config.BasePath, "storage.db"))
			}
		})
	}
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

	database, err := convdb.Open(ctx, filepath.Join(basePath, "storage.db"))
	require.NoError(t, err)
	runner := convdb.NewMigrationRunner(database)
	require.NoError(t, runner.Run(ctx, migrations.All()))
	require.NoError(t, database.Close())

	store, err := NewConversationStore(ctx, &Config{StoreType: "sqlite", BasePath: basePath})
	require.NoError(t, err)
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
	require.NoError(t, store.Close())

	database, err = convdb.Open(ctx, filepath.Join(basePath, "storage.db"))
	require.NoError(t, err)
	_, err = database.ExecContext(ctx, `UPDATE conversation_summaries SET updated_at = ? WHERE id = ?`, now, "older")
	require.NoError(t, err)
	_, err = database.ExecContext(ctx, `UPDATE conversation_summaries SET updated_at = ? WHERE id = ?`, now.Add(time.Minute), "newer")
	require.NoError(t, err)
	require.NoError(t, database.Close())

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
