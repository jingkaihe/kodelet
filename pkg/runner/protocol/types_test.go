package protocol

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceGitCommitApprovalValidation(t *testing.T) {
	valid := WorkspaceGitCommitParams{CWD: "/runner/repo", Generation: 1, Tree: strings.Repeat("a", 40), Message: "feat: commit"}
	require.NoError(t, valid.Validate(), "unborn HEAD is allowed")
	valid.Head, valid.Tree = strings.Repeat("b", 64), strings.Repeat("a", 64)
	require.NoError(t, valid.Validate(), "SHA-256 repositories are allowed")
	for _, change := range []func(*WorkspaceGitCommitParams){
		func(p *WorkspaceGitCommitParams) { p.Tree = "" },
		func(p *WorkspaceGitCommitParams) { p.Head = "--option" },
		func(p *WorkspaceGitCommitParams) { p.Tree = strings.Repeat("z", 40) },
		func(p *WorkspaceGitCommitParams) { p.CWD = "" },
		func(p *WorkspaceGitCommitParams) { p.Generation = 0 },
		func(p *WorkspaceGitCommitParams) { p.Message = "\x00" },
		func(p *WorkspaceGitCommitParams) { p.Message = strings.Repeat("a", 64*1024+1) },
		func(p *WorkspaceGitCommitParams) { p.HeadRef = "refs/heads/main\n" },
	} {
		invalid := valid
		change(&invalid)
		require.Error(t, invalid.Validate())
	}
}

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
		Capabilities: RunnerCapabilities{
			ConcurrentRuns:    true,
			WorkspaceGitDiff:  true,
			WorkspaceTerminal: true,
		},
		Host:      Host{InstanceID: "host-one"},
		Workspace: Workspace{Path: "/workspace", Name: "workspace"},
	}
	require.NoError(t, valid.Validate())
	assert.True(t, valid.Capabilities.ConcurrentRuns)
	assert.True(t, valid.Capabilities.WorkspaceGitDiff)
	assert.True(t, valid.Capabilities.WorkspaceTerminal)

	unsupported := valid
	unsupported.ProtocolVersions = []int{Version + 1}
	assert.ErrorContains(t, unsupported.Validate(), "does not support")

	missingHost := valid
	missingHost.Host.InstanceID = ""
	assert.ErrorContains(t, missingHost.Validate(), "host.instanceId")
}

func TestMessageAndRPCErrorValidationBranches(t *testing.T) {
	var nilRPCError *RPCError
	assert.Empty(t, nilRPCError.Error())
	assert.Equal(t, "runner rpc error -32600: invalid", (&RPCError{Code: ErrorCodeInvalidRequest, Message: "invalid"}).Error())
	assert.Equal(t, ErrorReasonRunNotActive, (&RPCError{Data: RPCErrorData{Reason: ErrorReasonRunNotActive}}).Reason())
	assert.Equal(t, ErrorReasonRunnerNotFound, (&RPCError{Data: map[string]any{"reason": ErrorReasonRunnerNotFound}}).Reason())
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
	multiple := HeartbeatParams{RunnerID: "runner-one", Generation: 1, State: RunnerStateRunning, ActiveRunIDs: []string{"run-two", "run-one"}}
	require.NoError(t, multiple.Validate())
	runIDs, err := multiple.NormalizedActiveRunIDs()
	require.NoError(t, err)
	assert.Equal(t, []string{"run-one", "run-two"}, runIDs)

	missingRunner := valid
	missingRunner.RunnerID = ""
	require.ErrorContains(t, missingRunner.Validate(), "runnerId")
	missingGeneration := valid
	missingGeneration.Generation = 0
	require.ErrorContains(t, missingGeneration.Validate(), "generation")
	unknown := valid
	unknown.State = RunnerState("future")
	require.ErrorContains(t, unknown.Validate(), "unsupported runner state")
	duplicate := multiple
	duplicate.ActiveRunIDs = []string{"run-one", "run-one"}
	require.ErrorContains(t, duplicate.Validate(), "duplicate run")
	mismatch := multiple
	mismatch.ActiveRunID = "run-three"
	require.ErrorContains(t, mismatch.Validate(), "does not match")
}
