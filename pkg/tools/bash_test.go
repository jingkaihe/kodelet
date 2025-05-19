package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
)

func TestBashTool_GenerateSchema(t *testing.T) {
	tool := &BashTool{}
	schema := tool.GenerateSchema()
	assert.NotNil(t, schema)

	assert.Equal(t, "https://github.com/jingkaihe/kodelet/pkg/tools/bash-input", string(schema.ID))
}

func TestBashTool_Name(t *testing.T) {
	tool := &BashTool{}
	assert.Equal(t, "bash", tool.Name())
}

func TestBashTool_Description(t *testing.T) {
	tool := &BashTool{}
	desc := tool.Description()
	assert.Contains(t, desc, "Executes a given bash command")
	assert.Contains(t, desc, "Important")
}

func execute(tool tooltypes.Tool, ctx context.Context, state tooltypes.State, parameters string) tooltypes.DefaultToolResult {
	_result := tool.Execute(ctx, state, parameters)
	result, _ := _result.(*tooltypes.DefaultToolResult)
	return *result
}

func TestBashTool_Execute_Success(t *testing.T) {
	tool := &BashTool{}
	input := BashInput{
		Description: "Echo test",
		Command:     "echo 'hello world'",
		Timeout:     10,
	}
	params, _ := json.Marshal(input)

	result := execute(tool, context.Background(), NewBasicState(), string(params))
	assert.Empty(t, result.Error)
	assert.Equal(t, "hello world\n", result.Result)
}

func TestBashTool_Execute_Timeout(t *testing.T) {
	tool := &BashTool{}
	input := BashInput{
		Description: "Sleep test",
		Command:     "sleep 2",
		Timeout:     1,
	}
	params, _ := json.Marshal(input)

	result := execute(tool, context.Background(), NewBasicState(), string(params))
	assert.Contains(t, result.Error, "Command timed out after 1 seconds")
	assert.Empty(t, result.Result)
}

func TestBashTool_Execute_Error(t *testing.T) {
	tool := &BashTool{}
	input := BashInput{
		Description: "Invalid command",
		Command:     "nonexistentcommand",
		Timeout:     10,
	}
	params, _ := json.Marshal(input)

	result := execute(tool, context.Background(), NewBasicState(), string(params))
	assert.Contains(t, result.Error, "Command exited with status 127")
	assert.Contains(t, result.Result, "nonexistentcommand: command not found")
}

func TestBashTool_Execute_InvalidJSON(t *testing.T) {
	tool := &BashTool{}
	result := execute(tool, context.Background(), NewBasicState(), "invalid json")
	assert.NotEmpty(t, result.Error)
	assert.Empty(t, result.Result)
}

func TestBashTool_Execute_ContextCancellation(t *testing.T) {
	tool := &BashTool{}
	input := BashInput{
		Description: "Long running command",
		Command:     "sleep 10",
		Timeout:     20,
	}
	params, _ := json.Marshal(input)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	result := execute(tool, ctx, NewBasicState(), string(params))
	assert.Contains(t, result.Error, "Command timed out")
	assert.Empty(t, result.Result)
}

func TestBashTool_ValidateInput(t *testing.T) {
	tool := &BashTool{}
	tests := []struct {
		name        string
		input       BashInput
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid single command",
			input: BashInput{
				Description: "test",
				Command:     "echo hello",
				Timeout:     10,
			},
			expectError: false,
		},
		{
			name: "valid multiple commands with &&",
			input: BashInput{
				Description: "test",
				Command:     "echo hello && echo world",
				Timeout:     10,
			},
			expectError: false,
		},
		{
			name: "valid multiple commands with ;",
			input: BashInput{
				Description: "test",
				Command:     "echo hello; echo world",
				Timeout:     10,
			},
			expectError: false,
		},
		{
			name: "valid complex command",
			input: BashInput{
				Description: "test",
				Command:     "echo hello && echo world; echo test || echo fail",
				Timeout:     10,
			},
			expectError: false,
		},
		{
			name: "banned command",
			input: BashInput{
				Description: "test",
				Command:     "echo hello && vim file.txt",
				Timeout:     10,
			},
			expectError: true,
			errorMsg:    "command is banned",
		},
		{
			name: "empty command",
			input: BashInput{
				Description: "test",
				Command:     "",
				Timeout:     10,
			},
			expectError: true,
			errorMsg:    "command is required",
		},
		{
			name: "missing description",
			input: BashInput{
				Command: "echo hello",
				Timeout: 10,
			},
			expectError: true,
			errorMsg:    "description is required",
		},
		{
			name: "invalid timeout too low",
			input: BashInput{
				Description: "test",
				Command:     "echo hello",
				Timeout:     5,
			},
			expectError: true,
			errorMsg:    "timeout must be between 10 and 120 seconds",
		},
		{
			name: "invalid timeout too high",
			input: BashInput{
				Description: "test",
				Command:     "echo hello",
				Timeout:     150,
			},
			expectError: true,
			errorMsg:    "timeout must be between 10 and 120 seconds",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input, _ := json.Marshal(tt.input)
			err := tool.ValidateInput(NewBasicState(), string(input))
			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
