package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/binaries"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	input := GlobInput{
		Pattern:         "*.go",
		Path:            "./testdata",
		IgnoreGitignore: true,
	}
	inputBytes, _ := json.Marshal(input)

	kvs, err := tool.TracingKVs(string(inputBytes))
	assert.NoError(t, err)
	assert.Len(t, kvs, 3)

	kvs, err = tool.TracingKVs("invalid json")
	assert.Error(t, err)
	assert.Nil(t, kvs)
}

func TestGlobTool_ValidateInput(t *testing.T) {
	tool := &GlobTool{}
	state := NewBasicState(context.TODO())

	validInput := GlobInput{
		Pattern: "*.go",
	}
	validBytes, _ := json.Marshal(validInput)
	err := tool.ValidateInput(state, string(validBytes))
	assert.NoError(t, err)

	err = tool.ValidateInput(state, "invalid json")
	assert.Error(t, err)

	invalidInput := GlobInput{
		Path: "./testdata",
	}
	invalidBytes, _ := json.Marshal(invalidInput)
	err = tool.ValidateInput(state, string(invalidBytes))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "pattern is required")

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
	ctx := context.Background()
	_, err := binaries.EnsureFd(ctx)
	if err != nil {
		t.Skip("fd not available, skipping glob tests")
	}

	tmpDir, err := os.MkdirTemp("", "glob-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

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
		err := os.MkdirAll(filepath.Dir(fullPath), 0o755)
		require.NoError(t, err)
		err = os.WriteFile(fullPath, []byte(content), 0o644)
		require.NoError(t, err)
	}

	now := time.Now()
	err = os.Chtimes(filepath.Join(tmpDir, "file1.go"), now.Add(-2*time.Hour), now.Add(-2*time.Hour))
	require.NoError(t, err)
	err = os.Chtimes(filepath.Join(tmpDir, "file2.go"), now, now)
	require.NoError(t, err)

	tool := &GlobTool{}
	state := NewBasicState(context.TODO())

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
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			inputBytes, _ := json.Marshal(tc.input)
			result := tool.Execute(ctx, state, string(inputBytes))

			if tc.expectError {
				assert.True(t, result.IsError())
				return
			}

			assert.False(t, result.IsError(), "Unexpected error: %s", result.GetError())

			for _, expectedFile := range tc.expectedFiles {
				expectedPath := filepath.Join(tmpDir, expectedFile)
				assert.Contains(t, result.GetResult(), expectedPath, "Expected file not found: %s", expectedFile)
			}

			for _, unexpectedFile := range tc.notExpected {
				unexpectedPath := filepath.Join(tmpDir, unexpectedFile)
				assert.NotContains(t, result.GetResult(), unexpectedPath, "Unexpected file found: %s", unexpectedFile)
			}

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

			if tc.checkTruncated {
				assert.Contains(t, result.GetResult(), "Results truncated to 100 files")
			}
		})
	}
}

func TestGlobTool_GitignoreRespected(t *testing.T) {
	ctx := context.Background()
	_, err := binaries.EnsureFd(ctx)
	if err != nil {
		t.Skip("fd not available, skipping glob tests")
	}

	tmpDir, err := os.MkdirTemp("", "glob-gitignore-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Initialize a git repo - fd only respects .gitignore in git repositories
	gitDir := filepath.Join(tmpDir, ".git")
	err = os.MkdirAll(gitDir, 0o755)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(gitDir, "config"), []byte("[core]\n\trepositoryformatversion = 0\n"), 0o644)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte("ignored/\n*.ignored\n"), 0o644)
	require.NoError(t, err)

	testFiles := map[string]string{
		"included.go":         "content",
		"ignored/file.go":     "content",
		"test.ignored":        "content",
		"subdir/included.go":  "content",
		"subdir/test.ignored": "content",
	}

	for path, content := range testFiles {
		fullPath := filepath.Join(tmpDir, path)
		err := os.MkdirAll(filepath.Dir(fullPath), 0o755)
		require.NoError(t, err)
		err = os.WriteFile(fullPath, []byte(content), 0o644)
		require.NoError(t, err)
	}

	tool := &GlobTool{}
	state := NewBasicState(context.TODO())

	t.Run("Respects gitignore by default", func(t *testing.T) {
		input := GlobInput{
			Pattern: "**/*",
			Path:    tmpDir,
		}
		inputBytes, _ := json.Marshal(input)
		result := tool.Execute(ctx, state, string(inputBytes))

		assert.False(t, result.IsError())
		assert.Contains(t, result.GetResult(), "included.go")
		assert.Contains(t, result.GetResult(), "subdir/included.go")
		assert.NotContains(t, result.GetResult(), "ignored/file.go")
		assert.NotContains(t, result.GetResult(), "test.ignored")
	})

	t.Run("Ignores gitignore when flag is set", func(t *testing.T) {
		input := GlobInput{
			Pattern:         "**/*",
			Path:            tmpDir,
			IgnoreGitignore: true,
		}
		inputBytes, _ := json.Marshal(input)
		result := tool.Execute(ctx, state, string(inputBytes))

		assert.False(t, result.IsError())
		assert.Contains(t, result.GetResult(), "included.go")
		assert.Contains(t, result.GetResult(), "ignored/file.go")
		assert.Contains(t, result.GetResult(), "test.ignored")
	})
}

func TestGlobTool_HiddenFiles(t *testing.T) {
	ctx := context.Background()
	_, err := binaries.EnsureFd(ctx)
	if err != nil {
		t.Skip("fd not available, skipping glob tests")
	}

	tmpDir, err := os.MkdirTemp("", "glob-hidden-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	testFiles := map[string]string{
		"visible.go":          "content",
		".hidden.go":          "content",
		".hidden_dir/file.go": "content",
	}

	for path, content := range testFiles {
		fullPath := filepath.Join(tmpDir, path)
		err := os.MkdirAll(filepath.Dir(fullPath), 0o755)
		require.NoError(t, err)
		err = os.WriteFile(fullPath, []byte(content), 0o644)
		require.NoError(t, err)
	}

	tool := &GlobTool{}
	state := NewBasicState(context.TODO())

	t.Run("Excludes hidden files by default", func(t *testing.T) {
		input := GlobInput{
			Pattern: "**/*.go",
			Path:    tmpDir,
		}
		inputBytes, _ := json.Marshal(input)
		result := tool.Execute(ctx, state, string(inputBytes))

		assert.False(t, result.IsError())
		assert.Contains(t, result.GetResult(), "visible.go")
		assert.NotContains(t, result.GetResult(), ".hidden.go")
		assert.NotContains(t, result.GetResult(), ".hidden_dir/file.go")
	})

	t.Run("Includes hidden files when ignore_gitignore is set", func(t *testing.T) {
		input := GlobInput{
			Pattern:         "**/*.go",
			Path:            tmpDir,
			IgnoreGitignore: true,
		}
		inputBytes, _ := json.Marshal(input)
		result := tool.Execute(ctx, state, string(inputBytes))

		assert.False(t, result.IsError())
		assert.Contains(t, result.GetResult(), "visible.go")
		assert.Contains(t, result.GetResult(), ".hidden.go")
		assert.Contains(t, result.GetResult(), ".hidden_dir/file.go")
	})
}
