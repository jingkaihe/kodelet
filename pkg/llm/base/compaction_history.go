package base

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/logger"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
)

// ArchiveCompactionLocked preserves the visible tail before replacing model context.
// The caller must hold Mu and the provider's history lock, when applicable.
func (t *Thread) ArchiveCompactionLocked(raw json.RawMessage, replacementCount int, method, summary string) error {
	if method == "summary" && strings.TrimSpace(summary) == "" {
		return errors.New("compact summary is empty")
	}
	marker := llmtypes.CompactionMarker{
		ID:        convtypes.GenerateID(),
		Method:    method,
		CreatedAt: time.Now().UTC(),
	}
	if method == "summary" {
		marker.Summary = summary
	}
	history, err := t.CompactionHistory.Append(raw, replacementCount, marker)
	if err != nil {
		return err
	}
	t.CompactionHistory = history
	return nil
}

// GetCompactionHistory returns an isolated display archive.
func (t *Thread) GetCompactionHistory() *convtypes.CompactionHistory {
	t.Mu.Lock()
	defer t.Mu.Unlock()
	return t.CompactionHistory.Clone()
}

// SetCompactionHistory restores an isolated display archive.
func (t *Thread) SetCompactionHistory(history *convtypes.CompactionHistory) {
	t.Mu.Lock()
	defer t.Mu.Unlock()
	t.CompactionHistory = history.Clone()
}

// CompactionMarkerID identifies the latest installed display checkpoint.
func (t *Thread) CompactionMarkerID() string {
	t.Mu.Lock()
	defer t.Mu.Unlock()
	if t.CompactionHistory == nil || len(t.CompactionHistory.Segments) == 0 {
		return ""
	}
	return t.CompactionHistory.Segments[len(t.CompactionHistory.Segments)-1].Marker.ID
}

// PublishCompaction attempts to save an installed checkpoint before announcing it.
// Persistence failures must not hide the in-memory compaction or abort the turn;
// the regular conversation saves can retry persisting the same archive later.
// Providers call this after admitting incoming input, and never for no-save turns.
func (t *Thread) PublishCompaction(ctx context.Context, provider llmtypes.Thread, handler llmtypes.MessageHandler, previousMarkerID string, beforeCurrentUser bool) {
	t.Mu.Lock()
	if t.CompactionHistory == nil || len(t.CompactionHistory.Segments) == 0 {
		t.Mu.Unlock()
		return
	}
	marker := t.CompactionHistory.Segments[len(t.CompactionHistory.Segments)-1].Marker
	t.Mu.Unlock()
	if marker.ID == previousMarkerID {
		return
	}
	if err := provider.SaveConversation(context.Background()); err != nil {
		logger.G(ctx).WithError(err).Warn("failed to save compacted context")
	}
	if receiver, ok := handler.(llmtypes.CompactionMessageHandler); ok {
		receiver.HandleCompaction(marker, beforeCurrentUser)
	}
}
