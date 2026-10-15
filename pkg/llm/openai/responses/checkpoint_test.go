package responses

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/llm/base"
	"github.com/jingkaihe/kodelet/pkg/tools"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/ssestream"
	openairesponses "github.com/openai/openai-go/v3/responses"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type reloadCheckpointEnvironment struct {
	agentenv.UtilityEnvironment
	manifest agentenv.Manifest
	pending  bool
	opened   int
	turnEnd  int
	reload   func() (agentenv.ReloadOutcome, error)
}

func (e *reloadCheckpointEnvironment) Open(ctx context.Context, spec agentenv.RunSpec) (agentenv.Manifest, error) {
	e.opened++
	_, err := e.UtilityEnvironment.Open(ctx, spec)
	return e.Manifest(), err
}

func (e *reloadCheckpointEnvironment) Manifest() agentenv.Manifest { return e.manifest.Clone() }
func (e *reloadCheckpointEnvironment) HasPendingReload() bool      { return e.pending }
func (e *reloadCheckpointEnvironment) ReloadPendingResources(context.Context) (agentenv.ReloadOutcome, error) {
	e.pending = false
	return e.reload()
}

func (*reloadCheckpointEnvironment) ProcessAgentInit(_ context.Context, prompt string, _ []string) (agentenv.AgentInitDecision, error) {
	return agentenv.AgentInitDecision{SystemPrompt: prompt}, nil
}

func (e *reloadCheckpointEnvironment) DispatchTurnEnd(_ context.Context, _ string, turn int) error {
	e.turnEnd = turn
	return nil
}

type reloadCheckpointHandler struct {
	compactionCaptureHandler
	reloads      int
	reloadKind   string
	reloadID     string
	reloadText   string
	userMessages []string
}

func (h *reloadCheckpointHandler) HandleResourceReload(kind, markerID, text string) {
	h.reloads++
	h.reloadKind, h.reloadID, h.reloadText = kind, markerID, text
}

func (h *reloadCheckpointHandler) HandleUserMessage(content string, _ []string) {
	h.userMessages = append(h.userMessages, content)
}

func TestResponsesDeferredReloadCheckpoint(t *testing.T) {
	for _, test := range []struct {
		name     string
		maxTurns int
		refused  bool
		failed   bool
	}{
		{name: "next round uses fresh environment", maxTurns: 2},
		{name: "refusal continues with old environment", maxTurns: 2, refused: true},
		{name: "uncertain handoff stops the run", maxTurns: 2, failed: true},
		{name: "reload does not add model turns", maxTurns: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := llmtypes.Config{
				Provider: "openai", Model: "gpt-4.1", AllowedTools: []string{"bash", "file_read"},
				Retry: llmtypes.RetryConfig{Attempts: 1},
			}
			thread := &Thread{Thread: base.NewThread(config, "same-conversation")}
			store := &mockResponsesConversationStore{}
			thread.Store, thread.Persisted = store, true
			environment := &reloadCheckpointEnvironment{manifest: agentenv.Manifest{
				Config: &agentenv.EnvironmentConfig{SystemPromptContent: "old prompt"},
				Tools:  []agentenv.ToolDefinition{{Name: "bash", Tool: &tools.BashTool{}}},
			}}
			thread.SetEnvironment(environment)
			handler := &reloadCheckpointHandler{}
			var requests []openairesponses.ResponseNewParams
			batchComplete := false
			environment.reload = func() (agentenv.ReloadOutcome, error) {
				assert.True(t, batchComplete)
				assert.Equal(t, 1, environment.turnEnd)
				assert.False(t, handler.done, "reload must not end the visible run")
				if test.failed {
					return agentenv.ReloadOutcome{}, errors.New("uncertain reload")
				}
				if test.refused {
					return agentenv.ReloadOutcome{Message: "Reload refused: background workers retained", Display: "Resource reload refused: workers retained"}, nil
				}
				environment.manifest.Config.SystemPromptContent = "new prompt"
				environment.manifest.Tools = []agentenv.ToolDefinition{{Name: "file_read", Tool: &tools.FileReadTool{}}}
				return agentenv.ReloadOutcome{Reloaded: true, Message: "Workspace resources reloaded", Display: "Resources reloaded"}, nil
			}
			thread.newStreamingFunc = func(_ context.Context, params openairesponses.ResponseNewParams, _ ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
				if len(requests) == 1 {
					require.NotEmpty(t, store.savedRecords)
					saved := store.savedRecords[len(store.savedRecords)-1]
					assert.Contains(t, string(saved.RawMessages), "[Resource reload ID:")
					assert.Contains(t, saved.Metadata, conversations.MessageDisplayMetadataKey, "marker must be durable before the next model turn")
				}
				requests = append(requests, params)
				return nil
			}
			thread.processStreamFunc = func(context.Context, *ssestream.Stream[openairesponses.ResponseStreamEventUnion], llmtypes.MessageHandler, string, llmtypes.MessageOpt) (processStreamResult, error) {
				if len(requests) == 1 {
					environment.pending = true
					thread.SetMetadataValue("allowed_tools", []string{"bash"})
					thread.AddAssistantMessage(t.Context(), "requesting reload")
					return processStreamResult{complete: func(context.Context) (processStreamResult, error) {
						batchComplete = true
						return processStreamResult{responseCompleted: true, toolsUsed: true}, nil
					}, responseCompleted: true}, nil
				}
				assert.False(t, handler.done)
				return processStreamResult{responseCompleted: true}, nil
			}
			_, err := thread.SendMessage(t.Context(), "reload and continue", handler, llmtypes.MessageOpt{MaxTurns: test.maxTurns})
			assert.Equal(t, 1, environment.opened, "reload must not reopen the run")
			assert.Equal(t, config.AllowedTools, thread.GetConfig().AllowedTools)
			assert.Equal(t, config.Model, thread.GetConfig().Model)
			assert.Equal(t, "same-conversation", thread.GetConversationID())
			if test.failed {
				assert.Zero(t, handler.reloads)
			} else {
				assert.Equal(t, 1, handler.reloads)
				assert.NotEmpty(t, handler.reloadID)
				assert.Empty(t, handler.userMessages, "outcomes are host markers, not visible user messages")
			}
			if !test.refused && !test.failed {
				assert.NotContains(t, thread.GetMetadata(), "allowed_tools")
			}
			if test.failed {
				require.ErrorContains(t, err, "uncertain reload")
				assert.Len(t, requests, 1)
				assert.False(t, handler.done)
				return
			}
			require.NoError(t, err)
			assert.True(t, handler.done)
			require.Len(t, requests, test.maxTurns)
			assert.Equal(t, "old prompt", requests[0].Instructions.Value)
			if test.maxTurns == 2 {
				wantPrompt, wantTool, feedback := "new prompt", "file_read", "Workspace resources reloaded"
				wantKind, wantDisplay := "resources-reloaded", "Resources reloaded"
				if test.refused {
					wantPrompt, wantTool, feedback = "old prompt", "bash", "Reload refused: background workers retained"
					wantKind, wantDisplay = "resource-reload-refused", "Resource reload refused: workers retained"
				}
				assert.Equal(t, wantPrompt, requests[1].Instructions.Value)
				require.Len(t, requests[1].Tools, 1)
				assert.Equal(t, wantTool, requests[1].Tools[0].OfFunction.Name)
				modelFeedback := extractInputItemText(requests[1].Input.OfInputItemList[len(requests[1].Input.OfInputItemList)-1])
				assert.Contains(t, modelFeedback, feedback)
				display := conversations.ApplyDisplayToStreamableMessages([]conversations.StreamableMessage{{Kind: "text", Role: "user", Content: modelFeedback}}, thread.GetMetadata())
				require.Len(t, display, 1)
				assert.Equal(t, wantKind, display[0].Kind)
				assert.Equal(t, wantKind, handler.reloadKind)
				assert.Equal(t, handler.reloadID, display[0].ResourceReloadID)
				assert.Equal(t, wantDisplay, display[0].Content)
				assert.Equal(t, wantDisplay, handler.reloadText)
			}
		})
	}
}

type cancelCheckpointEnvironment struct {
	agentenv.UtilityEnvironment
	cancel context.CancelFunc
}

type finishedEnvironment struct {
	agentenv.UtilityEnvironment
	onStart   func()
	followUps int
	outcomes  []llmtypes.AgentRunOutcome
	closeErr  error
}

func (e *finishedEnvironment) DispatchAgentStart(context.Context) error {
	if e.onStart != nil {
		e.onStart()
	}
	return nil
}

func (e *finishedEnvironment) DispatchAgentEnd(context.Context, []llmtypes.Message) ([]string, error) {
	if e.followUps > 0 {
		e.followUps--
		return []string{"continue"}, nil
	}
	return nil, nil
}

func (e *finishedEnvironment) CloseWithOutcome(ctx context.Context, outcome llmtypes.AgentRunOutcome) error {
	e.closeErr = ctx.Err()
	e.outcomes = append(e.outcomes, outcome)
	return e.Close(ctx)
}

func TestResponsesReportsTerminationOutcome(t *testing.T) {
	for _, test := range []struct {
		name          string
		exchangeErr   error
		cancelAtStart bool
		toolsUsed     bool
		followUps     int
		status        llmtypes.AgentRunStatus
		reason        llmtypes.AgentRunReason
		exchanges     int
	}{
		{name: "completed", status: "succeeded", reason: "completed", exchanges: 1},
		{name: "followup then completed", followUps: 1, status: "succeeded", reason: "completed", exchanges: 2},
		{name: "turn limit", toolsUsed: true, status: "succeeded", reason: "turn_limit", exchanges: 2},
		{name: "error", exchangeErr: errors.New("provider failed"), status: "failed", reason: "error", exchanges: 1},
		{name: "cancel before first turn", cancelAtStart: true, status: "canceled", reason: "canceled"},
		{name: "provider cancellation without canceled parent", exchangeErr: context.Canceled, status: "canceled", reason: "canceled", exchanges: 1},
		{name: "deadline", exchangeErr: context.DeadlineExceeded, status: "canceled", reason: "deadline_exceeded", exchanges: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			environment := &finishedEnvironment{followUps: test.followUps}
			if test.cancelAtStart {
				environment.onStart = cancel
			}
			config := llmtypes.Config{Provider: "openai", Model: "gpt-4.1"}
			thread := &Thread{Thread: base.NewThread(config, "finished-conversation")}
			thread.SetEnvironment(environment)
			exchanges := 0
			thread.processMessageExchangeFunc = func(context.Context, llmtypes.MessageHandler, string, int, string, llmtypes.MessageOpt) (string, bool, bool, error) {
				exchanges++
				assert.Empty(t, environment.outcomes, "follow-ups are not terminal")
				return "done", test.toolsUsed, true, test.exchangeErr
			}
			_, err := thread.SendMessage(ctx, "hello", &llmtypes.StringCollectorHandler{Silent: true}, llmtypes.MessageOpt{MaxTurns: 2, NoSaveConversation: true})
			if test.exchangeErr != nil && !errors.Is(test.exchangeErr, context.Canceled) {
				require.ErrorIs(t, err, test.exchangeErr)
			} else {
				require.NoError(t, err)
			}
			require.Len(t, environment.outcomes, 1)
			assert.Equal(t, test.status, environment.outcomes[0].Status)
			assert.Equal(t, test.reason, environment.outcomes[0].Reason)
			assert.Equal(t, test.exchanges, exchanges)
			assert.NoError(t, environment.closeErr, "cleanup must not inherit cancellation")
			assert.False(t, environment.IsOpen())
		})
	}
}

func (e *cancelCheckpointEnvironment) DispatchAgentStart(context.Context) error {
	e.cancel()
	return nil
}

func TestResponsesCancellationBeforeAppendPreservesAdmissionCheckpoint(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	thread := &Thread{Thread: base.NewThread(llmtypes.Config{Provider: "openai", Model: "gpt-4.1"}, "conversation")}
	store := &mockResponsesConversationStore{}
	thread.Store, thread.Persisted = store, true
	thread.SetEnvironment(&cancelCheckpointEnvironment{cancel: cancel})
	thread.AddUserMessage(ctx, "previous")
	require.NoError(t, thread.SavePendingUserMessage(ctx, "admitted input"))
	require.Len(t, store.savedRecords, 1)
	assert.Contains(t, string(store.savedRecords[0].RawMessages), "admitted input")
	_, err := thread.SendMessage(ctx, "admitted input", &llmtypes.StringCollectorHandler{Silent: true}, llmtypes.MessageOpt{})
	require.NoError(t, err)
	assert.ErrorIs(t, ctx.Err(), context.Canceled)
	assert.Len(t, store.savedRecords, 1, "cancelled pre-append context must not overwrite the durable checkpoint")
	history := thread.inputItemsSnapshot()
	require.Len(t, history, 1, "pre-turn compaction ordering must still exclude incoming input")
	assert.Equal(t, "previous", extractInputItemText(history[0]))
}

func TestSavePendingUserMessagePreservesCompactionHistory(t *testing.T) {
	thread := &Thread{Thread: base.NewThread(llmtypes.Config{Provider: "openai", Model: "gpt-4.1"}, "compacted-checkpoint")}
	thread.AddUserMessage(t.Context(), "original request")
	require.NoError(t, thread.SwapContext(t.Context(), "summary"))
	archive := thread.CompactionHistory.Clone()
	original := thread.snapshotHistory()
	store := &mockResponsesConversationStore{}
	thread.Store, thread.Persisted = store, true

	require.NoError(t, thread.SavePendingUserMessage(t.Context(), "pending input"))
	require.Len(t, store.savedRecords, 1)
	saved := store.savedRecords[0]
	assert.Equal(t, archive, saved.CompactionHistory)
	assert.NotSame(t, thread.CompactionHistory, saved.CompactionHistory)
	assert.Contains(t, string(saved.RawMessages), "pending input")
	assert.NotContains(t, string(saved.CompactionHistory.Segments[0].RawMessages), "pending input")
	require.NoError(t, saved.CompactionHistory.Validate(saved.RawMessages))
	assert.Equal(t, archive, thread.CompactionHistory)
	assert.Equal(t, original.storedItems, thread.snapshotHistory().storedItems)
	assert.Equal(t, original.inputItems, thread.inputItemsSnapshot())
}

func TestSavePendingUserMessageRestoresStateAndPreservesExternalAppend(t *testing.T) {
	for _, tt := range []struct {
		name    string
		saveErr error
	}{
		{name: "success"},
		{name: "save error", saveErr: errors.New("save failed")},
		{name: "cancelled", saveErr: context.Canceled},
	} {
		t.Run(tt.name, func(t *testing.T) {
			thread := &Thread{
				Thread:                base.NewThread(llmtypes.Config{Provider: "openai", Model: "gpt-4.1"}, "checkpoint"),
				codexWindowGeneration: 7,
				webSocketContinuation: responsesWebSocketContinuation{responseID: "previous-response"},
			}
			thread.AddUserMessage(t.Context(), "previous")
			thread.Usage.CurrentContextWindow, thread.Usage.MaxContextWindow = 10, 100
			thread.SetStructuredToolResult("existing-call", tooltypes.StructuredToolResult{ToolName: "bash", Success: true})
			_, _ = thread.pendingReasoning.WriteString("pending reasoning")
			original := thread.snapshotHistory()
			originalUsage, originalResults := thread.GetUsage(), thread.GetStructuredToolResults()
			store := &mockResponsesConversationStore{saveFunc: func(ctx context.Context, _ convtypes.ConversationRecord) error {
				require.NotNil(t, thread.activeNoSaveOperation)
				assert.Same(t, thread.activeNoSaveOperation, ctx.Value(responsesNoSaveOperationContextKey{}))
				assert.Empty(t, thread.activeNoSaveOperation.externalAppends, "the checkpoint input belongs to this operation, not external history")
				locked := thread.operationMu.TryLock()
				if locked {
					thread.operationMu.Unlock()
				}
				assert.False(t, locked, "Responses must own the operation lock during checkpoint persistence")
				thread.AddUserMessage(t.Context(), "external append")
				return tt.saveErr
			}}
			thread.Store, thread.Persisted = store, true

			err := thread.SavePendingUserMessage(t.Context(), "pending", "data:image/png;base64,aGVsbG8=")
			if tt.saveErr != nil {
				require.ErrorIs(t, err, tt.saveErr)
			} else {
				require.NoError(t, err)
			}
			history := thread.snapshotHistory()
			require.Len(t, history.inputItems, 2)
			require.Len(t, history.storedItems, 2)
			assert.Equal(t, original.inputItems[0], history.inputItems[0])
			assert.Equal(t, original.storedItems[0], history.storedItems[0])
			assert.Equal(t, "external append", extractInputItemText(history.inputItems[1]))
			assert.Equal(t, "external append", history.storedItems[1].Content)
			assert.Equal(t, original.codexWindowGeneration, history.codexWindowGeneration)
			assert.Greater(t, history.revision, original.revision)
			assert.Equal(t, originalUsage, thread.GetUsage())
			assert.Equal(t, originalResults, thread.GetStructuredToolResults())
			assert.Equal(t, "pending reasoning", thread.pendingReasoning.String())
			assert.Nil(t, thread.activeNoSaveOperation)
			assert.Empty(t, thread.webSocketContinuation.responseID, "restoring Responses history invalidates continuation state")
			assert.Equal(t, "previous", conversations.AutomaticConversationName(thread.GetMetadata()))
			require.Len(t, store.savedRecords, 1)
			var saved []StoredInputItem
			require.NoError(t, json.Unmarshal(store.savedRecords[0].RawMessages, &saved))
			require.Len(t, saved, 2)
			assert.Equal(t, "pending", saved[1].Content)
			assert.Contains(t, string(saved[1].RawItem), `"image_url":"data:image/png;base64,aGVsbG8="`)
			assert.Equal(t, "checkpoint", store.savedRecords[0].ID)
		})
	}
}
