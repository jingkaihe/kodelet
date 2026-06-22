package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aymanbagabas/go-udiff"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

type toolRenderGroup struct {
	toolStart    int
	toolEnd      int
	changeIndex  int
	label        string
	runningLabel string
	body         string
	wrapBody     bool
	diffBody     bool
	expanded     bool
	active       bool
}

func (m model) toolRenderGroups(block assistantBlock) []toolRenderGroup {
	groups := []toolRenderGroup{}

	for idx := 0; idx < len(block.tools); {
		tool := block.tools[idx]
		switch {
		case isBashTool(tool):
			end := idx + 1
			for end < len(block.tools) && isBashTool(block.tools[end]) {
				end++
			}
			groups = append(groups, buildBashToolGroup(block, idx, end))
			idx = end

		case isApplyPatchTool(tool):
			applyGroups := buildApplyPatchToolGroups(block, idx)
			groups = append(groups, applyGroups...)
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

func buildBashToolGroup(block assistantBlock, start, end int) toolRenderGroup {
	count := end - start
	return toolRenderGroup{
		toolStart:    start,
		toolEnd:      end - 1,
		changeIndex:  -1,
		label:        fmt.Sprintf("Ran %d %s", count, pluralize(count, "command", "commands")),
		runningLabel: fmt.Sprintf("Running %d %s", count, pluralize(count, "command", "commands")),
		body:         joinTools(block.tools[start:end]),
		wrapBody:     true,
		expanded:     block.expanded || anyExpandedTool(block.tools[start:end]),
		active:       hasActiveToolRange(block.tools[start:end]),
	}
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
	}
}

func buildApplyPatchToolGroups(block assistantBlock, idx int) []toolRenderGroup {
	tool := block.tools[idx]
	changes, hasMetadata := applyPatchChanges(tool)
	if len(changes) == 0 {
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
		}}
	}

	groups := make([]toolRenderGroup, 0, len(changes))
	for changeIdx, change := range changes {
		body := applyPatchChangeDiff(change)
		wrapBody := false
		diffBody := strings.TrimSpace(body) != ""
		if strings.TrimSpace(body) == "" && !hasMetadata {
			body = joinTools([]toolCall{tool})
			wrapBody = true
			diffBody = false
		}
		if strings.TrimSpace(body) == "" && strings.TrimSpace(tool.result) != "" {
			body = strings.TrimSpace(tool.result)
			wrapBody = true
			diffBody = false
		}

		groups = append(groups, toolRenderGroup{
			toolStart:    idx,
			toolEnd:      idx,
			changeIndex:  changeIdx,
			label:        applyPatchChangeLabel(change),
			runningLabel: "Applying patch",
			body:         body,
			wrapBody:     wrapBody,
			diffBody:     diffBody,
			expanded:     block.expanded || tool.expanded || tool.expandedChanges[changeIdx],
			active:       !tool.done,
		})
	}

	return groups
}

func applyPatchChanges(tool toolCall) ([]tooltypes.ApplyPatchChange, bool) {
	if tool.structured == nil {
		return nil, false
	}

	var meta tooltypes.ApplyPatchMetadata
	if !tooltypes.ExtractMetadata(tool.structured.Metadata, &meta) {
		return nil, false
	}

	if len(meta.Changes) > 0 {
		return meta.Changes, true
	}

	changes := make([]tooltypes.ApplyPatchChange, 0, len(meta.Added)+len(meta.Modified)+len(meta.Deleted))
	for _, path := range meta.Added {
		changes = append(changes, tooltypes.ApplyPatchChange{Path: path, Operation: tooltypes.ApplyPatchOperationAdd})
	}
	for _, path := range meta.Modified {
		changes = append(changes, tooltypes.ApplyPatchChange{Path: path, Operation: tooltypes.ApplyPatchOperationUpdate})
	}
	for _, path := range meta.Deleted {
		changes = append(changes, tooltypes.ApplyPatchChange{Path: path, Operation: tooltypes.ApplyPatchOperationDelete})
	}
	return changes, true
}

func applyPatchChangeLabel(change tooltypes.ApplyPatchChange) string {
	displayPath := change.Path
	if strings.TrimSpace(change.MovePath) != "" {
		displayPath = fmt.Sprintf("%s -> %s", change.Path, change.MovePath)
	}

	switch strings.ToLower(strings.TrimSpace(change.Operation)) {
	case tooltypes.ApplyPatchOperationAdd, "write":
		return fmt.Sprintf("Write %s", displayPath)
	case tooltypes.ApplyPatchOperationDelete:
		return fmt.Sprintf("Delete %s", displayPath)
	case "move":
		return fmt.Sprintf("Move %s", displayPath)
	default:
		if strings.TrimSpace(change.MovePath) != "" {
			return fmt.Sprintf("Move %s", displayPath)
		}
		return fmt.Sprintf("Edit %s", displayPath)
	}
}

func applyPatchChangeDiff(change tooltypes.ApplyPatchChange) string {
	if strings.TrimSpace(change.UnifiedDiff) != "" {
		return strings.TrimSuffix(change.UnifiedDiff, "\n")
	}

	switch strings.ToLower(strings.TrimSpace(change.Operation)) {
	case tooltypes.ApplyPatchOperationAdd, "write":
		if change.OldContent != "" || change.NewContent != "" {
			return strings.TrimSuffix(udiff.Unified(change.Path, change.Path, change.OldContent, change.NewContent), "\n")
		}
	case tooltypes.ApplyPatchOperationDelete:
		if change.OldContent != "" {
			return strings.TrimSuffix(udiff.Unified(change.Path, change.Path, change.OldContent, ""), "\n")
		}
	case "move", tooltypes.ApplyPatchOperationUpdate:
		if change.OldContent != "" || change.NewContent != "" {
			newPath := change.Path
			if strings.TrimSpace(change.MovePath) != "" {
				newPath = change.MovePath
			}
			return strings.TrimSuffix(udiff.Unified(change.Path, newPath, change.OldContent, change.NewContent), "\n")
		}
	}

	return ""
}

func renderToolGroupBody(body string, diffBody bool) string {
	if !diffBody {
		return toolBodyStyle.Render(body)
	}

	lines := strings.Split(body, "\n")
	for i, line := range lines {
		lines[i] = renderDiffLine(line)
	}
	return strings.Join(lines, "\n")
}

func renderDiffLine(line string) string {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "+++") || strings.HasPrefix(trimmed, "---") {
		return toolBodyStyle.Render(line)
	}
	if strings.HasPrefix(trimmed, "+") {
		return diffAddedStyle.Render(line)
	}
	if strings.HasPrefix(trimmed, "-") {
		return diffRemovedStyle.Render(line)
	}
	return toolBodyStyle.Render(line)
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
	return !isBashTool(tool) && !isApplyPatchTool(tool) && !isDedicatedBuiltinTool(tool)
}

func isBashTool(tool toolCall) bool {
	return normalizedToolName(tool) == "bash"
}

func isApplyPatchTool(tool toolCall) bool {
	return normalizedToolName(tool) == "apply_patch"
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
