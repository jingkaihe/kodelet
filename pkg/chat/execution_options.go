package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"

	conversationservice "github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/llm/anthropic"
	codexpreset "github.com/jingkaihe/kodelet/pkg/llm/openai/preset/codex"
	openaipreset "github.com/jingkaihe/kodelet/pkg/llm/openai/preset/openai"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
)

// UnmarshalJSON keeps the optional execution-options envelope strict too: the
// standard JSON decoder otherwise bypasses ExecutionOptions.UnmarshalJSON when
// assigning null to a pointer. Invalid requests leave the receiver untouched.
func (r *ChatRequest) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return errors.Wrap(err, "invalid chat request")
	}
	if fields == nil {
		return errors.New("chat request must be an object")
	}
	for name, raw := range fields {
		if strings.EqualFold(name, "options") && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return errors.New("execution options must be an object, not null; omit options to inherit")
		}
	}
	type wireRequest ChatRequest
	var value wireRequest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return errors.Wrap(err, "invalid chat request")
	}
	*r = ChatRequest(value)
	return nil
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
			return llmtypes.Config{}, errors.New("conversation model profile is locked; start a new conversation to change it")
		}
	}
	return applyExecutionOptions(config, req.Options, record)
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
		return llmtypes.Config{}, errors.New("options.provider must match the daemon model profile; select a daemon profile to change providers")
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
			return llmtypes.Config{}, errors.New("cannot override model options when resuming a legacy conversation without config_snapshot metadata")
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
	modelPolicy := config
	for _, option := range []struct {
		name   string
		value  *string
		target *string
	}{{"model", options.Model, &config.Model}, {"weakModel", options.WeakModel, &config.WeakModel}} {
		if option.value == nil {
			continue
		}
		model := strings.TrimSpace(*option.value)
		if alias, ok := config.Aliases[model]; ok {
			model = alias
		}
		if record == nil && !executionModelAllowed(modelPolicy, model) {
			return llmtypes.Config{}, errors.Errorf("options.%s %q is not in the daemon's configured models or provider catalog; configure a daemon model profile or alias", option.name, model)
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
		return llmtypes.Config{}, errors.New("conversation model options are locked; start a new conversation to change them")
	}
	return config, nil
}

// The provider namespace is selected by trusted daemon configuration. Requests
// may choose shipped models or models explicitly configured by that daemon,
// but cannot supply endpoints, credentials, pricing, or a new provider policy.
func executionModelAllowed(config llmtypes.Config, model string) bool {
	if config.Provider != "anthropic" && config.Provider != "openai" {
		return false
	}
	if model == config.Model || model == config.WeakModel {
		return true
	}
	for _, configured := range config.Aliases {
		if model == configured {
			return true
		}
	}
	if config.Provider == "anthropic" {
		for configured := range anthropic.ModelPricingMap {
			if configured == model {
				return true
			}
		}
		return false
	}
	if config.OpenAI != nil {
		if _, ok := config.OpenAI.Pricing[model]; ok {
			return true
		}
		if models := config.OpenAI.Models; models != nil && (slices.Contains(models.Reasoning, model) || slices.Contains(models.NonReasoning, model)) {
			return true
		}
	}
	models := openaipreset.Models
	if config.OpenAI != nil && config.OpenAI.Platform == "codex" {
		models = codexpreset.Models
	} else if config.OpenAI != nil && config.OpenAI.Platform != "" && config.OpenAI.Platform != "openai" {
		return false
	}
	return slices.Contains(models.Reasoning, model) || slices.Contains(models.NonReasoning, model)
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
