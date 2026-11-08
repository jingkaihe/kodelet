package client

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/jingkaihe/kodelet/pkg/skills"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeExtensionTool stands in for a tool an extension registered.
type fakeExtensionTool struct {
	tooltypes.Tool
	extensionID string
}

func (t fakeExtensionTool) ExtensionID() string { return t.extensionID }

func TestBuildWorkspaceInventoryGroupsPluginsAndStandaloneItems(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "repo")
	pluginDir := filepath.Join(home, ".kodelet", "plugins", "acme@kit")
	repoSkill := &skills.Skill{Name: "release", Description: "Cut a release. Then announce it.", Directory: filepath.Join(cwd, ".kodelet", "skills", "release")}
	pluginSkill := &skills.Skill{Name: "acme@kit/helper", Description: "Help with kits.", Directory: filepath.Join(pluginDir, "skills", "helper")}
	unusedSkill := &skills.Skill{Name: "acme@kit/unused", Description: "Not allowed.", Directory: filepath.Join(pluginDir, "skills", "unused")}
	manifest := agentenv.Manifest{
		WorkingDirectory: cwd,
		Tools: []agentenv.ToolDefinition{
			{Name: "bash", Description: "Run a bash command. It has many rules."},
			{Name: "skill", Description: "Use skills.", Tool: tools.NewSkillTool(map[string]*skills.Skill{
				repoSkill.Name:   repoSkill,
				pluginSkill.Name: pluginSkill,
			}, true, false)},
			{Name: "search_code", Description: "Search code by concept.", Tool: fakeExtensionTool{extensionID: "acme@kit/finder"}},
		},
	}
	statuses := []extensions.ExtensionStatus{
		{
			Extension: extensions.Extension{
				ID: "acme@kit/finder", Name: "finder", Kind: extensions.SourceKindGlobalPlugin, PluginPrefix: "acme@kit",
				RootDir: filepath.Join(pluginDir, "extensions"), Dir: filepath.Join(pluginDir, "extensions", "finder"),
				ExecPath: filepath.Join(pluginDir, "extensions", "finder", "kodelet-extension-finder"),
			},
			Version:  "1.2.0",
			Tools:    []extensions.ExtensionTool{{Name: "search_code", Description: "Search code by concept.", Enabled: true}, {Name: "hidden", Description: "Turned off."}},
			Commands: []string{"find"},
			Hooks:    []string{"turn.end"},
		},
		{
			Extension: extensions.Extension{
				ID: "broken", Name: "broken", Kind: extensions.SourceKindLocalStandalone,
				RootDir: filepath.Join(cwd, ".kodelet", "extensions"), Dir: filepath.Join(cwd, ".kodelet", "extensions", "broken"),
			},
			Err: errors.New("exit status 1"),
		},
		{
			Extension: extensions.Extension{
				ID: "subagent", Name: "subagent", Kind: extensions.SourceKindPath,
				RootDir: "/usr/local/bin", Dir: "/usr/local/bin", ExecPath: "/usr/local/bin/kodelet-extension-subagent",
			},
		},
	}
	discovered := map[string]*skills.Skill{repoSkill.Name: repoSkill, pluginSkill.Name: pluginSkill, unusedSkill.Name: unusedSkill}

	result := buildWorkspaceInventory(manifest, statuses, discovered, home)

	assert.Equal(t, cwd, result.CWD)
	builtins := map[string]protocol.InventoryTool{}
	for _, tool := range result.BuiltinTools {
		builtins[tool.Name] = tool
	}
	assert.Equal(t, protocol.InventoryTool{Name: "bash", Description: "Run a bash command.", Enabled: true}, builtins["bash"])
	assert.True(t, builtins["skill"].Enabled)
	assert.NotContains(t, builtins, "search_code", "extension tools are not built in")
	require.Contains(t, builtins, "file_read")
	assert.False(t, builtins["file_read"].Enabled, "registry tools the environment omits are off")
	assert.NotEmpty(t, builtins["file_read"].Description)

	require.Len(t, result.Plugins, 1)
	plugin := result.Plugins[0]
	assert.Equal(t, "acme@kit", plugin.Name)
	assert.Equal(t, protocol.InventoryScopeGlobal, plugin.Scope)
	assert.Equal(t, pluginDir, plugin.Path)
	require.Len(t, plugin.Extensions, 1)
	assert.Equal(t, protocol.InventoryExtension{
		ID: "acme@kit/finder", Name: "finder", Version: "1.2.0", Scope: protocol.InventoryScopeGlobal,
		Path: filepath.Join(pluginDir, "extensions", "finder"), Status: protocol.InventoryStatusReady,
		Tools: []protocol.InventoryTool{
			{Name: "hidden", Description: "Turned off."},
			{Name: "search_code", Description: "Search code by concept.", Enabled: true},
		},
		Commands: []string{"/find"},
		Hooks:    []string{"turn.end"},
	}, plugin.Extensions[0])
	assert.Equal(t, []protocol.InventorySkill{
		{Name: "helper", Description: "Help with kits.", Scope: protocol.InventoryScopeGlobal, Path: pluginSkill.Directory, Enabled: true},
		{Name: "unused", Description: "Not allowed.", Scope: protocol.InventoryScopeGlobal, Path: unusedSkill.Directory},
	}, plugin.Skills)

	require.Len(t, result.Extensions, 2)
	assert.Equal(t, "broken", result.Extensions[0].Name)
	assert.Equal(t, protocol.InventoryStatusFailed, result.Extensions[0].Status)
	assert.Equal(t, "exit status 1", result.Extensions[0].Error)
	assert.Equal(t, protocol.InventoryScopeRepo, result.Extensions[0].Scope)
	assert.Equal(t, "subagent", result.Extensions[1].Name)
	assert.Equal(t, protocol.InventoryScopePath, result.Extensions[1].Scope)
	assert.Equal(t, "/usr/local/bin/kodelet-extension-subagent", result.Extensions[1].Path, "direct executables show their own path")

	assert.Equal(t, []protocol.InventorySkill{
		{Name: "release", Description: "Cut a release.", Scope: protocol.InventoryScopeRepo, Path: repoSkill.Directory, Enabled: true},
	}, result.Skills)
}

func TestBuildWorkspaceInventoryKeepsBuiltInsThatExtensionsShadow(t *testing.T) {
	manifest := agentenv.Manifest{
		WorkingDirectory: "/repo",
		Tools:            []agentenv.ToolDefinition{{Name: "bash", Description: "Run a bash command."}},
	}
	statuses := []extensions.ExtensionStatus{{
		Extension: extensions.Extension{ID: "shadow", Name: "shadow", Kind: extensions.SourceKindPath, ExecPath: "/bin/kodelet-extension-shadow"},
		Tools:     []extensions.ExtensionTool{{Name: "bash", Description: "Another bash.", Enabled: true}},
	}}

	result := buildWorkspaceInventory(manifest, statuses, nil, "/home/user")

	for _, tool := range result.BuiltinTools {
		if tool.Name == "bash" {
			assert.True(t, tool.Enabled, "the run keeps the built-in")
		}
	}
	require.Len(t, result.Extensions, 1)
	assert.Equal(t, []protocol.InventoryTool{{Name: "bash", Description: "Another bash."}}, result.Extensions[0].Tools, "the shadowing extension tool is not loaded")
}

func TestInventoryScopeTreatsHomeWorkspaceAsGlobal(t *testing.T) {
	home := t.TempDir()
	skillDir := filepath.Join(home, ".kodelet", "skills", "notes")
	assert.Equal(t, protocol.InventoryScopeGlobal, inventoryScope(skillDir, home, home))
	assert.Equal(t, protocol.InventoryScopeRepo, inventoryScope(filepath.Join(home, "repo", ".agents", "skills", "x"), filepath.Join(home, "repo"), home))
	assert.Equal(t, protocol.InventoryScopePath, inventoryScope("/opt/skills/x", filepath.Join(home, "repo"), home))
}

func TestRunnerWorkspaceInventoryProbesTheRequestedWorkspace(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	workspace := filepath.Join(root, "repo")
	skillDir := filepath.Join(workspace, ".kodelet", "skills", "demo")
	require.NoError(t, os.MkdirAll(skillDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: demo\ndescription: Demonstrate skills.\n---\nBody"), 0o600))
	runtime := extensions.EmptyRuntime()
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	service, err := NewService(t.Context(), workspace, ServiceOptions{
		RuntimeProvider: staticRuntimeProvider{runtime: runtime},
		WorkspaceConfigLoader: func(cwd, _ string) (llmtypes.Config, error) {
			return llmtypes.Config{WorkingDirectory: cwd, ToolMode: llmtypes.ToolModeFull, AllowedTools: []string{"bash", "skill"}}, nil
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	initial, err := service.ProbeManifestDigest(t.Context())
	require.NoError(t, err)

	result := callService[protocol.WorkspaceInventoryResult](t, service, protocol.MethodWorkspaceInventory, protocol.WorkspaceInventoryParams{CWD: workspace, EnvironmentProfile: "review"})

	assert.Equal(t, workspace, result.CWD)
	assert.Equal(t, "review", result.EnvironmentProfile)
	enabled := map[string]bool{}
	for _, tool := range result.BuiltinTools {
		enabled[tool.Name] = tool.Enabled
	}
	assert.True(t, enabled["bash"])
	assert.True(t, enabled["skill"])
	assert.Contains(t, enabled, "file_write")
	assert.False(t, enabled["file_write"], "allowed tools exclude file_write")
	assert.Equal(t, []protocol.InventorySkill{
		{Name: "demo", Description: "Demonstrate skills.", Scope: protocol.InventoryScopeRepo, Path: skillDir, Enabled: true},
	}, result.Skills)
	assert.Empty(t, result.Plugins)
	assert.Empty(t, result.Extensions)

	state, active, digest := service.HeartbeatSnapshot()
	assert.Equal(t, protocol.RunnerStateIdle, state)
	assert.Empty(t, active, "listing tools must not reserve a run")
	assert.Equal(t, initial, digest, "listing tools must not replace the startup manifest digest")

	_, rpcErr := service.HandleRequest(t.Context(), protocol.MethodWorkspaceInventory, mustJSON(t, protocol.WorkspaceInventoryParams{
		Options: &llmtypes.ExecutionOptions{MaxTurns: new(3)},
	}))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeInvalidParams, rpcErr.Code)
}
