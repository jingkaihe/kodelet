package llm

// Usage represents token usage information from LLM API calls
type Usage struct {
	InputTokens              int               `json:"inputTokens"`                // Regular input tokens count
	OutputTokens             int               `json:"outputTokens"`               // Output tokens generated
	CacheCreationInputTokens int               `json:"cacheCreationInputTokens"`   // Tokens used for creating cache entries
	CacheReadInputTokens     int               `json:"cacheReadInputTokens"`       // Tokens used for reading from cache
	InputCost                float64           `json:"inputCost"`                  // Cost for input tokens in USD
	OutputCost               float64           `json:"outputCost"`                 // Cost for output tokens in USD
	CacheCreationCost        float64           `json:"cacheCreationCost"`          // Cost for cache creation in USD
	CacheReadCost            float64           `json:"cacheReadCost"`              // Cost for cache read in USD
	CurrentContextWindow     int               `json:"currentContextWindow"`       // Current context window size
	MaxContextWindow         int               `json:"maxContextWindow"`           // Max context window size
	ContextBreakdown         *ContextBreakdown `json:"contextBreakdown,omitempty"` // Estimated composition of the live model context
	// Per-response thinking generated since the last context replacement. Kept
	// separately from the estimate so message appends cannot discard the counts.
	ContextThinkingByResponse map[string]int `json:"contextThinkingByResponse,omitempty"`
}

// ContextBreakdown estimates the current model-visible context, not cumulative
// billed usage. It excludes archived history and opaque media/encrypted payloads.
// ThinkingTokens sums provider-reported thinking generated since the last context
// replacement. It estimates context contribution, not exact provider retention.
// Nil means no thinking counts have been reported for this context.
// Its total is independent of the provider-reported context window size.
type ContextBreakdown struct {
	SystemPromptTokens   int                `json:"systemPromptTokens"`
	ToolDefinitionTokens int                `json:"toolDefinitionTokens"`
	InputTextTokens      int                `json:"inputTextTokens"`
	OutputTextTokens     int                `json:"outputTextTokens"`
	ThinkingTokens       *int               `json:"thinkingTokens"`
	OtherTokens          int                `json:"otherTokens"`
	Tools                []ToolContextUsage `json:"tools"`
	TotalTokens          int                `json:"totalTokens"`
	CodeMode             string             `json:"codeMode"`
	Estimated            bool               `json:"estimated"`
}

// ToolContextUsage estimates arguments and results for model-visible calls to a
// named tool. Nested code-mode executions are not separate model tool calls.
type ToolContextUsage struct {
	Name         string `json:"name"`
	InputTokens  int    `json:"inputTokens"`
	OutputTokens int    `json:"outputTokens"`
	Calls        int    `json:"calls"`
}

// TotalCost returns the total cost of all token usage
func (u *Usage) TotalCost() float64 {
	return u.InputCost + u.OutputCost + u.CacheCreationCost + u.CacheReadCost
}

// TotalTokens returns the total number of tokens used
func (u *Usage) TotalTokens() int {
	return u.InputTokens + u.OutputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
}
