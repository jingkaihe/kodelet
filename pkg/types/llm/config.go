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
	Aliases              map[string]string  // Aliases maps short model names to full model names
}
