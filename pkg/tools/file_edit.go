package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/osutil"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"
)

// FileEditToolResult represents the result of a file edit operation
type FileEditToolResult struct {
	filename      string
	oldText       string
	newText       string
	oldContent    string
	newContent    string
	startLine     int
	endLine       int
	replaceAll    bool
	replacedCount int
	edits         []EditInfo
	err           string
}

// EditInfo contains information about a single edit operation
type EditInfo struct {
	StartLine  int
	EndLine    int
	OldContent string
	NewContent string
}

// GetResult returns a success message
func (r *FileEditToolResult) GetResult() string {
	if r.IsError() {
		return ""
	}
	if r.replaceAll {
		return fmt.Sprintf("File %s has been edited successfully. Replaced %d occurrences", r.filename, r.replacedCount)
	}
	return fmt.Sprintf("File %s has been edited successfully", r.filename)
}

// GetError returns the error message
func (r *FileEditToolResult) GetError() string {
	return r.err
}

// IsError returns true if the result contains an error
func (r *FileEditToolResult) IsError() bool {
	return r.err != ""
}

// AssistantFacing returns the string representation for the AI assistant
func (r *FileEditToolResult) AssistantFacing() string {
	if r.IsError() {
		return tooltypes.StringifyToolResult("", r.GetError())
	}

	var result string
	if r.replaceAll && r.replacedCount > 1 {
		result = fmt.Sprintf("File %s has been edited successfully. Replaced %d occurrences", r.filename, r.replacedCount)
		if len(r.edits) > 0 {
			result += "\n\nSample edited code blocks:"
			// Show first few edits as examples
			maxEdits := minInt(3, len(r.edits))
			for i := 0; i < maxEdits; i++ {
				edit := r.edits[i]
				if edit.NewContent != "" {
					newLines := strings.Split(edit.NewContent, "\n")
					formattedEdit := osutil.ContentWithLineNumber(newLines, edit.StartLine)
					result += fmt.Sprintf("\n\nEdit %d (lines %d-%d):\n%s", i+1, edit.StartLine, edit.EndLine, formattedEdit)
				}
			}
			if len(r.edits) > maxEdits {
				result += fmt.Sprintf("\n\n... and %d more replacements", len(r.edits)-maxEdits)
			}
		}
	} else {
		formattedEdit := ""
		if r.newText != "" {
			newLines := strings.Split(r.newText, "\n")
			formattedEdit = osutil.ContentWithLineNumber(newLines, r.startLine)
		}
		result = fmt.Sprintf("File %s has been edited successfully\n\nEdited code block:\n%s", r.filename, formattedEdit)
	}
	return tooltypes.StringifyToolResult(result, "")
}

// minInt returns the minimum of two integers
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// StructuredData returns structured metadata about the file edit operation
func (r *FileEditToolResult) StructuredData() tooltypes.StructuredToolResult {
	result := tooltypes.StructuredToolResult{
		ToolName:  "file_edit",
		Success:   !r.IsError(),
		Timestamp: time.Now(),
	}

	// Detect language from file extension
	language := osutil.DetectLanguageFromPath(r.filename)

	// Create edits array for structured data
	var edits []tooltypes.Edit
	if r.replaceAll && len(r.edits) > 0 {
		// Use multiple edits for replace all
		for _, edit := range r.edits {
			edits = append(edits, tooltypes.Edit{
				StartLine:  edit.StartLine,
				EndLine:    edit.EndLine,
				OldContent: edit.OldContent,
				NewContent: edit.NewContent,
			})
		}
	} else {
		// Use single edit for normal case
		edits = []tooltypes.Edit{
			{
				StartLine:  r.startLine,
				EndLine:    r.endLine,
				OldContent: r.oldText,
				NewContent: r.newText,
			},
		}
	}

	// Always populate metadata, even for errors
	result.Metadata = &tooltypes.FileEditMetadata{
		FilePath:      r.filename,
		Language:      language,
		Edits:         edits,
		ReplaceAll:    r.replaceAll,
		ReplacedCount: r.replacedCount,
	}

	if r.IsError() {
		result.Error = r.GetError()
	}

	return result
}

// FileEditTool provides functionality to edit files by replacing text
type FileEditTool struct{}

// Name returns the name of the tool
func (t *FileEditTool) Name() string {
	return "file_edit"
}

// FileEditInput defines the input parameters for the file_edit tool
type FileEditInput struct {
	FilePath   string `json:"file_path" jsonschema:"description=The absolute path of the file to edit"`
	OldText    string `json:"old_text" jsonschema:"description=The text to be replaced"`
	NewText    string `json:"new_text" jsonschema:"description=The text to replace the old text with"`
	ReplaceAll bool   `json:"replace_all" jsonschema:"description=If true, replace all occurrences of old_text; if false (default), old_text must be unique"`
}

// GenerateSchema generates the JSON schema for the tool's input parameters
func (t *FileEditTool) GenerateSchema() *jsonschema.Schema {
	return GenerateSchema[FileEditInput]()
}

// Description returns the description of the tool
func (t *FileEditTool) Description() string {
	return `Edit a file by replacing old text with new text.

If you are creating a new file, you can use the "FileWrite" tool to create instead of using this tool.

This tool takes four parameters:
- file_path: The absolute path of the file to edit
- old_text: The text to be replaced - The text must exactly match the text block in the file including the spaces and indentations.
- new_text: The text to replace the old text with
- replace_all: (optional, default: false) If true, replace all occurrences of old_text; if false, old_text must be unique

# RULES
## Read before editing
You must read the file using the "FileRead" tool before making any edits.

## Validate after edit
If the text edit is code or configuration related, you are encouraged to validate the edit via running the linting tool using bash.

## Replace All vs Unique text
**Default behavior (replace_all: false):**
The old text must be unique in the file. To ensure the uniqueness of the old text, make sure:
* Include the 3-5 lines BEFORE the block of text to be replaced.
* Include the 3-5 lines AFTER the block of text to be replaced.
* Any spaces and indentations must be honoured.
This tool will only edit one occurrence of the old text.

**Replace All behavior (replace_all: true):**
When replace_all is set to true, the tool will replace ALL occurrences of the old_text in the file.
This is useful for:
* Renaming variables or function names across a file
* Updating repeated patterns throughout the code
* Bulk text replacements

If you have multiple different text blocks to be replaced, you can call this tool multiple times in a single message.
`
}

// TracingKVs returns tracing key-value pairs for observability
func (t *FileEditTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	input := &FileEditInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return nil, err
	}

	return []attribute.KeyValue{
		attribute.String("file_path", input.FilePath),
		attribute.String("old_text", input.OldText),
		attribute.String("new_text", input.NewText),
		attribute.Bool("replace_all", input.ReplaceAll),
	}, nil
}

// ValidateInput validates the input parameters for the tool
func (t *FileEditTool) ValidateInput(state tooltypes.State, parameters string) error {
	var input FileEditInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return errors.Wrap(err, "invalid input")
	}

	// check if the file exists
	_, err := os.Stat(input.FilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return errors.Errorf("file %s does not exist, use the 'FileWrite' tool to create instead", input.FilePath)
		}
		return errors.Wrap(err, "failed to check the file status")
	}

	// Check if file has been read before (required for single edits, not for replaceAll)
	// replaceAll is a declarative global replacement that doesn't depend on prior context
	if !input.ReplaceAll {
		_, err = state.GetFileLastAccessed(input.FilePath)
		if err != nil {
			return errors.Wrap(err, "failed to get the last access time of the file")
		}
	}

	// Note: The mtime check is performed inside Execute() within the locked section
	// to support parallel file edits to the same file. This prevents race conditions
	// where multiple edits are queued and would fail validation after the first edit
	// updates the file's mtime.

	// check if the old text exists (basic check, detailed check in Execute with lock)
	content, err := os.ReadFile(input.FilePath)
	if err != nil {
		return errors.Wrap(err, "failed to read the file")
	}

	oldText := input.OldText

	if !strings.Contains(string(content), oldText) {
		return errors.New("old text not found in the file, please ensure the text exists")
	}

	// Count occurrences to ensure uniqueness (only if not replacing all)
	if !input.ReplaceAll {
		occurrences := strings.Count(string(content), oldText)
		if occurrences > 1 {
			return errors.Errorf("old text appears %d times in the file, please ensure the old text is unique or set replace_all to true", occurrences)
		}
	}

	return nil
}

// findAllOccurrences finds all occurrences of oldText in content and returns their positions and line numbers
func findAllOccurrences(content, oldText string) []EditInfo {
	var edits []EditInfo
	lines := strings.Split(content, "\n")
	oldTextLines := strings.Split(oldText, "\n")

	// Find all occurrences line by line
	for i := 0; i <= len(lines)-len(oldTextLines); i++ {
		match := true
		for j, oldLine := range oldTextLines {
			if i+j >= len(lines) || lines[i+j] != oldLine {
				match = false
				break
			}
		}
		if match {
			startLine := i + 1 // 1-indexed
			endLine := i + len(oldTextLines)
			edits = append(edits, EditInfo{
				StartLine:  startLine,
				EndLine:    endLine,
				OldContent: oldText,
				NewContent: "",
			})
			// Skip to avoid overlapping matches
			i += len(oldTextLines) - 1
		}
	}

	// If no exact line matches found, fall back to simple string search
	if len(edits) == 0 {
		searchText := content
		startPos := 0
		for {
			pos := strings.Index(searchText, oldText)
			if pos == -1 {
				break
			}

			// Find line number for this position
			beforeText := content[:startPos+pos]
			lineNum := strings.Count(beforeText, "\n") + 1

			edits = append(edits, EditInfo{
				StartLine:  lineNum,
				EndLine:    lineNum + strings.Count(oldText, "\n"),
				OldContent: oldText,
				NewContent: "",
			})

			// Move past this match
			startPos += pos + len(oldText)
			searchText = content[startPos:]
		}
	}

	return edits
}

// findLineNumbers finds the start and end line numbers for the given old text in the content
func findLineNumbers(content, oldText string) (int, int) {
	lines := strings.Split(content, "\n")
	oldTextLines := strings.Split(oldText, "\n")

	// Find the starting line index
	startLineIdx := -1
	for i := 0; i <= len(lines)-len(oldTextLines); i++ {
		match := true
		for j, oldLine := range oldTextLines {
			if i+j >= len(lines) || lines[i+j] != oldLine {
				match = false
				break
			}
		}
		if match {
			startLineIdx = i
			break
		}
	}

	if startLineIdx == -1 {
		// Fallback: try to find at least the first line
		for i, line := range lines {
			if strings.Contains(line, strings.Split(oldText, "\n")[0]) {
				startLineIdx = i
				break
			}
		}
	}

	// If still not found, default to line 0
	if startLineIdx == -1 {
		startLineIdx = 0
	}

	// Calculate end line (1-indexed)
	startLine := startLineIdx + 1
	endLine := startLineIdx + len(oldTextLines)

	return startLine, endLine
}

// FormatEditedBlock formats the edited text block with line numbers,
// using the original content and old text to find the starting line number.
func FormatEditedBlock(originalContent, oldText, newText string) string {
	// If new text is empty, return empty string as there's nothing to format
	if newText == "" {
		return ""
	}

	// Get the starting position of the edited block
	originalLines := strings.Split(originalContent, "\n")
	oldBlockStartIdx := 0

	// Find the starting line of the old text block
	oldTextStart := strings.Split(oldText, "\n")[0]
	if oldTextStart == "" && len(strings.Split(oldText, "\n")) > 1 {
		// Handle case where old text starts with a newline
		oldTextStart = strings.Split(oldText, "\n")[1]
	}

	found := false
	for i, line := range originalLines {
		if strings.Contains(line, oldTextStart) {
			oldBlockStartIdx = i
			found = true
			break
		}
	}

	// If we couldn't find the exact line, default to line 1
	if !found {
		oldBlockStartIdx = 0
	}

	// Get the edited text lines
	editedLines := strings.Split(newText, "\n")

	// Format with line numbers starting from the original position
	return osutil.ContentWithLineNumber(editedLines, oldBlockStartIdx+1)
}

// Execute performs the file edit operation
func (t *FileEditTool) Execute(_ context.Context, state tooltypes.State, parameters string) tooltypes.ToolResult {
	var input FileEditInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return &FileEditToolResult{
			filename: input.FilePath,
			err:      fmt.Sprintf("invalid input: %s", err),
		}
	}

	// Lock the file to prevent race conditions during read-modify-write
	state.LockFile(input.FilePath)
	defer state.UnlockFile(input.FilePath)

	// Check if file has been modified since last read (inside lock for atomicity)
	// This check is here instead of ValidateInput to support parallel file edits
	info, err := os.Stat(input.FilePath)
	if err != nil {
		return &FileEditToolResult{
			filename: input.FilePath,
			err:      fmt.Sprintf("failed to stat the file: %s", err),
		}
	}
	lastAccessed := info.ModTime()
	// Skip last access check for replaceAll since it's a declarative global replacement
	// that doesn't depend on positional context
	if !input.ReplaceAll {
		lastRead, err := state.GetFileLastAccessed(input.FilePath)
		if err != nil {
			return &FileEditToolResult{
				filename: input.FilePath,
				err:      fmt.Sprintf("failed to get the last access time: %s", err),
			}
		}
		if lastAccessed.After(lastRead) {
			return &FileEditToolResult{
				filename: input.FilePath,
				err:      fmt.Sprintf("file %s has been modified since the last read either by another tool or by the user, please read the file again", input.FilePath),
			}
		}
	}

	b, err := os.ReadFile(input.FilePath)
	if err != nil {
		return &FileEditToolResult{
			filename: input.FilePath,
			err:      fmt.Sprintf("failed to read the file: %s", err),
		}
	}

	originalContent := string(b)
	oldText := input.OldText
	newText := input.NewText
	replaceAll := input.ReplaceAll

	var content string
	var replacedCount int
	var edits []EditInfo
	var startLine, endLine int

	if replaceAll {
		// Find all occurrences and replace them
		allOccurrences := findAllOccurrences(originalContent, oldText)
		replacedCount = len(allOccurrences)

		// Update edits with new content
		for i := range allOccurrences {
			allOccurrences[i].NewContent = newText
		}
		edits = allOccurrences

		// Replace all occurrences
		content = strings.ReplaceAll(originalContent, oldText, newText)

		// For single edit compatibility, use first occurrence for startLine/endLine
		if len(allOccurrences) > 0 {
			startLine = allOccurrences[0].StartLine
			endLine = allOccurrences[0].EndLine
		} else {
			startLine, endLine = findLineNumbers(originalContent, oldText)
		}
	} else {
		// Find the line numbers for the old text (single occurrence)
		startLine, endLine = findLineNumbers(originalContent, oldText)

		// Replace only the first occurrence
		content = strings.Replace(originalContent, oldText, newText, 1)
		replacedCount = 1

		// Create single edit info
		edits = []EditInfo{
			{
				StartLine:  startLine,
				EndLine:    endLine,
				OldContent: oldText,
				NewContent: newText,
			},
		}
	}

	err = os.WriteFile(input.FilePath, []byte(content), 0o644)
	if err != nil {
		return &FileEditToolResult{
			filename: input.FilePath,
			err:      fmt.Sprintf("failed to write the file: %s", err),
		}
	}
	state.SetFileLastAccessed(input.FilePath, time.Now())

	return &FileEditToolResult{
		filename:      input.FilePath,
		oldText:       oldText,
		newText:       newText,
		oldContent:    originalContent,
		newContent:    content,
		startLine:     startLine,
		endLine:       endLine,
		replaceAll:    replaceAll,
		replacedCount: replacedCount,
		edits:         edits,
	}
}
