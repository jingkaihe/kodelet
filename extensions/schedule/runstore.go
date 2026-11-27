package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/pkg/errors"
)

// FinishRun records a running run's outcome, the agent's final output, and
// its stats. It leaves a run that has already finished unchanged.
func (s *Store) FinishRun(ctx context.Context, runID int64, status RunStatus, message string, result runResult, finishedAt time.Time) error {
	stats, err := json.Marshal(result.Stats)
	if err != nil {
		return errors.Wrap(err, "failed to encode run stats")
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE runs SET status = ?, error = ?, output = ?, stats = ?, finished_at = ? WHERE id = ? AND status = ?`,
		string(status), message, result.Output, string(stats), finishedAt.UnixMilli(), runID, string(RunRunning),
	)
	return errors.Wrap(err, "failed to record run outcome")
}

// SaveRunProgress records what a running run has done so far, for a run
// whose outcome is not known yet.
func (s *Store) SaveRunProgress(ctx context.Context, runID int64, result runResult) error {
	stats, err := json.Marshal(result.Stats)
	if err != nil {
		return errors.Wrap(err, "failed to encode run stats")
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE runs SET output = ?, stats = ? WHERE id = ? AND status = ?`,
		result.Output, string(stats), runID, string(RunRunning),
	)
	return errors.Wrap(err, "failed to record run progress")
}

// SettleRun records the outcome of a running run learned from its
// conversation. It keeps the stats SaveRunProgress recorded, and the output
// too when output is empty. It reports false for a run that has already
// finished, which it leaves unchanged.
func (s *Store) SettleRun(ctx context.Context, runID int64, status RunStatus, message, output string, finishedAt time.Time) (bool, error) {
	result, err := s.db.ExecContext(ctx,
		`UPDATE runs SET status = ?, error = ?, output = CASE WHEN ? = '' THEN output ELSE ? END, finished_at = ? WHERE id = ? AND status = ?`,
		string(status), message, output, output, finishedAt.UnixMilli(), runID, string(RunRunning),
	)
	if err != nil {
		return false, errors.Wrap(err, "failed to record run outcome")
	}
	affected, err := result.RowsAffected()
	return affected > 0, errors.Wrap(err, "failed to record run outcome")
}

// runningRun is a run whose outcome is not recorded yet.
type runningRun struct {
	ID             int64
	ScheduleID     string
	ConversationID string
	StartedAt      time.Time
}

// RunningRuns returns every run that has not finished, oldest first,
// including runs a previous daemon left behind.
func (s *Store) RunningRuns(ctx context.Context) ([]runningRun, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, schedule_id, conversation_id, started_at FROM runs WHERE status = ? ORDER BY id`, string(RunRunning))
	if err != nil {
		return nil, errors.Wrap(err, "failed to query running runs")
	}
	defer rows.Close()
	var runs []runningRun
	for rows.Next() {
		var run runningRun
		var startedAt int64
		if err := rows.Scan(&run.ID, &run.ScheduleID, &run.ConversationID, &startedAt); err != nil {
			return nil, errors.Wrap(err, "failed to read running run")
		}
		run.StartedAt = time.UnixMilli(startedAt)
		runs = append(runs, run)
	}
	return runs, errors.Wrap(rows.Err(), "failed to query running runs")
}

// runColumns are the run fields ListRuns loads; GetRun adds runDetailColumns.
const (
	runColumns = `
	r.id, r.schedule_id, s.name, r.scheduled_for, r.started_at, r.finished_at,
	r.status, r.conversation_id, r.error, r.stats`
	runDetailColumns = `, r.instruction, r.cwd, r.profile, r.output`
)

func scanRun(row rowScanner, detail bool) (Run, error) {
	var (
		run                     Run
		scheduledFor, startedAt int64
		finishedAt              sql.NullInt64
		status, stats           string
	)
	dest := []any{
		&run.ID, &run.ScheduleID, &run.ScheduleName, &scheduledFor, &startedAt, &finishedAt,
		&status, &run.ConversationID, &run.Error, &stats,
	}
	if detail {
		dest = append(dest, &run.Instruction, &run.CWD, &run.Profile, &run.Output)
	}
	if err := row.Scan(dest...); err != nil {
		return Run{}, err
	}
	run.ScheduledFor = time.UnixMilli(scheduledFor)
	run.StartedAt = time.UnixMilli(startedAt)
	run.FinishedAt = timePtr(finishedAt)
	run.Status = RunStatus(status)
	if stats != "" {
		// Stats are informational; a row that cannot be decoded keeps the rest.
		var decoded RunStats
		if json.Unmarshal([]byte(stats), &decoded) == nil {
			run.Stats = &decoded
		}
	}
	return run, nil
}

// RunFilter selects runs for ListRuns; empty fields match every run.
type RunFilter struct {
	ScheduleID string
	Status     RunStatus
	Limit      int
}

// ListRuns returns up to filter.Limit matching runs of workspace, newest
// first. It does not load instructions or outputs.
func (s *Store) ListRuns(ctx context.Context, workspace string, filter RunFilter) ([]Run, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT`+runColumns+`
FROM runs r
JOIN schedules s ON s.id = r.schedule_id
WHERE s.workspace = ?
	AND (? = '' OR r.schedule_id = ?)
	AND (? = '' OR r.status = ?)
ORDER BY r.id DESC
LIMIT ?`,
		workspace,
		filter.ScheduleID, filter.ScheduleID,
		string(filter.Status), string(filter.Status),
		filter.Limit,
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to query runs")
	}
	defer rows.Close()
	runs := []Run{}
	for rows.Next() {
		run, err := scanRun(rows, false)
		if err != nil {
			return nil, errors.Wrap(err, "failed to read run")
		}
		runs = append(runs, run)
	}
	return runs, errors.Wrap(rows.Err(), "failed to query runs")
}

// GetRun returns one run of workspace with everything recorded about it.
func (s *Store) GetRun(ctx context.Context, workspace string, runID int64) (Run, error) {
	row := s.db.QueryRowContext(ctx, `SELECT`+runColumns+runDetailColumns+`
FROM runs r
JOIN schedules s ON s.id = r.schedule_id
WHERE s.workspace = ? AND r.id = ?`, workspace, runID)
	run, err := scanRun(row, true)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, errRunNotFound
	}
	if err != nil {
		return Run{}, errors.Wrap(err, "failed to read run")
	}
	return run, nil
}
