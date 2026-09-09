package responses

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/llm/base"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type cancelCheckpointEnvironment struct {
	agentenv.UtilityEnvironment
	cancel context.CancelFunc
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
