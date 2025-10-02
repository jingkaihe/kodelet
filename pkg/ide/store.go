// Package ide manages IDE context integration for kodelet.
package ide

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/rogpeppe/go-internal/lockedfile"
)

// ErrContextNotFound is returned when an IDE context file does not exist.
var ErrContextNotFound = errors.New("IDE context not found")

// Store manages persistent storage of IDE context information including
// open files, code selections, and diagnostics. It provides thread-safe
// operations with file-based persistence.
type Store struct {
	ideDir string
	mu     sync.RWMutex
}

// Context represents the current state of the IDE including open files,
// selected code regions, and diagnostic messages (errors, warnings, etc.).
type Context struct {
	OpenFiles   []FileInfo       `json:"open_files"`
	Selection   *SelectionInfo   `json:"selection,omitempty"`
	Diagnostics []DiagnosticInfo `json:"diagnostics,omitempty"`
	UpdatedAt   time.Time        `json:"updated_at"`
}

// FileInfo represents an open file in the IDE with its path and programming
// language identifier.
type FileInfo struct {
	Path     string `json:"path"`
	Language string `json:"language,omitempty"`
}

// SelectionInfo represents a selected region of code in the IDE including the
// file path, line range, and the selected text content.
type SelectionInfo struct {
	FilePath  string `json:"file_path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Content   string `json:"content"`
}

// DiagnosticInfo represents a diagnostic message (error, warning, info, or hint)
// from language servers or linters in the IDE.
type DiagnosticInfo struct {
	FilePath string `json:"file_path"`
	Line     int    `json:"line"`
	Column   int    `json:"column,omitempty"`
	Severity string `json:"severity"` // "error", "warning", "info", "hint"
	Message  string `json:"message"`
	Source   string `json:"source,omitempty"` // e.g., "eslint", "gopls", "rust-analyzer"
	Code     string `json:"code,omitempty"`   // e.g., "unused-var", "E0308"
}

// NewIDEStore creates a new IDE context store with the storage directory
// initialized in the user's home directory at ~/.kodelet/ide.
func NewIDEStore() (*Store, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get user home directory")
	}

	ideDir := filepath.Join(homeDir, ".kodelet", "ide")

	if err := os.MkdirAll(ideDir, 0o755); err != nil {
		return nil, errors.Wrap(err, "failed to create ide directory")
	}

	return &Store{
		ideDir: ideDir,
	}, nil
}

func (s *Store) getContextPath(conversationID string) string {
	return filepath.Join(s.ideDir, fmt.Sprintf("context-%s.json", conversationID))
}

// WriteContext persists IDE context data to disk for the specified conversation ID.
// It updates the timestamp and uses locked file operations for thread safety.
func (s *Store) WriteContext(conversationID string, context *Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	filePath := s.getContextPath(conversationID)
	context.UpdatedAt = time.Now()

	data, err := json.MarshalIndent(context, "", "  ")
	if err != nil {
		return errors.Wrap(err, "failed to marshal IDE context")
	}

	if err := lockedfile.Write(filePath, bytes.NewReader(data), 0o644); err != nil {
		return errors.Wrap(err, "failed to write IDE context file")
	}

	return nil
}

// ReadContext loads IDE context data from disk for the specified conversation ID.
// It returns ErrContextNotFound if the context file does not exist.
func (s *Store) ReadContext(conversationID string) (*Context, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	filePath := s.getContextPath(conversationID)

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return nil, ErrContextNotFound
	}

	data, err := lockedfile.Read(filePath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read IDE context file")
	}

	var context Context
	if err := json.Unmarshal(data, &context); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal IDE context")
	}

	return &context, nil
}

// ClearContext removes the IDE context file for the specified conversation ID.
// It does not return an error if the file does not exist.
func (s *Store) ClearContext(conversationID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	filePath := s.getContextPath(conversationID)

	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "failed to remove IDE context file")
	}
	return nil
}

// HasContext checks if IDE context data exists for the specified conversation ID.
// It returns true if a non-empty context file exists.
func (s *Store) HasContext(conversationID string) bool {
	filePath := s.getContextPath(conversationID)

	if info, err := os.Stat(filePath); err == nil && info.Size() > 0 {
		return true
	}

	return false
}
