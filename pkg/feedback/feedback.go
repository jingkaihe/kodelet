// Package feedback provides functionality for managing user feedback messages
// for autonomous conversations in kodelet. It handles storing, loading and
// managing feedback messages with file-based persistence.
package feedback

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

// Message represents a feedback message
type Message struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

// FeedbackStore represents the feedback storage
type FeedbackStore struct {
	feedbackDir string
	mu          sync.RWMutex
}

// FeedbackData represents the structure of feedback file
type FeedbackData struct {
	Messages []Message `json:"messages"`
}

// NewFeedbackStore creates a new feedback store
func NewFeedbackStore() (*FeedbackStore, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get user home directory")
	}

	feedbackDir := filepath.Join(homeDir, ".kodelet", "feedback")

	// Ensure feedback directory exists
	if err := os.MkdirAll(feedbackDir, 0755); err != nil {
		return nil, errors.Wrap(err, "failed to create feedback directory")
	}

	return &FeedbackStore{
		feedbackDir: feedbackDir,
	}, nil
}

// getFeedbackPath returns the path to the feedback file for a conversation
func (fs *FeedbackStore) getFeedbackPath(conversationID string) string {
	return filepath.Join(fs.feedbackDir, fmt.Sprintf("feedback-%s.json", conversationID))
}

// WriteFeedback writes a feedback message to the feedback file
func (fs *FeedbackStore) WriteFeedback(conversationID, message string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	filePath := fs.getFeedbackPath(conversationID)

	return lockedfile.Transform(filePath, func(data []byte) ([]byte, error) {
		// Parse existing feedback data
		feedbackData := &FeedbackData{Messages: []Message{}}
		if len(data) > 0 {
			if err := json.Unmarshal(data, feedbackData); err != nil {
				logger.G(nil).WithError(err).Warn("failed to unmarshal existing feedback data, creating new")
				feedbackData = &FeedbackData{Messages: []Message{}}
			}
		}

		// Append new message
		newMessage := Message{
			Role:      "user",
			Content:   message,
			Timestamp: time.Now(),
		}
		feedbackData.Messages = append(feedbackData.Messages, newMessage)

		// Marshal back to JSON
		result, err := json.MarshalIndent(feedbackData, "", "  ")
		if err != nil {
			return nil, errors.Wrap(err, "failed to marshal feedback data")
		}

		return result, nil
	})
}

// ReadPendingFeedback reads and returns pending feedback messages
func (fs *FeedbackStore) ReadPendingFeedback(conversationID string) ([]Message, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	filePath := fs.getFeedbackPath(conversationID)

	// Check if feedback file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return []Message{}, nil
	}

	// Read feedback data with locked file
	data, err := lockedfile.Read(filePath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read feedback file")
	}

	var feedbackData FeedbackData
	if err := json.Unmarshal(data, &feedbackData); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal feedback data")
	}

	return feedbackData.Messages, nil
}

// ClearPendingFeedback clears all pending feedback messages
func (fs *FeedbackStore) ClearPendingFeedback(conversationID string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	filePath := fs.getFeedbackPath(conversationID)

	// Remove the feedback file (os.Remove is atomic)
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "failed to remove feedback file")
	}
	return nil
}

// HasPendingFeedback checks if there are pending feedback messages
func (fs *FeedbackStore) HasPendingFeedback(conversationID string) bool {
	filePath := fs.getFeedbackPath(conversationID)

	// Check if feedback file exists and has content
	if info, err := os.Stat(filePath); err == nil && info.Size() > 0 {
		return true
	}

	return false
}

// ListFeedbackFiles returns all feedback files for debugging
func (fs *FeedbackStore) ListFeedbackFiles() ([]string, error) {
	entries, err := os.ReadDir(fs.feedbackDir)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read feedback directory")
	}

	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			files = append(files, entry.Name())
		}
	}

	return files, nil
}
