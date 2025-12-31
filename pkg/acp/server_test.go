package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServer_Initialize(t *testing.T) {
	input := bytes.NewBuffer(nil)
	output := bytes.NewBuffer(nil)

	server := NewServer(
		WithInput(input),
		WithOutput(output),
		WithContext(context.Background()),
	)

	initReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": 1,
			"clientCapabilities": map[string]any{
				"fs": map[string]any{
					"readTextFile":  true,
					"writeTextFile": true,
				},
				"terminal": true,
			},
			"clientInfo": map[string]any{
				"name":    "test-client",
				"version": "1.0.0",
			},
		},
	}

	reqData, err := json.Marshal(initReq)
	require.NoError(t, err)
	reqData = append(reqData, '\n')

	err = server.handleMessage(reqData)
	require.NoError(t, err)

	scanner := bufio.NewScanner(output)
	require.True(t, scanner.Scan())

	var resp map[string]any
	err = json.Unmarshal(scanner.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, "2.0", resp["jsonrpc"])
	assert.NotNil(t, resp["result"])
	assert.Nil(t, resp["error"])

	result := resp["result"].(map[string]any)
	assert.Equal(t, float64(1), result["protocolVersion"])
	assert.NotNil(t, result["agentCapabilities"])
	assert.NotNil(t, result["agentInfo"])

	agentInfo := result["agentInfo"].(map[string]any)
	assert.Equal(t, "kodelet", agentInfo["name"])
	assert.Equal(t, "Kodelet", agentInfo["title"])

	assert.True(t, server.initialized)
	assert.NotNil(t, server.clientCaps)
	assert.True(t, server.clientCaps.Terminal)
}

func TestServer_SessionNew_NotInitialized(t *testing.T) {
	input := bytes.NewBuffer(nil)
	output := bytes.NewBuffer(nil)

	server := NewServer(
		WithInput(input),
		WithOutput(output),
		WithContext(context.Background()),
	)

	sessionReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "session/new",
		"params": map[string]any{
			"cwd": "/test",
		},
	}

	reqData, err := json.Marshal(sessionReq)
	require.NoError(t, err)
	reqData = append(reqData, '\n')

	err = server.handleMessage(reqData)
	require.NoError(t, err)

	scanner := bufio.NewScanner(output)
	require.True(t, scanner.Scan())

	var resp map[string]any
	err = json.Unmarshal(scanner.Bytes(), &resp)
	require.NoError(t, err)

	assert.NotNil(t, resp["error"])
	errObj := resp["error"].(map[string]any)
	assert.Equal(t, float64(acptypes.ErrCodeInternalError), errObj["code"])
	assert.Contains(t, errObj["message"], "Not initialized")
}

func TestServer_UnknownMethod(t *testing.T) {
	input := bytes.NewBuffer(nil)
	output := bytes.NewBuffer(nil)

	server := NewServer(
		WithInput(input),
		WithOutput(output),
		WithContext(context.Background()),
	)

	unknownReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "unknown/method",
		"params":  map[string]any{},
	}

	reqData, err := json.Marshal(unknownReq)
	require.NoError(t, err)
	reqData = append(reqData, '\n')

	err = server.handleMessage(reqData)
	require.NoError(t, err)

	scanner := bufio.NewScanner(output)
	require.True(t, scanner.Scan())

	var resp map[string]any
	err = json.Unmarshal(scanner.Bytes(), &resp)
	require.NoError(t, err)

	assert.NotNil(t, resp["error"])
	errObj := resp["error"].(map[string]any)
	assert.Equal(t, float64(acptypes.ErrCodeMethodNotFound), errObj["code"])
}

func TestServer_ParseError(t *testing.T) {
	input := bytes.NewBuffer(nil)
	output := bytes.NewBuffer(nil)

	server := NewServer(
		WithInput(input),
		WithOutput(output),
		WithContext(context.Background()),
	)

	invalidJSON := []byte("not valid json\n")

	err := server.handleMessage(invalidJSON)
	require.NoError(t, err)

	scanner := bufio.NewScanner(output)
	require.True(t, scanner.Scan())

	var resp map[string]any
	err = json.Unmarshal(scanner.Bytes(), &resp)
	require.NoError(t, err)

	assert.NotNil(t, resp["error"])
	errObj := resp["error"].(map[string]any)
	assert.Equal(t, float64(acptypes.ErrCodeParseError), errObj["code"])
}

func TestServer_Authenticate(t *testing.T) {
	input := bytes.NewBuffer(nil)
	output := bytes.NewBuffer(nil)

	server := NewServer(
		WithInput(input),
		WithOutput(output),
		WithContext(context.Background()),
	)

	authReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "authenticate",
		"params":  map[string]any{},
	}

	reqData, err := json.Marshal(authReq)
	require.NoError(t, err)
	reqData = append(reqData, '\n')

	err = server.handleMessage(reqData)
	require.NoError(t, err)

	scanner := bufio.NewScanner(output)
	require.True(t, scanner.Scan())

	var resp map[string]any
	err = json.Unmarshal(scanner.Bytes(), &resp)
	require.NoError(t, err)

	assert.NotNil(t, resp["result"])
	assert.Nil(t, resp["error"])
}

func TestServer_SendUpdate(t *testing.T) {
	input := bytes.NewBuffer(nil)
	output := bytes.NewBuffer(nil)

	server := NewServer(
		WithInput(input),
		WithOutput(output),
		WithContext(context.Background()),
	)

	sessionID := acptypes.SessionID("test-session")
	update := map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content": map[string]any{
			"type": "text",
			"text": "Hello",
		},
	}

	err := server.SendUpdate(sessionID, update)
	require.NoError(t, err)

	scanner := bufio.NewScanner(output)
	require.True(t, scanner.Scan())

	var notif map[string]any
	err = json.Unmarshal(scanner.Bytes(), &notif)
	require.NoError(t, err)

	assert.Equal(t, "2.0", notif["jsonrpc"])
	assert.Equal(t, "session/update", notif["method"])
	assert.Nil(t, notif["id"])

	params := notif["params"].(map[string]any)
	assert.Equal(t, "test-session", params["sessionId"])
	assert.NotNil(t, params["update"])
}

func TestServer_Notification_Cancel(t *testing.T) {
	input := bytes.NewBuffer(nil)
	output := bytes.NewBuffer(nil)

	server := NewServer(
		WithInput(input),
		WithOutput(output),
		WithContext(context.Background()),
	)

	cancelNotif := map[string]any{
		"jsonrpc": "2.0",
		"method":  "session/cancel",
		"params": map[string]any{
			"sessionId": "nonexistent-session",
		},
	}

	reqData, err := json.Marshal(cancelNotif)
	require.NoError(t, err)
	reqData = append(reqData, '\n')

	err = server.handleMessage(reqData)
	assert.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "session not found"))
}

func TestServer_Shutdown(t *testing.T) {
	input := bytes.NewBuffer(nil)
	output := bytes.NewBuffer(nil)

	server := NewServer(
		WithInput(input),
		WithOutput(output),
		WithContext(context.Background()),
	)

	assert.NotNil(t, server.ctx)
	assert.NotNil(t, server.cancel)

	server.Shutdown()

	select {
	case <-server.ctx.Done():
	default:
		t.Error("context should be cancelled after Shutdown")
	}
}
