package base

import (
	"context"
	"testing"

	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/goals"
	"github.com/jingkaihe/kodelet/pkg/steer"
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
func (t *threadStub) SaveConversation(context.Context) error            { return nil }
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
func (t *threadStub) SetMetadata(metadata map[string]any) {
	t.metadata = metadata
}

type recordingHandler struct {
	texts []string
}

func (h *recordingHandler) HandleText(text string)                                { h.texts = append(h.texts, text) }
func (h *recordingHandler) HandleToolUse(string, string, string)                  {}
func (h *recordingHandler) HandleToolResult(string, string, tooltypes.ToolResult) {}
func (h *recordingHandler) HandleThinking(string)                                 {}
func (h *recordingHandler) HandleDone()                                           {}

type recordingAgentEnvironment struct {
	open                  bool
	manifest              agentenv.Manifest
	state                 tooltypes.State
	processedMessage      string
	processMessageErr     error
	agentStartErr         error
	turnStartErr          error
	agentInitDecision     agentenv.AgentInitDecision
	agentInitErr          error
	agentInitAllowedTools []string
	turnEndErr            error
	followUps             []string
	agentEndErr           error
	toolCallDecision      agentenv.ToolCallDecision
	toolCallErr           error
	toolUpdateDecision    agentenv.ToolOutputDecision
	toolUpdateErr         error
	toolResultDecision    agentenv.ToolOutputDecision
	toolResultErr         error
	canStreamUpdates      bool
	executeTool           func(context.Context, agentenv.ToolRequest, agentenv.ToolUpdateSink) (agentenv.ToolExecution, error)
	agentStartCalls       int
	turnStartCalls        []int
	turnEndCalls          []string
}

func (e *recordingAgentEnvironment) Open(context.Context, agentenv.RunSpec) (agentenv.Manifest, error) {
	e.open = true
	return e.manifest.Clone(), nil
}

func (e *recordingAgentEnvironment) IsOpen() bool { return e != nil && e.open }
func (e *recordingAgentEnvironment) Manifest() agentenv.Manifest {
	if e == nil {
		return agentenv.Manifest{}
	}
	return e.manifest.Clone()
}

func (*recordingAgentEnvironment) ExecuteCommand(context.Context, agentenv.CommandRequest) (agentenv.CommandResult, error) {
	return agentenv.CommandResult{}, nil
}

func (e *recordingAgentEnvironment) ProcessUserMessage(_ context.Context, message string) (string, error) {
	if e.processedMessage == "" {
		e.processedMessage = message
	}
	return e.processedMessage, e.processMessageErr
}

func (e *recordingAgentEnvironment) DispatchAgentStart(context.Context) error {
	e.agentStartCalls++
	return e.agentStartErr
}

func (e *recordingAgentEnvironment) DispatchTurnStart(_ context.Context, turnNumber int) error {
	e.turnStartCalls = append(e.turnStartCalls, turnNumber)
	return e.turnStartErr
}

func (e *recordingAgentEnvironment) ProcessAgentInit(_ context.Context, _ string, allowedTools []string) (agentenv.AgentInitDecision, error) {
	e.agentInitAllowedTools = append([]string(nil), allowedTools...)
	return e.agentInitDecision, e.agentInitErr
}

func (e *recordingAgentEnvironment) DispatchTurnEnd(_ context.Context, finalOutput string, _ int) error {
	e.turnEndCalls = append(e.turnEndCalls, finalOutput)
	return e.turnEndErr
}

func (e *recordingAgentEnvironment) DispatchAgentEnd(context.Context, []llmtypes.Message) ([]string, error) {
	return append([]string(nil), e.followUps...), e.agentEndErr
}

func (e *recordingAgentEnvironment) DispatchToolCall(context.Context, agentenv.ToolRequest) (agentenv.ToolCallDecision, error) {
	return e.toolCallDecision, e.toolCallErr
}

func (e *recordingAgentEnvironment) DispatchToolUpdate(context.Context, agentenv.ToolOutputRequest) (agentenv.ToolOutputDecision, error) {
	return e.toolUpdateDecision, e.toolUpdateErr
}

func (e *recordingAgentEnvironment) DispatchToolResult(context.Context, agentenv.ToolOutputRequest) (agentenv.ToolOutputDecision, error) {
	return e.toolResultDecision, e.toolResultErr
}
func (e *recordingAgentEnvironment) CanStreamToolUpdates() bool { return e.canStreamUpdates }
func (e *recordingAgentEnvironment) ExecuteTool(ctx context.Context, request agentenv.ToolRequest, updates agentenv.ToolUpdateSink) (agentenv.ToolExecution, error) {
	if e.executeTool != nil {
		return e.executeTool(ctx, request, updates)
	}
	return agentenv.ToolExecution{Input: request.Input, Result: tooltypes.BaseToolResult{Result: "ok"}}, nil
}

func (e *recordingAgentEnvironment) Close(context.Context) error {
	e.open = false
	return nil
}
func (e *recordingAgentEnvironment) State() tooltypes.State { return e.state }

func TestAvailableTools(t *testing.T) {
	tools := []tooltypes.Tool{namedTool("read_file"), namedTool("update_goal")}
	state := &toolState{tools: tools}

	assert.Empty(t, AvailableTools(nil, false))
	assert.Empty(t, AvailableTools(state, true))
	assert.Equal(t, tools, AvailableTools(state, false))
}

func TestAvailableToolsForThreadHonorsExtensionAllowedTools(t *testing.T) {
	tools := []tooltypes.Tool{namedTool("read_file"), namedTool("bash"), namedTool("update_goal")}
	state := &toolState{tools: tools}
	thread := &threadStub{metadata: map[string]any{"allowed_tools": []string{"read_file", "update_goal"}}}

	available := AvailableToolsForThread(thread, state, false)

	assert.Equal(t, []tooltypes.Tool{tools[0], tools[2]}, available)
}

func TestAgentInitAllowedToolsUsesEffectiveStateToolsByDefault(t *testing.T) {
	state := &toolState{tools: []tooltypes.Tool{namedTool("file_read"), nil, namedTool("bash")}}

	assert.Equal(t, []string{"file_read", "bash", "openai_web_search"}, agentInitAllowedTools(llmtypes.Config{}, state))
	assert.Equal(t, []string{"file_read"}, agentInitAllowedTools(llmtypes.Config{AllowedTools: []string{"file_read"}}, state))
	assert.Empty(t, agentInitAllowedTools(llmtypes.Config{}, nil))
}

func TestProcessAgentInitClearsStaleAllowedToolsWhenNoPatchApplies(t *testing.T) {
	t.Run("no extension runtime", func(t *testing.T) {
		thread := &threadStub{metadata: map[string]any{extensionAllowedToolsMetadataKey: []string{"bash"}}}

		decision, err := ProcessAgentInit(context.Background(), thread, "base prompt")
		require.NoError(t, err)

		assert.Equal(t, "base prompt", decision.SystemPrompt)
		assert.False(t, decision.ToolsModified)
		assert.Nil(t, currentAllowedTools(thread))
		assert.NotContains(t, thread.metadata, extensionAllowedToolsMetadataKey)
	})

	t.Run("runtime without tools patch", func(t *testing.T) {
		thread := &threadStub{
			metadata: map[string]any{extensionAllowedToolsMetadataKey: []string{"bash"}},
			config: llmtypes.Config{
				Extensions: extensions.EmptyRuntime(),
			},
		}

		decision, err := ProcessAgentInit(context.Background(), thread, "base prompt")
		require.NoError(t, err)

		assert.Equal(t, "base prompt", decision.SystemPrompt)
		assert.False(t, decision.ToolsModified)
		assert.Nil(t, currentAllowedTools(thread))
		assert.NotContains(t, thread.metadata, extensionAllowedToolsMetadataKey)
	})
}

func TestTurnFlowUsesPinnedAgentEnvironment(t *testing.T) {
	environment := &recordingAgentEnvironment{
		open:             true,
		processedMessage: "rewritten message",
		manifest: agentenv.Manifest{Tools: []agentenv.ToolDefinition{
			{Name: "remote_tool", Tool: namedTool("remote_tool")},
			{Name: "duplicate", Tool: namedTool("remote_tool")},
			{Name: "empty", Tool: nil},
		}},
		agentInitDecision: agentenv.AgentInitDecision{
			SystemPrompt:  "runner prompt",
			AllowedTools:  []string{"remote_tool"},
			ToolsModified: true,
		},
		followUps: []string{"inspect the logs", "retry the check"},
	}
	thread := &environmentThreadStub{
		threadStub: &threadStub{
			config:   llmtypes.Config{AllowedTools: []string{"openai_web_search"}},
			metadata: map[string]any{extensionAllowedToolsMetadataKey: []string{"stale"}},
			messages: []llmtypes.Message{{Role: "assistant", Content: "done"}},
		},
		environment: environment,
	}

	message, err := ProcessUserMessage(t.Context(), thread, "original")
	require.NoError(t, err)
	assert.Equal(t, "rewritten message", message)
	require.NoError(t, DispatchAgentStart(t.Context(), thread))
	require.NoError(t, DispatchTurnStart(t.Context(), thread, 3))
	decision, err := ProcessAgentInit(t.Context(), thread, "base prompt")
	require.NoError(t, err)
	assert.Equal(t, AgentInitDecision{SystemPrompt: "runner prompt", AllowedTools: []string{"remote_tool"}, ToolsModified: true}, decision)
	assert.Equal(t, []string{"remote_tool", "openai_web_search"}, environment.agentInitAllowedTools)
	assert.Equal(t, []string{"remote_tool"}, thread.metadata[extensionAllowedToolsMetadataKey])
	prompt, err := ProcessSystemPrompt(t.Context(), thread, "base prompt")
	require.NoError(t, err)
	assert.Equal(t, "runner prompt", prompt)
	require.NoError(t, TriggerTurnEnd(t.Context(), thread, "final response", 3))
	require.NoError(t, TriggerTurnEnd(t.Context(), thread, "", 4))

	handler := &recordingHandler{}
	continued, err := HandleAgentStopFollowUps(t.Context(), thread, handler)
	require.NoError(t, err)
	assert.True(t, continued)
	assert.Equal(t, []string{"inspect the logs", "retry the check"}, thread.userMessages)
	require.Len(t, handler.texts, 2)
	assert.Contains(t, handler.texts[0], "inspect the logs")
	assert.Equal(t, 1, environment.agentStartCalls)
	assert.Equal(t, []int{3}, environment.turnStartCalls)
	assert.Equal(t, []string{"final response"}, environment.turnEndCalls)
}

func TestTurnFlowPropagatesEnvironmentFailures(t *testing.T) {
	sentinel := errors.New("runner unavailable")
	environment := &recordingAgentEnvironment{
		open:              true,
		processMessageErr: sentinel,
		agentStartErr:     sentinel,
		turnStartErr:      sentinel,
		agentInitErr:      sentinel,
		turnEndErr:        sentinel,
		agentEndErr:       sentinel,
	}
	thread := &environmentThreadStub{threadStub: &threadStub{}, environment: environment}

	_, err := ProcessUserMessage(t.Context(), thread, "message")
	require.ErrorIs(t, err, sentinel)
	require.ErrorIs(t, DispatchAgentStart(t.Context(), thread), sentinel)
	require.ErrorIs(t, DispatchTurnStart(t.Context(), thread, 1), sentinel)
	_, err = ProcessAgentInit(t.Context(), thread, "prompt")
	require.ErrorIs(t, err, sentinel)
	require.ErrorIs(t, TriggerTurnEnd(t.Context(), thread, "output", 1), sentinel)
	_, err = HandleAgentStopFollowUps(t.Context(), thread, &recordingHandler{})
	require.ErrorIs(t, err, sentinel)

	message, err := ProcessUserMessage(t.Context(), nil, "unchanged")
	require.NoError(t, err)
	assert.Equal(t, "unchanged", message)
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
	require.NoError(t, TriggerTurnEnd(context.Background(), thread, "final response", 7))
}

func TestHasPendingSteer(t *testing.T) {
	basePath := t.TempDir()
	t.Setenv("HOME", basePath)
	t.Setenv("KODELET_BASE_PATH", basePath)
	require.NoError(t, db.RunMigrations(context.Background(), migrations.All()))

	assert.False(t, HasPendingSteer(context.Background(), "conv-test"))

	steerStore, err := steer.NewSteerStore(context.Background())
	require.NoError(t, err)
	defer steerStore.Close()
	_, err = steerStore.Enqueue(context.Background(), "conv-test", "keep going", nil)
	require.NoError(t, err)

	assert.True(t, HasPendingSteer(context.Background(), "conv-test"))
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

	continued, err := HandleAgentStopFollowUps(context.Background(), thread, handler)
	require.NoError(t, err)

	assert.False(t, continued)
	assert.Empty(t, thread.userMessages)
	assert.Empty(t, handler.texts)
}

func TestHandleAgentStopFollowUpsReturnsFalse(t *testing.T) {
	t.Run("message retrieval error", func(t *testing.T) {
		thread := &threadStub{getMessagesErr: errors.New("boom")}

		continued, err := HandleAgentStopFollowUps(context.Background(), thread, &recordingHandler{})
		require.NoError(t, err)

		assert.False(t, continued)
	})

	t.Run("no follow ups", func(t *testing.T) {
		thread := &threadStub{messages: []llmtypes.Message{{Role: "assistant", Content: "done"}}}

		continued, err := HandleAgentStopFollowUps(context.Background(), thread, &recordingHandler{})
		require.NoError(t, err)

		assert.False(t, continued)
		assert.Empty(t, thread.userMessages)
	})
}
