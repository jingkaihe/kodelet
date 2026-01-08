package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/llm/anthropic"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/spf13/cobra"
)

var anthropicAccountsUsageCmd = &cobra.Command{
	Use:   "usage [alias]",
	Short: "Show rate limit usage for an Anthropic subscription account",
	Long: `Display the current rate limit utilization for an Anthropic subscription account.

Shows the 5-hour and 7-day usage windows including:
- Current status (allowed/limited)
- Utilization percentage
- Reset time

This command makes a minimal API request to retrieve the rate limit headers.

Examples:
  kodelet anthropic accounts usage           # Show usage for default account
  kodelet anthropic accounts usage work      # Show usage for 'work' account`,
	Args: cobra.MaximumNArgs(1),
	Run: func(_ *cobra.Command, args []string) {
		var alias string
		if len(args) > 0 {
			alias = args[0]
		}
		showAccountUsageCmd(alias)
	},
}

func init() {
	anthropicAccountsCmd.AddCommand(anthropicAccountsUsageCmd)
}

func showAccountUsageCmd(alias string) {
	// Resolve the account alias if not provided
	accountName := alias
	if accountName == "" {
		defaultAlias, err := auth.GetDefaultAnthropicAccount()
		if err != nil {
			presenter.Error(err, "No default account found")
			presenter.Info("Use 'kodelet anthropic login' to add an account or specify an account alias.")
			os.Exit(1)
		}
		accountName = defaultAlias
	}

	// Verify account exists
	accounts, err := auth.ListAnthropicAccounts()
	if err != nil {
		presenter.Error(err, "Failed to list accounts")
		os.Exit(1)
	}

	var accountExists bool
	var accountEmail string
	for _, account := range accounts {
		if account.Alias == accountName {
			accountExists = true
			accountEmail = account.Email
			break
		}
	}

	if !accountExists {
		presenter.Error(fmt.Errorf("account '%s' not found", accountName), "Invalid account")
		presenter.Info("Use 'kodelet anthropic accounts list' to see available accounts.")
		os.Exit(1)
	}

	presenter.Info(fmt.Sprintf("Fetching usage for account: %s (%s)...", accountName, accountEmail))

	ctx := context.Background()
	stats, err := anthropic.GetRateLimitStats(ctx, accountName)
	if err != nil {
		presenter.Error(err, "Failed to fetch rate limit stats")
		os.Exit(1)
	}

	fmt.Println()
	presenter.Section("5-Hour Window")
	fmt.Printf("  Status:      %s\n", formatStatus(stats.Status5h))
	fmt.Printf("  Utilization: %.2f%%\n", stats.Utilization5h*100)
	fmt.Printf("  Resets:      %s\n", formatResetTime(stats.Reset5h))

	fmt.Println()
	presenter.Section("7-Day Window")
	fmt.Printf("  Status:      %s\n", formatStatus(stats.Status7d))
	fmt.Printf("  Utilization: %.2f%%\n", stats.Utilization7d*100)
	fmt.Printf("  Resets:      %s\n", formatResetTime(stats.Reset7d))
}

func formatStatus(status string) string {
	switch status {
	case "allowed":
		return "✓ allowed"
	case "limited":
		return "⚠ limited"
	default:
		return status
	}
}

func formatResetTime(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	remaining := time.Until(t)
	if remaining < 0 {
		return t.Local().Format("2006-01-02 15:04:05") + " (passed)"
	}

	days := int(remaining.Hours()) / 24
	hours := int(remaining.Hours()) % 24
	minutes := int(remaining.Minutes()) % 60

	var duration string
	if days > 0 {
		duration = fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	} else {
		duration = fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%s (in %s)", t.Local().Format("2006-01-02 15:04:05"), duration)
}
