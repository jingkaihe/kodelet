package registry

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	conversationtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/jmoiron/sqlx"
	"github.com/pkg/errors"
)

const (
	defaultOwnerID          = "local"
	defaultLoadedRunHistory = 1000
)

// PersistedState is the durable registry state restored when the control plane starts.
type PersistedState struct {
	Runners    []Runner
	Runs       []Run
	Affinities map[string]ConversationAffinity
}

// ConversationAffinity is the durable environment selection for one conversation.
type ConversationAffinity struct {
	RunnerID           string
	EnvironmentProfile string
}

// Persistence stores durable runner identity, run state, and conversation affinity.
type Persistence interface {
	Load(ctx context.Context) (PersistedState, error)
	SaveRunner(ctx context.Context, runner Runner) error
	SaveRun(ctx context.Context, run Run) error
	SaveRunnerAndRun(ctx context.Context, runner Runner, run Run) error
	SaveRunnerAndRuns(ctx context.Context, runner Runner, runs []Run) error
	BindConversation(ctx context.Context, conversationID, runnerID, environmentProfile string, now time.Time) error
	ConversationAffinity(ctx context.Context, conversationID string) (ConversationAffinity, bool, error)
	RemoveRunner(ctx context.Context, runnerID string, force bool) (RemovalResult, error)
	Close() error
}

// SQLitePersistence stores runner state in Kodelet's shared SQLite database.
type SQLitePersistence struct {
	db      *sqlx.DB
	ownerID string
}

// NewSQLitePersistence opens a durable runner store at dbPath.
// Database migrations must have run before this constructor is used.
func NewSQLitePersistence(ctx context.Context, dbPath, ownerID string) (*SQLitePersistence, error) {
	sqlDB, err := db.Open(ctx, dbPath)
	if err != nil {
		return nil, err
	}
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		ownerID = defaultOwnerID
	}
	return &SQLitePersistence{db: sqlDB, ownerID: ownerID}, nil
}

// Load restores records for this authenticated owner.
func (s *SQLitePersistence) Load(ctx context.Context) (PersistedState, error) {
	if s == nil || s.db == nil {
		return PersistedState{}, errors.New("runner persistence is closed")
	}

	var runnerRows []runnerPersistenceRow
	if err := s.db.SelectContext(ctx, &runnerRows, `
		SELECT id, display_name, host_instance_id, hostname, host_os, host_arch, host_pid,
			workspace_path, workspace_name, kodelet_version, manifest_digest,
			manifest_changed, compatibility_error, status, active_run_id, generation,
			connected_at, last_heartbeat_at, created_at, updated_at
		FROM runner_registrations
		WHERE owner_id = ?
		ORDER BY id
	`, s.ownerID); err != nil {
		return PersistedState{}, errors.Wrap(err, "failed to load runner registrations")
	}

	state := PersistedState{
		Runners:    make([]Runner, 0, len(runnerRows)),
		Affinities: make(map[string]ConversationAffinity),
	}
	for _, row := range runnerRows {
		state.Runners = append(state.Runners, row.runner())
	}
	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM runner_runs
		WHERE runner_id IN (
			SELECT id FROM runner_registrations WHERE owner_id = ?
		) AND status NOT IN (?, ?) AND NOT EXISTS (
			SELECT 1 FROM conversations c WHERE c.id = runner_runs.conversation_id
		)
	`, s.ownerID, RunStatusOpening, RunStatusRunning); err != nil {
		return PersistedState{}, errors.Wrap(err, "failed to prune orphaned runner runs")
	}

	if err := s.db.SelectContext(ctx, &state.Runs, `
		SELECT rr.id, rr.conversation_id, rr.runner_id, rr.status,
			COALESCE(rr.manifest_digest, '') AS manifest_digest,
			COALESCE(rr.manifest_json, '') AS manifest_json,
			COALESCE(rr.error, '') AS error,
			rr.created_at, rr.updated_at
		FROM runner_runs rr
		JOIN runner_registrations r ON r.id = rr.runner_id
		WHERE r.owner_id = ? AND (
			rr.status IN (?, ?)
			OR rr.id IN (
				SELECT rr2.id
				FROM runner_runs rr2
				JOIN runner_registrations r2 ON r2.id = rr2.runner_id
				WHERE r2.owner_id = ? AND rr2.status NOT IN (?, ?)
				ORDER BY rr2.created_at DESC, rr2.id DESC
				LIMIT ?
			)
		)
		ORDER BY rr.created_at, rr.id
	`, s.ownerID, RunStatusOpening, RunStatusRunning, s.ownerID, RunStatusOpening, RunStatusRunning, defaultLoadedRunHistory); err != nil {
		return PersistedState{}, errors.Wrap(err, "failed to load runner runs")
	}

	var affinities []conversationAffinityRow
	if err := s.db.SelectContext(ctx, &affinities, `
		SELECT a.conversation_id, a.runner_id, a.environment_profile
		FROM conversation_runner_affinity a
		JOIN runner_registrations r ON r.id = a.runner_id
		WHERE r.owner_id = ?
	`, s.ownerID); err != nil {
		return PersistedState{}, errors.Wrap(err, "failed to load runner conversation affinity")
	}
	for _, affinity := range affinities {
		state.Affinities[affinity.ConversationID] = ConversationAffinity{
			RunnerID:           affinity.RunnerID,
			EnvironmentProfile: affinity.EnvironmentProfile,
		}
	}
	return state, nil
}

// SaveRunner inserts or updates one stable runner registration.
func (s *SQLitePersistence) SaveRunner(ctx context.Context, runner Runner) error {
	if s == nil || s.db == nil {
		return errors.New("runner persistence is closed")
	}
	return s.saveRunner(ctx, s.db, runner)
}

func (s *SQLitePersistence) saveRunner(ctx context.Context, executor sqlExecutor, runner Runner) error {
	_, err := executor.ExecContext(ctx, `
		INSERT INTO runner_registrations (
			id, owner_id, display_name, host_instance_id, hostname, host_os, host_arch,
			host_pid, workspace_path, workspace_name, kodelet_version, manifest_digest,
			manifest_changed, compatibility_error, status, active_run_id, generation,
			connected_at, last_heartbeat_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			owner_id = excluded.owner_id,
			display_name = excluded.display_name,
			host_instance_id = excluded.host_instance_id,
			hostname = excluded.hostname,
			host_os = excluded.host_os,
			host_arch = excluded.host_arch,
			host_pid = excluded.host_pid,
			workspace_path = excluded.workspace_path,
			workspace_name = excluded.workspace_name,
			kodelet_version = excluded.kodelet_version,
			manifest_digest = excluded.manifest_digest,
			manifest_changed = excluded.manifest_changed,
			compatibility_error = excluded.compatibility_error,
			status = excluded.status,
			active_run_id = excluded.active_run_id,
			generation = excluded.generation,
			connected_at = excluded.connected_at,
			last_heartbeat_at = excluded.last_heartbeat_at,
			updated_at = excluded.updated_at
	`,
		runner.ID,
		s.ownerID,
		nullString(runner.DisplayName),
		runner.Host.InstanceID,
		nullString(runner.Host.Hostname),
		nullString(runner.Host.OS),
		nullString(runner.Host.Arch),
		nullInt(runner.Host.PID),
		runner.Workspace.Path,
		runner.Workspace.Name,
		nullString(runner.KodeletVersion),
		nullString(runner.ManifestDigest),
		runner.ManifestChanged,
		nullString(runner.CompatibilityError),
		runner.Status,
		nullString(runner.ActiveRunID),
		runner.Generation,
		nullTime(runner.ConnectedAt),
		nullTime(runner.LastHeartbeatAt),
		runner.CreatedAt,
		runner.UpdatedAt,
	)
	return errors.Wrap(err, "failed to save runner registration")
}

// SaveRun inserts or updates one top-level runner run.
func (s *SQLitePersistence) SaveRun(ctx context.Context, run Run) error {
	if s == nil || s.db == nil {
		return errors.New("runner persistence is closed")
	}
	return s.saveRun(ctx, s.db, run)
}

func (s *SQLitePersistence) saveRun(ctx context.Context, executor sqlExecutor, run Run) error {
	_, err := executor.ExecContext(ctx, `
		INSERT INTO runner_runs (
			id, conversation_id, runner_id, status, manifest_digest, manifest_json, error, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			conversation_id = excluded.conversation_id,
			runner_id = excluded.runner_id,
			status = excluded.status,
			manifest_digest = excluded.manifest_digest,
			manifest_json = excluded.manifest_json,
			error = excluded.error,
			updated_at = excluded.updated_at
	`, run.ID, run.ConversationID, run.RunnerID, run.Status, nullString(run.ManifestDigest), nullString(run.ManifestJSON), nullString(run.Error), run.CreatedAt, run.UpdatedAt)
	return errors.Wrap(err, "failed to save runner run")
}

// SaveRunnerAndRun atomically stores a runner registration and one of its runs.
func (s *SQLitePersistence) SaveRunnerAndRun(ctx context.Context, runner Runner, run Run) error {
	return s.SaveRunnerAndRuns(ctx, runner, []Run{run})
}

// SaveRunnerAndRuns atomically stores a runner registration and its changed runs.
func (s *SQLitePersistence) SaveRunnerAndRuns(ctx context.Context, runner Runner, runs []Run) error {
	if s == nil || s.db == nil {
		return errors.New("runner persistence is closed")
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "failed to begin runner state transaction")
	}
	defer tx.Rollback()

	if err := s.saveRunner(ctx, tx, runner); err != nil {
		return err
	}
	for _, run := range runs {
		if err := s.saveRun(ctx, tx, run); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return errors.Wrap(err, "failed to commit runner state transaction")
	}
	return nil
}

// BindConversation durably establishes affinity without silently moving an existing conversation.
func (s *SQLitePersistence) BindConversation(ctx context.Context, conversationID, runnerID, environmentProfile string, now time.Time) error {
	if s == nil || s.db == nil {
		return errors.New("runner persistence is closed")
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO conversation_runner_affinity (conversation_id, runner_id, environment_profile, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(conversation_id) DO UPDATE SET
			updated_at = excluded.updated_at
		WHERE conversation_runner_affinity.runner_id = excluded.runner_id
			AND conversation_runner_affinity.environment_profile = excluded.environment_profile
	`, conversationID, runnerID, environmentProfile, now, now)
	if err != nil {
		return errors.Wrap(err, "failed to bind conversation to runner")
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "failed to inspect conversation runner binding")
	}
	if rows == 0 {
		return errors.New("conversation is already bound to another runner")
	}
	return nil
}

// ConversationAffinity reads the current durable affinity so a long-running control plane can observe external deletion.
func (s *SQLitePersistence) ConversationAffinity(ctx context.Context, conversationID string) (ConversationAffinity, bool, error) {
	if s == nil || s.db == nil {
		return ConversationAffinity{}, false, errors.New("runner persistence is closed")
	}
	var row conversationAffinityRow
	err := s.db.GetContext(ctx, &row, `
		SELECT a.conversation_id, a.runner_id, a.environment_profile
		FROM conversation_runner_affinity a
		JOIN runner_registrations r ON r.id = a.runner_id
		WHERE a.conversation_id = ? AND r.owner_id = ?
	`, strings.TrimSpace(conversationID), s.ownerID)
	if errors.Is(err, sql.ErrNoRows) {
		return ConversationAffinity{}, false, nil
	}
	if err != nil {
		return ConversationAffinity{}, false, errors.Wrap(err, "failed to load conversation runner affinity")
	}
	return ConversationAffinity{RunnerID: row.RunnerID, EnvironmentProfile: row.EnvironmentProfile}, true, nil
}

// RemoveRunner deletes one offline registration and its durable run history.
// Conversation records remain owned by the control plane; their concrete runner
// affinities are cleared so they can later be rebound explicitly.
func (s *SQLitePersistence) RemoveRunner(ctx context.Context, runnerID string, force bool) (RemovalResult, error) {
	// force is retained in the persistence contract for CLI/API compatibility.
	// Affinity is now cleared on every removal without deleting conversations.
	_ = force
	if s == nil || s.db == nil {
		return RemovalResult{}, errors.New("runner persistence is closed")
	}
	runnerID = strings.TrimSpace(runnerID)
	if runnerID == "" {
		return RemovalResult{}, errors.New("runner id is required")
	}

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return RemovalResult{}, errors.Wrap(err, "failed to begin runner removal")
	}
	defer tx.Rollback()

	locked, err := tx.ExecContext(ctx, `
		UPDATE runner_registrations
		SET updated_at = updated_at
		WHERE id = ? AND owner_id = ?
	`, runnerID, s.ownerID)
	if err != nil {
		return RemovalResult{}, errors.Wrap(err, "failed to lock runner registration for removal")
	}
	lockedCount, err := locked.RowsAffected()
	if err != nil {
		return RemovalResult{}, errors.Wrap(err, "failed to inspect locked runner registration")
	}
	if lockedCount != 1 {
		return RemovalResult{}, errors.Wrapf(ErrRunnerNotFound, "runner %s", runnerID)
	}

	result := RemovalResult{RunnerID: runnerID}
	for _, table := range []string{"conversations", "conversation_summaries"} {
		if _, err := tx.ExecContext(ctx, `
			UPDATE `+table+`
			SET metadata = json_remove(metadata, ?, ?)
			WHERE id IN (
				SELECT conversation_id FROM conversation_runner_affinity WHERE runner_id = ?
			)
		`, "$."+conversationtypes.RunnerIDMetadataKey, "$."+conversationtypes.RunnerEnvironmentProfileMetadataKey, runnerID); err != nil {
			return RemovalResult{}, errors.Wrapf(err, "failed to clear runner metadata from %s", table)
		}
	}
	removed, err := tx.ExecContext(ctx, "DELETE FROM conversation_runner_affinity WHERE runner_id = ?", runnerID)
	if err != nil {
		return RemovalResult{}, errors.Wrap(err, "failed to delete runner conversation affinities")
	}
	count, err := removed.RowsAffected()
	if err != nil {
		return RemovalResult{}, errors.Wrap(err, "failed to count removed runner conversation affinities")
	}
	result.RemovedConversationAffinities = int(count)

	removedRuns, err := tx.ExecContext(ctx, "DELETE FROM runner_runs WHERE runner_id = ?", runnerID)
	if err != nil {
		return RemovalResult{}, errors.Wrap(err, "failed to delete runner run history")
	}
	runCount, err := removedRuns.RowsAffected()
	if err != nil {
		return RemovalResult{}, errors.Wrap(err, "failed to count removed runner runs")
	}
	result.RemovedRuns = int(runCount)

	removedRunner, err := tx.ExecContext(ctx, "DELETE FROM runner_registrations WHERE id = ? AND owner_id = ?", runnerID, s.ownerID)
	if err != nil {
		return RemovalResult{}, errors.Wrap(err, "failed to delete runner registration")
	}
	runnerCount, err := removedRunner.RowsAffected()
	if err != nil {
		return RemovalResult{}, errors.Wrap(err, "failed to count removed runner registrations")
	}
	if runnerCount != 1 {
		return RemovalResult{}, errors.Wrapf(ErrRunnerNotFound, "runner %s", runnerID)
	}

	if err := tx.Commit(); err != nil {
		return RemovalResult{}, errors.Wrap(err, "failed to commit runner removal")
	}
	return result, nil
}

// Close releases the SQLite connection.
func (s *SQLitePersistence) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

type runnerPersistenceRow struct {
	ID                 string         `db:"id"`
	DisplayName        sql.NullString `db:"display_name"`
	HostInstanceID     string         `db:"host_instance_id"`
	Hostname           sql.NullString `db:"hostname"`
	HostOS             sql.NullString `db:"host_os"`
	HostArch           sql.NullString `db:"host_arch"`
	HostPID            sql.NullInt64  `db:"host_pid"`
	WorkspacePath      string         `db:"workspace_path"`
	WorkspaceName      string         `db:"workspace_name"`
	KodeletVersion     sql.NullString `db:"kodelet_version"`
	ManifestDigest     sql.NullString `db:"manifest_digest"`
	ManifestChanged    bool           `db:"manifest_changed"`
	CompatibilityError sql.NullString `db:"compatibility_error"`
	Status             RunnerStatus   `db:"status"`
	ActiveRunID        sql.NullString `db:"active_run_id"`
	Generation         int64          `db:"generation"`
	ConnectedAt        sql.NullTime   `db:"connected_at"`
	LastHeartbeatAt    sql.NullTime   `db:"last_heartbeat_at"`
	CreatedAt          time.Time      `db:"created_at"`
	UpdatedAt          time.Time      `db:"updated_at"`
}

func (row runnerPersistenceRow) runner() Runner {
	return Runner{
		ID:                 row.ID,
		DisplayName:        row.DisplayName.String,
		Host:               protocol.Host{InstanceID: row.HostInstanceID, Hostname: row.Hostname.String, OS: row.HostOS.String, Arch: row.HostArch.String, PID: int(row.HostPID.Int64)},
		Workspace:          protocol.Workspace{Path: row.WorkspacePath, Name: row.WorkspaceName},
		KodeletVersion:     row.KodeletVersion.String,
		ManifestDigest:     row.ManifestDigest.String,
		ManifestChanged:    row.ManifestChanged,
		CompatibilityError: row.CompatibilityError.String,
		Status:             row.Status,
		ActiveRunID:        row.ActiveRunID.String,
		Generation:         row.Generation,
		ConnectedAt:        row.ConnectedAt.Time,
		LastHeartbeatAt:    row.LastHeartbeatAt.Time,
		CreatedAt:          row.CreatedAt,
		UpdatedAt:          row.UpdatedAt,
	}
}

type conversationAffinityRow struct {
	ConversationID     string `db:"conversation_id"`
	RunnerID           string `db:"runner_id"`
	EnvironmentProfile string `db:"environment_profile"`
}

type sqlExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func nullString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullInt(value int) any {
	if value == 0 {
		return nil
	}
	return value
}

func nullTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
