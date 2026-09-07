package client

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunnerDiscoveryUsesRequestedDirectoryAndProfile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	workspace, selected := filepath.Join(root, "startup"), filepath.Join(root, "selected")
	for _, directory := range []string{workspace, selected} {
		require.NoError(t, os.MkdirAll(filepath.Join(directory, ".kodelet", "recipes"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(directory, "AGENTS.md"), []byte(directory), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(directory, ".kodelet", "recipes", filepath.Base(directory)+".md"), []byte("---\ndescription: Example\n---\nHello"), 0o600))
	}
	var loadedDirectory, loadedProfile string
	runtime := extensions.EmptyRuntime()
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	service, err := NewService(t.Context(), workspace, ServiceOptions{
		RuntimeProvider: staticRuntimeProvider{runtime: runtime},
		WorkspaceConfigLoader: func(cwd, profile string) (llmtypes.Config, error) {
			loadedDirectory, loadedProfile = cwd, profile
			return llmtypes.Config{WorkingDirectory: cwd, ToolMode: llmtypes.ToolModeFull}, nil
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	initial, err := service.ProbeManifestDigest(t.Context())
	require.NoError(t, err)
	result := callService[protocol.WorkspaceDiscoverResult](t, service, protocol.MethodWorkspaceDiscover, protocol.WorkspaceDiscoverParams{CWD: selected, EnvironmentProfile: "review"})
	assert.Equal(t, selected, result.CWD)
	assert.Equal(t, selected, loadedDirectory)
	assert.Equal(t, "review", loadedProfile)
	assert.Equal(t, "review", result.EnvironmentProfile)
	assert.NotEqual(t, initial, result.Digest)
	state, active, digest := service.HeartbeatSnapshot()
	assert.Equal(t, protocol.RunnerStateIdle, state)
	assert.Empty(t, active, "discovery must not reserve a run")
	assert.Equal(t, initial, digest, "directory probes must not replace the startup manifest digest")
	var names []string
	for _, command := range result.Commands {
		names = append(names, command.Name)
	}
	assert.Contains(t, names, "selected")
	assert.NotContains(t, names, "startup")
	_, rpcErr := service.HandleRequest(t.Context(), protocol.MethodWorkspaceDiscover, mustJSON(t, protocol.WorkspaceDiscoverParams{CWD: filepath.Join(root, "missing")}))
	require.NotNil(t, rpcErr)
	assert.Contains(t, rpcErr.Message, "does not exist")
}

func TestRunnerDirectoryHintsResolveOnRunnerHost(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	workspace := filepath.Join(root, "startup")
	for _, directory := range []string{workspace, filepath.Join(workspace, "project-one"), filepath.Join(root, "project-two")} {
		require.NoError(t, os.MkdirAll(directory, 0o700))
	}
	service, err := NewService(t.Context(), workspace, ServiceOptions{ConfigLoader: func(string) (llmtypes.Config, error) { return llmtypes.Config{}, nil }})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	result := callService[protocol.WorkspaceCWDHintsResult](t, service, protocol.MethodWorkspaceCWDHints, protocol.WorkspaceCWDHintsParams{Query: "pjt"})
	assert.Equal(t, workspace, result.BaseDir)
	assert.Equal(t, []protocol.DirectoryHint{{Path: filepath.Join(workspace, "project-one")}, {Path: filepath.Join(root, "project-two")}}, result.Hints)
	result = callService[protocol.WorkspaceCWDHintsResult](t, service, protocol.MethodWorkspaceCWDHints, protocol.WorkspaceCWDHintsParams{Query: "~/project"})
	assert.Equal(t, root, result.BaseDir)
	assert.Equal(t, []protocol.DirectoryHint{{Path: filepath.Join(root, "project-two")}}, result.Hints)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = service.workspaceCWDHints(ctx, protocol.WorkspaceCWDHintsParams{})
	require.ErrorIs(t, err, context.Canceled)
}

func TestRunnerDiscoveryRestrictionsPreventExtensionStartup(t *testing.T) {
	service, workspace := newBackgroundTestService(t, "plain")
	service.configLoader = func(string) (llmtypes.Config, error) {
		return llmtypes.Config{Skills: &llmtypes.SkillsConfig{Enabled: true}}, nil
	}
	skillDirectory := filepath.Join(workspace, ".kodelet", "skills", "local-skill")
	require.NoError(t, os.MkdirAll(skillDirectory, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(skillDirectory, "SKILL.md"), []byte("---\nname: local-skill\ndescription: Local test skill\n---\nInstructions"), 0o600))
	marker := filepath.Join(workspace, "initializations.log")
	for _, raw := range []string{
		`{"options":null}`, `{"options":{"noExtensions":null}}`,
		`{"options":{"model":"gpt-4.1"}}`, `{"options":{"maxTurns":1}}`,
	} {
		_, rpcErr := service.HandleRequest(t.Context(), protocol.MethodWorkspaceDiscover, []byte(raw))
		require.NotNil(t, rpcErr)
		_, err := os.Stat(marker)
		require.ErrorIs(t, err, os.ErrNotExist)
	}
	params := protocol.WorkspaceDiscoverParams{CWD: workspace, Options: &llmtypes.ExecutionOptions{NoExtensions: new(true), NoSkills: new(true)}}
	result := callService[protocol.WorkspaceDiscoverResult](t, service, protocol.MethodWorkspaceDiscover, params)
	assert.NotEmpty(t, result.Digest)
	assert.Equal(t, new(0), result.ExtensionCount)
	_, err := os.Stat(marker)
	require.ErrorIs(t, err, os.ErrNotExist, "no-extensions discovery must not start an extension process")
	for _, command := range result.Commands {
		assert.NotEqual(t, "local-skill", command.Name)
	}
	_, _, digest := service.HeartbeatSnapshot()
	assert.Empty(t, digest, "restricted discovery must not write the default manifest digest")
	restricted, err := service.ProbeManifestForCWDWithOptions(t.Context(), workspace, "", params.Options)
	require.NoError(t, err)
	assert.Empty(t, restricted.Skills)
	assert.Equal(t, new(0), restricted.ExtensionCount)
	unrestricted, err := service.ProbeManifestForCWD(t.Context(), workspace, "")
	require.NoError(t, err)
	assert.Equal(t, new(1), unrestricted.ExtensionCount)
	result = callService[protocol.WorkspaceDiscoverResult](t, service, protocol.MethodWorkspaceDiscover, protocol.WorkspaceDiscoverParams{CWD: workspace})
	assert.Equal(t, new(1), result.ExtensionCount)
	var skillNames []string
	for _, skill := range unrestricted.Skills {
		skillNames = append(skillNames, skill.Name)
	}
	assert.Contains(t, skillNames, "local-skill", "no-skills assertions must exercise an enabled installed skill")
	markerData, err := os.ReadFile(marker)
	require.NoError(t, err)
	assert.NotEmpty(t, markerData, "unrestricted discovery still starts the installed extension")
}
