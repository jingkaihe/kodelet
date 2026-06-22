package tui

import (
	"fmt"
	"strings"
)

func (m *model) toggleAllDetails() {
	shouldExpand := false
	for _, entry := range m.entries {
		if entry.kind != entryAssistant {
			continue
		}
		for _, block := range entry.blocks {
			if !isDetailBlock(block) {
				continue
			}
			if !block.expanded {
				shouldExpand = true
				break
			}
		}
		if shouldExpand {
			break
		}
	}
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].kind != entryAssistant {
			continue
		}
		for blockIdx := range m.entries[i].blocks {
			if isDetailBlock(m.entries[i].blocks[blockIdx]) {
				m.entries[i].blocks[blockIdx].expanded = shouldExpand
			}
		}
	}
}

func isDetailBlock(block assistantBlock) bool {
	return (block.kind == blockThoughts && len(block.thoughts) > 0) || (block.kind == blockTools && len(block.tools) > 0)
}

func (m *model) toggleDetailAt(screenY int) bool {
	viewportY := screenY
	if viewportY < 0 || viewportY >= m.viewport.Height {
		return false
	}
	contentLine := m.viewport.YOffset + viewportY
	for _, region := range m.detailRegions {
		if region.line != contentLine || region.entryIndex < 0 || region.entryIndex >= len(m.entries) || region.blockIndex < 0 || region.blockIndex >= len(m.entries[region.entryIndex].blocks) {
			continue
		}
		block := &m.entries[region.entryIndex].blocks[region.blockIndex]
		if region.kind == detailTools && region.toolStart >= 0 && region.toolStart < len(block.tools) {
			if region.changeIndex >= 0 {
				tool := &block.tools[region.toolStart]
				if tool.expandedChanges == nil {
					tool.expandedChanges = map[int]bool{}
				}
				tool.expandedChanges[region.changeIndex] = !tool.expandedChanges[region.changeIndex]
				return true
			}

			shouldExpand := true
			end := min(region.toolEnd, len(block.tools)-1)
			for toolIdx := region.toolStart; toolIdx <= end; toolIdx++ {
				if !block.tools[toolIdx].expanded {
					shouldExpand = true
					break
				}
				shouldExpand = false
			}
			for toolIdx := region.toolStart; toolIdx <= end; toolIdx++ {
				block.tools[toolIdx].expanded = shouldExpand
			}
			return true
		}

		block.expanded = !block.expanded
		return true
	}
	return false
}

func hasActiveThought(block assistantBlock) bool {
	return len(block.thoughts) > 0 && !block.thoughts[len(block.thoughts)-1].done
}

func hasActiveTool(block assistantBlock) bool {
	for _, tool := range block.tools {
		if !tool.done {
			return true
		}
	}
	return false
}

func joinThoughts(thoughts []thoughtBlock) string {
	parts := make([]string, 0, len(thoughts))
	for _, thought := range thoughts {
		if trimmed := strings.TrimSpace(thought.text); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return strings.Join(parts, "\n\n")
}

func joinTools(tools []toolCall) string {
	parts := make([]string, 0, len(tools))
	for _, tool := range tools {
		status := "running"
		if tool.done {
			status = "done"
			if tool.failed {
				status = "failed"
			}
		}
		title := tool.name
		if title == "" {
			title = "tool"
		}
		part := fmt.Sprintf("%s - %s", title, status)
		if strings.TrimSpace(tool.input) != "" {
			part += "\ninput: " + compactJSON(tool.input)
		}
		if strings.TrimSpace(tool.result) != "" {
			part += "\nresult: " + strings.TrimSpace(tool.result)
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "\n\n")
}
