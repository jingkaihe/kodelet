package main

import (
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunCodexStatusNoCredentials(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	output := captureAllStdout(t, runCodexStatus)

	assert.Contains(t, output, "Codex credentials not found")
	assert.Contains(t, output, "kodelet codex login")
	assert.Contains(t, output, "platform: codex")
}

func TestRunCodexStatusOAuthCredentialsUsageUnavailable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, err := auth.SaveCodexCredentials(&auth.CodexCredentials{
		AccessToken: "access-token",
		AccountID:   "acct_1234567890",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	})
	require.NoError(t, err)

	output := captureAllStdout(t, runCodexStatus)

	assert.Contains(t, output, "Codex credentials found")
	assert.Contains(t, output, "Authentication type: OAuth")
	assert.Contains(t, output, "Account ID: acct...7890")
	assert.Contains(t, output, "Live usage stats unavailable")
	assert.Contains(t, output, "chatgpt.com/codex/settings/usage")
}

func TestBuildCodexUsageBuckets(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.Local)
	stats := &auth.CodexUsageStats{
		Snapshots: []auth.CodexUsageSnapshot{
			{
				LimitID:   "codex",
				LimitName: "codex",
				Primary:   &auth.CodexUsageWindow{UsedPercent: 25, WindowDurationMinutes: 5 * 60, ResetsAt: now.Add(2 * time.Hour)},
				Secondary: &auth.CodexUsageWindow{UsedPercent: 90, WindowDurationMinutes: 7 * 24 * 60, ResetsAt: now.AddDate(0, 0, 3)},
				Credits:   &auth.CodexCredits{HasCredits: true, Balance: "12.4"},
			},
			{
				LimitID:   "feature-id",
				LimitName: "Premium models",
				Primary:   &auth.CodexUsageWindow{UsedPercent: 150, WindowDurationMinutes: 30 * 24 * 60},
			},
			{LimitID: "empty"},
		},
	}

	buckets := buildCodexUsageBuckets(stats, now)

	require.Len(t, buckets, 2)
	assert.Equal(t, "", buckets[0].Title)
	assert.Equal(t, []codexUsageLine{
		{Label: "5h limit", Value: "75% left (resets 14:00)"},
		{Label: "Weekly limit", Value: "10% left (resets 12:00 on 26 May)"},
		{Label: "Credits", Value: "12 credits"},
	}, buckets[0].Lines)
	assert.Equal(t, "Premium models", buckets[1].Title)
	assert.Equal(t, []codexUsageLine{{Label: "Monthly limit", Value: "0% left"}}, buckets[1].Lines)
	assert.Nil(t, buildCodexUsageBuckets(nil, now))
}

func TestPrintCodexUsageLinesAlignsLabels(t *testing.T) {
	output := captureStdout(t, func() {
		printCodexUsageLines([]codexUsageLine{
			{Label: "Short", Value: "one"},
			{Label: "Longer label", Value: "two"},
		}, "  ")
	})

	assert.Contains(t, output, "  Short:         one")
	assert.Contains(t, output, "  Longer label:  two")
}

func TestCodexUsageFormattingHelpers(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.Local)

	assert.Equal(t, "", codexUsageBucketTitle(auth.CodexUsageSnapshot{LimitID: "codex"}))
	assert.Equal(t, "premium", codexUsageBucketTitle(auth.CodexUsageSnapshot{LimitID: "premium"}))
	assert.Equal(t, "Named", codexUsageBucketTitle(auth.CodexUsageSnapshot{LimitID: "id", LimitName: " Named "}))

	assert.Equal(t, "5h limit", codexWindowLabel(&auth.CodexUsageWindow{WindowDurationMinutes: 299}, "weekly"))
	assert.Equal(t, "Weekly limit", codexWindowLabel(&auth.CodexUsageWindow{WindowDurationMinutes: 7 * 24 * 60}, "5h"))
	assert.Equal(t, "Monthly limit", codexWindowLabel(&auth.CodexUsageWindow{WindowDurationMinutes: 30 * 24 * 60}, "5h"))
	assert.Equal(t, "Annual limit", codexWindowLabel(&auth.CodexUsageWindow{WindowDurationMinutes: 400 * 24 * 60}, "5h"))
	assert.Equal(t, "Weekly limit", codexWindowLabel(nil, "weekly"))

	assert.Equal(t, "", formatCodexWindowValue(nil, now))
	assert.Equal(t, "62% left", formatCodexWindowValue(&auth.CodexUsageWindow{UsedPercent: 38}, now))
	assert.Equal(t, "100% left", formatCodexWindowValue(&auth.CodexUsageWindow{UsedPercent: -20}, now))
	assert.Equal(t, "0% left", formatCodexWindowValue(&auth.CodexUsageWindow{UsedPercent: 120}, now))
	assert.Equal(t, "50% left (resets 14:00)", formatCodexWindowValue(&auth.CodexUsageWindow{UsedPercent: 50, ResetsAt: now.Add(2 * time.Hour)}, now))
	assert.Contains(t, formatCodexWindowValue(&auth.CodexUsageWindow{UsedPercent: 50, ResetsAt: now.AddDate(0, 0, 1)}, now), "on 24 May")
}

func TestCodexCreditsAndPlanFormatting(t *testing.T) {
	assert.Equal(t, "", formatCodexCredits(nil))
	assert.Equal(t, "", formatCodexCredits(&auth.CodexCredits{HasCredits: false, Balance: "10"}))
	assert.Equal(t, "Unlimited", formatCodexCredits(&auth.CodexCredits{HasCredits: true, Unlimited: true}))
	assert.Equal(t, "42 credits", formatCodexCredits(&auth.CodexCredits{HasCredits: true, Balance: "42"}))
	assert.Equal(t, "13 credits", formatCodexCredits(&auth.CodexCredits{HasCredits: true, Balance: "12.6"}))
	assert.Equal(t, "", formatCodexCredits(&auth.CodexCredits{HasCredits: true, Balance: "0"}))
	assert.Equal(t, "", formatCodexCredits(&auth.CodexCredits{HasCredits: true, Balance: "not-a-number"}))

	assert.Equal(t, "", formatCodexPlanType(""))
	assert.Equal(t, "Business", formatCodexPlanType("team"))
	assert.Equal(t, "Business", formatCodexPlanType("self_serve_business_usage_based"))
	assert.Equal(t, "Enterprise", formatCodexPlanType("enterprise_cbp_usage_based"))
	assert.Equal(t, "Edu", formatCodexPlanType("education"))
	assert.Equal(t, "Custom Plan", formatCodexPlanType("custom_plan"))
}

func TestCodexStringHelpers(t *testing.T) {
	assert.Equal(t, "", capitalizeFirst(""))
	assert.Equal(t, "Hello", capitalizeFirst("hello"))
	assert.Equal(t, "One Two", titleWords("one two"))
	assert.Equal(t, "****", maskString("short"))
	assert.Equal(t, "abcd...wxyz", maskString("abcdefghijklmnopqrstuvwxyz"))

	assert.Equal(t, "5h", codexLimitDuration(5*60))
	assert.Equal(t, "1h", codexLimitDuration(-10))
	assert.Equal(t, "annual", codexLimitDuration(365*24*60))
	assert.True(t, strings.HasPrefix(formatCodexResetTimestamp(time.Now().Add(time.Hour), time.Now()), time.Now().Add(time.Hour).Format("15")))
}
