package usage

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// ConversationRecord represents the minimal data needed for usage calculation
type ConversationRecord struct {
	ID           string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	MessageCount int
	Usage        llmtypes.Usage
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

// CalculateUsageStats calculates usage statistics from a list of conversation records
func CalculateUsageStats(records []ConversationRecord, startTime, endTime time.Time) *UsageStats {
	// Create map to aggregate daily usage
	dailyMap := make(map[string]*DailyUsage)
	totalUsage := llmtypes.Usage{}

	for _, record := range records {
		// Use UpdatedAt as the date for this conversation's usage
		date := record.UpdatedAt.Truncate(24 * time.Hour)

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
		daily.Usage.InputTokens += record.Usage.InputTokens
		daily.Usage.OutputTokens += record.Usage.OutputTokens
		daily.Usage.CacheCreationInputTokens += record.Usage.CacheCreationInputTokens
		daily.Usage.CacheReadInputTokens += record.Usage.CacheReadInputTokens
		daily.Usage.InputCost += record.Usage.InputCost
		daily.Usage.OutputCost += record.Usage.OutputCost
		daily.Usage.CacheCreationCost += record.Usage.CacheCreationCost
		daily.Usage.CacheReadCost += record.Usage.CacheReadCost
		daily.Conversations++

		// Add to total
		totalUsage.InputTokens += record.Usage.InputTokens
		totalUsage.OutputTokens += record.Usage.OutputTokens
		totalUsage.CacheCreationInputTokens += record.Usage.CacheCreationInputTokens
		totalUsage.CacheReadInputTokens += record.Usage.CacheReadInputTokens
		totalUsage.InputCost += record.Usage.InputCost
		totalUsage.OutputCost += record.Usage.OutputCost
		totalUsage.CacheCreationCost += record.Usage.CacheCreationCost
		totalUsage.CacheReadCost += record.Usage.CacheReadCost
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
func CalculateConversationUsageStats(records []ConversationRecord) *ConversationUsageStats {
	stats := &ConversationUsageStats{
		TotalConversations: len(records),
	}

	if len(records) == 0 {
		return stats
	}

	// Calculate totals
	for _, record := range records {
		// Count messages
		stats.TotalMessages += record.MessageCount

		// Sum up usage
		stats.InputTokens += record.Usage.InputTokens
		stats.OutputTokens += record.Usage.OutputTokens
		stats.CacheReadTokens += record.Usage.CacheReadInputTokens
		stats.CacheWriteTokens += record.Usage.CacheCreationInputTokens
		stats.InputCost += record.Usage.InputCost
		stats.OutputCost += record.Usage.OutputCost
		stats.CacheReadCost += record.Usage.CacheReadCost
		stats.CacheWriteCost += record.Usage.CacheCreationCost
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
