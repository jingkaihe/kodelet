package base

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/logger"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
)

// UserMessageSourcesMetadataKey maps display-history raw user ordinals to their
// delegated source IDs. Values are replaced, never mutated, when recording input.
const UserMessageSourcesMetadataKey = "delegated_user_sources"

// RecordUserMessageSource identifies the last appended raw user item without
// changing inference input. Ordinals span the display archive, so compaction
// cannot accidentally hide a later typed message with the same text.
// The caller must own the provider history (historyMu for Responses).
func (t *Thread) RecordUserMessageSource(ctx context.Context, sourceID string, current any) {
	t.Mu.Lock()
	defer t.Mu.Unlock()
	const key = UserMessageSourcesMetadataKey
	if sourceID == "" && t.Metadata[key] == nil {
		return
	}
	raw, err := json.Marshal(current)
	if err != nil {
		logger.G(ctx).WithError(err).Warn("failed to record user message provenance")
		return
	}
	visible, err := t.CompactionHistory.VisibleMessages(raw)
	if err != nil {
		logger.G(ctx).WithError(err).Warn("failed to read user message provenance boundary")
		return
	}
	countUsers := func(data json.RawMessage) int {
		var items []struct {
			Role string `json:"role"`
		}
		if json.Unmarshal(data, &items) != nil {
			return 0
		}
		count := 0
		for _, item := range items {
			if item.Role == "user" {
				count++
			}
		}
		return count
	}
	visibleUsers := countUsers(visible)
	if visibleUsers == 0 {
		// A replacement/bootstrap user item is not part of display history;
		// never overwrite the last archived user's source (or ordinal -1).
		return
	}
	ordinal := visibleUsers - 1
	if t.CompactionHistory != nil {
		for _, segment := range t.CompactionHistory.Segments {
			ordinal += countUsers(segment.RawMessages)
		}
	}
	sources := make(map[string]string)
	switch previous := t.Metadata[key].(type) {
	case map[string]string:
		for id, source := range previous {
			sources[id] = source
		}
	case map[string]any:
		for id, value := range previous {
			if source, ok := value.(string); ok {
				sources[id] = source
			}
		}
	}
	if sourceID == "" {
		delete(sources, strconv.Itoa(ordinal))
	} else {
		sources[strconv.Itoa(ordinal)] = sourceID
	}
	if t.Metadata == nil {
		t.Metadata = make(map[string]any)
	}
	t.Metadata[key] = sources
}

// RestoreUserMessageSources restores only provenance when temporary history is
// rolled back, leaving name, usage, and other independently updated metadata alone.
func (t *Thread) RestoreUserMessageSources(sources any) {
	t.Mu.Lock()
	defer t.Mu.Unlock()
	if sources == nil {
		delete(t.Metadata, UserMessageSourcesMetadataKey)
		return
	}
	if t.Metadata == nil {
		t.Metadata = make(map[string]any)
	}
	t.Metadata[UserMessageSourcesMetadataKey] = sources
}

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
