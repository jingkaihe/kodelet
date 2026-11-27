package agent

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/jingkaihe/kodelet/sdk/go/internal/jsonrpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func conversationFake(t *testing.T, respond func(f *fakeACP, message jsonrpc.Message)) *fakeACP {
	fake := newFakeACP(t)
	fake.meta["conversations"] = map[string]any{"version": 1}
	fake.handle = func(f *fakeACP, message jsonrpc.Message) bool {
		if message.Method != conversationStatusMethod && message.Method != conversationStopMethod {
			return false
		}
		respond(f, message)
		return true
	}
	return fake
}

func TestGetConversationStatusUsesAShortLivedACPProcess(t *testing.T) {
	fake := conversationFake(t, func(f *fakeACP, message jsonrpc.Message) {
		f.respond(message.ID, map[string]any{
			"id":         "conv-9",
			"running":    true,
			"usage":      map[string]any{"inputTokens": 12, "totalCost": 0.5},
			"latestTurn": map[string]any{"id": "turn-1", "status": "running", "createdAt": "2026-11-27T10:00:00Z", "updatedAt": "2026-11-27T10:00:01Z"},
		})
	})
	client := fake.client(ClientOptions{Server: "http://daemon", Runner: "workstation"})
	status, err := client.GetConversationStatus(t.Context(), " conv-9 ")
	require.NoError(t, err)

	assert.Equal(t, []string{"acp", "--server", "http://daemon"}, fake.args, "status does not select a runner")
	request := fake.nextRequest(conversationStatusMethod)
	assert.JSONEq(t, `{"conversationId":"conv-9"}`, string(request.Params))
	assert.Equal(t, "conv-9", status.ID)
	assert.True(t, status.Running)
	assert.Equal(t, 12, status.Usage.InputTokens)
	require.NotNil(t, status.LatestTurn)
	assert.Equal(t, TurnRunning, status.LatestTurn.Status)
	assert.False(t, status.LatestTurn.Terminal())
	assert.Nil(t, status.CreatedAt)
	select {
	case <-fake.proc.Done():
	default:
		t.Fatal("the ACP process outlived the call")
	}
}

func TestGetConversationStatusReportsMissingConversations(t *testing.T) {
	fake := conversationFake(t, func(f *fakeACP, message jsonrpc.Message) {
		f.respondError(message.ID, codeResourceNotFound, "conversation not found")
	})
	_, err := fake.client(ClientOptions{}).GetConversationStatus(t.Context(), "gone")
	require.ErrorIs(t, err, ErrConversationNotFound)
	assert.Contains(t, err.Error(), "gone")

	_, err = NewClient(ClientOptions{}).GetConversationStatus(t.Context(), " ")
	require.Error(t, err)
}

func TestConversationMethodsRequireSupport(t *testing.T) {
	fake := newFakeACP(t)
	err := fake.client(ClientOptions{}).StopConversation(t.Context(), "conv-1", StopConversationOptions{})
	require.ErrorIs(t, err, ErrConversationsUnsupported)
}

func TestStopConversationSendsTheTurn(t *testing.T) {
	fake := conversationFake(t, func(f *fakeACP, message jsonrpc.Message) {
		f.respond(message.ID, map[string]any{})
	})
	require.NoError(t, fake.client(ClientOptions{}).StopConversation(t.Context(), "conv-1", StopConversationOptions{TurnID: "turn-2"}))
	request := fake.nextRequest(conversationStopMethod)
	var params map[string]any
	require.NoError(t, json.Unmarshal(request.Params, &params))
	assert.Equal(t, map[string]any{"conversationId": "conv-1", "turnId": "turn-2"}, params)

	failing := conversationFake(t, func(f *fakeACP, message jsonrpc.Message) {
		f.respondError(message.ID, jsonrpc.CodeServerError, "server unreachable")
	})
	err := failing.client(ClientOptions{}).StopConversation(t.Context(), "conv-1", StopConversationOptions{})
	var rpcErr *RPCError
	require.True(t, errors.As(err, &rpcErr))
	assert.Equal(t, "server unreachable", rpcErr.Message)
}
