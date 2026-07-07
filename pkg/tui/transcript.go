package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/jingkaihe/kodelet/pkg/diffview"
)

func (m *model) renderTranscript() (string, []detailRegion) {
	var b strings.Builder
	regions := []detailRegion{}
	line := 0

	if len(m.entries) == 0 {
		introLines := m.renderInitialMessage()
		for _, introLine := range introLines {
			b.WriteString(introLine)
			b.WriteString("\n")
		}
		line += len(introLines)
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
							if len(group.bodyLines) > 0 {
								indentedLines := make([]diffview.RenderedLine, 0, len(group.bodyLines))
								for _, line := range group.bodyLines {
									line.Text = "  " + line.Text
									indentedLines = append(indentedLines, line)
								}
								rendered := renderDiffRenderedLines(indentedLines)
								b.WriteString(rendered)
								b.WriteString("\n")
								line += lineCount(rendered)
								continue
							}
							body = indentText(body, "  ")
							if strings.TrimSpace(body) != "" {
								rendered := toolBodyStyle.Render(body)
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

func (m model) renderInitialMessage() []string {
	width := m.contentWidth()
	if m.viewport.Width > 0 {
		width = m.viewport.Width
	}
	width = max(1, width)
	height := max(2, m.viewport.Height)

	messageText := "Hello! What would you like me to work on?"
	messageStart := max(0, (width-lipgloss.Width(messageText))/2)
	message := renderPersistentStyle(assistantStyle, centerVisible(messageText, width))
	shortcutHint := renderInitialShortcutHint(width, messageStart)
	contentLines := []string{message, "", "", shortcutHint}

	lines := make([]string, 0, height)
	start := max(0, (height-len(contentLines))/2)
	for range start {
		lines = append(lines, "")
	}
	lines = append(lines, contentLines...)
	return lines
}

func renderInitialShortcutHint(width, start int) string {
	if width <= 0 {
		return ""
	}
	if start >= width {
		start = 0
	}
	text := fitVisible("? for shortcuts", width-start)
	if !strings.HasPrefix(text, "?") {
		return padVisible(strings.Repeat(" ", start)+renderPersistentStyle(mutedStyle, text), width)
	}
	hint := renderPersistentStyle(assistantStyle, "?") + renderPersistentStyle(mutedStyle, strings.TrimPrefix(text, "?"))
	return padVisible(strings.Repeat(" ", start)+hint, width)
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
