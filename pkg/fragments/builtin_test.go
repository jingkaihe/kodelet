package fragments

import (
	"io"
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuiltinFS_Open(t *testing.T) {
	builtinFS := NewBuiltinFS()

	tests := []struct {
		name        string
		fileName    string
		expectError bool
		errorType   error
	}{
		{
			name:        "open root directory",
			fileName:    ".",
			expectError: false,
		},
		{
			name:        "open issue-resolve.md",
			fileName:    "issue-resolve.md",
			expectError: false,
		},
		{
			name:        "open issue-resolve without extension",
			fileName:    "issue-resolve",
			expectError: false,
		},
		{
			name:        "open non-existent file",
			fileName:    "non-existent.md",
			expectError: true,
			errorType:   fs.ErrNotExist,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, err := builtinFS.Open(tt.fileName)
			
			if tt.expectError {
				require.Error(t, err)
				if tt.errorType != nil {
					assert.ErrorIs(t, err, tt.errorType)
				}
				assert.Nil(t, file)
			} else {
				require.NoError(t, err)
				require.NotNil(t, file)
				defer file.Close()
				
				// Test that we can get file info
				info, err := file.Stat()
				require.NoError(t, err)
				assert.NotEmpty(t, info.Name())
			}
		})
	}
}

func TestBuiltinFS_ReadDir(t *testing.T) {
	builtinFS := NewBuiltinFS()

	tests := []struct {
		name        string
		dirName     string
		expectError bool
		expectedLen int
	}{
		{
			name:        "read root directory",
			dirName:     ".",
			expectError: false,
			expectedLen: 3, // issue-resolve.md, commit-message.md, pr-response.md
		},
		{
			name:        "read empty string directory",
			dirName:     "",
			expectError: false,
			expectedLen: 3,
		},
		{
			name:        "read non-existent directory",
			dirName:     "non-existent",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use fs.ReadDir instead of builtinFS.ReadDir
			entries, err := fs.ReadDir(builtinFS, tt.dirName)
			
			if tt.expectError {
				require.Error(t, err)
				assert.Nil(t, entries)
			} else {
				require.NoError(t, err)
				assert.Len(t, entries, tt.expectedLen)
				
				// Check that entries are valid builtin fragments
				if len(entries) > 0 {
					expectedNames := []string{"issue-resolve.md", "commit-message.md", "pr-response.md"}
					for i, entry := range entries {
						assert.Equal(t, expectedNames[i], entry.Name())
						assert.False(t, entry.IsDir())
						assert.Equal(t, fs.FileMode(0644), entry.Type())
					}
				}
			}
		})
	}
}

func TestBuiltinFile_Read(t *testing.T) {
	builtinFS := NewBuiltinFS()
	
	file, err := builtinFS.Open("issue-resolve.md")
	require.NoError(t, err)
	defer file.Close()

	// Test reading the entire content
	content, err := io.ReadAll(file)
	require.NoError(t, err)
	
	contentStr := string(content)
	assert.NotEmpty(t, contentStr)
	
	// Check that the content contains expected fragments of the issue-resolve template
	assert.Contains(t, contentStr, "name: Issue Resolver")
	assert.Contains(t, contentStr, "IMPLEMENTATION ISSUE")
	assert.Contains(t, contentStr, "QUESTION ISSUE")
	assert.Contains(t, contentStr, "{{.IssueURL}}")
	assert.Contains(t, contentStr, "{{.BotMention}}")
	assert.Contains(t, contentStr, "{{.BinPath}}")
}

func TestBuiltinFile_ReadMultipleTimes(t *testing.T) {
	builtinFS := NewBuiltinFS()
	
	file, err := builtinFS.Open("issue-resolve.md")
	require.NoError(t, err)
	defer file.Close()

	// Read in chunks to test multiple reads
	buf1 := make([]byte, 100)
	n1, err1 := file.Read(buf1)
	require.NoError(t, err1)
	assert.Greater(t, n1, 0)
	
	buf2 := make([]byte, 100)
	n2, err2 := file.Read(buf2)
	
	// Should either read more data or reach EOF
	if err2 != nil {
		assert.ErrorIs(t, err2, io.EOF)
	} else {
		assert.Greater(t, n2, 0)
	}
	
	// Verify that different parts were read
	if err2 == nil {
		assert.NotEqual(t, string(buf1[:n1]), string(buf2[:n2]))
	}
}

func TestBuiltinFile_Stat(t *testing.T) {
	builtinFS := NewBuiltinFS()
	
	file, err := builtinFS.Open("issue-resolve.md")
	require.NoError(t, err)
	defer file.Close()

	info, err := file.Stat()
	require.NoError(t, err)
	
	assert.Equal(t, "issue-resolve.md", info.Name())
	assert.Greater(t, info.Size(), int64(0))
	assert.False(t, info.IsDir())
	assert.Equal(t, fs.FileMode(0644), info.Mode())
	assert.Equal(t, info.ModTime().IsZero(), true) // Should be zero time
}

func TestBuiltinDir_Operations(t *testing.T) {
	builtinFS := NewBuiltinFS()
	
	dir, err := builtinFS.Open(".")
	require.NoError(t, err)
	defer dir.Close()

	// Test directory stat
	info, err := dir.Stat()
	require.NoError(t, err)
	assert.Equal(t, ".", info.Name())
	assert.True(t, info.IsDir())
	assert.Equal(t, fs.ModeDir|0755, info.Mode())

	// Test that reading a directory returns an error
	buf := make([]byte, 100)
	_, err = dir.Read(buf)
	assert.ErrorIs(t, err, fs.ErrInvalid)
}

func TestGetBuiltinContent(t *testing.T) {
	tests := []struct {
		name        string
		fileName    string
		expectFound bool
		shouldContain string
	}{
		{
			name:        "issue-resolve.md",
			fileName:    "issue-resolve.md",
			expectFound: true,
			shouldContain: "name: Issue Resolver",
		},
		{
			name:        "issue-resolve without extension",
			fileName:    "issue-resolve",
			expectFound: true,
			shouldContain: "name: Issue Resolver",
		},
		{
			name:        "commit-message.md",
			fileName:    "commit-message.md",
			expectFound: true,
			shouldContain: "name: Commit Message Generator",
		},
		{
			name:        "commit-message without extension",
			fileName:    "commit-message",
			expectFound: true,
			shouldContain: "name: Commit Message Generator",
		},
		{
			name:        "pr-response.md",
			fileName:    "pr-response.md",
			expectFound: true,
			shouldContain: "name: Pull Request Response",
		},
		{
			name:        "pr-response without extension",
			fileName:    "pr-response",
			expectFound: true,
			shouldContain: "name: Pull Request Response",
		},
		{
			name:        "issue-resolve with leading slash",
			fileName:    "/issue-resolve.md",
			expectFound: true,
			shouldContain: "name: Issue Resolver",
		},
		{
			name:        "issue-resolve with leading dot slash",
			fileName:    "./issue-resolve.md",
			expectFound: true,
			shouldContain: "name: Issue Resolver",
		},
		{
			name:        "non-existent fragment",
			fileName:    "non-existent",
			expectFound: false,
		},
		{
			name:        "empty string",
			fileName:    "",
			expectFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content, found := getBuiltinContent(tt.fileName)
			
			assert.Equal(t, tt.expectFound, found)
			
			if tt.expectFound {
				assert.NotEmpty(t, content)
				if tt.shouldContain != "" {
					assert.Contains(t, content, tt.shouldContain)
				}
			} else {
				assert.Empty(t, content)
			}
		})
	}
}

func TestBuiltinDirEntry_Info(t *testing.T) {
	entry := &builtinDirEntry{name: "test.md"}
	
	info, err := entry.Info()
	require.NoError(t, err)
	
	assert.Equal(t, "test.md", info.Name())
	assert.False(t, info.IsDir())
	assert.Equal(t, int64(0), info.Size()) // Size is 0 until file is opened
}

func TestBuiltinFS_Integration(t *testing.T) {
	// Test the full workflow of opening and reading a builtin fragment
	builtinFS := NewBuiltinFS()
	
	// Open the file
	file, err := builtinFS.Open("issue-resolve")
	require.NoError(t, err)
	defer file.Close()
	
	// Read the full content
	content, err := io.ReadAll(file)
	require.NoError(t, err)
	
	contentStr := string(content)
	
	// Verify it's a valid markdown fragment with frontmatter
	assert.True(t, strings.HasPrefix(contentStr, "---"))
	assert.Contains(t, contentStr, "name:")
	assert.Contains(t, contentStr, "description:")
	assert.Contains(t, contentStr, "allowed_tools:")
	assert.Contains(t, contentStr, "allowed_commands:")
	
	// Verify it contains template variables
	assert.Contains(t, contentStr, "{{.IssueURL}}")
	assert.Contains(t, contentStr, "{{.BotMention}}")
	assert.Contains(t, contentStr, "{{.BinPath}}")
	
	// Verify it contains the expected workflow content
	assert.Contains(t, contentStr, "Step 1: Analyze the Issue")
	assert.Contains(t, contentStr, "Step 2: Choose the Appropriate Workflow")
	assert.Contains(t, contentStr, "IMPLEMENTATION ISSUES")
	assert.Contains(t, contentStr, "QUESTION ISSUES")
}