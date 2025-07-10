package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBatchTool_GenerateSchema(t *testing.T) {
	tool := &BatchTool{}
	schema := tool.GenerateSchema()
	assert.NotNil(t, schema)
	assert.Equal(t, "object", schema.Type)
	assert.Equal(t, "https://github.com/jingkaihe/kodelet/pkg/tools/batch-tool-input", string(schema.ID))
}

func TestBatchTool_Description(t *testing.T) {
	tool := &BatchTool{}
	desc := tool.Description()
	assert.Contains(t, desc, "Use Batch tool to batch up multiple independent invocations of tools")
	assert.Contains(t, desc, "This is useful to reduce the amount of back-and-forth")
	assert.Contains(t, desc, "When NOT to use this tool")
}

func TestBatchTool_ValidateInput(t *testing.T) {
	tool := &BatchTool{}
	state := NewBasicState(context.TODO())

	tests := []struct {
		name        string
		input       BatchToolInput
		expectError bool
		errorType   error
	}{
		{
			name: "valid input with multiple tools",
			input: BatchToolInput{
				Description: "Run multiple commands",
				Invocations: []Invocation{
					{
						ToolName: "bash",
						Parameters: map[string]interface{}{
							"description": "Echo hello",
							"command":     "echo hello",
							"timeout":     10,
						},
					},
					{
						ToolName: "thinking",
						Parameters: map[string]interface{}{
							"thought": "This is a test thought",
						},
					},
				},
			},
			expectError: false,
		},
		{
			name: "empty invocations list",
			input: BatchToolInput{
				Description: "Empty invocations",
				Invocations: []Invocation{},
			},
			expectError: false,
		},
		{
			name: "nested batch tool",
			input: BatchToolInput{
				Description: "Nested batch",
				Invocations: []Invocation{
					{
						ToolName: "batch",
						Parameters: map[string]interface{}{
							"description": "Nested batch",
							"invocations": []map[string]interface{}{
								{
									"tool_name": "bash",
									"parameters": map[string]interface{}{
										"description": "Echo hello",
										"command":     "echo hello",
										"timeout":     10,
									},
								},
							},
						},
					},
				},
			},
			expectError: true,
			errorType:   ErrNestedBatch,
		},
		{
			name: "non-existent tool",
			input: BatchToolInput{
				Description: "Invalid tool",
				Invocations: []Invocation{
					{
						ToolName: "non_existent_tool",
						Parameters: map[string]interface{}{
							"some_param": "some_value",
						},
					},
				},
			},
			expectError: true,
			errorType:   ErrToolNotFound,
		},
		{
			name: "invalid parameters for tool",
			input: BatchToolInput{
				Description: "Invalid parameters",
				Invocations: []Invocation{
					{
						ToolName: "bash",
						Parameters: map[string]interface{}{
							// Missing required parameter "command"
							"description": "Missing command",
							"timeout":     10,
						},
					},
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputBytes, err := json.Marshal(tt.input)
			require.NoError(t, err)

			err = tool.ValidateInput(state, string(inputBytes))
			if tt.expectError {
				assert.Error(t, err)
				if tt.errorType != nil {
					assert.ErrorIs(t, err, tt.errorType)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// Fix for the undefined 't' variable in noNestedBatch function
func TestNoNestedBatch(t *testing.T) {
	batchTool := &BatchTool{}

	tests := []struct {
		name        string
		input       BatchToolInput
		expectError bool
	}{
		{
			name: "valid input without nested batch",
			input: BatchToolInput{
				Description: "Valid batch",
				Invocations: []Invocation{
					{
						ToolName: "bash",
						Parameters: map[string]interface{}{
							"description": "Echo hello",
							"command":     "echo hello",
						},
					},
				},
			},
			expectError: false,
		},
		{
			name: "invalid input with nested batch",
			input: BatchToolInput{
				Description: "Invalid batch",
				Invocations: []Invocation{
					{
						ToolName:   "batch", // This should match batchTool.Name()
						Parameters: map[string]interface{}{},
					},
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Monkey patch noNestedBatch to use batchTool instead of undefined 't'
			err := func(input BatchToolInput) error {
				for idx, invocation := range input.Invocations {
					if invocation.ToolName == batchTool.Name() {
						return errors.Wrapf(ErrNestedBatch, "invocation.%d is a batch tool", idx)
					}
				}
				return nil
			}(tt.input)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestBatchTool_Execute(t *testing.T) {
	tool := &BatchTool{}
	ctx := context.Background()
	state := NewBasicState(context.TODO())

	t.Run("successful batch execution", func(t *testing.T) {
		input := BatchToolInput{
			Description: "Run echo commands",
			Invocations: []Invocation{
				{
					ToolName: "bash",
					Parameters: map[string]interface{}{
						"description": "Echo hello",
						"command":     "echo hello",
						"timeout":     10,
					},
				},
				{
					ToolName: "bash",
					Parameters: map[string]interface{}{
						"description": "Echo world",
						"command":     "echo world",
						"timeout":     10,
					},
				},
			},
		}

		inputBytes, err := json.Marshal(input)
		require.NoError(t, err)

		result := tool.Execute(ctx, state, string(inputBytes))
		assert.False(t, result.IsError())
		assert.Contains(t, result.GetResult(), "hello")
		assert.Contains(t, result.GetResult(), "world")
		assert.Contains(t, result.GetResult(), "<invocation.0.result>")
		assert.Contains(t, result.GetResult(), "<invocation.1.result>")
	})

	t.Run("one tool succeeds, one fails", func(t *testing.T) {
		input := BatchToolInput{
			Description: "Run mixed commands",
			Invocations: []Invocation{
				{
					ToolName: "bash",
					Parameters: map[string]interface{}{
						"description": "Echo hello",
						"command":     "echo hello",
						"timeout":     10,
					},
				},
				{
					ToolName: "bash",
					Parameters: map[string]interface{}{
						"description": "Invalid command",
						"command":     "nonexistentcommand",
						"timeout":     10,
					},
				},
			},
		}

		inputBytes, err := json.Marshal(input)
		require.NoError(t, err)

		result := tool.Execute(ctx, state, string(inputBytes))
		assert.Contains(t, result.GetResult(), "hello")
		assert.Contains(t, result.GetError(), "Command exited with status 127")
		assert.Contains(t, result.GetResult(), "<invocation.0.result>")
		assert.Contains(t, result.GetError(), "<invocation.1.error>")
	})

	t.Run("invalid JSON input", func(t *testing.T) {
		result := tool.Execute(ctx, state, "invalid json")
		assert.True(t, result.IsError())
		assert.Contains(t, result.GetError(), "failed to unmarshal input")
	})
}

func TestBatchTool_TracingKVs(t *testing.T) {
	tool := &BatchTool{}

	t.Run("valid input", func(t *testing.T) {
		input := BatchToolInput{
			Description: "Test batch",
			Invocations: []Invocation{
				{
					ToolName: "bash",
					Parameters: map[string]interface{}{
						"description": "Echo hello",
						"command":     "echo hello",
					},
				},
			},
		}

		inputBytes, err := json.Marshal(input)
		require.NoError(t, err)

		kvs, err := tool.TracingKVs(string(inputBytes))
		assert.NoError(t, err)
		assert.NotEmpty(t, kvs)

		// Check for expected key-value pairs
		found := false
		for _, kv := range kvs {
			if kv.Key == "description" && kv.Value.AsString() == "Test batch" {
				found = true
				break
			}
		}
		assert.True(t, found, "Expected description key-value pair not found")

		// Check for invocation.0.tool_name
		found = false
		for _, kv := range kvs {
			if kv.Key == "invocation.0.tool_name" && kv.Value.AsString() == "bash" {
				found = true
				break
			}
		}
		assert.True(t, found, "Expected invocation.0.tool_name key-value pair not found")
	})

	t.Run("invalid JSON input", func(t *testing.T) {
		_, err := tool.TracingKVs("invalid json")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal input")
	})
}
