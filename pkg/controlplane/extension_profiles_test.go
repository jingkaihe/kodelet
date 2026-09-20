package controlplane

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtensionProfilesOwnershipDiscoveryAndSnapshots(t *testing.T) {
	previous := viper.AllSettings()
	viper.Reset()
	t.Cleanup(func() { viper.Reset(); require.NoError(t, viper.MergeConfigMap(previous)) })
	viper.Set("profile", "deep")
	viper.Set("profiles", map[string]any{
		"deep": map[string]any{
			"provider": "openai", "model": "gpt-4o",
			"reasoning_effort": "xhigh", "allowed_reasoning_efforts": []string{"medium", "high", "xhigh"},
			"openai": map[string]any{"platform": "codex", "base_url": "https://active.invalid"},
		},
		"internal": map[string]any{"provider": "openai", "hidden": true, "model": "gpt-4o"},
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
		Options: llmtypes.ProfileConfig{
			"provider": "openai", "model": "copilot-private-model", "reasoning_effort": "none",
			"openai": map[string]any{"platform": "copilot"},
		},
	}
	manifest := runnerpayload.Manifest{RunnerID: runner.RunnerID, Generation: runner.Generation, Profiles: []extensions.Profile{profile}}
	require.NoError(t, server.registerExtensionProfiles(ctx, manifest))
	config, err := server.resolveModelProfile(ctx, runner.RunnerID, profile.Name, "")
	require.NoError(t, err)
	assert.Equal(t, "none", config.ReasoningEffort)
	assert.Equal(t, "copilot-private-model", config.Model)
	require.NotNil(t, config.OpenAI)
	assert.Equal(t, "copilot", config.OpenAI.Platform)
	assert.Nil(t, config.OpenAI.Models, "Copilot registration must not require an explicit model catalog")
	assert.Empty(t, config.AllowedReasoningEfforts)
	assert.Equal(t, profile.Name, config.Profile)
	assert.True(t, config.ExtensionProfile)
	assert.False(t, config.ExecutionOptions.HasModelOptions())
	assert.Equal(t, "deep", viper.GetString("profile"))
	_, err = server.resolveModelProfile(other, runner.RunnerID, profile.Name, "")
	require.ErrorContains(t, err, "not registered")
	_, err = server.resolveModelProfile(ctx, second.RunnerID, profile.Name, "")
	require.ErrorContains(t, err, "not registered")
	require.ErrorContains(t, server.registerExtensionProfiles(context.Background(), manifest), "authenticated")
	assert.NotContains(t, server.modelProfileOptions(ctx, runner.RunnerID, "deep", false), ChatProfileOption{Name: profile.Name, Scope: "extension", Hidden: true})
	assert.Contains(t, server.modelProfileOptions(ctx, runner.RunnerID, "deep", true), ChatProfileOption{Name: profile.Name, Scope: "extension", Hidden: true})
	assert.Contains(t, server.modelProfileOptions(ctx, runner.RunnerID, profile.Name, false), ChatProfileOption{Name: profile.Name, Scope: "extension", Hidden: true})
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
	assert.Empty(t, target.Profile)

	visible := profile.Clone()
	visible.Name, visible.Hidden = "review", false
	visible.ExtensionID = "other-extension"
	require.NoError(t, server.registerExtensionProfiles(ctx, runnerpayload.Manifest{
		RunnerID: runner.RunnerID, Generation: runner.Generation, Profiles: []extensions.Profile{visible},
	}))
	assert.Contains(t, server.modelProfileOptions(ctx, runner.RunnerID, "review", false), ChatProfileOption{Name: "review", Scope: "extension"})
	visible.Name = "deep"
	require.ErrorContains(t, server.registerExtensionProfiles(ctx, runnerpayload.Manifest{
		RunnerID: runner.RunnerID, Generation: runner.Generation, Profiles: []extensions.Profile{visible},
	}), "daemon-configured profile")
	for _, name := range []string{"", "deep", "internal"} {
		configured, err := server.resolveModelProfile(ctx, runner.RunnerID, name, "")
		require.NoError(t, err)
		assert.False(t, configured.ExtensionProfile)
	}

	metadata, err := conversations.AddConfigSnapshot(nil, config)
	require.NoError(t, err)
	profile.Options["reasoning_effort"] = "high"
	manifest.Profiles = []extensions.Profile{profile}
	require.NoError(t, server.registerExtensionProfiles(ctx, manifest))
	config, err = server.resolveModelProfile(ctx, runner.RunnerID, profile.Name, "")
	require.NoError(t, err)
	assert.Equal(t, "high", config.ReasoningEffort)
	_, err = chat.ResolveConfigForExistingConversation(&conversations.GetConversationResponse{Metadata: metadata})
	require.ErrorContains(t, err, "initialize its extension", "resume must resolve the caller's live registration")

	manifest.Profiles[0].ExtensionID = "other-extension"
	require.ErrorContains(t, server.registerExtensionProfiles(ctx, manifest), "already registered")
	manifest.Profiles[0].Name = "other"
	manifest.Profiles[0].Options["max_tokens"] = map[string]any{"invalid": true}
	require.ErrorContains(t, server.registerExtensionProfiles(ctx, manifest), "invalid extension profile")
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
		Options: llmtypes.ProfileConfig{"provider": "openai", "model": "gpt-5.6-luna", "reasoning_effort": "none"},
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
	profile.Options["reasoning_effort"] = "high"
	require.NoError(t, server.registerExtensionProfiles(ctx, manifest))
	other := contextWithPrincipal(t.Context(), administrativePrincipal("bob"))
	require.NoError(t, server.registerExtensionProfiles(other, manifest))
	server.extensionProfilesMu.RLock()
	current := maps.Clone(server.extensionProfiles)
	server.extensionProfilesMu.RUnlock()
	require.Len(t, current, 2)
	for _, registered := range current {
		assert.Equal(t, reconnected.Generation, registered.generation)
		assert.Equal(t, "high", registered.profile.Options["reasoning_effort"])
	}

	require.ErrorContains(t, server.publishExtensionProfiles("alice", runner.RunnerID, runner.Generation, validated), "inactive runner generation")
	server.extensionProfilesMu.RLock()
	assert.Equal(t, current, server.extensionProfiles)
	server.extensionProfilesMu.RUnlock()
	config, err := server.resolveModelProfile(ctx, runner.RunnerID, profile.Name, "")
	require.NoError(t, err)
	assert.Equal(t, "high", config.ReasoningEffort)
}

func TestExtensionProfilesBootstrapUnderRequestingCaller(t *testing.T) {
	previous := viper.AllSettings()
	viper.Reset()
	t.Cleanup(func() { viper.Reset(); require.NoError(t, viper.MergeConfigMap(previous)) })
	viper.Set("provider", "openai")
	viper.Set("model", "gpt-4o")
	server := newRunnerTestServer(t, "")
	server.conversationService = &mockConversationService{getFunc: func(_ context.Context, id string) (*conversations.GetConversationResponse, error) {
		if id != "saved" {
			return nil, convtypes.ErrConversationNotFound
		}
		return &conversations.GetConversationResponse{
			ID: "saved", CWD: "/workspace/selected", Metadata: map[string]any{chat.EnvironmentProfileMetadataKey: "review"},
		}, nil
	}}
	profiles := []extensions.Profile{
		{Name: "code-search", ExtensionID: "skills/code-search", Options: llmtypes.ProfileConfig{"provider": "openai", "model": "gpt-4o"}},
		{Name: "read-conversation", ExtensionID: "skills/read-conversation", Options: llmtypes.ProfileConfig{"provider": "openai", "model": "gpt-4o"}},
	}
	link := newRunnerAPITestLink()
	runner, err := server.runnerRegistry.Register(protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version}, Capabilities: protocol.RunnerCapabilities{WorkspaceDiscovery: true},
		Host:      protocol.Host{InstanceID: "profiles", Hostname: "worker", OS: "linux", Arch: "amd64"},
		Workspace: protocol.Workspace{Path: "/workspace", Name: "workspace"},
	}, link)
	require.NoError(t, err)
	require.NoError(t, server.runnerRegistry.Heartbeat(runner.RunnerID, runner.ConnectionID, runner.Generation, protocol.HeartbeatParams{
		RunnerID: runner.RunnerID, Generation: runner.Generation, State: protocol.RunnerStateIdle,
	}))
	parentProfile := profiles[0].Clone()
	parentProfile.Options["model"] = "parent-only-model"
	require.NoError(t, server.registerExtensionProfiles(contextWithPrincipal(t.Context(), administrativePrincipal("web-user")), runnerpayload.Manifest{
		RunnerID: runner.RunnerID, Generation: runner.Generation, Profiles: []extensions.Profile{parentProfile, profiles[1]},
	}))
	options := &llmtypes.ExecutionOptions{NoSkills: new(true), AllowedTools: &[]string{"grep", "file_read"}}
	for _, mode := range []string{"discovery", "direct-chat", "resume"} {
		t.Run(mode, func(t *testing.T) {
			ctx := contextWithPrincipal(t.Context(), administrativePrincipal(t.Name()))
			_, err := server.resolveModelProfile(ctx, runner.RunnerID, "code-search", "")
			require.ErrorIs(t, err, errExtensionProfileNotRegistered)
			calls := 0
			link.call = func(_ context.Context, method string, params, result any) error {
				calls++
				assert.Equal(t, protocol.MethodWorkspaceDiscover, method, "bootstrap must not open a model turn")
				assert.Equal(t, protocol.WorkspaceDiscoverParams{CWD: "/workspace/selected", EnvironmentProfile: "review", Options: options}, params)
				*result.(*runnerpayload.WorkspaceDiscoverResult) = runnerpayload.WorkspaceDiscoverResult{
					WorkspaceDiscoverResult: protocol.WorkspaceDiscoverResult{CWD: "/workspace/selected", EnvironmentProfile: "review"}, Profiles: profiles,
				}
				return nil
			}
			request := chat.ChatRequest{
				ConversationID: "new-child", RunnerID: runner.RunnerID, CWD: "/workspace/selected", EnvironmentProfile: "review", Options: options,
			}
			if mode == "discovery" {
				encoded, err := json.Marshal(options)
				require.NoError(t, err)
				query := url.Values{
					"runnerId": {runner.RunnerID}, "cwd": {request.CWD}, "environmentProfile": {"review"},
					"profile": {"code-search"}, "options": {string(encoded)},
				}
				response := httptest.NewRecorder()
				server.handleGetSlashCommands(response, httptest.NewRequest(http.MethodGet, "/?"+query.Encode(), nil).WithContext(ctx))
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				assert.NotContains(t, response.Body.String(), "profiles", "definitions remain on the daemon")
			} else {
				if mode == "resume" {
					request.ConversationID, request.CWD, request.EnvironmentProfile = "saved", "", ""
				}
				_, err = server.resolveChatModelProfile(ctx, request, "code-search", "")
				require.NoError(t, err)
			}
			for _, profile := range profiles {
				config, err := server.resolveChatModelProfile(ctx, request, profile.Name, "")
				require.NoError(t, err)
				assert.True(t, config.ExtensionProfile)
				assert.Equal(t, "gpt-4o", config.Model, "use the runner's definitions, not another caller's cache")
			}
			assert.Equal(t, 1, calls, "cached registrations do not need another probe")
		})
	}
	t.Run("extensions disabled", func(t *testing.T) {
		ctx := contextWithPrincipal(t.Context(), administrativePrincipal(t.Name()))
		options := &llmtypes.ExecutionOptions{NoExtensions: new(true)}
		link.call = func(_ context.Context, _ string, params, _ any) error {
			assert.Equal(t, options, params.(protocol.WorkspaceDiscoverParams).Options)
			return nil
		}
		_, err := server.resolveChatModelProfile(ctx, chat.ChatRequest{RunnerID: runner.RunnerID, Options: options}, "code-search", "")
		require.ErrorIs(t, err, errExtensionProfileNotRegistered, "never fall back to another model")
	})
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
