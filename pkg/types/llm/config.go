package llm

// Config holds the configuration for the LLM client
type Config struct {
	IsSubAgent           bool   // IsSubAgent is true if the LLM is a sub-agent
	Model                string // Model is the main driver
	WeakModel            string // WeakModel is the less capable but faster model to use
	MaxTokens            int
	WeakModelMaxTokens   int // WeakModelMaxTokens is the maximum tokens for the weak model
	ThinkingBudgetTokens int // ThinkingBudgetTokens is the budget for the thinking capability
}
