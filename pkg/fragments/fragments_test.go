package fragments

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFragmentProcessor_LoadFragment(t *testing.T) {
	// Create temp directory for test fragments
	tempDir, err := os.MkdirTemp("", "kodelet-fragments-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create a test fragment with both variables and bash commands
	fragmentContent := `Hello {{.name}}!

Current date: {{bash "date" "+%Y-%m-%d"}}

Your occupation is {{.occupation}}.`

	fragmentPath := filepath.Join(tempDir, "test.md")
	err = os.WriteFile(fragmentPath, []byte(fragmentContent), 0644)
	require.NoError(t, err)

	// Create processor with custom directory
	processor := &FragmentProcessor{
		fragmentDirs: []string{tempDir},
	}

	// Test fragment loading and processing
	config := &FragmentConfig{
		FragmentName: "test",
		Arguments: map[string]string{
			"name":       "Alice",
			"occupation": "Engineer",
		},
	}

	result, err := processor.LoadFragment(context.Background(), config)
	require.NoError(t, err)

	// Verify variable substitution worked
	assert.Contains(t, result, "Hello Alice!")
	assert.Contains(t, result, "Your occupation is Engineer.")

	// Verify bash command was executed (should contain a date)
	assert.Contains(t, result, "Current date: 20")
}

func TestFragmentProcessor_LoadFragment_ComplexBashCommands(t *testing.T) {
	// Create temp directory for test fragments
	tempDir, err := os.MkdirTemp("", "kodelet-fragments-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create a fragment with complex bash commands
	fragmentContent := `Project: {{.project}}

Files count: {{bash "sh" "-c" "find . -name '*.go' | wc -l"}}

Hello message: {{bash "sh" "-c" "echo 'Hello World' | tr '[:lower:]' '[:upper:]'"}}`

	fragmentPath := filepath.Join(tempDir, "complex.md")
	err = os.WriteFile(fragmentPath, []byte(fragmentContent), 0644)
	require.NoError(t, err)

	processor := &FragmentProcessor{
		fragmentDirs: []string{tempDir},
	}

	config := &FragmentConfig{
		FragmentName: "complex",
		Arguments: map[string]string{
			"project": "Kodelet",
		},
	}

	result, err := processor.LoadFragment(context.Background(), config)
	require.NoError(t, err)

	assert.Contains(t, result, "Project: Kodelet")
	assert.Contains(t, result, "Files count: ")
	assert.Contains(t, result, "Hello message: HELLO WORLD")
}

func TestFragmentProcessor_LoadFragment_BashCommandError(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "kodelet-fragments-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create a fragment with a failing bash command
	fragmentContent := `This will fail: {{bash "nonexistent-command-xyz"}}`

	fragmentPath := filepath.Join(tempDir, "failing.md")
	err = os.WriteFile(fragmentPath, []byte(fragmentContent), 0644)
	require.NoError(t, err)

	processor := &FragmentProcessor{
		fragmentDirs: []string{tempDir},
	}

	config := &FragmentConfig{
		FragmentName: "failing",
		Arguments:    map[string]string{},
	}

	result, err := processor.LoadFragment(context.Background(), config)
	require.NoError(t, err)

	// Should contain error message
	assert.Contains(t, result, "[ERROR executing command")
	assert.Contains(t, result, "nonexistent-command-xyz")
}

func TestFragmentProcessor_findFragmentFile(t *testing.T) {
	// Create temp directory for test fragments
	tempDir, err := os.MkdirTemp("", "kodelet-fragments-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create test fragments
	err = os.WriteFile(filepath.Join(tempDir, "test1.md"), []byte("test1"), 0644)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(tempDir, "test2"), []byte("test2"), 0644)
	require.NoError(t, err)

	processor := &FragmentProcessor{
		fragmentDirs: []string{tempDir},
	}

	// Test finding .md file
	path, err := processor.findFragmentFile("test1")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(tempDir, "test1.md"), path)

	// Test finding file without extension
	path, err = processor.findFragmentFile("test2")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(tempDir, "test2"), path)

	// Test file not found
	_, err = processor.findFragmentFile("nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "fragment 'nonexistent' not found")
}

func TestFragmentProcessor_DirectoryPrecedence(t *testing.T) {
	// Create two temp directories
	highPrecDir, err := os.MkdirTemp("", "kodelet-fragments-high")
	require.NoError(t, err)
	defer os.RemoveAll(highPrecDir)

	lowPrecDir, err := os.MkdirTemp("", "kodelet-fragments-low")
	require.NoError(t, err)
	defer os.RemoveAll(lowPrecDir)

	// Create the same fragment name in both directories
	err = os.WriteFile(filepath.Join(highPrecDir, "same.md"), []byte("high priority"), 0644)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(lowPrecDir, "same.md"), []byte("low priority"), 0644)
	require.NoError(t, err)

	// High precedence directory comes first
	processor := &FragmentProcessor{
		fragmentDirs: []string{highPrecDir, lowPrecDir},
	}

	path, err := processor.findFragmentFile("same")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(highPrecDir, "same.md"), path)
}

func TestFragmentProcessor_processTemplate_VariablesOnly(t *testing.T) {
	processor := &FragmentProcessor{}

	content := "Hello {{.name}}! You work as a {{.job}}."
	args := map[string]string{
		"name": "Bob",
		"job":  "Developer",
	}

	result, err := processor.processTemplate(context.Background(), content, args)
	require.NoError(t, err)

	expected := "Hello Bob! You work as a Developer."
	assert.Equal(t, expected, result)
}

func TestFragmentProcessor_processTemplate_BashOnly(t *testing.T) {
	processor := &FragmentProcessor{}

	content := `Hello {{bash "echo" "world"}}! Today is {{bash "date" "+%A"}}.`

	result, err := processor.processTemplate(context.Background(), content, map[string]string{})
	require.NoError(t, err)

	assert.Contains(t, result, "Hello world!")
	assert.Contains(t, result, "Today is ")
}

func TestFragmentProcessor_processTemplate_MixedContent(t *testing.T) {
	processor := &FragmentProcessor{}

	content := `User: {{.name}}
Command output: {{bash "echo" "test output"}}`
	args := map[string]string{
		"name": "TestUser",
	}

	result, err := processor.processTemplate(context.Background(), content, args)
	require.NoError(t, err)

	assert.Contains(t, result, "User: TestUser")
	assert.Contains(t, result, "Command output: test output")
}

func TestFragmentProcessor_ListFragments(t *testing.T) {
	// Create temp directories
	dir1, err := os.MkdirTemp("", "kodelet-fragments-1")
	require.NoError(t, err)
	defer os.RemoveAll(dir1)

	dir2, err := os.MkdirTemp("", "kodelet-fragments-2")
	require.NoError(t, err)
	defer os.RemoveAll(dir2)

	// Create fragments in both directories
	err = os.WriteFile(filepath.Join(dir1, "frag1.md"), []byte("fragment1"), 0644)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(dir1, "frag2"), []byte("fragment2"), 0644)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(dir2, "frag3.md"), []byte("fragment3"), 0644)
	require.NoError(t, err)

	// Same name in both directories (should prioritize first)
	err = os.WriteFile(filepath.Join(dir1, "duplicate.md"), []byte("first"), 0644)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(dir2, "duplicate.md"), []byte("second"), 0644)
	require.NoError(t, err)

	processor := &FragmentProcessor{
		fragmentDirs: []string{dir1, dir2},
	}

	fragments, err := processor.ListFragments()
	require.NoError(t, err)

	// Should find unique fragments, with precedence for first directory
	expected := []string{"frag1", "frag2", "duplicate", "frag3"}
	assert.ElementsMatch(t, expected, fragments)
}

func TestFragmentProcessor_createBashFunc(t *testing.T) {
	processor := &FragmentProcessor{}
	ctx := context.Background()

	bashFunc := processor.createBashFunc(ctx)

	// Test successful command
	result := bashFunc("echo", "hello")
	assert.Equal(t, "hello", result)

	// Test command with trailing newlines
	result = bashFunc("echo", "test")
	assert.Equal(t, "test", result)

	// Test failing command
	result = bashFunc("nonexistent-command")
	assert.Contains(t, result, "[ERROR executing command")

	// Test no arguments
	result = bashFunc()
	assert.Contains(t, result, "[ERROR: bash function requires at least one argument]")

	// Test multiple arguments
	result = bashFunc("echo", "-n", "hello world")
	assert.Equal(t, "hello world", result)
}

func TestFragmentProcessor_ErrorHandling(t *testing.T) {
	processor := &FragmentProcessor{}

	// Test malformed template syntax
	content := "Hello {{range}}" // Invalid range without end
	_, err := processor.processTemplate(context.Background(), content, map[string]string{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse template")

	// Test missing variable in strict mode - this should work fine as template engine handles missing variables
	content = "Hello {{.missing}}"
	result, err := processor.processTemplate(context.Background(), content, map[string]string{})
	require.NoError(t, err)
	assert.Equal(t, "Hello <no value>", result)

	// Test fragment file not found
	config := &FragmentConfig{
		FragmentName: "nonexistent-fragment-xyz",
		Arguments:    map[string]string{},
	}
	_, err = processor.LoadFragment(context.Background(), config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "fragment 'nonexistent-fragment-xyz' not found")
}

func TestFragmentProcessor_NewFragmentProcessor(t *testing.T) {
	processor := NewFragmentProcessor()

	// Should have two directories configured
	assert.Len(t, processor.fragmentDirs, 2)
	assert.Equal(t, "./receipts", processor.fragmentDirs[0])
	assert.True(t, strings.HasSuffix(processor.fragmentDirs[1], "/.kodelet/receipts"))
}
