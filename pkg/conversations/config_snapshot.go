package conversations

import (
	"encoding/json"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
)

const ConfigSnapshotMetadataKey = "config_snapshot"

// AddConfigSnapshot adds a versioned, sanitized effective LLM configuration
// snapshot to conversation metadata.
func AddConfigSnapshot(metadata map[string]any, config llmtypes.Config) (map[string]any, error) {
	snapshot, err := llmtypes.NewConversationConfigSnapshot(config)
	if err != nil {
		return nil, err
	}

	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal conversation config snapshot")
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, errors.Wrap(err, "failed to encode conversation config snapshot metadata")
	}
	if metadata == nil {
		metadata = make(map[string]any)
	}
	metadata[ConfigSnapshotMetadataKey] = value
	return metadata, nil
}

// ConfigSnapshotFromMetadata decodes and validates a persisted configuration
// snapshot. The boolean is false for legacy conversations without a snapshot.
func ConfigSnapshotFromMetadata(metadata map[string]any) (*llmtypes.ConversationConfigSnapshot, bool, error) {
	if metadata == nil {
		return nil, false, nil
	}
	value, ok := metadata[ConfigSnapshotMetadataKey]
	if !ok || value == nil {
		return nil, false, nil
	}

	raw, err := json.Marshal(value)
	if err != nil {
		return nil, true, errors.Wrap(err, "failed to marshal persisted conversation config snapshot")
	}
	var snapshot llmtypes.ConversationConfigSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return nil, true, errors.Wrap(err, "failed to decode persisted conversation config snapshot")
	}
	if err := snapshot.Validate(); err != nil {
		return nil, true, err
	}
	return &snapshot, true, nil
}
