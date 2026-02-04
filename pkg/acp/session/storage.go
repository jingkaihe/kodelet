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

type Storage struct {
	dbPath string
	db     *sqlx.DB

	sessionsMu sync.Mutex
	sessions   map[acptypes.SessionID]*sessionState
}

type StorageOption func(*Storage)

func WithDBPath(path string) StorageOption {
	return func(s *Storage) {
		s.dbPath = path
	}
}

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

	runner := db.NewMigrationRunner(sqlDB, componentName)
	if err := runner.Run(ctx, migrations); err != nil {
		sqlDB.Close()
		return nil, errors.Wrap(err, "failed to run migrations")
	}

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

func (s *Storage) Delete(sessionID acptypes.SessionID) error {
	s.sessionsMu.Lock()
	delete(s.sessions, sessionID)
	s.sessionsMu.Unlock()

	_, err := s.db.Exec("DELETE FROM acp_session_updates WHERE session_id = ?", string(sessionID))
	if err != nil {
		return errors.Wrap(err, "failed to delete session updates")
	}

	return nil
}

func (s *Storage) CloseSession(sessionID acptypes.SessionID) error {
	if err := s.Flush(sessionID); err != nil {
		return errors.Wrap(err, "failed to flush pending update")
	}

	s.sessionsMu.Lock()
	delete(s.sessions, sessionID)
	s.sessionsMu.Unlock()

	return nil
}

func (s *Storage) Exists(sessionID acptypes.SessionID) bool {
	var count int
	err := s.db.Get(&count, "SELECT COUNT(*) FROM acp_session_updates WHERE session_id = ? LIMIT 1", string(sessionID))
	if err != nil {
		return false
	}
	return count > 0
}

func (s *Storage) Close() error {
	s.sessionsMu.Lock()
	for sessionID, ss := range s.sessions {
		ss.mu.Lock()
		s.flushPendingLocked(sessionID, ss)
		ss.mu.Unlock()
	}
	s.sessions = make(map[acptypes.SessionID]*sessionState)
	s.sessionsMu.Unlock()

	if s.db != nil {
		return s.db.Close()
	}
	return nil
}
