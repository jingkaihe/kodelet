package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
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
	filename         string
	lines            []string
	offset           int
	lineLimit        int
	remainingLines   int
	truncationReason string
	err              string
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

func (r *FileReadToolResult) StructuredData() tooltypes.StructuredToolResult {
	result := tooltypes.StructuredToolResult{
		ToolName:  "file_read",
		Success:   !r.IsError(),
		Timestamp: time.Now(),
	}

	// Check if content was truncated (either by bytes or line limit)
	truncated := len(r.lines) > 0 && (strings.Contains(r.lines[len(r.lines)-1], "truncated") || strings.Contains(r.lines[len(r.lines)-1], "lines remaining"))

	// Detect language from file extension
	language := utils.DetectLanguageFromPath(r.filename)

	// Always populate metadata, even for errors
	result.Metadata = &tooltypes.FileReadMetadata{
		FilePath:       r.filename,
		Offset:         r.offset,
		LineLimit:      r.lineLimit,
		Lines:          r.lines,
		Language:       language,
		Truncated:      truncated,
		RemainingLines: r.remainingLines,
	}

	if r.IsError() {
		result.Error = r.GetError()
	}

	return result
}

type FileReadTool struct{}

type FileReadInput struct {
	FilePath  string `json:"file_path" jsonschema:"description=The absolute path of the file to read"`
	Offset    int    `json:"offset" jsonschema:"description=The 1-indexed line number to start reading from,default=1,minimum=1"`
	LineLimit int    `json:"line_limit" jsonschema:"description=The maximum number of lines to read from the offset,default=2000,minimum=1,maximum=2000"`
}

func (r *FileReadTool) GenerateSchema() *jsonschema.Schema {
	return GenerateSchema[FileReadInput]()
}

func (r *FileReadTool) Name() string {
	return "file_read"
}

func (r *FileReadTool) Description() string {
	return `Reads a file and returns its contents with line numbers.

This tool takes three parameters:
- file_path: The absolute path of the file to read
- offset: The 1-indexed line number to start reading from (default: 1, minimum: 1)
- line_limit: The maximum number of lines to read from the offset (default: 2000, minimum: 1, maximum: 2000)

Non-zero offset is recommended for the purpose of reading large files.

The result will include line numbers padded appropriately, followed by the content of each line.
If there are more lines beyond the line limit, a truncation message will be shown with the exact count of remaining lines.

Example:

---

  1: def hello():
  2:    print("Hello world")
...
101:  print(hello)

... [150 lines remaining - use offset=102 to continue reading]

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

	// Set default line limit if not provided
	if input.LineLimit == 0 {
		input.LineLimit = 2000
	}

	if input.LineLimit > 2000 {
		return errors.New("line_limit cannot exceed 2000")
	}

	return nil
}

func (r *FileReadTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	input := &FileReadInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return nil, err
	}

	// Set default line limit if not provided
	if input.LineLimit == 0 {
		input.LineLimit = 2000
	}

	return []attribute.KeyValue{
		attribute.String("file_path", input.FilePath),
		attribute.Int("offset", input.Offset),
		attribute.Int("line_limit", input.LineLimit),
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

	// Set default line limit if not provided
	if input.LineLimit == 0 {
		input.LineLimit = 2000
	}

	state.SetFileLastAccessed(input.FilePath, time.Now())

	file, err := os.Open(input.FilePath)
	if err != nil {
		return &FileReadToolResult{
			filename:  input.FilePath,
			err:       fmt.Sprintf("Failed to open file: %s", err.Error()),
			lineLimit: input.LineLimit,
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
			filename:  input.FilePath,
			err:       fmt.Sprintf("File has only %d lines, which is less than the requested offset %d", lineCount-1, input.Offset),
			lineLimit: input.LineLimit,
		}
	}

	// Read and buffer content with line limit
	bytesRead := 0
	linesRead := 0
	var lines []string
	
	for linesRead < input.LineLimit && scanner.Scan() {
		lineBytes := scanner.Bytes()
		// Check if adding this line would exceed the byte limit
		if bytesRead + len(lineBytes) > MaxOutputBytes {
			// This line would exceed the limit, so stop here
			break
		}
		lines = append(lines, scanner.Text())
		bytesRead += len(lineBytes)
		linesRead++
	}

	// Determine why we stopped reading
	hitByteLimit := false
	if linesRead < input.LineLimit {
		// We didn't reach the line limit, so either we hit byte limit or end of file
		// Check if there's more content
		if scanner.Scan() {
			hitByteLimit = true
		}
	}

	// Count remaining lines
	remainingLines := 0
	if hitByteLimit || linesRead == input.LineLimit {
		// Count all remaining lines (including the one we just scanned if hitByteLimit)
		if hitByteLimit {
			remainingLines = 1 // The line we just scanned
		}
		for scanner.Scan() {
			remainingLines++
		}
	}

	// Add truncation messages
	var truncationReason string
	if hitByteLimit {
		truncationReason = "max output bytes"
		lines = append(lines, fmt.Sprintf("... [truncated due to max output bytes limit of %d]", MaxOutputBytes))
	} else if linesRead == input.LineLimit && remainingLines > 0 {
		truncationReason = "line limit"
		nextOffset := input.Offset + input.LineLimit
		lines = append(lines, fmt.Sprintf("... [%d lines remaining - use offset=%d to continue reading]", remainingLines, nextOffset))
	}

	if err := scanner.Err(); err != nil {
		return &FileReadToolResult{
			filename:       input.FilePath,
			err:            fmt.Sprintf("Error reading file: %s", err.Error()),
			lineLimit:      input.LineLimit,
			remainingLines: remainingLines,
		}
	}

	return &FileReadToolResult{
		filename:         input.FilePath,
		lines:           lines,
		offset:          input.Offset,
		lineLimit:       input.LineLimit,
		remainingLines:  remainingLines,
		truncationReason: truncationReason,
	}
}
