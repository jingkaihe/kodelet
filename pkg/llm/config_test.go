package llm

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func configureTestProfile(t *testing.T, provider, model string) {
	t.Helper()
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("profile", "test")
	viper.Set("profiles.test.provider", provider)
	viper.Set("profiles.test.model", model)
}

func TestModelProfileSelection(t *testing.T) {
	for _, test := range []struct {
		name, selected, requested, want, wantErr string
		defineDefault                            bool
	}{
		{name: "configured", selected: "test", want: "test"},
		{name: "single profile still needs selector", wantErr: "no model profile selected"},
		{name: "explicit without selector", requested: " test ", want: "test"},
		{name: "explicit ignores default", selected: "default", requested: "test", want: "test", defineDefault: true},
		{name: "unknown selector", selected: "missing", wantErr: "profile 'missing' not found"},
		{name: "unknown request", selected: "test", requested: "missing", wantErr: "profile 'missing' not found"},
		{name: "no synthetic default", selected: "test", requested: "default", wantErr: "profile 'default' not found"},
		{name: "ordinary default name", selected: "test", requested: "default", want: "default", defineDefault: true},
		{name: "default name selected", selected: " default ", want: "default", defineDefault: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			configureTestProfile(t, "openai", "test-model")
			if test.defineDefault {
				viper.Set("profiles.default", map[string]any{"provider": "anthropic", "model": "default-model"})
			}
			viper.Set("profile", test.selected)
			before := viper.AllSettings()
			config, err := GetConfigFromViperWithProfile(test.requested)
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, config.Profile)
			assert.Equal(t, test.want+"-model", config.Model)
			assert.Equal(t, before, viper.AllSettings(), "selection must not mutate process defaults")
		})
	}
}

func TestModelProfileDefaults(t *testing.T) {
	for _, test := range []struct {
		name, provider string
		zeroes         bool
	}{
		{name: "OpenAI", provider: "openai"},
		{name: "Anthropic", provider: "anthropic"},
		{name: "normalized provider", provider: " OPENAI "},
		{name: "explicit zero and false", provider: "openai", zeroes: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			configureTestProfile(t, test.provider, "test-model")
			for _, key := range modelSettingKeys {
				viper.Set(key, "must-not-be-inherited")
			}
			maxTokens, thinking, effort, search := 8192, 4048, "medium", true
			if test.zeroes {
				for _, key := range []string{"max_tokens", "weak_model_max_tokens", "thinking_budget_tokens"} {
					viper.Set("profiles.test."+key, 0)
				}
				viper.Set("profiles.test.reasoning_effort", "none")
				viper.Set("profiles.test.openai.enable_search", false)
				viper.Set("profiles.test.openai.websocket_mode", false)
				maxTokens, thinking, effort, search = 0, 0, "none", false
			}
			config, err := GetConfigFromViper()
			require.NoError(t, err)
			assert.Equal(t, strings.ToLower(strings.TrimSpace(test.provider)), config.Provider)
			assert.Empty(t, config.WeakModel)
			assert.Equal(t, maxTokens, config.MaxTokens)
			assert.Equal(t, maxTokens, config.WeakModelMaxTokens)
			assert.Equal(t, thinking, config.ThinkingBudgetTokens)
			assert.Equal(t, effort, config.ReasoningEffort)
			assert.Equal(t, llmtypes.DefaultCompactRatio, config.CompactRatio)
			assert.Equal(t, llmtypes.DefaultRetryConfig, config.Retry)
			assert.Equal(t, llmtypes.AnthropicAPIAccessAuto, config.AnthropicAPIAccess)
			assert.Equal(t, &llmtypes.BashConfig{Timeout: llmtypes.DefaultBashTimeout}, config.Bash)
			if config.Provider == "openai" {
				assert.Equal(t, &llmtypes.OpenAIConfig{
					APIMode: llmtypes.OpenAIAPIModeResponses, EnableSearch: new(search), WebSocketMode: new(search),
				}, config.OpenAI)
			} else {
				assert.Nil(t, config.OpenAI)
			}
			assert.Nil(t, config.Anthropic)
			isolated, err := GetConfigFromProfile(viper.GetStringMap("profiles.test"))
			require.NoError(t, err)
			config.Profile, config.Profiles = "", nil
			assert.Equal(t, isolated, config, "extension profiles use the same field defaults")
		})
	}
}

func TestModelProfilePreservesSharedSettings(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.SetConfigType("yaml")
	require.NoError(t, viper.ReadConfig(strings.NewReader(`
profile: work
extensions:
  enabled: true
  allow: [shared]
  settings: {retained: shared-value, overridden: shared-value}
skills:
  enabled: true
  allowed: [pdf]
context:
  patterns: [AGENTS.md]
tracing:
  enabled: true
  ratio: 0.5
tool_mode: patch
allowed_tools: [bash, file_read]
compact_ratio: 0.65
bash:
  timeout: 45s
anthropic_api_access: subscription
anthropic_account: work
aliases:
  driver.v1: shared-model
  small: weak-model
openai:
  platform: custom
  base_url: https://provider.example
  api_key_env_var: SHARED_API_KEY
  models:
    reasoning: [shared-model]
    non_reasoning: [weak-model]
  pricing:
    custom-model.v1:
      input: 1
      cached_input: 0.5
      output: 2
      long_context_input: 3
      long_context_cached_input: 1.5
      long_context_output: 4
      long_context_threshold: 10000
      context_window: 20000
    partial:
      input: 1
anthropic:
  platform: copilot
  base_url: https://anthropic.example
profiles:
  work:
    provider: openai
    model: driver.v1
    weak_model: small
    aliases:
      driver.v1: profile-model
    extensions:
      allow: [profile]
      settings: {overridden: profile-value}
    skills:
      allowed: []
    openai:
      api_mode: chat_completions
      text_verbosity: ' HIGH '
      manual_cache: true
      pricing:
        custom-model.v1:
          input: 5
`)))
	before := viper.AllSettings()
	require.NoError(t, ValidateModelProfiles())
	config, err := GetConfigFromViper()
	require.NoError(t, err)
	assert.Equal(t, "profile-model", config.Model)
	assert.Equal(t, "weak-model", config.WeakModel)
	assert.Equal(t, map[string]any{
		"enabled": true, "allow": []any{"profile"},
		"settings": map[string]any{"retained": "shared-value", "overridden": "profile-value"},
	}, config.ExtensionSettings)
	require.NotNil(t, config.Skills)
	assert.True(t, config.Skills.Enabled)
	assert.Empty(t, config.Skills.Allowed, "explicit empty lists replace shared lists")
	assert.Equal(t, &llmtypes.ContextConfig{Patterns: []string{"AGENTS.md"}}, config.Context)
	assert.Equal(t, llmtypes.ToolModePatch, config.ToolMode)
	assert.Equal(t, []string{"bash", "file_read"}, config.AllowedTools)
	assert.Equal(t, 0.65, config.CompactRatio)
	assert.Equal(t, &llmtypes.BashConfig{Timeout: 45 * time.Second}, config.Bash)
	assert.Equal(t, llmtypes.AnthropicAPIAccessSubscription, config.AnthropicAPIAccess)
	assert.Equal(t, "work", config.AnthropicAccount)
	assert.Equal(t, &llmtypes.OpenAIConfig{
		Platform: "custom", BaseURL: "https://provider.example", APIKeyEnvVar: "SHARED_API_KEY",
		APIMode: llmtypes.OpenAIAPIModeChatCompletions, TextVerbosity: llmtypes.OpenAITextVerbosityHigh,
		ManualCache: true, EnableSearch: new(true), WebSocketMode: new(true),
		Models: &llmtypes.CustomModels{Reasoning: []string{"shared-model"}, NonReasoning: []string{"weak-model"}},
		Pricing: map[string]llmtypes.ModelPricing{
			"custom-model.v1": {
				Input: 5, CachedInput: 0.5, Output: 2, LongContextInput: 3, LongContextCachedInput: 1.5,
				LongContextOutput: 4, LongContextThreshold: 10000, ContextWindow: 20000,
			},
			"partial": {Input: 1},
		},
	}, config.OpenAI)
	assert.Equal(t, &llmtypes.AnthropicConfig{Platform: "copilot", BaseURL: "https://anthropic.example"}, config.Anthropic)
	config.Aliases["driver.v1"] = "changed"
	config.OpenAI.Models.Reasoning[0] = "changed"
	config.OpenAI.Pricing["partial"] = llmtypes.ModelPricing{Input: 99}
	assert.Equal(t, before, viper.AllSettings(), "resolution preserves shared process settings, including tracing")
}

func TestGetConfigFromProfileIsolatedConfiguration(t *testing.T) {
	configureTestProfile(t, "openai", "daemon-model")
	viper.Set("aliases", map[string]any{"private.v1": "daemon-alias"})
	viper.Set("anthropic.base_url", "https://daemon.invalid")
	t.Setenv("KODELET_THINKING_BUDGET_TOKENS", "200")
	var profile llmtypes.ProfileConfig
	require.NoError(t, json.Unmarshal([]byte(`{
  "provider": "anthropic", "model": " private.v1 ", "weak_model": "small",
  "aliases": {"private.v1": "private-model", "small": "weak-model"},
  "anthropic_api_access": "subscription", "anthropic_account": "work",
  "thinking_budget_tokens": 1024, "reasoning_effort": " LOW ",
  "allowed_reasoning_efforts": ["low", "high"],
  "anthropic": {"platform": "anthropic", "adaptive_thinking": true}
}`), &profile))
	before := cloneSettings(profile)
	config, err := GetConfigFromProfile(profile)
	require.NoError(t, err)
	assert.Empty(t, config.Profile)
	assert.Equal(t, "private-model", config.Model)
	assert.Equal(t, "weak-model", config.WeakModel)
	assert.Equal(t, "low", config.ReasoningEffort)
	assert.Equal(t, []string{"low", "high"}, config.AllowedReasoningEfforts)
	assert.Equal(t, 1024, config.ThinkingBudgetTokens)
	assert.Equal(t, llmtypes.AnthropicAPIAccessSubscription, config.AnthropicAPIAccess)
	assert.Equal(t, "work", config.AnthropicAccount)
	assert.Equal(t, &llmtypes.AnthropicConfig{Platform: "anthropic", AdaptiveThinking: true}, config.Anthropic)
	config.Aliases["private.v1"] = "changed"
	assert.Equal(t, before, map[string]any(profile))
}

func TestModelProfileValidation(t *testing.T) {
	for _, test := range []struct {
		key, wantErr string
		value        any
	}{
		{key: "provider", wantErr: "provider is required"},
		{key: "provider", value: 12, wantErr: "provider is required"},
		{key: "provider", value: "unsupported", wantErr: "unsupported provider"},
		{key: "model", wantErr: "model is required"},
		{key: "model", value: " ", wantErr: "model is required"},
		{key: "model", value: 12, wantErr: "model is required"},
		{key: "max_tokens", value: "invalid", wantErr: "failed to unmarshal"},
		{key: "reasoning_effort", value: "invalid", wantErr: "invalid reasoning_effort"},
		{key: "openai.text_verbosity", value: "invalid", wantErr: "invalid openai.text_verbosity"},
		{key: "openai.pricing", value: map[string]any{"invalid-entry": "not-a-map"}, wantErr: "failed to unmarshal"},
		{key: "bash.timeout", value: "5s", wantErr: "bash.timeout must be at least"},
		{key: "compact_ratio", value: 0, wantErr: "compact_ratio must be greater than"},
		{key: "compact_ratio", value: 1.1, wantErr: "compact_ratio must be greater than"},
	} {
		t.Run(test.key, func(t *testing.T) {
			configureTestProfile(t, "openai", "test-model")
			profile := map[string]any{"provider": "openai", "model": "test-model"}
			setSetting(profile, test.key, test.value)
			viper.Set("profiles", map[string]any{"test": profile})
			_, err := GetConfigFromViper()
			require.ErrorContains(t, err, test.wantErr)
			_, err = GetConfigFromProfile(profile)
			require.ErrorContains(t, err, test.wantErr, "extension profiles share validation")
		})
	}
}

func TestValidateModelProfiles(t *testing.T) {
	for _, test := range []struct {
		name, key, wantErr string
		value              any
	}{
		{name: "missing selector", key: "profile", value: "", wantErr: "no model profile selected"},
		{name: "unknown selector", key: "profile", value: "missing", wantErr: "profile 'missing' not found"},
		{name: "malformed profile", key: "profiles.test", value: "not-a-map", wantErr: "must be a mapping"},
		{name: "unselected profile", key: "profiles.hidden", value: map[string]any{"hidden": true, "provider": "openai"}, wantErr: `invalid model profile "hidden"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			configureTestProfile(t, "openai", "test-model")
			viper.Set(test.key, test.value)
			require.ErrorContains(t, ValidateModelProfiles(), test.wantErr)
			_, err := GetConfigFromViperWithoutProfile()
			require.NoError(t, err, "clients and runners do not require model configuration")
		})
	}
	for _, key := range modelSettingKeys {
		for _, source := range []string{"file", "environment"} {
			t.Run(key+"/"+source, func(t *testing.T) {
				configureTestProfile(t, "openai", "test-model")
				if source == "file" {
					settings := map[string]any{}
					setSetting(settings, key, "removed-value")
					require.NoError(t, viper.MergeConfigMap(settings))
				} else {
					t.Setenv("KODELET_"+strings.ToUpper(strings.ReplaceAll(key, ".", "_")), "removed-value")
				}
				require.ErrorContains(t, ValidateModelProfiles(), "is not supported")
			})
		}
	}
}

func TestModelProfileExplicitFlags(t *testing.T) {
	configureTestProfile(t, "anthropic", "test-model")
	viper.Set("profiles.test.context.patterns", []string{"PROFILE.md"})
	viper.Set("profiles.test.sysprompt_args", map[string]any{"project": "profile"})
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().String("provider", "openai", "Provider override")
	cmd.Flags().String("model", "flag-default", "Model override")
	cmd.Flags().String("weak-model", "flag-default", "Weak model override")
	cmd.Flags().Int("max-tokens", 123, "Token override")
	cmd.Flags().Bool("enable-openai-search", true, "Search override")
	cmd.Flags().Float64("compact-ratio", llmtypes.DefaultCompactRatio, "Compact ratio")
	cmd.Flags().StringSlice("context-patterns", nil, "Context patterns")
	cmd.Flags().String("sysprompt", "", "System prompt")
	cmd.Flags().StringToString("sysprompt-arg", nil, "System prompt arguments")
	require.NoError(t, viper.BindPFlags(cmd.Flags()))
	require.NoError(t, viper.BindPFlag("weak_model", cmd.Flags().Lookup("weak-model")))
	require.NoError(t, viper.BindPFlag("max_tokens", cmd.Flags().Lookup("max-tokens")))
	require.NoError(t, viper.BindPFlag("openai.enable_search", cmd.Flags().Lookup("enable-openai-search")))
	require.NoError(t, ValidateModelProfiles(), "bound flag defaults are not top-level configuration")
	config, err := GetConfigFromViperWithCmd(cmd)
	require.NoError(t, err)
	assert.Equal(t, "anthropic", config.Provider)
	assert.Equal(t, "test-model", config.Model)
	assert.Empty(t, config.WeakModel)
	assert.Equal(t, 8192, config.MaxTokens)

	require.NoError(t, cmd.ParseFlags([]string{
		"--provider=openai", "--model=gpt-6", "--weak-model=gpt-5.6", "--max-tokens=2048",
		"--enable-openai-search=false", "--compact-ratio=0.6", "--context-patterns=CODING.md,README.md",
		"--sysprompt=custom.tmpl", "--sysprompt-arg=project=flag", "--sysprompt-arg=env=dev",
	}))
	config, err = GetConfigFromViperWithCmd(cmd)
	require.NoError(t, err)
	assert.Equal(t, "test", config.Profile)
	assert.Equal(t, "openai", config.Provider)
	assert.Equal(t, "gpt-6-astra", config.Model)
	assert.Equal(t, "gpt-5.6-sol", config.WeakModel)
	assert.Equal(t, 2048, config.MaxTokens)
	assert.Equal(t, new(false), config.OpenAI.EnableSearch)
	assert.Equal(t, 0.6, config.CompactRatio)
	assert.Equal(t, &llmtypes.ContextConfig{Patterns: []string{"CODING.md", "README.md"}}, config.Context)
	assert.Equal(t, "custom.tmpl", config.Sysprompt)
	assert.Equal(t, map[string]string{"project": "flag", "env": "dev"}, config.SyspromptArgs)
	viper.Set("profiles.test.provider", "")
	_, err = GetConfigFromViperWithCmd(cmd)
	require.ErrorContains(t, err, "provider is required", "flags cannot supply a missing profile identity")
}

func TestSharedConfigLoaders(t *testing.T) {
	configureTestProfile(t, "openai", "must-not-be-inherited")
	for _, key := range modelSettingKeys {
		viper.Set(key, "invalid-model-value")
	}
	viper.Set("profiles.malformed", "not-a-map")
	viper.Set("extensions.enabled", true)
	viper.Set("tool_mode", "full")
	viper.Set("openai.platform", "custom")
	viper.Set("openai.base_url", "https://provider.example")
	viper.Set("environment_profiles.runner", map[string]any{"tool_mode": "patch", "model": "not-a-runner-setting"})
	settings := viper.AllSettings()
	before := cloneSettings(settings)
	for _, test := range []struct {
		name string
		load func() (llmtypes.Config, error)
		mode llmtypes.ToolMode
	}{
		{name: "shared", load: GetConfigFromViperWithoutProfile, mode: llmtypes.ToolModeFull},
		{
			name: "runner", mode: llmtypes.ToolModePatch,
			load: func() (llmtypes.Config, error) {
				return GetConfigFromViperWithEnvironmentProfile("runner")
			},
		},
		{
			name: "snapshot", mode: llmtypes.ToolModePatch,
			load: func() (llmtypes.Config, error) {
				return GetConfigFromSettingsWithEnvironmentProfile(settings, "runner")
			},
		},
		{
			name: "runner default", mode: llmtypes.ToolModeFull,
			load: func() (llmtypes.Config, error) {
				return GetConfigFromViperWithEnvironmentProfile("default")
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			config, err := test.load()
			require.NoError(t, err)
			assert.Empty(t, config.Profile)
			assert.Nil(t, config.Profiles)
			assert.Empty(t, config.Provider)
			assert.Empty(t, config.Model)
			assert.Empty(t, config.WeakModel)
			assert.Zero(t, config.MaxTokens)
			assert.Zero(t, config.WeakModelMaxTokens)
			assert.Zero(t, config.ThinkingBudgetTokens)
			assert.Empty(t, config.ReasoningEffort)
			assert.Empty(t, config.AllowedReasoningEfforts)
			assert.Nil(t, config.Anthropic)
			assert.Equal(t, &llmtypes.OpenAIConfig{Platform: "custom", BaseURL: "https://provider.example"}, config.OpenAI)
			assert.Equal(t, map[string]any{"enabled": true}, config.ExtensionSettings)
			assert.Equal(t, test.mode, config.ToolMode)
			assert.Equal(t, before, settings)
			assert.Equal(t, before, viper.AllSettings())
		})
	}
	_, err := GetConfigFromViperWithEnvironmentProfile("missing")
	require.ErrorContains(t, err, "profile 'missing' not found")
}

func TestGetConfigFromViperWithAliases(t *testing.T) {
	for _, test := range []struct {
		name, alias, model string
		shared, profile    map[string]any
	}{
		{name: "built-in", alias: "gpt-5.6", model: "gpt-5.6-sol"},
		{name: "shared dotted alias", alias: "driver.v1", model: "shared-model", shared: map[string]any{"driver.v1": "shared-model"}},
		{name: "profile override", alias: "driver.v1", model: "profile-model", shared: map[string]any{"driver.v1": "shared-model"}, profile: map[string]any{"driver.v1": "profile-model"}},
	} {
		for _, source := range []string{"file", "override"} {
			t.Run(test.name+"/"+source, func(t *testing.T) {
				viper.Reset()
				t.Cleanup(viper.Reset)
				settings := map[string]any{
					"profile": "test", "aliases": test.shared,
					"profiles": map[string]any{"test": map[string]any{
						"provider": "openai", "model": test.alias, "weak_model": test.alias, "aliases": test.profile,
					}},
				}
				if source == "file" {
					require.NoError(t, viper.MergeConfigMap(settings))
				} else {
					for key, value := range settings {
						viper.Set(key, value)
					}
				}
				config, err := GetConfigFromViper()
				require.NoError(t, err)
				assert.Equal(t, test.model, config.Model)
				assert.Equal(t, test.model, config.WeakModel)
				assert.Equal(t, "gpt-6-astra", config.Aliases["gpt-6"])
				assert.True(t, config.ModelAliasesResolved)
			})
		}
	}
}

func TestConfigAliasIntegrationWithNewThread(t *testing.T) {
	configureTestProfile(t, "anthropic", "sonnet-46")
	viper.Set("aliases", map[string]any{"sonnet-46": "claude-sonnet-4-6"})
	config, err := GetConfigFromViper()
	require.NoError(t, err)
	thread, err := NewThread(config)
	require.NoError(t, err)
	require.NotNil(t, thread)
	assert.Equal(t, "claude-sonnet-4-6", config.Model)
}

func TestSharedBashTimeoutFromEnv(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	t.Setenv("KODELET_BASH_TIMEOUT", "35s")
	require.NoError(t, viper.BindEnv("bash.timeout", "KODELET_BASH_TIMEOUT"))
	config, err := GetConfigFromViperWithoutProfile()
	require.NoError(t, err)
	assert.Equal(t, &llmtypes.BashConfig{Timeout: 35 * time.Second}, config.Bash)
}
