package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"

	conversationservice "github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/llm"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
)

// UnmarshalJSON validates the request and its execution-options envelope.
// Invalid requests leave the receiver untouched.
func (r *ChatRequest) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return errors.Wrap(err, "invalid chat request")
	}
	if fields == nil {
		return errors.New("chat request must be an object")
	}
	if hasNullExecutionOptions(fields) {
		return errors.New("execution options must be an object, not null; omit options to inherit")
	}
	type wireRequest ChatRequest
	var value wireRequest
	if err := decodeStrictRequestJSON(data, &value); err != nil {
		return errors.Wrap(err, "invalid chat request")
	}
	*r = ChatRequest(value)
	return nil
}

func decodeStrictRequestJSON(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
}

// hasNullExecutionOptions keeps optional execution-options envelopes strict:
// encoding/json bypasses ExecutionOptions.UnmarshalJSON for null pointers and
// matches field names case-insensitively. Omit options to inherit instead.
func hasNullExecutionOptions(fields map[string]json.RawMessage) bool {
	for name, raw := range fields {
		if strings.EqualFold(name, "options") && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return true
		}
	}
	return false
}

// resolveExecutionOptions runs before runner opening, extension initialization,
// command expansion, or provider construction. Store reads are side-effect free.
func resolveExecutionOptions(ctx context.Context, req ChatRequest, config llmtypes.Config) (llmtypes.Config, error) {
	var record *conversationservice.GetConversationResponse
	if strings.TrimSpace(req.ConversationID) != "" && (req.Options.HasModelOptions() || req.Profile != "") {
		service, err := conversationservice.GetDefaultConversationService(ctx)
		if err != nil {
			return llmtypes.Config{}, err
		}
		defer func() { _ = service.Close() }()
		record, err = service.GetConversation(ctx, req.ConversationID)
		if err != nil && !errors.Is(err, convtypes.ErrConversationNotFound) {
			return llmtypes.Config{}, err
		}
		if record != nil && req.Profile != "" && NormalizeRequestedProfile(req.Profile) != NormalizeRequestedProfile(config.Profile) {
			return llmtypes.Config{}, errors.New("the model profile cannot be changed for this conversation; start a new conversation to change it")
		}
	}
	policy := config.EnvironmentOptions()
	config, err := applyExecutionOptions(config, req.Options, record)
	if err != nil {
		return llmtypes.Config{}, err
	}
	if config.ExtensionProfile {
		return llmtypes.ApplyEnvironmentOptions(config, policy)
	}
	return config, nil
}

func applyExecutionOptions(config llmtypes.Config, options *llmtypes.ExecutionOptions, record *conversationservice.GetConversationResponse) (llmtypes.Config, error) {
	if err := options.Validate(); err != nil {
		return llmtypes.Config{}, err
	}
	config = config.Clone()
	config.ExecutionOptions = options.Clone()
	if options == nil {
		return config, nil
	}
	if options.Provider != nil && *options.Provider != config.Provider {
		return llmtypes.Config{}, errors.New("options.provider must match the selected model profile; select another profile to change providers")
	}
	var original *llmtypes.ConversationConfigSnapshot
	if options.HasModelOptions() && record != nil {
		var present bool
		var err error
		original, present, err = conversationservice.ConfigSnapshotFromMetadata(record.Metadata)
		if err != nil {
			return llmtypes.Config{}, err
		}
		if !present {
			return llmtypes.Config{}, errors.New("this older conversation has no saved model settings; start a new conversation to choose different settings")
		}
		// Compare effective snapshot values, including defaults for older
		// optional fields, without trusting the incoming live model config.
		pinned, err := original.Apply(llmtypes.Config{})
		if err != nil {
			return llmtypes.Config{}, err
		}
		original, err = llmtypes.NewConversationConfigSnapshot(pinned)
		if err != nil {
			return llmtypes.Config{}, err
		}
	}
	for _, option := range []struct {
		value  *string
		target *string
	}{{options.Model, &config.Model}, {options.WeakModel, &config.WeakModel}} {
		if option.value == nil {
			continue
		}
		model := strings.TrimSpace(*option.value)
		if alias, ok := config.Aliases[model]; ok {
			model = alias
		}
		*option.target = model
	}
	config.ModelAliasesResolved = true
	for _, option := range []struct {
		value  *int
		target *int
	}{
		{options.MaxTokens, &config.MaxTokens}, {options.WeakModelMaxTokens, &config.WeakModelMaxTokens}, {options.ThinkingBudgetTokens, &config.ThinkingBudgetTokens},
	} {
		if option.value != nil {
			*option.target = *option.value
		}
	}
	if options.ReasoningEffort != nil {
		config.ReasoningEffort = *options.ReasoningEffort
	}
	if err := llmtypes.NormalizeReasoningConfig(&config); err != nil {
		return llmtypes.Config{}, err
	}
	if !slices.Contains(llmtypes.ReasoningEffortOptions(llmtypes.Config{Provider: config.Provider}), config.ReasoningEffort) {
		return llmtypes.Config{}, errors.Errorf("reasoning effort %q is not supported by provider %s", config.ReasoningEffort, config.Provider)
	}
	if options.ThinkingBudgetTokens != nil && config.Provider != "anthropic" {
		return llmtypes.Config{}, errors.New("thinkingBudgetTokens requires the anthropic provider")
	}
	if config.Provider == "anthropic" && config.ThinkingBudgetTokens > 0 && config.MaxTokens > 0 && config.ThinkingBudgetTokens >= config.MaxTokens {
		return llmtypes.Config{}, errors.New("thinkingBudgetTokens must be less than maxTokens")
	}
	if options.UseWeakModel != nil && *options.UseWeakModel && strings.TrimSpace(config.WeakModel) == "" {
		return llmtypes.Config{}, errors.New("useWeakModel requires a configured weak model")
	}
	updated, err := llmtypes.NewConversationConfigSnapshot(config)
	if err != nil {
		return llmtypes.Config{}, err
	}
	if original != nil && !reflect.DeepEqual(original, updated) {
		return llmtypes.Config{}, errors.New("model settings cannot be changed for this conversation; start a new conversation to change them")
	}
	return config, nil
}

// ResolveExtensionProfile decodes a self-contained profile, not a session override.
// Provider credentials are resolved at execution time, not during registration.
func ResolveExtensionProfile(profile extensions.Profile, reasoningEffort string) (llmtypes.Config, error) {
	if err := profile.Validate(); err != nil {
		return llmtypes.Config{}, err
	}
	config, err := llm.GetConfigFromProfile(profile.Options)
	if err != nil {
		return llmtypes.Config{}, err
	}
	config.Profile = profile.Name
	config.ExtensionProfile = true
	if strings.TrimSpace(reasoningEffort) != "" {
		config.ReasoningEffort = reasoningEffort
		if err := llmtypes.NormalizeReasoningConfig(&config); err != nil {
			return llmtypes.Config{}, err
		}
	}
	return restrictExtensionProfile(config)
}

// Host permissions remain independent of isolated model/provider defaults.
func restrictExtensionProfile(config llmtypes.Config) (llmtypes.Config, error) {
	host := llmtypes.Config{
		AllowedTools:    viper.GetStringSlice("allowed_tools"),
		AllowedCommands: viper.GetStringSlice("allowed_commands"),
	}
	if viper.IsSet("skills.enabled") {
		host.Skills = &llmtypes.SkillsConfig{Enabled: viper.GetBool("skills.enabled")}
	}
	if viper.IsSet("extensions.enabled") {
		host.ExtensionSettings = map[string]any{"enabled": viper.GetBool("extensions.enabled")}
	}
	return llmtypes.ApplyEnvironmentOptions(config, host.EnvironmentOptions())
}

func extensionSnapshotBase(ctx context.Context, snapshot *llmtypes.ConversationConfigSnapshot) (llmtypes.Config, error) {
	platform := snapshot.Provider
	switch snapshot.Provider {
	case "openai":
		if snapshot.OpenAI != nil && snapshot.OpenAI.Platform != "" {
			platform = snapshot.OpenAI.Platform
		}
	case "anthropic":
		if snapshot.Anthropic != nil && snapshot.Anthropic.Platform != "" {
			platform = snapshot.Anthropic.Platform
		}
	}
	platform = strings.ToLower(strings.TrimSpace(platform))
	if resolve, ok := ctx.Value(profileResolverContextKey{}).(func(string, string) (llmtypes.Config, error)); ok {
		live, err := resolve(snapshot.Profile, "")
		if err == nil && live.ExtensionProfile && live.Provider == snapshot.Provider && llm.ProviderPlatform(live, snapshot.Provider) == platform {
			return live.Clone(), nil
		}
	}
	return llmtypes.Config{}, errors.Errorf("extension profile %q is unavailable or its provider changed; initialize its extension on this runner before resuming", snapshot.Profile)
}

func executionMessageOpt(options *llmtypes.ExecutionOptions) llmtypes.MessageOpt {
	opt := llmtypes.MessageOpt{PromptCache: true}
	if options != nil {
		opt.NoToolUse = options.ToolsDisabled()
		if options.MaxTurns != nil {
			opt.MaxTurns = *options.MaxTurns
		}
		if options.UseWeakModel != nil {
			opt.UseWeakModel = *options.UseWeakModel
		}
	}
	return opt
}
