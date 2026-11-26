package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateSpec(t *testing.T) {
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		spec string
		next time.Time
		err  string
	}{
		{name: "cron", spec: "CRON_TZ=UTC 0 9 * * *", next: time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)},
		{name: "timezone", spec: "CRON_TZ=Asia/Tokyo 0 9 * * *", next: time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)},
		{name: "descriptor", spec: "@every 2h", next: now.Add(2 * time.Hour)},
		{name: "surrounding space", spec: "  CRON_TZ=UTC 30 10 * * *  ", next: time.Date(2026, 11, 26, 10, 30, 0, 0, time.UTC)},
		{name: "empty", spec: " ", err: "schedule is required"},
		{name: "malformed", spec: "every day", err: "invalid schedule"},
		{name: "seconds field", spec: "0 0 9 * * *", err: "invalid schedule"},
		{name: "unknown timezone", spec: "CRON_TZ=Mars/Olympus 0 9 * * *", err: "invalid schedule"},
		{name: "too frequent", spec: "@every 30s", err: "more than once a minute"},
		{name: "never fires", spec: "0 0 30 2 *", err: "never fires"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schedule, err := validateSpec(tt.spec, now)
			if tt.err != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.err)
				return
			}
			require.NoError(t, err)
			assert.True(t, tt.next.Equal(schedule.Next(now)), "next run %s, want %s", schedule.Next(now), tt.next)
		})
	}
}

func TestUpcoming(t *testing.T) {
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	schedule, err := parseSpec("CRON_TZ=UTC 0 9 * * 1-5")
	require.NoError(t, err)

	runs := upcoming(schedule, now, 3)

	require.Len(t, runs, 3)
	assert.True(t, runs[0].Equal(time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)))
	assert.True(t, runs[1].Equal(time.Date(2026, 11, 30, 9, 0, 0, 0, time.UTC)), "skips the weekend")
	assert.True(t, runs[2].Equal(time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC)))
}
