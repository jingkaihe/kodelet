package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestThinkingTool_Name(t *testing.T) {
	tool := &ThinkingTool{}
	assert.Equal(t, "thinking", tool.Name())
}

func TestThinkingTool_GenerateSchema(t *testing.T) {
	tool := &ThinkingTool{}
	schema := tool.GenerateSchema()
	assert.NotNil(t, schema)
	assert.Equal(t, "object", schema.Type)
}

func TestThinkingTool_Description(t *testing.T) {
	tool := &ThinkingTool{}
	description := tool.Description()
	assert.NotEmpty(t, description)
}

func TestThinkingTool_Execute(t *testing.T) {
	tool := &ThinkingTool{}
	state := NewBasicState()

	result := tool.Execute(context.Background(), state, `{"thought": "Test thought"}`)

	assert.Equal(t, "Your thought have been recorded.", result.Result)
	assert.Empty(t, result.Error)
}

func TestThinkingTool_ValidateInput(t *testing.T) {
	tool := &ThinkingTool{}
	state := NewBasicState()

	tests := []struct {
		name       string
		input      string
		wantErrMsg string
	}{
		{
			name:       "valid input",
			input:      `{"thought": "Valid thought"}`,
			wantErrMsg: "",
		},
		{
			name:       "invalid JSON",
			input:      `{"thought": "Invalid JSON"`,
			wantErrMsg: "invalid input",
		},
		{
			name:       "empty thought",
			input:      `{"thought": ""}`,
			wantErrMsg: "thought is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tool.ValidateInput(state, tt.input)
			if tt.wantErrMsg == "" {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrMsg)
			}
		})
	}
}
