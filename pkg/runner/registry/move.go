package registry

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/pkg/errors"
)

// ErrMoveConflict leaves the saved conversation and its assignment untouched.
var ErrMoveConflict = errors.New("conversation changed or is active; review the move again after any active turn finishes")

// MoveConversation atomically reassigns a saved conversation, without contacting
// either runner. The record and source affinity must still match the move plan.
// Ordinary BindConversation calls continue to reject implicit reassignment.
func (r *Registry) MoveConversation(ctx context.Context, record convtypes.ConversationRecord, source ConversationAffinity, runnerID, cwd string) error {
	if strings.TrimSpace(record.ID) == "" || strings.TrimSpace(runnerID) == "" {
		return errors.New("move requires a conversation ID and destination runner ID")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.runners[runnerID] == nil {
		return ErrRunnerNotFound
	}
	current, exists := r.affinities.get(record.ID)
	if current.ConversationAffinity != source || (exists && !current.persisted) || r.affinities.activeRun(record.ID) != "" {
		return ErrMoveConflict
	}
	store, ok := r.persistence.(interface {
		moveConversation(context.Context, convtypes.ConversationRecord, ConversationAffinity, string, string, string, time.Time) error
	})
	if !ok {
		return errors.New("transactional conversation move is unavailable")
	}
	profile, _ := record.Metadata[convtypes.RunnerEnvironmentProfileMetadataKey].(string)
	if source.RunnerID != "" {
		profile = source.EnvironmentProfile
	}
	profile = normalizeEnvironmentProfile(profile)
	if err := store.moveConversation(ctx, record, source, runnerID, cwd, profile, r.now().UTC()); err != nil {
		return err
	}
	r.affinities.put(record.ID, ConversationAffinity{RunnerID: runnerID, EnvironmentProfile: profile}, true)
	return nil
}

func (s *SQLitePersistence) moveConversation(ctx context.Context, record convtypes.ConversationRecord, source ConversationAffinity, runnerID, cwd, profile string, now time.Time) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "failed to begin conversation move")
	}
	defer func() { _ = tx.Rollback() }()
	var current struct {
		CWD       string    `db:"cwd"`
		Metadata  string    `db:"metadata"`
		UpdatedAt time.Time `db:"updated_at"`
	}
	if err := tx.GetContext(ctx, &current, `SELECT COALESCE(cwd, '') AS cwd, COALESCE(metadata, '{}') AS metadata, updated_at FROM conversations WHERE id = ?`, record.ID); err != nil {
		return errors.Wrap(err, "failed to read conversation for move")
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(current.Metadata), &metadata); err != nil {
		return errors.Wrap(err, "invalid stored conversation metadata")
	}
	var affinity conversationAffinityRow
	err = tx.GetContext(ctx, &affinity, `SELECT runner_id, environment_profile FROM conversation_runner_affinity WHERE conversation_id = ?`, record.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return errors.Wrap(err, "failed to read move source affinity")
	}
	if affinity.RunnerID != source.RunnerID || normalizeEnvironmentProfile(affinity.EnvironmentProfile) != source.EnvironmentProfile {
		return ErrMoveConflict
	}
	// Match Store.Load's authoritative affinity overlay when comparing the
	// snapshot; older raw metadata can lag a separately persisted binding.
	if affinity.RunnerID != "" {
		if metadata == nil {
			metadata = make(map[string]any)
		}
		metadata[convtypes.RunnerIDMetadataKey] = affinity.RunnerID
		metadata[convtypes.RunnerEnvironmentProfileMetadataKey] = affinity.EnvironmentProfile
	}
	expected, err := json.Marshal(record.Metadata)
	if err != nil {
		return errors.Wrap(err, "invalid move snapshot metadata")
	}
	actual, err := json.Marshal(metadata)
	if err != nil {
		return errors.Wrap(err, "invalid stored move metadata")
	}
	metadataEqual := bytes.Equal(actual, expected) || (len(metadata) == 0 && len(record.Metadata) == 0)
	if current.CWD != record.CWD || !current.UpdatedAt.Equal(record.UpdatedAt) || !metadataEqual {
		return ErrMoveConflict
	}
	// Receipt admission and this upsert share SQLite's write serialization.
	// A turn admitted before the upsert blocks the move; one admitted after
	// commit observes the new binding. Never edit receipt or history contents.
	result, err := tx.ExecContext(ctx, `INSERT INTO conversation_runner_affinity
		(conversation_id, runner_id, environment_profile, created_at, updated_at)
		SELECT ?, ?, ?, ?, ? WHERE NOT EXISTS (
			SELECT 1 FROM chat_turns WHERE conversation_id = ? AND status IN ('accepted', 'running')
		) AND NOT EXISTS (
			SELECT 1 FROM runner_runs WHERE conversation_id = ? AND status IN ('opening', 'running')
		) AND EXISTS (SELECT 1 FROM runner_registrations WHERE id = ? AND owner_id = ?)
		ON CONFLICT(conversation_id) DO UPDATE SET runner_id = excluded.runner_id,
			environment_profile = excluded.environment_profile, updated_at = excluded.updated_at`, record.ID, runnerID, profile, now, now, record.ID, record.ID, runnerID, s.ownerID)
	if err != nil {
		return errors.Wrap(err, "failed to move conversation binding")
	}
	count, err := result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "failed to check move result")
	}
	if count != 1 {
		return ErrMoveConflict
	}
	// Change only destination fields. Leave model snapshots, usage, transcript,
	// summary text and timestamps unchanged in both history and list metadata.
	for _, table := range []string{"conversations", "conversation_summaries"} {
		_, err := tx.ExecContext(ctx, `UPDATE `+table+` SET cwd = ?, metadata = json_set(
			CASE WHEN metadata IS NULL OR json_type(metadata) = 'null' THEN '{}' ELSE metadata END,
			'$.runner_id', ?, '$.environment_profile', ?) WHERE id = ?`, cwd, runnerID, profile, record.ID)
		if err != nil {
			return errors.Wrap(err, "failed to update moved conversation metadata")
		}
	}
	return errors.Wrap(tx.Commit(), "failed to commit conversation move")
}
