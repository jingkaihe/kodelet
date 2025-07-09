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

func TestSubAgentTool_Execute_Success(t *testing.T) {
	tool := &SubAgentTool{}
	mockThread := new(subagentMockThread)

	mockThread.On("SendMessage", mock.Anything, "test question", mock.Anything, llmtypes.MessageOpt{
		PromptCache:        true,
		UseWeakModel:       false,
		NoSaveConversation: true,
		CompactRatio:       0.0,
		DisableAutoCompact: false,
	}).Return("test response", nil)

	ctx := context.WithValue(context.Background(), llmtypes.SubAgentConfig{}, llmtypes.SubAgentConfig{
		Thread:             mockThread,
		MessageHandler:     &llmtypes.StringCollectorHandler{Silent: true},
		CompactRatio:       0.0,
		DisableAutoCompact: false,
	})

	result := tool.Execute(ctx, NewBasicState(ctx), `{"question": "test question"}`)

	assert.False(t, result.IsError())
	assert.Equal(t, "test response", result.GetResult())

	// Test structured data - this is the key functionality we're testing
	structuredData := result.StructuredData()
	assert.Equal(t, "subagent", structuredData.ToolName)
	assert.True(t, structuredData.Success)

	var metadata tooltypes.SubAgentMetadata
	assert.True(t, tooltypes.ExtractMetadata(structuredData.Metadata, &metadata))
	assert.Equal(t, "test question", metadata.Question)
	assert.Equal(t, "test response", metadata.Response)

	mockThread.AssertExpectations(t)
}

func TestSubAgentTool_Execute_InheritsCompactConfig(t *testing.T) {
	tool := &SubAgentTool{}
	mockThread := new(subagentMockThread)

	// Test that subagent inherits parent's compact configuration
	mockThread.On("SendMessage", mock.Anything, "test question", mock.Anything, llmtypes.MessageOpt{
		PromptCache:        true,
		UseWeakModel:       false,
		NoSaveConversation: true,
		CompactRatio:       0.8,
		DisableAutoCompact: true,
	}).Return("test response", nil)

	ctx := context.WithValue(context.Background(), llmtypes.SubAgentConfig{}, llmtypes.SubAgentConfig{
		Thread:             mockThread,
		MessageHandler:     &llmtypes.StringCollectorHandler{Silent: true},
		CompactRatio:       0.8,
		DisableAutoCompact: true,
	})

	result := tool.Execute(ctx, NewBasicState(ctx), `{"question": "test question"}`)

	assert.False(t, result.IsError())
	assert.Equal(t, "test response", result.GetResult())

	mockThread.AssertExpectations(t)
}

func TestSubAgentTool_Execute_InheritsVariousCompactConfigs(t *testing.T) {
	tool := &SubAgentTool{}

	testCases := []struct {
		name               string
		compactRatio       float64
		disableAutoCompact bool
	}{
		{
			name:               "Default values",
			compactRatio:       0.0,
			disableAutoCompact: false,
		},
		{
			name:               "High compact ratio enabled",
			compactRatio:       0.9,
			disableAutoCompact: false,
		},
		{
			name:               "Low compact ratio enabled",
			compactRatio:       0.3,
			disableAutoCompact: false,
		},
		{
			name:               "Compact disabled",
			compactRatio:       0.8,
			disableAutoCompact: true,
		},
		{
			name:               "Edge case: ratio 1.0 with compaction disabled",
			compactRatio:       1.0,
			disableAutoCompact: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockThread := new(subagentMockThread)

			expectedOpt := llmtypes.MessageOpt{
				PromptCache:        true,
				UseWeakModel:       false,
				NoSaveConversation: true,
				CompactRatio:       tc.compactRatio,
				DisableAutoCompact: tc.disableAutoCompact,
			}

			mockThread.On("SendMessage", mock.Anything, "test question", mock.Anything, expectedOpt).Return("test response", nil)

			ctx := context.WithValue(context.Background(), llmtypes.SubAgentConfig{}, llmtypes.SubAgentConfig{
				Thread:             mockThread,
				MessageHandler:     &llmtypes.StringCollectorHandler{Silent: true},
				CompactRatio:       tc.compactRatio,
				DisableAutoCompact: tc.disableAutoCompact,
			})

			result := tool.Execute(ctx, NewBasicState(ctx), `{"question": "test question"}`)

			assert.False(t, result.IsError())
			assert.Equal(t, "test response", result.GetResult())

			mockThread.AssertExpectations(t)
		})
	}
}

func TestSubAgentTool_Execute_Errors(t *testing.T) {
	tool := &SubAgentTool{}

	// Test missing context
	result := tool.Execute(context.Background(), NewBasicState(context.TODO()), `{"question": "test"}`)
	assert.True(t, result.IsError())
	assert.Contains(t, result.GetError(), "sub-agent config not found")

	// Test thread error
	mockThread := new(subagentMockThread)
	mockThread.On("SendMessage", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("", errors.New("thread error"))

	ctx := context.WithValue(context.Background(), llmtypes.SubAgentConfig{}, llmtypes.SubAgentConfig{
		Thread:             mockThread,
		MessageHandler:     &llmtypes.StringCollectorHandler{Silent: true},
		CompactRatio:       0.0,
		DisableAutoCompact: false,
	})

	result = tool.Execute(ctx, NewBasicState(ctx), `{"question": "test"}`)
	assert.True(t, result.IsError())
	assert.Contains(t, result.GetError(), "thread error")

	// Even in error case, metadata should be preserved
	var metadata tooltypes.SubAgentMetadata
	assert.True(t, tooltypes.ExtractMetadata(result.StructuredData().Metadata, &metadata))
	assert.Equal(t, "test", metadata.Question)
	assert.Empty(t, metadata.Response)

	mockThread.AssertExpectations(t)
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
