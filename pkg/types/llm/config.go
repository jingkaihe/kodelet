package llm

// AnthropicAPIAccess defines the mode for Anthropic API access
type AnthropicAPIAccess string

const (
	// AnthropicAPIAccessAuto uses subscription auth if available, then falls back to API key
	AnthropicAPIAccessAuto AnthropicAPIAccess = "auto"
	// AnthropicAPIAccessSubscription forces use of subscription-based OAuth auth only
	AnthropicAPIAccessSubscription AnthropicAPIAccess = "subscription"
	// AnthropicAPIAccessAPIKey forces use of API key-based auth only
	AnthropicAPIAccessAPIKey AnthropicAPIAccess = "api-key"
)

// Config holds the configuration for the LLM client
type Config struct {
	IsSubAgent           bool               `mapstructure:"is_sub_agent" json:"is_sub_agent" yaml:"is_sub_agent"` // IsSubAgent is true if the LLM is a sub-agent
	IDE                  bool               `mapstructure:"ide" json:"ide" yaml:"ide"`                             // IDE enables IDE integration mode
	Provider             string             `mapstructure:"provider" json:"provider" yaml:"provider"`              // Provider is the LLM provider (anthropic, openai)
	Model                string             `mapstructure:"model" json:"model" yaml:"model"`                       // Model is the main driver
	WeakModel            string             `mapstructure:"weak_model" json:"weak_model" yaml:"weak_model"`       // WeakModel is the less capable but faster model to use
	MaxTokens            int                `mapstructure:"max_tokens" json:"max_tokens" yaml:"max_tokens"`
	WeakModelMaxTokens   int                `mapstructure:"weak_model_max_tokens" json:"weak_model_max_tokens" yaml:"weak_model_max_tokens"`    // WeakModelMaxTokens is the maximum tokens for the weak model
	ThinkingBudgetTokens int                `mapstructure:"thinking_budget_tokens" json:"thinking_budget_tokens" yaml:"thinking_budget_tokens"` // ThinkingBudgetTokens is the budget for the thinking capability
	ReasoningEffort      string             `mapstructure:"reasoning_effort" json:"reasoning_effort" yaml:"reasoning_effort"`                   // ReasoningEffort is used for OpenAI models (low, medium, high)
	CacheEvery           int                `mapstructure:"cache_every" json:"cache_every" yaml:"cache_every"`                                  // CacheEvery represents how often Thread messages should be cached (Anthropic only)
	AllowedCommands      []string           `mapstructure:"allowed_commands" json:"allowed_commands" yaml:"allowed_commands"`                   // AllowedCommands is a list of allowed command patterns for the bash tool
	AllowedDomainsFile   string             `mapstructure:"allowed_domains_file" json:"allowed_domains_file" yaml:"allowed_domains_file"`       // AllowedDomainsFile is the path to the file containing allowed domains for web_fetch tool
	AllowedTools         []string           `mapstructure:"allowed_tools" json:"allowed_tools" yaml:"allowed_tools"`                            // AllowedTools is a list of allowed tools for the main agent (empty means use defaults)
	AnthropicAPIAccess   AnthropicAPIAccess `mapstructure:"anthropic_api_access" json:"anthropic_api_access" yaml:"anthropic_api_access"`       // AnthropicAPIAccess controls how to authenticate with Anthropic API
	UseCopilot           bool               `mapstructure:"use_copilot" json:"use_copilot" yaml:"use_copilot"`                                  // UseCopilot enables GitHub Copilot subscription for OpenAI requests
	Aliases              map[string]string  `mapstructure:"aliases" json:"aliases,omitempty" yaml:"aliases,omitempty"`                          // Aliases maps short model names to full model names
	Retry                RetryConfig        `mapstructure:"retry" json:"retry" yaml:"retry"`                                                    // Retry configuration for API calls

	// Profile system configuration
	Profile  string                   `mapstructure:"profile" json:"profile,omitempty" yaml:"profile,omitempty"`    // Active profile name
	Profiles map[string]ProfileConfig `mapstructure:"profiles" json:"profiles,omitempty" yaml:"profiles,omitempty"` // Named configuration profiles

	// Provider-specific configurations
	OpenAI   *OpenAIConfig           `mapstructure:"openai" json:"openai,omitempty" yaml:"openai,omitempty"`       // OpenAI-specific configuration including compatible providers
	Google   *GoogleConfig           `mapstructure:"google" json:"google,omitempty" yaml:"google,omitempty"`       // Google GenAI-specific configuration
	SubAgent *SubAgentConfigSettings `mapstructure:"subagent" json:"subagent,omitempty" yaml:"subagent,omitempty"` // SubAgent configuration for different models/providers
}

// OpenAIConfig holds OpenAI-specific configuration including support for compatible APIs
type OpenAIConfig struct {
	Preset       string                  `mapstructure:"preset" json:"preset" yaml:"preset"`                            // Built-in preset for popular providers (e.g., "xai")
	BaseURL      string                  `mapstructure:"base_url" json:"base_url" yaml:"base_url"`                      // Custom API base URL (overrides preset)
	APIKeyEnvVar string                  `mapstructure:"api_key_env_var" json:"api_key_env_var" yaml:"api_key_env_var"` // Environment variable name for API key (defaults to OPENAI_API_KEY)
	Models       *CustomModels           `mapstructure:"models" json:"models,omitempty" yaml:"models,omitempty"`        // Custom model configuration
	Pricing      map[string]ModelPricing `mapstructure:"pricing" json:"pricing,omitempty" yaml:"pricing,omitempty"`     // Custom pricing configuration
}

// CustomModels holds model categorization for custom configurations
type CustomModels struct {
	Reasoning    []string `mapstructure:"reasoning" json:"reasoning" yaml:"reasoning"`             // Models that support reasoning (o1, o3, etc.)
	NonReasoning []string `mapstructure:"non_reasoning" json:"non_reasoning" yaml:"non_reasoning"` // Models that don't support reasoning (gpt-4, etc.)
}

// ModelPricing holds the per-token pricing for different operations
type ModelPricing struct {
	Input         float64 `mapstructure:"input" json:"input" yaml:"input"`                            // Input token cost per token
	CachedInput   float64 `mapstructure:"cached_input" json:"cached_input" yaml:"cached_input"`       // Cached input token cost per token
	Output        float64 `mapstructure:"output" json:"output" yaml:"output"`                         // Output token cost per token
	ContextWindow int     `mapstructure:"context_window" json:"context_window" yaml:"context_window"` // Maximum context window size
}

// CustomPricing maps model names to their pricing information
type CustomPricing map[string]ModelPricing

// GoogleConfig holds Google GenAI-specific configuration for both Vertex AI and Gemini API
type GoogleConfig struct {
	Backend        string `mapstructure:"backend" json:"backend" yaml:"backend"`                         // Backend to use: "gemini" or "vertexai" (auto-detected if not specified)
	APIKey         string `mapstructure:"api_key" json:"api_key" yaml:"api_key"`                         // API key for Gemini API
	Project        string `mapstructure:"project" json:"project" yaml:"project"`                         // Google Cloud project ID for Vertex AI
	Location       string `mapstructure:"location" json:"location" yaml:"location"`                      // Google Cloud region for Vertex AI (e.g., "us-central1")
	ThinkingBudget int32  `mapstructure:"thinking_budget" json:"thinking_budget" yaml:"thinking_budget"` // Token budget for thinking capability
}

type ProfileConfig map[string]interface{}

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

// SubAgentConfigSettings holds the configuration for subagent behavior
type SubAgentConfigSettings struct {
	Provider        string        `mapstructure:"provider" json:"provider" yaml:"provider"`                         // Provider for subagent (anthropic, openai, google)
	Model           string        `mapstructure:"model" json:"model" yaml:"model"`                                  // Model for subagent
	MaxTokens       int           `mapstructure:"max_tokens" json:"max_tokens" yaml:"max_tokens"`                   // Maximum tokens for subagent
	ReasoningEffort string        `mapstructure:"reasoning_effort" json:"reasoning_effort" yaml:"reasoning_effort"` // OpenAI specific reasoning effort
	ThinkingBudget  int           `mapstructure:"thinking_budget" json:"thinking_budget" yaml:"thinking_budget"`    // Anthropic/Google specific thinking budget
	AllowedTools    []string      `mapstructure:"allowed_tools" json:"allowed_tools" yaml:"allowed_tools"`          // AllowedTools is a list of allowed tools for the subagent (empty means use defaults)
	OpenAI          *OpenAIConfig `mapstructure:"openai" json:"openai,omitempty" yaml:"openai,omitempty"`           // OpenAI-compatible provider configuration
	Google          *GoogleConfig `mapstructure:"google" json:"google,omitempty" yaml:"google,omitempty"`           // Google GenAI-specific configuration
}
