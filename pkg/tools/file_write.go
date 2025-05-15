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

type FileWriteTool struct{}

func (t *FileWriteTool) Name() string {
	return "file_write"
}

type FileWriteInput struct {
	FilePath string `json:"file_path" jsonschema:"description=The absolute path of the file to write,required"`
	Text     string `json:"text" jsonschema:"description=The text of the file MUST BE provided"`
}

func (t *FileWriteTool) GenerateSchema() *jsonschema.Schema {
	return GenerateSchema[FileWriteInput]()
}

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

func (t *FileWriteTool) ValidateInput(state tooltypes.State, parameters string) error {
	var input FileWriteInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return fmt.Errorf("invalid input: %w", err)
	}

	if input.Text == "" {
		return fmt.Errorf("text is required. run 'touch' command to create an empty file")
	}

	// check if the file exists
	info, err := os.Stat(input.FilePath)
	if err != nil {
		if os.IsNotExist(err) {
			// if the file does not exist, we can create it
			return nil
		}
		return fmt.Errorf("failed to check the file status: %w", err)
	}

	// get the last modified time of the file
	lastAccessed := info.ModTime()
	lastRead, err := state.GetFileLastAccessed(input.FilePath)
	if err != nil {
		return fmt.Errorf("failed to get the last access time of the file: %w", err)
	}

	if lastAccessed.After(lastRead) {
		return fmt.Errorf("file %s has been modified since the last read either by another tool or by the user, please read the file again", input.FilePath)
	}

	return nil
}

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

func (t *FileWriteTool) Execute(ctx context.Context, state tooltypes.State, parameters string) tooltypes.ToolResult {
	var input FileWriteInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return tooltypes.ToolResult{Error: fmt.Sprintf("invalid input: %s", err.Error())}
	}

	state.SetFileLastAccessed(input.FilePath, time.Now())

	err := os.WriteFile(input.FilePath, []byte(input.Text), 0644)
	if err != nil {
		return tooltypes.ToolResult{Error: fmt.Sprintf("failed to write the file: %s", err.Error())}
	}

	lines := strings.Split(input.Text, "\n")
	textWithLineNumber := utils.ContentWithLineNumber(lines, 0)

	result := fmt.Sprintf(`file %s has been written successfully

%s`, input.FilePath, textWithLineNumber)

	return tooltypes.ToolResult{Result: result}
}
