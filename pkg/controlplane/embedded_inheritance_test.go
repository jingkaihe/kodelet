package controlplane

import (
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/agentenv"
	runnerclient "github.com/jingkaihe/kodelet/pkg/runner/client"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbeddedDiscoveryAndRemoteRunShareInheritedPermissionCeilings(t *testing.T) {
	config := embeddedRunnerTestConfig(t)
	t.Setenv("HOME", t.TempDir())
	loader, err := runnerclient.NewEmbeddedConfigLoader(map[string]any{
		"profile": "deep",
		"profiles": map[string]any{
			"deep": map[string]any{
				"tool_mode": "patch", "allowed_tools": []string{"bash"},
				"allowed_commands": []string{"git diff"},
				"skills":           map[string]any{"enabled": false}, "extensions": map[string]any{"enabled": false},
			},
		},
	}, map[string]any{
		"tool_mode": "full", "allowed_tools": []string{"bash", "file_read"},
		"allowed_commands": []string{"git diff", "git status"},
		"skills":           map[string]any{"enabled": true}, "extensions": map[string]any{"enabled": true},
	})
	require.NoError(t, err)
	config.EmbeddedRunner.ServiceOptions.ProfileConfigLoader = loader
	server, _, _ := startEmbeddedRunnerTestServer(t, config, "127.0.0.1:0")
	require.Eventually(t, func() bool { return server.EmbeddedRunnerStatus().Ready }, 5*time.Second, 10*time.Millisecond)
	runnerID := server.EmbeddedRunnerStatus().RunnerID
	runner, found := server.runnerRegistry.Runner(runnerID)
	require.True(t, found)
	var discovery protocol.WorkspaceDiscoverResult
	require.NoError(t, server.runnerRegistry.CallRunner(t.Context(), runnerID, runner.Generation, protocol.MethodWorkspaceDiscover,
		protocol.WorkspaceDiscoverParams{Profile: "deep"}, &discovery))

	// Exercise the ordinary RemoteEnvironment projection, rather than passing
	// empty run.open options directly to the runner. No model call is made.
	for _, restricted := range []bool{false, true} {
		runID := "ordinary"
		modelConfig := llmtypes.Config{
			Profile: "deep", Provider: "openai", Model: "unused",
			ToolMode: llmtypes.ToolModePatch, AllowedTools: []string{"bash"}, AllowedCommands: []string{"git diff"},
			Skills: &llmtypes.SkillsConfig{Enabled: false}, ExtensionSettings: map[string]any{"enabled": false},
		}
		if restricted {
			runID = "request-narrowed"
			modelConfig.ExecutionOptions = &llmtypes.ExecutionOptions{AllowedTools: new([]string{})}
		}
		environment := agentenv.NewRemoteEnvironment(server.runnerRegistry, runnerID,
			agentenv.WithRemoteRunIDGenerator(func() (string, error) { return runID, nil }))
		manifest, err := environment.Open(t.Context(), agentenv.RunSpec{ConversationID: runID, Config: modelConfig})
		require.NoError(t, err)
		run, found := server.runnerRegistry.Run(runID)
		require.True(t, found)
		if restricted {
			assert.NotEqual(t, discovery.Digest, run.ManifestDigest)
			assert.Empty(t, manifest.Tools, "request narrowing must still remove every tool")
		} else {
			assert.Equal(t, discovery.Digest, run.ManifestDigest, "discovery and actual remote runs must apply the same inherited policy")
			assert.Equal(t, []string{"bash"}, manifest.ToolNames())
		}
		require.NotNil(t, manifest.Config)
		assert.Equal(t, llmtypes.ToolModeFull, manifest.Config.ToolMode, "explicit runner preferences still override model preferences")
		assert.Equal(t, []string{"git diff"}, manifest.Config.AllowedCommands)
		assert.Equal(t, new(true), manifest.Config.Options.NoSkills)
		assert.Equal(t, new(true), manifest.Config.Options.NoExtensions)
		require.NoError(t, environment.Close(t.Context()))
	}
}
