package conversations

import (
	"context"
	"testing"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConversationNameMetadata(t *testing.T) {
	metadata := SetConversationName(nil, "  Authentication\n cleanup  ")

	assert.Equal(t, "Authentication cleanup", ExplicitConversationName(metadata))
	assert.Equal(t, "Authentication cleanup", ResolveConversationName(metadata, "fallback"))
	assert.Equal(t, "fallback", ResolveConversationName(nil, "fallback"))
}

func TestEnsureConversationNamePersistsAutomaticFallback(t *testing.T) {
	metadata, name := EnsureConversationName(nil, "  Authentication\n cleanup  ")

	assert.Equal(t, "Authentication cleanup", name)
	assert.Equal(t, "Authentication cleanup", AutomaticConversationName(metadata))

	metadata, name = EnsureConversationName(metadata, "different fallback")
	assert.Equal(t, "Authentication cleanup", name)
	assert.Equal(t, "Authentication cleanup", AutomaticConversationName(metadata))
}

func TestExplicitConversationNameOverridesAutomaticName(t *testing.T) {
	metadata := SetAutomaticConversationName(nil, "automatic name")
	metadata = SetConversationName(metadata, "explicit name")

	assert.Equal(t, "explicit name", ResolveConversationName(metadata, "fallback"))
}

func TestSetConversationNameIgnoresBlankName(t *testing.T) {
	metadata := map[string]any{"preserve": true}

	assert.Equal(t, metadata, SetConversationName(metadata, " \n "))
	assert.NotContains(t, metadata, ConversationNameMetadataKey)
}

func TestConversationForkNameContext(t *testing.T) {
	ctx := ContextWithConversationForkName(context.Background(), "  Investigate\n fork naming  ")

	assert.Equal(t, "Investigate fork naming", ConversationForkNameFromContext(ctx))
}

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

func TestAddMessageDisplayAndLookup(t *testing.T) {
	metadata := AddMessageDisplay(nil, "Inspect project dependencies", "Review dependencies", "", "")

	display, ok := LookupMessageDisplay(metadata, "Inspect project dependencies")
	require.True(t, ok)
	assert.Equal(t, "Review dependencies", display.Text)
	assert.Empty(t, display.Kind)
	assert.Empty(t, display.Command)
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

func TestApplyDisplayPreservesMessagesWithoutOverrides(t *testing.T) {
	content := "<context>\nInspect project dependencies.\n</context>"
	streamable := []StreamableMessage{{Kind: "text", Role: "user", Content: content}}
	assert.Equal(t, streamable, ApplyDisplayToStreamableMessages(streamable, nil))

	llmMessages := []llmtypes.Message{{Role: "user", Content: content}}
	assert.Equal(t, llmMessages, ApplyDisplayToLLMMessages(llmMessages, nil))
}

func TestApplyDisplayPreservesRepeatedCommandMessages(t *testing.T) {
	prompt := "Inspect project dependencies"
	metadata := AddSlashCommandDisplay(nil, prompt, "/review dependencies", "review")

	streamable := ApplyDisplayToStreamableMessages([]StreamableMessage{
		{Kind: "text", Role: "user", Content: prompt},
		{Kind: "text", Role: "user", Content: prompt},
	}, metadata)
	require.Len(t, streamable, 2)
	assert.Equal(t, "/review dependencies", streamable[0].Content)
	assert.Equal(t, "/review dependencies", streamable[1].Content)

	llmMessages := ApplyDisplayToLLMMessages([]llmtypes.Message{
		{Role: "user", Content: prompt},
		{Role: "user", Content: prompt},
	}, metadata)
	require.Len(t, llmMessages, 2)
	assert.Equal(t, "/review dependencies", llmMessages[0].Content)
	assert.Equal(t, "/review dependencies", llmMessages[1].Content)
}

func TestAddMessageDisplayIgnoresBlankInputs(t *testing.T) {
	metadata := map[string]any{"preserve": "value"}

	assert.Equal(t, metadata, AddMessageDisplay(metadata, " ", "/cmd", MessageDisplayKindSlashCommand, "cmd"))
	assert.Equal(t, metadata, AddMessageDisplay(metadata, "expanded", " ", MessageDisplayKindSlashCommand, "cmd"))
	assert.NotContains(t, metadata, MessageDisplayMetadataKey)
}

func TestLookupMessageDisplayParsesCurrentAndLegacyShapes(t *testing.T) {
	legacyKey := MessageDisplayKey("legacy prompt")
	currentKey := MessageDisplayKey("current prompt")
	metadata := map[string]any{
		legacyMessageDisplayMetadataKey: map[string]any{
			MessageDisplayVersion: map[string]MessageDisplay{
				legacyKey: {Text: "/legacy", Kind: MessageDisplayKindSlashCommand},
			},
		},
		MessageDisplayMetadataKey: map[string]any{
			MessageDisplayVersion: map[string]any{
				currentKey: map[string]string{
					"text":    "/current",
					"kind":    MessageDisplayKindSlashCommand,
					"command": "current",
				},
				"blank":       map[string]any{"text": "ignored"},
				"unknown-raw": 123,
			},
		},
	}

	legacy, ok := LookupMessageDisplay(metadata, "legacy prompt")
	require.True(t, ok)
	assert.Equal(t, "/legacy", legacy.Text)

	current, ok := LookupMessageDisplay(metadata, "current prompt")
	require.True(t, ok)
	assert.Equal(t, "/current", current.Text)
	assert.Equal(t, MessageDisplayKindSlashCommand, current.Kind)
	assert.Equal(t, "current", current.Command)

	_, ok = LookupMessageDisplay(metadata, " ")
	assert.False(t, ok)
}

func TestLookupMessageDisplayIgnoresMalformedMetadata(t *testing.T) {
	for _, metadata := range []map[string]any{
		{MessageDisplayMetadataKey: "not-a-map"},
		{MessageDisplayMetadataKey: map[string]any{}},
		{MessageDisplayMetadataKey: map[string]any{MessageDisplayVersion: "not-a-version-map"}},
		{MessageDisplayMetadataKey: map[string]any{MessageDisplayVersion: map[string]any{MessageDisplayKey("prompt"): map[string]any{}}}},
	} {
		_, ok := LookupMessageDisplay(metadata, "prompt")
		assert.False(t, ok)
	}
}
