package tools

import (
	"context"
	"testing"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
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
	calls := 0
	ctx := tooltypes.ContextWithModelHelper(t.Context(), func(_ context.Context, request tooltypes.ModelHelperRequest) (string, error) {
		calls++
		assert.Equal(t, tooltypes.ModelHelperRequest{
			Operation:      tooltypes.ModelHelperReadConversationExtract,
			ConversationID: "conv_123",
			Prompt:         "Extract the fix",
		}, request)
		return "\n Fixed the issue in `pkg/tools/read_conversation.go`. \n", nil
	})
	result := NewReadConversationTool().Execute(ctx, nil, `{"conversation_id":" conv_123 ","goal":" Extract the fix "}`)

	assert.Equal(t, 1, calls)
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

func TestReadConversationToolExecuteErrors(t *testing.T) {
	for _, tt := range []struct {
		name       string
		parameters string
		absent     bool
		helperErr  error
		wantCalls  int
		wantErr    string
	}{
		{
			name:    "missing helper",
			absent:  true,
			wantErr: "AI-assisted extraction is unavailable",
		},
		{
			name:       "malformed input",
			parameters: "{",
			wantErr:    "unexpected end of JSON input",
		},
		{
			name:      "provider failure",
			helperErr: errors.New("provider unavailable"),
			wantCalls: 1,
			wantErr:   "provider unavailable",
		},
		{
			name:      "empty response",
			wantCalls: 1,
			wantErr:   "empty extraction response",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			calls := 0
			if !tt.absent {
				ctx = tooltypes.ContextWithModelHelper(ctx, func(context.Context, tooltypes.ModelHelperRequest) (string, error) {
					calls++
					return " \n", tt.helperErr
				})
			}
			parameters := tt.parameters
			if parameters == "" {
				parameters = `{"conversation_id":"conv_123","goal":"Extract the fix"}`
			}
			result := NewReadConversationTool().Execute(ctx, nil, parameters)
			require.True(t, result.IsError())
			assert.Equal(t, tt.wantCalls, calls)
			assert.Contains(t, result.GetError(), tt.wantErr)
			assert.Empty(t, result.GetResult())
			assert.False(t, result.StructuredData().Success)
		})
	}
}
