package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/sdk/go/extension"
)

// Tool results show users Markdown through the SDK presentation contract,
// while the model reads the plain-text content.

const displayTimeLayout = "Mon 2 Jan 2006 15:04 MST"

// presentationData attaches a Markdown presentation to a tool result.
func presentationData(summary, body string) map[string]any {
	return map[string]any{"presentation": extension.Presentation{
		Summary: summary,
		Body:    strings.TrimSpace(body),
		Format:  "markdown",
	}}
}

func displayTime(t time.Time) string {
	return t.Local().Format(displayTimeLayout)
}

// markdownText escapes inline characters that Markdown would otherwise
// interpret in single-line text.
func markdownText(text string) string {
	var b strings.Builder
	for _, r := range text {
		if strings.ContainsRune("\\`*_[]<>#|~", r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// markdownCode renders text as inline code, with a fence longer than any
// backtick run inside it.
func markdownCode(text string) string {
	longest, run := 0, 0
	for _, r := range text {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", longest+1)
	if longest > 0 {
		return fence + " " + text + " " + fence
	}
	return fence + text + fence
}

// markdownBlock renders text verbatim in a fenced block, with a fence longer
// than any backtick run inside it.
func markdownBlock(text string) string {
	longest, run := 0, 0
	for _, r := range text {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", max(3, longest+1))
	return fence + "text\n" + text + "\n" + fence
}

func runStatusLabel(status RunStatus) string {
	switch status {
	case RunRunning:
		return "Running"
	case RunSucceeded:
		return "Succeeded"
	case RunFailed:
		return "Failed"
	case RunCanceled:
		return "Canceled"
	case RunSkipped:
		return "Skipped"
	default:
		return string(status)
	}
}

// writeScheduleMarkdown writes a schedule's title line, details, and
// instruction. It avoids headings, which terminal renderers show as raw
// markers.
func writeScheduleMarkdown(b *strings.Builder, schedule Schedule, runs []time.Time) {
	state := "enabled"
	if !schedule.Enabled {
		state = "paused"
	}
	fmt.Fprintf(b, "**%s** · %s · %s\n\n", markdownText(schedule.Name), markdownCode(schedule.Schedule), state)
	if schedule.Enabled {
		switch {
		case len(runs) > 0:
			formatted := make([]string, len(runs))
			for i, run := range runs {
				formatted[i] = displayTime(run)
			}
			fmt.Fprintf(b, "- **Next runs:** %s\n", strings.Join(formatted, ", "))
		case schedule.NextRunAt != nil:
			fmt.Fprintf(b, "- **Next run:** %s\n", displayTime(*schedule.NextRunAt))
		}
	}
	if run := schedule.LastRun; run != nil {
		fmt.Fprintf(b, "- **Last run:** %s, due %s", runStatusLabel(run.Status), displayTime(run.ScheduledFor))
		if run.ConversationID != "" {
			fmt.Fprintf(b, " · conversation %s", markdownCode(run.ConversationID))
		}
		b.WriteString("\n")
		if run.Error != "" {
			fmt.Fprintf(b, "- **Last run detail:** %s\n", markdownText(run.Error))
		}
	}
	fmt.Fprintf(b, "- **Directory:** %s\n", markdownCode(schedule.CWD))
	fmt.Fprintf(b, "- **Profile:** %s\n", markdownText(profileLabel(schedule.Profile)))
	fmt.Fprintf(b, "- **ID:** %s\n\n", markdownCode(schedule.ID))
	b.WriteString(markdownBlock(schedule.Instruction))
	b.WriteString("\n\n")
}

// writeWarningMarkdown writes the daemon warning, which this extension
// authors and may format as Markdown.
func writeWarningMarkdown(b *strings.Builder, health daemonHealth) {
	if health.Warning != "" {
		fmt.Fprintf(b, "**Warning:** %s\n\n", capitalize(health.Warning))
	}
}

// summaryWithHealth appends the daemon state to a compact summary when the
// daemon is not running, so users see it without expanding the result.
func summaryWithHealth(summary string, health daemonHealth) string {
	if health.State == daemonRunning {
		return summary
	}
	return summary + " · daemon " + health.State
}

func listPresentation(schedules []Schedule, health daemonHealth) map[string]any {
	var b strings.Builder
	writeWarningMarkdown(&b, health)
	if len(schedules) == 0 {
		b.WriteString("No schedules yet.\n")
	}
	for _, schedule := range schedules {
		writeScheduleMarkdown(&b, schedule, nil)
	}
	return presentationData(summaryWithHealth(countLabel(len(schedules)), health), b.String())
}

func createPresentation(schedule Schedule, runs []time.Time, health daemonHealth) map[string]any {
	var b strings.Builder
	writeWarningMarkdown(&b, health)
	writeScheduleMarkdown(&b, schedule, runs)
	return presentationData(summaryWithHealth("Created schedule "+schedule.Name, health), b.String())
}

// updatePresentation shows the saved schedule and the names of the fields
// that changed.
func updatePresentation(schedule Schedule, changed []string, runs []time.Time, health daemonHealth) map[string]any {
	var b strings.Builder
	writeWarningMarkdown(&b, health)
	fmt.Fprintf(&b, "**Changed:** %s\n\n", strings.Join(changed, ", "))
	writeScheduleMarkdown(&b, schedule, runs)
	return presentationData(summaryWithHealth("Updated schedule "+schedule.Name, health), b.String())
}

func deletePresentation(schedule Schedule) map[string]any {
	body := fmt.Sprintf("Deleted **%s** (%s) and its run history.", markdownText(schedule.Name), markdownCode(schedule.ID))
	return presentationData("Deleted schedule "+schedule.Name, body)
}

func countLabel(count int) string {
	if count == 1 {
		return "1 schedule"
	}
	return fmt.Sprintf("%d schedules", count)
}

func capitalize(text string) string {
	if text == "" {
		return text
	}
	return strings.ToUpper(text[:1]) + text[1:]
}

const maxDetailLength = 80

// runListPresentation shows runs as a table; scope names the schedule when
// the list is for one.
func runListPresentation(runs []Run, scope string) map[string]any {
	summary := runCountLabel(len(runs))
	if scope != "" {
		summary += " of " + scope
	}
	if len(runs) == 0 {
		return presentationData(summary, "No runs yet.")
	}
	var b strings.Builder
	b.WriteString("| Run | Schedule | Due | Status | Took | Tools | Cost | Conversation |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, run := range runs {
		took, tools, cost, conversation := "", "", "", ""
		if duration, ok := runDuration(run); ok && run.Status != RunSkipped {
			took = formatDuration(duration)
		}
		if run.Stats != nil {
			tools = fmt.Sprintf("%d", run.Stats.ToolCalls)
			if run.Stats.Usage != nil {
				cost = formatCost(run.Stats.Usage.Cost)
			}
		}
		if run.ConversationID != "" {
			conversation = markdownCode(run.ConversationID)
		}
		status := runStatusLabel(run.Status)
		if run.Error != "" {
			status += ": " + markdownText(truncate(singleLine(run.Error), maxDetailLength))
		}
		fmt.Fprintf(&b, "| %d | %s | %s | %s | %s | %s | %s | %s |\n",
			run.ID, markdownText(run.ScheduleName), displayTime(run.ScheduledFor),
			status, took, tools, cost, conversation,
		)
	}
	return presentationData(summary, b.String())
}

// runPresentation shows when a run ran, what it was asked and did, and the
// agent's final output, rendered as the Markdown it usually is.
func runPresentation(run Run) map[string]any {
	var b strings.Builder
	fmt.Fprintf(&b, "**%s** · run %d · %s\n\n", markdownText(run.ScheduleName), run.ID, runStatusLabel(run.Status))
	fmt.Fprintf(&b, "- **Due:** %s\n", displayTime(run.ScheduledFor))
	if duration, ok := runDuration(run); ok {
		fmt.Fprintf(&b, "- **Ran:** %s to %s (%s)\n", displayTime(run.StartedAt), displayTime(*run.FinishedAt), formatDuration(duration))
	} else {
		fmt.Fprintf(&b, "- **Started:** %s\n", displayTime(run.StartedAt))
	}
	if run.Error != "" {
		fmt.Fprintf(&b, "- **Detail:** %s\n", markdownText(singleLine(run.Error)))
	}
	if run.ConversationID != "" {
		fmt.Fprintf(&b, "- **Conversation:** %s\n", markdownCode(run.ConversationID))
	}
	if run.CWD != "" {
		fmt.Fprintf(&b, "- **Directory:** %s\n", markdownCode(run.CWD))
	}
	fmt.Fprintf(&b, "- **Profile:** %s\n", markdownText(profileLabel(run.Profile)))
	if stats := run.Stats; stats != nil {
		fmt.Fprintf(&b, "- **Tool calls:** %s\n", markdownText(describeToolCalls(*stats)))
		if usage := stats.Usage; usage != nil {
			fmt.Fprintf(&b, "- **Tokens:** %s\n", describeTokens(*usage))
			fmt.Fprintf(&b, "- **Cost:** %s\n", formatCost(usage.Cost))
		}
		if stats.Compactions > 0 {
			fmt.Fprintf(&b, "- **Context compactions:** %d\n", stats.Compactions)
		}
	}
	if run.Instruction != "" {
		b.WriteString("\n**Instruction**\n\n")
		b.WriteString(markdownBlock(run.Instruction))
		b.WriteString("\n")
	}
	b.WriteString("\n**Final output**\n\n")
	if run.Output == "" {
		b.WriteString("None.\n")
	} else {
		b.WriteString(run.Output)
		b.WriteString("\n")
	}
	summary := fmt.Sprintf("Run %d of %s · %s", run.ID, run.ScheduleName, runStatusLabel(run.Status))
	if duration, ok := runDuration(run); ok && run.Status != RunSkipped {
		summary += " · " + formatDuration(duration)
	}
	return presentationData(summary, b.String())
}

func singleLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
