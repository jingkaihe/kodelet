package feedback

import (
	"context"
	"time"

	"github.com/jingkaihe/kodelet/pkg/logger"
)

// CleanupService manages periodic cleanup of old feedback files
type CleanupService struct {
	store   *FeedbackStore
	maxAge  time.Duration
	running bool
	stopCh  chan struct{}
}

// NewCleanupService creates a new cleanup service
func NewCleanupService(maxAge time.Duration) (*CleanupService, error) {
	store, err := NewFeedbackStore()
	if err != nil {
		return nil, err
	}

	return &CleanupService{
		store:   store,
		maxAge:  maxAge,
		running: false,
		stopCh:  make(chan struct{}),
	}, nil
}

// Start starts the cleanup service with periodic cleanup
func (cs *CleanupService) Start(ctx context.Context, interval time.Duration) {
	if cs.running {
		return
	}

	cs.running = true
	go cs.cleanupLoop(ctx, interval)
}

// Stop stops the cleanup service
func (cs *CleanupService) Stop() {
	if !cs.running {
		return
	}

	cs.running = false
	close(cs.stopCh)
}

// cleanupLoop runs the cleanup process at regular intervals
func (cs *CleanupService) cleanupLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.G(ctx).Debug("feedback cleanup service stopped due to context cancellation")
			return
		case <-cs.stopCh:
			logger.G(ctx).Debug("feedback cleanup service stopped")
			return
		case <-ticker.C:
			if err := cs.cleanup(ctx); err != nil {
				logger.G(ctx).WithError(err).Warn("failed to cleanup old feedback files")
			}
		}
	}
}

// cleanup performs the actual cleanup of old feedback files
func (cs *CleanupService) cleanup(ctx context.Context) error {
	logger.G(ctx).Debug("starting feedback cleanup")

	err := cs.store.CleanupOldFeedback(cs.maxAge)
	if err != nil {
		return err
	}

	logger.G(ctx).Debug("feedback cleanup completed")
	return nil
}

// CleanupOnce performs a one-time cleanup of old feedback files
func CleanupOnce(ctx context.Context, maxAge time.Duration) error {
	store, err := NewFeedbackStore()
	if err != nil {
		return err
	}

	logger.G(ctx).WithField("max_age", maxAge).Debug("performing one-time feedback cleanup")
	
	err = store.CleanupOldFeedback(maxAge)
	if err != nil {
		return err
	}

	logger.G(ctx).Debug("one-time feedback cleanup completed")
	return nil
}

// GetFeedbackStats returns statistics about feedback files
func GetFeedbackStats(ctx context.Context) (map[string]interface{}, error) {
	store, err := NewFeedbackStore()
	if err != nil {
		return nil, err
	}

	files, err := store.ListFeedbackFiles()
	if err != nil {
		return nil, err
	}

	stats := map[string]interface{}{
		"total_files":  len(files),
		"files":        files,
		"storage_path": store.feedbackDir,
	}

	return stats, nil
}