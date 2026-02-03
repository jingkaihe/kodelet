// Package steer provides functionality for managing user steering messages
// for autonomous conversations in kodelet. It handles storing, loading and
// managing steering messages with file-based persistence.
package steer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/pkg/errors"
	"github.com/rogpeppe/go-internal/lockedfile"
)

// Message represents a steering message
type Message struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

// Store manages persistent storage of user steering messages for autonomous
// conversations. It provides thread-safe operations for writing, reading, and
// clearing steering data with file-based persistence.
type Store struct {
	steerDir string
	mu       sync.RWMutex
}

// Data represents the structure of the steer JSON file containing
// a collection of steering messages.
type Data struct {
	Messages []Message `json:"messages"`
}

// NewSteerStore creates a new steer store
func NewSteerStore() (*Store, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get user home directory")
	}

	steerDir := filepath.Join(homeDir, ".kodelet", "steer")

	// Ensure steer directory exists
	if err := os.MkdirAll(steerDir, 0o755); err != nil {
		return nil, errors.Wrap(err, "failed to create steer directory")
	}

	return &Store{
		steerDir: steerDir,
	}, nil
}

// getSteerPath returns the path to the steer file for a conversation
func (s *Store) getSteerPath(conversationID string) string {
	return filepath.Join(s.steerDir, fmt.Sprintf("steer-%s.json", conversationID))
}

// WriteSteer writes a steering message to the steer file
func (s *Store) WriteSteer(conversationID, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	filePath := s.getSteerPath(conversationID)

	return lockedfile.Transform(filePath, func(data []byte) ([]byte, error) {
		// Parse existing steer data
		steerData := &Data{Messages: []Message{}}
		if len(data) > 0 {
			if err := json.Unmarshal(data, steerData); err != nil {
				logger.G(nil).WithError(err).Warn("failed to unmarshal existing steer data, creating new")
				steerData = &Data{Messages: []Message{}}
			}
		}

		// Append new message
		newMessage := Message{
			Role:      "user",
			Content:   message,
			Timestamp: time.Now(),
		}
		steerData.Messages = append(steerData.Messages, newMessage)

		// Marshal back to JSON
		result, err := json.MarshalIndent(steerData, "", "  ")
		if err != nil {
			return nil, errors.Wrap(err, "failed to marshal steer data")
		}

		return result, nil
	})
}

// ReadPendingSteer reads and returns pending steering messages
func (s *Store) ReadPendingSteer(conversationID string) ([]Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	filePath := s.getSteerPath(conversationID)

	// Check if steer file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return []Message{}, nil
	}

	// Read steer data with locked file
	data, err := lockedfile.Read(filePath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read steer file")
	}

	var steerData Data
	if err := json.Unmarshal(data, &steerData); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal steer data")
	}

	return steerData.Messages, nil
}

// ClearPendingSteer clears all pending steering messages
func (s *Store) ClearPendingSteer(conversationID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	filePath := s.getSteerPath(conversationID)

	// Remove the steer file (os.Remove is atomic)
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "failed to remove steer file")
	}
	return nil
}

// HasPendingSteer checks if there are pending steering messages
func (s *Store) HasPendingSteer(conversationID string) bool {
	filePath := s.getSteerPath(conversationID)

	// Check if steer file exists and has content
	if info, err := os.Stat(filePath); err == nil && info.Size() > 0 {
		return true
	}

	return false
}

// ListSteerFiles returns all steer files for debugging
func (s *Store) ListSteerFiles() ([]string, error) {
	entries, err := os.ReadDir(s.steerDir)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read steer directory")
	}

	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			files = append(files, entry.Name())
		}
	}

	return files, nil
}
