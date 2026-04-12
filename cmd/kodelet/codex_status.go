package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/spf13/cobra"
)

var codexStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show Codex authentication status",
	Long: `Show the current OpenAI Codex authentication status.

This command checks if valid Codex credentials are available at ~/.kodelet/codex-credentials.json.
These credentials are created by running 'kodelet codex login'.

If credentials are found, Kodelet also fetches the live ChatGPT-backed Codex
usage snapshot when OAuth authentication is available, including rolling windows
and workspace credits.`,
	Run: func(_ *cobra.Command, _ []string) {
		runCodexStatus()
	},
}

func runCodexStatus() {
	ctx := context.Background()

	exists, err := auth.GetCodexCredentialsExists()
	if err != nil {
		presenter.Error(err, "Failed to check Codex credentials")
		os.Exit(1)
	}

	if !exists {
		presenter.Warning("Codex credentials not found")
		fmt.Println()
		presenter.Info("To enable Codex authentication:")
		fmt.Println("1. Run 'kodelet codex login' to authenticate with your ChatGPT account")
		fmt.Println("   - Use 'kodelet codex login --device-auth' on remote or headless machines")
		fmt.Println("2. Run 'kodelet codex status' again to verify")
		fmt.Println()
		presenter.Info("Once authenticated, add to your config:")
		fmt.Println("  provider: openai")
		fmt.Println("  openai:")
		fmt.Println("    platform: codex")
		return
	}

	creds, err := auth.GetCodexCredentials()
	if err != nil {
		presenter.Error(err, "Failed to read Codex credentials")
		os.Exit(1)
	}
	displayCreds := creds
	var usageRefreshErr error
	if auth.IsCodexOAuthEnabled(creds) {
		if refreshed, err := auth.GetCodexCredentialsForRequest(ctx); err == nil {
			displayCreds = refreshed
		} else {
			usageRefreshErr = err
		}
	}

	presenter.Success("Codex credentials found")
	fmt.Println()
	presenter.Section("Authentication")

	if auth.IsCodexOAuthEnabled(displayCreds) {
		presenter.Info("Authentication type: OAuth (ChatGPT account)")
		fmt.Printf("Account ID: %s\n", maskString(displayCreds.AccountID))

		if displayCreds.ExpiresAt > 0 {
			expiresAt := time.Unix(displayCreds.ExpiresAt, 0)
			now := time.Now()
			if expiresAt.After(now) {
				remaining := expiresAt.Sub(now).Round(time.Minute)
				fmt.Printf("Token expires: %s (in %s)\n", expiresAt.Format(time.RFC3339), remaining)
			} else {
				presenter.Warning("Token has expired")
				if displayCreds.RefreshToken != "" {
					presenter.Info("Token will be automatically refreshed on next use")
				} else {
					presenter.Info("Please run 'kodelet codex login' to re-authenticate")
				}
			}
		}

		if displayCreds.RefreshToken != "" {
			fmt.Println("Refresh token: available")
		}
	}

	if !auth.IsCodexOAuthEnabled(displayCreds) {
		fmt.Println()
		presenter.Section("Usage")
		presenter.Info("Live ChatGPT usage stats are only available with OAuth login.")
		return
	}

	stats, err := auth.GetCodexUsageStatsWithCredentials(ctx, displayCreds)
	if err != nil {
		if usageRefreshErr != nil {
			presenter.Warning(fmt.Sprintf("Live usage stats unavailable: %v", usageRefreshErr))
		} else {
			presenter.Warning(fmt.Sprintf("Live usage stats unavailable: %v", err))
		}
		presenter.Info("Visit https://chatgpt.com/codex/settings/usage for up-to-date information.")
		return
	}

	fmt.Println()
	presenter.Section("Usage")
	if plan := formatCodexPlanType(stats.PlanType); plan != "" {
		fmt.Printf("Plan: %s\n", plan)
	}
	presenter.Info("Visit https://chatgpt.com/codex/settings/usage for up-to-date information.")

	buckets := buildCodexUsageBuckets(stats, time.Now())
	if len(buckets) == 0 {
		fmt.Println("Limits: data not available yet")
		return
	}

	for _, bucket := range buckets {
		if bucket.Title != "" {
			fmt.Printf("\n%s:\n", bucket.Title)
			printCodexUsageLines(bucket.Lines, "  ")
			continue
		}

		printCodexUsageLines(bucket.Lines, "")
	}
}

type codexUsageBucket struct {
	Title string
	Lines []codexUsageLine
}

type codexUsageLine struct {
	Label string
	Value string
}

func buildCodexUsageBuckets(stats *auth.CodexUsageStats, now time.Time) []codexUsageBucket {
	if stats == nil {
		return nil
	}

	buckets := make([]codexUsageBucket, 0, len(stats.Snapshots))
	for _, snapshot := range stats.Snapshots {
		lines := make([]codexUsageLine, 0, 3)
		if snapshot.Primary != nil {
			lines = append(lines, codexUsageLine{
				Label: codexWindowLabel(snapshot.Primary, "5h"),
				Value: formatCodexWindowValue(snapshot.Primary, now),
			})
		}
		if snapshot.Secondary != nil {
			lines = append(lines, codexUsageLine{
				Label: codexWindowLabel(snapshot.Secondary, "weekly"),
				Value: formatCodexWindowValue(snapshot.Secondary, now),
			})
		}
		if credits := formatCodexCredits(snapshot.Credits); credits != "" {
			lines = append(lines, codexUsageLine{Label: "Credits", Value: credits})
		}

		if len(lines) == 0 {
			continue
		}

		buckets = append(buckets, codexUsageBucket{
			Title: codexUsageBucketTitle(snapshot),
			Lines: lines,
		})
	}

	return buckets
}

func printCodexUsageLines(lines []codexUsageLine, indent string) {
	maxLabelLen := 0
	for _, line := range lines {
		if len(line.Label) > maxLabelLen {
			maxLabelLen = len(line.Label)
		}
	}

	for _, line := range lines {
		fmt.Printf("%s%-*s  %s\n", indent, maxLabelLen+1, line.Label+":", line.Value)
	}
}

func codexUsageBucketTitle(snapshot auth.CodexUsageSnapshot) string {
	name := strings.TrimSpace(snapshot.LimitName)
	if name == "" {
		name = strings.TrimSpace(snapshot.LimitID)
	}
	if name == "" || strings.EqualFold(name, "codex") {
		return ""
	}
	return name
}

func codexWindowLabel(window *auth.CodexUsageWindow, fallback string) string {
	if window == nil || window.WindowDurationMinutes <= 0 {
		return capitalizeFirst(fallback) + " limit"
	}
	return capitalizeFirst(codexLimitDuration(window.WindowDurationMinutes)) + " limit"
}

func codexLimitDuration(windowMinutes int64) string {
	const (
		minutesPerHour      = int64(60)
		minutesPerDay       = 24 * minutesPerHour
		minutesPerWeek      = 7 * minutesPerDay
		minutesPerMonth     = 30 * minutesPerDay
		roundingBiasMinutes = int64(3)
	)

	windowMinutes = max(windowMinutes, 0)

	if windowMinutes <= minutesPerDay+roundingBiasMinutes {
		adjusted := windowMinutes + roundingBiasMinutes
		hours := max(adjusted/minutesPerHour, 1)
		return fmt.Sprintf("%dh", hours)
	}
	if windowMinutes <= minutesPerWeek+roundingBiasMinutes {
		return "weekly"
	}
	if windowMinutes <= minutesPerMonth+roundingBiasMinutes {
		return "monthly"
	}
	return "annual"
}

func formatCodexWindowValue(window *auth.CodexUsageWindow, now time.Time) string {
	if window == nil {
		return ""
	}

	remaining := math.Max(0, math.Min(100, 100-window.UsedPercent))
	if window.ResetsAt.IsZero() {
		return fmt.Sprintf("%.0f%% left", remaining)
	}

	return fmt.Sprintf("%.0f%% left (resets %s)", remaining, formatCodexResetTimestamp(window.ResetsAt, now))
}

func formatCodexCredits(credits *auth.CodexCredits) string {
	if credits == nil || !credits.HasCredits {
		return ""
	}
	if credits.Unlimited {
		return "Unlimited"
	}

	balance := strings.TrimSpace(credits.Balance)
	if balance == "" {
		return ""
	}

	if intValue, err := strconv.ParseInt(balance, 10, 64); err == nil && intValue > 0 {
		return fmt.Sprintf("%d credits", intValue)
	}
	if floatValue, err := strconv.ParseFloat(balance, 64); err == nil && floatValue > 0 {
		return fmt.Sprintf("%d credits", int64(math.Round(floatValue)))
	}

	return ""
}

func formatCodexPlanType(planType string) string {
	normalized := strings.ToLower(strings.TrimSpace(planType))
	switch normalized {
	case "":
		return ""
	case "team", "self_serve_business_usage_based":
		return "Business"
	case "business", "enterprise_cbp_usage_based", "enterprise":
		return "Enterprise"
	case "education", "edu":
		return "Edu"
	default:
		return titleWords(strings.ReplaceAll(normalized, "_", " "))
	}
}

func formatCodexResetTimestamp(resetAt time.Time, now time.Time) string {
	localReset := resetAt.Local()
	localNow := now.Local()
	timePart := localReset.Format("15:04")
	if localReset.YearDay() == localNow.YearDay() && localReset.Year() == localNow.Year() {
		return timePart
	}
	return fmt.Sprintf("%s on %d %s", timePart, localReset.Day(), localReset.Format("Jan"))
}

func capitalizeFirst(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func titleWords(s string) string {
	parts := strings.Fields(s)
	for i, part := range parts {
		parts[i] = capitalizeFirst(part)
	}
	return strings.Join(parts, " ")
}

// maskString masks a string, showing only the first and last 4 characters.
func maskString(s string) string {
	if len(s) <= 12 {
		return "****"
	}
	return s[:4] + "..." + s[len(s)-4:]
}
