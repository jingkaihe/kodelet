// Package artifacts persists immutable image files and their conversation references.
package artifacts

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/jingkaihe/kodelet/pkg/db"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/jmoiron/sqlx"
	"github.com/pkg/errors"
	_ "golang.org/x/image/webp"
)

const (
	// MaxBytes bounds an uploaded image's encoded size.
	MaxBytes int64 = 32 * 1024 * 1024
	// MaxPixels bounds decoded image allocation, including all frames of a GIF.
	MaxPixels    int64 = 40_000_000
	cleanupGrace       = 24 * time.Hour
)

// ErrNotFound means the image is missing or is not referenced in the requested scope.
var ErrNotFound = errors.New("image artifact not found")

// Store owns immutable files beside the shared SQLite database.
type Store struct {
	db  *sqlx.DB
	dir string
	// Serialize image decoding to keep concurrent uploads from multiplying memory use.
	putMu sync.Mutex
}

// Open opens artifact storage after the shared database migrations have been applied.
// It sweeps abandoned uploads and unreferenced files older than 24 hours.
func Open(ctx context.Context, dbPath string) (*Store, error) {
	database, err := db.Open(ctx, dbPath)
	if err != nil {
		return nil, err
	}
	store := &Store{db: database, dir: filepath.Join(filepath.Dir(dbPath), "artifacts")}
	if err := os.MkdirAll(store.dir, 0o700); err != nil {
		_ = database.Close()
		return nil, errors.Wrap(err, "failed to create artifact directory")
	}
	if err := store.sweep(ctx); err != nil {
		_ = database.Close()
		return nil, err
	}
	return store, nil
}

// Close releases the database connection.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	return s.db.Close()
}

// Put validates and stores an image, adding its conversation/tool reference before returning.
// The caller must authenticate the upload's conversation and tool-call ownership.
func (s *Store) Put(
	ctx context.Context,
	conversationID, toolCallID string,
	attachment tooltypes.ToolAttachment,
	source io.Reader,
) (tooltypes.ToolAttachment, error) {
	if strings.TrimSpace(conversationID) == "" || strings.TrimSpace(toolCallID) == "" {
		return tooltypes.ToolAttachment{}, errors.New("conversation ID and tool call ID are required")
	}
	if attachment.Type != "image" || source == nil {
		return tooltypes.ToolAttachment{}, errors.New("an image attachment and source are required")
	}
	if err := ctx.Err(); err != nil {
		return tooltypes.ToolAttachment{}, err
	}

	file, err := os.CreateTemp(s.dir, ".upload-")
	if err != nil {
		return tooltypes.ToolAttachment{}, errors.Wrap(err, "failed to create image upload")
	}
	defer file.Close()
	defer os.Remove(file.Name())
	size, err := io.Copy(file, io.LimitReader(&contextReader{ctx: ctx, reader: source}, MaxBytes+1))
	if err != nil {
		return tooltypes.ToolAttachment{}, errors.Wrap(err, "failed to receive image")
	}
	if size > MaxBytes {
		return tooltypes.ToolAttachment{}, errors.Errorf("image exceeds %d byte limit", MaxBytes)
	}
	s.putMu.Lock()
	defer s.putMu.Unlock()
	if err := ctx.Err(); err != nil {
		return tooltypes.ToolAttachment{}, err
	}
	config, format, err := validateImage(file)
	if err != nil {
		return tooltypes.ToolAttachment{}, err
	}
	if err := ctx.Err(); err != nil {
		return tooltypes.ToolAttachment{}, err
	}
	mimeType := map[string]string{
		"png":  "image/png",
		"jpeg": "image/jpeg",
		"gif":  "image/gif",
		"webp": "image/webp",
	}[format]
	id, err := randomID()
	if err != nil {
		return tooltypes.ToolAttachment{}, err
	}
	code, err := randomID()
	if err != nil {
		return tooltypes.ToolAttachment{}, err
	}
	attachment.Filename = imageFilename(attachment, format)
	attachment.Type, attachment.ArtifactID, attachment.ShortCode = "image", "art_"+id, code
	attachment.Path, attachment.ViewURL, attachment.Error = "", "", ""
	attachment.MimeType, attachment.Width, attachment.Height, attachment.Size = mimeType, config.Width, config.Height, size
	if err := file.Sync(); err != nil {
		return tooltypes.ToolAttachment{}, errors.Wrap(err, "failed to sync uploaded image")
	}
	if err := file.Close(); err != nil {
		return tooltypes.ToolAttachment{}, errors.Wrap(err, "failed to close uploaded image")
	}
	path := filepath.Join(s.dir, attachment.ArtifactID)
	// Link rather than overwrite: even an ID collision must preserve immutable bytes.
	if err := os.Link(file.Name(), path); err != nil {
		return tooltypes.ToolAttachment{}, errors.Wrap(err, "failed to finalize uploaded image")
	}
	// Persist the directory entry before publishing metadata. A failed transaction
	// may leave an orphan file; the startup sweep safely recovers it after the grace period.
	dir, err := os.Open(s.dir)
	if err != nil {
		return tooltypes.ToolAttachment{}, errors.Wrap(err, "failed to open artifact directory")
	}
	syncErr := dir.Sync()
	_ = dir.Close()
	if syncErr != nil {
		return tooltypes.ToolAttachment{}, errors.Wrap(syncErr, "failed to sync artifact directory")
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return tooltypes.ToolAttachment{}, errors.Wrap(err, "failed to begin image artifact transaction")
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO image_artifacts
		(id, short_code, filename, mime_type, width, height, size, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		attachment.ArtifactID,
		code,
		attachment.Filename,
		mimeType,
		config.Width,
		config.Height,
		size,
		time.Now().UTC(),
	)
	if err != nil {
		return tooltypes.ToolAttachment{}, errors.Wrap(err, "failed to save image artifact")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO conversation_artifacts (conversation_id, tool_call_id, artifact_id) VALUES (?, ?, ?)`,
		conversationID, toolCallID, attachment.ArtifactID)
	if err != nil {
		return tooltypes.ToolAttachment{}, errors.Wrap(err, "failed to save image artifact reference")
	}
	if err := tx.Commit(); err != nil {
		return tooltypes.ToolAttachment{}, errors.Wrap(err, "failed to commit image artifact")
	}
	return attachment, nil
}

// Get resolves an image referenced by the conversation. The returned path is local to the control plane.
func (s *Store) Get(ctx context.Context, conversationID, artifactID string) (tooltypes.ToolAttachment, string, error) {
	return s.get(ctx,
		`a.id = ? AND EXISTS (SELECT 1 FROM conversation_artifacts r WHERE r.artifact_id = a.id AND r.conversation_id = ?)`,
		artifactID, conversationID,
	)
}

// GetByShortCode resolves a short link only while at least one conversation references its image.
// HTTP callers must still enforce the control plane's authentication policy.
func (s *Store) GetByShortCode(ctx context.Context, code string) (tooltypes.ToolAttachment, string, error) {
	return s.get(ctx,
		`a.short_code = ? AND EXISTS (SELECT 1 FROM conversation_artifacts r WHERE r.artifact_id = a.id)`,
		code,
	)
}

func (s *Store) get(ctx context.Context, predicate string, args ...any) (tooltypes.ToolAttachment, string, error) {
	attachment := tooltypes.ToolAttachment{Type: "image"}
	err := s.db.QueryRowContext(ctx,
		`SELECT a.id, a.short_code, a.filename, a.mime_type, a.width, a.height, a.size FROM image_artifacts a WHERE `+predicate,
		args...,
	).Scan(
		&attachment.ArtifactID,
		&attachment.ShortCode,
		&attachment.Filename,
		&attachment.MimeType,
		&attachment.Width,
		&attachment.Height,
		&attachment.Size,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return tooltypes.ToolAttachment{}, "", ErrNotFound
	}
	if err != nil {
		return tooltypes.ToolAttachment{}, "", errors.Wrap(err, "failed to load image artifact")
	}
	path := filepath.Join(s.dir, attachment.ArtifactID)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) || (err == nil && !info.Mode().IsRegular()) {
		return tooltypes.ToolAttachment{}, "", ErrNotFound
	}
	if err != nil {
		return tooltypes.ToolAttachment{}, "", errors.Wrap(err, "failed to stat image artifact")
	}
	return attachment, path, nil
}

func randomID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", errors.Wrap(err, "failed to generate artifact identifier")
	}
	return base64.RawURLEncoding.EncodeToString(value[:]), nil
}

func imageFilename(attachment tooltypes.ToolAttachment, format string) string {
	name := attachment.Filename
	if name == "" {
		name = attachment.Path
	}
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, name)
	if name == "" || name == "." || name == "/" || len(name) > 255 {
		return "image." + format
	}
	return name
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
