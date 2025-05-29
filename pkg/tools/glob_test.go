package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestGlobTool_Name(t *testing.T) {
	tool := &GlobTool{}
	assert.Equal(t, "glob_tool", tool.Name())
}

func TestGlobTool_Description(t *testing.T) {
	tool := &GlobTool{}
	assert.Contains(t, tool.Description(), "Find files matching a glob pattern")
}

func TestGlobTool_GenerateSchema(t *testing.T) {
	tool := &GlobTool{}
	schema := tool.GenerateSchema()
	assert.NotNil(t, schema)
}

func TestGlobTool_TracingKVs(t *testing.T) {
	tool := &GlobTool{}

	// Test valid input
	input := GlobInput{
		Pattern: "*.go",
		Path:    "./testdata",
	}
	inputBytes, _ := json.Marshal(input)

	kvs, err := tool.TracingKVs(string(inputBytes))
	assert.NoError(t, err)
	assert.Len(t, kvs, 2)

	// Test invalid input
	kvs, err = tool.TracingKVs("invalid json")
	assert.Error(t, err)
	assert.Nil(t, kvs)
}

func TestGlobTool_ValidateInput(t *testing.T) {
	tool := &GlobTool{}
	state := NewBasicState(context.TODO())

	// Valid input
	validInput := GlobInput{
		Pattern: "*.go",
	}
	validBytes, _ := json.Marshal(validInput)
	err := tool.ValidateInput(state, string(validBytes))
	assert.NoError(t, err)

	// Invalid JSON
	err = tool.ValidateInput(state, "invalid json")
	assert.Error(t, err)

	// Missing pattern
	invalidInput := GlobInput{
		Path: "./testdata",
	}
	invalidBytes, _ := json.Marshal(invalidInput)
	err = tool.ValidateInput(state, string(invalidBytes))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "pattern is required")

	// Invalid path
	invalidInput = GlobInput{
		Pattern: "*.go",
		Path:    "./testdata/subdir",
	}
	invalidBytes, _ = json.Marshal(invalidInput)
	err = tool.ValidateInput(state, string(invalidBytes))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "path must be an absolute path")
}

func TestGlobTool_Execute(t *testing.T) {
	// Create a temporary directory structure for testing
	tmpDir, err := os.MkdirTemp("", "glob-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test directory structure
	testFiles := map[string]string{
		"file1.go":           "content",
		"file2.go":           "content",
		"config.json":        "{}",
		"config.yaml":        "key: value",
		"subdir/file3.go":    "content",
		"subdir/file4.txt":   "content",
		".hidden/secret.txt": "hidden content",
		".hidden_file.txt":   "hidden content",
	}

	for path, content := range testFiles {
		fullPath := filepath.Join(tmpDir, path)
		err := os.MkdirAll(filepath.Dir(fullPath), 0755)
		if err != nil {
			t.Fatal(err)
		}
		err = os.WriteFile(fullPath, []byte(content), 0644)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Modify file modification times to ensure consistent sorting
	// Make file2.go newer than file1.go
	now := time.Now()
	err = os.Chtimes(filepath.Join(tmpDir, "file1.go"), now.Add(-2*time.Hour), now.Add(-2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	err = os.Chtimes(filepath.Join(tmpDir, "file2.go"), now, now)
	if err != nil {
		t.Fatal(err)
	}

	tool := &GlobTool{}
	ctx := context.Background()
	state := NewBasicState(context.TODO())

	// Test cases
	testCases := []struct {
		name           string
		input          GlobInput
		expectedFiles  []string
		notExpected    []string
		expectError    bool
		checkTruncated bool
	}{
		{
			name: "Match Go files in current directory",
			input: GlobInput{
				Pattern: "*.go",
				Path:    tmpDir,
			},
			expectedFiles: []string{"file2.go", "file1.go"},
			notExpected:   []string{"subdir/file3.go", "config.json"},
		},
		{
			name: "Match Go files recursively",
			input: GlobInput{
				Pattern: "**/*.go",
				Path:    tmpDir,
			},
			expectedFiles: []string{"file2.go", "file1.go", "subdir/file3.go"},
			notExpected:   []string{"config.json", "subdir/file4.txt"},
		},
		{
			name: "Match multiple extensions",
			input: GlobInput{
				Pattern: "*.{json,yaml}",
				Path:    tmpDir,
			},
			expectedFiles: []string{"config.json", "config.yaml"},
			notExpected:   []string{"file1.go", "subdir/file4.txt"},
		},
		{
			name: "Match files in subdirectory",
			input: GlobInput{
				Pattern: "subdir/*.txt",
				Path:    tmpDir,
			},
			expectedFiles: []string{"subdir/file4.txt"},
			notExpected:   []string{"file1.go", "subdir/file3.go"},
		},
		{
			name: "Skip hidden files",
			input: GlobInput{
				Pattern: "**/*.txt",
				Path:    tmpDir,
			},
			expectedFiles: []string{"subdir/file4.txt"},
			notExpected:   []string{".hidden/secret.txt", ".hidden_file.txt"},
		},
		// {
		// 	name: "Invalid JSON input",
		// 	input: GlobInput{
		// 		Pattern: "",
		// 	},
		// 	expectError: true,
		// },
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			inputBytes, _ := json.Marshal(tc.input)
			result := tool.Execute(ctx, state, string(inputBytes))

			if tc.expectError {
				assert.False(t, result.IsError())
				return
			}

			assert.False(t, result.IsError())

			// Check for expected files
			for _, expectedFile := range tc.expectedFiles {
				expectedPath := filepath.ToSlash(filepath.Join(tmpDir, expectedFile))
				assert.Contains(t, result.GetResult(), expectedPath)
			}

			// Check that unexpected files are not included
			for _, unexpectedFile := range tc.notExpected {
				unexpectedPath := filepath.ToSlash(filepath.Join(tmpDir, unexpectedFile))
				assert.NotContains(t, result.GetResult(), unexpectedPath)
			}

			// Check that files are sorted by modification time (newest first)
			if len(tc.expectedFiles) >= 2 && tc.expectedFiles[0] == "file2.go" && tc.expectedFiles[1] == "file1.go" {
				resultLines := strings.Split(strings.TrimSpace(result.GetResult()), "\n")
				file2Index := -1
				file1Index := -1

				for i, line := range resultLines {
					if strings.HasSuffix(line, "file2.go") {
						file2Index = i
					}
					if strings.HasSuffix(line, "file1.go") {
						file1Index = i
					}
				}

				if file1Index >= 0 && file2Index >= 0 {
					assert.Less(t, file2Index, file1Index, "Newer file should come first")
				}
			}

			// Check truncation message if needed
			if tc.checkTruncated {
				assert.Contains(t, result.GetResult(), "Results truncated to 100 files")
			}
		})
	}
}
