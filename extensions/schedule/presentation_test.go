package main

import (
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/sdk/go/extension"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResultsPresentMarkdownToUsers(t *testing.T) {
	th := newToolHarness(t, "runner-a", true)

	summary, body := presentationOf(t, th.call(t, "schedule_list", map[string]any{}))
	assert.Equal(t, "0 schedules · daemon not started", summary)
	assert.Contains(t, body, "**Warning:** The schedule daemon has never run")
	assert.Contains(t, body, "No schedules yet.")

	require.NoError(t, th.store.SaveHeartbeat(t.Context(), DaemonStatus{PID: 1, StartedAt: toolNow, HeartbeatAt: toolNow, PollInterval: time.Minute}))
	result := th.call(t, "schedule_create", map[string]any{
		"name":        "nightly *review*",
		"schedule":    "CRON_TZ=UTC 0 9 * * *",
		"instruction": "Review PRs\nthen ```summarise```",
	})
	summary, body = presentationOf(t, result)
	assert.Equal(t, "Created schedule nightly *review*", summary)
	assert.Contains(t, body, `### nightly \*review\*`)
	assert.Contains(t, body, "`CRON_TZ=UTC 0 9 * * *` · enabled")
	assert.Contains(t, body, "- **Next runs:** "+displayTime(time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)))
	assert.Contains(t, body, "- **Profile:** anthropic")
	assert.Contains(t, body, "````text\nReview PRs\nthen ```summarise```\n````")
	assert.NotContains(t, body, "Warning")

	schedules, err := th.store.List(t.Context(), "runner-a")
	require.NoError(t, err)
	id := schedules[0].ID
	summary, body = presentationOf(t, th.call(t, "schedule_update", map[string]any{"id": id, "instruction": "x", "enabled": false}))
	assert.Equal(t, "Updated schedule nightly *review*", summary)
	assert.Contains(t, body, "**Changed:** instruction, paused")
	assert.Contains(t, body, "· paused")
	assert.NotContains(t, body, "Next run")

	summary, body = presentationOf(t, th.call(t, "schedule_delete", map[string]any{"id": id}))
	assert.Equal(t, "Deleted schedule nightly *review*", summary)
	assert.Contains(t, body, `Deleted **nightly \*review\***`)
}

func TestListPresentationShowsLastRun(t *testing.T) {
	due := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	finished := due.Add(time.Minute)
	schedule := Schedule{
		ID: "abc", Name: "daily", Schedule: "@daily", Instruction: "p", CWD: "/w", Enabled: true, NextRunAt: &finished,
		LastRun: &Run{ScheduledFor: due, StartedAt: due, FinishedAt: &finished, Status: RunFailed, ConversationID: "conv-1", Error: "runner <offline>"},
	}

	data := listPresentation([]Schedule{schedule}, daemonHealth{State: daemonRunning})

	presentation, ok := data["presentation"].(extension.Presentation)
	require.True(t, ok)
	assert.Equal(t, "1 schedule", presentation.Summary)
	assert.Contains(t, presentation.Body, "- **Next run:** "+displayTime(finished))
	assert.Contains(t, presentation.Body, "- **Last run:** Failed, due "+displayTime(due)+" · conversation `conv-1`")
	assert.Contains(t, presentation.Body, `- **Last run detail:** runner \<offline\>`)
}

func TestMarkdownHelpers(t *testing.T) {
	assert.Equal(t, "`a`", markdownCode("a"))
	assert.Equal(t, "`` a`b ``", markdownCode("a`b"))
	assert.Equal(t, "```text\nplain\n```", markdownBlock("plain"))
	assert.Equal(t, "`````text\n````\n`````", markdownBlock("````"))
	assert.Equal(t, `a\_b\#\|c`, markdownText("a_b#|c"))
}
