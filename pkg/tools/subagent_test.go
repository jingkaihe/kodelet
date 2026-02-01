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

// Note: Execute method tests are not included because the subagent tool
// uses shell-out pattern via exec.CommandContext. Testing the Execute
// method would require integration tests with actual kodelet binary
// or mocking the exec.Command, which is beyond unit test scope.
// The shell-out pattern is tested at the integration/acceptance level.
