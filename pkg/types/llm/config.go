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
	IsSubAgent           bool   // IsSubAgent is true if the LLM is a sub-agent
	Provider             string // Provider is the LLM provider (anthropic, openai)
	Model                string // Model is the main driver
	WeakModel            string // WeakModel is the less capable but faster model to use
	MaxTokens            int
	WeakModelMaxTokens   int                // WeakModelMaxTokens is the maximum tokens for the weak model
	ThinkingBudgetTokens int                // ThinkingBudgetTokens is the budget for the thinking capability
	ReasoningEffort      string             // ReasoningEffort is used for OpenAI models (low, medium, high)
	CacheEvery           int                // CacheEvery represents how often Thread messages should be cached (Anthropic only)
	AllowedCommands      []string           // AllowedCommands is a list of allowed command patterns for the bash tool
	AllowedDomainsFile   string             // AllowedDomainsFile is the path to the file containing allowed domains for web_fetch and browser tools
	AnthropicAPIAccess   AnthropicAPIAccess // AnthropicAPIAccess controls how to authenticate with Anthropic API
	UseCopilot           bool               // UseCopilot enables GitHub Copilot subscription for OpenAI requests
	Aliases              map[string]string  // Aliases maps short model names to full model names

	// Provider-specific configurations
	OpenAI   *OpenAIConfig           `mapstructure:"openai"`   // OpenAI-specific configuration including compatible providers
	SubAgent *SubAgentConfigSettings `mapstructure:"subagent"` // SubAgent configuration for different models/providers
}

// OpenAIConfig holds OpenAI-specific configuration including support for compatible APIs
type OpenAIConfig struct {
	Preset       string                  `mapstructure:"preset"`          // Built-in preset for popular providers (e.g., "xai")
	BaseURL      string                  `mapstructure:"base_url"`        // Custom API base URL (overrides preset)
	APIKeyEnvVar string                  `mapstructure:"api_key_env_var"` // Environment variable name for API key (defaults to OPENAI_API_KEY)
	Models       *CustomModels           `mapstructure:"models"`          // Custom model configuration
	Pricing      map[string]ModelPricing `mapstructure:"pricing"`         // Custom pricing configuration
}

// CustomModels holds model categorization for custom configurations
type CustomModels struct {
	Reasoning    []string `mapstructure:"reasoning"`     // Models that support reasoning (o1, o3, etc.)
	NonReasoning []string `mapstructure:"non_reasoning"` // Models that don't support reasoning (gpt-4, etc.)
}

// ModelPricing holds the per-token pricing for different operations
type ModelPricing struct {
	Input         float64 `mapstructure:"input"`          // Input token cost per token
	CachedInput   float64 `mapstructure:"cached_input"`   // Cached input token cost per token
	Output        float64 `mapstructure:"output"`         // Output token cost per token
	ContextWindow int     `mapstructure:"context_window"` // Maximum context window size
}

// CustomPricing maps model names to their pricing information
type CustomPricing map[string]ModelPricing

// SubAgentConfigSettings holds the configuration for subagent behavior
type SubAgentConfigSettings struct {
	Provider        string        `mapstructure:"provider"`         // Provider for subagent (anthropic, openai)
	Model           string        `mapstructure:"model"`            // Model for subagent
	MaxTokens       int           `mapstructure:"max_tokens"`       // Maximum tokens for subagent
	ReasoningEffort string        `mapstructure:"reasoning_effort"` // OpenAI specific reasoning effort
	ThinkingBudget  int           `mapstructure:"thinking_budget"`  // Anthropic specific thinking budget
	OpenAI          *OpenAIConfig `mapstructure:"openai"`           // OpenAI-compatible provider configuration
}
