package conversations

import (
	"context"

	"github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/pkg/errors"
)

// ConversationStore defines the interface for conversation persistence
type ConversationStore interface {
	// Basic CRUD operations
	Save(ctx context.Context, record conversations.ConversationRecord) error
	Load(ctx context.Context, id string) (conversations.ConversationRecord, error)
	Delete(ctx context.Context, id string) error

	// Advanced query operations
	Query(ctx context.Context, options conversations.QueryOptions) (conversations.QueryResult, error)

	// Lifecycle methods
	Close() error // Close doesn't need context
}

// AtomicConversationForkStore persists a fork and copies any durable runner
// affinity in the same transaction.
type AtomicConversationForkStore interface {
	SaveConversationFork(ctx context.Context, sourceConversationID string, forked conversations.ConversationRecord) error
}

// ConversationRunnerAffinityStore is an optional store capability for
// transferring authoritative runner affinity to a new conversation ID.
type ConversationRunnerAffinityStore interface {
	ConversationRunnerAffinity(ctx context.Context, conversationID string) (runnerID, environmentProfile string, ok bool, err error)
	BindConversationRunnerAffinity(ctx context.Context, conversationID, runnerID, environmentProfile string) error
}

// PersistConversationFork saves an isolated fork and transfers durable runner
// affinity when the store supports it. Identity-bound runner metadata remains
// excluded from the copied record itself.
func PersistConversationFork(
	ctx context.Context,
	store ConversationStore,
	source conversations.ConversationRecord,
	options conversations.ConversationForkOptions,
) (conversations.ConversationRecord, error) {
	forked := conversations.ForkConversationRecordWithOptions(source, options)
	if name := ConversationForkNameFromContext(ctx); name != "" {
		forked.Metadata = SetConversationName(forked.Metadata, name)
		forked.Summary = name
	}
	if atomicStore, ok := store.(AtomicConversationForkStore); ok {
		if err := atomicStore.SaveConversationFork(ctx, source.ID, forked); err != nil {
			return conversations.ConversationRecord{}, errors.Wrap(err, "failed to save forked conversation")
		}
		return forked, nil
	}

	var runnerID string
	var environmentProfile string
	var hasRunnerAffinity bool
	affinityStore, supportsRunnerAffinity := store.(ConversationRunnerAffinityStore)
	if supportsRunnerAffinity {
		var err error
		runnerID, environmentProfile, hasRunnerAffinity, err = affinityStore.ConversationRunnerAffinity(ctx, source.ID)
		if err != nil {
			return conversations.ConversationRecord{}, errors.Wrap(err, "failed to load source conversation runner affinity")
		}
	}

	if err := store.Save(ctx, forked); err != nil {
		return conversations.ConversationRecord{}, errors.Wrap(err, "failed to save forked conversation")
	}

	if !hasRunnerAffinity {
		return forked, nil
	}
	if err := affinityStore.BindConversationRunnerAffinity(ctx, forked.ID, runnerID, environmentProfile); err != nil {
		cleanupErr := store.Delete(context.WithoutCancel(ctx), forked.ID)
		if cleanupErr != nil {
			return conversations.ConversationRecord{}, errors.Wrapf(err, "failed to preserve forked conversation runner affinity; cleanup failed: %v", cleanupErr)
		}
		return conversations.ConversationRecord{}, errors.Wrap(err, "failed to preserve forked conversation runner affinity")
	}

	return forked, nil
}

// Config holds configuration for the conversation store
type Config struct {
	StoreType string // "sqlite"
	BasePath  string // Base storage path
}

// DefaultConfig returns a default configuration
func DefaultConfig() (*Config, error) {
	basePath, err := conversations.GetDefaultBasePath()
	if err != nil {
		return nil, err
	}

	return &Config{
		StoreType: "sqlite", // SQLite store is now the default
		BasePath:  basePath,
	}, nil
}

// GetMostRecentConversationID returns the ID of the most recent conversation
func GetMostRecentConversationID(ctx context.Context) (string, error) {
	store, err := GetConversationStore(ctx)
	if err != nil {
		return "", err
	}
	defer store.Close()

	// Query for the most recent conversation
	options := conversations.QueryOptions{
		Limit:     1,
		Offset:    0,
		SortBy:    "updated_at",
		SortOrder: "desc",
	}

	result, err := store.Query(ctx, options)
	if err != nil {
		return "", err
	}

	summaries := result.ConversationSummaries
	if len(summaries) == 0 {
		return "", errors.New("no conversations found")
	}

	return summaries[0].ID, nil
}
