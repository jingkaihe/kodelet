// Package steer provides persistent user steering for autonomous conversations.
package steer

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/osutil"
	"github.com/jmoiron/sqlx"
	"github.com/pkg/errors"
)

const MaxMessageLength = 10000

// FormatPendingNotice renders the user-facing notice shown when queued steering is injected.
func FormatPendingNotice(content string, imageCount int) string {
	if imageCount > 0 {
		return fmt.Sprintf("🗣️ User steering: %s (%d image%s)", content, imageCount, pluralSuffix(imageCount))
	}
	return fmt.Sprintf("🗣️ User steering: %s", content)
}

func pluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

// Message represents a queued steering message.
type Message struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Images    []string  `json:"images,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// Store manages the steering queue in Kodelet's shared SQLite database.
type Store struct {
	db *sqlx.DB
}

type storeConfig struct {
	dbPath string
}

// StoreOption configures a steering store.
type StoreOption func(*storeConfig)

// WithDBPath overrides the shared database path. It is primarily useful for tests.
func WithDBPath(dbPath string) StoreOption {
	return func(config *storeConfig) {
		config.dbPath = dbPath
	}
}

// NewSteerStore opens the shared SQLite database.
// Database migrations must be applied before the store is used.
func NewSteerStore(ctx context.Context, opts ...StoreOption) (*Store, error) {
	config := storeConfig{}
	for _, opt := range opts {
		opt(&config)
	}

	if strings.TrimSpace(config.dbPath) == "" {
		dbPath, err := db.DefaultDBPath()
		if err != nil {
			return nil, errors.Wrap(err, "failed to resolve steering database path")
		}
		config.dbPath = dbPath
	}

	database, err := db.Open(ctx, config.dbPath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to open steering database")
	}

	return &Store{db: database}, nil
}

// Close releases the store's database connection.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Enqueue appends a steering message and reports whether messages were already queued.
func (s *Store) Enqueue(ctx context.Context, conversationID, content string, images []string) (bool, error) {
	normalizedImages, err := normalizeImageInputs(images)
	if err != nil {
		return false, err
	}
	if normalizedImages == nil {
		normalizedImages = []string{}
	}

	imagesJSON, err := json.Marshal(normalizedImages)
	if err != nil {
		return false, errors.Wrap(err, "failed to marshal steering images")
	}

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return false, errors.Wrap(err, "failed to begin steering enqueue transaction")
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO steering_messages (conversation_id, content, images_json, created_at)
		VALUES (?, ?, ?, ?)
	`, conversationID, content, string(imagesJSON), time.Now().UTC()); err != nil {
		return false, errors.Wrap(err, "failed to enqueue steering message")
	}

	var count int
	if err := tx.GetContext(ctx, &count, `
		SELECT COUNT(*) FROM steering_messages WHERE conversation_id = ?
	`, conversationID); err != nil {
		return false, errors.Wrap(err, "failed to count queued steering messages")
	}

	if err := tx.Commit(); err != nil {
		return false, errors.Wrap(err, "failed to commit steering message")
	}

	return count > 1, nil
}

// Peek returns pending steering messages without consuming them.
func (s *Store) Peek(ctx context.Context, conversationID string) ([]Message, error) {
	rows, err := s.db.QueryxContext(ctx, `
		SELECT id, content, images_json, created_at
		FROM steering_messages
		WHERE conversation_id = ?
		ORDER BY id ASC
	`, conversationID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to query pending steering messages")
	}
	defer rows.Close()

	messages, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	return messages, nil
}

// Consume atomically removes and returns all pending steering messages for a conversation.
func (s *Store) Consume(ctx context.Context, conversationID string) ([]Message, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to begin steering consume transaction")
	}
	defer tx.Rollback()

	rows, err := tx.QueryxContext(ctx, `
		DELETE FROM steering_messages
		WHERE conversation_id = ?
		RETURNING id, content, images_json, created_at
	`, conversationID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to consume pending steering messages")
	}

	messages, scanErr := scanMessages(rows)
	closeErr := rows.Close()
	if scanErr != nil {
		return nil, scanErr
	}
	if closeErr != nil {
		return nil, errors.Wrap(closeErr, "failed to close consumed steering rows")
	}

	if err := tx.Commit(); err != nil {
		return nil, errors.Wrap(err, "failed to commit consumed steering messages")
	}

	return messages, nil
}

// HasPending reports whether a conversation has queued steering messages.
func (s *Store) HasPending(ctx context.Context, conversationID string) (bool, error) {
	var pending bool
	if err := s.db.GetContext(ctx, &pending, `
		SELECT EXISTS(
			SELECT 1 FROM steering_messages WHERE conversation_id = ?
		)
	`, conversationID); err != nil {
		return false, errors.Wrap(err, "failed to check pending steering messages")
	}
	return pending, nil
}

type messageRow struct {
	id         int64
	content    string
	imagesJSON string
	createdAt  time.Time
}

func scanMessages(rows *sqlx.Rows) ([]Message, error) {
	messageRows := make([]messageRow, 0)
	for rows.Next() {
		var row messageRow
		if err := rows.Scan(&row.id, &row.content, &row.imagesJSON, &row.createdAt); err != nil {
			return nil, errors.Wrap(err, "failed to scan steering message")
		}
		messageRows = append(messageRows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Wrap(err, "failed to iterate steering messages")
	}

	sort.Slice(messageRows, func(i, j int) bool {
		return messageRows[i].id < messageRows[j].id
	})

	messages := make([]Message, 0, len(messageRows))
	for _, row := range messageRows {
		var images []string
		if err := json.Unmarshal([]byte(row.imagesJSON), &images); err != nil {
			return nil, errors.Wrap(err, "failed to unmarshal steering images")
		}
		messages = append(messages, Message{
			Role:      "user",
			Content:   row.content,
			Images:    images,
			Timestamp: row.createdAt,
		})
	}

	return messages, nil
}

func normalizeImageInputs(images []string) ([]string, error) {
	if len(images) == 0 {
		return nil, nil
	}

	normalized := make([]string, 0, len(images))
	for _, image := range images {
		image = strings.TrimSpace(image)
		if image == "" {
			continue
		}

		if strings.HasPrefix(image, "https://") || strings.HasPrefix(image, "data:") {
			normalized = append(normalized, image)
			continue
		}

		filePath := image
		if path, ok := strings.CutPrefix(filePath, "file://"); ok {
			filePath = path
		}

		absPath, err := filepath.Abs(filePath)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to normalize image path %s", image)
		}
		absPath = osutil.CanonicalizePath(absPath)
		normalized = append(normalized, absPath)
	}

	return normalized, nil
}
