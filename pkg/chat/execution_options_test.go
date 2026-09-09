package chat

import (
	"encoding/json"
	"testing"

	conversationservice "github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecutionOptionsChatRequestJSON(t *testing.T) {
	for _, input := range []string{
		`null`, `[]`, `{"message":"hello","options":null}`, `{"message":"hello","Options":null}`,
		`{"message":"hello","oPtIoNs": null }`, `{"message":"hello","options":null,"Options":{}}`,
		`{"message":"hello","Options":null,"options":{}}`, `{"message":"hello","options":[]}`,
		`{"message":"hello","options":{"noTools":null}}`, `{"message":"hello","options":{"noSave":true}}`,
		`{"message":"hello","options":{"allowedTools":null}}`, `{"message":"hello","options":{"maxTurns":-1}}`,
		`{"message":"hello","provider":"openai"}`, `{"message":"hello","clientCapabilities":{"unknown":true}}`,
		`{"message":"hello","options":{}} {}`,
	} {
		t.Run(input, func(t *testing.T) {
			request := ChatRequest{Message: "original"}
			require.Error(t, json.Unmarshal([]byte(input), &request))
			assert.Equal(t, ChatRequest{Message: "original"}, request)
			require.Error(t, request.UnmarshalJSON([]byte(input)))
			assert.Equal(t, ChatRequest{Message: "original"}, request)
		})
	}
	var request ChatRequest
	require.NoError(t, json.Unmarshal([]byte(`{"message":"hello"}`), &request))
	assert.Nil(t, request.Options)
	require.NoError(t, json.Unmarshal([]byte(`{"message":"hello","options":{}}`), &request))
	assert.Equal(t, &llmtypes.ExecutionOptions{}, request.Options)
	require.NoError(t, json.Unmarshal([]byte(`{"message":"hello","options":{"noTools":false,"maxTurns":0,"allowedTools":[]}}`), &request))
	assert.Equal(t, new(false), request.Options.NoTools)
	assert.Equal(t, new(0), request.Options.MaxTurns)
	assert.True(t, request.Options.ToolsDisabled())
	require.NoError(t, json.Unmarshal([]byte(`{"message":"hello","OPTIONS":{}}`), &request))
	assert.Equal(t, &llmtypes.ExecutionOptions{}, request.Options)
}

func TestResolveExtensionProfileIsolatedFromDaemonDefaults(t *testing.T) {
	previous := viper.AllSettings()
	viper.Reset()
	t.Cleanup(func() { viper.Reset(); require.NoError(t, viper.MergeConfigMap(previous)) })
	viper.Set("provider", "anthropic")
	viper.Set("model", "parent-only")
	viper.Set("reasoning_effort", "high")
	viper.Set("allowed_reasoning_efforts", []string{"high"})
	viper.Set("aliases", map[string]string{"custom-model": "daemon-model"})
	viper.Set("allowed_tools", []string{"file_read"})
	viper.Set("openai", map[string]any{"platform": "openai", "base_url": "https://daemon.invalid", "api_key_env_var": "DAEMON_KEY"})
	viper.Set("profile", "deep")
	viper.Set("profiles.deep", map[string]any{"provider": "openai", "openai": map[string]any{"platform": "codex"}})
	profile := extensions.Profile{Name: "code-search", ExtensionID: "search", Options: llmtypes.ProfileConfig{
		"provider": "openai", "model": "custom-model", "reasoning_effort": "none",
		"openai": map[string]any{"platform": "copilot"},
	}}
	config, err := ResolveExtensionProfile(profile, "")
	require.NoError(t, err)
	assert.Equal(t, "code-search", config.Profile)
	assert.True(t, config.ExtensionProfile)
	assert.Equal(t, "openai", config.Provider)
	assert.Equal(t, "custom-model", config.Model)
	assert.Equal(t, "none", config.ReasoningEffort)
	assert.Equal(t, "copilot", config.OpenAI.Platform)
	assert.Empty(t, config.OpenAI.BaseURL)
	assert.Empty(t, config.OpenAI.APIKeyEnvVar)
	assert.Empty(t, config.OpenAI.Models, "no duplicate model catalog is required")
	assert.Empty(t, config.AllowedReasoningEfforts)
	assert.Equal(t, &[]string{"file_read"}, config.EnvironmentOptions().AllowedTools)
	assert.Empty(t, config.WeakModel)
	profile.Options["allowed_reasoning_efforts"] = []string{"none", "low"}
	config, err = ResolveExtensionProfile(profile, "low")
	require.NoError(t, err)
	assert.Equal(t, "low", config.ReasoningEffort)
	_, err = ResolveExtensionProfile(profile, "high")
	require.ErrorContains(t, err, "not included")
	assert.Equal(t, "none", profile.Options["reasoning_effort"])
	profile.Options["openai"] = map[string]any{"websocket_mode": "not-a-boolean"}
	_, err = ResolveExtensionProfile(profile, "")
	require.ErrorContains(t, err, "failed to unmarshal configuration")
}

func TestIsolatedProfileCannotWidenHostPermissions(t *testing.T) {
	previous := viper.AllSettings()
	viper.Reset()
	t.Cleanup(func() { viper.Reset(); require.NoError(t, viper.MergeConfigMap(previous)) })
	viper.Set("allowed_tools", []string{"file_read", "bash"})
	viper.Set("allowed_commands", []string{"go test"})
	viper.Set("skills.enabled", false)
	require.NoError(t, viper.MergeConfigMap(map[string]any{"extensions": map[string]any{"enabled": false}}))
	viper.Set("extensions.local_dir", "/runner/extensions")
	profile := extensions.Profile{Name: "search", ExtensionID: "extension", Options: llmtypes.ProfileConfig{
		"provider": "openai", "model": "custom-model",
		"allowed_tools":    []string{"file_read", "bash", "file_write"},
		"allowed_commands": []string{"rm -rf"},
		"skills":           map[string]any{"enabled": true}, "extensions": map[string]any{"enabled": true},
	}}
	config, err := ResolveExtensionProfile(profile, "")
	require.NoError(t, err)
	assert.True(t, config.EnvironmentOptions().ToolAllowed("file_read"))
	assert.False(t, config.EnvironmentOptions().ToolAllowed("file_write"))
	assert.False(t, config.EnvironmentOptions().ToolAllowed("bash"), "disjoint commands must deny all, not inherit")
	assert.True(t, *config.EnvironmentOptions().NoSkills)
	assert.True(t, *config.EnvironmentOptions().NoExtensions)
	config, err = resolveExecutionOptions(t.Context(), ChatRequest{Options: &llmtypes.ExecutionOptions{
		AllowedTools: &[]string{"file_read", "file_write", "bash"}, AllowedCommands: &[]string{"go test"},
		NoSkills: new(false), NoExtensions: new(false), MaxTurns: new(2),
	}}, config)
	require.NoError(t, err)
	assert.True(t, config.EnvironmentOptions().ToolAllowed("file_read"))
	assert.False(t, config.EnvironmentOptions().ToolAllowed("file_write"))
	assert.False(t, config.EnvironmentOptions().ToolAllowed("bash"))
	assert.True(t, *config.EnvironmentOptions().NoSkills)
	assert.True(t, *config.EnvironmentOptions().NoExtensions)
}

func TestRemoteExtensionSnapshotUsesMatchingLiveProvider(t *testing.T) {
	previous := viper.AllSettings()
	viper.Reset()
	t.Cleanup(func() { viper.Reset(); require.NoError(t, viper.MergeConfigMap(previous)) })
	viper.Set("provider", "openai")
	viper.Set("model", "gpt-4o")
	viper.Set("allowed_tools", []string{"file_read"})
	viper.Set("openai", map[string]any{
		"platform":        "openai",
		"base_url":        "https://daemon.invalid",
		"api_key_env_var": "DAEMON_KEY",
		"websocket_mode":  true,
		"enable_search":   true,
	})
	original, err := ResolveExtensionProfile(extensions.Profile{
		Name:        "code-search",
		ExtensionID: "search-extension",
		Options: llmtypes.ProfileConfig{
			"provider":         "openai",
			"model":            "gpt-5.6-luna",
			"reasoning_effort": "none",
			"openai": map[string]any{
				"base_url":        "https://original.invalid",
				"api_key_env_var": "ORIGINAL_KEY",
				"api_mode":        "responses",
				"service_tier":    "fast",
				"websocket_mode":  false,
				"enable_search":   false,
			},
		},
	}, "")
	require.NoError(t, err)
	for _, test := range []struct {
		name       string
		change     func(*llmtypes.Config)
		absent     bool
		noResolver bool
		ordinary   bool
		configured bool
		wantLive   bool
	}{
		{name: "matching registration", wantLive: true},
		{name: "implicit platform", change: func(c *llmtypes.Config) { c.OpenAI.Platform = "" }, wantLive: true},
		{name: "registration absent", absent: true},
		{name: "resolver absent", noResolver: true},
		{name: "provider changed", change: func(c *llmtypes.Config) { c.Provider = "anthropic" }},
		{name: "platform changed", change: func(c *llmtypes.Config) { c.OpenAI.Platform = "codex" }},
		{name: "configured name reused", configured: true},
		{name: "ordinary conversation", ordinary: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			saved := original.Clone()
			saved.ExtensionProfile = !test.ordinary
			metadata, err := conversationservice.AddConfigSnapshot(nil, saved)
			require.NoError(t, err)
			record := &conversationservice.GetConversationResponse{Metadata: metadata}
			if test.configured {
				viper.Set("profiles.code-search", map[string]any{
					"model":  "gpt-4o",
					"openai": map[string]any{"platform": "codex", "base_url": "https://reused.invalid"},
				})
				t.Cleanup(func() { viper.Set("profiles", nil) })
			}
			live := original.Clone()
			live.Model = "gpt-5.5"
			live.ReasoningEffort = "high"
			live.OpenAI.ServiceTier = llmtypes.OpenAIServiceTierDefault
			live.OpenAI.BaseURL = "https://live.invalid"
			live.OpenAI.APIKeyEnvVar = "LIVE_KEY"
			if test.change != nil {
				test.change(&live)
			}
			ctx := t.Context()
			lookups := 0
			if !test.noResolver {
				ctx = ContextWithProfileResolver(ctx, func(name, effort string) (llmtypes.Config, error) {
					lookups++
					assert.Equal(t, "code-search", name)
					assert.Empty(t, effort)
					if test.absent {
						return llmtypes.Config{}, assert.AnError
					}
					if test.configured {
						return ResolveConfigForNewConversation(name, effort)
					}
					return live, nil
				})
			}
			resumed, err := resolveConfigForExistingConversation(ctx, record)
			if !test.wantLive && !test.ordinary {
				require.ErrorContains(t, err, "initialize its extension on this runner before resuming")
				return
			}
			require.NoError(t, err)
			if test.noResolver || test.ordinary {
				assert.Zero(t, lookups)
			} else {
				assert.Equal(t, 1, lookups)
			}
			assert.Equal(t, saved.Profile, resumed.Profile)
			assert.Equal(t, saved.ExtensionProfile, resumed.ExtensionProfile)
			assert.Equal(t, saved.Provider, resumed.Provider)
			assert.Equal(t, saved.Model, resumed.Model)
			assert.Equal(t, saved.ReasoningEffort, resumed.ReasoningEffort)
			assert.Equal(t, saved.OpenAI.ServiceTier, resumed.OpenAI.ServiceTier)
			assert.Equal(t, saved.OpenAI.Platform, resumed.OpenAI.Platform)
			assert.Equal(t, saved.OpenAI.APIMode, resumed.OpenAI.APIMode)
			assert.Equal(t, &[]string{"file_read"}, resumed.EnvironmentOptions().AllowedTools)
			if test.wantLive {
				assert.Equal(t, "https://live.invalid", resumed.OpenAI.BaseURL)
				assert.Equal(t, "LIVE_KEY", resumed.OpenAI.APIKeyEnvVar)
				assert.Equal(t, new(false), resumed.OpenAI.WebSocketMode)
				assert.Equal(t, new(false), resumed.OpenAI.EnableSearch)
			} else {
				assert.Equal(t, "https://daemon.invalid", resumed.OpenAI.BaseURL)
				assert.Equal(t, "DAEMON_KEY", resumed.OpenAI.APIKeyEnvVar)
				assert.Equal(t, new(true), resumed.OpenAI.WebSocketMode)
				assert.Equal(t, new(true), resumed.OpenAI.EnableSearch)
			}
			assert.Equal(t, "gpt-5.5", live.Model)
			assert.Equal(t, llmtypes.OpenAIServiceTierDefault, live.OpenAI.ServiceTier)
		})
	}
}

func TestRegisteredProfileProviderSettings(t *testing.T) {
	previous := viper.AllSettings()
	viper.Reset()
	t.Cleanup(func() { viper.Reset(); require.NoError(t, viper.MergeConfigMap(previous)) })
	viper.Set("provider", "openai")
	viper.Set("model", "gpt-4o")
	viper.Set("allowed_tools", []string{"file_read"})
	viper.Set("openai", map[string]any{
		"platform":        "openai",
		"base_url":        "https://api-default.invalid",
		"api_key_env_var": "DAEMON_API_KEY",
		"enable_search":   true,
	})
	profile := extensions.Profile{
		Name:        "code-search",
		ExtensionID: "search-extension",
		Options: llmtypes.ProfileConfig{
			"provider":         "openai",
			"model":            "gpt-5.6-luna",
			"reasoning_effort": "none",
			"openai": map[string]any{
				"platform":               "codex",
				"api_mode":               "responses",
				"service_tier":           "fast",
				"text_verbosity":         "low",
				"enable_search":          false,
				"websocket_mode":         false,
				"future_provider_option": map[string]any{"enabled": true},
			},
		},
	}
	config, err := ResolveExtensionProfile(profile, "")
	require.NoError(t, err)
	assert.Equal(t, "codex", config.OpenAI.Platform)
	assert.Equal(t, llmtypes.OpenAIAPIModeResponses, config.OpenAI.APIMode)
	assert.Equal(t, llmtypes.OpenAIServiceTierFast, config.OpenAI.ServiceTier)
	assert.Equal(t, llmtypes.OpenAITextVerbosityLow, config.OpenAI.TextVerbosity)
	assert.Equal(t, new(false), config.OpenAI.EnableSearch)
	assert.Equal(t, new(false), config.OpenAI.WebSocketMode)
	assert.Empty(t, config.OpenAI.BaseURL)
	assert.Empty(t, config.OpenAI.APIKeyEnvVar)
	assert.Equal(t, &[]string{"file_read"}, config.EnvironmentOptions().AllowedTools)

	profile.Options["openai"] = map[string]any{"enable_search": false, "service_tier": "default"}
	config, err = ResolveExtensionProfile(profile, "")
	require.NoError(t, err)
	assert.Empty(t, config.OpenAI.BaseURL)
	assert.Empty(t, config.OpenAI.APIKeyEnvVar)
	assert.Equal(t, new(false), config.OpenAI.EnableSearch)
	assert.True(t, viper.GetBool("openai.enable_search"))
	profile.Options["openai"] = map[string]any{
		"platform":        "custom",
		"base_url":        "https://custom.invalid",
		"api_key_env_var": "CUSTOM_KEY",
	}
	profile.Options["model"] = "custom-model"
	config, err = ResolveExtensionProfile(profile, "")
	require.NoError(t, err)
	assert.Equal(t, "custom", config.OpenAI.Platform)
	assert.Equal(t, "https://custom.invalid", config.OpenAI.BaseURL)
	assert.Equal(t, "CUSTOM_KEY", config.OpenAI.APIKeyEnvVar)
	assert.Equal(t, "custom-model", config.Model)
	profile.Options["openai"].(map[string]any)["websocket_mode"] = "not-a-boolean"
	_, err = ResolveExtensionProfile(profile, "")
	require.ErrorContains(t, err, "failed to unmarshal configuration")
}

func TestRegisteredClaudeSubscriptionProfile(t *testing.T) {
	previous := viper.AllSettings()
	viper.Reset()
	t.Cleanup(func() { viper.Reset(); require.NoError(t, viper.MergeConfigMap(previous)) })
	viper.Set("provider", "anthropic")
	viper.Set("model", "claude-sonnet-4-6")
	viper.Set("anthropic", map[string]any{"platform": "copilot", "base_url": "https://copilot.invalid"})
	viper.Set("anthropic_api_access", "api-key")
	viper.Set("anthropic_account", "daemon-account")
	config, err := ResolveExtensionProfile(extensions.Profile{
		Name:        "review",
		ExtensionID: "review-extension",
		Options: llmtypes.ProfileConfig{
			"provider":             "anthropic",
			"model":                "claude-sonnet-4-6",
			"anthropic_api_access": "subscription",
			"anthropic_account":    "extension-account",
			"anthropic":            map[string]any{"adaptive_thinking": true},
		},
	}, "")
	require.NoError(t, err)
	assert.Empty(t, config.Anthropic.Platform)
	assert.Empty(t, config.Anthropic.BaseURL)
	assert.True(t, config.Anthropic.AdaptiveThinking)
	assert.Equal(t, llmtypes.AnthropicAPIAccessSubscription, config.AnthropicAPIAccess)
	assert.Equal(t, "extension-account", config.AnthropicAccount)
}

func TestApplyExecutionOptionsModelPolicy(t *testing.T) {
	base := llmtypes.Config{
		Provider: "openai", Model: "private-main", WeakModel: "private-weak",
		MaxTokens: 4096, WeakModelMaxTokens: 2048, ReasoningEffort: "medium",
		Aliases: map[string]string{"short": "private-main"},
		OpenAI:  &llmtypes.OpenAIConfig{Platform: "custom"},
	}
	for _, tt := range []struct {
		name    string
		options *llmtypes.ExecutionOptions
		want    string
		err     string
	}{
		{"inherit", nil, "private-main", ""},
		{"same provider", &llmtypes.ExecutionOptions{Provider: new("openai")}, "private-main", ""},
		{"different provider", &llmtypes.ExecutionOptions{Provider: new("anthropic")}, "", "must match the selected model profile"},
		{"unknown provider", &llmtypes.ExecutionOptions{Provider: new("unknown")}, "", "unsupported execution provider"},
		{"alias", &llmtypes.ExecutionOptions{Model: new(" short ")}, "private-main", ""},
		{"swap configured models", &llmtypes.ExecutionOptions{Model: new("private-weak"), WeakModel: new("private-main")}, "private-weak", ""},
		{"custom model", &llmtypes.ExecutionOptions{Model: new("unconfigured")}, "unconfigured", ""},
		{"custom weak model", &llmtypes.ExecutionOptions{WeakModel: new("unconfigured")}, "private-main", ""},
		{"zero output tokens", &llmtypes.ExecutionOptions{MaxTokens: new(0)}, "", "must be positive"},
		{"negative turns", &llmtypes.ExecutionOptions{MaxTurns: new(-1)}, "", "must not be negative"},
		{"OpenAI thinking budget", &llmtypes.ExecutionOptions{ThinkingBudgetTokens: new(0)}, "", "requires the anthropic provider"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			config, err := applyExecutionOptions(base, tt.options, nil)
			if tt.err != "" {
				require.ErrorContains(t, err, tt.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, config.Model)
			if tt.name == "swap configured models" {
				assert.Equal(t, "private-main", config.WeakModel)
			}
			if tt.name == "custom weak model" {
				assert.Equal(t, "unconfigured", config.WeakModel)
			}
			assert.Equal(t, "private-main", base.Model)
		})
	}

	base.Provider = "anthropic"
	base.AllowedReasoningEfforts = []string{"medium", "high"}
	_, err := applyExecutionOptions(base, &llmtypes.ExecutionOptions{ReasoningEffort: new("low")}, nil)
	require.ErrorContains(t, err, "allowed_reasoning_efforts")
	config, err := applyExecutionOptions(base, &llmtypes.ExecutionOptions{ReasoningEffort: new(" HIGH "), ThinkingBudgetTokens: new(0)}, nil)
	require.NoError(t, err)
	assert.Equal(t, "high", config.ReasoningEffort)
	assert.Zero(t, config.ThinkingBudgetTokens)
	base.AllowedReasoningEfforts = nil
	_, err = applyExecutionOptions(base, &llmtypes.ExecutionOptions{ReasoningEffort: new("minimal")}, nil)
	require.ErrorContains(t, err, "not supported by provider anthropic")
	_, err = applyExecutionOptions(base, &llmtypes.ExecutionOptions{ThinkingBudgetTokens: new(4096)}, nil)
	require.ErrorContains(t, err, "must be less than maxTokens")
	base.WeakModel = ""
	_, err = applyExecutionOptions(base, &llmtypes.ExecutionOptions{UseWeakModel: new(true)}, nil)
	require.ErrorContains(t, err, "requires a configured weak model")
	base.Provider = "unsupported"
	_, err = applyExecutionOptions(base, &llmtypes.ExecutionOptions{NoTools: new(true)}, nil)
	require.ErrorContains(t, err, "unsupported conversation config snapshot provider")
}

func TestApplyExecutionOptionsFreezesConfigAndRequest(t *testing.T) {
	base := llmtypes.Config{
		Provider: "anthropic", Model: "main", WeakModel: "weak", MaxTokens: 4096,
		ReasoningEffort: "medium", AllowedTools: []string{"file_read", "bash"},
		Profiles: map[string]llmtypes.ProfileConfig{"work": {"allowed_tools": []string{"file_read"}}},
	}
	options := &llmtypes.ExecutionOptions{NoTools: new(false), AllowedTools: new([]string{}), MaxTurns: new(0)}
	first, err := applyExecutionOptions(base, options, nil)
	require.NoError(t, err)
	second, err := applyExecutionOptions(base, nil, nil)
	require.NoError(t, err)
	*options.NoTools = true
	*options.AllowedTools = []string{"bash"}
	*options.MaxTurns = 99
	base.Profiles["work"]["allowed_tools"].([]string)[0] = "changed"
	base.AllowedTools[0] = "changed"
	assert.Equal(t, new(false), first.ExecutionOptions.NoTools)
	assert.Equal(t, []string{}, *first.ExecutionOptions.AllowedTools)
	assert.Equal(t, new(0), first.ExecutionOptions.MaxTurns)
	assert.Equal(t, []string{"file_read"}, first.Profiles["work"]["allowed_tools"])
	assert.Equal(t, []string{"file_read", "bash"}, first.AllowedTools)
	assert.Nil(t, second.ExecutionOptions, "request-only restrictions must not leak to another submission")
	assert.True(t, second.EnvironmentOptions().ToolAllowed("bash"))
	assert.False(t, first.EnvironmentOptions().ToolAllowed("bash"))
}

func TestApplyExecutionOptionsSnapshotLock(t *testing.T) {
	base := llmtypes.Config{
		Provider: "anthropic", Model: "private-main", WeakModel: "private-weak",
		MaxTokens: 4096, WeakModelMaxTokens: 2048, ThinkingBudgetTokens: 1024,
		ReasoningEffort: "medium",
	}
	metadata, err := conversationservice.AddConfigSnapshot(nil, base)
	require.NoError(t, err)
	record := &conversationservice.GetConversationResponse{Metadata: metadata}
	for _, options := range []*llmtypes.ExecutionOptions{
		{Model: new("private-weak")},
		{WeakModel: new("private-main")},
		{MaxTokens: new(8192)},
		{WeakModelMaxTokens: new(4096)},
		{ThinkingBudgetTokens: new(0)},
		{ReasoningEffort: new("high")},
	} {
		_, err := applyExecutionOptions(base, options, record)
		require.ErrorContains(t, err, "model settings cannot be changed")
	}
	unchanged := &llmtypes.ExecutionOptions{
		Provider: new("anthropic"), Model: new("private-main"), WeakModel: new("private-weak"),
		MaxTokens: new(4096), WeakModelMaxTokens: new(2048), ThinkingBudgetTokens: new(1024),
		ReasoningEffort: new(" MEDIUM "), MaxTurns: new(0), UseWeakModel: new(true), AllowedTools: new([]string{}),
	}
	config, err := applyExecutionOptions(base, unchanged, record)
	require.NoError(t, err, "restating frozen settings and changing request-only controls is allowed")
	snapshot, err := llmtypes.NewConversationConfigSnapshot(config)
	require.NoError(t, err)
	data, err := json.Marshal(snapshot)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "allowedTools")
	assert.NotContains(t, string(data), "maxTurns")
	assert.NotContains(t, string(data), "useWeakModel")

	legacy := &conversationservice.GetConversationResponse{}
	_, err = applyExecutionOptions(base, &llmtypes.ExecutionOptions{Model: new("private-main")}, legacy)
	require.ErrorContains(t, err, "older conversation has no saved model settings")
	_, err = applyExecutionOptions(base, &llmtypes.ExecutionOptions{NoTools: new(true), MaxTurns: new(0)}, legacy)
	require.NoError(t, err)
	changed := base.Clone()
	changed.MaxTokens = 8192
	_, err = applyExecutionOptions(changed, &llmtypes.ExecutionOptions{MaxTokens: new(8192)}, record)
	require.ErrorContains(t, err, "model settings cannot be changed", "compare against persisted metadata, not just the incoming config")
}

func TestExecutionOptionsRejectedBeforeEnvironmentEffects(t *testing.T) {
	originalSettings := viper.AllSettings()
	t.Cleanup(func() {
		viper.Reset()
		for key, value := range originalSettings {
			viper.Set(key, value)
		}
	})
	viper.Reset()
	viper.Set("provider", "anthropic")
	viper.Set("model", "private-main")
	viper.Set("weak_model", "private-weak")
	viper.Set("max_tokens", 4096)
	viper.Set("thinking_budget_tokens", 1024)
	viper.Set("profiles", map[string]any{"work": map[string]any{}, "other": map[string]any{}})
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	require.NoError(t, db.RunMigrations(t.Context(), migrations.All()))
	metadata, err := conversationservice.AddConfigSnapshot(nil, llmtypes.Config{
		Profile: "work", Provider: "anthropic", Model: "private-main", WeakModel: "private-weak",
		MaxTokens: 4096, ThinkingBudgetTokens: 1024, ReasoningEffort: "medium",
	})
	require.NoError(t, err)
	record := convtypes.NewConversationRecord("frozen-options")
	record.Provider = "anthropic"
	record.Metadata = metadata
	store, err := conversationservice.GetConversationStore(t.Context())
	require.NoError(t, err)
	require.NoError(t, store.Save(t.Context(), record))
	require.NoError(t, store.Close())

	for _, tt := range []struct {
		name    string
		request ChatRequest
		err     string
	}{
		{"invalid tokens", ChatRequest{Options: &llmtypes.ExecutionOptions{MaxTokens: new(0)}}, "must be positive"},
		{"provider policy", ChatRequest{Options: &llmtypes.ExecutionOptions{Provider: new("openai")}}, "must match the selected model profile"},
		{"thinking budget", ChatRequest{Options: &llmtypes.ExecutionOptions{ThinkingBudgetTokens: new(4096)}}, "must be less than maxTokens"},
		{"provider reasoning", ChatRequest{Options: &llmtypes.ExecutionOptions{ReasoningEffort: new("minimal")}}, "not supported by provider"},
		{"conflicting reasoning", ChatRequest{ReasoningEffort: "low", Options: &llmtypes.ExecutionOptions{ReasoningEffort: new("high")}}, "conflicts"},
		{"frozen model", ChatRequest{ConversationID: record.ID, Options: &llmtypes.ExecutionOptions{Model: new("private-weak")}}, "model settings cannot be changed"},
		{"frozen profile", ChatRequest{ConversationID: record.ID, Profile: "other"}, "model profile cannot be changed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resolver := &recordingEnvironmentResolver{}
			runtimes := &fakeExtensionRuntimeProvider{}
			runner := NewExecutor("", runtimes)
			runner.SetEnvironmentResolver(resolver)
			t.Cleanup(func() { require.NoError(t, runner.Close()) })
			tt.request.Message = "/command-that-must-not-run"
			tt.request.RunnerID = "runner-one"
			_, err := runner.Run(t.Context(), tt.request, &recordingChatSink{})
			require.ErrorContains(t, err, tt.err)
			assert.Empty(t, resolver.conversationID, "no runner/environment opening or discovery")
			assert.Zero(t, runtimes.calls, "no extension initialization")
			assert.Empty(t, runner.sessions, "no provider thread created or retained")
		})
	}
}

func TestApplyExecutionOptionsSnapshotLockUsesEffectiveDefaults(t *testing.T) {
	base := llmtypes.Config{Provider: "openai", Model: "private-main", ReasoningEffort: "medium"}
	metadata, err := conversationservice.AddConfigSnapshot(nil, base)
	require.NoError(t, err)
	saved := metadata[conversationservice.ConfigSnapshotMetadataKey].(map[string]any)
	delete(saved, "compact_ratio")
	delete(saved, "openai")
	saved["reasoning_effort"] = " MEDIUM "
	_, err = applyExecutionOptions(base, &llmtypes.ExecutionOptions{Model: new("private-main")}, &conversationservice.GetConversationResponse{Metadata: metadata})
	require.NoError(t, err, "valid snapshots with omitted defaults and normalized values remain resumable")
}

func TestExecutionMessageOpt(t *testing.T) {
	assert.Equal(t, llmtypes.MessageOpt{PromptCache: true}, executionMessageOpt(nil))
	assert.Equal(t, llmtypes.MessageOpt{PromptCache: true}, executionMessageOpt(&llmtypes.ExecutionOptions{NoTools: new(false), UseWeakModel: new(false), MaxTurns: new(0)}))
	assert.Equal(t, llmtypes.MessageOpt{PromptCache: true, NoToolUse: true, UseWeakModel: true, MaxTurns: 3}, executionMessageOpt(&llmtypes.ExecutionOptions{AllowedTools: new([]string{}), UseWeakModel: new(true), MaxTurns: new(3)}))
}
