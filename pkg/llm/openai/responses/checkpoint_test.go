package responses

import (
	"context"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/llm/base"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
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
