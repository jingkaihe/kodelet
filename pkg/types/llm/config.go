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
	Provider             string             `mapstructure:"provider" json:"provider" yaml:"provider"`             // Provider is the LLM provider (anthropic, openai)
	Model                string             `mapstructure:"model" json:"model" yaml:"model"`                      // Model is the main driver
	WeakModel            string             `mapstructure:"weak_model" json:"weak_model" yaml:"weak_model"`       // WeakModel is the less capable but faster model to use
	MaxTokens            int                `mapstructure:"max_tokens" json:"max_tokens" yaml:"max_tokens"`
	WeakModelMaxTokens   int                `mapstructure:"weak_model_max_tokens" json:"weak_model_max_tokens" yaml:"weak_model_max_tokens"`    // WeakModelMaxTokens is the maximum tokens for the weak model
	ThinkingBudgetTokens int                `mapstructure:"thinking_budget_tokens" json:"thinking_budget_tokens" yaml:"thinking_budget_tokens"` // ThinkingBudgetTokens is the budget for the thinking capability
	ReasoningEffort      string             `mapstructure:"reasoning_effort" json:"reasoning_effort" yaml:"reasoning_effort"`                   // ReasoningEffort is used for OpenAI models (low, medium, high)
	AllowedCommands      []string           `mapstructure:"allowed_commands" json:"allowed_commands" yaml:"allowed_commands"`                   // AllowedCommands is a list of allowed command patterns for the bash tool
	AllowedDomainsFile   string             `mapstructure:"allowed_domains_file" json:"allowed_domains_file" yaml:"allowed_domains_file"`       // AllowedDomainsFile is the path to the file containing allowed domains for web_fetch tool
	AllowedTools         []string           `mapstructure:"allowed_tools" json:"allowed_tools" yaml:"allowed_tools"`                            // AllowedTools is a list of allowed tools for the main agent (empty means use defaults)
	AnthropicAPIAccess   AnthropicAPIAccess `mapstructure:"anthropic_api_access" json:"anthropic_api_access" yaml:"anthropic_api_access"`       // AnthropicAPIAccess controls how to authenticate with Anthropic API
	AnthropicAccount     string             `mapstructure:"anthropic_account" json:"anthropic_account" yaml:"anthropic_account"`                // AnthropicAccount specifies which Anthropic subscription account to use
	UseCopilot           bool               `mapstructure:"use_copilot" json:"use_copilot" yaml:"use_copilot"`                                  // UseCopilot enables GitHub Copilot subscription for OpenAI requests
	Aliases              map[string]string  `mapstructure:"aliases" json:"aliases,omitempty" yaml:"aliases,omitempty"`                          // Aliases maps short model names to full model names
	Retry                RetryConfig        `mapstructure:"retry" json:"retry" yaml:"retry"`                                                    // Retry configuration for API calls
	MCPExecutionMode     string             `mapstructure:"mcp_execution_mode" json:"mcp_execution_mode" yaml:"mcp_execution_mode"`             // MCP execution mode (code, direct, or empty)
	MCPWorkspaceDir      string             `mapstructure:"mcp_workspace_dir" json:"mcp_workspace_dir" yaml:"mcp_workspace_dir"`                // MCP workspace directory for code execution mode

	// Profile system configuration
	Profile  string                   `mapstructure:"profile" json:"profile,omitempty" yaml:"profile,omitempty"`    // Active profile name
	Profiles map[string]ProfileConfig `mapstructure:"profiles" json:"profiles,omitempty" yaml:"profiles,omitempty"` // Named configuration profiles

	// Provider-specific configurations
	OpenAI *OpenAIConfig `mapstructure:"openai" json:"openai,omitempty" yaml:"openai,omitempty"` // OpenAI-specific configuration including compatible providers
	Google *GoogleConfig `mapstructure:"google" json:"google,omitempty" yaml:"google,omitempty"` // Google GenAI-specific configuration

	// SubagentArgs is CLI arguments to pass when spawning subagents via shell-out
	// Example: "--profile cheap" or "--use-weak-model"
	SubagentArgs string `mapstructure:"subagent_args" json:"subagent_args,omitempty" yaml:"subagent_args,omitempty"`

	// Skills configuration
	Skills *SkillsConfig `mapstructure:"skills" json:"skills,omitempty" yaml:"skills,omitempty"` // Skills configuration for agentic skills system

	// Context configuration
	Context *ContextConfig `mapstructure:"context" json:"context,omitempty" yaml:"context,omitempty"` // Context configuration for context file discovery

	// Hooks configuration
	NoHooks bool `mapstructure:"no_hooks" json:"no_hooks" yaml:"no_hooks"` // NoHooks disables agent lifecycle hooks
}

// OpenAIConfig holds OpenAI-specific configuration including support for compatible APIs
type OpenAIConfig struct {
	Preset          string                  `mapstructure:"preset" json:"preset" yaml:"preset"`                                  // Built-in preset for popular providers (e.g., "xai", "codex")
	BaseURL         string                  `mapstructure:"base_url" json:"base_url" yaml:"base_url"`                            // Custom API base URL (overrides preset)
	APIKeyEnvVar    string                  `mapstructure:"api_key_env_var" json:"api_key_env_var" yaml:"api_key_env_var"`       // Environment variable name for API key (defaults to OPENAI_API_KEY)
	UseResponsesAPI bool                    `mapstructure:"use_responses_api" json:"use_responses_api" yaml:"use_responses_api"` // Use the Responses API instead of Chat Completions API
	Models          *CustomModels           `mapstructure:"models" json:"models,omitempty" yaml:"models,omitempty"`              // Custom model configuration
	Pricing         map[string]ModelPricing `mapstructure:"pricing" json:"pricing,omitempty" yaml:"pricing,omitempty"`           // Custom pricing configuration
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

// ProfileConfig holds the configuration values for a named profile
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
