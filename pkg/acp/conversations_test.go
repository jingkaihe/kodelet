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
			return chat.ConversationStatus{ID: id, Running: true}, nil
		},
		stopTurn: func(id, turnID string) { stops = append(stops, id+"/"+turnID) },
	}
	output := new(bytes.Buffer)
	server := newRemoteACPTestServer(t, "", client, output)
	request := func(method, params string) map[string]any {
		t.Helper()
		require.NoError(t, server.handleRequest(mustJSONRawMessage(t, acptypes.Request{
			JSONRPC: "2.0", ID: json.RawMessage("7"), Method: method, Params: json.RawMessage(params),
		})))
		return readJSONRPCMessage(t, output)
	}

	init := request("initialize", `{"protocolVersion":1}`)
	meta := init["result"].(map[string]any)["_meta"].(map[string]any)
	assert.Equal(t, map[string]any{"version": float64(1)}, meta["conversations"])
	response := request("_conversation/status", `{"conversationId":" known "}`)
	status := response["result"].(map[string]any)
	assert.Equal(t, "known", status["id"])
	assert.Equal(t, true, status["running"])

	assertRPCErrorCode(t, request("_conversation/status", `{"conversationId":"missing"}`), acptypes.ErrCodeResourceNotFound)
	assertRPCErrorCode(t, request("_conversation/status", `{}`), acptypes.ErrCodeInvalidParams)

	response = request("_conversation/stop", `{"conversationId":"known","turnId":"turn-1"}`)
	assert.Equal(t, map[string]any{}, response["result"])
	request("_conversation/stop", `{"conversationId":"known"}`)
	assert.Equal(t, []string{"known/turn-1", "known/"}, stops)
}
