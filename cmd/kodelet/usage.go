package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/jingkaihe/kodelet/pkg/usage"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

// UsageConfig holds configuration for the usage command
type UsageConfig struct {
	Since     string
	Until     string
	Format    string
	Provider  string
	Breakdown bool
}

// NewUsageConfig creates a new UsageConfig with default values
func NewUsageConfig() *UsageConfig {
	return &UsageConfig{
		Since:     "10d", // Default to past 10 days
		Until:     "",
		Format:    "table",
		Provider:  "",
		Breakdown: false,
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
  kodelet usage --provider anthropic        # Filter by Anthropic/Claude
  kodelet usage --provider openai           # Filter by OpenAI
  kodelet usage --breakdown                  # Show breakdown by provider
  kodelet usage --breakdown --since 1w      # Provider breakdown for past week
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
	usageCmd.Flags().String("provider", defaults.Provider, "Filter usage by LLM provider (anthropic or openai)")
	usageCmd.Flags().Bool("breakdown", defaults.Breakdown, "Show usage breakdown by provider")
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
	if provider, err := cmd.Flags().GetString("provider"); err == nil {
		config.Provider = provider
	}
	if breakdown, err := cmd.Flags().GetBool("breakdown"); err == nil {
		config.Breakdown = breakdown
	}

	return config
}

// toUsageSummaries converts ConversationSummary slice to usage.ConversationSummary interface slice
func toUsageSummaries(summaries []convtypes.ConversationSummary) []usage.ConversationSummary {
	result := make([]usage.ConversationSummary, len(summaries))
	for i, s := range summaries {
		result[i] = s
	}
	return result
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
		return time.Time{}, errors.New(fmt.Sprintf("invalid time specification: %s (expected format: YYYY-MM-DD, 1d, 1w, etc.)", spec))
	}

	amount, err := strconv.Atoi(matches[1])
	if err != nil {
		return time.Time{}, errors.New(fmt.Sprintf("invalid number in time specification: %s", matches[1]))
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
		return time.Time{}, errors.New(fmt.Sprintf("invalid time unit: %s (supported: d, h, w)", unit))
	}
}

// Use types from usage package
type DailyUsage = usage.DailyUsage
type UsageStats = usage.UsageStats

// runUsageCmd executes the usage command
func runUsageCmd(ctx context.Context, config *UsageConfig) {
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
	store, err := conversations.GetConversationStore(ctx)
	if err != nil {
		presenter.Error(err, "Failed to initialize conversation store")
		os.Exit(1)
	}
	defer store.Close()

	// Query conversations with date filters
	options := convtypes.QueryOptions{
		SortBy:    "updated",
		SortOrder: "desc",
		Provider:  config.Provider, // Add provider filtering
	}

	if !startTime.IsZero() {
		options.StartDate = &startTime
	}
	if !endTime.IsZero() {
		options.EndDate = &endTime
	}

	result, err := store.Query(ctx, options)
	if err != nil {
		presenter.Error(err, "Failed to query conversations")
		os.Exit(1)
	}

	summaries := result.ConversationSummaries

	if len(summaries) == 0 {
		presenter.Info("No conversations found in the specified time range.")
		return
	}

	// Calculate usage statistics directly from summaries
	if config.Breakdown {
		// Calculate daily provider breakdown statistics
		dailyProviderStats := usage.CalculateDailyProviderBreakdownStats(toUsageSummaries(summaries), startTime, endTime)

		// Display results with daily provider breakdown
		if config.Format == "json" {
			displayDailyProviderBreakdownJSON(os.Stdout, dailyProviderStats)
		} else {
			displayDailyProviderBreakdownTable(os.Stdout, dailyProviderStats)
		}
	} else {
		// Calculate regular daily usage statistics
		stats := usage.CalculateUsageStats(toUsageSummaries(summaries), startTime, endTime)

		// Display results
		if config.Format == "json" {
			displayUsageJSON(os.Stdout, stats)
		} else {
			displayUsageTable(os.Stdout, stats)
		}
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
			usage.FormatNumber(daily.Usage.InputTokens),
			usage.FormatNumber(daily.Usage.OutputTokens),
			usage.FormatNumber(daily.Usage.CacheCreationInputTokens),
			usage.FormatNumber(daily.Usage.CacheReadInputTokens),
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
		usage.FormatNumber(stats.Total.InputTokens),
		usage.FormatNumber(stats.Total.OutputTokens),
		usage.FormatNumber(stats.Total.CacheCreationInputTokens),
		usage.FormatNumber(stats.Total.CacheReadInputTokens),
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

// formatNumber is a wrapper around usage.FormatNumber for testing
func formatNumber(n int) string {
	return usage.FormatNumber(n)
}

// aggregateUsageStats is a wrapper around usage.CalculateUsageStats for testing
func aggregateUsageStats(summaries []convtypes.ConversationSummary, startTime, endTime time.Time) *UsageStats {
	return usage.CalculateUsageStats(toUsageSummaries(summaries), startTime, endTime)
}

// displayDailyProviderBreakdownTable displays daily provider breakdown statistics in table format
func displayDailyProviderBreakdownTable(w io.Writer, stats *usage.DailyProviderBreakdownStats) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	// Print header (similar to regular usage table but with Provider column)
	fmt.Fprintln(tw, "Date\tProvider\tConversations\tInput Tokens\tOutput Tokens\tCache Write\tCache Read\tTotal Cost")
	fmt.Fprintln(tw, "----\t--------\t-------------\t------------\t-------------\t-----------\t----------\t----------")

	// Print daily breakdown - one row per provider per day
	for _, daily := range stats.Daily {
		// Sort providers for consistent display (Claude first, then OpenAI)
		providers := []string{}
		if _, exists := daily.ProviderUsage["anthropic"]; exists {
			providers = append(providers, "anthropic")
		}
		if _, exists := daily.ProviderUsage["openai"]; exists {
			providers = append(providers, "openai")
		}
		// Add any other providers
		for provider := range daily.ProviderUsage {
			if provider != "anthropic" && provider != "openai" {
				providers = append(providers, provider)
			}
		}

		for _, provider := range providers {
			providerStat := daily.ProviderUsage[provider]

			// Convert provider name to friendly display name
			displayName := provider
			switch provider {
			case "anthropic":
				displayName = "Anthropic"
			case "openai":
				displayName = "OpenAI"
			}

			fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\t%s\t%s\t$%.4f\n",
				daily.Date.Format("2006-01-02"),
				displayName,
				providerStat.Conversations,
				usage.FormatNumber(providerStat.Usage.InputTokens),
				usage.FormatNumber(providerStat.Usage.OutputTokens),
				usage.FormatNumber(providerStat.Usage.CacheCreationInputTokens),
				usage.FormatNumber(providerStat.Usage.CacheReadInputTokens),
				providerStat.Usage.TotalCost(),
			)
		}
	}

	// Print separator and total if there are multiple days
	if len(stats.Daily) > 1 {
		fmt.Fprintln(tw, "----\t--------\t-------------\t------------\t-------------\t-----------\t----------\t----------")

		// Calculate totals by provider across all days
		providerTotals := make(map[string]*usage.ProviderUsageStats)
		for _, daily := range stats.Daily {
			for provider, providerStat := range daily.ProviderUsage {
				if _, exists := providerTotals[provider]; !exists {
					providerTotals[provider] = &usage.ProviderUsageStats{
						Usage:         llmtypes.Usage{},
						Conversations: 0,
					}
				}
				total := providerTotals[provider]
				total.Conversations += providerStat.Conversations
				total.Usage.InputTokens += providerStat.Usage.InputTokens
				total.Usage.OutputTokens += providerStat.Usage.OutputTokens
				total.Usage.CacheCreationInputTokens += providerStat.Usage.CacheCreationInputTokens
				total.Usage.CacheReadInputTokens += providerStat.Usage.CacheReadInputTokens
				total.Usage.InputCost += providerStat.Usage.InputCost
				total.Usage.OutputCost += providerStat.Usage.OutputCost
				total.Usage.CacheCreationCost += providerStat.Usage.CacheCreationCost
				total.Usage.CacheReadCost += providerStat.Usage.CacheReadCost
			}
		}

		// Sort providers for totals (Claude first, then OpenAI)
		providers := []string{}
		if _, exists := providerTotals["anthropic"]; exists {
			providers = append(providers, "anthropic")
		}
		if _, exists := providerTotals["openai"]; exists {
			providers = append(providers, "openai")
		}
		// Add any other providers
		for provider := range providerTotals {
			if provider != "anthropic" && provider != "openai" {
				providers = append(providers, provider)
			}
		}

		// Print provider totals
		for _, provider := range providers {
			total := providerTotals[provider]

			// Convert provider name to friendly display name
			displayName := provider
			switch provider {
			case "anthropic":
				displayName = "Anthropic"
			case "openai":
				displayName = "OpenAI"
			}

			fmt.Fprintf(tw, "TOTAL\t%s\t%d\t%s\t%s\t%s\t%s\t$%.4f\n",
				displayName,
				total.Conversations,
				usage.FormatNumber(total.Usage.InputTokens),
				usage.FormatNumber(total.Usage.OutputTokens),
				usage.FormatNumber(total.Usage.CacheCreationInputTokens),
				usage.FormatNumber(total.Usage.CacheReadInputTokens),
				total.Usage.TotalCost(),
			)
		}
	}

	tw.Flush()
}

// DailyProviderBreakdownJSONOutput represents the JSON structure for daily provider breakdown statistics
type DailyProviderBreakdownJSONOutput struct {
	Daily []DailyProviderUsageJSON `json:"daily"`
	Total TotalUsageJSON           `json:"total"`
}

// DailyProviderUsageJSON represents daily provider usage in JSON format
type DailyProviderUsageJSON struct {
	Date      string                       `json:"date"`
	Providers map[string]ProviderUsageJSON `json:"providers"`
	Total     DailyTotalUsageJSON          `json:"total"`
}

// DailyTotalUsageJSON represents daily total usage in JSON format
type DailyTotalUsageJSON struct {
	Conversations int     `json:"conversations"`
	TotalCost     float64 `json:"total_cost"`
}

// ProviderUsageJSON represents provider usage in JSON format
type ProviderUsageJSON struct {
	Conversations    int     `json:"conversations"`
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	CacheWriteTokens int     `json:"cache_write_tokens"`
	CacheReadTokens  int     `json:"cache_read_tokens"`
	TotalCost        float64 `json:"total_cost"`
}

// displayDailyProviderBreakdownJSON displays daily provider breakdown statistics in JSON format
func displayDailyProviderBreakdownJSON(w io.Writer, stats *usage.DailyProviderBreakdownStats) {
	// Convert to JSON-friendly structure
	output := DailyProviderBreakdownJSONOutput{
		Daily: make([]DailyProviderUsageJSON, len(stats.Daily)),
	}

	// Convert daily usage
	for i, daily := range stats.Daily {
		providers := make(map[string]ProviderUsageJSON)

		for provider, providerStat := range daily.ProviderUsage {
			// Use friendly display names in JSON
			displayName := provider
			switch provider {
			case "anthropic":
				displayName = "Anthropic"
			case "openai":
				displayName = "OpenAI"
			}

			providers[displayName] = ProviderUsageJSON{
				Conversations:    providerStat.Conversations,
				InputTokens:      providerStat.Usage.InputTokens,
				OutputTokens:     providerStat.Usage.OutputTokens,
				CacheWriteTokens: providerStat.Usage.CacheCreationInputTokens,
				CacheReadTokens:  providerStat.Usage.CacheReadInputTokens,
				TotalCost:        providerStat.Usage.TotalCost(),
			}
		}

		output.Daily[i] = DailyProviderUsageJSON{
			Date:      daily.Date.Format("2006-01-02"),
			Providers: providers,
			Total: DailyTotalUsageJSON{
				Conversations: daily.TotalConversations,
				TotalCost:     daily.TotalUsage.TotalCost(),
			},
		}
	}

	// Convert total usage
	output.Total = TotalUsageJSON{
		Conversations:    stats.TotalConversations,
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
