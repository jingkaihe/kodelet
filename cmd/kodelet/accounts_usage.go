package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/jingkaihe/kodelet/pkg/chat"
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

The daemon makes a minimal provider request to retrieve the rate limit headers.

Examples:
  kodelet anthropic accounts usage           # Show usage for default account
  kodelet anthropic accounts usage work      # Show usage for 'work' account
  kodelet anthropic accounts usage --json    # Output in JSON format`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var alias string
		if len(args) > 0 {
			alias = args[0]
		}
		client, err := remoteAdministrationClient(cmd)
		if err != nil {
			return err
		}
		stats, err := client.AnthropicAccountUsage(cmd.Context(), alias)
		if err != nil {
			return err
		}
		jsonOutput, _ := cmd.Flags().GetBool("json")
		if jsonOutput {
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")
			return encoder.Encode(stats)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Account: %s (%s)\n", stats.Account, stats.Email)
		for _, window := range []struct {
			title string
			usage chat.AnthropicUsageWindow
		}{{"5-Hour Window", stats.Window5h}, {"7-Day Window", stats.Window7d}} {
			fmt.Fprintf(cmd.OutOrStdout(), "\n%s\n  Status: %s\n  Utilization: %.2f%%\n  Resets: %s\n", window.title, formatStatus(window.usage.Status), window.usage.Utilization*100, formatResetTime(time.Unix(window.usage.ResetUnix, 0)))
		}
		return nil
	},
}

func init() {
	anthropicAccountsUsageCmd.Flags().Bool("json", false, "Output in JSON format")
	anthropicAccountsCmd.AddCommand(anthropicAccountsUsageCmd)
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
