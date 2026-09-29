package searchindex

import (
	"context"
	"sync"
	"time"

	"github.com/pkg/errors"

	"github.com/jingkaihe/kodelet/pkg/logger"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
)

const (
	// DefaultRefreshBudget bounds how long a search waits for recently changed
	// conversations to be indexed before querying.
	DefaultRefreshBudget = 250 * time.Millisecond
	// DefaultSweepInterval is how often the background loop indexes changes,
	// including those saved by other processes.
	DefaultSweepInterval = 30 * time.Second
)

// Store is the persistence maintained by the indexer.
type Store interface {
	PendingSearchIndex(ctx context.Context, version int) ([]convtypes.SearchIndexCandidate, error)
	LoadSearchSource(ctx context.Context, id string) (convtypes.ConversationRecord, error)
	ReplaceSearchIndex(ctx context.Context, document convtypes.SearchDocument) (bool, error)
	PruneSearchIndex(ctx context.Context) (int, error)
}

// Indexer keeps the conversation search index current. Changed conversations
// are found by comparing saved and indexed update times, so the save path
// needs no hooks: searches catch up on recent changes within a short budget,
// and a background loop handles the initial backfill and periodic sweeps.
type Indexer struct {
	store         Store
	refreshBudget time.Duration
	sweepInterval time.Duration

	// mu serializes indexing, so a search refresh and the background loop
	// never extract or write the same conversation concurrently.
	mu sync.Mutex
	// indexed records the source update time this process last indexed per
	// conversation, letting overlapping passes skip work already done.
	indexed map[string]time.Time
}

// Option configures an Indexer.
type Option func(*Indexer)

// WithRefreshBudget overrides DefaultRefreshBudget.
func WithRefreshBudget(budget time.Duration) Option {
	return func(indexer *Indexer) { indexer.refreshBudget = budget }
}

// WithSweepInterval overrides DefaultSweepInterval.
func WithSweepInterval(interval time.Duration) Option {
	return func(indexer *Indexer) { indexer.sweepInterval = interval }
}

// New creates an indexer for store.
func New(store Store, options ...Option) *Indexer {
	indexer := &Indexer{
		store:         store,
		refreshBudget: DefaultRefreshBudget,
		sweepInterval: DefaultSweepInterval,
		indexed:       make(map[string]time.Time),
	}
	for _, option := range options {
		option(indexer)
	}
	return indexer
}

// Refresh indexes changed conversations, most recently updated first, until
// they are all indexed or the refresh budget is spent. It returns how many
// conversations still await indexing.
func (i *Indexer) Refresh(ctx context.Context) (int, error) {
	result, err := i.sync(ctx, time.Now().Add(i.refreshBudget))
	return result.pending, err
}

// Run backfills the index, then sweeps for changes every sweep interval until
// ctx is cancelled.
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
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			break
		}
		done, wrote, err := i.index(ctx, candidate)
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

// index reports whether the candidate no longer needs indexing and whether
// this call wrote it.
func (i *Indexer) index(ctx context.Context, candidate convtypes.SearchIndexCandidate) (bool, bool, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	if indexedAt, ok := i.indexed[candidate.ID]; ok && indexedAt.Equal(candidate.UpdatedAt) {
		return true, false, nil
	}
	record, err := i.store.LoadSearchSource(ctx, candidate.ID)
	if errors.Is(err, convtypes.ErrConversationNotFound) {
		// Deleted since listing; Store.Delete already removed its entries.
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
