package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/jingkaihe/kodelet/pkg/diffview"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

type toolRenderGroup struct {
	toolStart    int
	toolEnd      int
	changeIndex  int
	label        string
	labelParts   []toolRenderLabelPart
	runningLabel string
	body         string
	bodyLines    []diffview.RenderedLine
	wrapBody     bool
	markdownBody bool
	expanded     bool
	active       bool
	failed       bool
}

type toolRenderLabelPart struct {
	kind diffview.LineKind
	text string
}

func (m *model) toolRenderGroups(block assistantBlock) []toolRenderGroup {
	groups := []toolRenderGroup{}

	for idx := 0; idx < len(block.tools); {
		tool := block.tools[idx]
		switch {
		case isBashTool(tool):
			end := idx + 1
			for end < len(block.tools) && isBashTool(block.tools[end]) {
				end++
			}
			groups = append(groups, m.buildBashToolGroup(block, idx, end))
			idx = end

		case isApplyPatchTool(tool):
			applyGroups := m.buildApplyPatchToolGroups(block, idx)
			groups = append(groups, applyGroups...)
			idx++

		case isFileEditTool(tool):
			groups = append(groups, m.buildFileEditToolGroup(block, idx))
			idx++

		case isTaskRunTool(tool):
			groups = append(groups, m.buildTaskRunToolGroup(block, idx))
			idx++

		case isDedicatedBuiltinTool(tool):
			groups = append(groups, buildDedicatedBuiltinToolGroup(block, idx))
			idx++

		default:
			end := idx + 1
			for end < len(block.tools) && isFallbackAggregateTool(block.tools[end]) {
				end++
			}
			groups = append(groups, buildFallbackToolGroup(block, idx, end))
			idx = end
		}
	}

	return groups
}

func (m *model) buildTaskRunToolGroup(block assistantBlock, idx int) toolRenderGroup {
	tool := block.tools[idx]
	snapshot, metadata, _ := tooltypes.ExtractTaskRunSnapshot(tool.structured)
	runningLabel := strings.TrimSpace(snapshot.Title)
	if runningLabel == "" {
		runningLabel = "Running task"
	}
	if detail := strings.TrimSpace(snapshot.Detail); detail != "" {
		runningLabel += " - " + detail
	}

	label := strings.TrimSpace(snapshot.Title)
	if label == "" {
		label = "Task"
	}
	elapsed := formatTaskRunElapsed(taskRunElapsedMS(snapshot, tool, time.Now()))
	if !tool.done && snapshot.Status == "running" && tool.structured != nil && !tool.structured.Timestamp.IsZero() {
		elapsed = m.transcriptElapsedPlaceholder(func(now time.Time) string {
			return formatTaskRunElapsed(taskRunElapsedMS(snapshot, tool, now))
		})
	}
	if tool.done {
		label = taskRunCompletionLabel(snapshot, label)
	}

	body := renderTaskRunProgressBody(*m, snapshot, elapsed)
	markdownBody := false
	if tool.done {
		body = strings.TrimSpace(metadata.Output)
		if tool.structured != nil {
			errorText := strings.TrimSpace(tool.structured.Error)
			if errorText != "" && errorText != body {
				if body == "" {
					body = errorText
				} else {
					body = "**Error:** " + errorText + "\n\n" + body
				}
			}
		}
		markdownBody = body != ""
	}

	return toolRenderGroup{
		toolStart:    idx,
		toolEnd:      idx,
		changeIndex:  -1,
		label:        label,
		runningLabel: runningLabel,
		body:         body,
		wrapBody:     !markdownBody,
		markdownBody: markdownBody,
		expanded:     block.expanded || tool.expanded || tool.failed || snapshot.Status == "failed",
		active:       !tool.done,
		failed:       tool.failed || snapshot.Status == "failed",
	}
}

func taskRunCompletionLabel(snapshot tooltypes.TaskRunSnapshot, fallback string) string {
	parts := []string{fallback}
	if total := snapshot.Counts.Total(); total > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", total, pluralize(total, "action", "actions")))
	}
	if snapshot.Counts.Failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", snapshot.Counts.Failed))
	}
	if elapsed := formatTaskRunElapsed(snapshot.ElapsedMS); elapsed != "" {
		parts = append(parts, elapsed)
	}
	return strings.Join(parts, " · ")
}

func renderTaskRunProgressBody(m model, snapshot tooltypes.TaskRunSnapshot, elapsed string) string {
	parts := []string{}
	counts := []string{}
	if snapshot.Counts.Succeeded > 0 {
		counts = append(counts, fmt.Sprintf("%d done", snapshot.Counts.Succeeded))
	}
	if snapshot.Counts.Failed > 0 {
		counts = append(counts, fmt.Sprintf("%d failed", snapshot.Counts.Failed))
	}
	if snapshot.Counts.Running > 0 {
		counts = append(counts, fmt.Sprintf("%d running", snapshot.Counts.Running))
	}
	if elapsed != "" {
		counts = append(counts, elapsed)
	}
	if len(counts) > 0 {
		parts = append(parts, strings.Join(counts, " · "))
	}

	activityLines := make([]string, 0, len(snapshot.Activities)+3)
	for _, activity := range snapshot.Activities {
		marker := "✓"
		switch activity.Status {
		case "running":
			marker = m.spinnerGlyph()
		case "failed":
			marker = "✗"
		}
		activityLines = append(activityLines, marker+" "+strings.TrimSpace(activity.Label))
		if preview := taskRunActivityPreview(activity.Preview); activity.Status == "failed" && preview != "" {
			activityLines = append(activityLines, "  "+preview)
		}
	}
	if snapshot.OmittedSucceeded > 0 {
		activityLines = append(activityLines, fmt.Sprintf("+%d earlier completed", snapshot.OmittedSucceeded))
	}
	if snapshot.OmittedFailed > 0 {
		activityLines = append(activityLines, fmt.Sprintf("+%d earlier failed", snapshot.OmittedFailed))
	}
	if snapshot.OmittedRunning > 0 {
		activityLines = append(activityLines, fmt.Sprintf("+%d more running", snapshot.OmittedRunning))
	}
	if len(activityLines) > 0 {
		if len(parts) > 0 {
			parts = append(parts, "")
		}
		parts = append(parts, activityLines...)
	}
	return strings.Join(parts, "\n")
}

func taskRunElapsedMS(snapshot tooltypes.TaskRunSnapshot, tool toolCall, now time.Time) int64 {
	elapsedMS := snapshot.ElapsedMS
	if tool.structured == nil {
		return elapsedMS
	}
	observedAt := tool.structured.Timestamp
	if tool.done || snapshot.Status != "running" || observedAt.IsZero() {
		return elapsedMS
	}
	delta := now.Sub(observedAt).Milliseconds()
	if delta <= 0 {
		return elapsedMS
	}
	return elapsedMS + delta
}

func taskRunActivityPreview(value string) string {
	preview := ""
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			continue
		}
		preview = line
	}
	return preview
}

func (m model) spinnerGlyph() string {
	return strings.TrimSpace(m.spinner.View())
}

func formatTaskRunElapsed(elapsedMS int64) string {
	if elapsedMS <= 0 {
		return ""
	}
	duration := time.Duration(elapsedMS) * time.Millisecond
	if duration < time.Minute {
		return fmt.Sprintf("%ds", int(duration.Round(time.Second)/time.Second))
	}
	if duration < time.Hour {
		minutes := int(duration / time.Minute)
		seconds := int((duration % time.Minute).Round(time.Second) / time.Second)
		if seconds == 60 {
			minutes++
			seconds = 0
		}
		return fmt.Sprintf("%dm %02ds", minutes, seconds)
	}
	hours := int(duration / time.Hour)
	minutes := int((duration % time.Hour) / time.Minute)
	return fmt.Sprintf("%dh %02dm", hours, minutes)
}

func (m *model) buildBashToolGroup(block assistantBlock, start, end int) toolRenderGroup {
	bashTools := block.tools[start:end]
	count := len(bashTools)
	return toolRenderGroup{
		toolStart:    start,
		toolEnd:      end - 1,
		changeIndex:  -1,
		label:        fmt.Sprintf("Ran %d %s", count, pluralize(count, "command", "commands")),
		runningLabel: fmt.Sprintf("Running %d %s", count, pluralize(count, "command", "commands")),
		body:         m.renderBashToolsBody(bashTools, time.Now(), m.transcriptTextWidth()-2),
		wrapBody:     true,
		expanded:     block.expanded || anyExpandedTool(bashTools),
		active:       hasActiveToolRange(bashTools),
		failed:       anyFailedTool(bashTools),
	}
}

// renderBashToolsBody renders bash calls as the shell transcript they are,
// instead of dumping raw tool input JSON alongside CLI-oriented results.
func (m *model) renderBashToolsBody(tools []toolCall, now time.Time, width int) string {
	parts := make([]string, 0, len(tools))
	for _, tool := range tools {
		if part := m.renderBashToolBody(tool, now, width); part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, "\n\n")
}

func bashToolBody(tool toolCall, now time.Time, width int) string {
	var meta tooltypes.BashMetadata
	hasMeta := tool.structured != nil && tooltypes.ExtractMetadata(tool.structured.Metadata, &meta)
	return bashToolBodyWithElapsed(tool, meta, hasMeta, bashElapsed(tool, meta, now), width)
}

func (m *model) renderBashToolBody(tool toolCall, now time.Time, width int) string {
	var meta tooltypes.BashMetadata
	hasMeta := tool.structured != nil && tooltypes.ExtractMetadata(tool.structured.Metadata, &meta)
	elapsed := bashElapsed(tool, meta, now)
	if bashElapsedIsLive(tool, meta) {
		elapsed = m.transcriptElapsedPlaceholder(func(now time.Time) string {
			return bashElapsed(tool, meta, now)
		})
	}
	return bashToolBodyWithElapsed(tool, meta, hasMeta, elapsed, width)
}

func bashToolBodyWithElapsed(tool toolCall, meta tooltypes.BashMetadata, hasMeta bool, elapsed string, width int) string {
	lines := make([]string, 0, 3)
	command := strings.TrimSpace(meta.Command)
	if command == "" {
		command = stringField(toolInputFields(tool.input), "command")
	}
	if command != "" {
		lines = append(lines, bashCommandLine(command, elapsed, width))
	}

	errorText := bashErrorText(tool)
	output := strings.TrimRight(meta.Output, "\n")
	if !hasMeta && errorText == "" {
		// Results without metadata (older conversations) only carry rendered text.
		output = strings.TrimSpace(tool.result)
	}
	if strings.TrimSpace(output) != "" {
		lines = append(lines, output)
	}
	if errorText != "" {
		lines = append(lines, errorText)
	}

	if len(lines) == 0 {
		return strings.TrimSpace(tool.result)
	}
	return strings.Join(lines, "\n")
}

func bashCommandLine(command, elapsed string, width int) string {
	width = max(10, width)
	commandLines := strings.Split(wrapPreservingWhitespace("$ "+command, width), "\n")
	rendered := make([]string, 0, len(commandLines)+1)
	for _, line := range commandLines {
		rendered = append(rendered, renderPersistentStyle(bashCommandStyle, line))
	}
	if elapsed == "" {
		return strings.Join(rendered, "\n")
	}

	suffix := "  ·  " + elapsed
	last := len(rendered) - 1
	if lipgloss.Width(commandLines[last])+lipgloss.Width(suffix) <= width {
		rendered[last] += renderPersistentStyle(mutedStyle, suffix)
		return strings.Join(rendered, "\n")
	}
	for _, line := range strings.Split(wrapPreservingWhitespace(suffix, width), "\n") {
		rendered = append(rendered, renderPersistentStyle(mutedStyle, line))
	}
	return strings.Join(rendered, "\n")
}

func bashElapsed(tool toolCall, meta tooltypes.BashMetadata, now time.Time) string {
	elapsed := meta.ExecutionTime
	if !tool.done {
		if tool.structured != nil && !tool.structured.Timestamp.IsZero() {
			if delta := now.Sub(tool.structured.Timestamp); delta > 0 {
				elapsed += delta
			}
		} else if elapsed <= 0 && !tool.startedAt.IsZero() {
			elapsed = now.Sub(tool.startedAt)
		}
	}
	if elapsed <= 0 || (tool.done && elapsed < time.Second) {
		return ""
	}
	return formatTaskRunElapsed(elapsed.Milliseconds())
}

func bashElapsedIsLive(tool toolCall, meta tooltypes.BashMetadata) bool {
	if tool.done {
		return false
	}
	if tool.structured != nil && !tool.structured.Timestamp.IsZero() {
		return true
	}
	return meta.ExecutionTime <= 0 && !tool.startedAt.IsZero()
}

func bashErrorText(tool toolCall) string {
	if !tool.done || tool.structured == nil {
		return ""
	}
	return strings.TrimSpace(tool.structured.Error)
}

func buildFallbackToolGroup(block assistantBlock, start, end int) toolRenderGroup {
	count := end - start
	return toolRenderGroup{
		toolStart:    start,
		toolEnd:      end - 1,
		changeIndex:  -1,
		label:        fmt.Sprintf("Ran %d %s", count, pluralize(count, "tool", "tools")),
		runningLabel: fmt.Sprintf("Running %d %s", count, pluralize(count, "tool", "tools")),
		body:         joinTools(block.tools[start:end]),
		wrapBody:     true,
		expanded:     block.expanded || anyExpandedTool(block.tools[start:end]),
		active:       hasActiveToolRange(block.tools[start:end]),
		failed:       anyFailedTool(block.tools[start:end]),
	}
}

func buildDedicatedBuiltinToolGroup(block assistantBlock, idx int) toolRenderGroup {
	tool := block.tools[idx]
	label, runningLabel := dedicatedBuiltinToolLabels(tool)
	return toolRenderGroup{
		toolStart:    idx,
		toolEnd:      idx,
		changeIndex:  -1,
		label:        label,
		runningLabel: runningLabel,
		body:         joinTools([]toolCall{tool}),
		wrapBody:     true,
		expanded:     block.expanded || tool.expanded,
		active:       !tool.done,
		failed:       tool.failed,
	}
}

func (m model) buildFileEditToolGroup(block assistantBlock, idx int) toolRenderGroup {
	tool := block.tools[idx]
	summary := fileEditSummary(tool)
	if len(summary.Files) == 0 {
		label := "Edited file"
		if tool.failed {
			label = "File edit failed"
		}
		return toolRenderGroup{
			toolStart:    idx,
			toolEnd:      idx,
			changeIndex:  -1,
			label:        label,
			runningLabel: "Editing file",
			body:         joinTools([]toolCall{tool}),
			wrapBody:     true,
			expanded:     block.expanded || tool.expanded,
			active:       !tool.done,
			failed:       tool.failed,
		}
	}

	file := summary.Files[0]
	bodyLines := diffview.RenderFileBodyWidth(file, m.transcriptTextWidth()-2)
	if tool.failed {
		if errorText := fileEditErrorText(tool); errorText != "" {
			if len(bodyLines) > 0 {
				bodyLines = append(bodyLines, diffview.RenderedLine{Kind: diffview.LinePlain, Text: ""})
			}
			bodyLines = append(bodyLines, diffview.RenderedLine{Kind: diffview.LineMeta, Text: errorText})
		}
	}

	return toolRenderGroup{
		toolStart:    idx,
		toolEnd:      idx,
		changeIndex:  -1,
		label:        file.Header(),
		labelParts:   diffLabelParts(file),
		runningLabel: "Editing file",
		body:         diffview.RenderedText(bodyLines),
		bodyLines:    bodyLines,
		expanded:     block.expanded || tool.expanded,
		active:       !tool.done,
		failed:       tool.failed,
	}
}

func (m model) buildApplyPatchToolGroups(block assistantBlock, idx int) []toolRenderGroup {
	tool := block.tools[idx]
	summary := applyPatchSummary(tool)
	if len(summary.Files) == 0 {
		label := "Applied patch"
		if tool.failed {
			label = "Apply patch failed"
		}
		return []toolRenderGroup{{
			toolStart:    idx,
			toolEnd:      idx,
			changeIndex:  -1,
			label:        label,
			runningLabel: "Applying patch",
			body:         joinTools([]toolCall{tool}),
			wrapBody:     true,
			expanded:     block.expanded || tool.expanded,
			active:       !tool.done,
			failed:       tool.failed,
		}}
	}

	groups := make([]toolRenderGroup, 0, len(summary.Files))
	for changeIdx, file := range summary.Files {
		bodyLines := diffview.RenderFileBodyWidth(file, m.transcriptTextWidth()-2)
		body := diffview.RenderedText(bodyLines)
		wrapBody := false
		if tool.failed {
			errorText := applyPatchErrorText(tool)
			if strings.TrimSpace(errorText) != "" {
				bodyLines = append(bodyLines, diffview.RenderedLine{Kind: diffview.LinePlain, Text: ""})
				bodyLines = append(bodyLines, diffview.RenderedLine{Kind: diffview.LineMeta, Text: strings.TrimSpace(errorText)})
				body = diffview.RenderedText(bodyLines)
				wrapBody = false
			}
		}

		groups = append(groups, toolRenderGroup{
			toolStart:    idx,
			toolEnd:      idx,
			changeIndex:  changeIdx,
			label:        file.Header(),
			labelParts:   diffLabelParts(file),
			runningLabel: "Applying patch",
			body:         body,
			bodyLines:    bodyLines,
			wrapBody:     wrapBody,
			expanded:     block.expanded || tool.expanded || tool.expandedChanges[changeIdx],
			active:       !tool.done,
		})
	}

	return groups
}

func diffLabelParts(file diffview.FileDiff) []toolRenderLabelPart {
	return []toolRenderLabelPart{
		{kind: diffview.LinePlain, text: fmt.Sprintf("%s %s (", file.OperationLabel(), file.DisplayPath())},
		{kind: diffview.LineAdded, text: fmt.Sprintf("+%d", file.Added)},
		{kind: diffview.LinePlain, text: " "},
		{kind: diffview.LineRemoved, text: fmt.Sprintf("-%d", file.Removed)},
		{kind: diffview.LinePlain, text: ")"},
	}
}

func fileEditSummary(tool toolCall) diffview.Summary {
	if tool.structured == nil {
		return diffview.Summary{}
	}

	var meta tooltypes.FileEditMetadata
	if !tooltypes.ExtractMetadata(tool.structured.Metadata, &meta) {
		return diffview.Summary{}
	}

	return diffview.FromFileEditMetadata(meta)
}

func fileEditErrorText(tool toolCall) string {
	if tool.structured != nil && strings.TrimSpace(tool.structured.Error) != "" {
		return strings.TrimSpace(tool.structured.Error)
	}
	return strings.TrimSpace(tool.result)
}

func applyPatchSummary(tool toolCall) diffview.Summary {
	if tool.structured == nil {
		return diffview.Summary{}
	}

	var meta tooltypes.ApplyPatchMetadata
	if !tooltypes.ExtractMetadata(tool.structured.Metadata, &meta) {
		return diffview.Summary{}
	}

	return diffview.FromApplyPatchMetadata(meta)
}

func applyPatchErrorText(tool toolCall) string {
	if tool.structured != nil && strings.TrimSpace(tool.structured.Error) != "" {
		return strings.TrimSpace(tool.structured.Error)
	}
	return strings.TrimSpace(tool.result)
}

func renderDiffRenderedLines(lines []diffview.RenderedLine) string {
	rendered := make([]string, 0, len(lines))
	for _, line := range lines {
		rendered = append(rendered, renderDiffRenderedLine(line))
	}
	return strings.Join(rendered, "\n")
}

func renderDiffRenderedLine(line diffview.RenderedLine) string {
	switch line.Kind {
	case diffview.LineAdded:
		return diffAddedStyle.Render(line.Text)
	case diffview.LineRemoved:
		return diffRemovedStyle.Render(line.Text)
	default:
		return toolBodyStyle.Render(line.Text)
	}
}

func dedicatedBuiltinToolLabels(tool toolCall) (string, string) {
	switch normalizedToolName(tool) {
	case "openai_web_search", "web_search":
		return webSearchToolLabel(tool), "Searching web"
	case "web_fetch":
		return webFetchToolLabel(tool), "Fetching web page"
	case "view_image":
		return viewImageToolLabel(tool), "Viewing image"
	case "skill":
		return skillToolLabel(tool), "Loading skill"
	default:
		name := normalizedToolName(tool)
		if name == "" {
			name = "tool"
		}
		return fmt.Sprintf("Ran %s", name), fmt.Sprintf("Running %s", name)
	}
}

func webSearchToolLabel(tool toolCall) string {
	if tool.structured != nil {
		var meta tooltypes.OpenAIWebSearchMetadata
		if tooltypes.ExtractMetadata(tool.structured.Metadata, &meta) {
			switch strings.ToLower(strings.TrimSpace(meta.Action)) {
			case "open_page":
				return fmt.Sprintf("Opened %s", firstNonEmpty(meta.URL, "web page"))
			case "find_in_page":
				if meta.Pattern != "" {
					return fmt.Sprintf("Searched %s for %q", firstNonEmpty(meta.URL, "web page"), meta.Pattern)
				}
				return fmt.Sprintf("Searched %s", firstNonEmpty(meta.URL, "web page"))
			case "search":
				if len(meta.Queries) > 0 {
					return fmt.Sprintf("Searched web for %q", meta.Queries[0])
				}
			}
		}
	}

	fields := toolInputFields(tool.input)
	if queries, ok := stringSliceField(fields, "queries"); ok && len(queries) > 0 {
		return fmt.Sprintf("Searched web for %q", queries[0])
	}
	if query := stringField(fields, "query"); query != "" {
		return fmt.Sprintf("Searched web for %q", query)
	}
	if url := stringField(fields, "url"); url != "" {
		return fmt.Sprintf("Opened %s", url)
	}
	return "Searched web"
}

func webFetchToolLabel(tool toolCall) string {
	if tool.structured != nil {
		var meta tooltypes.WebFetchMetadata
		if tooltypes.ExtractMetadata(tool.structured.Metadata, &meta) && strings.TrimSpace(meta.URL) != "" {
			return fmt.Sprintf("Fetched %s", meta.URL)
		}
	}
	if url := stringField(toolInputFields(tool.input), "url"); url != "" {
		return fmt.Sprintf("Fetched %s", url)
	}
	return "Fetched web page"
}

func viewImageToolLabel(tool toolCall) string {
	if tool.structured != nil {
		var meta tooltypes.ViewImageMetadata
		if tooltypes.ExtractMetadata(tool.structured.Metadata, &meta) && strings.TrimSpace(meta.Path) != "" {
			return fmt.Sprintf("Viewed image %s", meta.Path)
		}
	}
	if path := stringField(toolInputFields(tool.input), "path"); path != "" {
		return fmt.Sprintf("Viewed image %s", path)
	}
	return "Viewed image"
}

func skillToolLabel(tool toolCall) string {
	if tool.structured != nil {
		var meta tooltypes.SkillMetadata
		if tooltypes.ExtractMetadata(tool.structured.Metadata, &meta) && strings.TrimSpace(meta.SkillName) != "" {
			return fmt.Sprintf("Loaded skill %s", meta.SkillName)
		}
	}
	if skillName := stringField(toolInputFields(tool.input), "skill_name"); skillName != "" {
		return fmt.Sprintf("Loaded skill %s", skillName)
	}
	if skillName := stringField(toolInputFields(tool.input), "skillName"); skillName != "" {
		return fmt.Sprintf("Loaded skill %s", skillName)
	}
	return "Loaded skill"
}

func isFallbackAggregateTool(tool toolCall) bool {
	return !isBashTool(tool) && !isApplyPatchTool(tool) && !isFileEditTool(tool) && !isTaskRunTool(tool) && !isDedicatedBuiltinTool(tool)
}

func isBashTool(tool toolCall) bool {
	return normalizedToolName(tool) == "bash"
}

func isApplyPatchTool(tool toolCall) bool {
	return normalizedToolName(tool) == "apply_patch"
}

func isFileEditTool(tool toolCall) bool {
	return normalizedToolName(tool) == "file_edit"
}

func isTaskRunTool(tool toolCall) bool {
	_, _, ok := tooltypes.ExtractTaskRunSnapshot(tool.structured)
	return ok
}

func isDedicatedBuiltinTool(tool toolCall) bool {
	switch normalizedToolName(tool) {
	case "openai_web_search", "web_search", "web_fetch", "view_image", "skill":
		return true
	default:
		return false
	}
}

func normalizedToolName(tool toolCall) string {
	if tool.structured != nil {
		if tool.structured.Metadata != nil {
			if metadataType := strings.TrimSpace(tool.structured.Metadata.ToolType()); metadataType != "" {
				return strings.ToLower(metadataType)
			}
		}
		if name := strings.TrimSpace(tool.structured.ToolName); name != "" {
			return strings.ToLower(name)
		}
	}
	return strings.ToLower(strings.TrimSpace(tool.name))
}

func anyExpandedTool(tools []toolCall) bool {
	for _, tool := range tools {
		if tool.expanded {
			return true
		}
	}
	return false
}

func anyFailedTool(tools []toolCall) bool {
	for _, tool := range tools {
		if tool.failed {
			return true
		}
	}
	return false
}

func hasActiveToolRange(tools []toolCall) bool {
	for _, tool := range tools {
		if !tool.done {
			return true
		}
	}
	return false
}

func pluralize(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func toolInputFields(input string) map[string]any {
	var fields map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(input)), &fields); err != nil {
		return nil
	}
	return fields
}

func stringField(fields map[string]any, key string) string {
	if len(fields) == 0 {
		return ""
	}
	value, _ := fields[key].(string)
	return strings.TrimSpace(value)
}

func stringSliceField(fields map[string]any, key string) ([]string, bool) {
	if len(fields) == 0 {
		return nil, false
	}
	value, ok := fields[key]
	if !ok {
		return nil, false
	}

	switch typed := value.(type) {
	case []string:
		return typed, true
	case []any:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				items = append(items, strings.TrimSpace(text))
			}
		}
		return items, len(items) > 0
	default:
		return nil, false
	}
}
