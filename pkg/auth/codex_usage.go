package auth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pkg/errors"
)

const codexUsageEndpoint = "https://chatgpt.com/backend-api/wham/usage"

// CodexUsageStats contains the live ChatGPT-backed Codex usage information.
type CodexUsageStats struct {
	PlanType  string
	Snapshots []CodexUsageSnapshot
}

// CodexUsageSnapshot contains usage data for a specific metered feature.
type CodexUsageSnapshot struct {
	LimitID   string
	LimitName string
	Primary   *CodexUsageWindow
	Secondary *CodexUsageWindow
	Credits   *CodexCredits
}

// CodexUsageWindow contains one rolling usage window.
type CodexUsageWindow struct {
	UsedPercent           float64
	WindowDurationMinutes int64
	ResetsAt              time.Time
}

// CodexCredits contains workspace credit information when available.
type CodexCredits struct {
	HasCredits bool
	Unlimited  bool
	Balance    string
}

type codexRateLimitStatusPayload struct {
	PlanType             string                           `json:"plan_type"`
	RateLimit            *codexRateLimitStatusDetails     `json:"rate_limit"`
	Credits              *codexCreditStatusDetails        `json:"credits"`
	AdditionalRateLimits []codexAdditionalRateLimitDetail `json:"additional_rate_limits"`
}

type codexRateLimitStatusDetails struct {
	Allowed         bool                          `json:"allowed"`
	LimitReached    bool                          `json:"limit_reached"`
	PrimaryWindow   *codexRateLimitWindowSnapshot `json:"primary_window"`
	SecondaryWindow *codexRateLimitWindowSnapshot `json:"secondary_window"`
}

type codexCreditStatusDetails struct {
	HasCredits bool   `json:"has_credits"`
	Unlimited  bool   `json:"unlimited"`
	Balance    string `json:"balance"`
}

type codexAdditionalRateLimitDetail struct {
	LimitName      string                       `json:"limit_name"`
	MeteredFeature string                       `json:"metered_feature"`
	RateLimit      *codexRateLimitStatusDetails `json:"rate_limit"`
}

type codexRateLimitWindowSnapshot struct {
	UsedPercent        int `json:"used_percent"`
	LimitWindowSeconds int `json:"limit_window_seconds"`
	ResetAfterSeconds  int `json:"reset_after_seconds"`
	ResetAt            int `json:"reset_at"`
}

// GetCodexUsageStats loads the current credentials, refreshing OAuth tokens when needed,
// and fetches the live ChatGPT-backed Codex usage windows.
func GetCodexUsageStats(ctx context.Context) (*CodexUsageStats, error) {
	creds, err := GetCodexCredentialsForRequest(ctx)
	if err != nil {
		return nil, err
	}

	return GetCodexUsageStatsWithCredentials(ctx, creds)
}

// GetCodexUsageStatsWithCredentials fetches live ChatGPT-backed Codex usage windows using
// the provided OAuth credentials.
func GetCodexUsageStatsWithCredentials(ctx context.Context, creds *CodexCredentials) (*CodexUsageStats, error) {
	if creds == nil || creds.AccessToken == "" || creds.AccountID == "" {
		return nil, errors.New("chatgpt authentication required to read Codex usage stats")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, codexUsageEndpoint, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create Codex usage request")
	}
	req.Header.Set("Authorization", "Bearer "+creds.AccessToken)
	req.Header.Set("ChatGPT-Account-ID", creds.AccountID)
	req.Header.Set("User-Agent", "kodelet")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to fetch Codex usage stats")
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return nil, errors.Errorf("failed to fetch Codex usage stats: %s: %s", resp.Status, msg)
	}

	var payload codexRateLimitStatusPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, errors.Wrap(err, "failed to decode Codex usage stats response")
	}

	return codexUsageStatsFromPayload(payload), nil
}

func codexUsageStatsFromPayload(payload codexRateLimitStatusPayload) *CodexUsageStats {
	snapshots := []CodexUsageSnapshot{makeCodexUsageSnapshot(
		"codex",
		"",
		payload.RateLimit,
		payload.Credits,
	)}

	for _, additional := range payload.AdditionalRateLimits {
		snapshots = append(snapshots, makeCodexUsageSnapshot(
			additional.MeteredFeature,
			additional.LimitName,
			additional.RateLimit,
			nil,
		))
	}

	return &CodexUsageStats{
		PlanType:  payload.PlanType,
		Snapshots: snapshots,
	}
}

func makeCodexUsageSnapshot(
	limitID string,
	limitName string,
	rateLimit *codexRateLimitStatusDetails,
	credits *codexCreditStatusDetails,
) CodexUsageSnapshot {
	var primary *CodexUsageWindow
	var secondary *CodexUsageWindow
	if rateLimit != nil {
		primary = mapCodexUsageWindow(rateLimit.PrimaryWindow)
		secondary = mapCodexUsageWindow(rateLimit.SecondaryWindow)
	}

	return CodexUsageSnapshot{
		LimitID:   limitID,
		LimitName: limitName,
		Primary:   primary,
		Secondary: secondary,
		Credits:   mapCodexCredits(credits),
	}
}

func mapCodexUsageWindow(window *codexRateLimitWindowSnapshot) *CodexUsageWindow {
	if window == nil {
		return nil
	}

	usageWindow := &CodexUsageWindow{
		UsedPercent: float64(window.UsedPercent),
	}
	if window.LimitWindowSeconds > 0 {
		usageWindow.WindowDurationMinutes = int64(window.LimitWindowSeconds+59) / 60
	}
	if window.ResetAt > 0 {
		usageWindow.ResetsAt = time.Unix(int64(window.ResetAt), 0)
	}

	return usageWindow
}

func mapCodexCredits(credits *codexCreditStatusDetails) *CodexCredits {
	if credits == nil {
		return nil
	}

	return &CodexCredits{
		HasCredits: credits.HasCredits,
		Unlimited:  credits.Unlimited,
		Balance:    credits.Balance,
	}
}
