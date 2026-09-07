package chat

import (
	"encoding/json"
	"testing"

	conversationservice "github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	"github.com/jingkaihe/kodelet/pkg/llm/anthropic"
	codexpreset "github.com/jingkaihe/kodelet/pkg/llm/openai/preset/codex"
	openaipreset "github.com/jingkaihe/kodelet/pkg/llm/openai/preset/openai"
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

func TestExecutionModelAllowed(t *testing.T) {
	var anthropicModel string
	for model := range anthropic.ModelPricingMap {
		anthropicModel = model
		break
	}
	require.NotEmpty(t, anthropicModel)
	openAIModel := openaipreset.Models.Reasoning[0]
	codexModel := codexpreset.Models.Reasoning[0]
	for _, tt := range []struct {
		name    string
		config  llmtypes.Config
		model   string
		allowed bool
	}{
		{"configured main", llmtypes.Config{Provider: "openai", Model: "private-main"}, "private-main", true},
		{"configured weak", llmtypes.Config{Provider: "anthropic", WeakModel: "private-weak"}, "private-weak", true},
		{"trusted alias", llmtypes.Config{Provider: "openai", Aliases: map[string]string{"short": "private"}}, "private", true},
		{"OpenAI catalog", llmtypes.Config{Provider: "openai"}, openAIModel, true},
		{"Codex catalog", llmtypes.Config{Provider: "openai", OpenAI: &llmtypes.OpenAIConfig{Platform: "codex"}}, codexModel, true},
		{"Anthropic catalog", llmtypes.Config{Provider: "anthropic"}, anthropicModel, true},
		{"wrong provider catalog", llmtypes.Config{Provider: "anthropic"}, openAIModel, false},
		{"custom platform has no implicit OpenAI catalog", llmtypes.Config{Provider: "openai", OpenAI: &llmtypes.OpenAIConfig{Platform: "custom"}}, openAIModel, false},
		{"custom reasoning model", llmtypes.Config{Provider: "openai", OpenAI: &llmtypes.OpenAIConfig{Platform: "custom", Models: &llmtypes.CustomModels{Reasoning: []string{"private"}}}}, "private", true},
		{"custom nonreasoning model", llmtypes.Config{Provider: "openai", OpenAI: &llmtypes.OpenAIConfig{Platform: "custom", Models: &llmtypes.CustomModels{NonReasoning: []string{"private"}}}}, "private", true},
		{"custom pricing", llmtypes.Config{Provider: "openai", OpenAI: &llmtypes.OpenAIConfig{Platform: "custom", Pricing: map[string]llmtypes.ModelPricing{"private": {}}}}, "private", true},
		{"unknown model", llmtypes.Config{Provider: "openai"}, "unconfigured-private-model", false},
		{"unknown provider", llmtypes.Config{Provider: "unsupported"}, openAIModel, false},
		{"unknown configured provider", llmtypes.Config{Provider: "unsupported", Model: "private"}, "private", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.allowed, executionModelAllowed(tt.config, tt.model))
		})
	}
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
		{"unknown model", &llmtypes.ExecutionOptions{Model: new("unconfigured")}, "", "not an available model"},
		{"unknown weak model", &llmtypes.ExecutionOptions{WeakModel: new("unconfigured")}, "", "not an available model"},
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
		{"model policy", ChatRequest{Options: &llmtypes.ExecutionOptions{Model: new("unconfigured")}}, "not an available model"},
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
