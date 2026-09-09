package controlplane

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtensionProfilesOwnershipDiscoveryAndSnapshots(t *testing.T) {
	previous := viper.AllSettings()
	viper.Reset()
	t.Cleanup(func() { viper.Reset(); require.NoError(t, viper.MergeConfigMap(previous)) })
	viper.Set("provider", "openai")
	viper.Set("model", "gpt-4o")
	viper.Set("reasoning_effort", "medium")
	viper.Set("profile", "deep")
	viper.Set("profiles", map[string]any{
		"deep": map[string]any{
			"reasoning_effort": "xhigh", "allowed_reasoning_efforts": []string{"medium", "high", "xhigh"},
			"openai": map[string]any{"platform": "codex"},
		},
		"internal": map[string]any{"hidden": true, "model": "gpt-4o"},
	})
	server := newRunnerTestServer(t, "")
	ctx := contextWithPrincipal(t.Context(), administrativePrincipal("alice"))
	other := contextWithPrincipal(t.Context(), administrativePrincipal("bob"))
	register := func(host string) protocol.RegisterResult {
		result, err := server.runnerRegistry.Register(protocol.RegisterParams{
			ProtocolVersions: []int{protocol.Version},
			Host:             protocol.Host{InstanceID: host, Hostname: host, OS: "linux", Arch: "amd64"},
			Workspace:        protocol.Workspace{Path: "/workspace", Name: "workspace"},
		}, newRunnerAPITestLink())
		require.NoError(t, err)
		return result
	}
	runner := register("one")
	second := register("two")
	profile := extensions.Profile{
		Name: "code-search", ExtensionID: "skills/code-search", Hidden: true,
		Options: &llmtypes.ExtensionProfileOptions{Provider: new("openai"), Model: new("gpt-5.6-luna"), ReasoningEffort: new("none")},
	}
	manifest := runnerpayload.Manifest{RunnerID: runner.RunnerID, Generation: runner.Generation, Profiles: []extensions.Profile{profile}}
	require.NoError(t, server.registerExtensionProfiles(ctx, manifest))
	config, err := server.resolveModelProfile(ctx, runner.RunnerID, profile.Name, "")
	require.NoError(t, err)
	assert.Equal(t, "none", config.ReasoningEffort)
	assert.Equal(t, "gpt-5.6-luna", config.Model)
	assert.Equal(t, "codex", config.OpenAI.Platform)
	assert.Empty(t, config.AllowedReasoningEfforts)
	assert.Equal(t, profile.Name, config.Profile)
	assert.True(t, config.ExtensionProfile)
	assert.Nil(t, config.ExecutionOptions)
	assert.Equal(t, "deep", viper.GetString("profile"))
	_, err = server.resolveModelProfile(other, runner.RunnerID, profile.Name, "")
	require.ErrorContains(t, err, "not registered")
	_, err = server.resolveModelProfile(ctx, second.RunnerID, profile.Name, "")
	require.ErrorContains(t, err, "not registered")
	require.ErrorContains(t, server.registerExtensionProfiles(context.Background(), manifest), "authenticated")
	assert.NotContains(t, server.modelProfileOptions(ctx, runner.RunnerID, "deep", false), ChatProfileOption{Name: profile.Name, Scope: "extension", Hidden: true})
	assert.Contains(t, server.modelProfileOptions(ctx, runner.RunnerID, "deep", true), ChatProfileOption{Name: profile.Name, Scope: "extension", Hidden: true})
	for _, option := range server.modelProfileOptions(ctx, runner.RunnerID, "deep", false) {
		assert.NotEqual(t, "internal", option.Name)
		assert.NotEqual(t, profile.Name, option.Name)
	}
	for _, option := range server.modelProfileOptions(other, runner.RunnerID, "deep", true) {
		assert.NotEqual(t, profile.Name, option.Name)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/chat/settings?runnerId="+runner.RunnerID+"&profile="+profile.Name, nil).WithContext(ctx)
	response := httptest.NewRecorder()
	server.handleGetChatSettings(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var settings ChatSettingsResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &settings))
	assert.Equal(t, "none", settings.ReasoningEffort)
	assert.Equal(t, profile.Name, settings.CurrentProfile)
	target, targetErr := server.resolveRunnerTarget(request)
	require.Nil(t, targetErr)
	assert.Equal(t, "default", target.Profile)

	visible := profile.Clone()
	visible.Name, visible.Hidden = "review", false
	visible.ExtensionID = "other-extension"
	require.NoError(t, server.registerExtensionProfiles(ctx, runnerpayload.Manifest{
		RunnerID: runner.RunnerID, Generation: runner.Generation, Profiles: []extensions.Profile{visible},
	}))
	assert.Contains(t, server.modelProfileOptions(ctx, runner.RunnerID, "review", false), ChatProfileOption{Name: "review", Scope: "extension", Active: true})
	visible.Name = "deep"
	require.ErrorContains(t, server.registerExtensionProfiles(ctx, runnerpayload.Manifest{
		RunnerID: runner.RunnerID, Generation: runner.Generation, Profiles: []extensions.Profile{visible},
	}), "daemon-configured profile")
	for _, name := range []string{"", "default", "deep", "internal"} {
		configured, err := server.resolveModelProfile(ctx, runner.RunnerID, name, "")
		require.NoError(t, err)
		assert.False(t, configured.ExtensionProfile)
	}

	metadata, err := conversations.AddConfigSnapshot(nil, config)
	require.NoError(t, err)
	profile.Options.ReasoningEffort = new("high")
	manifest.Profiles = []extensions.Profile{profile}
	require.NoError(t, server.registerExtensionProfiles(ctx, manifest))
	config, err = server.resolveModelProfile(ctx, runner.RunnerID, profile.Name, "")
	require.NoError(t, err)
	assert.Equal(t, "high", config.ReasoningEffort)
	resumed, err := chat.ResolveConfigForExistingConversation(&conversations.GetConversationResponse{Metadata: metadata})
	require.NoError(t, err)
	assert.Equal(t, "none", resumed.ReasoningEffort)
	assert.Equal(t, "codex", resumed.OpenAI.Platform)
	assert.Equal(t, profile.Name, resumed.Profile)

	manifest.Profiles[0].ExtensionID = "other-extension"
	require.ErrorContains(t, server.registerExtensionProfiles(ctx, manifest), "already registered")
	manifest.Profiles[0].Name = "other"
	manifest.Profiles[0].Options.AnthropicAPIAccess = new(llmtypes.AnthropicAPIAccessSubscription)
	require.ErrorContains(t, server.registerExtensionProfiles(ctx, manifest), "require provider anthropic")
	_, err = server.resolveModelProfile(ctx, runner.RunnerID, "other", "")
	require.ErrorContains(t, err, "not registered")

	reconnected := register("one")
	assert.Greater(t, reconnected.Generation, runner.Generation)
	_, err = server.resolveModelProfile(ctx, runner.RunnerID, profile.Name, "")
	require.ErrorContains(t, err, "not registered")
	require.ErrorContains(t, server.registerExtensionProfiles(ctx, manifest), "inactive runner generation")
	server.RunnerExtensionsDetached(runnerUIRequestIdentity(runner))
	assert.Empty(t, server.extensionProfiles)
}

func TestExtensionProfilesRejectLateOldGenerationPublication(t *testing.T) {
	previous := viper.AllSettings()
	viper.Reset()
	t.Cleanup(func() { viper.Reset(); require.NoError(t, viper.MergeConfigMap(previous)) })
	viper.Set("provider", "openai")
	viper.Set("model", "gpt-4o")
	server := newRunnerTestServer(t, "")
	ctx := contextWithPrincipal(t.Context(), administrativePrincipal("alice"))
	register := func() protocol.RegisterResult {
		result, err := server.runnerRegistry.Register(protocol.RegisterParams{
			ProtocolVersions: []int{protocol.Version},
			Host:             protocol.Host{InstanceID: "one", Hostname: "one", OS: "linux", Arch: "amd64"},
			Workspace:        protocol.Workspace{Path: "/workspace", Name: "workspace"},
		}, newRunnerAPITestLink())
		require.NoError(t, err)
		return result
	}
	runner := register()
	profile := extensions.Profile{
		Name: "code-search", ExtensionID: "skills/code-search",
		Options: &llmtypes.ExtensionProfileOptions{Provider: new("openai"), Model: new("gpt-5.6-luna"), ReasoningEffort: new("none")},
	}
	manifest := runnerpayload.Manifest{RunnerID: runner.RunnerID, Generation: runner.Generation, Profiles: []extensions.Profile{profile}}
	require.NoError(t, server.registerExtensionProfiles(ctx, manifest))
	server.extensionProfilesMu.RLock()
	validated := maps.Clone(server.extensionProfiles)
	server.extensionProfilesMu.RUnlock()
	reconnected := register()
	require.Equal(t, runner.RunnerID, reconnected.RunnerID)
	require.Greater(t, reconnected.Generation, runner.Generation)
	require.ErrorContains(t, server.publishExtensionProfiles("alice", runner.RunnerID, runner.Generation, validated), "inactive runner generation")
	server.extensionProfilesMu.RLock()
	assert.Equal(t, validated, server.extensionProfiles)
	server.extensionProfilesMu.RUnlock()

	manifest.Generation = reconnected.Generation
	profile.Options.ReasoningEffort = new("high")
	require.NoError(t, server.registerExtensionProfiles(ctx, manifest))
	other := contextWithPrincipal(t.Context(), administrativePrincipal("bob"))
	require.NoError(t, server.registerExtensionProfiles(other, manifest))
	server.extensionProfilesMu.RLock()
	current := maps.Clone(server.extensionProfiles)
	server.extensionProfilesMu.RUnlock()
	require.Len(t, current, 2)
	for _, registered := range current {
		assert.Equal(t, reconnected.Generation, registered.generation)
		assert.Equal(t, "high", *registered.profile.Options.ReasoningEffort)
	}

	require.ErrorContains(t, server.publishExtensionProfiles("alice", runner.RunnerID, runner.Generation, validated), "inactive runner generation")
	server.extensionProfilesMu.RLock()
	assert.Equal(t, current, server.extensionProfiles)
	server.extensionProfilesMu.RUnlock()
	config, err := server.resolveModelProfile(ctx, runner.RunnerID, profile.Name, "")
	require.NoError(t, err)
	assert.Equal(t, "high", config.ReasoningEffort)
}

func TestChatExecutionContextKeepsProfileOwner(t *testing.T) {
	server := &Server{}
	ctx, cancel := context.WithCancel(contextWithPrincipal(t.Context(), administrativePrincipal("alice")))
	cancel()
	execution := server.chatExecutionContext(ctx)
	assert.NoError(t, execution.Err())
	principal, ok := principalFromContext(execution)
	require.True(t, ok)
	assert.Equal(t, "alice", principal.ID)
}
