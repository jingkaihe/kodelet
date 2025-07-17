package usage

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/logger"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// ConversationSummary represents the interface for conversation summary data needed for usage calculations
type ConversationSummary interface {
	GetID() string
	GetCreatedAt() time.Time
	GetUpdatedAt() time.Time
	GetMessageCount() int
	GetUsage() llmtypes.Usage
	GetProvider() string
}

// DailyUsage represents usage statistics for a single day
type DailyUsage struct {
	Date          time.Time
	Usage         llmtypes.Usage
	Conversations int
}

// UsageStats represents aggregated usage statistics
type UsageStats struct {
	Daily []DailyUsage
	Total llmtypes.Usage
}

// ProviderUsageStats represents usage statistics for a single provider
type ProviderUsageStats struct {
	Usage         llmtypes.Usage
	Conversations int
}

// ProviderBreakdownStats represents usage statistics broken down by provider
type ProviderBreakdownStats struct {
	ProviderStats      map[string]*ProviderUsageStats
	Total              llmtypes.Usage
	TotalConversations int
}

// DailyProviderUsage represents usage statistics for a single day with provider breakdown
type DailyProviderUsage struct {
	Date               time.Time
	ProviderUsage      map[string]*ProviderUsageStats // provider -> usage stats
	TotalUsage         llmtypes.Usage
	TotalConversations int
}

// DailyProviderBreakdownStats represents daily usage statistics broken down by provider
type DailyProviderBreakdownStats struct {
	Daily              []DailyProviderUsage
	Total              llmtypes.Usage
	TotalConversations int
}

// ConversationUsageStats represents usage statistics for the conversation list
type ConversationUsageStats struct {
	TotalConversations int     `json:"totalConversations"`
	TotalMessages      int     `json:"totalMessages"`
	TotalTokens        int     `json:"totalTokens"`
	TotalCost          float64 `json:"totalCost"`
	InputTokens        int     `json:"inputTokens"`
	OutputTokens       int     `json:"outputTokens"`
	CacheReadTokens    int     `json:"cacheReadTokens"`
	CacheWriteTokens   int     `json:"cacheWriteTokens"`
	InputCost          float64 `json:"inputCost"`
	OutputCost         float64 `json:"outputCost"`
	CacheReadCost      float64 `json:"cacheReadCost"`
	CacheWriteCost     float64 `json:"cacheWriteCost"`
}

// CalculateUsageStats calculates usage statistics from a list of conversation summaries
func CalculateUsageStats(summaries []ConversationSummary, startTime, endTime time.Time) *UsageStats {
	// Create map to aggregate daily usage
	dailyMap := make(map[string]*DailyUsage)
	totalUsage := llmtypes.Usage{}

	for _, summary := range summaries {
		// Use UpdatedAt as the date for this conversation's usage
		date := summary.GetUpdatedAt().Truncate(24 * time.Hour)

		// Filter by time range if specified
		if !startTime.IsZero() && date.Before(startTime) {
			continue
		}
		if !endTime.IsZero() && date.After(endTime) {
			continue
		}

		dateKey := date.Format("2006-01-02")

		// Initialize daily usage if not exists
		if _, exists := dailyMap[dateKey]; !exists {
			dailyMap[dateKey] = &DailyUsage{
				Date:  date,
				Usage: llmtypes.Usage{},
			}
		}

		// Add to daily and total usage
		daily := dailyMap[dateKey]
		usage := summary.GetUsage()
		daily.Usage.InputTokens += usage.InputTokens
		daily.Usage.OutputTokens += usage.OutputTokens
		daily.Usage.CacheCreationInputTokens += usage.CacheCreationInputTokens
		daily.Usage.CacheReadInputTokens += usage.CacheReadInputTokens
		daily.Usage.InputCost += usage.InputCost
		daily.Usage.OutputCost += usage.OutputCost
		daily.Usage.CacheCreationCost += usage.CacheCreationCost
		daily.Usage.CacheReadCost += usage.CacheReadCost
		daily.Conversations++

		// Add to total
		totalUsage.InputTokens += usage.InputTokens
		totalUsage.OutputTokens += usage.OutputTokens
		totalUsage.CacheCreationInputTokens += usage.CacheCreationInputTokens
		totalUsage.CacheReadInputTokens += usage.CacheReadInputTokens
		totalUsage.InputCost += usage.InputCost
		totalUsage.OutputCost += usage.OutputCost
		totalUsage.CacheCreationCost += usage.CacheCreationCost
		totalUsage.CacheReadCost += usage.CacheReadCost
	}

	// Convert map to sorted slice
	var dailyUsage []DailyUsage
	for _, usage := range dailyMap {
		dailyUsage = append(dailyUsage, *usage)
	}

	// Sort by date (newest first)
	for i := 0; i < len(dailyUsage); i++ {
		for j := i + 1; j < len(dailyUsage); j++ {
			if dailyUsage[i].Date.Before(dailyUsage[j].Date) {
				dailyUsage[i], dailyUsage[j] = dailyUsage[j], dailyUsage[i]
			}
		}
	}

	return &UsageStats{
		Daily: dailyUsage,
		Total: totalUsage,
	}
}

// CalculateConversationUsageStats calculates usage statistics for the conversation list UI
func CalculateConversationUsageStats(summaries []ConversationSummary) *ConversationUsageStats {
	stats := &ConversationUsageStats{
		TotalConversations: len(summaries),
	}

	if len(summaries) == 0 {
		return stats
	}

	// Calculate totals
	for _, summary := range summaries {
		// Count messages
		stats.TotalMessages += summary.GetMessageCount()

		// Sum up usage
		usage := summary.GetUsage()
		stats.InputTokens += usage.InputTokens
		stats.OutputTokens += usage.OutputTokens
		stats.CacheReadTokens += usage.CacheReadInputTokens
		stats.CacheWriteTokens += usage.CacheCreationInputTokens
		stats.InputCost += usage.InputCost
		stats.OutputCost += usage.OutputCost
		stats.CacheReadCost += usage.CacheReadCost
		stats.CacheWriteCost += usage.CacheCreationCost
	}

	// Calculate totals
	stats.TotalTokens = stats.InputTokens + stats.OutputTokens + stats.CacheReadTokens + stats.CacheWriteTokens
	stats.TotalCost = stats.InputCost + stats.OutputCost + stats.CacheReadCost + stats.CacheWriteCost

	return stats
}

// FormatNumber formats large numbers with commas for readability
func FormatNumber(n int) string {
	str := strconv.Itoa(n)
	if len(str) <= 3 {
		return str
	}

	// Add commas
	var result strings.Builder
	for i, digit := range str {
		if i > 0 && (len(str)-i)%3 == 0 {
			result.WriteString(",")
		}
		result.WriteRune(digit)
	}
	return result.String()
}

// FormatCost formats cost values for display
func FormatCost(cost float64) string {
	return fmt.Sprintf("$%.4f", cost)
}

// CalculateProviderBreakdownStats calculates usage statistics broken down by provider (accumulated totals)
func CalculateProviderBreakdownStats(summaries []ConversationSummary, startTime, endTime time.Time) *ProviderBreakdownStats {
	// Create map to aggregate provider usage
	providerMap := make(map[string]*ProviderUsageStats)
	totalUsage := llmtypes.Usage{}
	totalConversations := 0

	for _, summary := range summaries {
		// Use UpdatedAt as the date for this conversation's usage
		date := summary.GetUpdatedAt().Truncate(24 * time.Hour)

		// Filter by time range if specified
		if !startTime.IsZero() && date.Before(startTime) {
			continue
		}
		if !endTime.IsZero() && date.After(endTime) {
			continue
		}

		provider := summary.GetProvider()

		// Initialize provider usage if not exists
		if _, exists := providerMap[provider]; !exists {
			providerMap[provider] = &ProviderUsageStats{
				Usage:         llmtypes.Usage{},
				Conversations: 0,
			}
		}

		// Add to provider and total usage
		providerStats := providerMap[provider]
		usage := summary.GetUsage()

		// Add to provider stats
		providerStats.Usage.InputTokens += usage.InputTokens
		providerStats.Usage.OutputTokens += usage.OutputTokens
		providerStats.Usage.CacheCreationInputTokens += usage.CacheCreationInputTokens
		providerStats.Usage.CacheReadInputTokens += usage.CacheReadInputTokens
		providerStats.Usage.InputCost += usage.InputCost
		providerStats.Usage.OutputCost += usage.OutputCost
		providerStats.Usage.CacheCreationCost += usage.CacheCreationCost
		providerStats.Usage.CacheReadCost += usage.CacheReadCost
		providerStats.Conversations++

		// Add to total
		totalUsage.InputTokens += usage.InputTokens
		totalUsage.OutputTokens += usage.OutputTokens
		totalUsage.CacheCreationInputTokens += usage.CacheCreationInputTokens
		totalUsage.CacheReadInputTokens += usage.CacheReadInputTokens
		totalUsage.InputCost += usage.InputCost
		totalUsage.OutputCost += usage.OutputCost
		totalUsage.CacheCreationCost += usage.CacheCreationCost
		totalUsage.CacheReadCost += usage.CacheReadCost
		totalConversations++
	}

	return &ProviderBreakdownStats{
		ProviderStats:      providerMap,
		Total:              totalUsage,
		TotalConversations: totalConversations,
	}
}

// CalculateDailyProviderBreakdownStats calculates daily usage statistics broken down by provider
func CalculateDailyProviderBreakdownStats(summaries []ConversationSummary, startTime, endTime time.Time) *DailyProviderBreakdownStats {
	// Create map to aggregate daily usage by date
	dailyMap := make(map[string]*DailyProviderUsage)
	totalUsage := llmtypes.Usage{}
	totalConversations := 0

	for _, summary := range summaries {
		// Use UpdatedAt as the date for this conversation's usage
		date := summary.GetUpdatedAt().Truncate(24 * time.Hour)

		// Filter by time range if specified
		if !startTime.IsZero() && date.Before(startTime) {
			continue
		}
		if !endTime.IsZero() && date.After(endTime) {
			continue
		}

		dateKey := date.Format("2006-01-02")
		provider := summary.GetProvider()

		// Initialize daily usage if not exists
		if _, exists := dailyMap[dateKey]; !exists {
			dailyMap[dateKey] = &DailyProviderUsage{
				Date:               date,
				ProviderUsage:      make(map[string]*ProviderUsageStats),
				TotalUsage:         llmtypes.Usage{},
				TotalConversations: 0,
			}
		}

		daily := dailyMap[dateKey]

		// Initialize provider usage for this day if not exists
		if _, exists := daily.ProviderUsage[provider]; !exists {
			daily.ProviderUsage[provider] = &ProviderUsageStats{
				Usage:         llmtypes.Usage{},
				Conversations: 0,
			}
		}

		// Add to daily provider and daily total usage
		providerStats := daily.ProviderUsage[provider]
		usage := summary.GetUsage()

		// Add to provider stats for this day
		providerStats.Usage.InputTokens += usage.InputTokens
		providerStats.Usage.OutputTokens += usage.OutputTokens
		providerStats.Usage.CacheCreationInputTokens += usage.CacheCreationInputTokens
		providerStats.Usage.CacheReadInputTokens += usage.CacheReadInputTokens
		providerStats.Usage.InputCost += usage.InputCost
		providerStats.Usage.OutputCost += usage.OutputCost
		providerStats.Usage.CacheCreationCost += usage.CacheCreationCost
		providerStats.Usage.CacheReadCost += usage.CacheReadCost
		providerStats.Conversations++

		// Add to daily total
		daily.TotalUsage.InputTokens += usage.InputTokens
		daily.TotalUsage.OutputTokens += usage.OutputTokens
		daily.TotalUsage.CacheCreationInputTokens += usage.CacheCreationInputTokens
		daily.TotalUsage.CacheReadInputTokens += usage.CacheReadInputTokens
		daily.TotalUsage.InputCost += usage.InputCost
		daily.TotalUsage.OutputCost += usage.OutputCost
		daily.TotalUsage.CacheCreationCost += usage.CacheCreationCost
		daily.TotalUsage.CacheReadCost += usage.CacheReadCost
		daily.TotalConversations++

		// Add to overall total
		totalUsage.InputTokens += usage.InputTokens
		totalUsage.OutputTokens += usage.OutputTokens
		totalUsage.CacheCreationInputTokens += usage.CacheCreationInputTokens
		totalUsage.CacheReadInputTokens += usage.CacheReadInputTokens
		totalUsage.InputCost += usage.InputCost
		totalUsage.OutputCost += usage.OutputCost
		totalUsage.CacheCreationCost += usage.CacheCreationCost
		totalUsage.CacheReadCost += usage.CacheReadCost
		totalConversations++
	}

	// Convert map to sorted slice
	var dailyUsage []DailyProviderUsage
	for _, usage := range dailyMap {
		dailyUsage = append(dailyUsage, *usage)
	}

	// Sort by date (newest first)
	for i := 0; i < len(dailyUsage); i++ {
		for j := i + 1; j < len(dailyUsage); j++ {
			if dailyUsage[i].Date.Before(dailyUsage[j].Date) {
				dailyUsage[i], dailyUsage[j] = dailyUsage[j], dailyUsage[i]
			}
		}
	}

	return &DailyProviderBreakdownStats{
		Daily:              dailyUsage,
		Total:              totalUsage,
		TotalConversations: totalConversations,
	}
}

// LogLLMUsage logs structured LLM usage information after request completion
func LogLLMUsage(ctx context.Context, usage llmtypes.Usage, model string, startTime time.Time, requestOutputTokens int) {
	fields := map[string]interface{}{
		"model":                       model,
		"input_tokens":                usage.InputTokens,
		"output_tokens":               usage.OutputTokens,
		"cache_creation_input_tokens": usage.CacheCreationInputTokens,
		"cache_read_input_tokens":     usage.CacheReadInputTokens,
		"input_cost":                  usage.InputCost,
		"output_cost":                 usage.OutputCost,
		"cache_creation_cost":         usage.CacheCreationCost,
		"cache_read_cost":             usage.CacheReadCost,
		"total_cost":                  usage.TotalCost(),
		"total_tokens":                usage.TotalTokens(),
		"current_context_window":      usage.CurrentContextWindow,
		"max_context_window":          usage.MaxContextWindow,
	}

	// Add context window usage ratio if max context window is not zero
	if usage.MaxContextWindow != 0 {
		ratio := float64(usage.CurrentContextWindow) / float64(usage.MaxContextWindow)
		fields["context_window_usage_ratio"] = ratio
	}

	// Calculate output tokens per second using per-request tokens
	duration := time.Since(startTime)
	if duration > 0 && requestOutputTokens > 0 {
		tokensPerSecond := float64(requestOutputTokens) / duration.Seconds()
		fields["output_tokens/s"] = tokensPerSecond
	}

	logger.G(ctx).WithFields(fields).Info("LLM usage completed")
}
