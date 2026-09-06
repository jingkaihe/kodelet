package llm

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestBashConfigMarshalJSONUsesDurationString(t *testing.T) {
	data, err := json.Marshal(BashConfig{Timeout: 5 * time.Minute})
	require.NoError(t, err)

	assert.JSONEq(t, `{"timeout":"5m0s"}`, string(data))
}

func TestBashConfigMarshalYAMLUsesDurationString(t *testing.T) {
	data, err := yaml.Marshal(BashConfig{Timeout: 5 * time.Minute})
	require.NoError(t, err)

	assert.Equal(t, "timeout: 5m0s\n", string(data))
}

func TestToolModeIsPatchMode(t *testing.T) {
	assert.True(t, ToolModePatch.IsPatchMode())
	assert.False(t, ToolModeFull.IsPatchMode())
	assert.False(t, ToolMode("").IsPatchMode())
}

func TestConfigBashTimeout(t *testing.T) {
	assert.Equal(t, DefaultBashTimeout, Config{}.BashTimeout())
	assert.Equal(t, DefaultBashTimeout, Config{Bash: &BashConfig{}}.BashTimeout())
	assert.Equal(t, 30*time.Second, Config{Bash: &BashConfig{Timeout: 30 * time.Second}}.BashTimeout())
}

func TestSystemInformationCloneAndTransientConfigSerialization(t *testing.T) {
	information := &SystemInformation{
		IsGitRepo: true,
		Platform:  "darwin",
		OSVersion: "macOS 26.0",
		Date:      "2026-08-09",
	}
	clone := information.Clone()
	require.NotNil(t, clone)
	assert.Equal(t, information, clone)
	clone.Platform = "linux"
	assert.Equal(t, "darwin", information.Platform)
	assert.Nil(t, (*SystemInformation)(nil).Clone())

	payload, err := json.Marshal(Config{SystemInformation: information})
	require.NoError(t, err)
	assert.NotContains(t, string(payload), "systemInformation")
	assert.NotContains(t, string(payload), "macOS 26.0")
}

func TestConfigClonePinsMutableSettings(t *testing.T) {
	original := Config{
		Provider:                "openai",
		Model:                   "main",
		AllowedTools:            []string{"file_read"},
		AllowedCommands:         []string{"git status"},
		AllowedReasoningEfforts: []string{"medium"},
		Aliases:                 map[string]string{"short": "main"},
		SyspromptArgs:           map[string]string{"name": "original"},
		SystemInformation:       &SystemInformation{Platform: "linux"},
		ExecutionOptions:        &ExecutionOptions{Model: new("main"), AllowedTools: new([]string{})},
		Extensions:              new("opaque runtime"),
		ExtensionSettings: map[string]any{
			"entries": []any{map[string]any{"args": []string{"original"}}, nil},
			"typed":   []map[string][]int{{"limits": {1, 2}}},
			"flags":   []bool{true},
			"nil":     []any(nil),
			"empty":   []any{},
			"profile": ProfileConfig{"env": map[string]string{"KEY": "original"}},
		},
		Profiles: map[string]ProfileConfig{
			"work": {"openai": map[string]any{"models": []any{"main"}}},
		},
		EnvironmentProfiles: map[string]ProfileConfig{
			"workspace": {"extensions": map[string]any{"paths": []string{"original"}}},
		},
		OpenAI: &OpenAIConfig{
			EnableSearch:  new(true),
			WebSocketMode: new(true),
			Models:        &CustomModels{Reasoning: []string{"main"}, NonReasoning: []string{"weak"}},
			Pricing:       map[string]ModelPricing{"main": {Input: 1}},
		},
		Anthropic: &AnthropicConfig{Platform: "anthropic"},
		Bash:      &BashConfig{Timeout: time.Minute},
		Skills:    &SkillsConfig{Enabled: true, Allowed: []string{"review"}},
		Context:   &ContextConfig{Patterns: []string{"AGENTS.md"}},
	}
	// Include transient settings that Config intentionally excludes from JSON.
	serialize := func() []byte {
		data, err := json.Marshal([]any{original, original.ExecutionOptions, original.ExtensionSettings, original.SystemInformation})
		require.NoError(t, err)
		return data
	}
	before := serialize()
	cloned := original.Clone()
	assert.Equal(t, original, cloned)
	assert.Same(t, original.Extensions, cloned.Extensions, "opaque runtime ownership is not duplicated")
	cloned.AllowedTools[0] = "bash"
	cloned.AllowedCommands[0] = "rm *"
	cloned.AllowedReasoningEfforts[0] = "high"
	cloned.Aliases["short"] = "other"
	cloned.SyspromptArgs["name"] = "changed"
	cloned.SystemInformation.Platform = "darwin"
	*cloned.ExecutionOptions.Model = "other"
	*cloned.ExecutionOptions.AllowedTools = append(*cloned.ExecutionOptions.AllowedTools, "bash")
	cloned.ExtensionSettings["entries"].([]any)[0].(map[string]any)["args"].([]string)[0] = "changed"
	cloned.ExtensionSettings["typed"].([]map[string][]int)[0]["limits"][0] = 99
	cloned.ExtensionSettings["flags"].([]bool)[0] = false
	cloned.ExtensionSettings["profile"].(ProfileConfig)["env"].(map[string]string)["KEY"] = "changed"
	cloned.Profiles["work"]["openai"].(map[string]any)["models"].([]any)[0] = "changed"
	cloned.EnvironmentProfiles["workspace"]["extensions"].(map[string]any)["paths"].([]string)[0] = "changed"
	*cloned.OpenAI.EnableSearch = false
	*cloned.OpenAI.WebSocketMode = false
	cloned.OpenAI.Models.Reasoning[0] = "changed"
	cloned.OpenAI.Models.NonReasoning[0] = "changed"
	cloned.OpenAI.Pricing["main"] = ModelPricing{Input: 99}
	cloned.Anthropic.Platform = "changed"
	cloned.Bash.Timeout = time.Second
	cloned.Skills.Allowed[0] = "changed"
	cloned.Context.Patterns[0] = "changed"
	assert.JSONEq(t, string(before), string(serialize()), "mutating a run snapshot must not mutate shared configuration")
	assert.Equal(t, Config{}, (Config{}).Clone(), "absent configuration stays absent")
}

func TestConfigExecutionOptionsAreTransient(t *testing.T) {
	config := Config{ExecutionOptions: &ExecutionOptions{NoTools: new(true), MaxTurns: new(0)}}
	data, err := json.Marshal(config)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "noTools")
	assert.NotContains(t, string(data), "maxTurns")
	data, err = yaml.Marshal(config)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "executionoptions")
}

func TestOpenAIServiceTierParsingAndWireValue(t *testing.T) {
	tier, ok := ParseOpenAIServiceTier(" FAST ")
	require.True(t, ok)
	assert.Equal(t, OpenAIServiceTierFast, tier)
	assert.Equal(t, "priority", tier.WireValue())

	tier, ok = ParseOpenAIServiceTier("scale")
	require.True(t, ok)
	assert.Equal(t, OpenAIServiceTierScale, tier)
	assert.Equal(t, "scale", tier.WireValue())

	tier, ok = ParseOpenAIServiceTier("")
	assert.False(t, ok)
	assert.Empty(t, tier)

	tier, ok = ParseOpenAIServiceTier("unknown")
	assert.False(t, ok)
	assert.Empty(t, tier)
	assert.Empty(t, OpenAIServiceTier("unknown").WireValue())
}

func TestOpenAITextVerbosityParsing(t *testing.T) {
	verbosity, ok := ParseOpenAITextVerbosity(" HIGH ")
	require.True(t, ok)
	assert.Equal(t, OpenAITextVerbosityHigh, verbosity)

	verbosity, ok = ParseOpenAITextVerbosity("unknown")
	assert.False(t, ok)
	assert.Empty(t, verbosity)
}

func TestConfiguredOpenAITextVerbosity(t *testing.T) {
	verbosity, configured, err := ConfiguredOpenAITextVerbosity(Config{})
	require.NoError(t, err)
	assert.False(t, configured)
	assert.Empty(t, verbosity)

	verbosity, configured, err = ConfiguredOpenAITextVerbosity(Config{OpenAI: &OpenAIConfig{TextVerbosity: " HIGH "}})
	require.NoError(t, err)
	assert.True(t, configured)
	assert.Equal(t, OpenAITextVerbosityHigh, verbosity)

	_, _, err = ConfiguredOpenAITextVerbosity(Config{OpenAI: &OpenAIConfig{TextVerbosity: "unknown"}})
	require.ErrorContains(t, err, "invalid openai.text_verbosity")
}

func TestNormalizeOpenAITextVerbosityPreservesUnsetState(t *testing.T) {
	config := Config{}
	require.NoError(t, NormalizeOpenAITextVerbosity(&config))
	assert.Nil(t, config.OpenAI)

	config.OpenAI = &OpenAIConfig{}
	require.NoError(t, NormalizeOpenAITextVerbosity(&config))
	assert.Empty(t, config.OpenAI.TextVerbosity)

	config.OpenAI.TextVerbosity = " HIGH "
	require.NoError(t, NormalizeOpenAITextVerbosity(&config))
	assert.Equal(t, OpenAITextVerbosityHigh, config.OpenAI.TextVerbosity)

	config.OpenAI.TextVerbosity = "unknown"
	require.ErrorContains(t, NormalizeOpenAITextVerbosity(&config), "invalid openai.text_verbosity")
}

func TestNormalizeOpenAITextVerbosityCopiesOpenAIConfig(t *testing.T) {
	original := &OpenAIConfig{
		Platform:      "openai",
		TextVerbosity: " HIGH ",
	}
	config := Config{OpenAI: original}

	require.NoError(t, NormalizeOpenAITextVerbosity(&config))

	assert.NotSame(t, original, config.OpenAI)
	assert.Equal(t, OpenAITextVerbosity(" HIGH "), original.TextVerbosity)
	assert.Equal(t, OpenAITextVerbosityHigh, config.OpenAI.TextVerbosity)
	assert.Equal(t, "openai", config.OpenAI.Platform)
}

func TestDefaultContextPatterns(t *testing.T) {
	patterns := DefaultContextPatterns()
	assert.Equal(t, []string{"AGENTS.md"}, patterns)

	patterns[0] = "changed"
	assert.Equal(t, []string{"AGENTS.md"}, DefaultContextPatterns())
}

func TestModelPricingForPromptTokens(t *testing.T) {
	pricing := ModelPricing{
		Input:                      1,
		CachedInput:                0.1,
		CacheWriteInput:            1.25,
		Output:                     2,
		LongContextInput:           3,
		LongContextCachedInput:     0.3,
		LongContextCacheWriteInput: 3.75,
		LongContextOutput:          4,
		LongContextThreshold:       272_000,
		ContextWindow:              1_050_000,
	}

	assert.Equal(t, 1.0, pricing.ForPromptTokens(272_000).Input)
	assert.Equal(t, 0.1, pricing.ForPromptTokens(272_000).CachedInput)
	assert.Equal(t, 1.25, pricing.ForPromptTokens(272_000).CacheWriteInput)
	assert.Equal(t, 2.0, pricing.ForPromptTokens(272_000).Output)

	longContext := pricing.ForPromptTokens(272_001)
	assert.Equal(t, 3.0, longContext.Input)
	assert.Equal(t, 0.3, longContext.CachedInput)
	assert.Equal(t, 3.75, longContext.CacheWriteInput)
	assert.Equal(t, 4.0, longContext.Output)
	assert.Equal(t, 1_050_000, longContext.ContextWindow)
}

func TestModelPricingForPromptTokensAllowsPartialLongContextRates(t *testing.T) {
	pricing := ModelPricing{
		Input:                1,
		CachedInput:          0,
		CacheWriteInput:      1.25,
		Output:               2,
		LongContextInput:     3,
		LongContextOutput:    4,
		LongContextThreshold: 272_000,
	}

	longContext := pricing.ForPromptTokens(272_001)
	assert.Equal(t, 3.0, longContext.Input)
	assert.Equal(t, 0.0, longContext.CachedInput)
	assert.Equal(t, 1.25, longContext.CacheWriteInput)
	assert.Equal(t, 4.0, longContext.Output)
}
