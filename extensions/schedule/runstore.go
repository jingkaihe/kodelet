package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/pkg/errors"
)

// FinishRun records a run's outcome, the agent's final output, and its stats.
func (s *Store) FinishRun(ctx context.Context, runID int64, status RunStatus, message string, result runResult, finishedAt time.Time) error {
	stats, err := json.Marshal(result.Stats)
	if err != nil {
		return errors.Wrap(err, "failed to encode run stats")
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE runs SET status = ?, error = ?, output = ?, stats = ?, finished_at = ? WHERE id = ?`,
		string(status), message, result.Output, string(stats), finishedAt.UnixMilli(), runID,
	)
	return errors.Wrap(err, "failed to record run outcome")
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

// ListRuns returns up to limit runs of workspace, newest first, optionally
// only those of one schedule. It does not load instructions or outputs.
func (s *Store) ListRuns(ctx context.Context, workspace, scheduleID string, limit int) ([]Run, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT`+runColumns+`
FROM runs r
JOIN schedules s ON s.id = r.schedule_id
WHERE s.workspace = ? AND (? = '' OR r.schedule_id = ?)
ORDER BY r.id DESC
LIMIT ?`, workspace, scheduleID, scheduleID, limit)
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
