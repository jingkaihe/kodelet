package client

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunnerShortcutUsesPinnedProcessAndBoundedCancellation(t *testing.T) {
	service, workspace := newBackgroundTestService(t, "plain")
	executable, err := os.Executable()
	require.NoError(t, err)
	script := fmt.Sprintf("#!/bin/sh\nKODELET_RUNNER_SHORTCUT_HELPER=1 exec %q -test.run '^TestRunnerShortcutHelper$'\n", executable)
	require.NoError(t, os.WriteFile(filepath.Join(workspace, ".kodelet", "extensions", "kodelet-extension-shortcut"), []byte(script), 0o700))
	options := &llmtypes.ExecutionOptions{NoSkills: new(true), AllowedTools: &[]string{}}
	probe, err := service.ProbeManifestForCWDWithOptions(t.Context(), workspace, "", options)
	require.NoError(t, err)
	require.Len(t, probe.Shortcuts, 2)
	manifest, err := service.openRun(t.Context(), protocol.RunOpenParams{RunID: "shortcut-run", ConversationID: "conversation", CWD: workspace, Options: options})
	require.NoError(t, err)
	assert.Equal(t, probe.Digest, manifest.Digest, "probe and isolated execution have stable equivalent environments")
	assert.NotEqual(t, probe.Shortcuts[0].Generation, manifest.Shortcuts[0].Generation)
	params := runnerpayload.ShortcutExecuteParams{RunID: manifest.RunID, Digest: manifest.Digest, Shortcut: manifest.Shortcuts[0]}
	marker := filepath.Join(workspace, "shortcut-invocation.json")
	for _, change := range []func(*runnerpayload.ShortcutExecuteParams){
		func(p *runnerpayload.ShortcutExecuteParams) { p.Digest = "stale" },
		func(p *runnerpayload.ShortcutExecuteParams) { p.Shortcut.Generation = probe.Shortcuts[0].Generation },
		func(p *runnerpayload.ShortcutExecuteParams) { p.Shortcut.ExtensionID = "other" },
		func(p *runnerpayload.ShortcutExecuteParams) { p.RunID = "other-run" },
	} {
		stale := params
		change(&stale)
		_, err := service.executeShortcut(t.Context(), stale)
		require.Error(t, err)
		_, err = os.Stat(marker)
		assert.ErrorIs(t, err, os.ErrNotExist, "reject stale calls before extension effects")
	}
	result, err := service.executeShortcut(t.Context(), params)
	require.NoError(t, err)
	require.NotNil(t, result.Result)
	assert.Equal(t, "/review", result.Result.Message)
	data, err := os.ReadFile(marker)
	require.NoError(t, err)
	var invocation extensions.ExtensionCallContext
	require.NoError(t, json.Unmarshal(data, &invocation))
	assert.Equal(t, workspace, invocation.CWD)
	assert.Equal(t, "conversation", invocation.ConversationID)
	params.Shortcut = manifest.Shortcuts[1]
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, err = service.executeShortcut(ctx, params)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	require.NoError(t, service.closeRun(t.Context(), manifest.RunID))
	assert.Empty(t, service.runs)
	assert.Empty(t, service.backgrounds, "an isolated shortcut run must not retain runtime without a lease")
}

func TestRunnerShortcutDiscoveryMatchesIdleRunWithTools(t *testing.T) {
	service, workspace := newBackgroundTestService(t, "plain")
	executable, err := os.Executable()
	require.NoError(t, err)
	script := fmt.Sprintf("#!/bin/sh\nKODELET_RUNNER_SHORTCUT_HELPER=1 exec %q -test.run '^TestRunnerShortcutHelper$'\n", executable)
	require.NoError(t, os.WriteFile(filepath.Join(workspace, ".kodelet", "extensions", "kodelet-extension-shortcut"), []byte(script), 0o700))
	loader, err := NewEmbeddedConfigLoader(map[string]any{"skills": map[string]any{"enabled": false}}, nil)
	require.NoError(t, err)
	service.profileConfigLoader = loader
	probe, err := service.ProbeManifestForCWD(t.Context(), workspace, "")
	require.NoError(t, err)
	discovery := callService[protocol.WorkspaceDiscoverResult](t, service, protocol.MethodWorkspaceDiscover, protocol.WorkspaceDiscoverParams{CWD: workspace})
	manifest, err := service.openRun(t.Context(), protocol.RunOpenParams{RunID: "shortcut-run", ConversationID: "conversation", CWD: workspace})
	require.NoError(t, err)
	assert.Equal(t, probe.Config, manifest.Config)
	assert.Equal(t, probe.Commands, manifest.Commands)
	assert.Len(t, manifest.Tools, len(probe.Tools)+1, "background-only tools are absent during discovery")
	assert.NotEqual(t, probe.Digest, manifest.Digest, "full run digests must still detect tool changes")
	digest, err := runnerpayload.ComputeDiscoveryDigest(manifest)
	require.NoError(t, err)
	assert.Equal(t, discovery.Digest, digest)
	result, err := service.executeShortcut(t.Context(), runnerpayload.ShortcutExecuteParams{RunID: manifest.RunID, Digest: manifest.Digest, Shortcut: manifest.Shortcuts[0]})
	require.NoError(t, err)
	require.NotNil(t, result.Result)
	assert.Equal(t, "/review", result.Result.Message)
}

func TestRunnerShortcutHelper(t *testing.T) {
	if os.Getenv("KODELET_RUNNER_SHORTCUT_HELPER") != "1" {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	for {
		request, err := readBackgroundHelperMessage(reader)
		if err != nil {
			os.Exit(0)
		}
		var result any
		switch request.Method {
		case "extension.initialize":
			var params struct {
				Capabilities struct {
					Runtime extensions.RuntimeCapabilities `json:"runtime"`
				} `json:"capabilities"`
			}
			_ = json.Unmarshal(request.Params, &params)
			initialized := extensions.InitializeResult{Name: "shortcut", Version: "1", Shortcuts: []extensions.ShortcutRegistration{{Key: "ctrl+r", Description: "Review"}, {Key: "ctrl+t", Description: "Wait"}}}
			if params.Capabilities.Runtime.BackgroundTasks {
				initialized.Tools = []extensions.ToolRegistration{{Name: "background_only", Description: "Requires background execution", InputSchema: map[string]any{"type": "object"}}}
			}
			result = initialized
		case "extension.shortcut.execute":
			var params struct {
				Key     string                          `json:"key"`
				Context extensions.ExtensionCallContext `json:"context"`
			}
			_ = json.Unmarshal(request.Params, &params)
			data, _ := json.Marshal(params.Context)
			_ = os.WriteFile(filepath.Join(params.Context.CWD, "shortcut-invocation.json"), data, 0o600)
			if params.Key == "ctrl+t" {
				continue
			}
			result = &extensions.ShortcutResult{Action: "submit", Message: "/review"}
		}
		if len(request.ID) != 0 {
			writeBackgroundHelperMessage(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		}
	}
}
