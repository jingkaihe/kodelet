package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/invopop/jsonschema"
	"go.opentelemetry.io/otel/attribute"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/jingkaihe/kodelet/pkg/utils"
)

const (
	MaxOutputBytes = 100_000 // 100KB
)

type FileReadToolResult struct {
	filename string
	lines    []string
	offset   int
	err      string
}

func (r *FileReadToolResult) GetResult() string {
	return utils.ContentWithLineNumber(r.lines, r.offset)
}

func (r *FileReadToolResult) GetError() string {
	return r.err
}

func (r *FileReadToolResult) IsError() bool {
	return r.err != ""
}

func (r *FileReadToolResult) AssistantFacing() string {
	var content string
	if !r.IsError() {
		content = utils.ContentWithLineNumber(r.lines, r.offset)
	}
	return tooltypes.StringifyToolResult(content, r.GetError())
}

func (r *FileReadToolResult) UserFacing() string {
	if r.IsError() {
		return r.GetError()
	}

	content := utils.ContentWithLineNumber(r.lines, r.offset)

	buf := bytes.NewBufferString(fmt.Sprintf("File Read: %s\n", r.filename))
	fmt.Fprintf(buf, "Offset: %d\n", r.offset)
	buf.WriteString(content)
	return buf.String()
}

type FileReadTool struct{}

type FileReadInput struct {
	FilePath string `json:"file_path" jsonschema:"description=The absolute path of the file to read"`
	Offset   int    `json:"offset" jsonschema:"description=The 1-indexed line number to start reading from,default=1,minimum=1"`
}

func (r *FileReadTool) GenerateSchema() *jsonschema.Schema {
	return GenerateSchema[FileReadInput]()
}

func (r *FileReadTool) Name() string {
	return "file_read"
}

func (r *FileReadTool) Description() string {
	return `Reads a file and returns its contents with line numbers.

This tool takes two parameters:
- file_path: The absolute path of the file to read
- offset: The 1-indexed line number to start reading from (default: 1, minimum: 1)

Non-zero offset is recommended for the purpose of reading large files.

The result will include line numbers padded appropriately, followed by the content of each line.
Example:

---

  1: def hello():
  2:    print("Hello world")
...
101:  print(hello)

---

If you need to read multiple files, use batch tool to wrap multiple read_file calls together.
`
}

func (r *FileReadTool) ValidateInput(state tooltypes.State, parameters string) error {
	input := &FileReadInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return err
	}

	if input.FilePath == "" {
		return errors.New("file_path is required")
	}

	if input.Offset < 0 {
		// sometimes offset is 0, which means the llm wants to read the whole file
		return errors.New("offset must be a positive integer")
	}

	return nil
}

func (r *FileReadTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	input := &FileReadInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return nil, err
	}

	return []attribute.KeyValue{
		attribute.String("file_path", input.FilePath),
		attribute.Int("offset", input.Offset),
	}, nil
}

func (r *FileReadTool) Execute(ctx context.Context, state tooltypes.State, parameters string) tooltypes.ToolResult {
	input := &FileReadInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return &FileReadToolResult{
			filename: input.FilePath,
			err:      err.Error(),
		}
	}

	state.SetFileLastAccessed(input.FilePath, time.Now())

	file, err := os.Open(input.FilePath)
	if err != nil {
		return &FileReadToolResult{
			filename: input.FilePath,
			err:      fmt.Sprintf("Failed to open file: %s", err.Error()),
		}
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)

	if input.Offset == 0 {
		input.Offset = 1
	}

	// Skip lines before offset
	lineCount := 1
	for lineCount < input.Offset && scanner.Scan() {
		lineCount++
	}

	if lineCount < input.Offset {
		return &FileReadToolResult{
			filename: input.FilePath,
			err:      fmt.Sprintf("File has only %d lines, which is less than the requested offset %d", lineCount-1, input.Offset),
		}
	}

	// Read and buffer content
	bytesRead := 0
	var lines []string
	for bytesRead < MaxOutputBytes && scanner.Scan() {
		lines = append(lines, scanner.Text())
		bytesRead += len(scanner.Bytes())
	}

	// result := utils.ContentWithLineNumber(lines, input.Offset)

	if bytesRead > MaxOutputBytes {
		// result += fmt.Sprintf("\n\n... [truncated due to max output bytes limit of %d]", MaxOutputBytes)
		lines = append(lines, fmt.Sprintf("... [truncated due to max output bytes limit of %d]", MaxOutputBytes))
	}

	if err := scanner.Err(); err != nil {
		return &FileReadToolResult{
			filename: input.FilePath,
			err:      fmt.Sprintf("Error reading file: %s", err.Error()),
		}
	}

	return &FileReadToolResult{
		filename: input.FilePath,
		lines:    lines,
		offset:   input.Offset,
	}
}
