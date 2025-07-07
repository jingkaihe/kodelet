package tools

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.opentelemetry.io/otel/attribute"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// Simple mock that only implements what we need for testing
type subagentMockThread struct {
	mock.Mock
}

func (m *subagentMockThread) SendMessage(ctx context.Context, message string, handler llmtypes.MessageHandler, opt llmtypes.MessageOpt) (string, error) {
	args := m.Called(ctx, message, handler, opt)
	return args.String(0), args.Error(1)
}

// Stub implementations for unused methods
func (m *subagentMockThread) SetState(s tooltypes.State) {}
func (m *subagentMockThread) GetState() tooltypes.State  { return nil }
func (m *subagentMockThread) AddUserMessage(ctx context.Context, message string, imagePaths ...string) {
}
func (m *subagentMockThread) GetUsage() llmtypes.Usage                                   { return llmtypes.Usage{} }
func (m *subagentMockThread) GetConversationID() string                                  { return "" }
func (m *subagentMockThread) SetConversationID(id string)                                {}
func (m *subagentMockThread) SaveConversation(ctx context.Context, summarise bool) error { return nil }
func (m *subagentMockThread) IsPersisted() bool                                          { return false }
func (m *subagentMockThread) EnablePersistence(ctx context.Context, enabled bool)        {}
func (m *subagentMockThread) Provider() string                                           { return "" }
func (m *subagentMockThread) GetMessages() ([]llmtypes.Message, error)                   { return nil, nil }

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
	err := tool.ValidateInput(state, `{"question": "test", "model_strength": "weak"}`)
	assert.NoError(t, err)

	err = tool.ValidateInput(state, `{"question": "test", "model_strength": "strong"}`)
	assert.NoError(t, err)

	// Invalid inputs
	err = tool.ValidateInput(state, `{"question": "", "model_strength": "weak"}`)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "question is required")

	err = tool.ValidateInput(state, `{"question": "test", "model_strength": "invalid"}`)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "model_strength must be either 'weak' or 'strong'")
}

func TestSubAgentTool_TracingKVs(t *testing.T) {
	tool := &SubAgentTool{}

	kvs, err := tool.TracingKVs(`{"question": "test question", "model_strength": "weak"}`)
	assert.NoError(t, err)
	expected := []attribute.KeyValue{
		attribute.String("question", "test question"),
	}
	assert.Equal(t, expected, kvs)
}

func TestSubAgentTool_Execute_Success(t *testing.T) {
	tool := &SubAgentTool{}
	mockThread := new(subagentMockThread)

	// Test weak model
	mockThread.On("SendMessage", mock.Anything, "test question", mock.Anything, llmtypes.MessageOpt{
		PromptCache:        false,
		UseWeakModel:       true,
		NoSaveConversation: true,
	}).Return("test response", nil)

	ctx := context.WithValue(context.Background(), llmtypes.SubAgentConfig{}, llmtypes.SubAgentConfig{
		Thread:         mockThread,
		MessageHandler: &llmtypes.StringCollectorHandler{Silent: true},
	})

	result := tool.Execute(ctx, NewBasicState(ctx), `{"question": "test question", "model_strength": "weak"}`)

	assert.False(t, result.IsError())
	assert.Equal(t, "test response", result.GetResult())

	// Test structured data - this is the key functionality we're testing
	structuredData := result.StructuredData()
	assert.Equal(t, "subagent", structuredData.ToolName)
	assert.True(t, structuredData.Success)

	var metadata tooltypes.SubAgentMetadata
	assert.True(t, tooltypes.ExtractMetadata(structuredData.Metadata, &metadata))
	assert.Equal(t, "test question", metadata.Question)
	assert.Equal(t, "weak", metadata.ModelStrength)
	assert.Equal(t, "test response", metadata.Response)

	mockThread.AssertExpectations(t)
}

func TestSubAgentTool_Execute_StrongModel(t *testing.T) {
	tool := &SubAgentTool{}
	mockThread := new(subagentMockThread)

	// Test strong model
	mockThread.On("SendMessage", mock.Anything, "complex question", mock.Anything, llmtypes.MessageOpt{
		PromptCache:        true,
		UseWeakModel:       false,
		NoSaveConversation: true,
	}).Return("complex response", nil)

	ctx := context.WithValue(context.Background(), llmtypes.SubAgentConfig{}, llmtypes.SubAgentConfig{
		Thread:         mockThread,
		MessageHandler: &llmtypes.StringCollectorHandler{Silent: true},
	})

	result := tool.Execute(ctx, NewBasicState(ctx), `{"question": "complex question", "model_strength": "strong"}`)

	assert.False(t, result.IsError())

	// Verify metadata propagation
	var metadata tooltypes.SubAgentMetadata
	assert.True(t, tooltypes.ExtractMetadata(result.StructuredData().Metadata, &metadata))
	assert.Equal(t, "complex question", metadata.Question)
	assert.Equal(t, "strong", metadata.ModelStrength)

	mockThread.AssertExpectations(t)
}

func TestSubAgentTool_Execute_Errors(t *testing.T) {
	tool := &SubAgentTool{}

	// Test missing context
	result := tool.Execute(context.Background(), NewBasicState(context.TODO()), `{"question": "test", "model_strength": "weak"}`)
	assert.True(t, result.IsError())
	assert.Contains(t, result.GetError(), "sub-agent config not found")

	// Test thread error
	mockThread := new(subagentMockThread)
	mockThread.On("SendMessage", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("", errors.New("thread error"))

	ctx := context.WithValue(context.Background(), llmtypes.SubAgentConfig{}, llmtypes.SubAgentConfig{
		Thread:         mockThread,
		MessageHandler: &llmtypes.StringCollectorHandler{Silent: true},
	})

	result = tool.Execute(ctx, NewBasicState(ctx), `{"question": "test", "model_strength": "weak"}`)
	assert.True(t, result.IsError())
	assert.Contains(t, result.GetError(), "thread error")

	// Even in error case, metadata should be preserved
	var metadata tooltypes.SubAgentMetadata
	assert.True(t, tooltypes.ExtractMetadata(result.StructuredData().Metadata, &metadata))
	assert.Equal(t, "test", metadata.Question)
	assert.Equal(t, "weak", metadata.ModelStrength)
	assert.Empty(t, metadata.Response)

	mockThread.AssertExpectations(t)
}

func TestSubAgentToolResult_Methods(t *testing.T) {
	// Test successful result
	result := &SubAgentToolResult{
		result:        "success",
		question:      "test q",
		modelStrength: "weak",
	}

	assert.Equal(t, "success", result.GetResult())
	assert.Empty(t, result.GetError())
	assert.False(t, result.IsError())
	assert.Contains(t, result.AssistantFacing(), "success")

	// Test error result
	errorResult := &SubAgentToolResult{
		err:           "error",
		question:      "test q",
		modelStrength: "strong",
	}

	assert.Empty(t, errorResult.GetResult())
	assert.Equal(t, "error", errorResult.GetError())
	assert.True(t, errorResult.IsError())
	assert.Contains(t, errorResult.AssistantFacing(), "error")
}
