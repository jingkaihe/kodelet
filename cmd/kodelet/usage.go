package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/spf13/cobra"
)

// UsageConfig holds configuration for the usage command
type UsageConfig struct {
	Since  string
	Until  string
	Format string
}

// NewUsageConfig creates a new UsageConfig with default values
func NewUsageConfig() *UsageConfig {
	return &UsageConfig{
		Since:  "10d", // Default to past 10 days
		Until:  "",
		Format: "table",
	}
}

var usageCmd = &cobra.Command{
	Use:   "usage",
	Short: "Show token usage statistics",
	Long: `Show token usage statistics including input tokens, output tokens, cache tokens, and costs.

By default shows usage for the past 10 days, broken down by day.

Examples:
  kodelet usage                              # Past 10 days
  kodelet usage --since 2025-06-01          # Since specific date
  kodelet usage --since 1d                  # Since 1 day ago
  kodelet usage --since 1w                  # Since 1 week ago
  kodelet usage --since 1w --until 2025-06-01  # Date range
`,
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		config := getUsageConfigFromFlags(cmd)
		runUsageCmd(ctx, config)
	},
}

func init() {
	defaults := NewUsageConfig()
	usageCmd.Flags().String("since", defaults.Since, "Show usage since this time (e.g., 2025-06-01, 1d, 1w)")
	usageCmd.Flags().String("until", defaults.Until, "Show usage until this time (e.g., 2025-06-01)")
	usageCmd.Flags().String("format", defaults.Format, "Output format: table or json")
}

// getUsageConfigFromFlags extracts usage configuration from command flags
func getUsageConfigFromFlags(cmd *cobra.Command) *UsageConfig {
	config := NewUsageConfig()

	if since, err := cmd.Flags().GetString("since"); err == nil {
		config.Since = since
	}
	if until, err := cmd.Flags().GetString("until"); err == nil {
		config.Until = until
	}
	if format, err := cmd.Flags().GetString("format"); err == nil {
		config.Format = format
	}

	return config
}

// parseTimeSpec parses time specifications like "1d", "1w", "2025-06-01"
func parseTimeSpec(spec string) (time.Time, error) {
	return parseTimeSpecWithClock(spec, time.Now)
}

// parseTimeSpecWithClock parses time specifications with a custom clock function for testing
func parseTimeSpecWithClock(spec string, now func() time.Time) (time.Time, error) {
	if spec == "" {
		return time.Time{}, nil
	}

	// Try parsing as absolute date first (YYYY-MM-DD)
	if t, err := time.Parse("2006-01-02", spec); err == nil {
		return t, nil
	}

	// Try parsing as relative time (1d, 1w, etc.)
	re := regexp.MustCompile(`^(\d+)([dhw])$`)
	matches := re.FindStringSubmatch(spec)
	if len(matches) != 3 {
		return time.Time{}, fmt.Errorf("invalid time specification: %s (expected format: YYYY-MM-DD, 1d, 1w, etc.)", spec)
	}

	amount, err := strconv.Atoi(matches[1])
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid number in time specification: %s", matches[1])
	}

	unit := matches[2]
	currentTime := now()

	switch unit {
	case "d":
		return currentTime.AddDate(0, 0, -amount), nil
	case "h":
		return currentTime.Add(-time.Duration(amount) * time.Hour), nil
	case "w":
		return currentTime.AddDate(0, 0, -amount*7), nil
	default:
		return time.Time{}, fmt.Errorf("invalid time unit: %s (supported: d, h, w)", unit)
	}
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

// runUsageCmd executes the usage command
func runUsageCmd(_ context.Context, config *UsageConfig) {
	// Parse time specifications
	var startTime, endTime time.Time
	var err error

	if config.Since != "" {
		startTime, err = parseTimeSpec(config.Since)
		if err != nil {
			presenter.Error(err, "Invalid since time specification")
			os.Exit(1)
		}
		// Set to beginning of day
		startTime = startTime.Truncate(24 * time.Hour)
	}

	if config.Until != "" {
		endTime, err = parseTimeSpec(config.Until)
		if err != nil {
			presenter.Error(err, "Invalid until time specification")
			os.Exit(1)
		}
		// Set to end of day
		endTime = endTime.Truncate(24 * time.Hour).Add(24*time.Hour - time.Second)
	}

	// Create conversation store
	store, err := conversations.GetConversationStore()
	if err != nil {
		presenter.Error(err, "Failed to initialize conversation store")
		os.Exit(1)
	}
	defer store.Close()

	// Query conversations with date filters
	options := conversations.QueryOptions{
		SortBy:    "updated",
		SortOrder: "desc",
	}

	if !startTime.IsZero() {
		options.StartDate = &startTime
	}
	if !endTime.IsZero() {
		options.EndDate = &endTime
	}

	summaries, err := store.Query(options)
	if err != nil {
		presenter.Error(err, "Failed to query conversations")
		os.Exit(1)
	}

	if len(summaries) == 0 {
		presenter.Info("No conversations found in the specified time range.")
		return
	}

	// Load full conversation records to get usage data
	var records []conversations.ConversationRecord
	for _, summary := range summaries {
		record, err := store.Load(summary.ID)
		if err != nil {
			// Skip records that can't be loaded but log the error
			fmt.Fprintf(os.Stderr, "Warning: failed to load conversation %s: %v\n", summary.ID, err)
			continue
		}
		records = append(records, record)
	}

	// Aggregate usage statistics
	stats := aggregateUsageStats(records, startTime, endTime)

	// Display results
	if config.Format == "json" {
		displayUsageJSON(os.Stdout, stats)
	} else {
		displayUsageTable(os.Stdout, stats)
	}
}

// aggregateUsageStats aggregates usage statistics from conversation records
func aggregateUsageStats(records []conversations.ConversationRecord, startTime, endTime time.Time) *UsageStats {
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
	sort.Slice(dailyUsage, func(i, j int) bool {
		return dailyUsage[i].Date.After(dailyUsage[j].Date)
	})

	return &UsageStats{
		Daily: dailyUsage,
		Total: totalUsage,
	}
}

// displayUsageTable displays usage statistics in table format
func displayUsageTable(w io.Writer, stats *UsageStats) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	// Print header
	fmt.Fprintln(tw, "Date\tConversations\tInput Tokens\tOutput Tokens\tCache Write\tCache Read\tTotal Cost")
	fmt.Fprintln(tw, "----\t-------------\t------------\t-------------\t-----------\t----------\t----------")

	// Print daily breakdown
	for _, daily := range stats.Daily {
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%s\t$%.4f\n",
			daily.Date.Format("2006-01-02"),
			daily.Conversations,
			formatNumber(daily.Usage.InputTokens),
			formatNumber(daily.Usage.OutputTokens),
			formatNumber(daily.Usage.CacheCreationInputTokens),
			formatNumber(daily.Usage.CacheReadInputTokens),
			daily.Usage.TotalCost(),
		)
	}

	// Print separator and total
	fmt.Fprintln(tw, "----\t-------------\t------------\t-------------\t-----------\t----------\t----------")
	totalConversations := 0
	for _, daily := range stats.Daily {
		totalConversations += daily.Conversations
	}

	fmt.Fprintf(tw, "TOTAL\t%d\t%s\t%s\t%s\t%s\t$%.4f\n",
		totalConversations,
		formatNumber(stats.Total.InputTokens),
		formatNumber(stats.Total.OutputTokens),
		formatNumber(stats.Total.CacheCreationInputTokens),
		formatNumber(stats.Total.CacheReadInputTokens),
		stats.Total.TotalCost(),
	)

	tw.Flush()
}

// UsageJSONOutput represents the JSON structure for usage statistics
type UsageJSONOutput struct {
	Daily []DailyUsageJSON `json:"daily"`
	Total TotalUsageJSON   `json:"total"`
}

// DailyUsageJSON represents daily usage in JSON format
type DailyUsageJSON struct {
	Date             string  `json:"date"`
	Conversations    int     `json:"conversations"`
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	CacheWriteTokens int     `json:"cache_write_tokens"`
	CacheReadTokens  int     `json:"cache_read_tokens"`
	TotalCost        float64 `json:"total_cost"`
}

// TotalUsageJSON represents total usage in JSON format
type TotalUsageJSON struct {
	Conversations    int     `json:"conversations"`
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	CacheWriteTokens int     `json:"cache_write_tokens"`
	CacheReadTokens  int     `json:"cache_read_tokens"`
	TotalCost        float64 `json:"total_cost"`
}

// displayUsageJSON displays usage statistics in JSON format
func displayUsageJSON(w io.Writer, stats *UsageStats) {
	// Convert to JSON-friendly structure
	output := UsageJSONOutput{
		Daily: make([]DailyUsageJSON, len(stats.Daily)),
	}

	// Convert daily usage
	for i, daily := range stats.Daily {
		output.Daily[i] = DailyUsageJSON{
			Date:             daily.Date.Format("2006-01-02"),
			Conversations:    daily.Conversations,
			InputTokens:      daily.Usage.InputTokens,
			OutputTokens:     daily.Usage.OutputTokens,
			CacheWriteTokens: daily.Usage.CacheCreationInputTokens,
			CacheReadTokens:  daily.Usage.CacheReadInputTokens,
			TotalCost:        daily.Usage.TotalCost(),
		}
	}

	// Calculate total conversations
	totalConversations := 0
	for _, daily := range stats.Daily {
		totalConversations += daily.Conversations
	}

	// Convert total usage
	output.Total = TotalUsageJSON{
		Conversations:    totalConversations,
		InputTokens:      stats.Total.InputTokens,
		OutputTokens:     stats.Total.OutputTokens,
		CacheWriteTokens: stats.Total.CacheCreationInputTokens,
		CacheReadTokens:  stats.Total.CacheReadInputTokens,
		TotalCost:        stats.Total.TotalCost(),
	}

	// Marshal to JSON with indentation
	jsonData, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		fmt.Fprintf(w, "Error generating JSON output: %v\n", err)
		return
	}

	fmt.Fprintln(w, string(jsonData))
}

// formatNumber formats large numbers with commas for readability
func formatNumber(n int) string {
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
