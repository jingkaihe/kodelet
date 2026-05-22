package conversations

import (
	"testing"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddSlashCommandDisplayAndLookup(t *testing.T) {
	metadata := AddSlashCommandDisplay(nil, "full recipe prompt", "/init focus", "init")

	display, ok := LookupMessageDisplay(metadata, "full recipe prompt")
	require.True(t, ok)
	assert.Equal(t, "/init focus", display.Text)
	assert.Equal(t, MessageDisplayKindSlashCommand, display.Kind)
	assert.Equal(t, "init", display.Command)
	assert.Contains(t, metadata, MessageDisplayMetadataKey)

	_, ok = LookupMessageDisplay(metadata, "different prompt")
	assert.False(t, ok)
}

func TestAddMessageDisplayGoalAndLookup(t *testing.T) {
	metadata := AddMessageDisplay(nil, "Objective: find cores", "/goal find cores", MessageDisplayKindGoal, "goal")

	display, ok := LookupMessageDisplay(metadata, "Objective: find cores")
	require.True(t, ok)
	assert.Equal(t, "/goal find cores", display.Text)
	assert.Equal(t, MessageDisplayKindGoal, display.Kind)
	assert.Equal(t, "goal", display.Command)
}

func TestLookupMessageDisplayReadsLegacyMetadata(t *testing.T) {
	key := MessageDisplayKey("full recipe prompt")
	metadata := map[string]any{
		legacyMessageDisplayMetadataKey: map[string]any{
			MessageDisplayVersion: map[string]any{
				key: map[string]any{
					"display": "/init focus",
					"kind":    MessageDisplayKindSlashCommand,
					"command": "init",
				},
			},
		},
	}

	display, ok := LookupMessageDisplay(metadata, "full recipe prompt")
	require.True(t, ok)
	assert.Equal(t, "/init focus", display.Text)
	assert.Equal(t, MessageDisplayKindSlashCommand, display.Kind)
}

func TestAddSlashCommandDisplayPreservesLegacyMetadata(t *testing.T) {
	legacyKey := MessageDisplayKey("legacy prompt")
	metadata := map[string]any{
		legacyMessageDisplayMetadataKey: map[string]any{
			MessageDisplayVersion: map[string]any{
				legacyKey: map[string]any{"display": "/legacy"},
			},
		},
	}

	metadata = AddSlashCommandDisplay(metadata, "new prompt", "/new", "new")

	_, ok := metadata[legacyMessageDisplayMetadataKey]
	assert.False(t, ok)
	display, ok := LookupMessageDisplay(metadata, "legacy prompt")
	require.True(t, ok)
	assert.Equal(t, "/legacy", display.Text)
	display, ok = LookupMessageDisplay(metadata, "new prompt")
	require.True(t, ok)
	assert.Equal(t, "/new", display.Text)
}

func TestApplyDisplayToStreamableMessages(t *testing.T) {
	metadata := AddSlashCommandDisplay(nil, "full recipe prompt", "/init focus", "init")
	messages := []StreamableMessage{
		{Kind: "text", Role: "user", Content: "full recipe prompt"},
		{Kind: "text", Role: "assistant", Content: "full recipe prompt"},
		{Kind: "tool-use", Role: "assistant", Input: "{}"},
	}

	got := ApplyDisplayToStreamableMessages(messages, metadata)
	require.Len(t, got, 3)
	assert.Equal(t, "/init focus", got[0].Content)
	assert.Equal(t, "full recipe prompt", got[1].Content)
	assert.Equal(t, "{}", got[2].Input)
	assert.Equal(t, "full recipe prompt", messages[0].Content, "input slice is not mutated")
}

func TestApplyDisplayToLLMMessages(t *testing.T) {
	metadata := AddSlashCommandDisplay(nil, "full recipe prompt", "/init focus", "init")
	messages := []llmtypes.Message{
		{Role: "user", Content: "full recipe prompt"},
		{Role: "assistant", Content: "full recipe prompt"},
	}

	got := ApplyDisplayToLLMMessages(messages, metadata)
	require.Len(t, got, 2)
	assert.Equal(t, "/init focus", got[0].Content)
	assert.Equal(t, "full recipe prompt", got[1].Content)
}

func TestApplyDisplayHidesGoalContextMessages(t *testing.T) {
	goalContext := "<goal_context>\nContinue working.\n</goal_context>"
	streamable := ApplyDisplayToStreamableMessages([]StreamableMessage{{Kind: "text", Role: "user", Content: goalContext}}, nil)
	assert.Empty(t, streamable)

	llmMessages := ApplyDisplayToLLMMessages([]llmtypes.Message{{Role: "user", Content: goalContext}}, nil)
	assert.Empty(t, llmMessages)
}

func TestApplyDisplayConsumesGoalContextOverrideOnce(t *testing.T) {
	goalContext := "<goal_context>\nContinue working.\n</goal_context>"
	metadata := AddMessageDisplay(nil, goalContext, "Objective: find cores", MessageDisplayKindGoal, "goal")

	streamable := ApplyDisplayToStreamableMessages([]StreamableMessage{
		{Kind: "text", Role: "user", Content: goalContext},
		{Kind: "text", Role: "user", Content: goalContext},
	}, metadata)
	require.Len(t, streamable, 1)
	assert.Equal(t, "Objective: find cores", streamable[0].Content)

	llmMessages := ApplyDisplayToLLMMessages([]llmtypes.Message{
		{Role: "user", Content: goalContext},
		{Role: "user", Content: goalContext},
	}, metadata)
	require.Len(t, llmMessages, 1)
	assert.Equal(t, "Objective: find cores", llmMessages[0].Content)
}
