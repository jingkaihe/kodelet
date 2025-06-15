package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFileReadTool_GenerateSchema(t *testing.T) {
	tool := &FileReadTool{}
	schema := tool.GenerateSchema()
	assert.NotNil(t, schema)

	assert.Equal(t, "https://github.com/jingkaihe/kodelet/pkg/tools/file-read-input", string(schema.ID))
}

func TestFileReadTool_Name(t *testing.T) {
	tool := &FileReadTool{}
	assert.Equal(t, "file_read", tool.Name())
}

func TestFileReadTool_Description(t *testing.T) {
	tool := &FileReadTool{}
	desc := tool.Description()
	assert.Contains(t, desc, "Reads a file and returns its contents with line numbers")
	assert.Contains(t, desc, "file_path")
	assert.Contains(t, desc, "offset")
	assert.Contains(t, desc, "Non-zero offset is recommended for the purpose of reading large files")
}

func TestFileReadTool_ValidateInput(t *testing.T) {
	tool := &FileReadTool{}
	tests := []struct {
		name        string
		input       FileReadInput
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid input",
			input: FileReadInput{
				FilePath: "/tmp/test.txt",
				Offset:   0,
			},
			expectError: false,
		},
		{
			name: "valid input with offset",
			input: FileReadInput{
				FilePath: "/tmp/test.txt",
				Offset:   5,
			},
			expectError: false,
		},
		{
			name: "empty file path",
			input: FileReadInput{
				FilePath: "",
				Offset:   0,
			},
			expectError: true,
			errorMsg:    "file_path is required",
		},
		{
			name: "negative offset",
			input: FileReadInput{
				FilePath: "/tmp/test.txt",
				Offset:   -1,
			},
			expectError: true,
			errorMsg:    "offset must be a positive integer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input, _ := json.Marshal(tt.input)
			err := tool.ValidateInput(NewBasicState(context.TODO()), string(input))
			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestFileReadTool_Execute(t *testing.T) {
	// Create a temporary test file
	content := []byte("Line 1\nLine 2\nLine 3\nLine 4\nLine 5\n")
	tmpfile, err := os.CreateTemp("", "FileReadtest")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	tool := &FileReadTool{}

	// Test reading from the beginning
	t.Run("read from beginning", func(t *testing.T) {
		input := FileReadInput{
			FilePath: tmpfile.Name(),
			Offset:   0,
		}
		params, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), NewBasicState(context.TODO()), string(params))

		assert.False(t, result.IsError())
		assert.Contains(t, result.GetResult(), "1: Line 1")
		assert.Contains(t, result.GetResult(), "5: Line 5")
	})

	// Test reading with offset
	t.Run("read with offset", func(t *testing.T) {
		input := FileReadInput{
			FilePath: tmpfile.Name(),
			Offset:   2,
		}
		params, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), NewBasicState(context.TODO()), string(params))

		assert.False(t, result.IsError())
		assert.Contains(t, result.GetResult(), "2: Line 2")
		assert.Contains(t, result.GetResult(), "3: Line 3")
		assert.Contains(t, result.GetResult(), "5: Line 5")
		assert.NotContains(t, result.GetResult(), "1: Line 1")
	})

	// Test reading with offset beyond file length
	t.Run("offset beyond file length", func(t *testing.T) {
		input := FileReadInput{
			FilePath: tmpfile.Name(),
			Offset:   10,
		}
		params, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), NewBasicState(context.TODO()), string(params))

		assert.Contains(t, result.GetError(), "File has only 5 lines")
		assert.Empty(t, result.GetResult())
	})

	// Test reading non-existent file
	t.Run("non-existent file", func(t *testing.T) {
		input := FileReadInput{
			FilePath: "/non/existent/file.txt",
			Offset:   0,
		}
		params, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), NewBasicState(context.TODO()), string(params))

		assert.Contains(t, result.GetError(), "Failed to open file")
		assert.Empty(t, result.GetResult())
	})

	// Test with invalid JSON
	t.Run("invalid JSON", func(t *testing.T) {
		result := tool.Execute(context.Background(), NewBasicState(context.TODO()), "invalid json")
		assert.True(t, result.IsError())
		assert.Empty(t, result.GetResult())
	})
}

func TestFileReadTool_Line_Padding(t *testing.T) {
	// Create a temporary test file with 100 lines
	var content strings.Builder
	for i := 1; i <= 100; i++ {
		content.WriteString(fmt.Sprintf("Line %d\n", i))
	}

	tmpfile, err := os.CreateTemp("", "FileReadtest_padding")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.WriteString(content.String()); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	tool := &FileReadTool{}

	// Test line number padding
	t.Run("line number padding", func(t *testing.T) {
		input := FileReadInput{
			FilePath: tmpfile.Name(),
			Offset:   0,
		}
		params, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), NewBasicState(context.TODO()), string(params))

		assert.False(t, result.IsError())

		// The padding is dynamic, so the exact space count may vary
		// Let's just check that the format is correct instead of exact spacing
		assert.Contains(t, result.GetResult(), "1: Line 1")
		assert.Contains(t, result.GetResult(), "10: Line 10")
		assert.Contains(t, result.GetResult(), "100: Line 100")
	})

	// Test with offset to see if padding is calculated properly
	t.Run("padding with offset", func(t *testing.T) {
		input := FileReadInput{
			FilePath: tmpfile.Name(),
			Offset:   50,
		}
		params, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), NewBasicState(context.TODO()), string(params))

		assert.False(t, result.IsError())

		// With offset 50, line numbers should start at 50
		assert.Contains(t, result.GetResult(), "50: Line 50")
		assert.Contains(t, result.GetResult(), "51: Line 51")
		assert.Contains(t, result.GetResult(), "100: Line 100")
	})
}

func TestFileReadTool_MaxOutputBytes(t *testing.T) {
	// Create a temporary test file with large content
	var content strings.Builder
	// Create a line that will be around 1KB
	largeLine := strings.Repeat("X", 1000) + "\n"

	// Write 200 of these lines (approx 200KB, which exceeds MaxOutputBytes of 100KB)
	for i := 1; i < 200; i++ {
		content.WriteString(fmt.Sprintf("Line %d: %s", i, largeLine))
	}

	tmpfile, err := os.CreateTemp("", "FileReadtest_large")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.WriteString(content.String()); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	tool := &FileReadTool{}

	// Test with a smaller offset that still allows reading some content
	t.Run("read with offset", func(t *testing.T) {
		input := FileReadInput{
			FilePath: tmpfile.Name(),
			Offset:   5, // Skip first few lines but still read content
		}
		params, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), NewBasicState(context.TODO()), string(params))

		assert.False(t, result.IsError())
		// Verify the content starts at the correct offset
		assert.Contains(t, result.GetResult(), "5: Line 5")
	})

	// Test skipping through byte count tracking during offset scanning
	t.Run("large offset scanning with byte tracking", func(t *testing.T) {
		// The implementation stops scanning when MaxOutputBytes is reached
		// First, count how many lines are in the file
		file, err := os.Open(tmpfile.Name())
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		lineCount := 0
		for scanner.Scan() {
			lineCount++
		}

		// Use an offset that exists but is less than the total line count
		validOffset := lineCount / 2

		input := FileReadInput{
			FilePath: tmpfile.Name(),
			Offset:   validOffset,
		}
		params, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), NewBasicState(context.TODO()), string(params))

		// Since our file is large, we should see the truncated message
		assert.False(t, result.IsError())
		assert.Contains(t, result.GetResult(), fmt.Sprintf("%d: Line %d", validOffset, validOffset))
		assert.Contains(t, result.GetResult(), "truncated due to max output bytes limit")
	})
}
