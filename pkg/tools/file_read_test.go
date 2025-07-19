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
	"github.com/stretchr/testify/require"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
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
	assert.Contains(t, desc, "Use these parameters only for large files when you need specific sections")
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
		{
			name: "valid input with line limit",
			input: FileReadInput{
				FilePath:  "/tmp/test.txt",
				Offset:    1,
				LineLimit: 100,
			},
			expectError: false,
		},
		{
			name: "line limit too low",
			input: FileReadInput{
				FilePath:  "/tmp/test.txt",
				Offset:    1,
				LineLimit: 0,
			},
			expectError: false, // 0 gets converted to default MaxLineLimit
		},

		{
			name: "line limit too high",
			input: FileReadInput{
				FilePath:  "/tmp/test.txt",
				Offset:    1,
				LineLimit: 2001,
			},
			expectError: true,
			errorMsg:    fmt.Sprintf("line_limit cannot exceed %d", MaxLineLimit),
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
		require.NoError(t, err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.Write(content); err != nil {
		require.NoError(t, err)
	}
	if err := tmpfile.Close(); err != nil {
		require.NoError(t, err)
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
		require.NoError(t, err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.WriteString(content.String()); err != nil {
		require.NoError(t, err)
	}
	if err := tmpfile.Close(); err != nil {
		require.NoError(t, err)
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
		require.NoError(t, err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.WriteString(content.String()); err != nil {
		require.NoError(t, err)
	}
	if err := tmpfile.Close(); err != nil {
		require.NoError(t, err)
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
			require.NoError(t, err)
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

func TestFileReadTool_LineLimit(t *testing.T) {
	// Create a temporary test file with 50 lines
	var content strings.Builder
	for i := 1; i <= 50; i++ {
		content.WriteString(fmt.Sprintf("Line %d\n", i))
	}

	tmpfile, err := os.CreateTemp("", "FileReadtest_linelimit")
	if err != nil {
		require.NoError(t, err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.WriteString(content.String()); err != nil {
		require.NoError(t, err)
	}
	if err := tmpfile.Close(); err != nil {
		require.NoError(t, err)
	}

	tool := &FileReadTool{}

	// Test default line limit (should read all 50 lines since default is MaxLineLimit)
	t.Run("default line limit", func(t *testing.T) {
		input := FileReadInput{
			FilePath: tmpfile.Name(),
			Offset:   1,
		}
		params, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), NewBasicState(context.TODO()), string(params))

		assert.False(t, result.IsError())
		assert.Contains(t, result.GetResult(), "1: Line 1")
		assert.Contains(t, result.GetResult(), "50: Line 50")
		assert.NotContains(t, result.GetResult(), "lines remaining")
	})

	// Test with line limit smaller than file size
	t.Run("line limit smaller than file", func(t *testing.T) {
		input := FileReadInput{
			FilePath:  tmpfile.Name(),
			Offset:    1,
			LineLimit: 10,
		}
		params, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), NewBasicState(context.TODO()), string(params))

		assert.False(t, result.IsError())
		assert.Contains(t, result.GetResult(), "1: Line 1")
		assert.Contains(t, result.GetResult(), "10: Line 10")
		assert.NotContains(t, result.GetResult(), "11: Line 11")
		assert.Contains(t, result.GetResult(), "40 lines remaining")
		assert.Contains(t, result.GetResult(), "use offset=11 to continue reading")
	})

	// Test with line limit and offset
	t.Run("line limit with offset", func(t *testing.T) {
		input := FileReadInput{
			FilePath:  tmpfile.Name(),
			Offset:    20,
			LineLimit: 5,
		}
		params, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), NewBasicState(context.TODO()), string(params))

		assert.False(t, result.IsError())
		assert.Contains(t, result.GetResult(), "20: Line 20")
		assert.Contains(t, result.GetResult(), "24: Line 24")
		assert.NotContains(t, result.GetResult(), "25: Line 25")
		assert.Contains(t, result.GetResult(), "26 lines remaining")
		assert.Contains(t, result.GetResult(), "use offset=25 to continue reading")
	})

	// Test line limit exactly matching remaining lines
	t.Run("line limit matches remaining lines", func(t *testing.T) {
		input := FileReadInput{
			FilePath:  tmpfile.Name(),
			Offset:    41,
			LineLimit: 10,
		}
		params, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), NewBasicState(context.TODO()), string(params))

		assert.False(t, result.IsError())
		assert.Contains(t, result.GetResult(), "41: Line 41")
		assert.Contains(t, result.GetResult(), "50: Line 50")
		assert.NotContains(t, result.GetResult(), "lines remaining")
	})

	// Test line limit larger than remaining lines
	t.Run("line limit larger than remaining lines", func(t *testing.T) {
		input := FileReadInput{
			FilePath:  tmpfile.Name(),
			Offset:    45,
			LineLimit: 20,
		}
		params, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), NewBasicState(context.TODO()), string(params))

		assert.False(t, result.IsError())
		assert.Contains(t, result.GetResult(), "45: Line 45")
		assert.Contains(t, result.GetResult(), "50: Line 50")
		assert.NotContains(t, result.GetResult(), "lines remaining")
	})
}

func TestFileReadTool_LineLimitMetadata(t *testing.T) {
	// Create a temporary test file with 20 lines
	var content strings.Builder
	for i := 1; i <= 20; i++ {
		content.WriteString(fmt.Sprintf("Line %d\n", i))
	}

	tmpfile, err := os.CreateTemp("", "FileReadtest_metadata")
	if err != nil {
		require.NoError(t, err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.WriteString(content.String()); err != nil {
		require.NoError(t, err)
	}
	if err := tmpfile.Close(); err != nil {
		require.NoError(t, err)
	}

	tool := &FileReadTool{}

	t.Run("metadata includes line limit and remaining lines", func(t *testing.T) {
		input := FileReadInput{
			FilePath:  tmpfile.Name(),
			Offset:    5,
			LineLimit: 10,
		}
		params, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), NewBasicState(context.TODO()), string(params))

		assert.False(t, result.IsError())
		structured := result.StructuredData()

		assert.Equal(t, "file_read", structured.ToolName)
		assert.True(t, structured.Success)

		// Check metadata type and extract it
		meta, ok := structured.Metadata.(*tooltypes.FileReadMetadata)
		require.True(t, ok, "Expected FileReadMetadata, got %T", structured.Metadata)

		assert.Equal(t, tmpfile.Name(), meta.FilePath)
		assert.Equal(t, 5, meta.Offset)
		assert.Equal(t, 10, meta.LineLimit)
		assert.Equal(t, 6, meta.RemainingLines) // 20 total - 5 offset - 10 read + 1 = 6 remaining
		assert.True(t, meta.Truncated)
		assert.Len(t, meta.Lines, 11) // 10 content lines + 1 truncation message
	})

	t.Run("metadata with no remaining lines", func(t *testing.T) {
		input := FileReadInput{
			FilePath:  tmpfile.Name(),
			Offset:    1,
			LineLimit: 20, // Read all lines
		}
		params, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), NewBasicState(context.TODO()), string(params))

		assert.False(t, result.IsError())
		structured := result.StructuredData()

		meta, ok := structured.Metadata.(*tooltypes.FileReadMetadata)
		require.True(t, ok)

		assert.Equal(t, 20, meta.LineLimit)
		assert.Equal(t, 0, meta.RemainingLines)
		assert.False(t, meta.Truncated)
		assert.Len(t, meta.Lines, 20) // Exactly 20 lines, no truncation message
	})
}

func TestFileReadTool_LineTruncation(t *testing.T) {
	tool := &FileReadTool{}

	t.Run(fmt.Sprintf("line exactly %d characters", MaxLineCharacterLimit), func(t *testing.T) {
		// Create a line that is exactly MaxLineCharacterLimit characters
		exactLine := strings.Repeat("A", MaxLineCharacterLimit)
		content := fmt.Sprintf("Short line\n%s\nAnother short line\n", exactLine)

		tmpfile, err := os.CreateTemp("", "FileReadtest_exactMax")
		require.NoError(t, err)
		defer os.Remove(tmpfile.Name())

		_, err = tmpfile.WriteString(content)
		require.NoError(t, err)
		require.NoError(t, tmpfile.Close())

		input := FileReadInput{
			FilePath: tmpfile.Name(),
			Offset:   1,
		}
		params, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), NewBasicState(context.TODO()), string(params))

		assert.False(t, result.IsError())
		resultContent := result.GetResult()

		// Line should not be truncated (no "...")
		assert.Contains(t, resultContent, "1: Short line")
		assert.Contains(t, resultContent, "3: Another short line")

		// The MaxLineCharacterLimit-character line should be present and not truncated
		lines := strings.Split(resultContent, "\n")
		var longLine string
		for _, line := range lines {
			if strings.HasPrefix(line, "2: ") {
				longLine = line[3:] // Remove "2: " prefix
				break
			}
		}
		assert.Equal(t, exactLine, longLine)
		assert.NotContains(t, longLine, "...")
	})

	t.Run(fmt.Sprintf("line longer than %d characters", MaxLineCharacterLimit), func(t *testing.T) {
		// Create a line that is longer than MaxLineCharacterLimit characters
		longLine := strings.Repeat("B", MaxLineCharacterLimit+1000)
		content := fmt.Sprintf("Short line\n%s\nAnother short line\n", longLine)

		tmpfile, err := os.CreateTemp("", "FileReadtest_long3000")
		require.NoError(t, err)
		defer os.Remove(tmpfile.Name())

		_, err = tmpfile.WriteString(content)
		require.NoError(t, err)
		require.NoError(t, tmpfile.Close())

		input := FileReadInput{
			FilePath: tmpfile.Name(),
			Offset:   1,
		}
		params, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), NewBasicState(context.TODO()), string(params))

		assert.False(t, result.IsError())
		resultContent := result.GetResult()

		// Find the truncated line
		lines := strings.Split(resultContent, "\n")
		var truncatedLine string
		for _, line := range lines {
			if strings.HasPrefix(line, "2: ") {
				truncatedLine = line[3:] // Remove "2: " prefix
				break
			}
		}

		// Line should be truncated to MaxLineCharacterLimit chars + "..."
		assert.Equal(t, MaxLineCharacterLimit+3, len(truncatedLine)) // MaxLineCharacterLimit + len("...")
		assert.Equal(t, strings.Repeat("B", MaxLineCharacterLimit)+"...", truncatedLine)
		assert.Contains(t, truncatedLine, "...")
		assert.True(t, strings.HasSuffix(truncatedLine, "..."))
	})

	t.Run("multiple long lines", func(t *testing.T) {
		// Create multiple lines that are longer than MaxLineCharacterLimit characters
		longLine1 := strings.Repeat("X", MaxLineCharacterLimit+500)
		longLine2 := strings.Repeat("Y", MaxLineCharacterLimit+800)
		longLine3 := strings.Repeat("Z", MaxLineCharacterLimit+200)
		content := fmt.Sprintf("%s\n%s\n%s\n", longLine1, longLine2, longLine3)

		tmpfile, err := os.CreateTemp("", "FileReadtest_multiple_long")
		require.NoError(t, err)
		defer os.Remove(tmpfile.Name())

		_, err = tmpfile.WriteString(content)
		require.NoError(t, err)
		require.NoError(t, tmpfile.Close())

		input := FileReadInput{
			FilePath: tmpfile.Name(),
			Offset:   1,
		}
		params, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), NewBasicState(context.TODO()), string(params))

		assert.False(t, result.IsError())
		resultContent := result.GetResult()

		// All lines should be truncated
		lines := strings.Split(resultContent, "\n")

		// Check first line
		var line1 string
		for _, line := range lines {
			if strings.HasPrefix(line, "1: ") {
				line1 = line[3:]
				break
			}
		}
		assert.Equal(t, strings.Repeat("X", MaxLineCharacterLimit)+"...", line1)

		// Check second line
		var line2 string
		for _, line := range lines {
			if strings.HasPrefix(line, "2: ") {
				line2 = line[3:]
				break
			}
		}
		assert.Equal(t, strings.Repeat("Y", MaxLineCharacterLimit)+"...", line2)

		// Check third line
		var line3 string
		for _, line := range lines {
			if strings.HasPrefix(line, "3: ") {
				line3 = line[3:]
				break
			}
		}
		assert.Equal(t, strings.Repeat("Z", MaxLineCharacterLimit)+"...", line3)
	})

	t.Run("line truncation with byte limit interaction", func(t *testing.T) {
		// Create many long lines to test interaction with MaxOutputBytes
		var content strings.Builder
		for i := 1; i <= 100; i++ {
			// Each line will be longer than MaxLineCharacterLimit, truncated to MaxLineCharacterLimit + "..."
			longLine := strings.Repeat(fmt.Sprintf("%d", i%10), MaxLineCharacterLimit+500)
			content.WriteString(longLine + "\n")
		}

		tmpfile, err := os.CreateTemp("", "FileReadtest_truncation_byte_limit")
		require.NoError(t, err)
		defer os.Remove(tmpfile.Name())

		_, err = tmpfile.WriteString(content.String())
		require.NoError(t, err)
		require.NoError(t, tmpfile.Close())

		input := FileReadInput{
			FilePath: tmpfile.Name(),
			Offset:   1,
		}
		params, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), NewBasicState(context.TODO()), string(params))

		assert.False(t, result.IsError())
		resultContent := result.GetResult()

		// Should hit byte limit before reading all 100 lines
		assert.Contains(t, resultContent, "truncated due to max output bytes limit")

		// Verify that all included lines are properly truncated
		lines := strings.Split(resultContent, "\n")
		for _, line := range lines {
			if strings.Contains(line, ": ") && !strings.Contains(line, "truncated") {
				// Extract the content part (after line number)
				parts := strings.SplitN(line, ": ", 2)
				if len(parts) == 2 {
					content := parts[1]
					if len(content) > MaxLineCharacterLimit {
						// Should be truncated
						assert.Equal(t, MaxLineCharacterLimit+3, len(content))
						assert.True(t, strings.HasSuffix(content, "..."))
					}
				}
			}
		}
	})

	t.Run("mixed short and long lines", func(t *testing.T) {
		// Mix of short and long lines
		content := fmt.Sprintf("Short line 1\n%s\nShort line 2\n%s\nShort line 3\n",
			strings.Repeat("L", MaxLineCharacterLimit+1000), // Long line
			strings.Repeat("M", MaxLineCharacterLimit+100))  // Another long line

		tmpfile, err := os.CreateTemp("", "FileReadtest_mixed")
		require.NoError(t, err)
		defer os.Remove(tmpfile.Name())

		_, err = tmpfile.WriteString(content)
		require.NoError(t, err)
		require.NoError(t, tmpfile.Close())

		input := FileReadInput{
			FilePath: tmpfile.Name(),
			Offset:   1,
		}
		params, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), NewBasicState(context.TODO()), string(params))

		assert.False(t, result.IsError())
		resultContent := result.GetResult()

		// Short lines should remain unchanged
		assert.Contains(t, resultContent, "1: Short line 1")
		assert.Contains(t, resultContent, "3: Short line 2")
		assert.Contains(t, resultContent, "5: Short line 3")

		// Long lines should be truncated
		lines := strings.Split(resultContent, "\n")

		// Check line 2 (first long line)
		var line2 string
		for _, line := range lines {
			if strings.HasPrefix(line, "2: ") {
				line2 = line[3:]
				break
			}
		}
		assert.Equal(t, strings.Repeat("L", MaxLineCharacterLimit)+"...", line2)

		// Check line 4 (second long line)
		var line4 string
		for _, line := range lines {
			if strings.HasPrefix(line, "4: ") {
				line4 = line[3:]
				break
			}
		}
		assert.Equal(t, strings.Repeat("M", MaxLineCharacterLimit)+"...", line4)
	})

	t.Run(fmt.Sprintf("edge case: line with exactly %d characters", MaxLineCharacterLimit+1), func(t *testing.T) {
		// Test the boundary condition
		longLine := strings.Repeat("C", MaxLineCharacterLimit+1)
		content := fmt.Sprintf("Before\n%s\nAfter\n", longLine)

		tmpfile, err := os.CreateTemp("", "FileReadtest_edge")
		require.NoError(t, err)
		defer os.Remove(tmpfile.Name())

		_, err = tmpfile.WriteString(content)
		require.NoError(t, err)
		require.NoError(t, tmpfile.Close())

		input := FileReadInput{
			FilePath: tmpfile.Name(),
			Offset:   1,
		}
		params, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), NewBasicState(context.TODO()), string(params))

		assert.False(t, result.IsError())
		resultContent := result.GetResult()

		// Find the truncated line
		lines := strings.Split(resultContent, "\n")
		var truncatedLine string
		for _, line := range lines {
			if strings.HasPrefix(line, "2: ") {
				truncatedLine = line[3:]
				break
			}
		}

		// Should be truncated to exactly MaxLineCharacterLimit chars + "..."
		assert.Equal(t, MaxLineCharacterLimit+3, len(truncatedLine))
		assert.Equal(t, strings.Repeat("C", MaxLineCharacterLimit)+"...", truncatedLine)
	})
}
