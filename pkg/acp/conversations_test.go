package acp

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	"github.com/jingkaihe/kodelet/pkg/chat"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConversationMethodsUseTheServerWithoutASession(t *testing.T) {
	var stops []string
	client := &fakeRemoteChatClient{
		status: func(id string) (chat.ConversationStatus, error) {
			if id != "known" {
				return chat.ConversationStatus{}, errors.Wrapf(convtypes.ErrConversationNotFound, "%s", id)
			}
			return chat.ConversationStatus{
				ID:         id,
				Running:    true,
				LatestTurn: &chat.ConversationTurn{ID: "turn-1", Status: "running"},
			}, nil
		},
		stopTurn: func(id, turnID string) { stops = append(stops, id+"/"+turnID) },
	}
	output := bytes.NewBuffer(nil)
	server := newRemoteACPTestServer(t, t.TempDir(), client, output)
	request := func(method string, params any) map[string]any {
		t.Helper()
		data, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 7, "method": method, "params": params})
		require.NoError(t, err)
		require.NoError(t, server.handleRequest(data))
		messages := readJSONRPCMessages(t, output)
		require.Len(t, messages, 1)
		return messages[0]
	}

	response := request(acptypes.ConversationStatusMethod, acptypes.ConversationStatusRequest{ConversationID: " known "})
	status := response["result"].(map[string]any)
	assert.Equal(t, "known", status["id"])
	assert.Equal(t, true, status["running"])
	assert.Equal(t, "running", status["latestTurn"].(map[string]any)["status"])

	assertRPCErrorCode(t, request(acptypes.ConversationStatusMethod, acptypes.ConversationStatusRequest{ConversationID: "missing"}), acptypes.ErrCodeResourceNotFound)
	assertRPCErrorCode(t, request(acptypes.ConversationStatusMethod, acptypes.ConversationStatusRequest{}), acptypes.ErrCodeInvalidParams)

	response = request(acptypes.ConversationStopMethod, acptypes.ConversationStopRequest{ConversationID: "known", TurnID: "turn-1"})
	assert.Equal(t, map[string]any{}, response["result"])
	request(acptypes.ConversationStopMethod, acptypes.ConversationStopRequest{ConversationID: "known"})
	assert.Equal(t, []string{"known/turn-1", "known/"}, stops)
}

func TestConversationMethodsAreAdvertisedOnlyForServerSessions(t *testing.T) {
	output := bytes.NewBuffer(nil)
	server := newRemoteACPTestServer(t, t.TempDir(), &fakeRemoteChatClient{}, output)
	require.NoError(t, server.handleInitialize(&acptypes.Request{ID: json.RawMessage("1"), Params: json.RawMessage(`{"protocolVersion":1}`)}))
	messages := readJSONRPCMessages(t, output)
	require.Len(t, messages, 1)
	meta := messages[0]["result"].(map[string]any)["_meta"].(map[string]any)
	assert.Equal(t, map[string]any{"version": float64(1)}, meta["conversations"])
}
