package auth

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetCodexUsageStatsWithCredentials(t *testing.T) {
	t.Run("uses fixed chatgpt usage endpoint and maps response", func(t *testing.T) {
		originalClient := http.DefaultClient
		http.DefaultClient = &http.Client{
			Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				assert.Equal(t, http.MethodGet, req.Method)
				assert.Equal(t, codexUsageEndpoint, req.URL.String())
				assert.Equal(t, "Bearer test-access-token", req.Header.Get("Authorization"))
				assert.Equal(t, "account-123", req.Header.Get("ChatGPT-Account-ID"))
				assert.Equal(t, "kodelet", req.Header.Get("User-Agent"))

				body := `{
					"plan_type":"pro",
					"rate_limit":{
						"allowed":true,
						"limit_reached":false,
						"primary_window":{"used_percent":42,"limit_window_seconds":3600,"reset_after_seconds":120,"reset_at":1735689720},
						"secondary_window":{"used_percent":5,"limit_window_seconds":86400,"reset_after_seconds":43200,"reset_at":1735776000}
					},
					"credits":{"has_credits":true,"unlimited":false,"balance":"38"},
					"additional_rate_limits":[
						{
							"limit_name":"codex_other",
							"metered_feature":"codex_other",
							"rate_limit":{
								"allowed":true,
								"limit_reached":false,
								"primary_window":{"used_percent":88,"limit_window_seconds":1800,"reset_after_seconds":600,"reset_at":1735693200}
							}
						}
					]
				}`

				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(body)),
				}, nil
			}),
		}
		defer func() { http.DefaultClient = originalClient }()

		stats, err := GetCodexUsageStatsWithCredentials(context.Background(), &CodexCredentials{
			AccessToken: "test-access-token",
			AccountID:   "account-123",
		})
		require.NoError(t, err)
		require.NotNil(t, stats)

		assert.Equal(t, "pro", stats.PlanType)
		require.Len(t, stats.Snapshots, 2)

		primary := stats.Snapshots[0]
		assert.Equal(t, "codex", primary.LimitID)
		assert.Equal(t, "", primary.LimitName)
		require.NotNil(t, primary.Primary)
		assert.Equal(t, 42.0, primary.Primary.UsedPercent)
		assert.Equal(t, int64(60), primary.Primary.WindowDurationMinutes)
		assert.Equal(t, time.Unix(1735689720, 0), primary.Primary.ResetsAt)
		require.NotNil(t, primary.Secondary)
		assert.Equal(t, 5.0, primary.Secondary.UsedPercent)
		assert.Equal(t, int64(1440), primary.Secondary.WindowDurationMinutes)
		require.NotNil(t, primary.Credits)
		assert.Equal(t, "38", primary.Credits.Balance)

		secondary := stats.Snapshots[1]
		assert.Equal(t, "codex_other", secondary.LimitID)
		assert.Equal(t, "codex_other", secondary.LimitName)
		require.NotNil(t, secondary.Primary)
		assert.Equal(t, 88.0, secondary.Primary.UsedPercent)
		assert.Equal(t, int64(30), secondary.Primary.WindowDurationMinutes)
		assert.Nil(t, secondary.Credits)
	})

	t.Run("requires chatgpt oauth credentials", func(t *testing.T) {
		stats, err := GetCodexUsageStatsWithCredentials(context.Background(), &CodexCredentials{})
		assert.Nil(t, stats)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "chatgpt authentication required")
	})
}
