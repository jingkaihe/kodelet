package base

import (
	"context"
	"testing"

	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/goals"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

type namedTool string

func (t namedTool) GenerateSchema() *jsonschema.Schema { return &jsonschema.Schema{} }
func (t namedTool) Name() string                       { return string(t) }
func (t namedTool) Description() string                { return "test tool" }
func (t namedTool) ValidateInput(tooltypes.State, string) error {
	return nil
}

func (t namedTool) Execute(context.Context, tooltypes.State, string) tooltypes.ToolResult {
	return tooltypes.BaseToolResult{Result: "ok"}
}
func (t namedTool) TracingKVs(string) ([]attribute.KeyValue, error) { return nil, nil }

type toolState struct {
	mockState
	tools []tooltypes.Tool
}

func (s *toolState) Tools() []tooltypes.Tool { return s.tools }

type threadStub struct {
	state          tooltypes.State
	userMessages   []string
	userImages     [][]string
	metadata       map[string]any
	messages       []llmtypes.Message
	getMessagesErr error
	config         llmtypes.Config
	usage          llmtypes.Usage
	conversationID string
	persisted      bool
}

func (t *threadStub) SetState(s tooltypes.State) { t.state = s }
func (t *threadStub) GetState() tooltypes.State  { return t.state }
func (t *threadStub) AddUserMessage(_ context.Context, message string, imagePaths ...string) {
	t.userMessages = append(t.userMessages, message)
	t.userImages = append(t.userImages, imagePaths)
}

func (t *threadStub) SendMessage(context.Context, string, llmtypes.MessageHandler, llmtypes.MessageOpt) (string, error) {
	return "", nil
}
func (t *threadStub) GetUsage() llmtypes.Usage                          { return t.usage }
func (t *threadStub) GetConversationID() string                         { return t.conversationID }
func (t *threadStub) SetConversationID(id string)                       { t.conversationID = id }
func (t *threadStub) SaveConversation(context.Context, bool) error      { return nil }
func (t *threadStub) IsPersisted() bool                                 { return t.persisted }
func (t *threadStub) EnablePersistence(_ context.Context, enabled bool) { t.persisted = enabled }
func (t *threadStub) Provider() string                                  { return "test" }
func (t *threadStub) GetMessages() ([]llmtypes.Message, error) {
	if t.getMessagesErr != nil {
		return nil, t.getMessagesErr
	}
	return t.messages, nil
}
func (t *threadStub) GetConfig() llmtypes.Config                  { return t.config }
func (t *threadStub) AggregateSubagentUsage(usage llmtypes.Usage) { t.usage = usage }
func (t *threadStub) SetMetadataValue(key string, value any) {
	if t.metadata == nil {
		t.metadata = make(map[string]any)
	}
	t.metadata[key] = value
}
func (t *threadStub) GetMetadata() map[string]any { return t.metadata }

type recordingHandler struct {
	texts []string
}

func (h *recordingHandler) HandleText(text string)                                { h.texts = append(h.texts, text) }
func (h *recordingHandler) HandleToolUse(string, string, string)                  {}
func (h *recordingHandler) HandleToolResult(string, string, tooltypes.ToolResult) {}
func (h *recordingHandler) HandleThinking(string)                                 {}
func (h *recordingHandler) HandleDone()                                           {}

func TestAvailableTools(t *testing.T) {
	tools := []tooltypes.Tool{namedTool("read_file"), namedTool("update_goal")}
	state := &toolState{tools: tools}

	assert.Empty(t, AvailableTools(nil, false))
	assert.Empty(t, AvailableTools(state, true))
	assert.Equal(t, tools, AvailableTools(state, false))
}

func TestBase64ImageSourceMediaType(t *testing.T) {
	tests := []struct {
		name      string
		mimeType  string
		expected  string
		wantError bool
	}{
		{name: "jpeg", mimeType: "image/jpeg", expected: "image/jpeg"},
		{name: "png with whitespace and case", mimeType: " Image/PNG ", expected: "image/png"},
		{name: "gif", mimeType: "image/gif", expected: "image/gif"},
		{name: "webp", mimeType: "image/webp", expected: "image/webp"},
		{name: "unsupported", mimeType: "image/svg+xml", wantError: true},
		{name: "empty", mimeType: "", wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual, err := Base64ImageSourceMediaType(tt.mimeType)
			if tt.wantError {
				require.Error(t, err)
				assert.Empty(t, actual)
				assert.Contains(t, err.Error(), "unsupported image mime type")
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, actual)
		})
	}
}

func TestHandleGoalAutoContinuation(t *testing.T) {
	t.Run("continues active goal when update tool is available", func(t *testing.T) {
		thread := &threadStub{
			metadata: map[string]any{
				goals.MetadataKey: goals.Goal{Objective: "finish coverage", Status: goals.StatusActive, Version: 1},
			},
		}

		continued := HandleGoalAutoContinuation(context.Background(), thread, []tooltypes.Tool{namedTool("update_goal")})

		assert.True(t, continued)
		require.Len(t, thread.userMessages, 1)
		assert.Contains(t, thread.userMessages[0], goals.ContextStartMarker)
		assert.Contains(t, thread.userMessages[0], "finish coverage")
	})

	t.Run("skips when no active goal", func(t *testing.T) {
		thread := &threadStub{}

		continued := HandleGoalAutoContinuation(context.Background(), thread, []tooltypes.Tool{namedTool("update_goal")})

		assert.False(t, continued)
		assert.Empty(t, thread.userMessages)
	})

	t.Run("skips when update goal tool is unavailable", func(t *testing.T) {
		thread := &threadStub{
			metadata: map[string]any{
				goals.MetadataKey: goals.Goal{Objective: "finish coverage", Status: goals.StatusActive, Version: 1},
			},
		}

		continued := HandleGoalAutoContinuation(context.Background(), thread, []tooltypes.Tool{namedTool("read_file")})

		assert.False(t, continued)
		assert.Empty(t, thread.userMessages)
	})
}

func TestHasTool(t *testing.T) {
	assert.True(t, hasTool([]tooltypes.Tool{nil, namedTool("update_goal")}, "update_goal"))
	assert.False(t, hasTool([]tooltypes.Tool{nil, namedTool("read_file")}, "update_goal"))
}

func TestTriggerTurnEnd(t *testing.T) {
	thread := &threadStub{}
	TriggerTurnEnd(context.Background(), thread, "final response", 7)
}

func TestHandleAgentStopFollowUps(t *testing.T) {
	runtime := extensions.EmptyRuntime()
	thread := &threadStub{
		messages: []llmtypes.Message{{Role: "assistant", Content: "done"}},
		config: llmtypes.Config{
			Extensions: runtime,
		},
	}
	handler := &recordingHandler{}

	continued := HandleAgentStopFollowUps(context.Background(), thread, handler)

	assert.False(t, continued)
	assert.Empty(t, thread.userMessages)
	assert.Empty(t, handler.texts)
}

func TestHandleAgentStopFollowUpsReturnsFalse(t *testing.T) {
	t.Run("message retrieval error", func(t *testing.T) {
		thread := &threadStub{getMessagesErr: errors.New("boom")}

		continued := HandleAgentStopFollowUps(context.Background(), thread, &recordingHandler{})

		assert.False(t, continued)
	})

	t.Run("no follow ups", func(t *testing.T) {
		thread := &threadStub{messages: []llmtypes.Message{{Role: "assistant", Content: "done"}}}

		continued := HandleAgentStopFollowUps(context.Background(), thread, &recordingHandler{})

		assert.False(t, continued)
		assert.Empty(t, thread.userMessages)
	})
}
