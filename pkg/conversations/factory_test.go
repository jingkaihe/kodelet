package conversations

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/conversations/sqlite"
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
