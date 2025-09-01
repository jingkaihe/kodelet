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
	AllowedDomainsFile   string             // AllowedDomainsFile is the path to the file containing allowed domains for web_fetch tool
	AllowedTools         []string           // AllowedTools is a list of allowed tools for the main agent (empty means use defaults)
	AnthropicAPIAccess   AnthropicAPIAccess // AnthropicAPIAccess controls how to authenticate with Anthropic API
	UseCopilot           bool               // UseCopilot enables GitHub Copilot subscription for OpenAI requests
	Aliases              map[string]string  // Aliases maps short model names to full model names
	Retry                RetryConfig        // Retry configuration for API calls

	Profile  string                   `mapstructure:"profile"`
	Profiles map[string]ProfileConfig `mapstructure:"profiles"`

	OpenAI   *OpenAIConfig           `mapstructure:"openai"`
	SubAgent *SubAgentConfigSettings `mapstructure:"subagent"`
}

type OpenAIConfig struct {
	Preset       string                  `mapstructure:"preset"`
	BaseURL      string                  `mapstructure:"base_url"`
	APIKeyEnvVar string                  `mapstructure:"api_key_env_var"`
	Models       *CustomModels           `mapstructure:"models"`
	Pricing      map[string]ModelPricing `mapstructure:"pricing"`
}

type CustomModels struct {
	Reasoning    []string `mapstructure:"reasoning"`
	NonReasoning []string `mapstructure:"non_reasoning"`
}

type ModelPricing struct {
	Input         float64 `mapstructure:"input"`
	CachedInput   float64 `mapstructure:"cached_input"`
	Output        float64 `mapstructure:"output"`
	ContextWindow int     `mapstructure:"context_window"`
}

type CustomPricing map[string]ModelPricing

type ProfileConfig map[string]interface{}

// RetryConfig holds the retry configuration for API calls
// Note: Anthropic only uses Attempts (relies on SDK retry), OpenAI uses all fields
type RetryConfig struct {
	Attempts     int    `mapstructure:"attempts"`      // Maximum number of retry attempts (default: 3)
	InitialDelay int    `mapstructure:"initial_delay"` // Initial delay in milliseconds (default: 1000) - OpenAI only
	MaxDelay     int    `mapstructure:"max_delay"`     // Maximum delay in milliseconds (default: 10000) - OpenAI only
	BackoffType  string `mapstructure:"backoff_type"`  // Backoff strategy: "fixed", "exponential" (default: "exponential") - OpenAI only
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
	Provider        string        `mapstructure:"provider"`         // Provider for subagent (anthropic, openai)
	Model           string        `mapstructure:"model"`            // Model for subagent
	MaxTokens       int           `mapstructure:"max_tokens"`       // Maximum tokens for subagent
	ReasoningEffort string        `mapstructure:"reasoning_effort"` // OpenAI specific reasoning effort
	ThinkingBudget  int           `mapstructure:"thinking_budget"`  // Anthropic specific thinking budget
	AllowedTools    []string      `mapstructure:"allowed_tools"`    // AllowedTools is a list of allowed tools for the subagent (empty means use defaults)
	OpenAI          *OpenAIConfig `mapstructure:"openai"`           // OpenAI-compatible provider configuration
}
