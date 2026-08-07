package registry

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/jmoiron/sqlx"
	"github.com/pkg/errors"
)

const defaultOwnerID = "local"

// PersistedState is the durable registry state restored when the control plane starts.
type PersistedState struct {
	Runners    []Runner
	Runs       []Run
	Affinities map[string]string
}

// Persistence stores durable runner identity, run state, and conversation affinity.
type Persistence interface {
	Load(ctx context.Context) (PersistedState, error)
	SaveRunner(ctx context.Context, runner Runner) error
	SaveRun(ctx context.Context, run Run) error
	BindConversation(ctx context.Context, conversationID, runnerID string, now time.Time) error
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
		Affinities: make(map[string]string),
	}
	for _, row := range runnerRows {
		state.Runners = append(state.Runners, row.runner())
	}

	if err := s.db.SelectContext(ctx, &state.Runs, `
		SELECT rr.id, rr.conversation_id, rr.runner_id, rr.status,
			COALESCE(rr.manifest_digest, '') AS manifest_digest,
			COALESCE(rr.manifest_json, '') AS manifest_json,
			COALESCE(rr.error, '') AS error,
			rr.created_at, rr.updated_at
		FROM runner_runs rr
		JOIN runner_registrations r ON r.id = rr.runner_id
		WHERE r.owner_id = ?
		ORDER BY rr.created_at, rr.id
	`, s.ownerID); err != nil {
		return PersistedState{}, errors.Wrap(err, "failed to load runner runs")
	}

	var affinities []conversationAffinityRow
	if err := s.db.SelectContext(ctx, &affinities, `
		SELECT a.conversation_id, a.runner_id
		FROM conversation_runner_affinity a
		JOIN runner_registrations r ON r.id = a.runner_id
		WHERE r.owner_id = ?
	`, s.ownerID); err != nil {
		return PersistedState{}, errors.Wrap(err, "failed to load runner conversation affinity")
	}
	for _, affinity := range affinities {
		state.Affinities[affinity.ConversationID] = affinity.RunnerID
	}
	return state, nil
}

// SaveRunner inserts or updates one stable runner registration.
func (s *SQLitePersistence) SaveRunner(ctx context.Context, runner Runner) error {
	if s == nil || s.db == nil {
		return errors.New("runner persistence is closed")
	}
	_, err := s.db.ExecContext(ctx, `
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
	_, err := s.db.ExecContext(ctx, `
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

// BindConversation durably establishes affinity without silently moving an existing conversation.
func (s *SQLitePersistence) BindConversation(ctx context.Context, conversationID, runnerID string, now time.Time) error {
	if s == nil || s.db == nil {
		return errors.New("runner persistence is closed")
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO conversation_runner_affinity (conversation_id, runner_id, created_at, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(conversation_id) DO UPDATE SET
			updated_at = excluded.updated_at
		WHERE conversation_runner_affinity.runner_id = excluded.runner_id
	`, conversationID, runnerID, now, now)
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
	ConversationID string `db:"conversation_id"`
	RunnerID       string `db:"runner_id"`
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
