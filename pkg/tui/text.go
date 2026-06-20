package tui

import (
	"encoding/json"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func rightLabeledBorder(left, right string, width int, label string) string {
	if width <= 2 {
		return left + right
	}
	fill := []rune(strings.Repeat("─", width-2))
	label = strings.TrimSpace(label)
	if label != "" && len(fill) > 2 {
		label = " " + fitVisible(label, len(fill)-2) + " "
		labelRunes := []rune(label)
		start := len(fill) - len(labelRunes) - 1
		if start < 0 {
			start = 0
		}
		for i, r := range labelRunes {
			pos := start + i
			if pos >= len(fill) {
				break
			}
			fill[pos] = r
		}
	}
	return left + string(fill) + right
}

func padVisible(text string, width int) string {
	textWidth := lipgloss.Width(text)
	if textWidth >= width {
		return text
	}
	return text + strings.Repeat(" ", width-textWidth)
}

func fitVisible(text string, width int) string {
	if width <= 0 || lipgloss.Width(text) <= width {
		return text
	}
	if width == 1 {
		return "…"
	}
	runes := []rune(text)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > width {
		runes = runes[1:]
	}
	return "…" + string(runes)
}

func wrapText(text string, width int) string {
	width = max(10, width)
	paragraphs := strings.Split(text, "\n")
	wrapped := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		if paragraph == "" {
			wrapped = append(wrapped, "")
			continue
		}
		wrapped = append(wrapped, wrapLine(paragraph, width)...)
	}
	return strings.Join(wrapped, "\n")
}

func wrapLine(line string, width int) []string {
	words := strings.Fields(line)
	if len(words) == 0 {
		return []string{line}
	}
	lines := []string{}
	current := ""
	for _, word := range words {
		if current == "" {
			current = word
			continue
		}
		candidate := current + " " + word
		if lipgloss.Width(candidate) <= width {
			current = candidate
			continue
		}
		lines = append(lines, current)
		current = word
	}
	if current != "" {
		for lipgloss.Width(current) > width {
			chunk, rest := splitVisible(current, width)
			lines = append(lines, chunk)
			current = rest
		}
		if current != "" {
			lines = append(lines, current)
		}
	}
	return lines
}

func splitVisible(text string, width int) (string, string) {
	runes := []rune(text)
	for i := 1; i <= len(runes); i++ {
		if lipgloss.Width(string(runes[:i])) > width {
			return string(runes[:i-1]), string(runes[i-1:])
		}
	}
	return text, ""
}

func compactJSON(input string) string {
	var v any
	if err := json.Unmarshal([]byte(input), &v); err != nil {
		return strings.TrimSpace(input)
	}
	data, err := json.Marshal(v)
	if err != nil {
		return strings.TrimSpace(input)
	}
	return string(data)
}

func indentText(text, prefix string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			lines[i] = prefix
		} else {
			lines[i] = prefix + line
		}
	}
	return strings.Join(lines, "\n")
}

func lineCount(text string) int {
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}
