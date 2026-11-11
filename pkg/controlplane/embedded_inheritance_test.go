package controlplane

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/db/dbtest"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	runnerclient "github.com/jingkaihe/kodelet/pkg/runner/client"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/spf13/viper"
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
	}, nil)
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
		var wire runnerpayload.Manifest
		require.NoError(t, json.Unmarshal([]byte(run.ManifestJSON), &wire))
		digest, err := runnerpayload.ComputeDiscoveryDigest(wire)
		require.NoError(t, err)
		if restricted {
			assert.NotEqual(t, discovery.Digest, digest)
			assert.Empty(t, manifest.Tools, "request narrowing must still remove every tool")
		} else {
			assert.Equal(t, discovery.Digest, digest, "discovery and actual remote runs must apply the same inherited policy")
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

// Run a real extension process without a provider, SDK installation, or live plugin.
func TestEmbeddedProfileExtensionProcess(_ *testing.T) {
	if os.Getenv("KODELET_EMBEDDED_PROFILE_EXTENSION") != "1" {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	for {
		request, err := readNativeReleaseMessage(reader)
		if err != nil {
			os.Exit(0)
		}
		var result any = extensions.EventResult{}
		switch request.Method {
		case "extension.initialize":
			result = extensions.InitializeResult{
				Name:    "profile-tools",
				Version: "1",
				Profiles: []extensions.ProfileRegistration{
					{
						Name: "rush",
						Options: llmtypes.ProfileConfig{
							"provider":               "openai",
							"model":                  "unused",
							"tool_mode":              "full",
							"enable_fs_search_tools": true,
						},
					},
					{
						Name: "other",
						Options: llmtypes.ProfileConfig{
							"provider": "openai",
							"model":    "unused",
						},
					},
					{
						Name: "broken",
						Options: llmtypes.ProfileConfig{
							"provider":  "openai",
							"model":     "unused",
							"tool_mode": "Patch",
						},
					},
				},
				Subscriptions: []extensions.Subscription{{Event: extensions.EventAgentInit}},
			}
		case "extension.event.handle":
			var params struct {
				Event   string                          `json:"event"`
				Context extensions.ExtensionCallContext `json:"context"`
			}
			_ = json.Unmarshal(request.Params, &params)
			if params.Event == extensions.EventAgentInit {
				patch := extensions.EventResult{
					SystemPrompt: &extensions.SystemPromptPatch{Append: new("active profile: " + params.Context.Profile)},
				}
				if params.Context.Profile == "rush" {
					patch.Tools = &extensions.ToolListPatch{Disable: []string{"view_image"}}
				}
				result = patch
			}
		}
		if len(request.ID) > 0 {
			data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
			_, _ = fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(data), data)
		}
	}
}

func TestEmbeddedExtensionProfileToolsSurviveReloadAndResume(t *testing.T) {
	previous := viper.AllSettings()
	viper.Reset()
	t.Cleanup(func() {
		viper.Reset()
		require.NoError(t, viper.MergeConfigMap(previous))
	})
	t.Setenv("HOME", t.TempDir())
	config := embeddedRunnerTestConfig(t)
	workspace := config.EmbeddedRunner.Workspace
	settings := config.EmbeddedRunner.Settings
	settings["tool_mode"] = "patch"
	settings["code_mode"] = "only"
	settings["extensions"] = map[string]any{"enabled": true, "discover_path": false}
	directory := filepath.Join(workspace, ".kodelet", "extensions")
	require.NoError(t, os.MkdirAll(directory, 0o700))
	executable, err := os.Executable()
	require.NoError(t, err)
	script := fmt.Sprintf(`#!/bin/sh
KODELET_EMBEDDED_PROFILE_EXTENSION=1 exec %q -test.run '^TestEmbeddedProfileExtensionProcess$'
`, executable)
	path := filepath.Join(directory, "kodelet-extension-profile-tools")
	require.NoError(t, os.WriteFile(path, []byte(script), 0o700))

	server, _, _ := startEmbeddedRunnerTestServer(t, config, "127.0.0.1:0")
	require.Eventually(t, func() bool {
		return server.EmbeddedRunnerStatus().Ready
	}, 5*time.Second, 10*time.Millisecond)
	runnerID := server.EmbeddedRunnerStatus().RunnerID
	ctx := contextWithPrincipal(t.Context(), administrativePrincipal("alice"))
	// With no daemon profiles, discover the actual extension process before
	// selecting the first valid visible profile ("broken" is unavailable).
	selected, err := server.resolveChatModelProfile(ctx, chat.ChatRequest{RunnerID: runnerID, CWD: workspace}, "", "")
	require.NoError(t, err)
	assert.Equal(t, "other", selected.Profile)
	assert.True(t, selected.ExtensionProfile)
	viper.Set("profile", "rush")
	selected, err = server.resolveChatModelProfile(ctx, chat.ChatRequest{RunnerID: runnerID, CWD: workspace}, "", "")
	require.NoError(t, err)
	assert.Equal(t, "rush", selected.Profile, "a default can name an extension-only profile")
	ctx = chat.ContextWithProfileResolver(ctx, func(name, effort string) (llmtypes.Config, error) {
		return server.resolveModelProfile(ctx, runnerID, name, effort)
	})
	store := conversations.NewConversationStore(dbtest.OpenDefault(t))
	runner := &serverChatRunner{server: server}
	for _, test := range []struct {
		profile string
		phases  []string
		present []string
		absent  []string
		mode    llmtypes.ToolMode
	}{
		{
			profile: "rush",
			phases:  []string{"open", "reload", "resume"},
			mode:    llmtypes.ToolModeFull,
			present: []string{"file_edit", "file_write", "grep_tool", "glob_tool"},
			// file_read requires explicit selection even in full mode.
			absent: []string{"file_read", "apply_patch", "view_image"},
		},
		{
			profile: "other",
			phases:  []string{"open"},
			mode:    llmtypes.ToolModePatch,
			present: []string{"apply_patch", "view_image"},
			absent:  []string{"file_read", "file_edit", "file_write", "grep_tool", "glob_tool"},
		},
	} {
		t.Run(test.profile, func(t *testing.T) {
			conversationID := "profile-" + test.profile
			request := chat.ChatRequest{RunnerID: runnerID, CWD: workspace}
			// This bootstraps profile registration from the actual runner's extension.
			modelConfig, err := server.resolveChatModelProfile(ctx, request, test.profile, "")
			require.NoError(t, err)
			modelConfig.WorkingDirectory = workspace
			metadata, err := conversations.AddConfigSnapshot(nil, modelConfig)
			require.NoError(t, err)
			record := convtypes.NewConversationRecord(conversationID)
			record.Provider = modelConfig.Provider
			record.CWD = workspace
			record.RawMessages = json.RawMessage(`[]`)
			record.Metadata = metadata
			require.NoError(t, store.Save(ctx, record))

			var remote *agentenv.RemoteEnvironment
			for _, phase := range test.phases {
				if phase == "reload" {
					execution, err := remote.ExecuteTool(ctx, agentenv.ToolRequest{
						Name:       agentenv.ReloadResourcesToolName,
						Input:      "{}",
						ToolCallID: "reload",
					}, nil)
					require.NoError(t, err)
					require.False(t, execution.Result.IsError(), execution.Result.GetError())
					outcome, err := remote.ReloadPendingResources(ctx)
					require.NoError(t, err)
					require.True(t, outcome.Reloaded, outcome.Message)
				} else {
					if phase == "resume" {
						require.NoError(t, remote.Close(ctx))
						modelConfig, _, err = chat.ResolveRemoteConfigWithReasoningAndEnvironmentProfile(
							ctx, store, conversationID, "", "", "")
						request.ConversationID = conversationID
						require.NoError(t, err)
						assert.Equal(t, test.profile, modelConfig.Profile, "resume retains the saved selection, not the daemon default")
					}
					modelConfig, err = chat.ResolveExecutionOptions(ctx, store, request, modelConfig)
					require.NoError(t, err)
					environment, err := runner.ResolveEnvironment(ctx, request, conversationID, modelConfig, workspace)
					require.NoError(t, err)
					remote = environment.(*agentenv.RemoteEnvironment)
					t.Cleanup(func() { assert.NoError(t, environment.Close(ctx)) })
					_, err = remote.Open(ctx, agentenv.RunSpec{ConversationID: conversationID, Config: modelConfig})
					require.NoError(t, err, phase)
				}
				manifest := remote.Manifest()
				assert.Equal(t, test.mode, manifest.Config.ToolMode, phase)
				decision, err := remote.ProcessAgentInit(ctx, "system", manifest.ToolNames())
				require.NoError(t, err)
				assert.Contains(t, decision.SystemPrompt, "active profile: "+test.profile, phase)

				// A stale, pre-agent.init allowlist must not re-enable view_image.
				var callable []string
				for _, definition := range manifest.Tools {
					if definition.Placement == agentenv.ToolPlacementEnvironment &&
						!definition.ModelOnly && definition.Name != "code_execute" {
						callable = append(callable, definition.Name)
					}
				}
				code := ""
				if test.profile == "rush" {
					code = `
try {
  await tools.view_image({path: "not-readable.png"});
} catch (_) {}
`
				}
				code += `
const page = await catalog.list({limit: 100});
return page.tools.map(tool => tool.name);
`
				input, err := json.Marshal(map[string]string{"code": code})
				require.NoError(t, err)
				execution, err := remote.ExecuteTool(ctx, agentenv.ToolRequest{
					Name:          "code_execute",
					Input:         string(input),
					CallableTools: &callable,
					ToolCallID:    phase,
				}, nil)
				require.NoError(t, err)
				require.False(t, execution.Result.IsError(), execution.Result.GetError())
				var codeResult tooltypes.CodeExecutionMetadata
				require.True(t, tooltypes.ExtractMetadata(execution.StructuredResult.Metadata, &codeResult))
				if test.profile == "rush" {
					require.Len(t, codeResult.Calls, 1)
					assert.Equal(t, "blocked", codeResult.Calls[0].Status, phase)
				}
				require.Len(t, codeResult.Items, 1)
				var names []string
				require.NoError(t, json.Unmarshal(codeResult.Items[0].Value, &names))
				for _, name := range test.present {
					assert.Contains(t, names, name, phase)
				}
				for _, name := range test.absent {
					assert.NotContains(t, names, name, phase)
				}
			}
		})
	}
}
