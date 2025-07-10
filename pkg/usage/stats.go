package usage

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// ConversationSummary represents the interface for conversation summary data needed for usage calculations
type ConversationSummary interface {
	GetID() string
	GetCreatedAt() time.Time
	GetUpdatedAt() time.Time
	GetMessageCount() int
	GetUsage() llmtypes.Usage
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
