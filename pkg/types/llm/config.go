package llm

// Config holds the configuration for the LLM client
type Config struct {
	IsSubAgent     bool   // IsSubAgent is true if the LLM is a sub-agent
	Model          string // Model is the main driver
	WeakModel      string // WeakModel is the less capable but faster model to use
	MaxTokens      int
	ThinkingTokens int // ThinkingTokens is the budget for the thinking capability
}
