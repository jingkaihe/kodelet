package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

func TestSubAgentTool_BasicMethods(t *testing.T) {
	tool := &SubAgentTool{}

	assert.Equal(t, "subagent", tool.Name())
	assert.NotNil(t, tool.GenerateSchema())
	assert.Contains(t, tool.Description(), "delegate tasks to a sub-agent")
}

func TestSubAgentTool_ValidateInput(t *testing.T) {
	tool := &SubAgentTool{}
	state := NewBasicState(context.TODO())

	// Valid inputs
	err := tool.ValidateInput(state, `{"question": "test"}`)
	assert.NoError(t, err)

	// Invalid inputs
	err = tool.ValidateInput(state, `{"question": ""}`)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "question is required")
}

func TestSubAgentTool_TracingKVs(t *testing.T) {
	tool := &SubAgentTool{}

	kvs, err := tool.TracingKVs(`{"question": "test question"}`)
	assert.NoError(t, err)
	expected := []attribute.KeyValue{
		attribute.String("question", "test question"),
	}
	assert.Equal(t, expected, kvs)
}

func TestSubAgentToolResult_Methods(t *testing.T) {
	// Test successful result
	result := &SubAgentToolResult{
		result:   "success",
		question: "test q",
	}

	assert.Equal(t, "success", result.GetResult())
	assert.Empty(t, result.GetError())
	assert.False(t, result.IsError())
	assert.Contains(t, result.AssistantFacing(), "success")

	// Test error result
	errorResult := &SubAgentToolResult{
		err:      "error",
		question: "test q",
	}

	assert.Empty(t, errorResult.GetResult())
	assert.Equal(t, "error", errorResult.GetError())
	assert.True(t, errorResult.IsError())
	assert.Contains(t, errorResult.AssistantFacing(), "error")
}

func TestSubAgentToolResult_StructuredData(t *testing.T) {
	// Test successful result structured data
	result := &SubAgentToolResult{
		result:   "test response",
		question: "test question",
	}

	structuredData := result.StructuredData()
	assert.Equal(t, "subagent", structuredData.ToolName)
	assert.True(t, structuredData.Success)

	var metadata tooltypes.SubAgentMetadata
	assert.True(t, tooltypes.ExtractMetadata(structuredData.Metadata, &metadata))
	assert.Equal(t, "test question", metadata.Question)
	assert.Equal(t, "test response", metadata.Response)

	// Test error result structured data
	errorResult := &SubAgentToolResult{
		err:      "some error",
		question: "test question",
	}

	errorStructuredData := errorResult.StructuredData()
	assert.Equal(t, "subagent", errorStructuredData.ToolName)
	assert.False(t, errorStructuredData.Success)
	assert.Equal(t, "some error", errorStructuredData.Error)

	var errorMetadata tooltypes.SubAgentMetadata
	assert.True(t, tooltypes.ExtractMetadata(errorStructuredData.Metadata, &errorMetadata))
	assert.Equal(t, "test question", errorMetadata.Question)
	assert.Empty(t, errorMetadata.Response)
}

func TestBuildSubagentArgs(t *testing.T) {
	t.Run("basic args without subagent_args", func(t *testing.T) {
		args := BuildSubagentArgs("", "What is foo?")

		assert.Equal(t, []string{
			"run", "--result-only", "--as-subagent",
			"What is foo?",
		}, args)
	})

	t.Run("with --use-weak-model", func(t *testing.T) {
		args := BuildSubagentArgs("--use-weak-model", "What is foo?")

		assert.Equal(t, []string{
			"run", "--result-only", "--as-subagent",
			"--use-weak-model",
			"What is foo?",
		}, args)
	})

	t.Run("with --profile flag", func(t *testing.T) {
		args := BuildSubagentArgs("--profile cheap", "What is foo?")

		assert.Equal(t, []string{
			"run", "--result-only", "--as-subagent",
			"--profile", "cheap",
			"What is foo?",
		}, args)
	})

	t.Run("with multiple flags", func(t *testing.T) {
		args := BuildSubagentArgs("--profile openai-subagent --use-weak-model", "What is foo?")

		assert.Equal(t, []string{
			"run", "--result-only", "--as-subagent",
			"--profile", "openai-subagent",
			"--use-weak-model",
			"What is foo?",
		}, args)
	})

	t.Run("with quoted argument in subagent_args", func(t *testing.T) {
		// shlex should handle quoted strings correctly
		args := BuildSubagentArgs(`--profile "my profile"`, "What is foo?")

		assert.Equal(t, []string{
			"run", "--result-only", "--as-subagent",
			"--profile", "my profile",
			"What is foo?",
		}, args)
	})

	t.Run("preserves question with special characters", func(t *testing.T) {
		question := `Where is the "foo()" function defined?`
		args := BuildSubagentArgs("", question)

		assert.Equal(t, []string{
			"run", "--result-only", "--as-subagent",
			question,
		}, args)
	})

	t.Run("invalid shlex syntax falls back gracefully", func(t *testing.T) {
		// Unclosed quote - shlex.Split returns error, so subagentArgs is ignored
		args := BuildSubagentArgs(`--profile "unclosed`, "What is foo?")

		// Should still have base args and question, just skip the invalid subagent_args
		assert.Equal(t, []string{
			"run", "--result-only", "--as-subagent",
			"What is foo?",
		}, args)
	})

	t.Run("empty question", func(t *testing.T) {
		args := BuildSubagentArgs("--use-weak-model", "")

		assert.Equal(t, []string{
			"run", "--result-only", "--as-subagent",
			"--use-weak-model",
			"",
		}, args)
	})
}

func TestBuildSubagentArgs_CommonPatterns(t *testing.T) {
	// Test common configuration patterns from ADR 027
	testCases := []struct {
		name         string
		subagentArgs string
		expected     []string
	}{
		{
			name:         "cost optimization with weak model",
			subagentArgs: "--use-weak-model",
			expected:     []string{"run", "--result-only", "--as-subagent", "--use-weak-model", "query"},
		},
		{
			name:         "cross-provider via profile",
			subagentArgs: "--profile openai-subagent",
			expected:     []string{"run", "--result-only", "--as-subagent", "--profile", "openai-subagent", "query"},
		},
		{
			name:         "empty subagent_args uses default",
			subagentArgs: "",
			expected:     []string{"run", "--result-only", "--as-subagent", "query"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			args := BuildSubagentArgs(tc.subagentArgs, "query")
			assert.Equal(t, tc.expected, args)
		})
	}
}

// Execute tests require integration testing (shell-out via exec.CommandContext)
