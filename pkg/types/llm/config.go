package llm

import (
	"encoding/json"
	"strings"
	"time"
)

// AnthropicAPIAccess defines the mode for Anthropic API access
type AnthropicAPIAccess string

// ToolMode defines how the agent can interact with project files.
type ToolMode string

// ConversationSummaryMode defines how persisted conversation summaries are produced.
type ConversationSummaryMode string

const (
	// MinBashTimeout is the minimum timeout a bash tool call can request.
	MinBashTimeout = 10 * time.Second

	// DefaultBashTimeout is the default maximum timeout for bash tool calls.
	DefaultBashTimeout = 120 * time.Second

	// AnthropicAPIAccessAuto uses subscription auth if available, then falls back to API key
	AnthropicAPIAccessAuto AnthropicAPIAccess = "auto"
	// AnthropicAPIAccessSubscription forces use of subscription-based OAuth auth only
	AnthropicAPIAccessSubscription AnthropicAPIAccess = "subscription"
	// AnthropicAPIAccessAPIKey forces use of API key-based auth only
	AnthropicAPIAccessAPIKey AnthropicAPIAccess = "api-key"

	// ToolModeFull allows the standard direct file tools.
	ToolModeFull ToolMode = "full"
	// ToolModePatch restricts file operations to apply_patch plus search/navigation tools.
	ToolModePatch ToolMode = "patch"

	// ConversationSummaryModeLLM generates a short summary via an LLM.
	ConversationSummaryModeLLM ConversationSummaryMode = "llm"
	// ConversationSummaryModeFirstMessage uses the first user message as the summary.
	ConversationSummaryModeFirstMessage ConversationSummaryMode = "first_message"

	// DefaultCompactRatio is the default context window utilization threshold for automatic compaction.
	DefaultCompactRatio = 0.8
)

// IsPatchMode reports whether the tool mode should use apply_patch-only workflows.
func (m ToolMode) IsPatchMode() bool {
	return m == ToolModePatch
}

// UsesLLM reports whether conversation summaries should be generated via an LLM.
func (m ConversationSummaryMode) UsesLLM() bool {
	return m == "" || m == ConversationSummaryModeLLM
}

// Config holds the configuration for the LLM client
type Config struct {
	IsSubAgent           bool               `mapstructure:"is_sub_agent" json:"is_sub_agent" yaml:"is_sub_agent"` // IsSubAgent is true if the LLM is a sub-agent
	Provider             string             `mapstructure:"provider" json:"provider" yaml:"provider"`             // Provider is the LLM provider (anthropic, openai)
	Model                string             `mapstructure:"model" json:"model" yaml:"model"`                      // Model is the main driver
	WeakModel            string             `mapstructure:"weak_model" json:"weak_model" yaml:"weak_model"`       // WeakModel is the less capable but faster model to use
	MaxTokens            int                `mapstructure:"max_tokens" json:"max_tokens" yaml:"max_tokens"`
	WeakModelMaxTokens   int                `mapstructure:"weak_model_max_tokens" json:"weak_model_max_tokens" yaml:"weak_model_max_tokens"`    // WeakModelMaxTokens is the maximum tokens for the weak model
	ThinkingBudgetTokens int                `mapstructure:"thinking_budget_tokens" json:"thinking_budget_tokens" yaml:"thinking_budget_tokens"` // ThinkingBudgetTokens is sent as Anthropic manual budget_tokens on non-adaptive Claude models; adaptive Claude models ignore it
	ReasoningEffort      string             `mapstructure:"reasoning_effort" json:"reasoning_effort" yaml:"reasoning_effort"`                   // ReasoningEffort controls supported provider effort settings (e.g. OpenAI reasoning models, Anthropic adaptive thinking models where "none" disables adaptive thinking)
	AllowedCommands      []string           `mapstructure:"allowed_commands" json:"allowed_commands" yaml:"allowed_commands"`                   // AllowedCommands is a list of allowed command patterns for the bash tool
	AllowedDomainsFile   string             `mapstructure:"allowed_domains_file" json:"allowed_domains_file" yaml:"allowed_domains_file"`       // AllowedDomainsFile is the path to the file containing allowed domains for web_fetch tool
	AllowedTools         []string           `mapstructure:"allowed_tools" json:"allowed_tools" yaml:"allowed_tools"`                            // AllowedTools is a list of allowed tools for the main agent (empty means use defaults)
	WorkingDirectory     string             `mapstructure:"working_directory" json:"working_directory" yaml:"working_directory"`
	ToolMode             ToolMode           `mapstructure:"tool_mode" json:"tool_mode" yaml:"tool_mode"`                                    // ToolMode controls file-interaction behavior (e.g. full or patch)
	AnthropicAPIAccess   AnthropicAPIAccess `mapstructure:"anthropic_api_access" json:"anthropic_api_access" yaml:"anthropic_api_access"`   // AnthropicAPIAccess controls how to authenticate with Anthropic API
	AnthropicAccount     string             `mapstructure:"anthropic_account" json:"anthropic_account" yaml:"anthropic_account"`            // AnthropicAccount specifies which Anthropic subscription account to use
	Aliases              map[string]string  `mapstructure:"aliases" json:"aliases,omitempty" yaml:"aliases,omitempty"`                      // Aliases maps short model names to full model names
	Retry                RetryConfig        `mapstructure:"retry" json:"retry" yaml:"retry"`                                                // Retry configuration for API calls
	Sysprompt            string             `mapstructure:"sysprompt" json:"sysprompt,omitempty" yaml:"sysprompt,omitempty"`                // Sysprompt is the path to a custom system prompt template file
	SyspromptArgs        map[string]string  `mapstructure:"sysprompt_args" json:"sysprompt_args,omitempty" yaml:"sysprompt_args,omitempty"` // SyspromptArgs are custom template arguments for system prompt rendering
	Bash                 *BashConfig        `mapstructure:"bash" json:"bash,omitempty" yaml:"bash,omitempty"`                               // Bash contains bash tool configuration

	// Profile system configuration
	Profile  string                   `mapstructure:"profile" json:"profile,omitempty" yaml:"profile,omitempty"`    // Active profile name
	Profiles map[string]ProfileConfig `mapstructure:"profiles" json:"profiles,omitempty" yaml:"profiles,omitempty"` // Named configuration profiles

	// Provider-specific configurations
	OpenAI    *OpenAIConfig    `mapstructure:"openai" json:"openai,omitempty" yaml:"openai,omitempty"`          // OpenAI-specific configuration including compatible providers
	Anthropic *AnthropicConfig `mapstructure:"anthropic" json:"anthropic,omitempty" yaml:"anthropic,omitempty"` // Anthropic-specific configuration including compatible providers

	// SubagentArgs is CLI arguments to pass when spawning subagents via shell-out
	// Example: "--profile cheap" or "--use-weak-model"
	SubagentArgs string `mapstructure:"subagent_args" json:"subagent_args,omitempty" yaml:"subagent_args,omitempty"`

	// Skills configuration
	Skills *SkillsConfig `mapstructure:"skills" json:"skills,omitempty" yaml:"skills,omitempty"` // Skills configuration for agentic skills system

	// Context configuration
	Context *ContextConfig `mapstructure:"context" json:"context,omitempty" yaml:"context,omitempty"` // Context configuration for context file discovery

	// Runtime feature toggle configuration
	Extensions              any                     `mapstructure:"-" json:"-" yaml:"-"`                                                                         // Extensions is the active extension runtime for lifecycle events
	EnableFSSearchTools     bool                    `mapstructure:"enable_fs_search_tools" json:"enable_fs_search_tools" yaml:"enable_fs_search_tools"`          // EnableFSSearchTools enables glob_tool and grep_tool and updates prompt/tool guidance accordingly
	ConversationSummaryMode ConversationSummaryMode `mapstructure:"conversation_summary_mode" json:"conversation_summary_mode" yaml:"conversation_summary_mode"` // ConversationSummaryMode controls whether persisted conversation summaries come from the LLM or first user message
	DisableSubagent         bool                    `mapstructure:"disable_subagent" json:"disable_subagent" yaml:"disable_subagent"`                            // DisableSubagent disables the subagent tool and removes subagent-related system prompt context
	RecipeName              string                  `mapstructure:"recipe_name" json:"recipe_name" yaml:"recipe_name"`                                           // RecipeName is the active recipe/fragment name for extension context metadata
	CompactRatio            float64                 `mapstructure:"compact_ratio" json:"compact_ratio" yaml:"compact_ratio"`                                     // CompactRatio is the context utilization threshold for automatic compaction (>0.0-1.0)
}

// BashConfig holds configuration for the bash tool.
type BashConfig struct {
	Timeout time.Duration `mapstructure:"timeout" json:"timeout" yaml:"timeout"` // Timeout is the maximum allowed timeout for a bash tool call
}

// MarshalJSON renders durations as config-friendly strings instead of nanoseconds.
func (c BashConfig) MarshalJSON() ([]byte, error) {
	type bashConfig struct {
		Timeout string `json:"timeout"`
	}

	return json.Marshal(bashConfig{Timeout: c.Timeout.String()})
}

// MarshalYAML renders durations as config-friendly strings instead of nanoseconds.
func (c BashConfig) MarshalYAML() (any, error) {
	type bashConfig struct {
		Timeout string `yaml:"timeout"`
	}

	return bashConfig{Timeout: c.Timeout.String()}, nil
}

// BashTimeout returns the configured bash tool timeout, or the default if unset.
func (c Config) BashTimeout() time.Duration {
	if c.Bash == nil || c.Bash.Timeout == 0 {
		return DefaultBashTimeout
	}
	return c.Bash.Timeout
}

// OpenAIAPIMode defines which OpenAI-compatible API surface to use.
type OpenAIAPIMode string

// OpenAIServiceTier defines the optional OpenAI service tier preference.
// Kodelet accepts the native OpenAI values plus Codex's user-facing `fast`
// alias, which is sent to the API as `priority`.
type OpenAIServiceTier string

const (
	// OpenAIAPIModeChatCompletions routes requests via chat completions API.
	OpenAIAPIModeChatCompletions OpenAIAPIMode = "chat_completions"
	// OpenAIAPIModeResponses routes requests via responses API.
	OpenAIAPIModeResponses OpenAIAPIMode = "responses"

	// OpenAIServiceTierAuto defers to the project default service tier.
	OpenAIServiceTierAuto OpenAIServiceTier = "auto"
	// OpenAIServiceTierDefault uses the standard service tier.
	OpenAIServiceTierDefault OpenAIServiceTier = "default"
	// OpenAIServiceTierFast is the Codex-friendly alias for priority processing.
	OpenAIServiceTierFast OpenAIServiceTier = "fast"
	// OpenAIServiceTierFlex requests flex processing.
	OpenAIServiceTierFlex OpenAIServiceTier = "flex"
	// OpenAIServiceTierPriority requests priority processing.
	OpenAIServiceTierPriority OpenAIServiceTier = "priority"
	// OpenAIServiceTierScale requests scale processing when supported.
	OpenAIServiceTierScale OpenAIServiceTier = "scale"
)

// ParseOpenAIServiceTier normalizes and validates a configured service tier.
func ParseOpenAIServiceTier(raw string) (OpenAIServiceTier, bool) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		return "", false
	}

	switch OpenAIServiceTier(normalized) {
	case OpenAIServiceTierAuto,
		OpenAIServiceTierDefault,
		OpenAIServiceTierFast,
		OpenAIServiceTierFlex,
		OpenAIServiceTierPriority,
		OpenAIServiceTierScale:
		return OpenAIServiceTier(normalized), true
	default:
		return "", false
	}
}

// WireValue returns the value that should be sent to the upstream API.
func (t OpenAIServiceTier) WireValue() string {
	if normalized, ok := ParseOpenAIServiceTier(string(t)); ok {
		if normalized == OpenAIServiceTierFast {
			return string(OpenAIServiceTierPriority)
		}
		return string(normalized)
	}

	return ""
}

// OpenAIConfig holds OpenAI-specific configuration including support for compatible APIs
type OpenAIConfig struct {
	Platform      string                  `mapstructure:"platform" json:"platform" yaml:"platform"`                                       // Canonical platform name for OpenAI-compatible APIs (e.g., openai, codex, fireworks)
	BaseURL       string                  `mapstructure:"base_url" json:"base_url" yaml:"base_url"`                                       // Custom API base URL (overrides platform defaults)
	APIKeyEnvVar  string                  `mapstructure:"api_key_env_var" json:"api_key_env_var" yaml:"api_key_env_var"`                  // Environment variable name for API key (overrides platform default)
	APIMode       OpenAIAPIMode           `mapstructure:"api_mode" json:"api_mode" yaml:"api_mode"`                                       // Preferred API mode selection (chat_completions or responses)
	ServiceTier   OpenAIServiceTier       `mapstructure:"service_tier" json:"service_tier" yaml:"service_tier"`                           // Optional service tier hint (e.g. auto, default, fast, flex, priority, scale)
	EnableSearch  *bool                   `mapstructure:"enable_search" json:"enable_search,omitempty" yaml:"enable_search,omitempty"`    // Enable native OpenAI Responses web_search tool when supported (defaults to true)
	WebSocketMode *bool                   `mapstructure:"websocket_mode" json:"websocket_mode,omitempty" yaml:"websocket_mode,omitempty"` // Use Responses API WebSocket transport when supported (defaults to true)
	ManualCache   bool                    `mapstructure:"manual_cache" json:"manual_cache" yaml:"manual_cache"`                           // Enables manual cache affinity headers for Chat Completions when prompt caching is requested
	Models        *CustomModels           `mapstructure:"models" json:"models,omitempty" yaml:"models,omitempty"`                         // Custom model configuration
	Pricing       map[string]ModelPricing `mapstructure:"pricing" json:"pricing,omitempty" yaml:"pricing,omitempty"`                      // Custom pricing configuration
}

// AnthropicConfig holds Anthropic-specific configuration including compatible platforms.
type AnthropicConfig struct {
	Platform         string `mapstructure:"platform" json:"platform" yaml:"platform"`                                                // Canonical platform name for Anthropic-compatible APIs (e.g., anthropic, copilot)
	BaseURL          string `mapstructure:"base_url" json:"base_url" yaml:"base_url"`                                                // Custom API base URL (overrides platform defaults)
	AdaptiveThinking bool   `mapstructure:"adaptive_thinking" json:"adaptive_thinking,omitempty" yaml:"adaptive_thinking,omitempty"` // Forces Anthropic adaptive-thinking request plumbing for the configured custom model ID when true
}

// CustomModels holds model categorization for custom configurations
type CustomModels struct {
	Reasoning    []string `mapstructure:"reasoning" json:"reasoning" yaml:"reasoning"`             // Models that support reasoning (o1, o3, etc.)
	NonReasoning []string `mapstructure:"non_reasoning" json:"non_reasoning" yaml:"non_reasoning"` // Models that don't support reasoning (gpt-4, etc.)
}

// ModelPricing holds the per-token pricing for different operations
type ModelPricing struct {
	// Input token cost per token.
	Input float64 `mapstructure:"input" json:"input" yaml:"input"`
	// Cached input token cost per token.
	CachedInput float64 `mapstructure:"cached_input" json:"cached_input" yaml:"cached_input"`
	// Output token cost per token.
	Output float64 `mapstructure:"output" json:"output" yaml:"output"`
	// Long-context input token cost per token.
	LongContextInput float64 `mapstructure:"long_context_input" json:"long_context_input,omitempty" yaml:"long_context_input,omitempty"`
	// Long-context cached input token cost per token.
	LongContextCachedInput float64 `mapstructure:"long_context_cached_input" json:"long_context_cached_input,omitempty" yaml:"long_context_cached_input,omitempty"`
	// Long-context output token cost per token.
	LongContextOutput float64 `mapstructure:"long_context_output" json:"long_context_output,omitempty" yaml:"long_context_output,omitempty"`
	// Prompt token threshold for long-context pricing.
	LongContextThreshold int `mapstructure:"long_context_threshold" json:"long_context_threshold,omitempty" yaml:"long_context_threshold,omitempty"`
	// Maximum context window size.
	ContextWindow int `mapstructure:"context_window" json:"context_window" yaml:"context_window"`
}

// ForPromptTokens returns long-context pricing when the configured prompt token
// threshold is exceeded. OpenAI applies long-context rates to the full session,
// not just the tokens above the threshold.
func (p ModelPricing) ForPromptTokens(promptTokens int) ModelPricing {
	if p.LongContextThreshold <= 0 || promptTokens <= p.LongContextThreshold {
		return p
	}

	if p.LongContextInput > 0 {
		p.Input = p.LongContextInput
	}
	if p.LongContextCachedInput > 0 {
		p.CachedInput = p.LongContextCachedInput
	}
	if p.LongContextOutput > 0 {
		p.Output = p.LongContextOutput
	}
	return p
}

// CustomPricing maps model names to their pricing information
type CustomPricing map[string]ModelPricing

// ProfileConfig holds the configuration values for a named profile
type ProfileConfig map[string]any

// RetryConfig holds the retry configuration for API calls
// Note: Anthropic only uses Attempts (relies on SDK retry), OpenAI uses all fields
type RetryConfig struct {
	Attempts     int    `mapstructure:"attempts" json:"attempts" yaml:"attempts"`                // Maximum number of retry attempts (default: 3)
	InitialDelay int    `mapstructure:"initial_delay" json:"initial_delay" yaml:"initial_delay"` // Initial delay in milliseconds (default: 1000) - OpenAI only
	MaxDelay     int    `mapstructure:"max_delay" json:"max_delay" yaml:"max_delay"`             // Maximum delay in milliseconds (default: 10000) - OpenAI only
	BackoffType  string `mapstructure:"backoff_type" json:"backoff_type" yaml:"backoff_type"`    // Backoff strategy: "fixed", "exponential" (default: "exponential") - OpenAI only
}

// DefaultRetryConfig holds the default retry configuration
var DefaultRetryConfig = RetryConfig{
	Attempts:     3,
	InitialDelay: 1000,  // 1 second
	MaxDelay:     10000, // 10 seconds
	BackoffType:  "exponential",
}

// SkillsConfig holds configuration for the agentic skills system.
// When this config is nil or omitted, skills are enabled by default.
// To disable skills, explicitly set Enabled to false.
type SkillsConfig struct {
	// Enabled controls whether skills are active. When the SkillsConfig is nil
	// (not specified in config), skills default to enabled. Set to false to disable.
	Enabled bool `mapstructure:"enabled" json:"enabled" yaml:"enabled"`
	// Allowed is an allowlist of skill names. When empty, all discovered skills are available.
	// When specified, only the listed skills will be enabled.
	Allowed []string `mapstructure:"allowed" json:"allowed" yaml:"allowed"`
}

// ContextConfig holds configuration for context file discovery.
// Context files provide project-specific instructions and guidelines to the agent.
type ContextConfig struct {
	// Patterns is a list of filenames to search for in each directory.
	// Default is ["AGENTS.md"]. Files are searched in order; first match wins per directory.
	Patterns []string `mapstructure:"patterns" json:"patterns" yaml:"patterns"`
}

// DefaultContextPatterns returns the default context file patterns.
func DefaultContextPatterns() []string {
	return []string{"AGENTS.md"}
}
