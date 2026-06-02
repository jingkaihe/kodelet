package webui

import (
	"context"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebUIInputBrokerSendsEventAndWaitsForResponse(t *testing.T) {
	sink := &recordingChatSink{}
	broker := newWebUIInputBroker("conv-123", sink)

	resultCh := make(chan extensions.UIInputResponse, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := broker.Input(context.Background(), extensions.UIInputRequest{
			ID:               "input-1",
			Title:            "Choose",
			HelpText:         "1. A\n2. B",
			SubmitButtonText: "Select",
		})
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()

	require.Eventually(t, func() bool { return len(sink.events) == 1 }, time.Second, 10*time.Millisecond)
	assert.Equal(t, "ui-input-request", sink.events[0].Kind)
	require.NotNil(t, sink.events[0].UIInput)
	assert.Equal(t, "input-1", sink.events[0].UIInput.ID)
	assert.Equal(t, "Choose", sink.events[0].UIInput.Title)
	assert.Equal(t, "Select", sink.events[0].UIInput.SubmitButtonText)

	assert.True(t, broker.Respond("input-1", extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted, Value: "2"}))

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case result := <-resultCh:
		assert.Equal(t, extensions.UIInputStatusSubmitted, result.Status)
		assert.Equal(t, "2", result.Value)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ui input response")
	}
}

func TestWebUIInputBrokerSendsSeparateConfirmSelectAndNotifyEvents(t *testing.T) {
	sink := &recordingChatSink{}
	broker := newWebUIInputBroker("conv-123", sink)

	confirmCh := make(chan extensions.UIInputResponse, 1)
	go func() {
		result, _ := broker.Confirm(context.Background(), extensions.UIConfirmRequest{
			ID:                "confirm-1",
			Title:             "Allow bash?",
			Message:           "A tool call incoming",
			ConfirmButtonText: "Allow",
		})
		confirmCh <- result
	}()
	require.Eventually(t, func() bool { return len(sink.events) == 1 }, time.Second, 10*time.Millisecond)
	assert.Equal(t, "ui-confirm-request", sink.events[0].Kind)
	require.NotNil(t, sink.events[0].UIConfirm)
	assert.Equal(t, "confirm-1", sink.events[0].UIConfirm.ID)
	assert.Equal(t, "Allow", sink.events[0].UIConfirm.ConfirmButtonText)
	assert.True(t, broker.Respond("confirm-1", extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted, Confirmed: true}))
	assert.True(t, (<-confirmCh).Confirmed)

	selectCh := make(chan extensions.UIInputResponse, 1)
	go func() {
		result, _ := broker.Select(context.Background(), extensions.UISelectRequest{
			ID:      "select-1",
			Title:   "Pick food",
			Options: []string{"Pasta", "Pizza"},
		})
		selectCh <- result
	}()
	require.Eventually(t, func() bool { return len(sink.events) == 2 }, time.Second, 10*time.Millisecond)
	assert.Equal(t, "ui-select-request", sink.events[1].Kind)
	require.NotNil(t, sink.events[1].UISelect)
	assert.Equal(t, []string{"Pasta", "Pizza"}, sink.events[1].UISelect.Options)
	assert.True(t, broker.Respond("select-1", extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted, Value: "Pizza"}))
	assert.Equal(t, "Pizza", (<-selectCh).Value)

	result, err := broker.Notify(context.Background(), extensions.UINotifyRequest{Message: "Done"})
	require.NoError(t, err)
	assert.Equal(t, extensions.UIInputStatusSubmitted, result.Status)
	require.Len(t, sink.events, 3)
	assert.Equal(t, "ui-notification", sink.events[2].Kind)
	require.NotNil(t, sink.events[2].UINotify)
	assert.Equal(t, "Done", sink.events[2].UINotify.Message)
}
