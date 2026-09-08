package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/jmoiron/sqlx"
	"github.com/pkg/errors"
)

// Child and ordinary admissions share chat_turns, so neither can overwrite or
// begin effects over the other's accepted turn, even before run.open.
func admitChild(ctx context.Context, tx *sqlx.Tx, record conversations.ConversationRecord, admission conversations.ChildAdmission) error {
	if admission.ConversationID != record.ID || admission.RunID == "" || admission.RunnerID == "" {
		return errors.New("child identity does not match its admission")
	}
	var current struct {
		UpdatedAt time.Time `db:"updated_at"`
		CWD       string    `db:"cwd"`
	}
	err := tx.GetContext(ctx, &current, `SELECT updated_at, COALESCE(cwd, '') AS cwd FROM conversations WHERE id=?`, record.ID)
	if admission.ExpectedUpdatedAt.IsZero() {
		if !errors.Is(err, sql.ErrNoRows) {
			return errors.New("child conversation already exists or cannot be checked")
		}
	} else if err != nil || !current.UpdatedAt.Equal(admission.ExpectedUpdatedAt) || current.CWD != record.CWD {
		// A metadata-only move preserves history timestamps. Do not let a
		// prepared child resume overwrite a newly selected directory.
		return errors.New("child conversation changed during resume preparation")
	}
	var busy bool
	if err := tx.GetContext(ctx, &busy, `SELECT EXISTS(SELECT 1 FROM chat_turns WHERE conversation_id=? AND status IN ('accepted','running'))
		OR EXISTS(SELECT 1 FROM runner_runs WHERE conversation_id=? AND status IN ('opening','running','closing'))`, record.ID, record.ID); err != nil {
		return err
	}
	if busy {
		return errors.New("child conversation already has an active turn")
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `INSERT INTO chat_turns (conversation_id,turn_id,run_id,request_hash,status,created_at,updated_at)
		VALUES (?,?,?,?,'running',?,?)`, record.ID, admission.RunID, admission.RunID, "delegated:"+admission.RunID, now, now); err != nil {
		return errors.Wrap(err, "failed to reserve child turn")
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO conversation_runner_affinity (conversation_id,runner_id,environment_profile,created_at,updated_at)
		VALUES (?,?,?,?,?) ON CONFLICT(conversation_id) DO UPDATE SET updated_at=excluded.updated_at
		WHERE conversation_runner_affinity.runner_id=excluded.runner_id AND conversation_runner_affinity.environment_profile=excluded.environment_profile`,
		record.ID, admission.RunnerID, admission.EnvironmentProfile, now, now)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return errors.New("child runner affinity changed")
	}
	// Only an authenticated, provenance-checked explicit child followup may
	// inherit acknowledged guidance left by a prior child run. Ordinary turns
	// cannot consume these scoped rows.
	if !admission.ExpectedUpdatedAt.IsZero() {
		_, err = tx.ExecContext(ctx, `UPDATE steering_messages SET run_id=? WHERE conversation_id=? AND run_id<>''`, admission.RunID, record.ID)
	}
	return err
}

func (s *Store) FinishChildTurn(ctx context.Context, conversationID, runID string, cancelled bool, runErr error) error {
	status, message := "succeeded", ""
	if runErr != nil {
		status, message = "failed", runErr.Error()
	}
	if cancelled {
		status = "cancelled"
	}
	_, err := s.db.ExecContext(ctx, `UPDATE chat_turns SET status=CASE WHEN cancel_requested THEN 'cancelled' ELSE ? END,
		error=?,updated_at=? WHERE conversation_id=? AND turn_id=? AND run_id=? AND status='running'`,
		status, message, time.Now().UTC(), conversationID, runID, runID)
	return errors.Wrap(err, "failed to finish child turn")
}
