package controlplane

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
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
	require.NoError(t, server.registerExtensionProfiles(ctx, manifest))
	_, err = server.resolveModelProfile(ctx, runner.RunnerID, "other", "")
	require.ErrorContains(t, err, "max_tokens")

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
	for _, mode := range []string{"discovery", "direct-chat", "resume", "settings", "settings missing"} {
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
			if mode == "discovery" || mode == "settings" || mode == "settings missing" {
				encoded, err := json.Marshal(options)
				require.NoError(t, err)
				query := url.Values{
					"runnerId": {runner.RunnerID}, "cwd": {request.CWD}, "environmentProfile": {"review"},
					"profile": {"code-search"}, "options": {string(encoded)},
				}
				response := httptest.NewRecorder()
				if mode == "settings" || mode == "settings missing" {
					query.Del("profile")
					if mode == "settings missing" {
						query.Set("profile", "missing")
					}
					server.handleGetChatSettings(response, httptest.NewRequest(http.MethodGet, "/?"+query.Encode(), nil).WithContext(ctx))
					if mode == "settings missing" {
						require.Equal(t, http.StatusBadRequest, response.Code)
						var body map[string]any
						require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
						assert.Equal(t, `profile "missing": not defined in daemon configuration or provided by this workspace's extensions`, body["error"])
						assert.Equal(t, 1, calls)
						link.call = func(context.Context, string, any, any) error {
							return errors.New("internal discovery error")
						}
						response = httptest.NewRecorder()
						server.handleGetChatSettings(response, httptest.NewRequest(http.MethodGet, "/?"+query.Encode(), nil).WithContext(ctx))
						require.Equal(t, http.StatusBadRequest, response.Code)
						require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
						assert.Equal(t, "failed to resolve chat settings", body["error"], "unexpected discovery errors remain private")
						return
					}
					require.Equal(t, http.StatusOK, response.Code, response.Body.String())
					var settings ChatSettingsResponse
					require.NoError(t, json.Unmarshal(response.Body.Bytes(), &settings))
					assert.Equal(t, "code-search", settings.CurrentProfile)
				} else {
					server.handleGetSlashCommands(response, httptest.NewRequest(http.MethodGet, "/?"+query.Encode(), nil).WithContext(ctx))
					require.Equal(t, http.StatusOK, response.Code, response.Body.String())
					assert.NotContains(t, response.Body.String(), "profiles", "definitions remain on the daemon")
				}
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
		require.ErrorIs(t, err, errModelProfileNotFound, "never fall back to another model")
	})
}

func TestAutomaticModelProfileSelection(t *testing.T) {
	for _, test := range []struct {
		name, selected, requested, configured, want, wantError string
		hiddenOnly, noExtensions, noProfiles                   bool
		wantDiscovery                                          bool
	}{
		{name: "extension only", want: "alpha", wantDiscovery: true},
		{name: "extension before configured", configured: "zulu", want: "alpha", wantDiscovery: true},
		{name: "configured before extension", configured: "aardvark", want: "aardvark", wantDiscovery: true},
		{name: "configured default", selected: "zulu", configured: "zulu", want: "zulu"},
		{name: "extension default", selected: "zulu", want: "zulu", wantDiscovery: true},
		{name: "explicit overrides default", selected: "missing", requested: "zulu", want: "zulu", wantDiscovery: true},
		{name: "hidden explicit", requested: "aaa-internal", want: "aaa-internal", wantDiscovery: true},
		{name: "hidden default", selected: "aaa-internal", want: "aaa-internal", wantDiscovery: true},
		{name: "missing explicit", requested: "missing", wantError: "not defined in daemon configuration or provided by this workspace's extensions", wantDiscovery: true},
		{name: "missing default", selected: "missing", wantError: "not defined in daemon configuration or provided by this workspace's extensions", wantDiscovery: true},
		{name: "invalid default", selected: "aaa-invalid", wantError: "max_tokens", wantDiscovery: true},
		{name: "hidden only", hiddenOnly: true, wantError: "no visible model profiles", wantDiscovery: true},
		{name: "none available", noProfiles: true, wantError: "no visible model profiles", wantDiscovery: true},
		{name: "extensions disabled", noExtensions: true, wantError: "no visible model profiles", wantDiscovery: true},
		{name: "configured with extensions disabled", noExtensions: true, configured: "configured", want: "configured", wantDiscovery: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			previous := viper.AllSettings()
			viper.Reset()
			t.Cleanup(func() { viper.Reset(); require.NoError(t, viper.MergeConfigMap(previous)) })
			viper.Set("profile", test.selected)
			if test.configured != "" {
				viper.Set("profiles", map[string]any{
					test.configured: map[string]any{"provider": "openai", "model": "gpt-4o"},
					"aaa-hidden":    map[string]any{"provider": "openai", "model": "gpt-4o", "hidden": true},
				})
			}
			server := newRunnerTestServer(t, "")
			ctx := contextWithPrincipal(t.Context(), administrativePrincipal("alice"))
			link := newRunnerAPITestLink()
			runner, err := server.runnerRegistry.Register(protocol.RegisterParams{
				ProtocolVersions: []int{protocol.Version},
				Capabilities:     protocol.RunnerCapabilities{WorkspaceDiscovery: true},
				Host:             protocol.Host{InstanceID: "profiles", Hostname: "worker", OS: "linux", Arch: "amd64"},
				Workspace:        protocol.Workspace{Path: "/workspace", Name: "workspace"},
			}, link)
			require.NoError(t, err)
			require.NoError(t, server.runnerRegistry.Heartbeat(runner.RunnerID, runner.ConnectionID, runner.Generation, protocol.HeartbeatParams{
				RunnerID: runner.RunnerID, Generation: runner.Generation, State: protocol.RunnerStateIdle,
			}))
			// A prior workspace's earlier-sorting registration must not win.
			require.NoError(t, server.registerExtensionProfiles(ctx, runnerpayload.Manifest{
				RunnerID: runner.RunnerID, Generation: runner.Generation,
				Profiles: []extensions.Profile{{
					Name: "aaa-stale", ExtensionID: "other-workspace",
					Options: llmtypes.ProfileConfig{"provider": "openai", "model": "gpt-4o"},
				}},
			}))
			calls := 0
			link.call = func(_ context.Context, method string, params, result any) error {
				calls++
				assert.Equal(t, protocol.MethodWorkspaceDiscover, method)
				discovery := params.(protocol.WorkspaceDiscoverParams)
				assert.Equal(t, "/workspace/selected", discovery.CWD)
				assert.Equal(t, "review", discovery.EnvironmentProfile)
				var profiles []extensions.Profile
				if !test.noExtensions && !test.noProfiles {
					for _, name := range []string{"zulu", "aaa-internal", "alpha", "aaa-invalid"} {
						if name == test.configured {
							continue
						}
						profile := extensions.Profile{
							Name: name, ExtensionID: "profiles", Hidden: test.hiddenOnly || name == "aaa-internal",
							Options: llmtypes.ProfileConfig{"provider": "openai", "model": "gpt-4o"},
						}
						if name == "aaa-invalid" {
							profile.Options["max_tokens"] = map[string]any{"invalid": true}
						}
						profiles = append(profiles, profile)
					}
				} else if test.noExtensions {
					require.NotNil(t, discovery.Options)
					assert.Equal(t, new(true), discovery.Options.NoExtensions)
				}
				*result.(*runnerpayload.WorkspaceDiscoverResult) = runnerpayload.WorkspaceDiscoverResult{Profiles: profiles}
				return nil
			}
			request := chat.ChatRequest{RunnerID: runner.RunnerID, CWD: "/workspace/selected", EnvironmentProfile: "review"}
			if test.noExtensions {
				request.Options = &llmtypes.ExecutionOptions{NoExtensions: new(true)}
			}
			config, err := server.resolveChatModelProfile(ctx, request, test.requested, "high")
			assert.Equal(t, test.wantDiscovery, calls == 1)
			if test.wantError != "" {
				require.ErrorContains(t, err, test.wantError)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, config.Profile)
			assert.Equal(t, test.want != test.configured, config.ExtensionProfile)
			assert.Equal(t, "high", config.ReasoningEffort)
		})
	}
}

func TestChatSettingsBootstrapWithoutModel(t *testing.T) {
	previous := viper.AllSettings()
	viper.Reset()
	t.Cleanup(func() { viper.Reset(); require.NoError(t, viper.MergeConfigMap(previous)) })
	server := newRunnerTestServer(t, "")
	for _, selected := range []string{"", "extension-default"} {
		viper.Set("profile", selected)
		for _, query := range []string{"", "?bootstrap=true&runnerId=not-connected"} {
			response := httptest.NewRecorder()
			server.handleGetChatSettings(response, httptest.NewRequest(http.MethodGet, "/api/chat/settings"+query, nil))
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			var settings ChatSettingsResponse
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &settings))
			assert.Equal(t, 1, settings.ConversationHierarchyVersion)
			assert.Empty(t, settings.CurrentProfile)
			assert.Empty(t, settings.Model)
		}
	}
	for _, options := range []string{`{`, `{"noExtensions":"wrong"}`, `{"model":"not-a-restriction"}`} {
		response := httptest.NewRecorder()
		query := url.Values{"runnerId": {"runner"}, "options": {options}}
		server.handleGetChatSettings(response, httptest.NewRequest(http.MethodGet, "/api/chat/settings?"+query.Encode(), nil))
		assert.Equal(t, http.StatusBadRequest, response.Code)
		assert.Contains(t, response.Body.String(), "invalid settings options")
	}
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

type profileReloadTestController struct {
	agentenv.RemoteController
	result       runnerpayload.RunReloadResult
	err          error
	closedRunID  string
	closedStatus protocol.RunStatus
}

func (c *profileReloadTestController) OpenRun(context.Context, string, protocol.RunOpenParams) (runnerpayload.Manifest, error) {
	return c.result.Manifest, c.err
}

func (c *profileReloadTestController) ReloadRun(context.Context, protocol.RunReloadParams) (runnerpayload.RunReloadResult, error) {
	return c.result, c.err
}

func (c *profileReloadTestController) CloseRun(_ context.Context, runID string, status protocol.RunStatus, _ error) error {
	c.closedRunID, c.closedStatus = runID, status
	return nil
}

func TestProfileRegisteringControllerReloadRegistersProfilesOrStopsRun(t *testing.T) {
	previous := viper.AllSettings()
	viper.Reset()
	t.Cleanup(func() { viper.Reset(); require.NoError(t, viper.MergeConfigMap(previous)) })
	viper.Set("provider", "openai")
	viper.Set("model", "gpt-4o")
	alice := contextWithPrincipal(t.Context(), administrativePrincipal("alice"))
	for _, test := range []struct {
		name       string
		ctx        context.Context
		refusal    string
		reloadErr  error
		wantErr    string
		registered bool
	}{
		{name: "success", ctx: alice, registered: true},
		{name: "refusal", ctx: alice, refusal: "background workers retained"},
		{name: "reload error", ctx: alice, reloadErr: errors.New("lost reply"), wantErr: "lost reply"},
		// Registration requires a principal, so an anonymous caller exercises the failure path.
		{name: "registration failure", ctx: t.Context(), wantErr: "failed to register reloaded extension profiles"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := newRunnerTestServer(t, "")
			runner, err := server.runnerRegistry.Register(protocol.RegisterParams{
				ProtocolVersions: []int{protocol.Version},
				Host:             protocol.Host{InstanceID: "one", Hostname: "one", OS: "linux", Arch: "amd64"},
				Workspace:        protocol.Workspace{Path: "/workspace", Name: "workspace"},
			}, newRunnerAPITestLink())
			require.NoError(t, err)
			inner := &profileReloadTestController{err: test.reloadErr, result: runnerpayload.RunReloadResult{
				Refusal: test.refusal,
				Manifest: runnerpayload.Manifest{
					RunnerID: runner.RunnerID, Generation: runner.Generation, EnvironmentRevision: 2,
					Profiles: []extensions.Profile{{
						Name: "code-search", ExtensionID: "skills/code-search",
						Options: llmtypes.ProfileConfig{"provider": "openai", "model": "gpt-4o", "reasoning_effort": "high"},
					}},
				},
			}}
			controller := profileRegisteringController{RemoteController: inner, server: server}

			result, err := controller.ReloadRun(test.ctx, protocol.RunReloadParams{RunID: "run", ExpectedEnvironmentRevision: 1})

			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, inner.result, result)
			}
			if test.name == "registration failure" {
				assert.Equal(t, runnerpayload.RunReloadResult{}, result, "never publish a replacement whose profiles were rejected")
				assert.Equal(t, "run", inner.closedRunID)
				assert.Equal(t, protocol.RunStatusFailed, inner.closedStatus)
			} else {
				assert.Empty(t, inner.closedRunID, "only a rejected replacement stops the run here")
			}
			config, err := server.resolveModelProfile(alice, runner.RunnerID, "code-search", "")
			if test.registered {
				require.NoError(t, err)
				assert.Equal(t, "high", config.ReasoningEffort)
			} else {
				require.ErrorContains(t, err, "not registered")
			}
		})
	}
}

func TestExtensionProfilesIsolateInvalidSettings(t *testing.T) {
	previous := viper.AllSettings()
	viper.Reset()
	t.Cleanup(func() {
		viper.Reset()
		require.NoError(t, viper.MergeConfigMap(previous))
	})
	server := newRunnerTestServer(t, "")
	ctx := contextWithPrincipal(t.Context(), administrativePrincipal("alice"))
	sink := &recordingChatSink{}
	broker := newWebUIInputBroker("conversation", sink)
	t.Cleanup(broker.close)
	t.Cleanup(broker.setOwner(ctx, "client", sink))
	ctx = extensions.ContextWithUIInputBroker(ctx, broker)
	runner, err := server.runnerRegistry.Register(protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version},
		Host:             protocol.Host{InstanceID: "profiles"},
		Workspace:        protocol.Workspace{Path: "/workspace", Name: "workspace"},
	}, newRunnerAPITestLink())
	require.NoError(t, err)
	profile := extensions.Profile{
		Name:        "rush",
		ExtensionID: "profile-tools",
		Options: llmtypes.ProfileConfig{
			"provider":  "openai",
			"model":     "custom-model",
			"tool_mode": "full",
		},
	}
	other := profile.Clone()
	other.Name = "other"
	inner := &profileReloadTestController{
		result: runnerpayload.RunReloadResult{
			Manifest: runnerpayload.Manifest{
				RunnerID:   runner.RunnerID,
				Generation: runner.Generation,
				Profiles:   []extensions.Profile{profile, other},
			},
		},
	}
	controller := profileRegisteringController{
		RemoteController: inner,
		server:           server,
		profile:          "other",
	}
	open := protocol.RunOpenParams{RunID: "run"}
	reload := protocol.RunReloadParams{RunID: "run"}
	_, err = controller.OpenRun(ctx, runner.RunnerID, open)
	require.NoError(t, err)
	require.Empty(t, sink.Events())

	// An invalid replacement removes the old selectable profile, not its sibling.
	profile.Options["tool_mode"] = "Patch"
	_, err = controller.ReloadRun(ctx, reload)
	require.NoError(t, err)
	assert.Empty(t, inner.closedRunID)
	options := server.modelProfileOptions(ctx, runner.RunnerID, "rush", true)
	assert.Equal(t, []ChatProfileOption{{Name: "other", Scope: "extension"}}, options)
	_, err = server.resolveModelProfile(ctx, runner.RunnerID, "other", "")
	require.NoError(t, err)
	_, err = server.resolveChatModelProfile(ctx, chat.ChatRequest{RunnerID: runner.RunnerID}, "rush", "")
	require.ErrorContains(t, err, "extension profile \"rush\" from \"profile-tools\" is unavailable")
	require.ErrorContains(t, err, "tool_mode must be full or patch")
	events := sink.Events()
	require.Len(t, events, 1)
	assert.Equal(t, "ui-notification", events[0].Kind)
	require.NotNil(t, events[0].UINotify)
	assert.Equal(t, err.Error(), events[0].UINotify.Message)

	// Only a run selecting the invalid profile must stop, on open or reload.
	controller.profile = "rush"
	_, err = controller.OpenRun(ctx, runner.RunnerID, open)
	require.ErrorContains(t, err, "tool_mode must be full or patch")
	assert.Equal(t, "run", inner.closedRunID)
	assert.Equal(t, protocol.RunStatusFailed, inner.closedStatus)
	inner.closedRunID = ""
	_, err = controller.ReloadRun(ctx, reload)
	require.ErrorContains(t, err, "tool_mode must be full or patch")
	assert.Equal(t, "run", inner.closedRunID)

	// A corrected declaration replaces the stored diagnostic and is usable again.
	profile.Options["tool_mode"] = "patch"
	_, err = controller.OpenRun(ctx, runner.RunnerID, open)
	require.NoError(t, err)
	config, err := server.resolveModelProfile(ctx, runner.RunnerID, "rush", "")
	require.NoError(t, err)
	assert.Equal(t, llmtypes.ToolModePatch, config.ToolMode)
	assert.Contains(t, server.modelProfileOptions(ctx, runner.RunnerID, "", false),
		ChatProfileOption{Name: "rush", Scope: "extension"})
}

func TestConversationReasoningEffortOptionsUseCallersExtensionProfile(t *testing.T) {
	server := newRunnerTestServer(t, "")
	alice := contextWithPrincipal(t.Context(), administrativePrincipal("alice"))
	runner, err := server.runnerRegistry.Register(protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version},
		Host:             protocol.Host{InstanceID: "one", Hostname: "one", OS: "linux", Arch: "amd64"},
		Workspace:        protocol.Workspace{Path: "/workspace", Name: "workspace"},
	}, newRunnerAPITestLink())
	require.NoError(t, err)
	profile := extensions.Profile{
		Name: "code-search", ExtensionID: "skills/code-search", Hidden: true,
		Options: llmtypes.ProfileConfig{
			"provider": "openai", "model": "copilot-private-model", "reasoning_effort": "high",
			"allowed_reasoning_efforts": []string{"medium", "high"},
			"openai":                    map[string]any{"platform": "copilot"},
		},
	}
	manifest := runnerpayload.Manifest{RunnerID: runner.RunnerID, Generation: runner.Generation, Profiles: []extensions.Profile{profile}}
	require.NoError(t, server.registerExtensionProfiles(alice, manifest))
	config, err := server.resolveModelProfile(alice, runner.RunnerID, profile.Name, "")
	require.NoError(t, err)
	metadata, err := conversations.AddConfigSnapshot(nil, config)
	require.NoError(t, err)
	record := &conversations.GetConversationResponse{ID: "conv", Provider: "openai", Metadata: metadata}

	assert.Equal(t, []string{"medium", "high"}, server.conversationReasoningEffortOptions(alice, record, runner.RunnerID))
	bob := contextWithPrincipal(t.Context(), administrativePrincipal("bob"))
	assert.Equal(t, []string{"high"}, server.conversationReasoningEffortOptions(bob, record, runner.RunnerID),
		"an unresolvable extension policy offers only the current effort")
}
