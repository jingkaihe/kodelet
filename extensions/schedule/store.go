package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"

	// sqlite driver for database/sql registration
	_ "modernc.org/sqlite"
)

// runHistoryLimit bounds the runs kept per schedule.
const runHistoryLimit = 20

var (
	errNotFound  = errors.New("schedule not found")
	errNameTaken = errors.New("a schedule with this name already exists")
)

// RunStatus is the outcome of one scheduled run.
type RunStatus string

// Run statuses.
const (
	RunRunning   RunStatus = "running"
	RunSucceeded RunStatus = "succeeded"
	RunFailed    RunStatus = "failed"
	RunCanceled  RunStatus = "canceled"
	RunSkipped   RunStatus = "skipped"
)

// Schedule is a recurring agent instruction bound to one workspace runner.
type Schedule struct {
	ID          string     `json:"id"`
	Workspace   string     `json:"workspace"`
	Name        string     `json:"name"`
	Schedule    string     `json:"schedule"`
	Instruction string     `json:"instruction"`
	CWD         string     `json:"cwd"`
	Profile     string     `json:"profile,omitempty"`
	Enabled     bool       `json:"enabled"`
	NextRunAt   *time.Time `json:"nextRunAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	LastRun     *Run       `json:"lastRun,omitempty"`
}

// Run records one claimed occurrence of a schedule.
type Run struct {
	ID             int64      `json:"id"`
	ScheduledFor   time.Time  `json:"scheduledFor"`
	StartedAt      time.Time  `json:"startedAt"`
	FinishedAt     *time.Time `json:"finishedAt,omitempty"`
	Status         RunStatus  `json:"status"`
	ConversationID string     `json:"conversationId,omitempty"`
	Error          string     `json:"error,omitempty"`
}

// DaemonStatus is the daemon's latest heartbeat.
type DaemonStatus struct {
	PID          int
	Hostname     string
	Version      string
	StartedAt    time.Time
	HeartbeatAt  time.Time
	PollInterval time.Duration
	StoppedAt    *time.Time
}

// Store persists schedules, run history, and daemon heartbeats in SQLite. The
// extension and the daemon open the same file from separate processes.
type Store struct {
	db *sql.DB
}

// openStore opens or creates the database at path and applies pending
// migrations.
func openStore(ctx context.Context, path string) (*Store, error) {
	db, err := openDB(ctx, path)
	if err != nil {
		return nil, err
	}
	if err := migrate(ctx, db, scheduleMigrations()); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// openDB opens the SQLite database at path without migrating it.
func openDB(ctx context.Context, path string) (*sql.DB, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, errors.Wrap(err, "failed to resolve schedule database path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, errors.Wrap(err, "failed to create schedule database directory")
	}
	dsn := url.URL{
		Scheme: "file",
		Path:   path,
		RawQuery: url.Values{
			"_pragma": {"busy_timeout=5000", "foreign_keys=ON", "journal_mode=WAL", "synchronous=NORMAL"},
			// Transactions take the write lock up front, so processes that
			// migrate or claim runs at once wait instead of failing.
			"_txlock": {"immediate"},
		}.Encode(),
	}
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, errors.Wrap(err, "failed to open schedule database")
	}
	// One connection per process: writes are rare, and other processes wait
	// in SQLite's busy handler.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, errors.Wrap(err, "failed to open schedule database")
	}
	return db, nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

const selectSchedules = `
SELECT
	s.id, s.workspace, s.name, s.schedule, s.instruction, s.cwd, s.profile, s.enabled,
	s.next_run_at, s.created_at, s.updated_at,
	r.id, r.scheduled_for, r.started_at, r.finished_at, r.status, r.conversation_id, r.error
FROM schedules s
LEFT JOIN runs r ON r.id = (SELECT MAX(id) FROM runs WHERE schedule_id = s.id)
`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSchedule(row rowScanner) (Schedule, error) {
	var (
		schedule                                       Schedule
		enabled                                        bool
		nextRunAt                                      sql.NullInt64
		createdAt, updatedAt                           int64
		runID, scheduledFor, runStartedAt, runFinished sql.NullInt64
		runStatus, conversationID, runError            sql.NullString
	)
	err := row.Scan(
		&schedule.ID, &schedule.Workspace, &schedule.Name, &schedule.Schedule, &schedule.Instruction,
		&schedule.CWD, &schedule.Profile, &enabled,
		&nextRunAt, &createdAt, &updatedAt,
		&runID, &scheduledFor, &runStartedAt, &runFinished, &runStatus, &conversationID, &runError,
	)
	if err != nil {
		return Schedule{}, err
	}
	schedule.Enabled = enabled
	schedule.NextRunAt = timePtr(nextRunAt)
	schedule.CreatedAt = time.UnixMilli(createdAt)
	schedule.UpdatedAt = time.UnixMilli(updatedAt)
	if runID.Valid {
		schedule.LastRun = &Run{
			ID:             runID.Int64,
			ScheduledFor:   time.UnixMilli(scheduledFor.Int64),
			StartedAt:      time.UnixMilli(runStartedAt.Int64),
			FinishedAt:     timePtr(runFinished),
			Status:         RunStatus(runStatus.String),
			ConversationID: conversationID.String,
			Error:          runError.String,
		}
	}
	return schedule, nil
}

func (s *Store) query(ctx context.Context, where string, args ...any) ([]Schedule, error) {
	rows, err := s.db.QueryContext(ctx, selectSchedules+where, args...)
	if err != nil {
		return nil, errors.Wrap(err, "failed to query schedules")
	}
	defer rows.Close()
	var schedules []Schedule
	for rows.Next() {
		schedule, err := scanSchedule(rows)
		if err != nil {
			return nil, errors.Wrap(err, "failed to read schedule")
		}
		schedules = append(schedules, schedule)
	}
	return schedules, errors.Wrap(rows.Err(), "failed to query schedules")
}

// List returns the schedules of workspace, or of every workspace when it is
// empty, with their latest runs.
func (s *Store) List(ctx context.Context, workspace string) ([]Schedule, error) {
	return s.query(ctx, `WHERE ? = '' OR s.workspace = ? ORDER BY s.workspace, s.name`, workspace, workspace)
}

// Get returns one schedule of workspace.
func (s *Store) Get(ctx context.Context, workspace, id string) (Schedule, error) {
	schedules, err := s.query(ctx, `WHERE s.workspace = ? AND s.id = ?`, workspace, id)
	if err != nil {
		return Schedule{}, err
	}
	if len(schedules) == 0 {
		return Schedule{}, errNotFound
	}
	return schedules[0], nil
}

// Create assigns an ID and timestamps and inserts schedule.
func (s *Store) Create(ctx context.Context, schedule *Schedule, now time.Time) error {
	id, err := newID()
	if err != nil {
		return err
	}
	schedule.ID = id
	schedule.CreatedAt = now
	schedule.UpdatedAt = now
	_, err = s.db.ExecContext(ctx, `
INSERT INTO schedules (id, workspace, name, schedule, instruction, cwd, profile, enabled, next_run_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		schedule.ID, schedule.Workspace, schedule.Name, schedule.Schedule, schedule.Instruction,
		schedule.CWD, schedule.Profile, schedule.Enabled, millisPtr(schedule.NextRunAt),
		now.UnixMilli(), now.UnixMilli(),
	)
	return constraintError(err, "failed to create schedule")
}

// Update saves the editable fields of schedule, matched by ID and workspace.
// next_run_at changes only with resetNext, so an edit cannot undo a run the
// daemon claimed while the edit was being confirmed.
func (s *Store) Update(ctx context.Context, schedule *Schedule, resetNext bool, now time.Time) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE schedules SET
	name = ?, schedule = ?, instruction = ?, cwd = ?, profile = ?, enabled = ?,
	next_run_at = CASE WHEN ? THEN ? ELSE next_run_at END,
	updated_at = ?
WHERE id = ? AND workspace = ?`,
		schedule.Name, schedule.Schedule, schedule.Instruction, schedule.CWD, schedule.Profile, schedule.Enabled,
		resetNext, millisPtr(schedule.NextRunAt),
		now.UnixMilli(),
		schedule.ID, schedule.Workspace,
	)
	if err != nil {
		return constraintError(err, "failed to update schedule")
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return errNotFound
	}
	schedule.UpdatedAt = now
	return nil
}

// Delete removes a schedule of workspace and its run history.
func (s *Store) Delete(ctx context.Context, workspace, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM schedules WHERE id = ? AND workspace = ?`, id, workspace)
	if err != nil {
		return errors.Wrap(err, "failed to delete schedule")
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return errNotFound
	}
	return nil
}

// Due returns enabled schedules across all workspaces whose next run is at or
// before now, oldest first.
func (s *Store) Due(ctx context.Context, now time.Time) ([]Schedule, error) {
	return s.query(ctx, `WHERE s.enabled = 1 AND s.next_run_at IS NOT NULL AND s.next_run_at <= ? ORDER BY s.next_run_at, s.id`, now.UnixMilli())
}

// Claim moves a schedule's next run from due to next (none when next is
// zero) and records run, returning the run ID. It reports false without
// changes when the schedule was edited, disabled, deleted, or claimed since it
// was read.
func (s *Store) Claim(ctx context.Context, scheduleID string, due, next time.Time, run Run) (int64, bool, error) {
	var nextRunAt *time.Time
	if !next.IsZero() {
		nextRunAt = &next
	}
	return s.claim(ctx, scheduleID, due, nextRunAt, true, run)
}

// Disable claims a due schedule like Claim but also disables it, for
// schedules that can no longer run.
func (s *Store) Disable(ctx context.Context, scheduleID string, due time.Time, run Run) (int64, bool, error) {
	return s.claim(ctx, scheduleID, due, nil, false, run)
}

func (s *Store) claim(ctx context.Context, scheduleID string, due time.Time, next *time.Time, enabled bool, run Run) (int64, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, errors.Wrap(err, "failed to claim schedule")
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx,
		`UPDATE schedules SET next_run_at = ?, enabled = ? WHERE id = ? AND enabled = 1 AND next_run_at = ?`,
		millisPtr(next), enabled, scheduleID, due.UnixMilli(),
	)
	if err != nil {
		return 0, false, errors.Wrap(err, "failed to claim schedule")
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, false, errors.Wrap(err, "failed to claim schedule")
	}
	if affected == 0 {
		return 0, false, nil
	}
	inserted, err := tx.ExecContext(ctx, `
INSERT INTO runs (schedule_id, scheduled_for, started_at, finished_at, status, conversation_id, error)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		scheduleID, due.UnixMilli(), run.StartedAt.UnixMilli(), millisPtr(run.FinishedAt),
		string(run.Status), run.ConversationID, run.Error,
	)
	if err != nil {
		return 0, false, errors.Wrap(err, "failed to record run")
	}
	runID, err := inserted.LastInsertId()
	if err != nil {
		return 0, false, errors.Wrap(err, "failed to record run")
	}
	if _, err := tx.ExecContext(ctx, `
DELETE FROM runs WHERE schedule_id = ? AND id NOT IN (
	SELECT id FROM runs WHERE schedule_id = ? ORDER BY id DESC LIMIT ?
)`, scheduleID, scheduleID, runHistoryLimit); err != nil {
		return 0, false, errors.Wrap(err, "failed to prune run history")
	}
	if err := tx.Commit(); err != nil {
		return 0, false, errors.Wrap(err, "failed to claim schedule")
	}
	return runID, true, nil
}

// SetRunConversation records the conversation a run created.
func (s *Store) SetRunConversation(ctx context.Context, runID int64, conversationID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE runs SET conversation_id = ? WHERE id = ?`, conversationID, runID)
	return errors.Wrap(err, "failed to record run conversation")
}

// FinishRun records a run's outcome.
func (s *Store) FinishRun(ctx context.Context, runID int64, status RunStatus, message string, finishedAt time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE runs SET status = ?, error = ?, finished_at = ? WHERE id = ?`,
		string(status), message, finishedAt.UnixMilli(), runID,
	)
	return errors.Wrap(err, "failed to record run outcome")
}

// AbandonRunningRuns marks runs left running by a previous daemon as failed.
func (s *Store) AbandonRunningRuns(ctx context.Context, message string, now time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`UPDATE runs SET status = ?, error = ?, finished_at = ? WHERE status = ?`,
		string(RunFailed), message, now.UnixMilli(), string(RunRunning),
	)
	if err != nil {
		return 0, errors.Wrap(err, "failed to update interrupted runs")
	}
	return result.RowsAffected()
}

// SaveHeartbeat records that the daemon is alive.
func (s *Store) SaveHeartbeat(ctx context.Context, status DaemonStatus) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO daemon (id, pid, hostname, version, started_at, heartbeat_at, poll_interval_ms, stopped_at)
VALUES (1, ?, ?, ?, ?, ?, ?, NULL)
ON CONFLICT (id) DO UPDATE SET
	pid = excluded.pid, hostname = excluded.hostname, version = excluded.version,
	started_at = excluded.started_at, heartbeat_at = excluded.heartbeat_at,
	poll_interval_ms = excluded.poll_interval_ms, stopped_at = NULL`,
		status.PID, status.Hostname, status.Version, status.StartedAt.UnixMilli(),
		status.HeartbeatAt.UnixMilli(), status.PollInterval.Milliseconds(),
	)
	return errors.Wrap(err, "failed to record daemon heartbeat")
}

// MarkDaemonStopped records a clean daemon shutdown.
func (s *Store) MarkDaemonStopped(ctx context.Context, pid int, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE daemon SET stopped_at = ? WHERE id = 1 AND pid = ?`, now.UnixMilli(), pid)
	return errors.Wrap(err, "failed to record daemon shutdown")
}

// Daemon returns the latest heartbeat; found is false if no daemon has run.
func (s *Store) Daemon(ctx context.Context) (status DaemonStatus, found bool, err error) {
	var startedAt, heartbeatAt, pollInterval int64
	var stoppedAt sql.NullInt64
	err = s.db.QueryRowContext(ctx, `
SELECT pid, hostname, version, started_at, heartbeat_at, poll_interval_ms, stopped_at FROM daemon WHERE id = 1`,
	).Scan(&status.PID, &status.Hostname, &status.Version, &startedAt, &heartbeatAt, &pollInterval, &stoppedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return DaemonStatus{}, false, nil
	}
	if err != nil {
		return DaemonStatus{}, false, errors.Wrap(err, "failed to read daemon status")
	}
	status.StartedAt = time.UnixMilli(startedAt)
	status.HeartbeatAt = time.UnixMilli(heartbeatAt)
	status.PollInterval = time.Duration(pollInterval) * time.Millisecond
	status.StoppedAt = timePtr(stoppedAt)
	return status, true, nil
}

func newID() (string, error) {
	var buf [6]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", errors.Wrap(err, "failed to generate schedule ID")
	}
	return hex.EncodeToString(buf[:]), nil
}

func constraintError(err error, message string) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return errNameTaken
	}
	return errors.Wrap(err, message)
}

func millisPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UnixMilli()
}

func timePtr(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	t := time.UnixMilli(value.Int64)
	return &t
}
