package migrations

import (
	"database/sql"

	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/pkg/errors"
)

// Migration20260910120000CreateImageArtifacts adds immutable image artifacts and conversation references.
func Migration20260910120000CreateImageArtifacts() db.Migration {
	return db.Migration{
		Version:     20260910120000,
		Description: "Create image artifacts and conversation references",
		Up: func(tx *sql.Tx) error {
			_, err := tx.Exec(`CREATE TABLE image_artifacts (
				id TEXT PRIMARY KEY,
				short_code TEXT NOT NULL UNIQUE,
				filename TEXT NOT NULL,
				mime_type TEXT NOT NULL,
				width INTEGER NOT NULL CHECK (width > 0),
				height INTEGER NOT NULL CHECK (height > 0),
				size INTEGER NOT NULL CHECK (size > 0),
				created_at DATETIME NOT NULL
			);
			CREATE TABLE conversation_artifacts (
				conversation_id TEXT NOT NULL,
				tool_call_id TEXT NOT NULL,
				artifact_id TEXT NOT NULL REFERENCES image_artifacts(id),
				PRIMARY KEY (conversation_id, tool_call_id, artifact_id)
			);
			CREATE INDEX idx_conversation_artifacts_artifact ON conversation_artifacts(artifact_id);`)
			// No conversation foreign key: uploads may precede the first checkpoint.
			return errors.Wrap(err, "failed to create image artifact tables")
		},
		Down: func(tx *sql.Tx) error {
			_, err := tx.Exec(`DROP TABLE IF EXISTS conversation_artifacts; DROP TABLE IF EXISTS image_artifacts;`)
			return errors.Wrap(err, "failed to drop image artifact tables")
		},
	}
}
