package renderers

import (
	"strings"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// BrowserRenderer renders browser actions without exposing raw tool-input JSON.
type BrowserRenderer struct{}

// BrowserActionLabel provides stable action wording for CLI and TUI summaries.
func BrowserActionLabel(meta tools.BrowserMetadata) string {
	label, detail := "Browser", ""
	switch meta.Action {
	case "open":
		label = "Browser: Open"
	case "navigate":
		label, detail = "Browser: Go to", meta.URL
	case "screenshot":
		label, detail = "Browser: Screenshot", meta.Path
	case "evaluate":
		label = "Browser: Run code"
	case "stop":
		label = "Browser: Stop"
	}
	if detail = strings.Join(strings.Fields(detail), " "); detail != "" {
		label += " " + detail
	}
	return label
}

// RenderCLI keeps evaluation code, output, and errors visible. Image links are
// rendered once by the registry, rather than repeating the image-reader output.
func (*BrowserRenderer) RenderCLI(result tools.StructuredToolResult) string {
	var meta tools.BrowserMetadata
	tools.ExtractMetadata(result.Metadata, &meta)
	parts := []string{BrowserActionLabel(meta)}
	if meta.Action == "evaluate" && strings.TrimSpace(meta.Expression) != "" {
		parts = append(parts, meta.Expression)
	}
	if meta.Action == "stop" && meta.SessionID != "" {
		parts = append(parts, "Session: "+meta.SessionID)
	}
	if meta.Action != "screenshot" && strings.TrimSpace(meta.Output) != "" && meta.Output != result.Error {
		parts = append(parts, meta.Output)
	}
	if result.Error != "" {
		parts = append(parts, "Error: "+result.Error)
	}
	return strings.Join(parts, "\n\n")
}
