package artifacts

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/jingkaihe/kodelet/pkg/logger"
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
// A failure is recoverable by the next background sweep and cannot roll back the committed deletion.
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

const cleanupBatchSize = 128

type artifactReference struct {
	ToolCallID string `db:"tool_call_id"`
	ArtifactID string `db:"artifact_id"`
}

type artifactSnapshot struct {
	exists    bool
	updatedAt time.Time
	abandoned []artifactReference
}

// Sweep releases abandoned uploads and unreferenced files older than 24 hours.
// Run one pass at a time, with a context canceled before the database is closed.
// JSON is inspected once per conversation through the reader pool; only guarded,
// bounded deletions acquire the writer connection.
func (s *Store) Sweep(ctx context.Context) error {
	cutoff := time.Now().UTC().Add(-cleanupGrace)
	var after string
	for {
		var conversations []string
		if err := s.db.Reader.SelectContext(ctx, &conversations, `
			SELECT DISTINCT r.conversation_id FROM conversation_artifacts r
			JOIN image_artifacts a ON a.id = r.artifact_id
			WHERE r.conversation_id > ? AND a.created_at < ?
			AND NOT EXISTS (SELECT 1 FROM runner_runs rr
				WHERE rr.conversation_id = r.conversation_id AND rr.status IN ('opening', 'running'))
			ORDER BY r.conversation_id LIMIT ?`, after, cutoff, cleanupBatchSize); err != nil {
			return errors.Wrap(err, "failed to list artifact cleanup candidates")
		}
		if len(conversations) == 0 {
			break
		}
		for _, id := range conversations {
			snapshot, err := s.abandonedReferences(ctx, id, cutoff)
			if err != nil {
				return err
			}
			for refs := snapshot.abandoned; len(refs) > 0; {
				n := min(len(refs), cleanupBatchSize)
				unchanged, err := s.pruneReferences(ctx, id, snapshot, refs[:n], cutoff)
				if err != nil {
					return err
				}
				if !unchanged {
					break
				}
				refs = refs[n:]
			}
		}
		after = conversations[len(conversations)-1]
	}
	if err := s.pruneMetadata(ctx, cutoff); err != nil {
		return err
	}
	return s.sweepFiles(ctx, cutoff)
}

func (s *Store) abandonedReferences(ctx context.Context, id string, cutoff time.Time) (artifactSnapshot, error) {
	var snapshot artifactSnapshot
	// Read the references and saved results from the same snapshot, then release
	// the connection before parsing potentially large tool-result JSON.
	tx, err := s.db.Reader.BeginTxx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return snapshot, errors.Wrap(err, "failed to read artifact cleanup snapshot")
	}
	defer tx.Rollback()
	var raw sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT tool_results, updated_at FROM conversations WHERE id = ?`, id).Scan(&raw, &snapshot.updatedAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return snapshot, errors.Wrap(err, "failed to read saved artifact references")
	}
	snapshot.exists = err == nil
	var refs []artifactReference
	if err := tx.SelectContext(ctx, &refs, `
		SELECT r.tool_call_id, r.artifact_id FROM conversation_artifacts r
		JOIN image_artifacts a ON a.id = r.artifact_id
		WHERE r.conversation_id = ? AND a.created_at < ?`, id, cutoff); err != nil {
		return snapshot, errors.Wrap(err, "failed to read artifact cleanup references")
	}
	if err := tx.Commit(); err != nil {
		return snapshot, errors.Wrap(err, "failed to finish artifact cleanup snapshot")
	}
	if err := ctx.Err(); err != nil {
		return snapshot, err
	}
	// Ignore tool data and metadata: only the (tool call, artifact) pair proves
	// a reference is checkpointed. Do not invoke StructuredToolResult decoding.
	var results map[string]struct {
		Attachments []struct {
			ArtifactID string `json:"artifactId"`
		} `json:"attachments"`
	}
	if raw.Valid {
		if err := json.Unmarshal([]byte(raw.String), &results); err != nil {
			logger.G(ctx).WithError(err).WithField("conversation_id", id).Warn("skipping artifact cleanup for unreadable tool results")
			return snapshot, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return snapshot, err
	}
	checkpointed := make(map[artifactReference]struct{})
	for callID, result := range results {
		for _, attachment := range result.Attachments {
			checkpointed[artifactReference{ToolCallID: callID, ArtifactID: attachment.ArtifactID}] = struct{}{}
		}
	}
	for _, ref := range refs {
		if _, ok := checkpointed[ref]; !ok {
			snapshot.abandoned = append(snapshot.abandoned, ref)
		}
	}
	return snapshot, nil
}

func (s *Store) pruneReferences(ctx context.Context, id string, snapshot artifactSnapshot, refs []artifactReference, cutoff time.Time) (bool, error) {
	tx, err := s.db.Writer.BeginTxx(ctx, nil)
	if err != nil {
		return false, errors.Wrap(err, "failed to begin artifact cleanup")
	}
	defer tx.Rollback()
	// Conversation saves update this timestamp and references atomically. A
	// missing conversation must still be missing. Never upgrade a read snapshot
	// to a writer, or delete against results that changed while being parsed.
	var updatedAt time.Time
	err = tx.GetContext(ctx, &updatedAt, `SELECT updated_at FROM conversations WHERE id = ?`, id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, errors.Wrap(err, "failed to recheck artifact cleanup snapshot")
	}
	if snapshot.exists != (err == nil) || !updatedAt.Equal(snapshot.updatedAt) {
		return false, nil
	}
	var active bool
	if err := tx.GetContext(ctx, &active, `SELECT EXISTS (SELECT 1 FROM runner_runs
		WHERE conversation_id = ? AND status IN ('opening', 'running'))`, id); err != nil {
		return false, errors.Wrap(err, "failed to recheck active artifact uploads")
	}
	if active {
		return false, nil
	}
	for _, ref := range refs {
		if _, err := tx.ExecContext(ctx, `DELETE FROM conversation_artifacts
			WHERE conversation_id = ? AND tool_call_id = ? AND artifact_id = ?
			AND EXISTS (SELECT 1 FROM image_artifacts WHERE id = ? AND created_at < ?)`,
			id, ref.ToolCallID, ref.ArtifactID, ref.ArtifactID, cutoff); err != nil {
			return false, errors.Wrap(err, "failed to release abandoned image uploads")
		}
	}
	return true, errors.Wrap(tx.Commit(), "failed to commit artifact cleanup")
}

func (s *Store) pruneMetadata(ctx context.Context, cutoff time.Time) error {
	var after string
	for {
		var ids []string
		if err := s.db.Reader.SelectContext(ctx, &ids, `SELECT id FROM image_artifacts
			WHERE id > ? AND created_at < ?
			AND NOT EXISTS (SELECT 1 FROM conversation_artifacts WHERE artifact_id = image_artifacts.id)
			ORDER BY id LIMIT ?`, after, cutoff, cleanupBatchSize); err != nil {
			return errors.Wrap(err, "failed to list unreferenced image metadata")
		}
		if len(ids) == 0 {
			return nil
		}
		query, args, err := sqlx.In(`DELETE FROM image_artifacts WHERE id IN (?) AND created_at < ?
			AND NOT EXISTS (SELECT 1 FROM conversation_artifacts WHERE artifact_id = image_artifacts.id)`, ids, cutoff)
		if err != nil {
			return errors.Wrap(err, "failed to prepare image metadata cleanup")
		}
		if _, err := s.db.Writer.ExecContext(ctx, query, args...); err != nil {
			return errors.Wrap(err, "failed to delete unreferenced image metadata")
		}
		after = ids[len(ids)-1]
	}
}

func (s *Store) sweepFiles(ctx context.Context, cutoff time.Time) error {
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
			if err := s.db.Reader.GetContext(ctx, &exists,
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
