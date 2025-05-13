package types

// Config holds the configuration for the LLM client
type Config struct {
	Model     string // Model is the main driver
	WeakModel string // WeakModel is the less capable but faster model to use
	MaxTokens int
}
