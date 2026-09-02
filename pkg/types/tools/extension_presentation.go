package tools

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxExtensionToolPresentationSummaryRunes bounds compact presentation labels.
	MaxExtensionToolPresentationSummaryRunes = 160
	// MaxExtensionToolPresentationBodyBytes bounds expanded presentation content.
	MaxExtensionToolPresentationBodyBytes = 100 * 1024
)

// ExtensionToolPresentation controls the generic user-facing rendering of an extension result.
type ExtensionToolPresentation struct {
	Summary string `json:"summary"`
	Body    string `json:"body,omitempty"`
	Format  string `json:"format,omitempty"`
	hasBody bool
}

// ExtractExtensionToolPresentation reads data.presentation from an extension tool result.
func ExtractExtensionToolPresentation(result *StructuredToolResult) (ExtensionToolPresentation, ExtensionToolMetadata, bool) {
	if result == nil {
		return ExtensionToolPresentation{}, ExtensionToolMetadata{}, false
	}

	var metadata ExtensionToolMetadata
	if !ExtractMetadata(result.Metadata, &metadata) || metadata.Data == nil {
		return ExtensionToolPresentation{}, ExtensionToolMetadata{}, false
	}
	raw, ok := metadata.Data["presentation"]
	if !ok {
		return ExtensionToolPresentation{}, metadata, false
	}
	presentation, ok := ParseExtensionToolPresentation(raw)
	if !ok {
		return ExtensionToolPresentation{}, metadata, false
	}
	return presentation, metadata, true
}

// ParseExtensionToolPresentation validates and normalizes a presentation payload.
func ParseExtensionToolPresentation(raw any) (ExtensionToolPresentation, bool) {
	payload, err := json.Marshal(raw)
	if err != nil {
		return ExtensionToolPresentation{}, false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil || fields == nil {
		return ExtensionToolPresentation{}, false
	}
	if fieldMissingOrNull(fields, "summary") || fieldIsNull(fields, "body") || fieldIsNull(fields, "format") {
		return ExtensionToolPresentation{}, false
	}
	var presentation ExtensionToolPresentation
	if err := json.Unmarshal(payload, &presentation); err != nil {
		return ExtensionToolPresentation{}, false
	}
	if strings.IndexFunc(presentation.Summary, func(r rune) bool {
		return unicode.Is(unicode.Cf, r)
	}) >= 0 {
		return ExtensionToolPresentation{}, false
	}
	presentation.Summary = strings.Join(strings.Fields(presentation.Summary), " ")
	presentation.Format = strings.ToLower(strings.TrimSpace(presentation.Format))
	if presentation.Format == "" {
		presentation.Format = "text"
	}
	_, presentation.hasBody = fields["body"]
	if !presentation.Valid() {
		return ExtensionToolPresentation{}, false
	}
	return presentation, true
}

// HasBody reports whether the presentation explicitly supplied expanded content.
func (p ExtensionToolPresentation) HasBody() bool {
	return p.hasBody || p.Body != ""
}

// Valid reports whether an extension presentation can be rendered safely.
func (p ExtensionToolPresentation) Valid() bool {
	if p.Summary == "" || utf8.RuneCountInString(p.Summary) > MaxExtensionToolPresentationSummaryRunes || len(p.Body) > MaxExtensionToolPresentationBodyBytes {
		return false
	}
	if strings.IndexFunc(p.Summary, func(r rune) bool {
		return unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
	}) >= 0 {
		return false
	}
	switch p.Format {
	case "text", "markdown":
		return true
	default:
		return false
	}
}

func fieldMissingOrNull(fields map[string]json.RawMessage, name string) bool {
	raw, ok := fields[name]
	return !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func fieldIsNull(fields map[string]json.RawMessage, name string) bool {
	raw, ok := fields[name]
	return ok && bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}
