package tui

import (
	"fmt"
	"strings"
)

func (m *model) renderTranscript() (string, []detailRegion) {
	var b strings.Builder
	regions := []detailRegion{}
	line := 0

	if len(m.entries) == 0 {
		intro := mutedStyle.Render("Hello! What would you like me to work on?")
		b.WriteString("\n")
		b.WriteString(intro)
		b.WriteString("\n")
		line += lineCount(intro) + 2
		m.renderQueuedSteering(&b, &line)
		return b.String(), regions
	}

	for i, entry := range m.entries {
		if i > 0 {
			b.WriteString("\n")
			line++
		}

		switch entry.kind {
		case entryUser:
			block := userStyle.Render("┃ " + strings.ReplaceAll(wrapText(strings.TrimSpace(entry.content), m.transcriptTextWidth()-2), "\n", "\n┃ "))
			b.WriteString(block)
			b.WriteString("\n")
			line += lineCount(block)
		case entryAssistant:
			renderedAssistantBlock := false
			for blockIdx, block := range entry.blocks {
				switch block.kind {
				case blockThoughts:
					if len(block.thoughts) == 0 {
						continue
					}
					m.renderAssistantBlockSeparator(&b, &line, &renderedAssistantBlock)
					header := m.renderThoughtHeader(block)
					b.WriteString(header)
					b.WriteString("\n")
					regions = append(regions, detailRegion{entryIndex: i, blockIndex: blockIdx, kind: detailThoughts, line: line})
					line++
					if block.expanded || hasActiveThought(block) {
						body := indentText(m.renderMarkdown(joinThoughts(block.thoughts), m.transcriptTextWidth()-2, markdownThought), "  ")
						if strings.TrimSpace(body) != "" {
							b.WriteString("\n")
							line++
							rendered := renderPersistentStyle(thoughtBodyStyle, body)
							b.WriteString(rendered)
							b.WriteString("\n")
							line += lineCount(rendered)
						}
					}
				case blockTools:
					if len(block.tools) == 0 {
						continue
					}
					for _, group := range m.toolRenderGroups(block) {
						m.renderAssistantBlockSeparator(&b, &line, &renderedAssistantBlock)
						header := m.renderToolGroupHeader(group)
						b.WriteString(header)
						b.WriteString("\n")
						regions = append(regions, detailRegion{entryIndex: i, blockIndex: blockIdx, kind: detailTools, line: line, toolStart: group.toolStart, toolEnd: group.toolEnd, changeIndex: group.changeIndex})
						line++
						if group.expanded || group.active {
							body := group.body
							if group.wrapBody {
								body = wrapPreservingWhitespace(body, m.transcriptTextWidth()-2)
							}
							body = indentText(body, "  ")
							if strings.TrimSpace(body) != "" {
								rendered := renderToolGroupBody(body, group.diffBody)
								b.WriteString(rendered)
								b.WriteString("\n")
								line += lineCount(rendered)
							}
						}
					}
				case blockText:
					trimmed := strings.TrimSpace(block.text)
					if trimmed != "" {
						m.renderAssistantBlockSeparator(&b, &line, &renderedAssistantBlock)
						renderedMarkdown := m.renderMarkdown(trimmed, m.transcriptTextWidth(), markdownAssistant)
						rendered := renderPersistentStyle(assistantStyle, renderedMarkdown)
						b.WriteString(rendered)
						b.WriteString("\n")
						line += lineCount(rendered)
					}
				}
			}
		}
	}

	m.renderQueuedSteering(&b, &line)

	return b.String(), regions
}

func (m model) renderAssistantBlockSeparator(b *strings.Builder, line *int, renderedBlock *bool) {
	if !*renderedBlock {
		*renderedBlock = true
		return
	}
	b.WriteString("\n")
	(*line)++
}

func (m model) renderQueuedSteering(b *strings.Builder, line *int) {
	if len(m.queuedSteering) == 0 && strings.TrimSpace(m.steerError) == "" {
		return
	}

	b.WriteString("\n")
	(*line)++
	for _, message := range m.queuedSteering {
		rendered := steeringStyle.Render("↳ queued steering: " + wrapText(strings.TrimSpace(message), m.transcriptTextWidth()-20))
		b.WriteString(rendered)
		b.WriteString("\n")
		*line += lineCount(rendered)
	}
	if trimmed := strings.TrimSpace(m.steerError); trimmed != "" {
		rendered := steeringErrorStyle.Render("⚠ " + wrapText(trimmed, m.transcriptTextWidth()-2))
		b.WriteString(rendered)
		b.WriteString("\n")
		*line += lineCount(rendered)
	}
}

func (m model) renderThoughtHeader(block assistantBlock) string {
	active := hasActiveThought(block)
	count := len(block.thoughts)
	word := "Thoughts"
	if count == 1 {
		word = "Thought"
	}
	if active {
		return thoughtHeaderStyle.Render(fmt.Sprintf("%s Thinking… ▾", m.spinner.View()))
	}
	chevron := "▸"
	if block.expanded {
		chevron = "▾"
	}
	return thoughtHeaderStyle.Render(fmt.Sprintf("✓ Had %d %s %s", count, word, chevron))
}

func (m model) renderToolGroupHeader(group toolRenderGroup) string {
	if group.active {
		return toolHeaderStyle.Render(fmt.Sprintf("%s %s… ▾", m.spinner.View(), group.runningLabel))
	}

	chevron := "▸"
	if group.expanded {
		chevron = "▾"
	}
	return toolHeaderStyle.Render(fmt.Sprintf("✓ %s %s", group.label, chevron))
}
