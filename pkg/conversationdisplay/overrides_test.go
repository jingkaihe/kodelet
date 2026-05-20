package conversationdisplay

import (
	"testing"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddSlashCommandOverrideAndLookup(t *testing.T) {
	metadata := AddSlashCommandOverride(nil, "full recipe prompt", "/init focus", "init")

	override, ok := Lookup(metadata, "full recipe prompt")
	require.True(t, ok)
	assert.Equal(t, "/init focus", override.Display)
	assert.Equal(t, KindSlash, override.Kind)
	assert.Equal(t, "init", override.Command)

	_, ok = Lookup(metadata, "different prompt")
	assert.False(t, ok)
}

func TestApplyToStreamableMessages(t *testing.T) {
	metadata := AddSlashCommandOverride(nil, "full recipe prompt", "/init focus", "init")
	messages := []conversations.StreamableMessage{
		{Kind: "text", Role: "user", Content: "full recipe prompt"},
		{Kind: "text", Role: "assistant", Content: "full recipe prompt"},
		{Kind: "tool-use", Role: "assistant", Input: "{}"},
	}

	got := ApplyToStreamableMessages(messages, metadata)
	assert.Equal(t, "/init focus", got[0].Content)
	assert.Equal(t, "full recipe prompt", got[1].Content)
	assert.Equal(t, "{}", got[2].Input)
	assert.Equal(t, "full recipe prompt", messages[0].Content, "input slice is not mutated")
}

func TestApplyToLLMMessages(t *testing.T) {
	metadata := AddSlashCommandOverride(nil, "full recipe prompt", "/init focus", "init")
	messages := []llmtypes.Message{
		{Role: "user", Content: "full recipe prompt"},
		{Role: "assistant", Content: "full recipe prompt"},
	}

	got := ApplyToLLMMessages(messages, metadata)
	assert.Equal(t, "/init focus", got[0].Content)
	assert.Equal(t, "full recipe prompt", got[1].Content)
}
