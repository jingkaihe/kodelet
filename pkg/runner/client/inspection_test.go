package client

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceInspectionUsesProfilePolicyWithoutStartingExtensions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	startup, selected := filepath.Join(root, "startup"), filepath.Join(root, "selected")
	require.NoError(t, os.MkdirAll(startup, 0o700))
	extPath := filepath.Join(selected, ".kodelet", "extensions", "kodelet-extension-poison")
	require.NoError(t, os.MkdirAll(filepath.Dir(extPath), 0o700))
	require.NoError(t, os.WriteFile(extPath, []byte("#!/bin/sh\ntouch unexpected-startup\n"), 0o700))
	loaded := 0
	service, err := NewService(t.Context(), startup, ServiceOptions{ProfileConfigLoader: func(cwd, model, environment string) (llmtypes.Config, error) {
		loaded++
		assert.Equal(t, selected, cwd)
		assert.Equal(t, "model", model)
		assert.Equal(t, "restricted", environment)
		return llmtypes.Config{ExtensionSettings: map[string]any{"enabled": false}}, nil
	}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	params := protocol.WorkspaceInspectParams{Operation: "extension.list", CWD: selected, Profile: "model", EnvironmentProfile: "restricted"}
	result := callService[protocol.WorkspaceInspectResult](t, service, protocol.MethodWorkspaceInspect, params)
	assert.Equal(t, selected, result.CWD)
	assert.Empty(t, result.Extensions)
	assert.Equal(t, 1, loaded)
	assert.NoFileExists(t, filepath.Join(selected, "unexpected-startup"))
	params.Operation, params.Name = "extension.inspect", "poison"
	_, rpcErr := service.HandleRequest(t.Context(), protocol.MethodWorkspaceInspect, mustJSON(t, params))
	require.NotNil(t, rpcErr)
	assert.Contains(t, rpcErr.Message, "extension not found")
	params.Operation, params.Name, params.CWD = "recipe.show", "missing", filepath.Join(root, "missing")
	_, rpcErr = service.HandleRequest(t.Context(), protocol.MethodWorkspaceInspect, mustJSON(t, params))
	require.NotNil(t, rpcErr)
	assert.Contains(t, rpcErr.Message, "does not exist")
	params.Operation = "unknown"
	_, rpcErr = service.HandleRequest(t.Context(), protocol.MethodWorkspaceInspect, mustJSON(t, params))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeInvalidParams, rpcErr.Code)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	params.Operation = "recipe.show"
	_, err = service.inspectWorkspace(ctx, params)
	require.ErrorIs(t, err, context.Canceled)
}
