package client

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockRPCCaller struct {
	capabilities *acptypes.ClientCapabilities
	responses    map[string]json.RawMessage
	errors       map[string]error
	calls        []struct {
		Method string
		Params any
	}
}

func (m *mockRPCCaller) CallClient(_ context.Context, method string, params any) (json.RawMessage, error) {
	m.calls = append(m.calls, struct {
		Method string
		Params any
	}{method, params})

	if err, ok := m.errors[method]; ok {
		return nil, err
	}
	if resp, ok := m.responses[method]; ok {
		return resp, nil
	}
	return nil, nil
}

func (m *mockRPCCaller) GetClientCapabilities() *acptypes.ClientCapabilities {
	return m.capabilities
}

func TestClient_HasFSCapability(t *testing.T) {
	tests := []struct {
		name     string
		caps     *acptypes.ClientCapabilities
		expected bool
	}{
		{
			name:     "nil capabilities",
			caps:     nil,
			expected: false,
		},
		{
			name:     "nil FS",
			caps:     &acptypes.ClientCapabilities{},
			expected: false,
		},
		{
			name:     "has FS capability",
			caps:     &acptypes.ClientCapabilities{FS: &acptypes.FSCapabilities{}},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caller := &mockRPCCaller{capabilities: tt.caps}
			client := New(caller)
			assert.Equal(t, tt.expected, client.HasFSCapability())
		})
	}
}

func TestClient_HasReadFileCapability(t *testing.T) {
	tests := []struct {
		name     string
		caps     *acptypes.ClientCapabilities
		expected bool
	}{
		{
			name:     "nil capabilities",
			caps:     nil,
			expected: false,
		},
		{
			name:     "no read capability",
			caps:     &acptypes.ClientCapabilities{FS: &acptypes.FSCapabilities{ReadTextFile: false}},
			expected: false,
		},
		{
			name:     "has read capability",
			caps:     &acptypes.ClientCapabilities{FS: &acptypes.FSCapabilities{ReadTextFile: true}},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caller := &mockRPCCaller{capabilities: tt.caps}
			client := New(caller)
			assert.Equal(t, tt.expected, client.HasReadFileCapability())
		})
	}
}

func TestClient_HasWriteFileCapability(t *testing.T) {
	tests := []struct {
		name     string
		caps     *acptypes.ClientCapabilities
		expected bool
	}{
		{
			name:     "nil capabilities",
			caps:     nil,
			expected: false,
		},
		{
			name:     "no write capability",
			caps:     &acptypes.ClientCapabilities{FS: &acptypes.FSCapabilities{WriteTextFile: false}},
			expected: false,
		},
		{
			name:     "has write capability",
			caps:     &acptypes.ClientCapabilities{FS: &acptypes.FSCapabilities{WriteTextFile: true}},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caller := &mockRPCCaller{capabilities: tt.caps}
			client := New(caller)
			assert.Equal(t, tt.expected, client.HasWriteFileCapability())
		})
	}
}

func TestClient_HasTerminalCapability(t *testing.T) {
	tests := []struct {
		name     string
		caps     *acptypes.ClientCapabilities
		expected bool
	}{
		{
			name:     "nil capabilities",
			caps:     nil,
			expected: false,
		},
		{
			name:     "no terminal capability",
			caps:     &acptypes.ClientCapabilities{Terminal: false},
			expected: false,
		},
		{
			name:     "has terminal capability",
			caps:     &acptypes.ClientCapabilities{Terminal: true},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caller := &mockRPCCaller{capabilities: tt.caps}
			client := New(caller)
			assert.Equal(t, tt.expected, client.HasTerminalCapability())
		})
	}
}

func TestClient_ReadTextFile(t *testing.T) {
	caller := &mockRPCCaller{
		capabilities: &acptypes.ClientCapabilities{
			FS: &acptypes.FSCapabilities{ReadTextFile: true},
		},
		responses: map[string]json.RawMessage{
			"fs/read_text_file": json.RawMessage(`{"text": "file content"}`),
		},
	}

	client := New(caller)
	content, err := client.ReadTextFile(context.Background(), "/test/file.txt")

	require.NoError(t, err)
	assert.Equal(t, "file content", content)
	assert.Len(t, caller.calls, 1)
	assert.Equal(t, "fs/read_text_file", caller.calls[0].Method)
}

func TestClient_ReadTextFile_NoCapability(t *testing.T) {
	caller := &mockRPCCaller{
		capabilities: &acptypes.ClientCapabilities{},
	}

	client := New(caller)
	_, err := client.ReadTextFile(context.Background(), "/test/file.txt")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support reading files")
}

func TestClient_WriteTextFile(t *testing.T) {
	caller := &mockRPCCaller{
		capabilities: &acptypes.ClientCapabilities{
			FS: &acptypes.FSCapabilities{WriteTextFile: true},
		},
		responses: map[string]json.RawMessage{
			"fs/write_text_file": json.RawMessage(`{}`),
		},
	}

	client := New(caller)
	err := client.WriteTextFile(context.Background(), "/test/file.txt", "new content")

	require.NoError(t, err)
	assert.Len(t, caller.calls, 1)
	assert.Equal(t, "fs/write_text_file", caller.calls[0].Method)
}

func TestClient_WriteTextFile_NoCapability(t *testing.T) {
	caller := &mockRPCCaller{
		capabilities: &acptypes.ClientCapabilities{},
	}

	client := New(caller)
	err := client.WriteTextFile(context.Background(), "/test/file.txt", "content")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support writing files")
}

func TestClient_TerminalExecute(t *testing.T) {
	caller := &mockRPCCaller{
		capabilities: &acptypes.ClientCapabilities{Terminal: true},
		responses: map[string]json.RawMessage{
			"terminal/execute": json.RawMessage(`{"exitCode": 0, "stdout": "output", "stderr": ""}`),
		},
	}

	client := New(caller)
	result, err := client.TerminalExecute(context.Background(), "ls -la", "/tmp")

	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, "output", result.Stdout)
}

func TestClient_TerminalExecute_NoCapability(t *testing.T) {
	caller := &mockRPCCaller{
		capabilities: &acptypes.ClientCapabilities{Terminal: false},
	}

	client := New(caller)
	_, err := client.TerminalExecute(context.Background(), "ls", "/tmp")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support terminal execution")
}

func TestClient_RequestPermission(t *testing.T) {
	caller := &mockRPCCaller{
		capabilities: &acptypes.ClientCapabilities{},
		responses: map[string]json.RawMessage{
			"session/request_permission": json.RawMessage(`{"outcome": {"outcome": "selected", "optionId": "approve"}}`),
		},
	}

	client := New(caller)
	result, err := client.RequestPermission(
		context.Background(),
		"session-123",
		"call-1",
		"bash",
		json.RawMessage(`{"command": "rm -rf /"}`),
		"This will delete everything",
		[]acptypes.PermissionOption{
			{ID: "approve", Label: "Approve"},
			{ID: "deny", Label: "Deny"},
		},
	)

	require.NoError(t, err)
	assert.Equal(t, OutcomeSelected, result.Outcome)
	assert.Equal(t, "approve", result.OptionID)
}

func TestClient_RequestApproval(t *testing.T) {
	tests := []struct {
		name     string
		response string
		expected bool
	}{
		{
			name:     "approved",
			response: `{"outcome": {"outcome": "selected", "optionId": "approve"}}`,
			expected: true,
		},
		{
			name:     "denied",
			response: `{"outcome": {"outcome": "selected", "optionId": "deny"}}`,
			expected: false,
		},
		{
			name:     "dismissed",
			response: `{"outcome": {"outcome": "dismissed"}}`,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caller := &mockRPCCaller{
				capabilities: &acptypes.ClientCapabilities{},
				responses: map[string]json.RawMessage{
					"session/request_permission": json.RawMessage(tt.response),
				},
			}

			client := New(caller)
			approved, err := client.RequestApproval(
				context.Background(),
				"session-123",
				"call-1",
				"bash",
				nil,
				"Continue?",
			)

			require.NoError(t, err)
			assert.Equal(t, tt.expected, approved)
		})
	}
}
