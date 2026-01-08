package anthropic

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/pkg/errors"
)

// RateLimitStats contains Anthropic subscription rate limit information
type RateLimitStats struct {
	// 5-hour window stats
	Status5h      string
	Utilization5h float64
	Reset5h       time.Time

	// 7-day window stats
	Status7d      string
	Utilization7d float64
	Reset7d       time.Time
}

// GetRateLimitStats makes a lightweight API call to retrieve rate limit statistics
// from the Anthropic API response headers. Only works with subscription accounts.
func GetRateLimitStats(ctx context.Context, accountAlias string) (*RateLimitStats, error) {
	antCredsExists, _ := auth.GetAnthropicCredentialsExists()
	if !antCredsExists {
		return nil, errors.New("no Anthropic subscription credentials found")
	}

	headerOpts, err := auth.AnthropicHeader(ctx, accountAlias)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get access token")
	}

	var stats RateLimitStats
	var capturedHeaders http.Header

	mw := func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		resp, err := next(req)
		if resp != nil {
			capturedHeaders = resp.Header.Clone()
		}
		return resp, err
	}

	opts := append(headerOpts, option.WithMiddleware(mw))
	client := anthropic.NewClient(opts...)

	// Make a minimal request using haiku (cheapest model)
	_, err = client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeHaiku4_5_20251001,
		MaxTokens: 1,
		System:    auth.AnthropicSystemPrompt(),
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock("hi")),
		},
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to make API request")
	}

	stats.Status5h = capturedHeaders.Get("Anthropic-Ratelimit-Unified-5h-Status")
	stats.Status7d = capturedHeaders.Get("Anthropic-Ratelimit-Unified-7d-Status")

	if v := capturedHeaders.Get("Anthropic-Ratelimit-Unified-5h-Utilization"); v != "" {
		stats.Utilization5h, _ = strconv.ParseFloat(v, 64)
	}
	if v := capturedHeaders.Get("Anthropic-Ratelimit-Unified-7d-Utilization"); v != "" {
		stats.Utilization7d, _ = strconv.ParseFloat(v, 64)
	}

	if v := capturedHeaders.Get("Anthropic-Ratelimit-Unified-5h-Reset"); v != "" {
		if ts, err := strconv.ParseInt(v, 10, 64); err == nil {
			stats.Reset5h = time.Unix(ts, 0)
		}
	}
	if v := capturedHeaders.Get("Anthropic-Ratelimit-Unified-7d-Reset"); v != "" {
		if ts, err := strconv.ParseInt(v, 10, 64); err == nil {
			stats.Reset7d = time.Unix(ts, 0)
		}
	}

	return &stats, nil
}
