package protocol

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeMessageValidatesEnvelope(t *testing.T) {
	id := "runner:1"
	request, err := json.Marshal(Message{
		JSONRPC: JSONRPCVersion,
		ID:      &id,
		Method:  MethodRunnerRegister,
		Params:  json.RawMessage(`{"protocolVersions":[1]}`),
	})
	require.NoError(t, err)

	decoded, err := DecodeMessage(request)
	require.NoError(t, err)
	assert.Equal(t, MethodRunnerRegister, decoded.Method)
	assert.Equal(t, id, *decoded.ID)

	_, err = DecodeMessage([]byte(`{"jsonrpc":"1.0","method":"runner.register"}`))
	assert.ErrorContains(t, err, "unsupported jsonrpc version")

	_, err = DecodeMessage([]byte(`{"jsonrpc":"2.0"}`))
	assert.ErrorContains(t, err, "response id is required")
}

func TestRegisterParamsValidate(t *testing.T) {
	valid := RegisterParams{
		ProtocolVersions: []int{Version},
		Host:             Host{InstanceID: "host-one"},
		Workspace:        Workspace{Path: "/workspace", Name: "workspace"},
	}
	require.NoError(t, valid.Validate())

	unsupported := valid
	unsupported.ProtocolVersions = []int{Version + 1}
	assert.ErrorContains(t, unsupported.Validate(), "does not support")

	missingHost := valid
	missingHost.Host.InstanceID = ""
	assert.ErrorContains(t, missingHost.Validate(), "host.instanceId")
}

func TestComputeManifestDigestIgnoresExistingDigest(t *testing.T) {
	manifest := Manifest{
		ProtocolVersion: Version,
		RunnerID:        "runner-one",
		RunID:           "run-one",
		Tools: []ToolDefinition{{
			Name:        "bash",
			Description: "execute a command",
			InputSchema: map[string]any{"type": "object"},
			Placement:   "environment",
		}},
	}

	first, err := ComputeManifestDigest(manifest)
	require.NoError(t, err)
	manifest.Digest = "stale"
	second, err := ComputeManifestDigest(manifest)
	require.NoError(t, err)
	assert.Equal(t, first, second)
	assert.Regexp(t, `^sha256:[0-9a-f]{64}$`, first)
}

func TestMessageAndRPCErrorValidationBranches(t *testing.T) {
	var nilRPCError *RPCError
	assert.Empty(t, nilRPCError.Error())
	assert.Equal(t, "runner rpc error -32600: invalid", (&RPCError{Code: ErrorCodeInvalidRequest, Message: "invalid"}).Error())
	_, err := DecodeMessage([]byte(`not-json`))
	require.ErrorContains(t, err, "decode runner rpc message")

	emptyID := " "
	validID := "rpc:1"
	tests := []struct {
		name      string
		message   Message
		wantError string
	}{
		{name: "empty id", message: Message{JSONRPC: JSONRPCVersion, ID: &emptyID, Method: "call"}, wantError: "id must not be empty"},
		{name: "request result", message: Message{JSONRPC: JSONRPCVersion, ID: &validID, Method: "call", Result: json.RawMessage(`{}`)}, wantError: "request cannot contain"},
		{name: "request error", message: Message{JSONRPC: JSONRPCVersion, ID: &validID, Method: "call", Error: &RPCError{}}, wantError: "request cannot contain"},
		{name: "response both", message: Message{JSONRPC: JSONRPCVersion, ID: &validID, Result: json.RawMessage(`{}`), Error: &RPCError{}}, wantError: "both result and error"},
		{name: "response neither", message: Message{JSONRPC: JSONRPCVersion, ID: &validID}, wantError: "must contain result or error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.ErrorContains(t, test.message.Validate(), test.wantError)
		})
	}
}

func TestRegistrationAndRunOpenValidationBranches(t *testing.T) {
	valid := RegisterParams{
		ProtocolVersions: []int{Version},
		Host:             Host{InstanceID: "host-one"},
		Workspace:        Workspace{Path: "/workspace", Name: "workspace"},
	}
	missingPath := valid
	missingPath.Workspace.Path = ""
	require.ErrorContains(t, missingPath.Validate(), "workspace.path")
	missingName := valid
	missingName.Workspace.Name = ""
	require.ErrorContains(t, missingName.Validate(), "workspace.name")

	require.ErrorContains(t, (RunOpenParams{}).Validate(), "runId")
	require.ErrorContains(t, (RunOpenParams{RunID: "run-one"}).Validate(), "conversationId")
	require.NoError(t, (RunOpenParams{RunID: "run-one", ConversationID: "conversation-one"}).Validate())
}

func TestHeartbeatParamsValidate(t *testing.T) {
	valid := HeartbeatParams{RunnerID: "runner-one", Generation: 1, State: RunnerStateIdle}
	require.NoError(t, valid.Validate())

	missingRunner := valid
	missingRunner.RunnerID = ""
	require.ErrorContains(t, missingRunner.Validate(), "runnerId")
	missingGeneration := valid
	missingGeneration.Generation = 0
	require.ErrorContains(t, missingGeneration.Validate(), "generation")
	unknown := valid
	unknown.State = RunnerState("future")
	require.ErrorContains(t, unknown.Validate(), "unsupported runner state")
}
