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

type FileMultiEditToolResult struct {
	filename       string
	oldText        string
	newText        string
	oldContent     string
	newContent     string
	occurrence     int
	actualReplaced int
	err            string
}

func (r *FileMultiEditToolResult) GetResult() string {
	if r.IsError() {
		return ""
	}
	return fmt.Sprintf("File %s has been edited successfully. Replaced %d occurrence(s) of the text.", r.filename, r.actualReplaced)
}

func (r *FileMultiEditToolResult) GetError() string {
	return r.err
}

func (r *FileMultiEditToolResult) IsError() bool {
	return r.err != ""
}

func (r *FileMultiEditToolResult) AssistantFacing() string {
	if r.IsError() {
		return tooltypes.StringifyToolResult("", r.GetError())
	}

	formattedEdit := ""
	if r.actualReplaced > 0 && r.newText != "" {
		// Use the same FormatEditedBlock function as FileEditTool
		formattedEdit = FormatEditedBlock(r.oldContent, r.oldText, r.newText)
	}

	result := fmt.Sprintf("File %s has been edited successfully. Replaced %d occurrence(s) of the text.\n\nExample of edited code block:\n%s",
		r.filename, r.actualReplaced, formattedEdit)
	return tooltypes.StringifyToolResult(result, "")
}

type FileMultiEditTool struct{}

func (t *FileMultiEditTool) Name() string {
	return "file_multi_edit"
}

type FileMultiEditInput struct {
	FilePath   string `json:"file_path" jsonschema:"description=The absolute path of the file to edit"`
	OldText    string `json:"old_text" jsonschema:"description=The text to be replaced"`
	NewText    string `json:"new_text" jsonschema:"description=The text to replace the old text with"`
	Occurrence int    `json:"occurrence" jsonschema:"description=Number of occurrences to replace, must be greater than 0"`
}

func (t *FileMultiEditTool) GenerateSchema() *jsonschema.Schema {
	return GenerateSchema[FileMultiEditInput]()
}

func (t *FileMultiEditTool) Description() string {
	return `Edit a file by replacing multiple occurrences of old text with the new text.

You are encouraged to use this "file_multi_edit" tool instead of the "file_edit" tool for effectively editing multiple occurrences of the same text.
If you are creating a new file, you can use the "file_write" tool to create instead of using this tool.

This tool takes four parameters:
- file_path: The absolute path of the file to edit
- old_text: The text to be replaced - The text must exactly match the text block in the file including the spaces and indentations.
- new_text: The text to replace the old text with
- occurrence: Number of occurrences to replace, must be greater than 0

# RULES
## Read before editing
You must read the file using the "FileRead" tool before making any edits.

## Validate after edit
If the text edit is code or configuration related, you are encouraged to validate the edit via running the linting tool using bash.

## Multiple occurrences
This tool will edit the specified number of occurrences of the old text. If the number of occurrences in the file is less than the specified number, it will replace all occurrences and report how many were actually replaced.
`
}

func (t *FileMultiEditTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	input := &FileMultiEditInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return nil, err
	}

	return []attribute.KeyValue{
		attribute.String("file_path", input.FilePath),
		attribute.String("old_text", input.OldText),
		attribute.String("new_text", input.NewText),
		attribute.Int("occurrence", input.Occurrence),
	}, nil
}

func (t *FileMultiEditTool) ValidateInput(state tooltypes.State, parameters string) error {
	var input FileMultiEditInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return fmt.Errorf("invalid input: %w", err)
	}

	// Check if occurrence is valid
	if input.Occurrence <= 0 {
		return fmt.Errorf("occurrence must be greater than 0")
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

	// check if the old text exists in the file
	content, err := os.ReadFile(input.FilePath)
	if err != nil {
		return fmt.Errorf("failed to read the file: %w", err)
	}

	oldText := input.OldText

	if !strings.Contains(string(content), oldText) {
		return fmt.Errorf("old text not found in the file, please ensure the text exists")
	}

	// Count occurrences to ensure there are enough instances
	occurrences := strings.Count(string(content), oldText)
	if occurrences < input.Occurrence {
		return fmt.Errorf("old text appears %d times in the file, but %d occurrences were requested", occurrences, input.Occurrence)
	}

	return nil
}

func (t *FileMultiEditTool) Execute(ctx context.Context, state tooltypes.State, parameters string) tooltypes.ToolResult {
	var input FileMultiEditInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return &FileMultiEditToolResult{
			filename: input.FilePath,
			err:      fmt.Sprintf("invalid input: %s", err),
		}
	}

	b, err := os.ReadFile(input.FilePath)
	if err != nil {
		return &FileMultiEditToolResult{
			filename: input.FilePath,
			err:      fmt.Sprintf("failed to read the file: %s", err),
		}
	}

	originalContent := string(b)
	oldText := input.OldText
	newText := input.NewText
	occurrence := input.Occurrence

	// Replace the specified number of occurrences
	content := strings.Replace(originalContent, oldText, newText, occurrence)

	err = os.WriteFile(input.FilePath, []byte(content), 0644)
	if err != nil {
		return &FileMultiEditToolResult{
			filename: input.FilePath,
			err:      fmt.Sprintf("failed to write the file: %s", err),
		}
	}
	state.SetFileLastAccessed(input.FilePath, time.Now())

	// Count how many occurrences were actually replaced
	actualReplaced := strings.Count(originalContent, oldText) - strings.Count(content, oldText)

	return &FileMultiEditToolResult{
		filename:       input.FilePath,
		oldText:        oldText,
		newText:        newText,
		oldContent:     originalContent,
		newContent:     content,
		occurrence:     occurrence,
		actualReplaced: actualReplaced,
	}
}

func (r *FileMultiEditToolResult) StructuredData() tooltypes.StructuredToolResult {
	result := tooltypes.StructuredToolResult{
		ToolName:  "file_multi_edit",
		Success:   !r.IsError(),
		Timestamp: time.Now(),
	}

	// Detect language from file extension
	language := utils.DetectLanguageFromPath(r.filename)

	// For multi-edit, we only have info about the last edit
	// This tool would need restructuring to track all edits properly
	edits := []tooltypes.Edit{
		{
			StartLine:  0, // Not tracked in current structure
			EndLine:    0, // Not tracked in current structure
			OldContent: r.oldText,
			NewContent: r.newText,
		},
	}

	// Always populate metadata, even for errors
	result.Metadata = &tooltypes.FileMultiEditMetadata{
		FilePath:       r.filename,
		Edits:          edits,
		Language:       language,
		ActualReplaced: r.actualReplaced,
	}

	if r.IsError() {
		result.Error = r.GetError()
	}

	return result
}
