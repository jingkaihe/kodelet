package artifacts

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"time"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/jmoiron/sqlx"
	"github.com/pkg/errors"
)

// SaveReferences adds references from an authoritative conversation snapshot in its save transaction.
// It deliberately retains earlier uploads not yet present in a checkpoint. This also handles live forks.
func SaveReferences(ctx context.Context, tx *sqlx.Tx, conversationID string, results map[string]tooltypes.StructuredToolResult) error {
	for callID, result := range results {
		for _, attachment := range result.Attachments {
			if attachment.ArtifactID == "" || attachment.Error != "" {
				continue
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO conversation_artifacts (conversation_id, tool_call_id, artifact_id)
				VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, conversationID, callID, attachment.ArtifactID)
			if err != nil {
				return errors.Wrap(err, "failed to save conversation artifact reference")
			}
		}
	}
	return nil
}

// DeleteReferences removes a conversation's references and metadata for images with no remaining references.
// Returned IDs must be unlinked only after the caller commits its transaction.
func DeleteReferences(ctx context.Context, tx *sqlx.Tx, conversationID string) ([]string, error) {
	var candidates []string
	if err := tx.SelectContext(ctx, &candidates,
		`SELECT DISTINCT artifact_id FROM conversation_artifacts WHERE conversation_id = ?`,
		conversationID,
	); err != nil {
		return nil, errors.Wrap(err, "failed to load conversation artifact references")
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM conversation_artifacts WHERE conversation_id = ?`,
		conversationID,
	); err != nil {
		return nil, errors.Wrap(err, "failed to remove conversation artifact references")
	}
	var removed []string
	for _, id := range candidates {
		result, err := tx.ExecContext(ctx, `DELETE FROM image_artifacts WHERE id = ?
			AND NOT EXISTS (SELECT 1 FROM conversation_artifacts WHERE artifact_id = image_artifacts.id)`, id)
		if err != nil {
			return nil, errors.Wrap(err, "failed to remove unreferenced image metadata")
		}
		count, err := result.RowsAffected()
		if err != nil {
			return nil, errors.Wrap(err, "failed to inspect image metadata deletion")
		}
		if count > 0 {
			removed = append(removed, id)
		}
	}
	return removed, nil
}

// RemoveFiles unlinks immutable files after their metadata deletion has committed.
// A failure is recoverable by the next startup sweep and cannot roll back the committed deletion.
func RemoveFiles(dbPath string, artifactIDs []string) error {
	for _, id := range artifactIDs {
		if id == "" || id == "." || id == ".." || filepath.Base(id) != id {
			return errors.New("invalid artifact ID for deletion")
		}
		if err := os.Remove(filepath.Join(filepath.Dir(dbPath), "artifacts", id)); err != nil && !os.IsNotExist(err) {
			return errors.Wrap(err, "failed to remove unreferenced image file")
		}
	}
	return nil
}

func (s *Store) sweep(ctx context.Context) error {
	cutoff := time.Now().UTC().Add(-cleanupGrace)
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "failed to begin artifact cleanup")
	}
	defer tx.Rollback()
	// Uploads can precede checkpoints. Only release old references absent from
	// persisted results when no run is still executing for that conversation.
	_, err = tx.ExecContext(ctx, `DELETE FROM conversation_artifacts AS r
		WHERE EXISTS (SELECT 1 FROM image_artifacts a WHERE a.id = r.artifact_id AND a.created_at < ?)
		AND NOT EXISTS (SELECT 1 FROM runner_runs rr WHERE rr.conversation_id = r.conversation_id AND rr.status IN ('opening', 'running'))
		AND NOT EXISTS (
			SELECT 1 FROM conversations c, json_each(c.tool_results) t, json_each(t.value, '$.attachments') a
			WHERE c.id = r.conversation_id AND t.key = r.tool_call_id AND json_extract(a.value, '$.artifactId') = r.artifact_id
		)`, cutoff)
	if err != nil {
		return errors.Wrap(err, "failed to release abandoned image uploads")
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM image_artifacts WHERE created_at < ?
		AND NOT EXISTS (SELECT 1 FROM conversation_artifacts WHERE artifact_id = image_artifacts.id)`, cutoff)
	if err != nil {
		return errors.Wrap(err, "failed to delete unreferenced image metadata")
	}
	if err := tx.Commit(); err != nil {
		return errors.Wrap(err, "failed to commit artifact cleanup")
	}
	directory, err := os.Open(s.dir)
	if err != nil {
		return errors.Wrap(err, "failed to open artifact directory for cleanup")
	}
	defer directory.Close()
	for {
		entries, err := directory.ReadDir(128)
		if err != nil && !errors.Is(err, io.EOF) {
			return errors.Wrap(err, "failed to read artifact directory")
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			info, err := entry.Info()
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return errors.Wrap(err, "failed to inspect artifact file")
			}
			if !info.Mode().IsRegular() || !info.ModTime().Before(cutoff) {
				continue
			}
			var exists bool
			if err := s.db.GetContext(ctx, &exists,
				`SELECT EXISTS (SELECT 1 FROM image_artifacts WHERE id = ?)`,
				entry.Name(),
			); err != nil {
				return errors.Wrap(err, "failed to check artifact file ownership")
			}
			if !exists {
				if err := os.Remove(filepath.Join(s.dir, entry.Name())); err != nil && !os.IsNotExist(err) {
					return errors.Wrap(err, "failed to clean up orphan image file")
				}
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
	}
}
