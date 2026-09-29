package migrations

import (
	"database/sql"

	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/pkg/errors"
)

// The daemon backfills this schema because entry extraction needs Go parsers.
func Migration20260929120000CreateConversationSearch() db.Migration {
	return db.Migration{
		Version:     20260929120000,
		Description: "Create conversation full-text search index",
		Up: func(tx *sql.Tx) error {
			_, err := tx.Exec(`
				CREATE TABLE conversation_search_entries (
					id INTEGER PRIMARY KEY,
					conversation_id TEXT NOT NULL,
					entry_index INTEGER NOT NULL,
					role TEXT NOT NULL,
					kind TEXT NOT NULL,
					payload TEXT NOT NULL
				);
				CREATE INDEX idx_conversation_search_entries_conversation
					ON conversation_search_entries(conversation_id);

				CREATE VIRTUAL TABLE conversation_search USING fts5(
					payload,
					content = 'conversation_search_entries',
					content_rowid = 'id',
					tokenize = 'unicode61 remove_diacritics 2'
				);
				CREATE TRIGGER conversation_search_entries_ai
				AFTER INSERT ON conversation_search_entries BEGIN
					INSERT INTO conversation_search(rowid, payload) VALUES (new.id, new.payload);
				END;
				CREATE TRIGGER conversation_search_entries_ad
				AFTER DELETE ON conversation_search_entries BEGIN
					INSERT INTO conversation_search(conversation_search, rowid, payload)
					VALUES ('delete', old.id, old.payload);
				END;
				CREATE TRIGGER conversation_search_entries_au
				AFTER UPDATE ON conversation_search_entries BEGIN
					INSERT INTO conversation_search(conversation_search, rowid, payload)
					VALUES ('delete', old.id, old.payload);
					INSERT INTO conversation_search(rowid, payload) VALUES (new.id, new.payload);
				END;

				CREATE TABLE conversation_search_state (
					conversation_id TEXT PRIMARY KEY,
					source_updated_at DATETIME NOT NULL,
					index_version INTEGER NOT NULL,
					content_hash TEXT NOT NULL
				);`)
			return errors.Wrap(err, "failed to create conversation search index")
		},
		Down: func(tx *sql.Tx) error {
			_, err := tx.Exec(`
				DROP TABLE IF EXISTS conversation_search_state;
				DROP TRIGGER IF EXISTS conversation_search_entries_au;
				DROP TRIGGER IF EXISTS conversation_search_entries_ad;
				DROP TRIGGER IF EXISTS conversation_search_entries_ai;
				DROP TABLE IF EXISTS conversation_search;
				DROP TABLE IF EXISTS conversation_search_entries;`)
			return errors.Wrap(err, "failed to drop conversation search index")
		},
	}
}
