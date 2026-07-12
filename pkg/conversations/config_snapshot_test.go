package conversations

import (
	"testing"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigSnapshotMetadataRoundTrip(t *testing.T) {
	metadata, err := AddConfigSnapshot(map[string]any{"existing": "value"}, llmtypes.Config{
		Profile:         "work",
		Provider:        "openai",
		Model:           "gpt-5",
		ReasoningEffort: "xhigh",
		OpenAI:          &llmtypes.OpenAIConfig{APIMode: llmtypes.OpenAIAPIModeResponses},
	})
	require.NoError(t, err)
	assert.Equal(t, "value", metadata["existing"])

	snapshot, ok, err := ConfigSnapshotFromMetadata(metadata)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "work", snapshot.Profile)
	assert.Equal(t, "gpt-5", snapshot.Model)
	assert.Equal(t, "xhigh", snapshot.ReasoningEffort)
	require.NotNil(t, snapshot.OpenAI)
	assert.Equal(t, llmtypes.OpenAIAPIModeResponses, snapshot.OpenAI.APIMode)
}

func TestConfigSnapshotFromMetadataHandlesLegacyAndInvalidValues(t *testing.T) {
	snapshot, ok, err := ConfigSnapshotFromMetadata(nil)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Nil(t, snapshot)

	_, ok, err = ConfigSnapshotFromMetadata(map[string]any{
		ConfigSnapshotMetadataKey: map[string]any{"version": 99},
	})
	assert.True(t, ok)
	require.ErrorContains(t, err, "unsupported conversation config snapshot version")
}
