package main

import (
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/sdk/go/extension"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func presentationOf(t *testing.T, result extension.ToolResult) extension.Presentation {
	t.Helper()
	p := decodeValue[extension.Presentation](t, result.Data["presentation"])
	require.Equal(t, "markdown", p.Format)
	return p
}

func TestScheduleReports(t *testing.T) {
	th := newToolHarness(t, "runner-a", true)
	empty := th.success(t, "schedule_list", map[string]any{})
	assert.Contains(t, empty.Content, "has never run on this host")
	assert.Contains(t, presentationOf(t, empty).Summary, "daemon not started")

	created := th.success(t, "schedule_create", map[string]any{
		"name": "nightly *review*", "schedule": "CRON_TZ=UTC 0 9 * * *",
		"instruction": "Review PRs\nthen ```summarise```",
	})
	p := presentationOf(t, created)
	assert.Contains(t, p.Summary, "Created schedule nightly *review*")
	assert.Contains(t, p.Body, "**nightly \\*review\\***")
	assert.Contains(t, p.Body, "````text\nReview PRs\nthen ```summarise```\n````")
	assert.Contains(t, p.Body, "**Next runs:**")

	id := decodeValue[scheduleOutput](t, created.StructuredContent).Schedule.ID
	schedule, err := th.store.Get(t.Context(), "runner-a", id)
	require.NoError(t, err)
	finishToolRun(t, th, schedule, RunFailed, "runner <offline>", runResult{})
	now := schedule.NextRunAt.Add(2 * time.Minute)
	th.clock.Set(now)
	require.NoError(t, th.store.SaveHeartbeat(t.Context(), DaemonStatus{
		PID: 1, StartedAt: toolNow, HeartbeatAt: now, PollInterval: time.Minute,
	}))
	listed := th.success(t, "schedule_list", map[string]any{})
	view := decodeValue[listOutput](t, listed.StructuredContent)
	assert.Equal(t, daemonRunning, view.Daemon.State)
	require.Len(t, view.Schedules, 1)
	require.NotNil(t, view.Schedules[0].LastRun)
	assert.Equal(t, "runner <offline>", view.Schedules[0].LastRun.Error)
	assert.Contains(t, presentationOf(t, listed).Body, `**Last run detail:** runner \<offline\>`)
}
