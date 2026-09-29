package searchindex

import (
	"context"
	"time"

	"github.com/pkg/errors"

	"github.com/jingkaihe/kodelet/pkg/logger"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
)

const (
	DefaultRefreshBudget = 250 * time.Millisecond
	DefaultSweepInterval = 30 * time.Second
)

type Store interface {
	PendingSearchIndex(ctx context.Context, version int) ([]convtypes.SearchIndexCandidate, error)
	LoadSearchSource(ctx context.Context, id string) (convtypes.ConversationRecord, error)
	ReplaceSearchIndex(ctx context.Context, document convtypes.SearchDocument) (bool, error)
	PruneSearchIndex(ctx context.Context) (int, error)
}

// Indexer detects changes by comparing saved and indexed update times,
// without save-path hooks.
type Indexer struct {
	store         Store
	refreshBudget time.Duration
	sweepInterval time.Duration

	// lock serializes indexing, so a search refresh and the background loop
	// never extract or write the same conversation concurrently. It is a
	// channel so waits can give up at a deadline or on cancellation.
	lock chan struct{}
	// Source update times let overlapping passes skip work already done.
	indexed map[string]time.Time
}

type Option func(*Indexer)

func WithRefreshBudget(budget time.Duration) Option {
	return func(indexer *Indexer) { indexer.refreshBudget = budget }
}

func WithSweepInterval(interval time.Duration) Option {
	return func(indexer *Indexer) { indexer.sweepInterval = interval }
}

func New(store Store, options ...Option) *Indexer {
	indexer := &Indexer{
		store:         store,
		refreshBudget: DefaultRefreshBudget,
		sweepInterval: DefaultSweepInterval,
		lock:          make(chan struct{}, 1),
		indexed:       make(map[string]time.Time),
	}
	for _, option := range options {
		option(indexer)
	}
	return indexer
}

// Refresh returns the pending count after a budgeted pass, newest first.
// Waiting for background indexing consumes the budget; once started, a
// conversation finishes even if the budget expires.
func (i *Indexer) Refresh(ctx context.Context) (int, error) {
	result, err := i.sync(ctx, time.Now().Add(i.refreshBudget))
	return result.pending, err
}

func (i *Indexer) Run(ctx context.Context) {
	ticker := time.NewTicker(i.sweepInterval)
	defer ticker.Stop()
	for first := true; ; first = false {
		i.sweep(ctx, first)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (i *Indexer) sweep(ctx context.Context, backfill bool) {
	log := logger.G(ctx)
	if pruned, err := i.store.PruneSearchIndex(ctx); err != nil {
		if ctx.Err() == nil {
			log.WithError(err).Warn("failed to remove deleted conversations from the search index")
		}
	} else if pruned > 0 {
		log.WithField("count", pruned).Debug("removed deleted conversations from the search index")
	}

	start := time.Now()
	result, err := i.sync(ctx, time.Time{})
	if err != nil && ctx.Err() == nil {
		log.WithError(err).Warn("failed to index conversations for search; retrying on the next sweep")
	}
	if result.indexed == 0 {
		return
	}
	entry := log.WithField("count", result.indexed).WithField("duration", time.Since(start).Round(time.Millisecond))
	if backfill {
		entry.Info("indexed conversations for search")
	} else {
		entry.Debug("indexed conversations for search")
	}
}

type syncResult struct {
	indexed int
	pending int
}

// sync makes one pass over the pending conversations. A zero deadline means
// the pass is unbounded. Each conversation is attempted at most once per
// pass, so a conversation that keeps changing cannot stall it.
func (i *Indexer) sync(ctx context.Context, deadline time.Time) (syncResult, error) {
	candidates, err := i.store.PendingSearchIndex(ctx, Version)
	if err != nil {
		return syncResult{}, err
	}
	result := syncResult{pending: len(candidates)}
	for _, candidate := range candidates {
		if !i.acquire(ctx, deadline) {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			break
		}
		done, wrote, err := i.index(ctx, candidate)
		<-i.lock
		if err != nil {
			return result, err
		}
		if done {
			result.pending--
		}
		if wrote {
			result.indexed++
		}
	}
	return result, nil
}

func (i *Indexer) acquire(ctx context.Context, deadline time.Time) bool {
	var expired <-chan time.Time
	if !deadline.IsZero() {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false
		}
		timer := time.NewTimer(remaining)
		defer timer.Stop()
		expired = timer.C
	}
	select {
	case i.lock <- struct{}{}:
	case <-ctx.Done():
		return false
	case <-expired:
		return false
	}
	// The lock can be won as the deadline passes or ctx ends; do not start
	// work that the caller has no time left for.
	if ctx.Err() != nil || (!deadline.IsZero() && !time.Now().Before(deadline)) {
		<-i.lock
		return false
	}
	return true
}

// index reports whether the candidate no longer needs indexing and whether
// this call wrote it. The caller must hold the indexing lock.
func (i *Indexer) index(ctx context.Context, candidate convtypes.SearchIndexCandidate) (bool, bool, error) {
	if indexedAt, ok := i.indexed[candidate.ID]; ok && indexedAt.Equal(candidate.UpdatedAt) {
		return true, false, nil
	}
	record, err := i.store.LoadSearchSource(ctx, candidate.ID)
	if errors.Is(err, convtypes.ErrConversationNotFound) {
		delete(i.indexed, candidate.ID)
		return true, false, nil
	}
	if err != nil {
		return false, false, err
	}
	document, err := BuildDocument(record)
	if err != nil {
		// Keep the title entry and record the version, so an unreadable
		// transcript is retried only after the conversation changes.
		logger.G(ctx).WithError(err).WithField("conversation_id", candidate.ID).
			Warn("indexed only the title of a conversation whose transcript could not be read")
	}
	wrote, err := i.store.ReplaceSearchIndex(ctx, document)
	if err != nil {
		return false, false, err
	}
	if wrote {
		i.indexed[candidate.ID] = record.UpdatedAt
	}
	return wrote, wrote, nil
}
