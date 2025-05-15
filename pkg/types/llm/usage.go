package llm

// Usage represents token usage information from LLM API calls
type Usage struct {
	InputTokens              int     // Regular input tokens count
	OutputTokens             int     // Output tokens generated
	CacheCreationInputTokens int     // Tokens used for creating cache entries
	CacheReadInputTokens     int     // Tokens used for reading from cache
	InputCost                float64 // Cost for input tokens in USD
	OutputCost               float64 // Cost for output tokens in USD
	CacheCreationCost        float64 // Cost for cache creation in USD
	CacheReadCost            float64 // Cost for cache read in USD
	CurrentContextWindow     int     // Current context window size
	MaxContextWindow         int     // Max context window size
}

// TotalCost returns the total cost of all token usage
func (u *Usage) TotalCost() float64 {
	return u.InputCost + u.OutputCost + u.CacheCreationCost + u.CacheReadCost
}

// TotalTokens returns the total number of tokens used
func (u *Usage) TotalTokens() int {
	return u.InputTokens + u.OutputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
}
