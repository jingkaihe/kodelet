package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/pkg/errors"
)

// ErrAdoptionConflict leaves legacy history untouched. Adoption never rebinds a
// conversation, even to the same runner, or competes with an admitted turn.
var ErrAdoptionConflict = errors.New("conversation changed, is active, or is already bound; preview again or start a new conversation")

// AdoptConversation binds a validated legacy snapshot to the exact runner
// generation the user confirmed. It is intentionally separate from first-turn
// reservations and the idempotent BindConversation API.
func (r *Registry) AdoptConversation(ctx context.Context, record convtypes.ConversationRecord, runnerID string, generation int64, profile string) error {
	if strings.TrimSpace(record.ID) == "" || strings.TrimSpace(record.CWD) == "" {
		return errors.New("adoption requires an existing conversation with a validated directory")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, _, _, err := r.runnerCallTargetLocked(runnerID, generation, protocol.MethodWorkspaceDiscover); err != nil {
		return err
	}
	if _, exists := r.affinities.get(record.ID); exists || r.affinities.activeRun(record.ID) != "" {
		return ErrAdoptionConflict
	}
	store, ok := r.persistence.(interface {
		adoptConversation(context.Context, convtypes.ConversationRecord, string, string, time.Time) error
	})
	if !ok {
		return errors.New("transactional conversation adoption is unavailable")
	}
	profile = normalizeEnvironmentProfile(profile)
	if err := store.adoptConversation(ctx, record, runnerID, profile, r.now().UTC()); err != nil {
		return err
	}
	r.affinities.put(record.ID, ConversationAffinity{RunnerID: runnerID, EnvironmentProfile: profile}, true)
	return nil
}

func (s *SQLitePersistence) adoptConversation(ctx context.Context, record convtypes.ConversationRecord, runnerID, profile string, now time.Time) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "failed to begin conversation adoption")
	}
	defer func() { _ = tx.Rollback() }()
	var current struct {
		CWD       string    `db:"cwd"`
		Metadata  string    `db:"metadata"`
		UpdatedAt time.Time `db:"updated_at"`
	}
	if err := tx.GetContext(ctx, &current, `SELECT COALESCE(cwd, '') AS cwd, COALESCE(metadata, '{}') AS metadata, updated_at FROM conversations WHERE id = ?`, record.ID); err != nil {
		return errors.Wrap(err, "failed to read conversation for adoption")
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(current.Metadata), &metadata); err != nil {
		return errors.Wrap(err, "invalid stored conversation metadata")
	}
	expected, err := json.Marshal(record.Metadata)
	if err != nil {
		return errors.Wrap(err, "invalid adoption snapshot metadata")
	}
	actual, err := json.Marshal(metadata)
	if err != nil {
		return errors.Wrap(err, "invalid stored adoption metadata")
	}
	metadataEqual := bytes.Equal(actual, expected) || (len(metadata) == 0 && len(record.Metadata) == 0)
	if current.CWD != record.CWD || !current.UpdatedAt.Equal(record.UpdatedAt) || !metadataEqual {
		return ErrAdoptionConflict
	}
	if value, exists := metadata[convtypes.RunnerIDMetadataKey]; exists && value != "" {
		return ErrAdoptionConflict
	}
	// Receipt admission and this insertion share SQLite's write serialization.
	// A turn admitted before the insertion blocks adoption; one admitted after
	// commit observes the new binding. Never edit receipt or history contents.
	result, err := tx.ExecContext(ctx, `INSERT INTO conversation_runner_affinity
		(conversation_id, runner_id, environment_profile, created_at, updated_at)
		SELECT ?, ?, ?, ?, ? WHERE NOT EXISTS (
			SELECT 1 FROM chat_turns WHERE conversation_id = ? AND status IN ('accepted', 'running')
		) AND NOT EXISTS (
			SELECT 1 FROM runner_runs WHERE conversation_id = ? AND status IN ('opening', 'running')
		) AND EXISTS (SELECT 1 FROM runner_registrations WHERE id = ? AND owner_id = ?)
		ON CONFLICT(conversation_id) DO NOTHING`, record.ID, runnerID, profile, now, now, record.ID, record.ID, runnerID, s.ownerID)
	if err != nil {
		return errors.Wrap(err, "failed to bind legacy conversation")
	}
	count, err := result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "failed to check adoption result")
	}
	if count != 1 {
		return ErrAdoptionConflict
	}
	// Add only environment provenance. Retain other metadata values and leave
	// snapshots, usage, transcript, summary fields and timestamps unchanged.
	for _, table := range []string{"conversations", "conversation_summaries"} {
		_, err := tx.ExecContext(ctx, `UPDATE `+table+` SET metadata = json_set(
			CASE WHEN metadata IS NULL OR metadata = 'null' THEN '{}' ELSE metadata END,
			'$.runner_id', ?, '$.environment_profile', ?) WHERE id = ?`, runnerID, profile, record.ID)
		if err != nil {
			return errors.Wrap(err, "failed to attach adopted environment metadata")
		}
	}
	return errors.Wrap(tx.Commit(), "failed to commit conversation adoption")
}
