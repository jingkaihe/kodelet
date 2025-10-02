package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/invopop/jsonschema"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/jingkaihe/kodelet/pkg/utils"
)

// FileWriteToolResult represents the result of a file write operation
type FileWriteToolResult struct {
	filename string
	text     string
	err      string
}

// GetResult returns a success message
func (r *FileWriteToolResult) GetResult() string {
	lines := strings.Split(r.text, "\n")
	textWithLineNumber := utils.ContentWithLineNumber(lines, 0)
	return fmt.Sprintf(`file %s has been written successfully

%s`, r.filename, textWithLineNumber)
}

// GetError returns the error message
func (r *FileWriteToolResult) GetError() string {
	return r.err
}

// IsError returns true if the result contains an error
func (r *FileWriteToolResult) IsError() bool {
	return r.err != ""
}

// AssistantFacing returns the string representation for the AI assistant
func (r *FileWriteToolResult) AssistantFacing() string {
	var content string
	if !r.IsError() {
		content = r.GetResult()
	}
	return tooltypes.StringifyToolResult(content, r.GetError())
}

// StructuredData returns structured metadata about the file write operation
func (r *FileWriteToolResult) StructuredData() tooltypes.StructuredToolResult {
	result := tooltypes.StructuredToolResult{
		ToolName:  "file_write",
		Success:   !r.IsError(),
		Timestamp: time.Now(),
	}

	// Detect language from file extension
	language := utils.DetectLanguageFromPath(r.filename)

	// Always populate metadata, even for errors
	result.Metadata = &tooltypes.FileWriteMetadata{
		FilePath: r.filename,
		Content:  r.text,
		Size:     int64(len(r.text)),
		Language: language,
	}

	if r.IsError() {
		result.Error = r.GetError()
	}

	return result
}

// FileWriteTool provides functionality to write files
type FileWriteTool struct{}

// Name returns the name of the tool
func (t *FileWriteTool) Name() string {
	return "file_write"
}

// FileWriteInput defines the input parameters for the file_write tool
type FileWriteInput struct {
	FilePath string `json:"file_path" jsonschema:"description=The absolute path of the file to write,required"`
	Text     string `json:"text" jsonschema:"description=The text of the file MUST BE provided"`
}

// GenerateSchema generates the JSON schema for the tool's input parameters
func (t *FileWriteTool) GenerateSchema() *jsonschema.Schema {
	return GenerateSchema[FileWriteInput]()
}

// Description returns the description of the tool
func (t *FileWriteTool) Description() string {
	return `Writes a file with the given text. If the file already exists, its text will be overwritten by the new text.

This tool takes two parameters:
- file_path: The absolute path of the file to write
- text: The text to be written to the file. It must not be empty.

IMPORTANT: If you want to create an empty file, use ${bash} tool to run "touch" command instead of calling this tool.
IMPORTANT: If you are performing file overwrites, read the file using the ${read_file} tool first to get the existing text, and then append the new text to the existing text.
IMPORTANT: Make sure that the directory of the file exists before writing to it. You can verify it via running "ls" command.
By default the file is created with 0644 permissions. You can change the permissions by using ${bash} tool chmod command as a follow up.
`
}

// ValidateInput validates the input parameters for the tool
func (t *FileWriteTool) ValidateInput(state tooltypes.State, parameters string) error {
	var input FileWriteInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return errors.Wrap(err, "invalid input")
	}

	if input.Text == "" {
		return errors.New("text is required. run 'touch' command to create an empty file")
	}

	// check if the file exists
	info, err := os.Stat(input.FilePath)
	if err != nil {
		if os.IsNotExist(err) {
			// if the file does not exist, we can create it
			return nil
		}
		return errors.Wrap(err, "failed to check the file status")
	}

	// get the last modified time of the file
	lastAccessed := info.ModTime()
	lastRead, err := state.GetFileLastAccessed(input.FilePath)
	if err != nil {
		return errors.Wrap(err, "failed to get the last access time of the file")
	}

	if lastAccessed.After(lastRead) {
		return errors.Errorf("file %s has been modified since the last read either by another tool or by the user, please read the file again", input.FilePath)
	}

	return nil
}

// TracingKVs returns tracing key-value pairs for observability
func (t *FileWriteTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	input := &FileWriteInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return nil, err
	}

	return []attribute.KeyValue{
		attribute.String("file_path", input.FilePath),
		attribute.String("text", input.Text),
	}, nil
}

// Execute writes the file and returns the result
func (t *FileWriteTool) Execute(_ context.Context, state tooltypes.State, parameters string) tooltypes.ToolResult {
	var input FileWriteInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return &FileWriteToolResult{
			filename: input.FilePath,
			err:      fmt.Sprintf("invalid input: %s", err.Error()),
		}
	}

	state.SetFileLastAccessed(input.FilePath, time.Now())

	err := os.WriteFile(input.FilePath, []byte(input.Text), 0o644)
	if err != nil {
		return &FileWriteToolResult{
			filename: input.FilePath,
			err:      fmt.Sprintf("failed to write the file: %s", err.Error()),
		}
	}

	return &FileWriteToolResult{
		filename: input.FilePath,
		text:     input.Text,
	}
}
