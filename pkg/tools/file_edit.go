package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/invopop/jsonschema"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/jingkaihe/kodelet/pkg/utils"
	"go.opentelemetry.io/otel/attribute"
)

type FileEditTool struct{}

func (t *FileEditTool) Name() string {
	return "file_edit"
}

type FileEditInput struct {
	FilePath string `json:"file_path" jsonschema:"description=The absolute path of the file to edit"`
	OldText  string `json:"old_text" jsonschema:"description=The unique text to be replaced"`
	NewText  string `json:"new_text" jsonschema:"description=The text to replace the old text with"`
}

func (t *FileEditTool) GenerateSchema() *jsonschema.Schema {
	return GenerateSchema[FileEditInput]()
}

func (t *FileEditTool) Description() string {
	return `Edit a file by replacing the UNIQUE old text with the new text.

If you are creating a new file, you can use the "FileWrite" tool to create instead of using this tool.

This tool takes three parameters:
- file_path: The absolute path of the file to edit
- old_text: The **UNIQUE** text to be replaced - The text must exactly match the text block in the file including the spaces and indentations.
- new_text: The text to replace the old text with

# RULES
## Read before editing
You must read the file using the "FileRead" tool before making any edits.

## Validate after edit
If the text edit is code or configuration related, you are encouraged to validate the edit via running the linting tool using bash.

## Unique text
The old text must be unique in the file. To ensure the uniqueness of the old text, make sure:
* Include the 3-5 lines BEFORE the block of text to be replaced.
* Include the 3-5 lines AFTER the block of text to be replaced.
* Any spaces and indentations must be honoured.

## Edit ONCE
!!! IMPORTANT !!! This tool will only edit one occurrence of the old text.

If you have multiple text blocks to be replaced, you can call this tool multiple times in a single message.
`
}

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
	}, nil
}

func (t *FileEditTool) ValidateInput(state tooltypes.State, parameters string) error {
	var input FileEditInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return fmt.Errorf("invalid input: %w", err)
	}

	// check if the file exists
	info, err := os.Stat(input.FilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("file %s does not exist, use the 'FileWrite' tool to create instead", input.FilePath)
		}
		return fmt.Errorf("failed to check the file status: %w", err)
	}

	lastAccessed := info.ModTime()
	lastRead, err := state.GetFileLastAccessed(input.FilePath)
	if err != nil {
		return fmt.Errorf("failed to get the last access time of the file: %w", err)
	}

	if lastAccessed.After(lastRead) {
		return fmt.Errorf("file %s has been modified since the last read either by another tool or by the user, please read the file again", input.FilePath)
	}

	// check if the old text is unique
	content, err := os.ReadFile(input.FilePath)
	if err != nil {
		return fmt.Errorf("failed to read the file: %w", err)
	}

	oldText := input.OldText

	if !strings.Contains(string(content), oldText) {
		return fmt.Errorf("old text not found in the file, please ensure the text exists")
	}

	// Count occurrences to ensure uniqueness
	occurrences := strings.Count(string(content), oldText)
	if occurrences > 1 {
		return fmt.Errorf("old text appears %d times in the file, please ensure the old text is unique", occurrences)
	}

	return nil
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
	return utils.ContentWithLineNumber(editedLines, oldBlockStartIdx+1)
}

func (t *FileEditTool) Execute(ctx context.Context, state tooltypes.State, parameters string) tooltypes.ToolResultInterface {
	var input FileEditInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return tooltypes.ToolResult{
			Error: fmt.Sprintf("invalid input: %s", err),
		}
	}

	b, err := os.ReadFile(input.FilePath)
	if err != nil {
		return tooltypes.ToolResult{
			Error: fmt.Sprintf("failed to read the file: %s", err),
		}
	}

	originalContent := string(b)
	oldText := input.OldText
	newText := input.NewText

	// Since we already validated the text exists and is unique, we can safely replace it
	content := strings.Replace(originalContent, oldText, newText, 1)

	err = os.WriteFile(input.FilePath, []byte(content), 0644)
	if err != nil {
		return tooltypes.ToolResult{
			Error: fmt.Sprintf("failed to write the file: %s", err),
		}
	}
	state.SetFileLastAccessed(input.FilePath, time.Now())

	// Format the edited block with line numbers
	formattedEdit := FormatEditedBlock(originalContent, oldText, newText)

	return tooltypes.ToolResult{
		Result: fmt.Sprintf("File %s has been edited successfully\n\nEdited code block:\n%s", input.FilePath, formattedEdit),
	}
}
