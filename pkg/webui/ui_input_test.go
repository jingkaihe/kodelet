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
