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

func TestShouldExcludePath(t *testing.T) {
	testCases := []struct {
		name              string
		path              string
		includeHighVolume bool
		expectExclude     bool
	}{
		{
			name:              "Exclude node_modules by default",
			path:              "node_modules/package/index.js",
			includeHighVolume: false,
			expectExclude:     true,
		},
		{
			name:              "Include node_modules when flag is set",
			path:              "node_modules/package/index.js",
			includeHighVolume: true,
			expectExclude:     false,
		},
		{
			name:              "Exclude .git directory",
			path:              ".git/objects/abc123",
			includeHighVolume: false,
			expectExclude:     true,
		},
		{
			name:              "Exclude build directory",
			path:              "src/build/output.js",
			includeHighVolume: false,
			expectExclude:     true,
		},
		{
			name:              "Allow normal paths",
			path:              "src/components/App.js",
			includeHighVolume: false,
			expectExclude:     false,
		},
		{
			name:              "Allow .github directory",
			path:              ".github/workflows/test.yml",
			includeHighVolume: false,
			expectExclude:     false,
		},
		{
			name:              "Allow .vscode directory",
			path:              ".vscode/settings.json",
			includeHighVolume: false,
			expectExclude:     false,
		},
		{
			name:              "Exclude vendor directory",
			path:              "vendor/github.com/pkg/errors",
			includeHighVolume: false,
			expectExclude:     true,
		},
		{
			name:              "Exclude dist directory",
			path:              "dist/bundle.js",
			includeHighVolume: false,
			expectExclude:     true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			shouldExclude := shouldExcludePath(tc.path, tc.includeHighVolume)
			assert.Equal(t, tc.expectExclude, shouldExclude, "shouldExclude mismatch")
		})
	}
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
	require.NoError(t, err)
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
		err := os.MkdirAll(filepath.Dir(fullPath), 0o755)
		require.NoError(t, err)
		err = os.WriteFile(fullPath, []byte(content), 0o644)
		require.NoError(t, err)
	}

	// Modify file modification times to ensure consistent sorting
	// Make file2.go newer than file1.go
	now := time.Now()
	err = os.Chtimes(filepath.Join(tmpDir, "file1.go"), now.Add(-2*time.Hour), now.Add(-2*time.Hour))
	require.NoError(t, err)
	err = os.Chtimes(filepath.Join(tmpDir, "file2.go"), now, now)
	require.NoError(t, err)

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
			name: "Allows .github directory while still working",
			input: GlobInput{
				Pattern: "**/*.yml",
				Path:    tmpDir,
			},
			expectedFiles: []string{".github/workflows/test.yml"},
			notExpected:   []string{},
		},
		{
			name: "Excludes node_modules by default",
			input: GlobInput{
				Pattern: "**/*.js",
				Path:    tmpDir,
			},
			expectedFiles: []string{},
			notExpected:   []string{"node_modules/package/index.js"},
		},
		{
			name: "Include node_modules with include_high_volume flag",
			input: GlobInput{
				Pattern:           "**/*.js",
				Path:              tmpDir,
				IncludeHighVolume: true,
			},
			expectedFiles: []string{"node_modules/package/index.js"},
			notExpected:   []string{},
		},
		{
			name: "Excludes .git directory by default",
			input: GlobInput{
				Pattern: "**/*",
				Path:    tmpDir,
			},
			notExpected: []string{".git/objects/abc123"},
		},
		{
			name: "Allows .vscode directory",
			input: GlobInput{
				Pattern: "**/*.json",
				Path:    tmpDir,
			},
			expectedFiles: []string{".vscode/settings.json"},
			notExpected:   []string{},
		},
		{
			name: "Excludes build directory by default",
			input: GlobInput{
				Pattern: "**/*.js",
				Path:    tmpDir,
			},
			notExpected: []string{"build/bundle.js", "dist/main.js"},
		},
	}

	// Create test directories and files
	// .github/workflows directory
	githubDir := filepath.Join(tmpDir, ".github", "workflows")
	err = os.MkdirAll(githubDir, 0o755)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(githubDir, "test.yml"), []byte("name: test"), 0o644)
	require.NoError(t, err)

	// node_modules directory (excluded by default)
	nodeModulesDir := filepath.Join(tmpDir, "node_modules", "package")
	err = os.MkdirAll(nodeModulesDir, 0o755)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(nodeModulesDir, "index.js"), []byte("module.exports = {}"), 0o644)
	require.NoError(t, err)

	// .git directory (excluded by default)
	gitDir := filepath.Join(tmpDir, ".git", "objects")
	err = os.MkdirAll(gitDir, 0o755)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(gitDir, "abc123"), []byte("git object"), 0o644)
	require.NoError(t, err)

	// .vscode directory (allowed)
	vscodeDir := filepath.Join(tmpDir, ".vscode")
	err = os.MkdirAll(vscodeDir, 0o755)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(vscodeDir, "settings.json"), []byte("{}"), 0o644)
	require.NoError(t, err)

	// build and dist directories (excluded by default)
	buildDir := filepath.Join(tmpDir, "build")
	err = os.MkdirAll(buildDir, 0o755)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(buildDir, "bundle.js"), []byte("// bundle"), 0o644)
	require.NoError(t, err)

	distDir := filepath.Join(tmpDir, "dist")
	err = os.MkdirAll(distDir, 0o755)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(distDir, "main.js"), []byte("// main"), 0o644)
	require.NoError(t, err)

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
