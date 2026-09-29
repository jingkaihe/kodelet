package sqlite

import (
	"context"
	"database/sql"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/pkg/errors"

	"github.com/jingkaihe/kodelet/pkg/types/conversations"
)

const (
	searchSnippetTokens   = 32
	searchSnippetEllipsis = "…"
	searchHighlight       = "**"
)

// searchScoreSQL weights FTS5 bm25 ranks by entry kind. Ranks are negative and
// lower is better, so a factor below one demotes tool inputs relative to prose.
const searchScoreSQL = `m.rank * CASE e.kind WHEN 'tool-use' THEN 0.5 ELSE 1.0 END`

// searchHitsCTE groups matching entries by conversation. The FTS5 match is
// materialized first because SQLite rejects rank and auxiliary functions once
// a MATCH query is flattened into joins or aggregates. bm25 ranks are only
// computed when ranking by relevance, roughly halving broad filter queries.
func searchHitsCTE(ranked bool) string {
	rank, score := "", ""
	if ranked {
		rank = ", rank"
		score = ", MIN(" + searchScoreSQL + ") AS search_score"
	}
	return `WITH search_matches AS MATERIALIZED (
	SELECT rowid AS entry_id` + rank + ` FROM conversation_search WHERE conversation_search MATCH :search_match
), search_hits AS (
	SELECT e.conversation_id` + score + `, COUNT(*) AS search_match_count
	FROM search_matches m
	JOIN conversation_search_entries e ON e.id = m.entry_id
	GROUP BY e.conversation_id
)
`
}

// PendingSearchIndex returns conversations whose search entries are missing,
// stale, or built by another extraction version, most recently updated first.
// Dirtiness is derived from the database, so saves by any process are found.
func (s *Store) PendingSearchIndex(ctx context.Context, version int) ([]conversations.SearchIndexCandidate, error) {
	var candidates []conversations.SearchIndexCandidate
	err := s.db.SelectContext(ctx, &candidates, `
		SELECT s.id, s.updated_at
		FROM conversation_summaries s
		LEFT JOIN conversation_search_state st ON st.conversation_id = s.id
		WHERE st.conversation_id IS NULL
			OR st.index_version <> ?
			OR st.source_updated_at IS NOT s.updated_at
		ORDER BY s.updated_at DESC
	`, version)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list conversations pending search indexing")
	}
	return candidates, nil
}

// LoadSearchSource loads the fields needed to build search entries, skipping
// usage and structured tool results, which dominate stored conversation size.
func (s *Store) LoadSearchSource(ctx context.Context, id string) (conversations.ConversationRecord, error) {
	var dbRecord dbConversationRecord
	err := s.db.GetContext(ctx, &dbRecord, `
		SELECT id, cwd, raw_messages, provider, summary, created_at, updated_at, metadata, compaction_history
		FROM conversations WHERE id = ?
	`, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return conversations.ConversationRecord{}, errors.Wrapf(conversations.ErrConversationNotFound, "%s", id)
		}
		return conversations.ConversationRecord{}, errors.Wrap(err, "failed to load conversation for search indexing")
	}
	return dbRecord.ToConversationRecord(), nil
}

// ReplaceSearchIndex atomically replaces one conversation's search entries.
// It reports false without writing when the conversation changed after the
// document was extracted, leaving it pending for the next pass. Unchanged
// content only advances the recorded source version.
func (s *Store) ReplaceSearchIndex(ctx context.Context, document conversations.SearchDocument) (bool, error) {
	connection, err := s.db.Connx(ctx)
	if err != nil {
		return false, errors.Wrap(err, "failed to open search index connection")
	}
	defer connection.Close()
	// Reserve the write lock before reading, so a concurrent save cannot make
	// this transaction's snapshot unwritable (SQLITE_BUSY_SNAPSHOT).
	if _, err := connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return false, errors.Wrap(err, "failed to begin search index transaction")
	}
	defer func() { _, _ = connection.ExecContext(context.WithoutCancel(ctx), "ROLLBACK") }()

	var updatedAt sql.NullTime
	err = connection.GetContext(ctx, &updatedAt, `SELECT updated_at FROM conversation_summaries WHERE id = ?`, document.ConversationID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if err := deleteSearchIndex(ctx, connection, document.ConversationID); err != nil {
			return false, err
		}
		_, err = connection.ExecContext(ctx, "COMMIT")
		return false, errors.Wrap(err, "failed to commit search index removal")
	case err != nil:
		return false, errors.Wrap(err, "failed to check conversation for search indexing")
	case !updatedAt.Valid || !updatedAt.Time.Equal(document.SourceUpdatedAt):
		return false, nil
	}

	var state struct {
		Version     int    `db:"index_version"`
		ContentHash string `db:"content_hash"`
	}
	err = connection.GetContext(ctx, &state, `
		SELECT index_version, content_hash FROM conversation_search_state WHERE conversation_id = ?
	`, document.ConversationID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, errors.Wrap(err, "failed to load search index state")
	}
	unchanged := err == nil && state.Version == document.Version && state.ContentHash == document.ContentHash
	if !unchanged {
		if err := deleteSearchIndex(ctx, connection, document.ConversationID); err != nil {
			return false, err
		}
		if err := insertSearchEntries(ctx, connection, document); err != nil {
			return false, err
		}
	}
	// Copy updated_at through SQL so the stored value compares exactly with
	// conversation_summaries.updated_at in PendingSearchIndex.
	_, err = connection.ExecContext(ctx, `
		INSERT INTO conversation_search_state (conversation_id, source_updated_at, index_version, content_hash)
		SELECT id, updated_at, ?, ? FROM conversation_summaries WHERE id = ?
		ON CONFLICT(conversation_id) DO UPDATE SET
			source_updated_at = excluded.source_updated_at,
			index_version = excluded.index_version,
			content_hash = excluded.content_hash
	`, document.Version, document.ContentHash, document.ConversationID)
	if err != nil {
		return false, errors.Wrap(err, "failed to save search index state")
	}
	if _, err := connection.ExecContext(ctx, "COMMIT"); err != nil {
		return false, errors.Wrap(err, "failed to commit search index")
	}
	return true, nil
}

// PruneSearchIndex removes search entries of conversations deleted without
// going through Store.Delete, such as by another process or an older binary.
func (s *Store) PruneSearchIndex(ctx context.Context) (int, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return 0, errors.Wrap(err, "failed to begin search index pruning")
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
		DELETE FROM conversation_search_entries WHERE conversation_id IN (
			SELECT st.conversation_id FROM conversation_search_state st
			WHERE NOT EXISTS (SELECT 1 FROM conversation_summaries s WHERE s.id = st.conversation_id)
		)
	`)
	if err != nil {
		return 0, errors.Wrap(err, "failed to prune search entries")
	}
	result, err := tx.ExecContext(ctx, `
		DELETE FROM conversation_search_state
		WHERE NOT EXISTS (SELECT 1 FROM conversation_summaries s WHERE s.id = conversation_search_state.conversation_id)
	`)
	if err != nil {
		return 0, errors.Wrap(err, "failed to prune search index state")
	}
	pruned, err := result.RowsAffected()
	if err != nil {
		return 0, errors.Wrap(err, "failed to count pruned search index state")
	}
	if err := tx.Commit(); err != nil {
		return 0, errors.Wrap(err, "failed to commit search index pruning")
	}
	return int(pruned), nil
}

func deleteSearchIndex(ctx context.Context, execer sqlx.ExecerContext, conversationID string) error {
	if _, err := execer.ExecContext(ctx, `DELETE FROM conversation_search_entries WHERE conversation_id = ?`, conversationID); err != nil {
		return errors.Wrap(err, "failed to delete search entries")
	}
	if _, err := execer.ExecContext(ctx, `DELETE FROM conversation_search_state WHERE conversation_id = ?`, conversationID); err != nil {
		return errors.Wrap(err, "failed to delete search index state")
	}
	return nil
}

func insertSearchEntries(ctx context.Context, connection *sqlx.Conn, document conversations.SearchDocument) error {
	if len(document.Entries) == 0 {
		return nil
	}
	statement, err := connection.PreparexContext(ctx, `
		INSERT INTO conversation_search_entries (conversation_id, entry_index, role, kind, payload)
		VALUES (?, ?, ?, ?, ?)
	`)
	if err != nil {
		return errors.Wrap(err, "failed to prepare search entry insert")
	}
	defer statement.Close()
	for _, entry := range document.Entries {
		_, err := statement.ExecContext(ctx, document.ConversationID, entry.EntryIndex, entry.Role, entry.Kind, entry.Payload)
		if err != nil {
			return errors.Wrap(err, "failed to insert search entry")
		}
	}
	return nil
}

// loadSearchMatches returns the best highlighted matches for each conversation.
// Ranks are computed only for the page's entries and snippets only for the
// selected rows. The unary + keeps rowid filters out of the FTS5 query plan:
// as a constraint, FTS5 would re-evaluate the whole MATCH for every rowid,
// which takes seconds for broad prefixes.
func (s *Store) loadSearchMatches(ctx context.Context, match string, conversationIDs []string, perConversation int) (map[string][]conversations.SearchMatch, error) {
	matches := make(map[string][]conversations.SearchMatch, len(conversationIDs))
	if len(conversationIDs) == 0 || perConversation <= 0 {
		return matches, nil
	}
	query, args, err := sqlx.In(`
		WITH search_matches AS MATERIALIZED (
			SELECT rowid AS entry_id, rank FROM conversation_search
			WHERE conversation_search MATCH ?
				AND +rowid IN (SELECT id FROM conversation_search_entries WHERE conversation_id IN (?))
		), ranked AS (
			SELECT e.id, e.conversation_id, e.entry_index, e.role, e.kind,
				ROW_NUMBER() OVER (
					PARTITION BY e.conversation_id
					ORDER BY `+searchScoreSQL+`, e.entry_index
				) AS position
			FROM search_matches m
			JOIN conversation_search_entries e ON e.id = m.entry_id
		)
		SELECT id, conversation_id, entry_index, role, kind
		FROM ranked
		WHERE position <= ?
		ORDER BY conversation_id, position
	`, match, conversationIDs, perConversation)
	if err != nil {
		return nil, errors.Wrap(err, "failed to build search match query")
	}
	var rows []struct {
		ID             int64  `db:"id"`
		ConversationID string `db:"conversation_id"`
		EntryIndex     int    `db:"entry_index"`
		Role           string `db:"role"`
		Kind           string `db:"kind"`
	}
	if err := s.db.SelectContext(ctx, &rows, s.db.Rebind(query), args...); err != nil {
		return nil, errors.Wrap(err, "failed to rank search matches")
	}
	if len(rows) == 0 {
		return matches, nil
	}

	entryIDs := make([]int64, len(rows))
	for index, row := range rows {
		entryIDs[index] = row.ID
	}
	query, args, err = sqlx.In(`
		SELECT rowid AS id, snippet(conversation_search, 0, ?, ?, ?, ?) AS snippet
		FROM conversation_search
		WHERE conversation_search MATCH ? AND +rowid IN (?)
	`, searchHighlight, searchHighlight, searchSnippetEllipsis, searchSnippetTokens, match, entryIDs)
	if err != nil {
		return nil, errors.Wrap(err, "failed to build search snippet query")
	}
	var snippets []struct {
		ID      int64  `db:"id"`
		Snippet string `db:"snippet"`
	}
	if err := s.db.SelectContext(ctx, &snippets, s.db.Rebind(query), args...); err != nil {
		return nil, errors.Wrap(err, "failed to load search snippets")
	}
	snippetByID := make(map[int64]string, len(snippets))
	for _, snippet := range snippets {
		snippetByID[snippet.ID] = strings.Join(strings.Fields(snippet.Snippet), " ")
	}
	for _, row := range rows {
		matches[row.ConversationID] = append(matches[row.ConversationID], conversations.SearchMatch{
			EntryIndex: row.EntryIndex,
			Role:       row.Role,
			Kind:       row.Kind,
			Snippet:    snippetByID[row.ID],
		})
	}
	return matches, nil
}
