package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestThinkingTool_GenerateSchema(t *testing.T) {
	tool := &ThinkingTool{}
	schema := tool.GenerateSchema()
	assert.NotNil(t, schema)
	assert.Equal(t, "object", schema.Type)
	assert.NotNil(t, schema.Properties, "Schema should have properties")
	
	// Verify the schema has a thought property
	thoughtProp, ok := schema.Properties.Get("thought")
	assert.True(t, ok, "Schema should have a 'thought' property")
	assert.Equal(t, "string", thoughtProp.Type, "Thought property should be a string")
	assert.NotEmpty(t, thoughtProp.Description, "Thought property should have a description")
	
	// Verify required fields
	assert.Contains(t, schema.Required, "thought", "Thought should be a required field")
}

func TestThinkingTool_Description(t *testing.T) {
	tool := &ThinkingTool{}
	description := tool.Description()
	assert.NotEmpty(t, description)
	
	// Verify the description contains key information
	assert.Contains(t, description, "think", "Description should mention thinking")
	assert.Contains(t, description, "reasoning", "Description should mention reasoning or complex thought")
}

func TestThinkingTool_Execute(t *testing.T) {
	tool := &ThinkingTool{}
	state := NewBasicState(context.TODO())

	result := tool.Execute(context.Background(), state, `{"thought": "Test thought"}`)

	assert.Equal(t, "Your thought have been recorded.", result.GetResult())
	assert.False(t, result.IsError())
}

func TestThinkingTool_ValidateInput(t *testing.T) {
	tool := &ThinkingTool{}
	state := NewBasicState(context.TODO())

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
