// Package usage provides functionality for tracking and calculating usage
// statistics for LLM conversations including token counts, conversation
// metrics, and time-based analytics for monitoring system performance.
package usage

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/logger"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// ConversationSummary provides access to conversation metadata and usage statistics
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

// UsageStats represents aggregated usage statistics with daily breakdown and totals
type UsageStats struct {
	Daily []DailyUsage
	Total llmtypes.Usage
}

// ProviderUsageStats represents usage statistics for a single provider
type ProviderUsageStats struct {
	Usage         llmtypes.Usage
	Conversations int
}

// ProviderBreakdownStats represents aggregated usage statistics broken down by provider
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

// DailyProviderBreakdownStats represents usage statistics with daily breakdown and provider breakdown
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

// CalculateUsageStats calculates aggregated usage statistics from conversation summaries
// within the specified time range, with daily breakdown sorted newest first
func CalculateUsageStats(summaries []ConversationSummary, startTime, endTime time.Time) *UsageStats {
	dailyMap := make(map[string]*DailyUsage)
	totalUsage := llmtypes.Usage{}

	for _, summary := range summaries {
		// Use UpdatedAt as the date for this conversation's usage
		date := summary.GetUpdatedAt().Truncate(24 * time.Hour)

		if !startTime.IsZero() && date.Before(startTime) {
			continue
		}
		if !endTime.IsZero() && date.After(endTime) {
			continue
		}

		dateKey := date.Format("2006-01-02")

		if _, exists := dailyMap[dateKey]; !exists {
			dailyMap[dateKey] = &DailyUsage{
				Date:  date,
				Usage: llmtypes.Usage{},
			}
		}

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

		totalUsage.InputTokens += usage.InputTokens
		totalUsage.OutputTokens += usage.OutputTokens
		totalUsage.CacheCreationInputTokens += usage.CacheCreationInputTokens
		totalUsage.CacheReadInputTokens += usage.CacheReadInputTokens
		totalUsage.InputCost += usage.InputCost
		totalUsage.OutputCost += usage.OutputCost
		totalUsage.CacheCreationCost += usage.CacheCreationCost
		totalUsage.CacheReadCost += usage.CacheReadCost
	}

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

	for _, summary := range summaries {
		stats.TotalMessages += summary.GetMessageCount()

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

	var result strings.Builder
	for i, digit := range str {
		if i > 0 && (len(str)-i)%3 == 0 {
			result.WriteString(",")
		}
		result.WriteRune(digit)
	}
	return result.String()
}

// FormatCost formats a cost value as a currency string with 4 decimal places
func FormatCost(cost float64) string {
	return fmt.Sprintf("$%.4f", cost)
}

func roundToFourDecimalPlaces(value float64) float64 {
	return math.Round(value*10000) / 10000
}

// CalculateProviderBreakdownStats calculates usage statistics broken down by provider (accumulated totals)
func CalculateProviderBreakdownStats(summaries []ConversationSummary, startTime, endTime time.Time) *ProviderBreakdownStats {
	providerMap := make(map[string]*ProviderUsageStats)
	totalUsage := llmtypes.Usage{}
	totalConversations := 0

	for _, summary := range summaries {
		// Use UpdatedAt as the date for this conversation's usage
		date := summary.GetUpdatedAt().Truncate(24 * time.Hour)

		if !startTime.IsZero() && date.Before(startTime) {
			continue
		}
		if !endTime.IsZero() && date.After(endTime) {
			continue
		}

		provider := summary.GetProvider()

		if _, exists := providerMap[provider]; !exists {
			providerMap[provider] = &ProviderUsageStats{
				Usage:         llmtypes.Usage{},
				Conversations: 0,
			}
		}

		providerStats := providerMap[provider]
		usage := summary.GetUsage()

		providerStats.Usage.InputTokens += usage.InputTokens
		providerStats.Usage.OutputTokens += usage.OutputTokens
		providerStats.Usage.CacheCreationInputTokens += usage.CacheCreationInputTokens
		providerStats.Usage.CacheReadInputTokens += usage.CacheReadInputTokens
		providerStats.Usage.InputCost += usage.InputCost
		providerStats.Usage.OutputCost += usage.OutputCost
		providerStats.Usage.CacheCreationCost += usage.CacheCreationCost
		providerStats.Usage.CacheReadCost += usage.CacheReadCost
		providerStats.Conversations++

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

// CalculateDailyProviderBreakdownStats calculates usage statistics with both daily and provider breakdown
// within the specified time range, sorted newest first
func CalculateDailyProviderBreakdownStats(summaries []ConversationSummary, startTime, endTime time.Time) *DailyProviderBreakdownStats {
	dailyMap := make(map[string]*DailyProviderUsage)
	totalUsage := llmtypes.Usage{}
	totalConversations := 0

	for _, summary := range summaries {
		// Use UpdatedAt as the date for this conversation's usage
		date := summary.GetUpdatedAt().Truncate(24 * time.Hour)

		if !startTime.IsZero() && date.Before(startTime) {
			continue
		}
		if !endTime.IsZero() && date.After(endTime) {
			continue
		}

		dateKey := date.Format("2006-01-02")
		provider := summary.GetProvider()

		if _, exists := dailyMap[dateKey]; !exists {
			dailyMap[dateKey] = &DailyProviderUsage{
				Date:               date,
				ProviderUsage:      make(map[string]*ProviderUsageStats),
				TotalUsage:         llmtypes.Usage{},
				TotalConversations: 0,
			}
		}

		daily := dailyMap[dateKey]

		if _, exists := daily.ProviderUsage[provider]; !exists {
			daily.ProviderUsage[provider] = &ProviderUsageStats{
				Usage:         llmtypes.Usage{},
				Conversations: 0,
			}
		}

		providerStats := daily.ProviderUsage[provider]
		usage := summary.GetUsage()

		providerStats.Usage.InputTokens += usage.InputTokens
		providerStats.Usage.OutputTokens += usage.OutputTokens
		providerStats.Usage.CacheCreationInputTokens += usage.CacheCreationInputTokens
		providerStats.Usage.CacheReadInputTokens += usage.CacheReadInputTokens
		providerStats.Usage.InputCost += usage.InputCost
		providerStats.Usage.OutputCost += usage.OutputCost
		providerStats.Usage.CacheCreationCost += usage.CacheCreationCost
		providerStats.Usage.CacheReadCost += usage.CacheReadCost
		providerStats.Conversations++

		daily.TotalUsage.InputTokens += usage.InputTokens
		daily.TotalUsage.OutputTokens += usage.OutputTokens
		daily.TotalUsage.CacheCreationInputTokens += usage.CacheCreationInputTokens
		daily.TotalUsage.CacheReadInputTokens += usage.CacheReadInputTokens
		daily.TotalUsage.InputCost += usage.InputCost
		daily.TotalUsage.OutputCost += usage.OutputCost
		daily.TotalUsage.CacheCreationCost += usage.CacheCreationCost
		daily.TotalUsage.CacheReadCost += usage.CacheReadCost
		daily.TotalConversations++

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

// LogLLMUsage logs detailed LLM usage statistics including tokens, costs, and performance metrics
func LogLLMUsage(ctx context.Context, usage llmtypes.Usage, model string, startTime time.Time, requestOutputTokens int) {
	fields := map[string]any{
		"model":                       model,
		"input_tokens":                usage.InputTokens,
		"output_tokens":               usage.OutputTokens,
		"cache_creation_input_tokens": usage.CacheCreationInputTokens,
		"cache_read_input_tokens":     usage.CacheReadInputTokens,
		"input_cost":                  roundToFourDecimalPlaces(usage.InputCost),
		"output_cost":                 roundToFourDecimalPlaces(usage.OutputCost),
		"cache_creation_cost":         roundToFourDecimalPlaces(usage.CacheCreationCost),
		"cache_read_cost":             roundToFourDecimalPlaces(usage.CacheReadCost),
		"total_cost":                  roundToFourDecimalPlaces(usage.TotalCost()),
		"total_tokens":                usage.TotalTokens(),
		"current_context_window":      usage.CurrentContextWindow,
		"max_context_window":          usage.MaxContextWindow,
	}

	if usage.MaxContextWindow != 0 {
		ratio := float64(usage.CurrentContextWindow) / float64(usage.MaxContextWindow)
		fields["context_window_usage_ratio"] = roundToFourDecimalPlaces(ratio)
	}

	// Calculate output tokens per second using per-request tokens
	duration := time.Since(startTime)
	if duration > 0 && requestOutputTokens > 0 {
		tokensPerSecond := float64(requestOutputTokens) / duration.Seconds()
		fields["output_tokens/s"] = roundToFourDecimalPlaces(tokensPerSecond)
	}

	logger.G(ctx).WithFields(fields).Info("LLM usage completed")
}
