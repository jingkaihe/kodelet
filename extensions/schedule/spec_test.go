package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpcomingWeekdaysFollowTheScheduleTimezone(t *testing.T) {
	friday := time.Date(2026, 3, 6, 14, 0, 0, 0, time.UTC) // 09:00 in New York.
	schedule, err := validateSpec("  CRON_TZ=America/New_York 0 9 * * 1-5  ", friday)
	require.NoError(t, err)

	// Skip the current occurrence and the weekend. DST starts on Sunday.
	assert.Equal(t, []time.Time{
		time.Date(2026, 3, 9, 13, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 10, 13, 0, 0, 0, time.UTC),
	}, upcoming(schedule, friday, 2))
}

func TestValidateSpec(t *testing.T) {
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	schedule, err := validateSpec("@every 1m", now)
	require.NoError(t, err)
	assert.Equal(t, now.Add(time.Minute), schedule.Next(now))

	for spec, message := range map[string]string{
		" ":                              "schedule is required",
		"0 0 9 * * *":                    "invalid schedule", // No seconds field.
		"CRON_TZ=Mars/Olympus 0 9 * * *": "invalid schedule",
		"@every 59s":                     "more than once a minute",
		"0 0 30 2 *":                     "never fires",
	} {
		_, err := validateSpec(spec, now)
		assert.ErrorContains(t, err, message, "spec %q", spec)
	}
}
