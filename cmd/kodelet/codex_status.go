package main

import (
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/spf13/cobra"
)

var codexStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show ChatGPT subscription status and usage",
	Long:  "Show the selected server's ChatGPT connection, account, plan, usage limits and credits. Sign-in details stay on the server.",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		client, err := remoteAdministrationClient(cmd)
		if err != nil {
			return err
		}
		status, err := client.CodexStatus(cmd.Context())
		if err != nil {
			return err
		}
		return renderCodexStatus(cmd.OutOrStdout(), status)
	},
}

func renderCodexStatus(w io.Writer, status chat.CodexStatus) error {
	fmt.Fprintf(w, "ChatGPT subscription connected: %t\n", status.Connected)
	if !status.Connected {
		_, err := fmt.Fprintln(w, "Run 'kodelet codex login' to connect your ChatGPT account.")
		return err
	}
	if status.Authentication != "" {
		fmt.Fprintf(w, "Authentication: %s\n", status.Authentication)
	}
	if status.AccountID != "" {
		fmt.Fprintf(w, "Account ID: %s\n", status.AccountID)
	}
	if status.ExpiresAt > 0 {
		expires := time.Unix(status.ExpiresAt, 0)
		if expires.After(time.Now()) {
			fmt.Fprintf(w, "Token expires: %s\n", expires.Format(time.RFC3339))
		} else if status.CanRefresh {
			fmt.Fprintln(w, "Sign-in will refresh automatically on next use.")
		} else {
			fmt.Fprintln(w, "Sign-in has expired. Run 'kodelet codex login' to reconnect.")
		}
	}
	if status.UsageMessage != "" {
		fmt.Fprintln(w, status.UsageMessage)
	}
	if stats := status.Usage; stats != nil {
		fmt.Fprintln(w, "\nUsage")
		if plan := formatCodexPlanType(stats.PlanType); plan != "" {
			fmt.Fprintf(w, "Plan: %s\n", plan)
		}
		buckets := buildCodexUsageBuckets(stats, time.Now())
		if len(buckets) == 0 {
			fmt.Fprintln(w, "Limits: data not available yet")
		}
		for _, bucket := range buckets {
			indent := ""
			if bucket.Title != "" {
				fmt.Fprintf(w, "\n%s:\n", bucket.Title)
				indent = "  "
			}
			writeCodexUsageLines(w, bucket.Lines, indent)
		}
	}
	_, err := fmt.Fprintln(w, "https://chatgpt.com/codex/settings/usage")
	return err
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

func writeCodexUsageLines(w io.Writer, lines []codexUsageLine, indent string) {
	maxLabelLen := 0
	for _, line := range lines {
		if len(line.Label) > maxLabelLen {
			maxLabelLen = len(line.Label)
		}
	}

	for _, line := range lines {
		fmt.Fprintf(w, "%s%-*s  %s\n", indent, maxLabelLen+1, line.Label+":", line.Value)
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
