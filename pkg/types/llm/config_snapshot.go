package llm

import (
	"strings"

	"github.com/pkg/errors"
)

const ConversationConfigSnapshotVersion = 1

// ConversationConfigSnapshot captures immutable model and provider request
// settings for a persisted conversation. Runtime credentials, endpoints,
// prompt/context inputs, tool permissions, extensions, and other live policy
// remain sourced from current configuration.
type ConversationConfigSnapshot struct {
	Version                 int                            `json:"version" yaml:"version"`
	Profile                 string                         `json:"profile,omitempty" yaml:"profile,omitempty"`
	Provider                string                         `json:"provider" yaml:"provider"`
	Model                   string                         `json:"model" yaml:"model"`
	WeakModel               string                         `json:"weak_model,omitempty" yaml:"weak_model,omitempty"`
	MaxTokens               int                            `json:"max_tokens,omitempty" yaml:"max_tokens,omitempty"`
	WeakModelMaxTokens      int                            `json:"weak_model_max_tokens,omitempty" yaml:"weak_model_max_tokens,omitempty"`
	ThinkingBudgetTokens    int                            `json:"thinking_budget_tokens,omitempty" yaml:"thinking_budget_tokens,omitempty"`
	ReasoningEffort         string                         `json:"reasoning_effort" yaml:"reasoning_effort"`
	ConversationSummaryMode ConversationSummaryMode        `json:"conversation_summary_mode,omitempty" yaml:"conversation_summary_mode,omitempty"`
	CompactRatio            float64                        `json:"compact_ratio,omitempty" yaml:"compact_ratio,omitempty"`
	OpenAI                  *ConversationOpenAISnapshot    `json:"openai,omitempty" yaml:"openai,omitempty"`
	Anthropic               *ConversationAnthropicSnapshot `json:"anthropic,omitempty" yaml:"anthropic,omitempty"`
}

// ConversationOpenAISnapshot captures OpenAI request semantics that must remain
// stable to interpret and continue a persisted thread.
type ConversationOpenAISnapshot struct {
	Platform      string                  `json:"platform,omitempty" yaml:"platform,omitempty"`
	APIMode       OpenAIAPIMode           `json:"api_mode,omitempty" yaml:"api_mode,omitempty"`
	TextVerbosity OpenAITextVerbosity     `json:"text_verbosity,omitempty" yaml:"text_verbosity,omitempty"`
	ServiceTier   OpenAIServiceTier       `json:"service_tier,omitempty" yaml:"service_tier,omitempty"`
	ManualCache   bool                    `json:"manual_cache,omitempty" yaml:"manual_cache,omitempty"`
	Models        *CustomModels           `json:"models,omitempty" yaml:"models,omitempty"`
	Pricing       map[string]ModelPricing `json:"pricing,omitempty" yaml:"pricing,omitempty"`
}

// ConversationAnthropicSnapshot captures Anthropic request semantics that must
// remain stable for custom/adaptive-thinking models.
type ConversationAnthropicSnapshot struct {
	Platform         string `json:"platform,omitempty" yaml:"platform,omitempty"`
	AdaptiveThinking bool   `json:"adaptive_thinking,omitempty" yaml:"adaptive_thinking,omitempty"`
}

// NewConversationConfigSnapshot creates a safe, versioned snapshot from the
// effective configuration used to construct a thread.
func NewConversationConfigSnapshot(config Config) (*ConversationConfigSnapshot, error) {
	if err := NormalizeReasoningConfig(&config); err != nil {
		return nil, errors.Wrap(err, "failed to snapshot reasoning configuration")
	}
	if config.ConversationSummaryMode == "" {
		config.ConversationSummaryMode = ConversationSummaryModeLLM
	}
	if config.CompactRatio <= 0 {
		config.CompactRatio = DefaultCompactRatio
	}

	snapshot := &ConversationConfigSnapshot{
		Version:                 ConversationConfigSnapshotVersion,
		Profile:                 strings.TrimSpace(config.Profile),
		Provider:                strings.ToLower(strings.TrimSpace(config.Provider)),
		Model:                   strings.TrimSpace(config.Model),
		WeakModel:               strings.TrimSpace(config.WeakModel),
		MaxTokens:               config.MaxTokens,
		WeakModelMaxTokens:      config.WeakModelMaxTokens,
		ThinkingBudgetTokens:    config.ThinkingBudgetTokens,
		ReasoningEffort:         config.ReasoningEffort,
		ConversationSummaryMode: config.ConversationSummaryMode,
		CompactRatio:            config.CompactRatio,
	}
	switch snapshot.Provider {
	case "openai":
		if err := NormalizeOpenAITextVerbosity(&config); err != nil {
			return nil, errors.Wrap(err, "failed to snapshot OpenAI configuration")
		}
		snapshot.OpenAI = &ConversationOpenAISnapshot{}
		if config.OpenAI != nil {
			snapshot.OpenAI.Platform = strings.TrimSpace(config.OpenAI.Platform)
			snapshot.OpenAI.APIMode = config.OpenAI.APIMode
			snapshot.OpenAI.TextVerbosity = config.OpenAI.TextVerbosity
			snapshot.OpenAI.ServiceTier = config.OpenAI.ServiceTier
			snapshot.OpenAI.ManualCache = config.OpenAI.ManualCache
			snapshot.OpenAI.Models = cloneCustomModels(config.OpenAI.Models)
			snapshot.OpenAI.Pricing = cloneModelPricing(config.OpenAI.Pricing)
		}
	case "anthropic":
		snapshot.Anthropic = &ConversationAnthropicSnapshot{}
		if config.Anthropic != nil {
			snapshot.Anthropic.Platform = strings.TrimSpace(config.Anthropic.Platform)
			snapshot.Anthropic.AdaptiveThinking = config.Anthropic.AdaptiveThinking
		}
	}
	if err := snapshot.Validate(); err != nil {
		return nil, err
	}
	return snapshot, nil
}

// Validate validates a persisted configuration snapshot.
func (s *ConversationConfigSnapshot) Validate() error {
	if s == nil {
		return nil
	}
	if s.Version != ConversationConfigSnapshotVersion {
		return errors.Errorf("unsupported conversation config snapshot version %d", s.Version)
	}
	if strings.TrimSpace(s.Provider) == "" {
		return errors.New("conversation config snapshot provider is required")
	}
	switch strings.ToLower(strings.TrimSpace(s.Provider)) {
	case "anthropic", "openai":
	default:
		return errors.Errorf("unsupported conversation config snapshot provider %q", s.Provider)
	}
	if strings.TrimSpace(s.Model) == "" {
		return errors.New("conversation config snapshot model is required")
	}
	effort, err := NormalizeReasoningEffort(s.ReasoningEffort)
	if err != nil {
		return errors.Wrap(err, "invalid conversation config snapshot")
	}
	if effort == "" {
		return errors.New("conversation config snapshot reasoning_effort is required")
	}
	if s.ConversationSummaryMode != "" && s.ConversationSummaryMode != ConversationSummaryModeLLM && s.ConversationSummaryMode != ConversationSummaryModeFirstMessage {
		return errors.Errorf("invalid conversation config snapshot conversation_summary_mode %q", s.ConversationSummaryMode)
	}
	if s.CompactRatio < 0 || s.CompactRatio > 1 {
		return errors.Errorf("invalid conversation config snapshot compact_ratio %v", s.CompactRatio)
	}
	if s.OpenAI != nil {
		if s.OpenAI.APIMode != "" && s.OpenAI.APIMode != OpenAIAPIModeChatCompletions && s.OpenAI.APIMode != OpenAIAPIModeResponses {
			return errors.Errorf("invalid conversation config snapshot OpenAI api_mode %q", s.OpenAI.APIMode)
		}
		if s.OpenAI.TextVerbosity != "" {
			if _, ok := ParseOpenAITextVerbosity(string(s.OpenAI.TextVerbosity)); !ok {
				return errors.Errorf("invalid conversation config snapshot OpenAI text_verbosity %q", s.OpenAI.TextVerbosity)
			}
		}
		if s.OpenAI.ServiceTier != "" {
			if _, ok := ParseOpenAIServiceTier(string(s.OpenAI.ServiceTier)); !ok {
				return errors.Errorf("invalid conversation config snapshot OpenAI service_tier %q", s.OpenAI.ServiceTier)
			}
		}
	}
	return nil
}

// Apply overlays immutable snapshot values onto current live configuration.
// Creation-time reasoning policy is cleared so an existing conversation remains
// resumable after allowed_reasoning_efforts changes.
func (s *ConversationConfigSnapshot) Apply(config Config) (Config, error) {
	if s == nil {
		return config, nil
	}
	if err := s.Validate(); err != nil {
		return Config{}, err
	}
	effort, _ := NormalizeReasoningEffort(s.ReasoningEffort)

	config.Profile = strings.TrimSpace(s.Profile)
	config.Provider = strings.ToLower(strings.TrimSpace(s.Provider))
	config.Model = strings.TrimSpace(s.Model)
	config.WeakModel = strings.TrimSpace(s.WeakModel)
	// Snapshot model names are already the effective identifiers used by the
	// original thread. Discard live aliases and prevent NewThread from applying
	// user-configured or built-in aliases to them again.
	config.Aliases = nil
	config.ModelAliasesResolved = true
	config.MaxTokens = s.MaxTokens
	config.WeakModelMaxTokens = s.WeakModelMaxTokens
	config.ThinkingBudgetTokens = s.ThinkingBudgetTokens
	config.ReasoningEffort = effort
	config.AllowedReasoningEfforts = nil
	config.ConversationSummaryMode = s.ConversationSummaryMode
	config.CompactRatio = s.CompactRatio

	switch strings.ToLower(strings.TrimSpace(s.Provider)) {
	case "openai":
		config.Anthropic = nil
		openAI := s.OpenAI
		if openAI == nil {
			openAI = &ConversationOpenAISnapshot{}
		}
		if config.OpenAI == nil {
			config.OpenAI = &OpenAIConfig{}
		}
		config.OpenAI.Platform = strings.TrimSpace(openAI.Platform)
		config.OpenAI.APIMode = openAI.APIMode
		config.OpenAI.TextVerbosity = ""
		if openAI.TextVerbosity != "" {
			config.OpenAI.TextVerbosity, _ = ParseOpenAITextVerbosity(string(openAI.TextVerbosity))
		}
		config.OpenAI.ServiceTier = openAI.ServiceTier
		config.OpenAI.ManualCache = openAI.ManualCache
		config.OpenAI.Models = cloneCustomModels(openAI.Models)
		config.OpenAI.Pricing = cloneModelPricing(openAI.Pricing)
	case "anthropic":
		config.OpenAI = nil
		anthropic := s.Anthropic
		if anthropic == nil {
			anthropic = &ConversationAnthropicSnapshot{}
		}
		if config.Anthropic == nil {
			config.Anthropic = &AnthropicConfig{}
		}
		config.Anthropic.Platform = strings.TrimSpace(anthropic.Platform)
		config.Anthropic.AdaptiveThinking = anthropic.AdaptiveThinking
	}
	return config, nil
}

// CloneConversationConfigSnapshot returns a deep copy suitable for forks and
// service responses.
func CloneConversationConfigSnapshot(snapshot *ConversationConfigSnapshot) *ConversationConfigSnapshot {
	if snapshot == nil {
		return nil
	}
	cloned := *snapshot
	if snapshot.OpenAI != nil {
		openAI := *snapshot.OpenAI
		openAI.Models = cloneCustomModels(snapshot.OpenAI.Models)
		openAI.Pricing = cloneModelPricing(snapshot.OpenAI.Pricing)
		cloned.OpenAI = &openAI
	}
	if snapshot.Anthropic != nil {
		anthropic := *snapshot.Anthropic
		cloned.Anthropic = &anthropic
	}
	return &cloned
}

func cloneCustomModels(models *CustomModels) *CustomModels {
	if models == nil {
		return nil
	}
	return &CustomModels{
		Reasoning:    append([]string(nil), models.Reasoning...),
		NonReasoning: append([]string(nil), models.NonReasoning...),
	}
}

func cloneModelPricing(pricing map[string]ModelPricing) map[string]ModelPricing {
	if pricing == nil {
		return nil
	}
	cloned := make(map[string]ModelPricing, len(pricing))
	for model, value := range pricing {
		cloned[model] = value
	}
	return cloned
}
