package llm

// Usage represents token usage information from LLM API calls
type Usage struct {
	InputTokens              int     `json:"inputTokens"`              // Regular input tokens count
	OutputTokens             int     `json:"outputTokens"`             // Output tokens generated
	CacheCreationInputTokens int     `json:"cacheCreationInputTokens"` // Tokens used for creating cache entries
	CacheReadInputTokens     int     `json:"cacheReadInputTokens"`     // Tokens used for reading from cache
	InputCost                float64 `json:"inputCost"`                // Cost for input tokens in USD
	OutputCost               float64 `json:"outputCost"`               // Cost for output tokens in USD
	CacheCreationCost        float64 `json:"cacheCreationCost"`        // Cost for cache creation in USD
	CacheReadCost            float64 `json:"cacheReadCost"`            // Cost for cache read in USD
	CurrentContextWindow     int     `json:"currentContextWindow"`     // Current context window size
	MaxContextWindow         int     `json:"maxContextWindow"`         // Max context window size
}

// TotalCost returns the total cost of all token usage
func (u *Usage) TotalCost() float64 {
	return u.InputCost + u.OutputCost + u.CacheCreationCost + u.CacheReadCost
}

// TotalTokens returns the total number of tokens used
func (u *Usage) TotalTokens() int {
	return u.InputTokens + u.OutputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
}
