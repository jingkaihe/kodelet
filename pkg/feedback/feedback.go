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
	
	// Create a file lock by creating a temporary lock file
	lockPath := filePath + ".lock"
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		if os.IsExist(err) {
			// Lock file exists, wait and retry
			time.Sleep(100 * time.Millisecond)
			return fs.WriteFeedback(conversationID, message)
		}
		return errors.Wrap(err, "failed to create lock file")
	}
	defer func() {
		lockFile.Close()
		os.Remove(lockPath)
	}()

	// Read existing feedback data
	feedbackData := &FeedbackData{Messages: []Message{}}
	if data, err := os.ReadFile(filePath); err == nil {
		if err := json.Unmarshal(data, feedbackData); err != nil {
			logger.G(nil).WithError(err).Warn("failed to unmarshal existing feedback data, creating new")
			feedbackData = &FeedbackData{Messages: []Message{}}
		}
	} else if !os.IsNotExist(err) {
		return errors.Wrap(err, "failed to read existing feedback file")
	}

	// Append new message
	newMessage := Message{
		Role:      "user",
		Content:   message,
		Timestamp: time.Now(),
	}
	feedbackData.Messages = append(feedbackData.Messages, newMessage)

	// Write back to file
	data, err := json.MarshalIndent(feedbackData, "", "  ")
	if err != nil {
		return errors.Wrap(err, "failed to marshal feedback data")
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return errors.Wrap(err, "failed to write feedback file")
	}

	return nil
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

	// Create a file lock by creating a temporary lock file
	lockPath := filePath + ".lock"
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		if os.IsExist(err) {
			// Lock file exists, wait and retry
			time.Sleep(100 * time.Millisecond)
			return fs.ReadPendingFeedback(conversationID)
		}
		return nil, errors.Wrap(err, "failed to create lock file")
	}
	defer func() {
		lockFile.Close()
		os.Remove(lockPath)
	}()

	// Read feedback data
	data, err := os.ReadFile(filePath)
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
	
	// Check if feedback file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return nil // Nothing to clear
	}

	// Create a file lock by creating a temporary lock file
	lockPath := filePath + ".lock"
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		if os.IsExist(err) {
			// Lock file exists, wait and retry
			time.Sleep(100 * time.Millisecond)
			return fs.ClearPendingFeedback(conversationID)
		}
		return errors.Wrap(err, "failed to create lock file")
	}
	defer func() {
		lockFile.Close()
		os.Remove(lockPath)
	}()

	// Remove the feedback file
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

// CleanupOldFeedback removes feedback files older than the specified duration
func (fs *FeedbackStore) CleanupOldFeedback(maxAge time.Duration) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	entries, err := os.ReadDir(fs.feedbackDir)
	if err != nil {
		return errors.Wrap(err, "failed to read feedback directory")
	}

	cutoff := time.Now().Add(-maxAge)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		filePath := filepath.Join(fs.feedbackDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}

		if info.ModTime().Before(cutoff) {
			if err := os.Remove(filePath); err != nil {
				logger.G(nil).WithError(err).Warnf("failed to remove old feedback file: %s", filePath)
			}
		}
	}

	return nil
}