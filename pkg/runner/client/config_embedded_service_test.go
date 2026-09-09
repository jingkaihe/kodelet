package client

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbeddedProfilesReachRunAndDiscoveryManifests(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	prompt := filepath.Join(root, "deep.tmpl")
	require.NoError(t, os.WriteFile(prompt, []byte("Deep profile instructions"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "TEAM.md"), []byte("Team instructions"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("Default instructions"), 0o600))
	for _, skill := range []string{"approved", "other"} {
		directory := filepath.Join(root, ".kodelet", "skills", skill)
		require.NoError(t, os.MkdirAll(directory, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte("---\nname: "+skill+"\ndescription: Test skill\n---\nInstructions"), 0o600))
	}
	loader, err := NewEmbeddedConfigLoader(map[string]any{
		"profile": "deep", "tool_mode": "full",
		"extensions": map[string]any{"enabled": false},
		"profiles": map[string]any{
			"deep": map[string]any{
				"tool_mode": "patch", "sysprompt": prompt,
				"sysprompt_args": map[string]any{"style": "brief"},
				"context":        map[string]any{"patterns": []string{"TEAM.md"}},
				"skills":         map[string]any{"enabled": true, "allowed": []string{"approved"}},
			},
			"flair": map[string]any{"tool_mode": "full", "enable_fs_search_tools": true, "skills": map[string]any{"enabled": false}},
		},
	}, nil)
	require.NoError(t, err)
	service, err := NewService(t.Context(), root, ServiceOptions{ProfileConfigLoader: loader})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	require.NoError(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "embedded", Generation: 1}))
	initial, err := service.ProbeManifestDigest(t.Context())
	require.NoError(t, err)
	probe := callService[protocol.WorkspaceDiscoverResult](t, service, protocol.MethodWorkspaceDiscover, protocol.WorkspaceDiscoverParams{Profile: "flair"})
	assert.NotEqual(t, initial, probe.Digest)
	_, _, cached := service.HeartbeatSnapshot()
	assert.Equal(t, initial, cached, "named profile probes must not replace the default heartbeat digest")

	var wg sync.WaitGroup
	for _, profile := range []string{"deep", "flair"} {
		wg.Go(func() {
			manifest := callService[runnerpayload.Manifest](t, service, protocol.MethodRunOpen, protocol.RunOpenParams{
				RunID: profile, ConversationID: profile, CWD: root,
				Agent: protocol.AgentDescriptor{Profile: profile, Model: "unused", Provider: "openai"},
			})
			names := make([]string, 0, len(manifest.Tools))
			for _, tool := range manifest.Tools {
				names = append(names, tool.Name)
			}
			if profile == "deep" {
				assert.Equal(t, llmtypes.ToolModePatch, manifest.Config.ToolMode)
				assert.Contains(t, names, "apply_patch")
				assert.NotContains(t, names, "file_edit")
				assert.NotContains(t, names, "file_write")
				assert.NotContains(t, names, "file_read")
				assert.Equal(t, "Deep profile instructions", manifest.Config.SystemPromptContent)
				assert.Equal(t, "brief", manifest.Config.SystemPromptArgs["style"])
				require.Len(t, manifest.Skills, 1)
				assert.Equal(t, "approved", manifest.Skills[0].Name)
				var contextPaths []string
				for _, file := range manifest.ContextFiles {
					contextPaths = append(contextPaths, file.Path)
				}
				assert.Contains(t, contextPaths, filepath.Join(root, "TEAM.md"))
				assert.NotContains(t, contextPaths, filepath.Join(root, "AGENTS.md"))
			} else {
				assert.Equal(t, llmtypes.ToolModeFull, manifest.Config.ToolMode)
				assert.Contains(t, names, "file_edit")
				assert.NotContains(t, names, "apply_patch")
				assert.Contains(t, names, "grep_tool")
				assert.Contains(t, names, "glob_tool")
				assert.True(t, manifest.Config.EnableFSSearchTools)
				assert.Empty(t, manifest.Config.SystemPromptContent)
				assert.Empty(t, manifest.Skills)
				digest, err := runnerpayload.ComputeDiscoveryDigest(manifest)
				require.NoError(t, err)
				assert.Equal(t, probe.Digest, digest)
			}
			callService[struct{}](t, service, protocol.MethodRunClose, protocol.RunCloseParams{RunID: profile})
		})
	}
	wg.Wait()
	_, _, cached = service.HeartbeatSnapshot()
	assert.Equal(t, initial, cached, "profile-specific runs must not replace the default heartbeat digest")
	manifest, err := service.ProbeManifestForProfile(t.Context(), root, "default", "", nil)
	require.NoError(t, err)
	assert.Equal(t, llmtypes.ToolModeFull, manifest.Config.ToolMode)
	assert.Empty(t, manifest.Config.SystemPromptContent)
}

func TestStandaloneRunnerDoesNotApplyDaemonModelProfile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	loader, err := NewWorkspaceConfigLoader(map[string]any{
		"tool_mode": "full", "extensions": map[string]any{"enabled": false},
		"profiles": map[string]any{"deep": map[string]any{"tool_mode": "patch"}},
	})
	require.NoError(t, err)
	service, err := NewService(t.Context(), root, ServiceOptions{WorkspaceConfigLoader: loader})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	manifest, err := service.ProbeManifestForProfile(t.Context(), root, "deep", "", nil)
	require.NoError(t, err)
	assert.Equal(t, llmtypes.ToolModeFull, manifest.Config.ToolMode)
	var names []string
	for _, tool := range manifest.Tools {
		names = append(names, tool.Name)
	}
	assert.Contains(t, names, "file_edit")
	assert.NotContains(t, names, "apply_patch")
}
