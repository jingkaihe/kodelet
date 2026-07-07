// Package diffview renders structured unified diffs consistently across Kodelet surfaces.
package diffview

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/mattn/go-runewidth"
)

type LineKind string

const (
	LineContext LineKind = "context"
	LineAdded   LineKind = "added"
	LineRemoved LineKind = "removed"
	LineHeader  LineKind = "header"
	LineMeta    LineKind = "meta"
	LinePlain   LineKind = "plain"
)

type Line struct {
	Kind    LineKind
	OldLine int
	NewLine int
	Content string
}

type FileDiff struct {
	Path      string
	MovePath  string
	Operation string
	Added     int
	Removed   int
	Lines     []Line
}

type Summary struct {
	Files   []FileDiff
	Added   int
	Removed int
}

type RenderedLine struct {
	Kind LineKind
	Text string
}

var hunkHeaderRE = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

func FromApplyPatchMetadata(meta tooltypes.ApplyPatchMetadata) Summary {
	changes := meta.Changes
	files := make([]FileDiff, 0, len(changes))
	summary := Summary{}
	for _, change := range changes {
		file := FromApplyPatchChange(change)
		summary.Added += file.Added
		summary.Removed += file.Removed
		files = append(files, file)
	}
	summary.Files = files
	return summary
}

func FromApplyPatchChange(change tooltypes.ApplyPatchChange) FileDiff {
	file := FileDiff{
		Path:      change.Path,
		MovePath:  strings.TrimSpace(change.MovePath),
		Operation: normalizeOperation(change.Operation),
	}

	lines := linesForChange(change)
	file.Lines = lines
	for _, line := range lines {
		switch line.Kind {
		case LineAdded:
			file.Added++
		case LineRemoved:
			file.Removed++
		}
	}
	return file
}

func (f FileDiff) DisplayPath() string {
	if strings.TrimSpace(f.MovePath) != "" {
		return fmt.Sprintf("%s → %s", f.Path, f.MovePath)
	}
	return f.Path
}

func (f FileDiff) OperationLabel() string {
	if strings.TrimSpace(f.MovePath) != "" {
		return "Move"
	}
	switch normalizeOperation(f.Operation) {
	case tooltypes.ApplyPatchOperationAdd, "write":
		return "Write"
	case tooltypes.ApplyPatchOperationDelete:
		return "Delete"
	case "move":
		return "Move"
	default:
		return "Edit"
	}
}

func (f FileDiff) Header() string {
	return fmt.Sprintf("%s %s (+%d -%d)", f.OperationLabel(), f.DisplayPath(), f.Added, f.Removed)
}

func RenderSummary(summary Summary) []RenderedLine {
	if len(summary.Files) == 0 {
		return []RenderedLine{{Kind: LinePlain, Text: "No files were modified."}}
	}

	var out []RenderedLine
	for i, file := range summary.Files {
		if i > 0 {
			out = append(out, RenderedLine{Kind: LinePlain, Text: ""})
		}
		out = append(out, RenderedLine{Kind: LinePlain, Text: file.Header()})
		out = append(out, RenderFileBody(file)...)
	}
	return out
}

func RenderSummaryWidth(summary Summary, width int) []RenderedLine {
	if len(summary.Files) == 0 {
		return []RenderedLine{{Kind: LinePlain, Text: "No files were modified."}}
	}

	var out []RenderedLine
	for i, file := range summary.Files {
		if i > 0 {
			out = append(out, RenderedLine{Kind: LinePlain, Text: ""})
		}
		out = append(out, RenderedLine{Kind: LinePlain, Text: file.Header()})
		out = append(out, RenderFileBodyWidth(file, width)...)
	}
	return out
}

func RenderFileBody(file FileDiff) []RenderedLine {
	if len(file.Lines) == 0 {
		return nil
	}

	oldWidth, newWidth := lineNumberWidths(file.Lines)
	out := make([]RenderedLine, 0, len(file.Lines))
	for _, line := range file.Lines {
		out = append(out, RenderedLine{
			Kind: line.Kind,
			Text: renderLine(line, oldWidth, newWidth),
		})
	}
	return out
}

func RenderFileBodyWidth(file FileDiff, width int) []RenderedLine {
	if len(file.Lines) == 0 {
		return nil
	}
	if width <= 0 {
		return RenderFileBody(file)
	}

	oldWidth, newWidth := lineNumberWidths(file.Lines)
	var out []RenderedLine
	for _, line := range file.Lines {
		out = append(out, renderLineWrapped(line, oldWidth, newWidth, width)...)
	}
	return out
}

func RenderedText(lines []RenderedLine) string {
	parts := make([]string, 0, len(lines))
	for _, line := range lines {
		parts = append(parts, line.Text)
	}
	return strings.TrimSuffix(strings.Join(parts, "\n"), "\n")
}

func linesForChange(change tooltypes.ApplyPatchChange) []Line {
	if strings.TrimSpace(change.UnifiedDiff) != "" {
		return parseUnifiedDiff(change.UnifiedDiff)
	}
	return nil
}

func parseUnifiedDiff(diff string) []Line {
	var lines []Line
	oldLine, newLine := 0, 0
	inHunk := false

	for _, raw := range splitDiffLines(diff) {
		if !inHunk && (strings.HasPrefix(raw, "--- ") || strings.HasPrefix(raw, "+++ ")) {
			continue
		}

		if strings.HasPrefix(raw, "@@") {
			oldLine, newLine = parseHunkStart(raw)
			inHunk = true
			lines = append(lines, Line{Kind: LineHeader, Content: raw})
			continue
		}

		if strings.HasPrefix(raw, `\ No newline`) {
			lines = append(lines, Line{Kind: LineMeta, Content: raw})
			continue
		}

		if !inHunk {
			lines = append(lines, Line{Kind: LineMeta, Content: raw})
			continue
		}

		if strings.HasPrefix(raw, "+") {
			lines = append(lines, Line{Kind: LineAdded, NewLine: newLine, Content: strings.TrimPrefix(raw, "+")})
			newLine++
			continue
		}
		if strings.HasPrefix(raw, "-") {
			lines = append(lines, Line{Kind: LineRemoved, OldLine: oldLine, Content: strings.TrimPrefix(raw, "-")})
			oldLine++
			continue
		}
		if strings.HasPrefix(raw, " ") {
			lines = append(lines, Line{Kind: LineContext, OldLine: oldLine, NewLine: newLine, Content: strings.TrimPrefix(raw, " ")})
			oldLine++
			newLine++
			continue
		}

		lines = append(lines, Line{Kind: LineContext, OldLine: oldLine, NewLine: newLine, Content: raw})
		oldLine++
		newLine++
	}

	return lines
}

func parseHunkStart(header string) (int, int) {
	matches := hunkHeaderRE.FindStringSubmatch(header)
	if len(matches) != 3 {
		return 0, 0
	}
	oldLine, _ := strconv.Atoi(matches[1])
	newLine, _ := strconv.Atoi(matches[2])
	return oldLine, newLine
}

func lineNumberWidths(lines []Line) (int, int) {
	maxOld, maxNew := 0, 0
	for _, line := range lines {
		if line.OldLine > maxOld {
			maxOld = line.OldLine
		}
		if line.NewLine > maxNew {
			maxNew = line.NewLine
		}
	}
	return numberWidth(maxOld), numberWidth(maxNew)
}

func renderLine(line Line, oldWidth, newWidth int) string {
	return linePrefix(line, oldWidth, newWidth) + line.Content
}

func renderLineWrapped(line Line, oldWidth, newWidth, width int) []RenderedLine {
	firstPrefix := linePrefix(line, oldWidth, newWidth)
	continuationPrefix := continuationPrefix(oldWidth, newWidth)
	contentWidth := width - displayWidth(firstPrefix)
	if contentWidth <= 4 {
		return []RenderedLine{{Kind: line.Kind, Text: firstPrefix + line.Content}}
	}

	chunks := wrapContent(line.Content, contentWidth)
	if len(chunks) == 0 {
		chunks = []string{""}
	}

	out := make([]RenderedLine, 0, len(chunks))
	for i, chunk := range chunks {
		prefix := firstPrefix
		if i > 0 {
			prefix = continuationPrefix
		}
		out = append(out, RenderedLine{Kind: line.Kind, Text: prefix + chunk})
	}
	return out
}

func linePrefix(line Line, oldWidth, newWidth int) string {
	oldNumber := formatLineNumber(line.OldLine, oldWidth)
	newNumber := formatLineNumber(line.NewLine, newWidth)
	return fmt.Sprintf("%s %s │ %s", oldNumber, newNumber, signFor(line.Kind))
}

func continuationPrefix(oldWidth, newWidth int) string {
	return fmt.Sprintf("%s %s │  ", strings.Repeat(" ", oldWidth), strings.Repeat(" ", newWidth))
}

func signFor(kind LineKind) string {
	switch kind {
	case LineAdded:
		return "+"
	case LineRemoved:
		return "-"
	case LineMeta:
		return "›"
	default:
		return " "
	}
}

func wrapContent(content string, width int) []string {
	if content == "" {
		return []string{""}
	}
	if width <= 0 || displayWidth(content) <= width {
		return []string{content}
	}

	var chunks []string
	var current strings.Builder
	currentWidth := 0
	for _, r := range content {
		runeWidth := runeDisplayWidth(r)
		if currentWidth > 0 && currentWidth+runeWidth > width {
			chunks = append(chunks, current.String())
			current.Reset()
			currentWidth = 0
		}
		current.WriteRune(r)
		currentWidth += runeWidth
		if currentWidth >= width {
			chunks = append(chunks, current.String())
			current.Reset()
			currentWidth = 0
		}
	}
	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}
	return chunks
}

func displayWidth(value string) int {
	width := 0
	for _, r := range value {
		width += runeDisplayWidth(r)
	}
	return width
}

func runeDisplayWidth(r rune) int {
	if r == '\t' {
		return 4
	}
	width := runewidth.RuneWidth(r)
	if width <= 0 {
		return 1
	}
	return width
}

func formatLineNumber(lineNumber, width int) string {
	if lineNumber <= 0 {
		return strings.Repeat(" ", width)
	}
	return fmt.Sprintf("%*d", width, lineNumber)
}

func numberWidth(number int) int {
	if number <= 0 {
		return 1
	}
	return len(strconv.Itoa(number))
}

func normalizeOperation(operation string) string {
	operation = strings.ToLower(strings.TrimSpace(operation))
	if operation == "" {
		return tooltypes.ApplyPatchOperationUpdate
	}
	return operation
}

func splitDiffLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
