package tools

import (
	"context"
	"errors"
	"testing"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadConversationToolValidateInput(t *testing.T) {
	tool := NewReadConversationTool()

	err := tool.ValidateInput(nil, `{"conversation_id":"conv_123","goal":"Extract the bug fix"}`)
	require.NoError(t, err)

	err = tool.ValidateInput(nil, `{"goal":"missing id"}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "conversation_id is required")

	err = tool.ValidateInput(nil, `{"conversation_id":"conv_123"}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "goal is required")
}

func TestReadConversationToolExecuteSuccess(t *testing.T) {
	tool := &ReadConversationTool{
		renderConversation: func(_ context.Context, conversationID string) (string, error) {
			assert.Equal(t, "conv_123", conversationID)
			return "## Messages\n\n### Assistant\n\nFixed the issue in `pkg/tools/read_conversation.go`.", nil
		},
		extractContent: func(_ context.Context, _ tooltypes.State, markdown string, goal string) (string, error) {
			assert.Contains(t, markdown, "## Messages")
			assert.Equal(t, "Extract the fix", goal)
			return "Fixed the issue in `pkg/tools/read_conversation.go`.", nil
		},
	}

	state := NewBasicState(context.Background(), WithLLMConfig(llmtypes.Config{Provider: "openai", Model: "gpt-4.1"}))
	result := tool.Execute(context.Background(), state, `{"conversation_id":"conv_123","goal":"Extract the fix"}`)

	require.False(t, result.IsError())
	assert.Contains(t, result.AssistantFacing(), "Fixed the issue")

	structured := result.StructuredData()
	assert.Equal(t, "read_conversation", structured.ToolName)
	assert.True(t, structured.Success)

	var meta tooltypes.ReadConversationMetadata
	require.True(t, tooltypes.ExtractMetadata(structured.Metadata, &meta))
	assert.Equal(t, "conv_123", meta.ConversationID)
	assert.Equal(t, "Extract the fix", meta.Goal)
	assert.Equal(t, "Fixed the issue in `pkg/tools/read_conversation.go`.", meta.Content)
}

func TestReadConversationToolExecuteRenderError(t *testing.T) {
	tool := &ReadConversationTool{
		renderConversation: func(_ context.Context, _ string) (string, error) {
			return "", errors.New("conversation not found")
		},
		extractContent: func(_ context.Context, _ tooltypes.State, _ string, _ string) (string, error) {
			t.Fatal("extractContent should not be called on render error")
			return "", nil
		},
	}

	result := tool.Execute(context.Background(), NewBasicState(context.Background()), `{"conversation_id":"missing","goal":"Extract the fix"}`)

	require.True(t, result.IsError())
	assert.Contains(t, result.GetError(), "Failed to render conversation")
}

func TestParseReadConversationResponse(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "plain json",
			input:    `{"relevantContent":"hello"}`,
			expected: "hello",
		},
		{
			name:     "fenced json",
			input:    "```json\n{\"relevantContent\":\"hello\"}\n```",
			expected: "hello",
		},
		{
			name:     "fallback raw markdown",
			input:    "### Assistant\n\nHere is the extracted content.",
			expected: "### Assistant\n\nHere is the extracted content.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content, err := parseReadConversationResponse(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, content)
		})
	}
}
