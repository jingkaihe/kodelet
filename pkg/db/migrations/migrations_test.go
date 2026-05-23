package migrations

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jingkaihe/kodelet/pkg/db"
)

func TestAll(t *testing.T) {
	migrations := All()
	require.Len(t, migrations, 7)

	versions := make([]int64, 0, len(migrations))
	for _, migration := range migrations {
		versions = append(versions, migration.Version)
		assert.NotEmpty(t, migration.Description)
		require.NotNil(t, migration.Up)
	}

	assert.Equal(t, []int64{
		20260204163000,
		20260204163001,
		20260204163002,
		20260204163003,
		20260204163004,
		20260226120000,
		20260331120000,
	}, versions)
}

func TestMigrationsCreateExpectedSchema(t *testing.T) {
	ctx := context.Background()
	database := openMigrationsTestDB(t)
	runner := db.NewMigrationRunner(database)

	require.NoError(t, runner.Run(ctx, All()))
	require.NoError(t, runner.Run(ctx, All()))

	assertTableExists(t, database.DB, "conversations")
	assertTableExists(t, database.DB, "conversation_summaries")
	assertTableExists(t, database.DB, "acp_session_updates")
	assertColumnExists(t, database.DB, "conversations", "background_processes")
	assertColumnExists(t, database.DB, "conversations", "cwd")
	assertColumnExists(t, database.DB, "conversation_summaries", "provider")
	assertColumnExists(t, database.DB, "conversation_summaries", "metadata")
	assertColumnExists(t, database.DB, "conversation_summaries", "cwd")
	assertIndexExists(t, database.DB, "idx_conversations_created_at")
	assertIndexExists(t, database.DB, "idx_summaries_provider")
	assertIndexExists(t, database.DB, "idx_acp_session_updates_session_id")
	assertIndexExists(t, database.DB, "idx_conversations_cwd_updated_at")

	versions, err := runner.GetAppliedVersions(ctx)
	require.NoError(t, err)
	assert.Equal(t, []int64{
		20260204163000,
		20260204163001,
		20260204163002,
		20260204163003,
		20260204163004,
		20260226120000,
		20260331120000,
	}, versions)
}

func TestProviderMetadataAndCWDBackfillMigrations(t *testing.T) {
	ctx := context.Background()
	database := openMigrationsTestDB(t)
	runner := db.NewMigrationRunner(database)
	baseMigrations := All()[:2]

	require.NoError(t, runner.Run(ctx, baseMigrations))
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := database.ExecContext(ctx, `
		INSERT INTO conversations (id, raw_messages, provider, file_last_access, usage, summary, created_at, updated_at, metadata, tool_results)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "conv-1", `[]`, "openai", `{}`, `{}`, "summary", now, now, `{"profile":"codex"}`, `{}`)
	require.NoError(t, err)
	_, err = database.ExecContext(ctx, `
		INSERT INTO conversation_summaries (id, message_count, first_message, summary, usage, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, "conv-1", 1, "hello", "summary", `{}`, now, now)
	require.NoError(t, err)

	require.NoError(t, runner.Run(ctx, All()[2:6]))

	var provider, metadata string
	require.NoError(t, database.QueryRowContext(ctx, `SELECT provider, metadata FROM conversation_summaries WHERE id = ?`, "conv-1").Scan(&provider, &metadata))
	assert.Equal(t, "openai", provider)
	assert.Equal(t, `{"profile":"codex"}`, metadata)

	_, err = database.ExecContext(ctx, `ALTER TABLE conversations ADD COLUMN cwd TEXT`)
	require.NoError(t, err)
	_, err = database.ExecContext(ctx, `UPDATE conversations SET cwd = ? WHERE id = ?`, "/tmp/project", "conv-1")
	require.NoError(t, err)
	require.NoError(t, runner.Run(ctx, All()[6:]))

	var cwd string
	require.NoError(t, database.QueryRowContext(ctx, `SELECT cwd FROM conversation_summaries WHERE id = ?`, "conv-1").Scan(&cwd))
	assert.Equal(t, "/tmp/project", cwd)
}

func TestColumnMigrationsAreIdempotentWhenColumnsAlreadyExist(t *testing.T) {
	ctx := context.Background()
	database := openMigrationsTestDB(t)
	runner := db.NewMigrationRunner(database)

	require.NoError(t, runner.Run(ctx, All()[:2]))
	_, err := database.ExecContext(ctx, `ALTER TABLE conversation_summaries ADD COLUMN provider TEXT`)
	require.NoError(t, err)
	_, err = database.ExecContext(ctx, `ALTER TABLE conversations ADD COLUMN background_processes TEXT`)
	require.NoError(t, err)
	_, err = database.ExecContext(ctx, `ALTER TABLE conversation_summaries ADD COLUMN metadata TEXT`)
	require.NoError(t, err)
	_, err = database.ExecContext(ctx, `ALTER TABLE conversations ADD COLUMN cwd TEXT`)
	require.NoError(t, err)
	_, err = database.ExecContext(ctx, `ALTER TABLE conversation_summaries ADD COLUMN cwd TEXT`)
	require.NoError(t, err)

	require.NoError(t, runner.Run(ctx, All()[2:]))

	assertColumnExists(t, database.DB, "conversation_summaries", "provider")
	assertColumnExists(t, database.DB, "conversations", "background_processes")
	assertColumnExists(t, database.DB, "conversation_summaries", "metadata")
	assertColumnExists(t, database.DB, "conversations", "cwd")
	assertColumnExists(t, database.DB, "conversation_summaries", "cwd")
	assertIndexExists(t, database.DB, "idx_summaries_provider")
	assertIndexExists(t, database.DB, "idx_conversations_cwd_updated_at")
}

func TestCreateMigrationsAreSafeWhenSchemaAlreadyExists(t *testing.T) {
	ctx := context.Background()
	database := openMigrationsTestDB(t)

	tx, err := database.BeginTx(ctx, nil)
	require.NoError(t, err)
	initial := Migration20260204163000CreateConversations()
	require.NoError(t, initial.Up(tx))
	require.NoError(t, initial.Up(tx))
	require.NoError(t, tx.Commit())
	assertTableExists(t, database.DB, "conversations")
	assertTableExists(t, database.DB, "conversation_summaries")

	tx, err = database.BeginTx(ctx, nil)
	require.NoError(t, err)
	acp := Migration20260204163004CreateACPSessionUpdates()
	require.NoError(t, acp.Up(tx))
	require.NoError(t, acp.Up(tx))
	require.NoError(t, tx.Commit())
	assertTableExists(t, database.DB, "acp_session_updates")
	assertIndexExists(t, database.DB, "idx_acp_session_updates_session_id")
	assertIndexExists(t, database.DB, "idx_acp_session_updates_created_at")
}

func TestMigrationFunctionsReturnTransactionErrors(t *testing.T) {
	ctx := context.Background()
	database := openMigrationsTestDB(t)

	closedTx := func(t *testing.T) *sql.Tx {
		t.Helper()
		tx, err := database.BeginTx(ctx, nil)
		require.NoError(t, err)
		require.NoError(t, tx.Rollback())
		return tx
	}

	for _, tt := range []struct {
		name string
		run  func(*sql.Tx) error
	}{
		{"create conversations up", Migration20260204163000CreateConversations().Up},
		{"create conversations down", Migration20260204163000CreateConversations().Down},
		{"performance indexes up", Migration20260204163001AddPerformanceIndexes().Up},
		{"performance indexes down", Migration20260204163001AddPerformanceIndexes().Down},
		{"provider up", Migration20260204163002AddProviderToSummaries().Up},
		{"provider down", Migration20260204163002AddProviderToSummaries().Down},
		{"background processes up", Migration20260204163003AddBackgroundProcesses().Up},
		{"acp session updates up", Migration20260204163004CreateACPSessionUpdates().Up},
		{"acp session updates down", Migration20260204163004CreateACPSessionUpdates().Down},
		{"metadata up", Migration20260226120000AddMetadataToSummaries().Up},
		{"cwd up", Migration20260331120000AddCWDToConversations().Up},
		{"cwd down", Migration20260331120000AddCWDToConversations().Down},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run(closedTx(t))

			require.Error(t, err)
		})
	}
}

func TestMigrationsDownFunctions(t *testing.T) {
	ctx := context.Background()
	database := openMigrationsTestDB(t)
	runner := db.NewMigrationRunner(database)
	require.NoError(t, runner.Run(ctx, All()))

	// The last migration drops only indexes, because SQLite cannot drop columns cheaply.
	require.NoError(t, runner.Rollback(ctx, All()))
	assertIndexMissing(t, database.DB, "idx_conversations_cwd_updated_at")
	assertIndexMissing(t, database.DB, "idx_summaries_cwd_updated_at")

	// Metadata migration intentionally has no rollback work, but should remove the migration record.
	require.NoError(t, runner.Rollback(ctx, All()))
	versions, err := runner.GetAppliedVersions(ctx)
	require.NoError(t, err)
	assert.NotContains(t, versions, int64(20260226120000))

	// ACPSession rollback drops its table.
	require.NoError(t, runner.Rollback(ctx, All()))
	assertTableMissing(t, database.DB, "acp_session_updates")

	// Background process and provider migrations leave columns in place but still roll back cleanly.
	require.NoError(t, runner.Rollback(ctx, All()))
	require.NoError(t, runner.Rollback(ctx, All()))

	// Performance index rollback removes its indexes.
	require.NoError(t, runner.Rollback(ctx, All()))
	assertIndexMissing(t, database.DB, "idx_conversations_created_at")

	// Initial migration rollback drops the conversation tables.
	require.NoError(t, runner.Rollback(ctx, All()))
	assertTableMissing(t, database.DB, "conversations")
	assertTableMissing(t, database.DB, "conversation_summaries")
}

func openMigrationsTestDB(t *testing.T) *sqlx.DB {
	t.Helper()

	database, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "migrations.db"))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, database.Close())
	})
	return database
}

func assertTableExists(t *testing.T, database *sql.DB, name string) {
	t.Helper()

	var exists bool
	require.NoError(t, database.QueryRow(`SELECT COUNT(*) > 0 FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&exists))
	assert.True(t, exists, "table %s should exist", name)
}

func assertTableMissing(t *testing.T, database *sql.DB, name string) {
	t.Helper()

	var exists bool
	require.NoError(t, database.QueryRow(`SELECT COUNT(*) > 0 FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&exists))
	assert.False(t, exists, "table %s should not exist", name)
}

func assertColumnExists(t *testing.T, database *sql.DB, table, column string) {
	t.Helper()

	var exists bool
	require.NoError(t, database.QueryRow(`SELECT COUNT(*) > 0 FROM pragma_table_info(?1) WHERE name = ?2`, table, column).Scan(&exists))
	assert.True(t, exists, "column %s.%s should exist", table, column)
}

func assertIndexExists(t *testing.T, database *sql.DB, name string) {
	t.Helper()

	var exists bool
	require.NoError(t, database.QueryRow(`SELECT COUNT(*) > 0 FROM sqlite_master WHERE type = 'index' AND name = ?`, name).Scan(&exists))
	assert.True(t, exists, "index %s should exist", name)
}

func assertIndexMissing(t *testing.T, database *sql.DB, name string) {
	t.Helper()

	var exists bool
	require.NoError(t, database.QueryRow(`SELECT COUNT(*) > 0 FROM sqlite_master WHERE type = 'index' AND name = ?`, name).Scan(&exists))
	assert.False(t, exists, "index %s should not exist", name)
}
