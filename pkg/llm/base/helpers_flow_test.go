package base

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/db/dbtest"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/steer"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"
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
func (t *threadStub) GetUsage() llmtypes.Usage               { return t.usage }
func (t *threadStub) GetConversationID() string              { return t.conversationID }
func (t *threadStub) SetConversationID(id string)            { t.conversationID = id }
func (t *threadStub) SaveConversation(context.Context) error { return nil }
func (t *threadStub) IsPersisted() bool                      { return t.persisted }
func (t *threadStub) EnablePersistence(_ context.Context, enabled bool) error {
	t.persisted = enabled
	return nil
}
func (t *threadStub) Provider() string { return "test" }
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
	turnFollowUps         []extensions.FollowUpMessage
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
	agentEndCalls         int
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

func (e *recordingAgentEnvironment) DispatchTurnEnd(_ context.Context, finalOutput string, _ int) ([]extensions.FollowUpMessage, error) {
	e.turnEndCalls = append(e.turnEndCalls, finalOutput)
	return e.turnFollowUps, e.turnEndErr
}

func (e *recordingAgentEnvironment) DispatchAgentEnd(context.Context, []llmtypes.Message) ([]extensions.FollowUpMessage, error) {
	e.agentEndCalls++
	var messages []extensions.FollowUpMessage
	for _, text := range e.followUps {
		messages = append(messages, extensions.FollowUpMessage{FollowUp: extensions.FollowUp{Text: text}, ExtensionID: "test"})
	}
	return messages, e.agentEndErr
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
	tools := []tooltypes.Tool{namedTool("file_read"), namedTool("file_write")}
	state := &toolState{tools: tools}

	assert.Empty(t, AvailableTools(nil, false))
	assert.Empty(t, AvailableTools(state, true))
	assert.Equal(t, tools, AvailableTools(state, false))
}

func TestAvailableToolsForThreadHonorsExtensionAllowedTools(t *testing.T) {
	tools := []tooltypes.Tool{namedTool("file_read"), namedTool("bash"), namedTool("file_write")}
	state := &toolState{tools: tools}
	thread := &threadStub{metadata: map[string]any{"allowed_tools": []string{"file_read", "file_write"}}}

	available := AvailableToolsForThread(thread, state, false)

	assert.Equal(t, []tooltypes.Tool{tools[0], tools[2]}, available)
}

type groupedTool struct{ namedTool }

func (groupedTool) ToolGroup() string { return "mcp/test" }

type modelOnlyTool struct{ namedTool }

func (modelOnlyTool) ModelOnly() bool { return true }

func toolNames(tools []tooltypes.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name())
	}
	return names
}

func TestCodeModeSeparatesAdvertisementFromAuthorization(t *testing.T) {
	tools := []tooltypes.Tool{
		namedTool("bash"),
		namedTool("code_execute"),
		groupedTool{namedTool("search_issues")},
		modelOnlyTool{namedTool("skill")},
	}
	state := &toolState{tools: tools}
	for _, mode := range []string{"off", "on", "only"} {
		t.Run(mode, func(t *testing.T) {
			thread := &environmentThreadStub{
				threadStub: &threadStub{state: state, config: llmtypes.Config{CodeMode: mode}},
				environment: &recordingAgentEnvironment{open: true, manifest: agentenv.Manifest{Tools: []agentenv.ToolDefinition{
					{Name: "bash", Description: "test tool", Tool: tools[0]},
					{Name: "code_execute", Description: "test tool", Tool: tools[1]},
					{Name: "search_issues", Description: "test tool", Group: "mcp/test", Tool: tools[2]},
					{Name: "skill", Description: "test tool", Tool: tools[3], ModelOnly: true},
				}}},
			}
			want := []string{"bash", "code_execute", "search_issues", "skill"}
			if mode == "only" {
				want = []string{"code_execute", "skill"}
			}
			assert.Equal(t, want, toolNames(AvailableEnvironmentToolsForThread(thread, false)))
			assert.Equal(t, want, toolNames(AvailableToolsForThread(thread, state, false)))
			assert.Equal(t, want, toolNames(AvailableToolsForThread(thread.threadStub, state, false)),
				"state-based advertisement follows the same mode")
			if mode == "only" {
				for _, advertised := range [][]tooltypes.Tool{
					AvailableEnvironmentToolsForThread(thread, false),
					AvailableToolsForThread(thread.threadStub, state, false),
				} {
					description := advertised[0].Description()
					assert.True(t, strings.HasPrefix(description, "test tool\n\nCallable tools,"), "the index follows the base description")
					assert.Contains(t, description, "Built-in tools:\n  bash({}) — test tool")
					assert.Contains(t, description, "Group mcp/test:\n  search_issues({}) — test tool")
					assert.NotContains(t, description, "skill", "model-only tools are not script-callable")
					assert.Equal(t, tooltypes.JSONSchemaForTool(tools[1]), tooltypes.JSONSchemaForTool(advertised[0]))
				}
			} else {
				assert.IsType(t, namedTool(""), AvailableEnvironmentToolsForThread(thread, false)[1],
					"other modes advertise code_execute unchanged")
			}
			assert.True(t, ToolAllowedForThread(thread, "bash"), "core tools remain callable from scripts")
			assert.True(t, ToolAllowedForThread(thread, "search_issues"), "hiding a schema must not change authorization")
			assert.Len(t, state.Tools(), 4, "the underlying callable state is unchanged")
			assert.Empty(t, AvailableEnvironmentToolsForThread(thread, true))
			thread.SetMetadataValue(extensionAllowedToolsMetadataKey, []string{"bash", "search_issues"})
			if mode == "only" {
				assert.Empty(t, AvailableEnvironmentToolsForThread(thread, false), "never fall back when code_execute is denied")
				assert.Empty(t, AvailableToolsForThread(thread.threadStub, state, false))
				thread.SetMetadataValue(extensionAllowedToolsMetadataKey, []string{"bash", "skill"})
				assert.Equal(t, []string{"skill"}, toolNames(AvailableEnvironmentToolsForThread(thread, false)),
					"model-only tools stay declared without code_execute")
				thread.SetMetadataValue(extensionAllowedToolsMetadataKey, []string{"code_execute", "bash"})
				description := AvailableEnvironmentToolsForThread(thread, false)[0].Description()
				assert.Contains(t, description, "bash({})")
				assert.NotContains(t, description, "search_issues", "the index lists only this turn's callable tools")
			} else {
				assert.Equal(t, []string{"bash", "search_issues"}, toolNames(AvailableEnvironmentToolsForThread(thread, false)))
			}
		})
	}
	assert.Equal(t, tools, AvailableToolsForThread(nil, state, false))
}

func TestAgentInitAllowedToolsUsesEffectiveStateToolsByDefault(t *testing.T) {
	state := &toolState{tools: []tooltypes.Tool{namedTool("file_read"), nil, namedTool("bash")}}

	assert.Equal(t,
		[]string{"file_read", "bash", "openai_web_search", "anthropic_web_search"},
		agentInitAllowedTools(llmtypes.Config{}, state),
	)
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
	_, err = TriggerTurnEnd(t.Context(), thread, nil, "final response", 3, true)
	require.NoError(t, err)
	_, err = TriggerTurnEnd(t.Context(), thread, nil, "", 4, true)
	require.NoError(t, err)
	_, err = TriggerTurnEnd(t.Context(), thread, nil, " \n\t", 5, true)
	require.NoError(t, err)

	handler := &recordingHandler{}
	continued, err := HandleAgentStopFollowUps(t.Context(), thread, handler, true)
	require.NoError(t, err)
	assert.True(t, continued)
	assert.Equal(t, []string{"inspect the logs", "retry the check"}, thread.userMessages)
	require.Len(t, handler.texts, 2)
	assert.Contains(t, handler.texts[0], "inspect the logs")
	assert.Equal(t, 1, environment.agentStartCalls)
	assert.Equal(t, []int{3}, environment.turnStartCalls)
	assert.Equal(t, []string{"final response", "", " \n\t"}, environment.turnEndCalls)
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
	_, err = TriggerTurnEnd(t.Context(), thread, nil, "output", 1, true)
	require.ErrorIs(t, err, sentinel)
	_, err = HandleAgentStopFollowUps(t.Context(), thread, &recordingHandler{}, true)
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

type provenanceThread struct {
	llmtypes.Thread
	base *Thread
	raw  []map[string]string
}

func (t *provenanceThread) GetMetadata() map[string]any { return t.base.GetMetadata() }
func (t *provenanceThread) AddUserMessage(ctx context.Context, text string, _ ...string) {
	t.raw = append(t.raw, map[string]string{"role": "user", "content": text})
	t.base.RecordUserMessageSource(ctx, llmtypes.UserMessageSource(ctx), t.raw)
}

type recordingUserHandler struct {
	recordingHandler
	users []string
}

func (h *recordingUserHandler) HandleUserMessage(content string, _ []string) {
	h.users = append(h.users, content)
}

type recordingExtensionHandler struct {
	recordingUserHandler
	extensionIDs []string
}

func (h *recordingExtensionHandler) HandleExtensionUserMessage(content, extensionID string) {
	h.users = append(h.users, content)
	h.extensionIDs = append(h.extensionIDs, extensionID)
}

func TestExtensionFollowUpsLiveAttributionAndFallbacks(t *testing.T) {
	for _, kind := range []string{"attributed", "user", "legacy", "nil"} {
		t.Run(kind, func(t *testing.T) {
			thread := &provenanceThread{base: NewThread(llmtypes.Config{}, "conversation")}
			attributed, user, legacy := &recordingExtensionHandler{}, &recordingUserHandler{}, &recordingHandler{}
			handler := map[string]llmtypes.MessageHandler{
				"attributed": attributed, "user": user, "legacy": legacy,
			}[kind]
			visible := extensions.FollowUpMessage{
				ExtensionID: "reminders",
				FollowUp:    extensions.FollowUp{Text: "same words", Key: "visible", Revision: 1},
			}
			hidden := visible
			hidden.Text, hidden.Key, hidden.Visibility = "private follow-up", "hidden", "hidden"
			require.True(t, appendExtensionFollowUps(t.Context(), thread, handler, []extensions.FollowUpMessage{visible, hidden, visible}))
			require.Len(t, thread.raw, 2)
			assert.Equal(t, "same words", thread.raw[0]["content"], "attribution does not change model input")
			assert.Equal(t, "private follow-up", thread.raw[1]["content"])
			switch kind {
			case "attributed":
				assert.Equal(t, []string{"same words"}, attributed.users)
				assert.Equal(t, []string{"reminders"}, attributed.extensionIDs)
				assert.Empty(t, attributed.texts)
			case "user":
				assert.Equal(t, []string{"Extension: reminders\n\nsame words"}, user.users)
				assert.Empty(t, user.texts)
			case "legacy":
				assert.Equal(t, []string{"\n📨 Extension follow-up (reminders): same words\n"}, legacy.texts)
			}
		})
	}
}

func TestExtensionFollowUpsLogOnlyAcceptanceCounts(t *testing.T) {
	log, hook := logrustest.NewNullLogger()
	log.SetLevel(logrus.DebugLevel)
	ctx := logger.WithLogger(t.Context(), logrus.NewEntry(log))
	thread := &provenanceThread{base: NewThread(llmtypes.Config{}, "conversation")}
	message := extensions.FollowUpMessage{
		ExtensionID: "private-extension",
		FollowUp:    extensions.FollowUp{Text: "private follow-up", Visibility: "hidden", Key: "private-key", Revision: 1},
	}
	require.True(t, appendExtensionFollowUps(ctx, thread, nil, []extensions.FollowUpMessage{message, message, {}}))
	entries := hook.AllEntries()
	require.Len(t, entries, 3)
	assert.Equal(t, logrus.InfoLevel, entries[0].Level)
	assert.Equal(t, "accepted extension follow-ups", entries[0].Message)
	assert.Equal(t, logrus.Fields{"count": 1}, entries[0].Data)
	assert.Equal(t, logrus.DebugLevel, entries[1].Level)
	assert.Equal(t, "deduplicated extension follow-ups", entries[1].Message)
	assert.Equal(t, logrus.Fields{"count": 1}, entries[1].Data)
	assert.Equal(t, logrus.WarnLevel, entries[2].Level)
	assert.Equal(t, "rejected invalid extension follow-ups", entries[2].Message)
	assert.Equal(t, logrus.Fields{"count": 1}, entries[2].Data)

	hook.Reset()
	require.False(t, appendExtensionFollowUps(ctx, thread, nil, []extensions.FollowUpMessage{message}))
	require.Len(t, hook.AllEntries(), 1)
	assert.Equal(t, logrus.DebugLevel, hook.LastEntry().Level)
	hook.Reset()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	require.False(t, appendExtensionFollowUps(cancelled, thread, nil, []extensions.FollowUpMessage{message}))
	assert.Empty(t, hook.AllEntries())

	invalid := message
	invalid.Revision = -1
	missingSource := message
	missingSource.ExtensionID = ""
	metadata := thread.GetMetadata()
	handler := &recordingExtensionHandler{}
	require.False(t, appendExtensionFollowUps(ctx, thread, handler, []extensions.FollowUpMessage{invalid, missingSource}))
	require.Len(t, hook.AllEntries(), 1)
	assert.Equal(t, logrus.WarnLevel, hook.LastEntry().Level)
	assert.Equal(t, "rejected invalid extension follow-ups", hook.LastEntry().Message)
	assert.Equal(t, logrus.Fields{"count": 2}, hook.LastEntry().Data)
	assert.Equal(t, metadata, thread.GetMetadata())
	assert.Len(t, thread.raw, 1)
	assert.Empty(t, handler.users)
	assert.Empty(t, handler.texts)
}

func TestExtensionFollowUpMetadataCopyOnWrite(t *testing.T) {
	for _, persisted := range []bool{false, true} {
		t.Run(map[bool]string{false: "typed", true: "JSON"}[persisted], func(t *testing.T) {
			thread := &provenanceThread{base: NewThread(llmtypes.Config{}, "conversation")}
			message := extensions.FollowUpMessage{
				ExtensionID: "mailbox",
				FollowUp:    extensions.FollowUp{Text: "notice", Visibility: "hidden", Key: "inbox", Revision: math.MaxInt64 - 1},
			}
			require.True(t, appendExtensionFollowUps(t.Context(), thread, nil, []extensions.FollowUpMessage{message}))
			snapshot := thread.GetMetadata()
			before, err := json.Marshal(snapshot)
			require.NoError(t, err)
			if persisted {
				require.NoError(t, json.Unmarshal(before, &snapshot))
				thread.base.SetMetadata(snapshot)
			}
			message.Revision++
			require.True(t, appendExtensionFollowUps(t.Context(), thread, nil, []extensions.FollowUpMessage{message}))
			message.ExtensionID = "other"
			require.True(t, appendExtensionFollowUps(t.Context(), thread, nil, []extensions.FollowUpMessage{message}))
			after, err := json.Marshal(snapshot)
			require.NoError(t, err)
			assert.JSONEq(t, string(before), string(after), "both provenance and nested revision maps are immutable snapshots")
			assert.Equal(t, map[string]map[string]string{
				"mailbox": {"inbox": "9223372036854775807"},
				"other":   {"inbox": "9223372036854775807"},
			}, followUpRevisions(thread.GetMetadata()[FollowUpRevisionsMetadataKey]))
			thread.base.RestoreMessageMetadata(snapshot)
			message.ExtensionID = "mailbox"
			require.True(t, appendExtensionFollowUps(t.Context(), thread, nil, []extensions.FollowUpMessage{message}), "rollback restores the old watermark")
		})
	}
}

func TestFollowUpRevisionsTypedReadDoesNotAllocate(t *testing.T) {
	value := map[string]map[string]string{"mailbox": {"inbox": "9223372036854775807"}}
	var got string
	allocations := testing.AllocsPerRun(10, func() {
		got = followUpRevisions(value)["mailbox"]["inbox"]
	})
	assert.Equal(t, "9223372036854775807", got)
	assert.Zero(t, allocations)
}

func TestExtensionFollowUpsPersistExactProvenanceAndMonotonicRevisions(t *testing.T) {
	ctx := llmtypes.WithUserMessageSource(t.Context(), "human-steer")
	thread := &provenanceThread{base: NewThread(llmtypes.Config{}, "conversation")}
	handler := &recordingHandler{}
	notification := extensions.FollowUpMessage{
		ExtensionID: "mailbox",
		FollowUp:    extensions.FollowUp{Text: "same text", Visibility: "hidden", Key: "inbox", Revision: math.MaxInt64 - 1},
	}
	appendMessage := func(message extensions.FollowUpMessage) bool {
		return appendExtensionFollowUps(ctx, thread, handler, []extensions.FollowUpMessage{message})
	}
	thread.AddUserMessage(ctx, "same text")
	assert.True(t, appendMessage(notification))
	assert.False(t, appendMessage(notification))
	older := notification
	older.Revision--
	assert.False(t, appendMessage(older))
	assert.Empty(t, handler.texts, "hidden notifications never emit visible text")
	assert.Equal(t, map[string]string{"0": "human-steer"}, thread.GetMetadata()[UserMessageSourcesMetadataKey])
	assert.Equal(t, map[string]ExtensionMessageMetadata{"1": {ExtensionID: "mailbox", Visibility: "hidden"}}, thread.GetMetadata()[ExtensionMessagesMetadataKey])

	// Persisted JSON and compaction must not lose the int64 watermark or ordinal.
	raw, err := json.Marshal(thread.raw)
	require.NoError(t, err)
	require.NoError(t, thread.base.ArchiveCompactionLocked(raw, 1, "summary", "summary"))
	thread.raw = []map[string]string{{"role": "user", "content": "summary"}}
	metadataJSON, err := json.Marshal(thread.GetMetadata())
	require.NoError(t, err)
	var restored map[string]any
	require.NoError(t, json.Unmarshal(metadataJSON, &restored))
	resumed := NewThread(llmtypes.Config{}, "conversation")
	resumed.SetMetadata(restored)
	resumed.SetCompactionHistory(thread.base.GetCompactionHistory())
	thread.base = resumed
	assert.False(t, appendMessage(notification))
	notification.Revision++
	assert.True(t, appendMessage(notification))
	thread.AddUserMessage(context.Background(), "same text")
	assert.Equal(t, map[string]ExtensionMessageMetadata{
		"1": {ExtensionID: "mailbox", Visibility: "hidden"},
		"2": {ExtensionID: "mailbox", Visibility: "hidden"},
	}, thread.GetMetadata()[ExtensionMessagesMetadataKey])

	// Different source extensions and conversations do not suppress each other.
	other := notification
	other.ExtensionID, other.Visibility = "other", "visible"
	assert.True(t, appendMessage(other))
	require.Len(t, handler.texts, 1)
	assert.Contains(t, handler.texts[0], "other")
	fresh := &provenanceThread{base: NewThread(llmtypes.Config{}, "other-conversation")}
	assert.True(t, appendExtensionFollowUps(ctx, fresh, handler, []extensions.FollowUpMessage{notification}))

	// Temporary messages roll back both provenance and accepted revisions.
	snapshot := thread.GetMetadata()
	original := thread.raw
	temporary := notification
	temporary.Key = "temporary"
	assert.True(t, appendMessage(temporary))
	thread.raw = original
	thread.base.RestoreMessageMetadata(snapshot)
	assert.Equal(t, snapshot, thread.GetMetadata())
	assert.True(t, appendMessage(temporary), "rolled-back messages can be emitted again")
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	temporary.Key = "cancelled"
	beforeCancel := thread.GetMetadata()
	assert.False(t, appendExtensionFollowUps(cancelled, thread, handler, []extensions.FollowUpMessage{temporary}))
	assert.Equal(t, beforeCancel, thread.GetMetadata())
	assert.True(t, appendMessage(temporary), "cancellation does not advance the watermark")
}

func TestEndHooksRemainObservationalWithoutFollowUpBudget(t *testing.T) {
	for _, hook := range []string{"turn.end", "agent.end"} {
		t.Run(hook, func(t *testing.T) {
			environment := &recordingAgentEnvironment{
				open: true,
				turnFollowUps: []extensions.FollowUpMessage{{
					ExtensionID: "review",
					FollowUp:    extensions.FollowUp{Text: "review again", Key: "review", Revision: 42},
				}},
				followUps: []string{"review again"},
			}
			thread := &environmentThreadStub{threadStub: &threadStub{}, environment: environment}
			handler := &recordingHandler{}
			dispatch := func(accept bool) (bool, error) {
				if hook == "turn.end" {
					return TriggerTurnEnd(t.Context(), thread, handler, "answer", 1, accept)
				}
				return HandleAgentStopFollowUps(t.Context(), thread, handler, accept)
			}
			continued, err := dispatch(false)
			require.NoError(t, err)
			assert.False(t, continued)
			assert.Empty(t, thread.userMessages)
			assert.Empty(t, handler.texts)
			if hook == "turn.end" {
				assert.Equal(t, []string{"answer"}, environment.turnEndCalls)
			} else {
				assert.Equal(t, 1, environment.agentEndCalls)
			}

			environment.turnEndErr, environment.agentEndErr = context.Canceled, context.Canceled
			_, err = dispatch(false)
			require.ErrorIs(t, err, context.Canceled, "observational dispatch must still propagate cancellation")
			environment.turnEndErr, environment.agentEndErr = nil, nil
			continued, err = dispatch(true)
			require.NoError(t, err)
			assert.True(t, continued)
			assert.Equal(t, []string{"review again"}, thread.userMessages)
			require.Len(t, handler.texts, 1)
		})
	}
}

func TestTriggerTurnEnd(t *testing.T) {
	thread := &threadStub{}
	continued, err := TriggerTurnEnd(context.Background(), thread, nil, "final response", 7, true)
	require.NoError(t, err)
	assert.False(t, continued)
}

func TestHasPendingSteer(t *testing.T) {
	thread := NewThread(llmtypes.Config{}, "conv-test")
	assert.False(t, thread.HasPendingSteer(t.Context()), "a thread without a steering queue has nothing pending")

	steerStore := steer.NewStore(dbtest.OpenTemp(t))
	thread.SetStorage(Storage{Steering: steerStore})
	assert.False(t, thread.HasPendingSteer(t.Context()))
	_, err := steerStore.Enqueue(t.Context(), "conv-test", "keep going", nil)
	require.NoError(t, err)
	assert.True(t, thread.HasPendingSteer(t.Context()))

	_, err = steerStore.Enqueue(t.Context(), "other-conversation", "unrelated", nil)
	require.NoError(t, err)
	other := NewThread(llmtypes.Config{}, "third-conversation")
	other.SetStorage(Storage{Steering: steerStore})
	assert.False(t, other.HasPendingSteer(t.Context()), "steering belongs to its conversation")
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

	continued, err := HandleAgentStopFollowUps(context.Background(), thread, handler, true)
	require.NoError(t, err)

	assert.False(t, continued)
	assert.Empty(t, thread.userMessages)
	assert.Empty(t, handler.texts)
}

func TestHandleAgentStopFollowUpsReturnsFalse(t *testing.T) {
	t.Run("message retrieval error", func(t *testing.T) {
		thread := &threadStub{getMessagesErr: errors.New("boom")}

		continued, err := HandleAgentStopFollowUps(context.Background(), thread, &recordingHandler{}, true)
		require.NoError(t, err)

		assert.False(t, continued)
	})

	t.Run("no follow ups", func(t *testing.T) {
		thread := &threadStub{messages: []llmtypes.Message{{Role: "assistant", Content: "done"}}}

		continued, err := HandleAgentStopFollowUps(context.Background(), thread, &recordingHandler{}, true)
		require.NoError(t, err)

		assert.False(t, continued)
		assert.Empty(t, thread.userMessages)
	})
}

func TestCodeModeRunAllowlistWithoutParentDeclaresToolsDirectly(t *testing.T) {
	tools := []tooltypes.Tool{namedTool("file_read"), namedTool("grep_tool")}
	thread := &environmentThreadStub{
		threadStub: &threadStub{config: llmtypes.Config{
			CodeMode:         "only",
			ExecutionOptions: &llmtypes.ExecutionOptions{AllowedTools: &[]string{"file_read", "grep_tool"}},
		}},
		environment: &recordingAgentEnvironment{open: true, manifest: agentenv.Manifest{Tools: []agentenv.ToolDefinition{
			{Name: "file_read", Tool: tools[0]},
			{Name: "grep_tool", Tool: tools[1]},
		}}},
	}
	assert.Equal(t, []string{"file_read", "grep_tool"}, toolNames(AvailableEnvironmentToolsForThread(thread, false)),
		"a run whose allowlist excludes code_execute runs with code mode off")
}

type warnOnceThread struct {
	*environmentThreadStub
	warned bool
}

func (t *warnOnceThread) claimCodeModeHiddenWarning() bool {
	first := !t.warned
	t.warned = true
	return first
}

func TestCodeModeWarnsWhenTurnHidesEveryTool(t *testing.T) {
	hook := logrustest.NewLocal(logger.L.Logger)
	t.Cleanup(hook.Reset)
	tools := []tooltypes.Tool{namedTool("bash"), namedTool("code_execute")}
	thread := &warnOnceThread{environmentThreadStub: &environmentThreadStub{
		threadStub: &threadStub{conversationID: "warned", config: llmtypes.Config{CodeMode: "only"}},
		environment: &recordingAgentEnvironment{open: true, manifest: agentenv.Manifest{Tools: []agentenv.ToolDefinition{
			{Name: "bash", Tool: tools[0]},
			{Name: "code_execute", Tool: tools[1]},
		}}},
	}}
	assert.NotEmpty(t, AvailableEnvironmentToolsForThread(thread, false))
	assert.Empty(t, hook.AllEntries(), "no warning while code_execute is available")

	thread.SetMetadataValue(extensionAllowedToolsMetadataKey, []string{"bash"})
	assert.Empty(t, AvailableEnvironmentToolsForThread(thread, false), "a per-turn patch without code_execute stays strict")
	assert.Empty(t, AvailableEnvironmentToolsForThread(thread, false))
	require.Len(t, hook.AllEntries(), 1, "the warning is logged once per thread")
	entry := hook.LastEntry()
	assert.Equal(t, logrus.WarnLevel, entry.Level)
	assert.Equal(t, "warned", entry.Data["conversation_id"])
	assert.Equal(t, 1, entry.Data["hidden_tools"])
	assert.Contains(t, entry.Message, "code_execute is not in the turn's tool list")

	thread.SetMetadataValue(extensionAllowedToolsMetadataKey, []string{})
	hook.Reset()
	assert.Empty(t, AvailableEnvironmentToolsForThread(thread.environmentThreadStub, false))
	assert.Empty(t, hook.AllEntries(), "an empty tool list hides nothing")
}

func TestBaseThreadClaimsCodeModeWarningOnce(t *testing.T) {
	thread := NewThread(llmtypes.Config{}, "conversation")
	assert.True(t, thread.claimCodeModeHiddenWarning())
	assert.False(t, thread.claimCodeModeHiddenWarning())
}
