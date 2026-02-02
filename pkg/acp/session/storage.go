package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	"github.com/pkg/errors"
)

// defaultSessionsDir returns the default path to the ACP sessions directory.
func defaultSessionsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.Wrap(err, "failed to get home directory")
	}
	return filepath.Join(home, ".kodelet", "acp", "sessions"), nil
}

// StoredUpdate represents a stored session update for replay
type StoredUpdate struct {
	SessionID acptypes.SessionID `json:"sessionId"`
	Update    json.RawMessage    `json:"update"`
}

// pendingUpdate tracks a partially accumulated update for merging
type pendingUpdate struct {
	updateType string // e.g., "agent_message_chunk"
	text       strings.Builder
	sessionID  acptypes.SessionID
}

// sessionFile wraps a file handle with its own mutex for per-session locking
type sessionFile struct {
	mu      sync.Mutex
	file    *os.File
	pending *pendingUpdate // Current update being accumulated
}

// Storage handles persistence of ACP session updates as JSONL files
type Storage struct {
	basePath string

	filesMu sync.Mutex
	files   map[acptypes.SessionID]*sessionFile
}

// StorageOption configures a Storage instance
type StorageOption func(*Storage)

// WithBasePath sets a custom base path for session storage
func WithBasePath(path string) StorageOption {
	return func(s *Storage) {
		s.basePath = path
	}
}

// NewStorage creates a new session storage instance
func NewStorage(opts ...StorageOption) (*Storage, error) {
	s := &Storage{
		files: make(map[acptypes.SessionID]*sessionFile),
	}

	for _, opt := range opts {
		opt(s)
	}

	if s.basePath == "" {
		basePath, err := defaultSessionsDir()
		if err != nil {
			return nil, err
		}
		s.basePath = basePath
	}

	if err := os.MkdirAll(s.basePath, 0o755); err != nil {
		return nil, errors.Wrap(err, "failed to create storage directory")
	}

	return s, nil
}

// getSessionPath returns the path to a session's JSONL file
func (s *Storage) getSessionPath(sessionID acptypes.SessionID) string {
	return filepath.Join(s.basePath, string(sessionID)+".jsonl")
}

// mergeableUpdateTypes are update types that can be merged (text content accumulated)
var mergeableUpdateTypes = map[string]bool{
	acptypes.UpdateAgentMessageChunk: true,
	acptypes.UpdateThoughtChunk:      true,
	acptypes.UpdateUserMessageChunk:  true,
}

// AppendUpdate appends a session update to the JSONL file, merging consecutive
// text chunks of the same type for efficient replay.
func (s *Storage) AppendUpdate(sessionID acptypes.SessionID, update any) error {
	sf, err := s.getOrCreateSessionFile(sessionID)
	if err != nil {
		return err
	}

	sf.mu.Lock()
	defer sf.mu.Unlock()

	// Try to extract update type and text content for merging
	updateType, text, isMergeable := extractMergeableContent(update)

	if isMergeable {
		// Check if we can merge with pending update
		if sf.pending != nil && sf.pending.updateType == updateType {
			// Merge: accumulate text
			sf.pending.text.WriteString(text)
			return nil
		}

		// Different type or no pending - flush pending and start new
		if err := sf.flushPending(); err != nil {
			return err
		}

		sf.pending = &pendingUpdate{
			updateType: updateType,
			sessionID:  sessionID,
		}
		sf.pending.text.WriteString(text)
		return nil
	}

	// Non-mergeable update: flush pending and write directly
	if err := sf.flushPending(); err != nil {
		return err
	}

	return sf.write(sessionID, update)
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

// flushPending writes any pending update to disk. Caller must hold sf.mu.
func (sf *sessionFile) flushPending() error {
	if sf.pending == nil || sf.pending.text.Len() == 0 {
		return nil
	}

	update := map[string]any{
		"sessionUpdate": sf.pending.updateType,
		"content": map[string]any{
			"type": acptypes.ContentTypeText,
			"text": sf.pending.text.String(),
		},
	}

	err := sf.write(sf.pending.sessionID, update)
	sf.pending = nil
	return err
}

// write marshals and writes an update to the file. Caller must hold sf.mu.
func (sf *sessionFile) write(sessionID acptypes.SessionID, update any) error {
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

	if _, err := sf.file.Write(append(data, '\n')); err != nil {
		return errors.Wrap(err, "failed to write to session file")
	}

	return sf.file.Sync()
}

// getOrCreateSessionFile returns a session file handle, creating if needed
func (s *Storage) getOrCreateSessionFile(sessionID acptypes.SessionID) (*sessionFile, error) {
	s.filesMu.Lock()
	defer s.filesMu.Unlock()

	if sf, ok := s.files[sessionID]; ok {
		return sf, nil
	}

	path := s.getSessionPath(sessionID)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, errors.Wrap(err, "failed to open session file")
	}

	sf := &sessionFile{file: file}
	s.files[sessionID] = sf
	return sf, nil
}

// ReadUpdates reads all updates for a session for replay
func (s *Storage) ReadUpdates(sessionID acptypes.SessionID) ([]StoredUpdate, error) {
	path := s.getSessionPath(sessionID)

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No updates to replay
		}
		return nil, errors.Wrap(err, "failed to open session file")
	}
	defer file.Close()

	var updates []StoredUpdate
	scanner := bufio.NewScanner(file)

	// Increase buffer size for large updates
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024) // Max 1MB per line

	for scanner.Scan() {
		var update StoredUpdate
		if err := json.Unmarshal(scanner.Bytes(), &update); err != nil {
			return nil, errors.Wrap(err, "failed to unmarshal session update")
		}
		updates = append(updates, update)
	}

	if err := scanner.Err(); err != nil {
		return nil, errors.Wrap(err, "failed to read session file")
	}

	return updates, nil
}

// Delete removes the session's JSONL file
func (s *Storage) Delete(sessionID acptypes.SessionID) error {
	s.filesMu.Lock()
	defer s.filesMu.Unlock()

	// Close file handle if open (no need to flush - we're deleting anyway)
	if sf, ok := s.files[sessionID]; ok {
		sf.mu.Lock()
		sf.file.Close()
		sf.mu.Unlock()
		delete(s.files, sessionID)
	}

	path := s.getSessionPath(sessionID)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "failed to delete session file")
	}

	return nil
}

// CloseSession closes the file handle for a session without deleting
func (s *Storage) CloseSession(sessionID acptypes.SessionID) error {
	s.filesMu.Lock()
	defer s.filesMu.Unlock()

	sf, ok := s.files[sessionID]
	if !ok {
		return nil
	}

	sf.mu.Lock()
	defer sf.mu.Unlock()

	if err := sf.flushPending(); err != nil {
		return errors.Wrap(err, "failed to flush pending update")
	}
	if err := sf.file.Close(); err != nil {
		return errors.Wrap(err, "failed to close session file")
	}
	delete(s.files, sessionID)
	return nil
}

// Close closes all open file handles
func (s *Storage) Close() error {
	s.filesMu.Lock()
	defer s.filesMu.Unlock()

	var errs []error
	for sessionID, sf := range s.files {
		sf.mu.Lock()
		if err := sf.flushPending(); err != nil {
			errs = append(errs, errors.Wrapf(err, "failed to flush pending update for session %s", sessionID))
		}
		if err := sf.file.Close(); err != nil {
			errs = append(errs, errors.Wrapf(err, "failed to close session %s", sessionID))
		}
		sf.mu.Unlock()
		delete(s.files, sessionID)
	}

	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// Flush writes any pending update for a session to disk.
// Should be called at turn boundaries (e.g., after prompt completes).
func (s *Storage) Flush(sessionID acptypes.SessionID) error {
	s.filesMu.Lock()
	sf, ok := s.files[sessionID]
	s.filesMu.Unlock()

	if !ok {
		return nil
	}

	sf.mu.Lock()
	defer sf.mu.Unlock()

	return sf.flushPending()
}

// Exists checks if a session file exists
func (s *Storage) Exists(sessionID acptypes.SessionID) bool {
	path := s.getSessionPath(sessionID)
	_, err := os.Stat(path)
	return err == nil
}
