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

type IDEStore struct {
	ideDir string
	mu     sync.RWMutex
}

type IDEContext struct {
	OpenFiles   []FileInfo       `json:"open_files"`
	Selection   *SelectionInfo   `json:"selection,omitempty"`
	Diagnostics []DiagnosticInfo `json:"diagnostics,omitempty"`
	UpdatedAt   time.Time        `json:"updated_at"`
}

type FileInfo struct {
	Path     string `json:"path"`
	Language string `json:"language,omitempty"`
}

type SelectionInfo struct {
	FilePath  string `json:"file_path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Content   string `json:"content"`
}

type DiagnosticInfo struct {
	FilePath string `json:"file_path"`
	Line     int    `json:"line"`
	Column   int    `json:"column,omitempty"`
	Severity string `json:"severity"` // "error", "warning", "info", "hint"
	Message  string `json:"message"`
	Source   string `json:"source,omitempty"` // e.g., "eslint", "gopls", "rust-analyzer"
	Code     string `json:"code,omitempty"`   // e.g., "unused-var", "E0308"
}

func NewIDEStore() (*IDEStore, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get user home directory")
	}

	ideDir := filepath.Join(homeDir, ".kodelet", "ide")

	if err := os.MkdirAll(ideDir, 0755); err != nil {
		return nil, errors.Wrap(err, "failed to create ide directory")
	}

	return &IDEStore{
		ideDir: ideDir,
	}, nil
}

func (s *IDEStore) getContextPath(conversationID string) string {
	return filepath.Join(s.ideDir, fmt.Sprintf("context-%s.json", conversationID))
}

func (s *IDEStore) WriteContext(conversationID string, context *IDEContext) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	filePath := s.getContextPath(conversationID)
	context.UpdatedAt = time.Now()

	data, err := json.MarshalIndent(context, "", "  ")
	if err != nil {
		return errors.Wrap(err, "failed to marshal IDE context")
	}

	if err := lockedfile.Write(filePath, bytes.NewReader(data), 0644); err != nil {
		return errors.Wrap(err, "failed to write IDE context file")
	}

	return nil
}

func (s *IDEStore) ReadContext(conversationID string) (*IDEContext, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	filePath := s.getContextPath(conversationID)

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return nil, nil
	}

	data, err := lockedfile.Read(filePath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read IDE context file")
	}

	var context IDEContext
	if err := json.Unmarshal(data, &context); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal IDE context")
	}

	return &context, nil
}

func (s *IDEStore) ClearContext(conversationID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	filePath := s.getContextPath(conversationID)

	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "failed to remove IDE context file")
	}
	return nil
}

func (s *IDEStore) HasContext(conversationID string) bool {
	filePath := s.getContextPath(conversationID)

	if info, err := os.Stat(filePath); err == nil && info.Size() > 0 {
		return true
	}

	return false
}
