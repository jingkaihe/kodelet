// Package llmstxt provides access to the embedded llms.txt content.
// This file contains LLM-friendly documentation about kodelet usage.
package llmstxt

import (
	_ "embed"
)

//go:embed llms.txt
var content string

// GetContent returns the full llms.txt content.
func GetContent() string {
	return content
}
