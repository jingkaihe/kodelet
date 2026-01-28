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

	"github.com/jingkaihe/kodelet/pkg/osutil"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

const (
	// MaxOutputBytes is the maximum number of bytes to output from a file read operation
	MaxOutputBytes = 100_000 // 100KB
	// MaxLineCharacterLimit is the maximum characters per line before truncation
	MaxLineCharacterLimit = 2000
	// MaxLineLimit is the maximum number of lines that can be read at once
	MaxLineLimit = 2000
)

// FileReadToolResult represents the result of a file read operation
type FileReadToolResult struct {
	filename         string
	lines            []string
	offset           int
	lineLimit        int
	remainingLines   int
	truncationReason string
	err              string
}

// GetResult returns the file content
func (r *FileReadToolResult) GetResult() string {
	return osutil.ContentWithLineNumber(r.lines, r.offset)
}

// GetError returns the error message
func (r *FileReadToolResult) GetError() string {
	return r.err
}

// IsError returns true if the result contains an error
func (r *FileReadToolResult) IsError() bool {
	return r.err != ""
}

// AssistantFacing returns the string representation for the AI assistant
func (r *FileReadToolResult) AssistantFacing() string {
	var content string
	if !r.IsError() {
		content = osutil.ContentWithLineNumber(r.lines, r.offset)
	}
	return tooltypes.StringifyToolResult(content, r.GetError())
}

// StructuredData returns structured metadata about the file read operation
func (r *FileReadToolResult) StructuredData() tooltypes.StructuredToolResult {
	result := tooltypes.StructuredToolResult{
		ToolName:  "file_read",
		Success:   !r.IsError(),
		Timestamp: time.Now(),
	}

	// Check if content was truncated (either by bytes or line limit)
	truncated := len(r.lines) > 0 && (strings.Contains(r.lines[len(r.lines)-1], "truncated") || strings.Contains(r.lines[len(r.lines)-1], "lines remaining"))

	// Detect language from file extension
	language := osutil.DetectLanguageFromPath(r.filename)

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

// FileReadTool provides functionality to read files with line numbers
type FileReadTool struct{}

// FileReadInput defines the input parameters for the file_read tool
type FileReadInput struct {
	FilePath  string `json:"file_path" jsonschema:"description=The absolute path of the file to read"`
	Offset    int    `json:"offset" jsonschema:"description=The 1-indexed line number to start reading from"`
	LineLimit int    `json:"line_limit" jsonschema:"description=The maximum number of lines to read from the offset"`
}

// GenerateSchema generates the JSON schema for the tool's input parameters
func (r *FileReadTool) GenerateSchema() *jsonschema.Schema {
	return GenerateSchema[FileReadInput]()
}

// Name returns the name of the tool
func (r *FileReadTool) Name() string {
	return "file_read"
}

// Description returns the description of the tool
func (r *FileReadTool) Description() string {
	return `Reads a file and returns its contents with line numbers.

This tool takes three parameters:
- file_path: The absolute path of the file to read
- offset: The 1-indexed line number to start reading from (default: 1, minimum: 1)
- line_limit: The maximum number of lines to read from the offset (default: 2000, minimum: 1, maximum: 2000)

For most files, omit offset and line_limit to read the entire file. Use these parameters only for large files when you need specific sections.

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

If you need to read multiple files, use parallel tool calling to read multiple files simultaneously.
`
}

// ValidateInput validates the input parameters for the tool
func (r *FileReadTool) ValidateInput(_ tooltypes.State, parameters string) error {
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
		input.LineLimit = MaxLineLimit
	}

	if input.LineLimit > MaxLineLimit {
		return fmt.Errorf("line_limit cannot exceed %d", MaxLineLimit)
	}

	return nil
}

// TracingKVs returns tracing key-value pairs for observability
func (r *FileReadTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	input := &FileReadInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return nil, err
	}

	// Set default line limit if not provided
	if input.LineLimit == 0 {
		input.LineLimit = MaxLineLimit
	}

	return []attribute.KeyValue{
		attribute.String("file_path", input.FilePath),
		attribute.Int("offset", input.Offset),
		attribute.Int("line_limit", input.LineLimit),
	}, nil
}

// Execute reads the file and returns the result
func (r *FileReadTool) Execute(_ context.Context, state tooltypes.State, parameters string) tooltypes.ToolResult {
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
		input.LineLimit = MaxLineLimit
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
		lineText := scanner.Text()
		// Truncate line if it exceeds MaxLineCharacterLimit characters
		if len(lineText) > MaxLineCharacterLimit {
			lineText = lineText[:MaxLineCharacterLimit] + "..."
		}

		// Check if adding this (potentially truncated) line would exceed the byte limit
		lineBytes := len([]byte(lineText))
		if bytesRead+lineBytes > MaxOutputBytes {
			// This line would exceed the limit, so stop here
			break
		}

		lines = append(lines, lineText)
		bytesRead += lineBytes
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
		lines:            lines,
		offset:           input.Offset,
		lineLimit:        input.LineLimit,
		remainingLines:   remainingLines,
		truncationReason: truncationReason,
	}
}
