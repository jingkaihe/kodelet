package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/state"
	"github.com/jingkaihe/kodelet/pkg/utils"
)

const (
	MaxOutputBytes = 100_000 // 100KB
)

type FileReadTool struct{}

type FileReadInput struct {
	FilePath string `json:"file_path" jsonschema:"description=The absolute path of the file to read"`
	Offset   int    `json:"offset" jsonschema:"description=The 0-indexed line number to start reading from,default=0"`
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
- offset: The 0-indexed line number to start reading from (default: 0)

Non-zero offset is recommended for the purpose of reading large files.

The result will include line numbers padded appropriately, followed by the content of each line.
Example:

---

  0: def hello():
  1:    print("Hello world")
...
100:  print(hello)

---
`
}

func (r *FileReadTool) ValidateInput(state state.State, parameters string) error {
	input := &FileReadInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return err
	}

	if input.FilePath == "" {
		return errors.New("file_path is required")
	}

	if input.Offset < 0 {
		return errors.New("offset must be a non-negative integer")
	}

	return nil
}

func (r *FileReadTool) Execute(ctx context.Context, state state.State, parameters string) ToolResult {
	input := &FileReadInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return ToolResult{
			Error: err.Error(),
		}
	}

	state.SetFileLastAccessed(input.FilePath, time.Now())

	file, err := os.Open(input.FilePath)
	if err != nil {
		return ToolResult{
			Error: fmt.Sprintf("Failed to open file: %s", err.Error()),
		}
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)

	// Skip lines before offset
	lineCount := 0
	bytesRead := 0
	for lineCount < input.Offset && bytesRead < MaxOutputBytes && scanner.Scan() {
		lineCount++
		bytesRead += len(scanner.Bytes())
	}

	if lineCount < input.Offset {
		return ToolResult{
			Error: fmt.Sprintf("File has only %d lines, which is less than the requested offset %d", lineCount, input.Offset),
		}
	}

	// Read and buffer content
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	result := utils.ContentWithLineNumber(lines, input.Offset)

	if bytesRead > MaxOutputBytes {
		result += fmt.Sprintf("\n\n... [truncated due to max output bytes limit of %d]", MaxOutputBytes)
	}

	if err := scanner.Err(); err != nil {
		return ToolResult{
			Error: fmt.Sprintf("Error reading file: %s", err.Error()),
		}
	}

	return ToolResult{
		Result: result,
	}
}
