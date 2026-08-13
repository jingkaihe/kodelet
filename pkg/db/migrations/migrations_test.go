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
	require.Len(t, migrations, 13)

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
		20260719170000,
		20260806120000,
		20260807120000,
		20260812120000,
		20260813120000,
		20260813130000,
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
	assertTableExists(t, database.DB, "steering_messages")
	assertTableExists(t, database.DB, "runner_registrations")
	assertTableExists(t, database.DB, "runner_runs")
	assertTableExists(t, database.DB, "conversation_runner_affinity")
	assertTableExists(t, database.DB, "web_auth_sessions")
	assertTableExists(t, database.DB, "oidc_login_transactions")
	assertTableExists(t, database.DB, "runner_credentials")
	assertTableExists(t, database.DB, "runner_enrollments")
	assertTableExists(t, database.DB, "runner_dpop_replays")
	assertTableMissing(t, database.DB, "runner_auth_challenges")
	assertTableExists(t, database.DB, "user_api_credentials")
	assertTableExists(t, database.DB, "user_login_authorizations")
	assertColumnExists(t, database.DB, "conversations", "background_processes")
	assertColumnExists(t, database.DB, "conversations", "cwd")
	assertColumnExists(t, database.DB, "conversation_summaries", "provider")
	assertColumnExists(t, database.DB, "conversation_summaries", "metadata")
	assertColumnExists(t, database.DB, "conversation_summaries", "cwd")
	assertColumnExists(t, database.DB, "runner_runs", "manifest_json")
	assertColumnExists(t, database.DB, "conversation_runner_affinity", "environment_profile")
	assertColumnExists(t, database.DB, "web_auth_sessions", "token_sha256")
	assertColumnExists(t, database.DB, "web_auth_sessions", "csrf_token_sha256")
	assertColumnExists(t, database.DB, "oidc_login_transactions", "state_sha256")
	assertColumnExists(t, database.DB, "runner_credentials", "public_key_sha256")
	assertColumnExists(t, database.DB, "runner_credentials", "approved_by")
	assertColumnExists(t, database.DB, "runner_enrollments", "device_code_sha256")
	assertColumnExists(t, database.DB, "runner_enrollments", "approved_by")
	assertColumnExists(t, database.DB, "runner_enrollments", "denied_by")
	assertColumnExists(t, database.DB, "runner_dpop_replays", "jti_sha256")
	assertColumnExists(t, database.DB, "user_api_credentials", "token_sha256")
	assertColumnExists(t, database.DB, "user_api_credentials", "revoked_at")
	assertColumnExists(t, database.DB, "user_login_authorizations", "device_code_sha256")
	assertColumnExists(t, database.DB, "user_login_authorizations", "credential_id")
	assertIndexExists(t, database.DB, "idx_conversations_created_at")
	assertIndexExists(t, database.DB, "idx_summaries_provider")
	assertIndexExists(t, database.DB, "idx_acp_session_updates_session_id")
	assertIndexExists(t, database.DB, "idx_conversations_cwd_updated_at")
	assertIndexExists(t, database.DB, "idx_steering_messages_conversation_id")
	assertIndexExists(t, database.DB, "idx_runner_registrations_status")
	assertIndexExists(t, database.DB, "idx_runner_runs_conversation_created")
	assertIndexExists(t, database.DB, "idx_conversation_runner_affinity_runner")
	assertIndexExists(t, database.DB, "idx_web_auth_sessions_expiry")
	assertIndexExists(t, database.DB, "idx_web_auth_sessions_principal")
	assertIndexExists(t, database.DB, "idx_oidc_login_transactions_expiry")
	assertIndexExists(t, database.DB, "idx_runner_credentials_active_runner")
	assertIndexExists(t, database.DB, "idx_runner_enrollments_status_expiry")
	assertIndexExists(t, database.DB, "idx_runner_enrollments_public_key")
	assertIndexExists(t, database.DB, "idx_runner_dpop_replays_expiry")
	assertIndexExists(t, database.DB, "idx_user_api_credentials_expiry")
	assertIndexExists(t, database.DB, "idx_user_api_credentials_principal")
	assertIndexExists(t, database.DB, "idx_user_login_authorizations_status_expiry")

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
		20260719170000,
		20260806120000,
		20260807120000,
		20260812120000,
		20260813120000,
		20260813130000,
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

	tx, err = database.BeginTx(ctx, nil)
	require.NoError(t, err)
	steering := Migration20260719170000CreateSteeringMessages()
	require.NoError(t, steering.Up(tx))
	require.NoError(t, steering.Up(tx))
	require.NoError(t, tx.Commit())
	assertTableExists(t, database.DB, "steering_messages")
	assertIndexExists(t, database.DB, "idx_steering_messages_conversation_id")

	tx, err = database.BeginTx(ctx, nil)
	require.NoError(t, err)
	runnerState := Migration20260806120000CreateRunnerState()
	require.NoError(t, runnerState.Up(tx))
	require.NoError(t, tx.Commit())

	tx, err = database.BeginTx(ctx, nil)
	require.NoError(t, err)
	controlPlaneAuth := Migration20260812120000CreateControlPlaneAuth()
	require.NoError(t, controlPlaneAuth.Up(tx))
	require.NoError(t, controlPlaneAuth.Up(tx))
	require.NoError(t, tx.Commit())
	assertTableExists(t, database.DB, "web_auth_sessions")
	assertTableExists(t, database.DB, "runner_credentials")
	assertTableExists(t, database.DB, "runner_enrollments")
	assertTableExists(t, database.DB, "runner_auth_challenges")
	assertIndexExists(t, database.DB, "idx_runner_credentials_active_runner")
	assertIndexExists(t, database.DB, "idx_runner_enrollments_status_expiry")
	assertIndexExists(t, database.DB, "idx_runner_auth_challenges_credential_expiry")

	tx, err = database.BeginTx(ctx, nil)
	require.NoError(t, err)
	userCredentials := Migration20260813120000CreateUserAPICredentials()
	require.NoError(t, userCredentials.Up(tx))
	require.NoError(t, userCredentials.Up(tx))
	require.NoError(t, tx.Commit())
	assertTableExists(t, database.DB, "user_api_credentials")
	assertTableExists(t, database.DB, "user_login_authorizations")
	assertIndexExists(t, database.DB, "idx_user_api_credentials_expiry")
	assertIndexExists(t, database.DB, "idx_user_login_authorizations_status_expiry")

	tx, err = database.BeginTx(ctx, nil)
	require.NoError(t, err)
	runnerDPoP := Migration20260813130000CreateRunnerDPoPReplays()
	require.NoError(t, runnerDPoP.Up(tx))
	require.NoError(t, runnerDPoP.Up(tx))
	require.NoError(t, tx.Commit())
	assertTableExists(t, database.DB, "runner_dpop_replays")
	assertTableMissing(t, database.DB, "runner_auth_challenges")
	assertIndexExists(t, database.DB, "idx_runner_dpop_replays_expiry")
}

func TestControlPlaneAuthMigrationConstraints(t *testing.T) {
	ctx := context.Background()
	database := openMigrationsTestDB(t)
	runner := db.NewMigrationRunner(database)
	require.NoError(t, runner.Run(ctx, All()))

	now := time.Now().UTC()
	_, err := database.ExecContext(ctx, `
		INSERT INTO runner_registrations (
			id, owner_id, host_instance_id, workspace_path, workspace_name, status, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, "runner-1", "owner-1", "host-1", "/workspace", "workspace", "offline", now, now)
	require.NoError(t, err)

	publicKey := make([]byte, 32)
	firstFingerprint := make([]byte, 32)
	firstFingerprint[0] = 1
	secondFingerprint := make([]byte, 32)
	secondFingerprint[0] = 2
	_, err = database.ExecContext(ctx, `
		INSERT INTO runner_credentials (
			id, runner_id, key_algorithm, public_key, public_key_sha256, approved_by, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`, "credential-1", "runner-1", "ed25519", publicKey, firstFingerprint, "admin@example.com", now)
	require.NoError(t, err)

	_, err = database.ExecContext(ctx, `
		INSERT INTO runner_credentials (
			id, runner_id, key_algorithm, public_key, public_key_sha256, approved_by, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`, "credential-2", "runner-1", "ed25519", publicKey, secondFingerprint, "admin@example.com", now)
	require.Error(t, err)

	_, err = database.ExecContext(ctx, `UPDATE runner_credentials SET revoked_at = ? WHERE id = ?`, now, "credential-1")
	require.NoError(t, err)
	_, err = database.ExecContext(ctx, `
		INSERT INTO runner_credentials (
			id, runner_id, key_algorithm, public_key, public_key_sha256, approved_by, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`, "credential-2", "runner-1", "ed25519", publicKey, secondFingerprint, "admin@example.com", now)
	require.NoError(t, err)

	deviceCodeHash := make([]byte, 32)
	deviceCodeHash[0] = 3
	_, err = database.ExecContext(ctx, `
		INSERT INTO runner_enrollments (
			id, device_code_sha256, user_code, owner_id, status, protocol_version,
			key_algorithm, public_key, public_key_sha256, host_instance_id,
			workspace_path, workspace_name, poll_interval_seconds, created_at, expires_at,
			runner_id, credential_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "enrollment-1", deviceCodeHash, "ABCD-EFGH", "owner-1", "approved", 1,
		"ed25519", publicKey, secondFingerprint, "host-1", "/workspace", "workspace", 5,
		now, now.Add(time.Minute), "runner-1", "credential-2")
	require.NoError(t, err)

	otherDeviceCodeHash := make([]byte, 32)
	otherDeviceCodeHash[0] = 4
	_, err = database.ExecContext(ctx, `
		INSERT INTO runner_enrollments (
			id, device_code_sha256, user_code, status, protocol_version, key_algorithm,
			public_key, public_key_sha256, host_instance_id, workspace_path,
			workspace_name, poll_interval_seconds, created_at, expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "enrollment-2", otherDeviceCodeHash, "ABCD-EFGH", "pending", 1, "ed25519",
		publicKey, secondFingerprint, "host-2", "/other", "other", 5, now, now.Add(time.Minute))
	require.Error(t, err)

	jtiHash := make([]byte, 32)
	jtiHash[0] = 5
	_, err = database.ExecContext(ctx, `
		INSERT INTO runner_dpop_replays (
			credential_id, jti_sha256, created_at, expires_at
		) VALUES (?, ?, ?, ?)
	`, "credential-2", jtiHash, now, now.Add(time.Minute))
	require.NoError(t, err)
	_, err = database.ExecContext(ctx, `
		INSERT INTO runner_dpop_replays (
			credential_id, jti_sha256, created_at, expires_at
		) VALUES (?, ?, ?, ?)
	`, "credential-2", jtiHash, now, now.Add(time.Minute))
	require.Error(t, err)

	_, err = database.ExecContext(ctx, `DELETE FROM runner_registrations WHERE id = ?`, "runner-1")
	require.NoError(t, err)

	var count int
	require.NoError(t, database.QueryRowContext(ctx, `SELECT COUNT(*) FROM runner_credentials WHERE runner_id = ?`, "runner-1").Scan(&count))
	assert.Zero(t, count)
	require.NoError(t, database.QueryRowContext(ctx, `SELECT COUNT(*) FROM runner_dpop_replays`).Scan(&count))
	assert.Zero(t, count)

	var enrollmentRunnerID, enrollmentCredentialID sql.NullString
	require.NoError(t, database.QueryRowContext(ctx, `SELECT runner_id, credential_id FROM runner_enrollments WHERE id = ?`, "enrollment-1").Scan(&enrollmentRunnerID, &enrollmentCredentialID))
	assert.False(t, enrollmentRunnerID.Valid)
	assert.False(t, enrollmentCredentialID.Valid)
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
		{"steering messages up", Migration20260719170000CreateSteeringMessages().Up},
		{"steering messages down", Migration20260719170000CreateSteeringMessages().Down},
		{"runner state up", Migration20260806120000CreateRunnerState().Up},
		{"runner state down", Migration20260806120000CreateRunnerState().Down},
		{"runner environment profile up", Migration20260807120000AddRunnerEnvironmentProfile().Up},
		{"runner environment profile down", Migration20260807120000AddRunnerEnvironmentProfile().Down},
		{"control plane auth up", Migration20260812120000CreateControlPlaneAuth().Up},
		{"control plane auth down", Migration20260812120000CreateControlPlaneAuth().Down},
		{"user API credentials up", Migration20260813120000CreateUserAPICredentials().Up},
		{"user API credentials down", Migration20260813120000CreateUserAPICredentials().Down},
		{"runner DPoP replays up", Migration20260813130000CreateRunnerDPoPReplays().Up},
		{"runner DPoP replays down", Migration20260813130000CreateRunnerDPoPReplays().Down},
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

	// DPoP rollback restores the legacy challenge table for the preceding migration.
	require.NoError(t, runner.Rollback(ctx, All()))
	assertTableMissing(t, database.DB, "runner_dpop_replays")
	assertTableExists(t, database.DB, "runner_auth_challenges")

	// User-credential rollback drops non-browser login state.
	require.NoError(t, runner.Rollback(ctx, All()))
	assertTableMissing(t, database.DB, "user_api_credentials")
	assertTableMissing(t, database.DB, "user_login_authorizations")

	// Control-plane-auth rollback drops web and runner authentication state.
	require.NoError(t, runner.Rollback(ctx, All()))
	assertTableMissing(t, database.DB, "web_auth_sessions")
	assertTableMissing(t, database.DB, "oidc_login_transactions")
	assertTableMissing(t, database.DB, "runner_credentials")
	assertTableMissing(t, database.DB, "runner_enrollments")
	assertTableMissing(t, database.DB, "runner_auth_challenges")

	// Runner-environment-profile rollback removes the affinity column.
	require.NoError(t, runner.Rollback(ctx, All()))
	assertColumnMissing(t, database.DB, "conversation_runner_affinity", "environment_profile")

	// Runner-state rollback drops durable runner tables.
	require.NoError(t, runner.Rollback(ctx, All()))
	assertTableMissing(t, database.DB, "runner_registrations")
	assertTableMissing(t, database.DB, "runner_runs")
	assertTableMissing(t, database.DB, "conversation_runner_affinity")

	// Steering rollback drops its queue table.
	require.NoError(t, runner.Rollback(ctx, All()))
	assertTableMissing(t, database.DB, "steering_messages")

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

func assertColumnMissing(t *testing.T, database *sql.DB, table, column string) {
	t.Helper()

	var exists bool
	require.NoError(t, database.QueryRow(`SELECT COUNT(*) > 0 FROM pragma_table_info(?1) WHERE name = ?2`, table, column).Scan(&exists))
	assert.False(t, exists, "column %s.%s should not exist", table, column)
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
