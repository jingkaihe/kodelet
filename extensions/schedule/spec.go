package main

import (
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/robfig/cron/v3"
)

// minInterval is the shortest allowed gap between two consecutive runs.
const minInterval = time.Minute

// parseSpec parses a five-field cron expression or a descriptor such as
// @daily or @every 2h. A CRON_TZ=<zone> prefix selects the timezone; without
// it, the host's local time applies.
func parseSpec(spec string) (cron.Schedule, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, errors.New("schedule is required")
	}
	schedule, err := cron.ParseStandard(spec)
	if err != nil {
		return nil, errors.Wrapf(err, "invalid schedule %q", spec)
	}
	return schedule, nil
}

// validateSpec parses spec and checks that it fires, and no more than once
// per minInterval.
func validateSpec(spec string, now time.Time) (cron.Schedule, error) {
	schedule, err := parseSpec(spec)
	if err != nil {
		return nil, err
	}
	first := schedule.Next(now)
	if first.IsZero() {
		return nil, errors.Errorf("schedule %q never fires", spec)
	}
	if second := schedule.Next(first); !second.IsZero() && second.Sub(first) < minInterval {
		return nil, errors.Errorf("schedule %q fires more than once a minute", spec)
	}
	return schedule, nil
}

// upcoming returns up to n activation times after from.
func upcoming(schedule cron.Schedule, from time.Time, n int) []time.Time {
	times := make([]time.Time, 0, n)
	for next := schedule.Next(from); !next.IsZero() && len(times) < n; next = schedule.Next(next) {
		times = append(times, next)
	}
	return times
}
