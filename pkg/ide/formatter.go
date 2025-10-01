package ide

import (
	"fmt"
	"path/filepath"
	"strings"
)

// FormatContextPrompt converts IDE context into a prompt string
func FormatContextPrompt(context *IDEContext) string {
	if context == nil {
		return ""
	}

	var prompt strings.Builder

	if len(context.OpenFiles) > 0 {
		prompt.WriteString("\n## Currently Open Files in IDE\n")
		for _, file := range context.OpenFiles {
			if file.Language != "" {
				prompt.WriteString(fmt.Sprintf("- %s (%s)\n", file.Path, file.Language))
			} else {
				prompt.WriteString(fmt.Sprintf("- %s\n", file.Path))
			}
		}
	}

	if context.Selection != nil {
		prompt.WriteString("\n## Currently Selected Code in IDE\n")
		prompt.WriteString(fmt.Sprintf("File: %s (lines %d-%d)\n```\n%s\n```\n",
			context.Selection.FilePath,
			context.Selection.StartLine,
			context.Selection.EndLine,
			context.Selection.Content))
	}

	if len(context.Diagnostics) > 0 {
		prompt.WriteString("\n## IDE Diagnostics\n")

		// Group by severity: errors, warnings, others
		errors := []DiagnosticInfo{}
		warnings := []DiagnosticInfo{}
		others := []DiagnosticInfo{}

		for _, diag := range context.Diagnostics {
			switch diag.Severity {
			case "error":
				errors = append(errors, diag)
			case "warning":
				warnings = append(warnings, diag)
			default:
				others = append(others, diag)
			}
		}

		if len(errors) > 0 {
			prompt.WriteString("\n### Errors\n")
			for _, diag := range errors {
				// Format: file:line:col - [source/code] message
				sourceCode := ""
				if diag.Source != "" && diag.Code != "" {
					sourceCode = fmt.Sprintf("[%s/%s] ", diag.Source, diag.Code)
				} else if diag.Source != "" {
					sourceCode = fmt.Sprintf("[%s] ", diag.Source)
				} else if diag.Code != "" {
					sourceCode = fmt.Sprintf("[%s] ", diag.Code)
				}

				prompt.WriteString(fmt.Sprintf("- %s:%d:%d - %s%s\n",
					filepath.Base(diag.FilePath), diag.Line, diag.Column,
					sourceCode, diag.Message))
			}
		}

		if len(warnings) > 0 {
			prompt.WriteString("\n### Warnings\n")
			for _, diag := range warnings {
				sourceCode := ""
				if diag.Source != "" && diag.Code != "" {
					sourceCode = fmt.Sprintf("[%s/%s] ", diag.Source, diag.Code)
				} else if diag.Source != "" {
					sourceCode = fmt.Sprintf("[%s] ", diag.Source)
				} else if diag.Code != "" {
					sourceCode = fmt.Sprintf("[%s] ", diag.Code)
				}

				prompt.WriteString(fmt.Sprintf("- %s:%d:%d - %s%s\n",
					filepath.Base(diag.FilePath), diag.Line, diag.Column,
					sourceCode, diag.Message))
			}
		}

		if len(others) > 0 {
			prompt.WriteString("\n### Other Diagnostics\n")
			for _, diag := range others {
				sourceCode := ""
				if diag.Source != "" && diag.Code != "" {
					sourceCode = fmt.Sprintf("[%s/%s] ", diag.Source, diag.Code)
				} else if diag.Source != "" {
					sourceCode = fmt.Sprintf("[%s] ", diag.Source)
				} else if diag.Code != "" {
					sourceCode = fmt.Sprintf("[%s] ", diag.Code)
				}

				prompt.WriteString(fmt.Sprintf("- %s:%d:%d - %s%s\n",
					filepath.Base(diag.FilePath), diag.Line, diag.Column,
					sourceCode, diag.Message))
			}
		}
	}

	return prompt.String()
}
