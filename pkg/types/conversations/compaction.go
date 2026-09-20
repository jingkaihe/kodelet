package conversations

import (
	"encoding/json"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
)

// CompactionHistory preserves display history independently of active model input.
type CompactionHistory struct {
	Segments           []CompactedSegment `json:"segments"`
	ActiveDisplayStart int                `json:"activeDisplayStart"`
}

// CompactedSegment contains only the visible tail preceding its completed marker.
type CompactedSegment struct {
	RawMessages json.RawMessage           `json:"rawMessages"`
	Marker      llmtypes.CompactionMarker `json:"marker"`
}

// Clone returns an isolated archive, including the underlying JSON bytes.
func (h *CompactionHistory) Clone() *CompactionHistory {
	if h == nil {
		return nil
	}
	clone := &CompactionHistory{
		ActiveDisplayStart: h.ActiveDisplayStart,
		Segments:           make([]CompactedSegment, len(h.Segments)),
	}
	for i, segment := range h.Segments {
		clone.Segments[i] = segment
		clone.Segments[i].RawMessages = append(json.RawMessage(nil), segment.RawMessages...)
	}
	return clone
}

// Validate checks the persisted-item boundary without changing inference input.
func (h *CompactionHistory) Validate(raw json.RawMessage) error {
	if h == nil {
		return nil
	}
	_, err := h.VisibleMessages(raw)
	return err
}

// VisibleMessages excludes replacement/bootstrap items before provider normalization.
func (h *CompactionHistory) VisibleMessages(raw json.RawMessage) (json.RawMessage, error) {
	if h == nil {
		return raw, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, errors.Wrap(err, "failed to decode active compaction history")
	}
	if h.ActiveDisplayStart < 0 || h.ActiveDisplayStart > len(items) {
		return nil, errors.Errorf("invalid compaction display boundary %d for %d items", h.ActiveDisplayStart, len(items))
	}
	return json.Marshal(items[h.ActiveDisplayStart:])
}

// Append returns a new archive; the receiver remains unchanged on success or failure.
func (h *CompactionHistory) Append(raw json.RawMessage, replacementCount int, marker llmtypes.CompactionMarker) (*CompactionHistory, error) {
	if replacementCount < 1 {
		return nil, errors.New("compaction replacement must not be empty")
	}
	visible, err := h.VisibleMessages(raw)
	if err != nil {
		return nil, err
	}
	var items []json.RawMessage
	if err := json.Unmarshal(visible, &items); err != nil {
		return nil, errors.Wrap(err, "failed to decode archived compaction history")
	}
	result := h.Clone()
	if result == nil {
		result = &CompactionHistory{}
	}
	result.Segments = append(result.Segments, CompactedSegment{
		RawMessages: append(json.RawMessage(nil), visible...),
		Marker:      marker,
	})
	result.ActiveDisplayStart = replacementCount
	return result, nil
}
