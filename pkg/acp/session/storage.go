package session

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/pkg/errors"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	"github.com/jingkaihe/kodelet/pkg/db"
)

// StoredUpdate represents a stored session update for replay
type StoredUpdate struct {
	SessionID acptypes.SessionID `json:"sessionId"`
	Update    json.RawMessage    `json:"update"`
}

// pendingUpdate tracks a partially accumulated update for merging
type pendingUpdate struct {
	updateType string
	text       strings.Builder
	sessionID  acptypes.SessionID
}

// mergeableUpdateTypes are update types that can be merged (text content accumulated)
var mergeableUpdateTypes = map[string]bool{
	acptypes.UpdateAgentMessageChunk: true,
	acptypes.UpdateThoughtChunk:      true,
	acptypes.UpdateUserMessageChunk:  true,
}

// extractMergeableContent checks if an update is mergeable and extracts its type and text
func extractMergeableContent(update any) (updateType string, text string, isMergeable bool) {
	m, ok := update.(map[string]any)
	if !ok {
		return "", "", false
	}

	ut, ok := m["sessionUpdate"].(string)
	if !ok || !mergeableUpdateTypes[ut] {
		return "", "", false
	}

	content, ok := m["content"].(map[string]any)
	if !ok {
		return "", "", false
	}

	contentType, ok := content["type"].(string)
	if !ok || contentType != "text" {
		return "", "", false
	}

	t, ok := content["text"].(string)
	if !ok {
		return "", "", false
	}

	return ut, t, true
}

type sessionState struct {
	mu      sync.Mutex
	pending *pendingUpdate
}

// Storage handles persistence of ACP session updates using SQLite.
type Storage struct {
	dbPath string
	db     *sqlx.DB

	sessionsMu sync.Mutex
	sessions   map[acptypes.SessionID]*sessionState
}

// StorageOption configures a Storage instance.
type StorageOption func(*Storage)

// WithDBPath sets a custom database path for session storage.
func WithDBPath(path string) StorageOption {
	return func(s *Storage) {
		s.dbPath = path
	}
}

// NewStorage creates a new SQLite-based session storage.
// Note: Migrations should be run via db.RunMigrations() at CLI startup before calling this.
func NewStorage(ctx context.Context, opts ...StorageOption) (*Storage, error) {
	s := &Storage{
		sessions: make(map[acptypes.SessionID]*sessionState),
	}

	for _, opt := range opts {
		opt(s)
	}

	if s.dbPath == "" {
		dbPath, err := db.DefaultDBPath()
		if err != nil {
			return nil, err
		}
		s.dbPath = dbPath
	}

	sqlDB, err := db.Open(ctx, s.dbPath)
	if err != nil {
		return nil, err
	}
	s.db = sqlDB

	return s, nil
}

func (s *Storage) getOrCreateSession(sessionID acptypes.SessionID) *sessionState {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()

	if ss, ok := s.sessions[sessionID]; ok {
		return ss
	}

	ss := &sessionState{}
	s.sessions[sessionID] = ss
	return ss
}

// AppendUpdate appends a session update, merging consecutive text chunks.
func (s *Storage) AppendUpdate(sessionID acptypes.SessionID, update any) error {
	ss := s.getOrCreateSession(sessionID)

	ss.mu.Lock()
	defer ss.mu.Unlock()

	updateType, text, isMergeable := extractMergeableContent(update)

	if isMergeable {
		if ss.pending != nil && ss.pending.updateType == updateType {
			ss.pending.text.WriteString(text)
			return nil
		}

		if err := s.flushPendingLocked(sessionID, ss); err != nil {
			return err
		}

		ss.pending = &pendingUpdate{
			updateType: updateType,
			sessionID:  sessionID,
		}
		ss.pending.text.WriteString(text)
		return nil
	}

	if err := s.flushPendingLocked(sessionID, ss); err != nil {
		return err
	}

	return s.writeUpdate(sessionID, update)
}

func (s *Storage) flushPendingLocked(sessionID acptypes.SessionID, ss *sessionState) error {
	if ss.pending == nil || ss.pending.text.Len() == 0 {
		return nil
	}

	update := map[string]any{
		"sessionUpdate": ss.pending.updateType,
		"content": map[string]any{
			"type": acptypes.ContentTypeText,
			"text": ss.pending.text.String(),
		},
	}

	err := s.writeUpdate(sessionID, update)
	ss.pending = nil
	return err
}

func (s *Storage) writeUpdate(sessionID acptypes.SessionID, update any) error {
	updateJSON, err := json.Marshal(update)
	if err != nil {
		return errors.Wrap(err, "failed to marshal update")
	}

	entry := StoredUpdate{
		SessionID: sessionID,
		Update:    updateJSON,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return errors.Wrap(err, "failed to marshal session update entry")
	}

	_, err = s.db.Exec(
		"INSERT INTO acp_session_updates (session_id, update_data, created_at) VALUES (?, ?, ?)",
		string(sessionID), string(data), time.Now())
	if err != nil {
		return errors.Wrap(err, "failed to insert session update")
	}

	return nil
}

// ReadUpdates reads all updates for a session for replay.
func (s *Storage) ReadUpdates(sessionID acptypes.SessionID) ([]StoredUpdate, error) {
	var rows []struct {
		UpdateData string `db:"update_data"`
	}

	err := s.db.Select(&rows,
		"SELECT update_data FROM acp_session_updates WHERE session_id = ? ORDER BY id ASC",
		string(sessionID))
	if err != nil {
		return nil, errors.Wrap(err, "failed to read session updates")
	}

	if len(rows) == 0 {
		return nil, nil
	}

	updates := make([]StoredUpdate, 0, len(rows))
	for _, row := range rows {
		var update StoredUpdate
		if err := json.Unmarshal([]byte(row.UpdateData), &update); err != nil {
			return nil, errors.Wrap(err, "failed to unmarshal session update")
		}
		updates = append(updates, update)
	}

	return updates, nil
}

// Flush writes any pending update for a session to disk.
func (s *Storage) Flush(sessionID acptypes.SessionID) error {
	s.sessionsMu.Lock()
	ss, ok := s.sessions[sessionID]
	s.sessionsMu.Unlock()

	if !ok {
		return nil
	}

	ss.mu.Lock()
	defer ss.mu.Unlock()

	return s.flushPendingLocked(sessionID, ss)
}

// Delete removes all session updates from storage.
func (s *Storage) Delete(sessionID acptypes.SessionID) error {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()

	if ss, ok := s.sessions[sessionID]; ok {
		ss.mu.Lock()
		ss.pending = nil
		ss.mu.Unlock()
		delete(s.sessions, sessionID)
	}

	_, err := s.db.Exec("DELETE FROM acp_session_updates WHERE session_id = ?", string(sessionID))
	if err != nil {
		return errors.Wrap(err, "failed to delete session updates")
	}

	return nil
}

// CloseSession flushes pending updates and removes the session from memory.
func (s *Storage) CloseSession(sessionID acptypes.SessionID) error {
	if err := s.Flush(sessionID); err != nil {
		return errors.Wrap(err, "failed to flush pending update")
	}

	s.sessionsMu.Lock()
	delete(s.sessions, sessionID)
	s.sessionsMu.Unlock()

	return nil
}

// Exists checks if a session has any stored updates.
func (s *Storage) Exists(sessionID acptypes.SessionID) bool {
	var exists bool
	err := s.db.Get(&exists, "SELECT EXISTS(SELECT 1 FROM acp_session_updates WHERE session_id = ?)", string(sessionID))
	if err != nil {
		return false
	}
	return exists
}

// Close flushes all pending updates and closes the database connection.
func (s *Storage) Close() error {
	s.sessionsMu.Lock()
	var flushErr error
	for sessionID, ss := range s.sessions {
		ss.mu.Lock()
		if err := s.flushPendingLocked(sessionID, ss); err != nil && flushErr == nil {
			flushErr = err
		}
		ss.mu.Unlock()
	}
	s.sessions = make(map[acptypes.SessionID]*sessionState)
	s.sessionsMu.Unlock()

	if s.db != nil {
		if err := s.db.Close(); err != nil {
			return err
		}
	}
	return flushErr
}
