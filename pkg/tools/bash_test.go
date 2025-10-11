package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBashTool_GenerateSchema(t *testing.T) {
	tool := &BashTool{}
	schema := tool.GenerateSchema()
	assert.NotNil(t, schema)

	assert.Equal(t, "https://github.com/jingkaihe/kodelet/pkg/tools/bash-input", string(schema.ID))
}

func TestBashTool_Name(t *testing.T) {
	tool := &BashTool{}
	assert.Equal(t, "bash", tool.Name())
}

func TestBashTool_Description(t *testing.T) {
	tool := &BashTool{}
	desc := tool.Description()
	assert.Contains(t, desc, "Executes a given bash command")
	assert.Contains(t, desc, "Important")
}

func TestBashTool_Description_BannedCommands(t *testing.T) {
	// Test with no allowed commands configured (uses banned commands)
	tool := NewBashTool([]string{})
	desc := tool.Description()

	// Should contain banned commands section
	assert.Contains(t, desc, "## Banned Commands")
	assert.Contains(t, desc, "The following commands are banned and cannot be used:")
	assert.Contains(t, desc, "* vim")
	assert.Contains(t, desc, "* view")
	assert.Contains(t, desc, "* less")
	assert.Contains(t, desc, "* more")
	assert.Contains(t, desc, "* cd")

	// Should NOT contain allowed commands section
	assert.NotContains(t, desc, "## Allowed Commands")
	assert.NotContains(t, desc, "Only the following commands/patterns are allowed:")
}

func TestBashTool_Description_AllowedCommands(t *testing.T) {
	// Test with allowed commands configured
	allowedCommands := []string{"ls *", "pwd", "echo *", "git status"}
	tool := NewBashTool(allowedCommands)
	desc := tool.Description()

	// Should contain allowed commands section
	assert.Contains(t, desc, "## Allowed Commands")
	assert.Contains(t, desc, "Only the following commands/patterns are allowed:")
	assert.Contains(t, desc, "* ls *")
	assert.Contains(t, desc, "* pwd")
	assert.Contains(t, desc, "* echo *")
	assert.Contains(t, desc, "* git status")
	assert.Contains(t, desc, "Commands not matching these patterns will be rejected.")

	// Should NOT contain banned commands section
	assert.NotContains(t, desc, "## Banned Commands")
	assert.NotContains(t, desc, "The following commands are banned and cannot be used:")
}

func TestBashTool_Description_EmptyAllowedCommands(t *testing.T) {
	// Test with empty allowed commands (should fall back to banned commands)
	tool := NewBashTool(nil)
	desc := tool.Description()

	// Should contain banned commands section since no allowed commands configured
	assert.Contains(t, desc, "## Banned Commands")
	assert.Contains(t, desc, "* vim")
	assert.NotContains(t, desc, "## Allowed Commands")
}

func TestBashTool_Description_ConsistentOutput(t *testing.T) {
	// Test that the description is consistent and contains all expected sections
	tool := NewBashTool([]string{"test *", "example"})
	desc := tool.Description()

	// Basic structure should always be present
	assert.Contains(t, desc, "Executes a given bash command in a persistent shell session with timeout.")
	assert.Contains(t, desc, "# Command Restrictions")
	assert.Contains(t, desc, "# Important")
	assert.Contains(t, desc, "# Background Parameter")
	assert.Contains(t, desc, "# Examples")

	// Should be well-formed
	assert.NotEmpty(t, desc)
	assert.Greater(t, len(desc), 1000) // Should be a substantial description

	// Test that multiple calls return the same result
	desc2 := tool.Description()
	assert.Equal(t, desc, desc2)
}

func TestBashTool_Description_SpecialCharacters(t *testing.T) {
	// Test with allowed commands that contain special characters
	allowedCommands := []string{
		"find . -name '*.go'",
		"grep -r \"pattern\" .",
		"awk '{print $1}'",
		"sed 's/old/new/g'",
	}
	tool := NewBashTool(allowedCommands)
	desc := tool.Description()

	// Should handle special characters in command patterns
	assert.Contains(t, desc, "## Allowed Commands")
	assert.Contains(t, desc, "* find . -name '*.go'")
	assert.Contains(t, desc, "* grep -r \"pattern\" .")
	assert.Contains(t, desc, "* awk '{print $1}'")
	assert.Contains(t, desc, "* sed 's/old/new/g'")
}

func TestBashTool_Execute_Success(t *testing.T) {
	tool := &BashTool{}
	input := BashInput{
		Description: "Echo test",
		Command:     "echo 'hello world'",
		Timeout:     10,
	}
	params, _ := json.Marshal(input)

	result := tool.Execute(context.Background(), NewBasicState(context.TODO()), string(params))
	assert.False(t, result.IsError())
	assert.Equal(t, "hello world\n", result.GetResult())
}

func TestBashTool_Execute_Timeout(t *testing.T) {
	tool := &BashTool{}
	input := BashInput{
		Description: "Sleep test",
		Command:     "sleep 0.2",
		Timeout:     1,
	}
	params, _ := json.Marshal(input)

	// Use a shorter context timeout to simulate timeout faster
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	result := tool.Execute(ctx, NewBasicState(context.TODO()), string(params))
	assert.Contains(t, result.GetError(), "Command timed out")
	assert.Empty(t, result.GetResult())
}

func TestBashTool_Execute_Error(t *testing.T) {
	tool := &BashTool{}
	input := BashInput{
		Description: "Invalid command",
		Command:     "nonexistentcommand",
		Timeout:     10,
	}
	params, _ := json.Marshal(input)

	result := tool.Execute(context.Background(), NewBasicState(context.TODO()), string(params))
	assert.Contains(t, result.GetError(), "Command exited with status 127")
	assert.Contains(t, result.GetResult(), "nonexistentcommand: command not found")
}

func TestBashTool_Execute_InvalidJSON(t *testing.T) {
	tool := &BashTool{}
	result := tool.Execute(context.Background(), NewBasicState(context.TODO()), "invalid json")
	assert.True(t, result.IsError())
	assert.Empty(t, result.GetResult())
}

func TestBashTool_Execute_ContextCancellation(t *testing.T) {
	tool := &BashTool{}
	input := BashInput{
		Description: "Long running command",
		Command:     "sleep 5",
		Timeout:     20,
	}
	params, _ := json.Marshal(input)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	result := tool.Execute(ctx, NewBasicState(context.TODO()), string(params))
	assert.Contains(t, result.GetError(), "Command timed out")
	assert.Empty(t, result.GetResult())
}

func TestBashTool_ValidateInput(t *testing.T) {
	tool := &BashTool{}
	tests := []struct {
		name        string
		input       BashInput
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid single command",
			input: BashInput{
				Description: "test",
				Command:     "echo hello",
				Timeout:     10,
			},
			expectError: false,
		},
		{
			name: "valid multiple commands with &&",
			input: BashInput{
				Description: "test",
				Command:     "echo hello && echo world",
				Timeout:     10,
			},
			expectError: false,
		},
		{
			name: "valid multiple commands with ;",
			input: BashInput{
				Description: "test",
				Command:     "echo hello; echo world",
				Timeout:     10,
			},
			expectError: false,
		},
		{
			name: "valid complex command",
			input: BashInput{
				Description: "test",
				Command:     "echo hello && echo world; echo test || echo fail",
				Timeout:     10,
			},
			expectError: false,
		},
		{
			name: "banned command",
			input: BashInput{
				Description: "test",
				Command:     "echo hello && vim file.txt",
				Timeout:     10,
			},
			expectError: true,
			errorMsg:    "command is banned",
		},
		{
			name: "empty command",
			input: BashInput{
				Description: "test",
				Command:     "",
				Timeout:     10,
			},
			expectError: true,
			errorMsg:    "command is required",
		},
		{
			name: "missing description",
			input: BashInput{
				Command: "echo hello",
				Timeout: 10,
			},
			expectError: true,
			errorMsg:    "description is required",
		},
		{
			name: "invalid timeout too low",
			input: BashInput{
				Description: "test",
				Command:     "echo hello",
				Timeout:     5,
			},
			expectError: true,
			errorMsg:    "timeout must be between 10 and 120 seconds",
		},
		{
			name: "invalid timeout too high",
			input: BashInput{
				Description: "test",
				Command:     "echo hello",
				Timeout:     150,
			},
			expectError: true,
			errorMsg:    "timeout must be between 10 and 120 seconds",
		},
		{
			name: "valid background command with timeout 0",
			input: BashInput{
				Description: "test",
				Command:     "sleep 1",
				Timeout:     0,
				Background:  true,
			},
			expectError: false,
		},
		{
			name: "invalid background command with non-zero timeout",
			input: BashInput{
				Description: "test",
				Command:     "echo hello",
				Timeout:     30,
				Background:  true,
			},
			expectError: true,
			errorMsg:    "background processes must have timeout=0",
		},
		{
			name: "invalid background command with negative timeout",
			input: BashInput{
				Description: "test",
				Command:     "echo hello",
				Timeout:     -1,
				Background:  true,
			},
			expectError: true,
			errorMsg:    "background processes must have timeout=0",
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

func TestBashTool_BackgroundExecution(t *testing.T) {
	tool := &BashTool{}

	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "bash_bg_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Change to temp directory
	oldPwd, _ := os.Getwd()
	defer os.Chdir(oldPwd)
	os.Chdir(tempDir)

	state := NewBasicState(context.TODO())
	defer cleanupBackgroundProcesses(t, state)

	input := BashInput{
		Description: "Background echo test",
		Command:     "echo 'background process' && sleep 0.2 && echo 'done'",
		Timeout:     0, // Background processes must have timeout=0
		Background:  true,
	}
	params, _ := json.Marshal(input)

	result := tool.Execute(context.Background(), state, string(params))

	assert.False(t, result.IsError(), "Background execution should not error: %s", result.GetError())

	// Check if it's a background result
	bgResult, ok := result.(*BackgroundBashToolResult)
	require.True(t, ok, "Result should be BackgroundBashToolResult")

	// Verify PID is set
	assert.Greater(t, bgResult.pid, 0, "PID should be set")

	// Verify log path is correct
	assert.Contains(t, bgResult.logPath, ".kodelet")
	assert.Contains(t, bgResult.logPath, "out.log")

	// Check that the process was added to state
	processes := state.GetBackgroundProcesses()
	assert.Len(t, processes, 1, "Should have one background process")
	assert.Equal(t, bgResult.pid, processes[0].PID)
	assert.Equal(t, input.Command, processes[0].Command)

	content := waitForLogContent(t, bgResult.logPath, "background process", 40, 0)
	assert.NotContains(t, string(content), "done")

	content = waitForLogContent(t, bgResult.logPath, "done", 120, 0)
	assert.Contains(t, string(content), "background process")
	assert.Contains(t, string(content), "done")
}

func TestBashTool_GlobPatternMatching(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		pattern  string
		expected bool
	}{
		// Exact matches
		{"exact match", "ls", "ls", true},
		{"exact match with args", "git status", "git status", true},
		{"no match exact", "ls", "cat", false},

		// Realistic wildcard patterns
		{"wildcard with args", "ls -la", "ls *", true},
		{"wildcard with multiple args", "ls -la /home", "ls *", true},
		{"wildcard no match", "cat file.txt", "ls *", false},

		// Multiple wildcards
		{"multiple wildcards", "git log --oneline", "git * --oneline", true},
		{"multiple wildcards no match", "git status", "git * --oneline", false},

		// Wildcard at start
		{"wildcard at start", "npm start", "* start", true},
		{"wildcard at start no match", "npm build", "* start", false},

		// Wildcard at end
		{"wildcard at end", "npm start", "npm *", true},
		{"wildcard at end no match", "yarn start", "npm *", false},

		// Full wildcard
		{"full wildcard", "any command here", "*", true},
		{"full wildcard empty", "", "*", true},

		// Edge cases
		{"empty command exact", "", "", true},
		{"empty command pattern", "", "ls", false},
		{"command with pattern empty", "ls", "", false},

		// Complex patterns
		{"complex pattern match", "docker run -it ubuntu bash", "docker * ubuntu *", true},
		{"complex pattern no match", "docker run -it alpine bash", "docker * ubuntu *", false},

		// Real world examples
		{"npm commands", "npm install", "npm *", true},
		{"yarn commands", "yarn build", "yarn *", true},
		{"git status exact", "git status", "git status", true},
		{"git log with args", "git log --oneline --graph", "git log *", true},
		{"echo variations", "echo hello world", "echo *", true},
		{"ls variations", "ls -la", "ls *", true},
		{"pwd exact", "pwd", "pwd", true},
		{"find commands", "find . -name '*.go'", "find *", true},

		// Prefix matches
		{"prefix match", "git status", "git", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := NewBashTool([]string{tt.pattern})
			result := tool.MatchesCommand(tt.command)
			assert.Equal(t, tt.expected, result,
				"BashTool.MatchesCommand(%q) with pattern %q = %v, want %v",
				tt.command, tt.pattern, result, tt.expected)
		})
	}
}

func TestNewBashTool(t *testing.T) {
	tests := []struct {
		name            string
		allowedCommands []string
	}{
		{"empty allowed commands", []string{}},
		{"single command", []string{"ls"}},
		{"multiple commands", []string{"ls *", "pwd", "echo *"}},
		{"complex patterns", []string{"git status", "npm *", "docker * ubuntu *"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := NewBashTool(tt.allowedCommands)
			assert.NotNil(t, tool)
			assert.Equal(t, "bash", tool.Name())
			assert.Equal(t, tt.allowedCommands, tool.allowedCommands)
		})
	}
}

func TestBashTool_ValidateInput_AllowedCommands(t *testing.T) {
	tests := []struct {
		name            string
		allowedCommands []string
		input           BashInput
		expectError     bool
		errorMsg        string
	}{
		// Empty allowed commands (should use banned commands)
		{
			name:            "empty allowed commands - valid command",
			allowedCommands: []string{},
			input: BashInput{
				Description: "test",
				Command:     "echo hello",
				Timeout:     10,
			},
			expectError: false,
		},
		{
			name:            "empty allowed commands - banned command",
			allowedCommands: []string{},
			input: BashInput{
				Description: "test",
				Command:     "vim file.txt",
				Timeout:     10,
			},
			expectError: true,
			errorMsg:    "command is banned: vim",
		},

		// Single exact command allowed
		{
			name:            "exact command allowed",
			allowedCommands: []string{"pwd"},
			input: BashInput{
				Description: "test",
				Command:     "pwd",
				Timeout:     10,
			},
			expectError: false,
		},
		{
			name:            "exact command not allowed",
			allowedCommands: []string{"pwd"},
			input: BashInput{
				Description: "test",
				Command:     "ls",
				Timeout:     10,
			},
			expectError: true,
			errorMsg:    "command not in allowed list: ls",
		},

		// Wildcard patterns
		{
			name:            "wildcard pattern allows command",
			allowedCommands: []string{"ls *"},
			input: BashInput{
				Description: "test",
				Command:     "ls -la",
				Timeout:     10,
			},
			expectError: false,
		},
		{
			name:            "wildcard pattern allows command with args",
			allowedCommands: []string{"ls *"},
			input: BashInput{
				Description: "test",
				Command:     "ls -la",
				Timeout:     10,
			},
			expectError: false,
		},
		{
			name:            "wildcard pattern rejects non-matching command",
			allowedCommands: []string{"ls *"},
			input: BashInput{
				Description: "test",
				Command:     "cat file.txt",
				Timeout:     10,
			},
			expectError: true,
			errorMsg:    "command not in allowed list: cat file.txt",
		},

		// Multiple allowed commands
		{
			name:            "multiple commands - first matches",
			allowedCommands: []string{"ls *", "pwd", "echo *"},
			input: BashInput{
				Description: "test",
				Command:     "ls -la",
				Timeout:     10,
			},
			expectError: false,
		},
		{
			name:            "multiple commands - second matches",
			allowedCommands: []string{"ls *", "pwd", "echo *"},
			input: BashInput{
				Description: "test",
				Command:     "pwd",
				Timeout:     10,
			},
			expectError: false,
		},
		{
			name:            "multiple commands - third matches",
			allowedCommands: []string{"ls *", "pwd", "echo *"},
			input: BashInput{
				Description: "test",
				Command:     "echo hello world",
				Timeout:     10,
			},
			expectError: false,
		},
		{
			name:            "multiple commands - none match",
			allowedCommands: []string{"ls *", "pwd", "echo *"},
			input: BashInput{
				Description: "test",
				Command:     "cat file.txt",
				Timeout:     10,
			},
			expectError: true,
			errorMsg:    "command not in allowed list: cat file.txt",
		},

		// Complex commands with operators
		{
			name:            "compound command - both parts allowed",
			allowedCommands: []string{"echo *", "ls *"},
			input: BashInput{
				Description: "test",
				Command:     "echo hello && ls -la",
				Timeout:     10,
			},
			expectError: false,
		},
		{
			name:            "compound command - first part not allowed",
			allowedCommands: []string{"echo *", "ls *"},
			input: BashInput{
				Description: "test",
				Command:     "cat file.txt && ls -la",
				Timeout:     10,
			},
			expectError: true,
			errorMsg:    "command not in allowed list: cat file.txt",
		},
		{
			name:            "compound command - second part not allowed",
			allowedCommands: []string{"echo *", "ls *"},
			input: BashInput{
				Description: "test",
				Command:     "echo hello && cat file.txt",
				Timeout:     10,
			},
			expectError: true,
			errorMsg:    "command not in allowed list: cat file.txt",
		},
		{
			name:            "complex compound command with semicolon",
			allowedCommands: []string{"echo *", "pwd"},
			input: BashInput{
				Description: "test",
				Command:     "echo start; pwd; echo done",
				Timeout:     10,
			},
			expectError: false,
		},
		{
			name:            "complex compound command with or operator",
			allowedCommands: []string{"echo *", "pwd"},
			input: BashInput{
				Description: "test",
				Command:     "echo start || pwd",
				Timeout:     10,
			},
			expectError: false,
		},

		// Real world scenarios
		{
			name:            "npm commands",
			allowedCommands: []string{"npm *", "yarn *"},
			input: BashInput{
				Description: "build project",
				Command:     "npm run build",
				Timeout:     60,
			},
			expectError: false,
		},
		{
			name:            "git commands",
			allowedCommands: []string{"git status", "git log *", "git diff *"},
			input: BashInput{
				Description: "check git status",
				Command:     "git status",
				Timeout:     10,
			},
			expectError: false,
		},
		{
			name:            "git commands with args",
			allowedCommands: []string{"git status", "git log *", "git diff *"},
			input: BashInput{
				Description: "check git log",
				Command:     "git log --oneline -10",
				Timeout:     10,
			},
			expectError: false,
		},
		{
			name:            "git command not allowed",
			allowedCommands: []string{"git status", "git log *", "git diff *"},
			input: BashInput{
				Description: "git push",
				Command:     "git push origin main",
				Timeout:     10,
			},
			expectError: true,
			errorMsg:    "command not in allowed list: git push origin main",
		},

		// DENY FIRST: Test that banned commands are rejected even if in allowed list
		{
			name:            "banned command denied even when in allowed list - vim",
			allowedCommands: []string{"vim *", "echo *", "ls *"},
			input: BashInput{
				Description: "edit file",
				Command:     "vim file.txt",
				Timeout:     10,
			},
			expectError: true,
			errorMsg:    "command is banned: vim",
		},
		{
			name:            "banned command denied even when in allowed list - cd",
			allowedCommands: []string{"cd *", "echo *", "pwd"},
			input: BashInput{
				Description: "change directory",
				Command:     "cd /home",
				Timeout:     10,
			},
			expectError: true,
			errorMsg:    "command is banned: cd",
		},
		{
			name:            "banned command denied even when in allowed list - less",
			allowedCommands: []string{"less *", "cat *", "echo *"},
			input: BashInput{
				Description: "view file",
				Command:     "less file.txt",
				Timeout:     10,
			},
			expectError: true,
			errorMsg:    "command is banned: less",
		},
		{
			name:            "banned command denied even when in allowed list - more",
			allowedCommands: []string{"more *", "cat *", "echo *"},
			input: BashInput{
				Description: "view file",
				Command:     "more file.txt",
				Timeout:     10,
			},
			expectError: true,
			errorMsg:    "command is banned: more",
		},
		{
			name:            "banned command denied even when in allowed list - view",
			allowedCommands: []string{"view *", "cat *", "echo *"},
			input: BashInput{
				Description: "view file",
				Command:     "view file.txt",
				Timeout:     10,
			},
			expectError: true,
			errorMsg:    "command is banned: view",
		},
		{
			name:            "banned command in compound statement denied - with allowed",
			allowedCommands: []string{"echo *", "vim *"},
			input: BashInput{
				Description: "echo then edit",
				Command:     "echo hello && vim file.txt",
				Timeout:     10,
			},
			expectError: true,
			errorMsg:    "command is banned: vim",
		},
		{
			name:            "banned command mixed with allowed commands - first command banned",
			allowedCommands: []string{"ls *", "cd *", "pwd"},
			input: BashInput{
				Description: "change dir then list",
				Command:     "cd /tmp && ls -la",
				Timeout:     10,
			},
			expectError: true,
			errorMsg:    "command is banned: cd",
		},
		{
			name:            "banned command mixed with allowed commands - second command banned",
			allowedCommands: []string{"ls *", "vim *", "pwd"},
			input: BashInput{
				Description: "list then edit",
				Command:     "ls -la && vim file.txt",
				Timeout:     10,
			},
			expectError: true,
			errorMsg:    "command is banned: vim",
		},
		{
			name:            "work around to banned command - with parenthesis", // this is OK by design
			allowedCommands: []string{"(cd *", "ls *", "pwd"},
			input: BashInput{
				Description: "cd then ls",
				Command:     "(cd /foo && ls -la) && pwd",
				Timeout:     10,
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := NewBashTool(tt.allowedCommands)
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

func TestBashToolResult_StructuredDataFields(t *testing.T) {
	tool := NewBashTool(nil)
	state := NewBasicState(context.TODO())

	t.Run("successful command has all metadata fields", func(t *testing.T) {
		input := BashInput{
			Description: "Test command",
			Command:     "echo 'hello world'",
			Timeout:     10,
		}

		inputJSON, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), state, string(inputJSON))

		assert.False(t, result.IsError())

		structuredResult := result.StructuredData()
		assert.True(t, structuredResult.Success)
		assert.Equal(t, "bash", structuredResult.ToolName)

		// Extract metadata and verify all fields are populated
		bashResult, ok := result.(*BashToolResult)
		assert.True(t, ok)

		// Verify execution time is tracked
		assert.Greater(t, bashResult.executionTime, time.Duration(0))

		// Verify exit code is set correctly
		assert.Equal(t, 0, bashResult.exitCode)

		// Verify working directory is captured
		assert.NotEmpty(t, bashResult.workingDir)

		// Verify command and output
		assert.Equal(t, "echo 'hello world'", bashResult.command)
		assert.Contains(t, bashResult.combinedOutput, "hello world")

		// Check structured metadata
		metadata := structuredResult.Metadata.(*tooltypes.BashMetadata)
		assert.Equal(t, "echo 'hello world'", metadata.Command)
		assert.Equal(t, 0, metadata.ExitCode)
		assert.Greater(t, metadata.ExecutionTime, time.Duration(0))
		assert.NotEmpty(t, metadata.WorkingDir)
		assert.Contains(t, metadata.Output, "hello world")
	})

	t.Run("failed command has correct exit code", func(t *testing.T) {
		input := BashInput{
			Description: "Test failing command",
			Command:     "exit 42",
			Timeout:     10,
		}

		inputJSON, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), state, string(inputJSON))

		assert.True(t, result.IsError())

		bashResult, ok := result.(*BashToolResult)
		assert.True(t, ok)

		// Verify execution time is tracked even for failures
		assert.Greater(t, bashResult.executionTime, time.Duration(0))

		// Verify correct exit code is captured
		assert.Equal(t, 42, bashResult.exitCode)

		// Verify working directory is captured
		assert.NotEmpty(t, bashResult.workingDir)

		// Check structured metadata for error case
		structuredResult := result.StructuredData()
		assert.False(t, structuredResult.Success)
		assert.Contains(t, structuredResult.Error, "Command exited with status 42")
	})

	t.Run("timeout has execution time and working dir", func(t *testing.T) {
		input := BashInput{
			Description: "Test timeout command",
			Command:     "sleep 20", // Will timeout with 10s limit
			Timeout:     10,
		}

		inputJSON, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), state, string(inputJSON))

		assert.True(t, result.IsError())

		bashResult, ok := result.(*BashToolResult)
		assert.True(t, ok)

		// Verify execution time is tracked even for timeouts
		assert.Greater(t, bashResult.executionTime, time.Duration(0))

		// Verify working directory is captured
		assert.NotEmpty(t, bashResult.workingDir)

		// Verify timeout error message
		assert.Contains(t, bashResult.error, "Command timed out after 10 seconds")
	})
}

// Helper function to check if a process is still running
func isProcessRunning(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}

// Helper function to kill a process and wait for it to exit
func killAndWaitProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}

	// Try graceful termination first
	if err := process.Signal(syscall.SIGTERM); err != nil {
		// If SIGTERM fails, try SIGKILL
		if err := process.Signal(syscall.SIGKILL); err != nil {
			return err
		}
	}

	// Wait for process to actually exit
	for i := 0; i < 50; i++ { // Wait up to 500ms
		if !isProcessRunning(pid) {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}

	return nil
}

// Helper function to cleanup background processes during test teardown
func cleanupBackgroundProcesses(t *testing.T, state tooltypes.State) {
	t.Helper()
	processes := state.GetBackgroundProcesses()
	for _, process := range processes {
		if isProcessRunning(process.PID) {
			t.Logf("Cleaning up background process PID %d", process.PID)
			if err := killAndWaitProcess(process.PID); err != nil {
				t.Logf("Failed to kill process %d: %v", process.PID, err)
			}
		}
		// Remove from state tracking
		state.RemoveBackgroundProcess(process.PID)
	}
}

func TestBashTool_ProcessDetachment_MultipleProcesses(t *testing.T) {
	tool := &BashTool{}

	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "bash_multi_detach_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Change to temp directory
	oldPwd, _ := os.Getwd()
	defer os.Chdir(oldPwd)
	os.Chdir(tempDir)

	state := NewBasicState(context.TODO())
	defer cleanupBackgroundProcesses(t, state)

	// Start multiple background processes
	var results []*BackgroundBashToolResult
	for i := 0; i < 3; i++ {
		input := BashInput{
			Description: fmt.Sprintf("Background process %d", i),
			Command:     fmt.Sprintf("sleep 1 && echo 'process_%d_done' > output_%d.txt", i, i),
			Timeout:     0,
			Background:  true,
		}
		params, _ := json.Marshal(input)

		result := tool.Execute(context.Background(), state, string(params))
		assert.False(t, result.IsError(), "Background execution should not error")

		bgResult, ok := result.(*BackgroundBashToolResult)
		require.True(t, ok, "Result should be BackgroundBashToolResult")
		results = append(results, bgResult)
	}

	// Verify all processes are tracked
	processes := state.GetBackgroundProcesses()
	assert.Len(t, processes, 3, "Should have three background processes")

	// Verify all processes are running
	for _, bgResult := range results {
		assert.True(t, isProcessRunning(bgResult.pid), "Background process %d should be running", bgResult.pid)
	}

	// Wait for all processes to complete
	time.Sleep(1500 * time.Millisecond)

	// Verify all processes have completed
	for _, bgResult := range results {
		assert.False(t, isProcessRunning(bgResult.pid), "Background process %d should have completed", bgResult.pid)
	}

	// Check that all output files were created
	for i := 0; i < 3; i++ {
		outputFile := filepath.Join(tempDir, fmt.Sprintf("output_%d.txt", i))
		assert.FileExists(t, outputFile)

		content, err := os.ReadFile(outputFile)
		require.NoError(t, err)
		assert.Contains(t, string(content), fmt.Sprintf("process_%d_done", i))
	}

	// Allow time for cleanup
	time.Sleep(100 * time.Millisecond)

	// All processes should be removed from state tracking
	processes = state.GetBackgroundProcesses()
	assert.Len(t, processes, 0, "All background processes should be removed from state after completion")
}

func TestBashTool_ProcessDetachment_LogFileOutput(t *testing.T) {
	tool := &BashTool{}

	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "bash_log_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Change to temp directory
	oldPwd, _ := os.Getwd()
	defer os.Chdir(oldPwd)
	os.Chdir(tempDir)

	state := NewBasicState(context.TODO())
	defer cleanupBackgroundProcesses(t, state)

	// Start a background process that produces output
	input := BashInput{
		Description: "Process with output",
		Command:     "echo 'first_line' && echo 'second_line' && echo 'third_line'",
		Timeout:     0,
		Background:  true,
	}
	params, _ := json.Marshal(input)

	result := tool.Execute(context.Background(), state, string(params))
	assert.False(t, result.IsError(), "Background execution should not error")

	bgResult, ok := result.(*BackgroundBashToolResult)
	require.True(t, ok, "Result should be BackgroundBashToolResult")

	// Verify log file path is correct
	expectedLogPath := filepath.Join(tempDir, ".kodelet", strconv.Itoa(bgResult.pid), "out.log")
	assert.Equal(t, expectedLogPath, bgResult.logPath)

	// Wait for process to complete and output to be written
	// Poll until process completes
	for i := 0; i < 50; i++ {
		if !isProcessRunning(bgResult.pid) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Give a bit more time for the goroutine to capture all output
	time.Sleep(200 * time.Millisecond)

	// Verify log file contains expected output
	logContent, err := os.ReadFile(bgResult.logPath)
	require.NoError(t, err)

	logString := string(logContent)
	assert.Contains(t, logString, "first_line")
	assert.Contains(t, logString, "second_line")
	assert.Contains(t, logString, "third_line")

	// Verify the log file has proper directory structure
	assert.FileExists(t, bgResult.logPath)
	assert.DirExists(t, filepath.Dir(bgResult.logPath))
}

func TestBashTool_ProcessDetachment_ErrorHandling(t *testing.T) {
	tool := &BashTool{}

	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "bash_error_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Change to temp directory
	oldPwd, _ := os.Getwd()
	defer os.Chdir(oldPwd)
	os.Chdir(tempDir)

	state := NewBasicState(context.TODO())
	defer cleanupBackgroundProcesses(t, state)

	t.Run("command that fails", func(t *testing.T) {
		input := BashInput{
			Description: "Failing command",
			Command:     "exit 42",
			Timeout:     0,
			Background:  true,
		}
		params, _ := json.Marshal(input)

		result := tool.Execute(context.Background(), state, string(params))
		assert.False(t, result.IsError(), "Background execution should not error even if command fails")

		bgResult, ok := result.(*BackgroundBashToolResult)
		require.True(t, ok, "Result should be BackgroundBashToolResult")

		// Wait for process to complete
		time.Sleep(200 * time.Millisecond)

		// Check log file for error message
		logContent, err := os.ReadFile(bgResult.logPath)
		require.NoError(t, err)
		assert.Contains(t, string(logContent), "Process exited with error")
	})

	t.Run("invalid command", func(t *testing.T) {
		input := BashInput{
			Description: "Invalid command",
			Command:     "nonexistentcommand12345",
			Timeout:     0,
			Background:  true,
		}
		params, _ := json.Marshal(input)

		result := tool.Execute(context.Background(), state, string(params))
		assert.False(t, result.IsError(), "Background execution should not error even if command is invalid")

		bgResult, ok := result.(*BackgroundBashToolResult)
		require.True(t, ok, "Result should be BackgroundBashToolResult")

		// Wait for process to complete
		time.Sleep(200 * time.Millisecond)

		// Check log file for error message
		logContent, err := os.ReadFile(bgResult.logPath)
		require.NoError(t, err)
		assert.Contains(t, string(logContent), "command not found")
	})
}

func TestBashTool_ProcessDetachment_StructuredData(t *testing.T) {
	tool := &BashTool{}

	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "bash_structured_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Change to temp directory
	oldPwd, _ := os.Getwd()
	defer os.Chdir(oldPwd)
	os.Chdir(tempDir)

	state := NewBasicState(context.TODO())
	defer cleanupBackgroundProcesses(t, state)

	// Start a background process
	input := BashInput{
		Description: "Structured data test",
		Command:     "echo 'structured test'",
		Timeout:     0,
		Background:  true,
	}
	params, _ := json.Marshal(input)

	result := tool.Execute(context.Background(), state, string(params))
	assert.False(t, result.IsError(), "Background execution should not error")

	bgResult, ok := result.(*BackgroundBashToolResult)
	require.True(t, ok, "Result should be BackgroundBashToolResult")

	// Test structured data
	structuredData := result.StructuredData()
	assert.Equal(t, "bash_background", structuredData.ToolName)
	assert.True(t, structuredData.Success)
	assert.Empty(t, structuredData.Error)
	assert.NotNil(t, structuredData.Metadata)

	// Check background-specific metadata
	metadata, ok := structuredData.Metadata.(*tooltypes.BackgroundBashMetadata)
	require.True(t, ok, "Metadata should be BackgroundBashMetadata")
	assert.Equal(t, input.Command, metadata.Command)
	assert.Equal(t, bgResult.pid, metadata.PID)
	assert.Equal(t, bgResult.logPath, metadata.LogPath)
	assert.False(t, metadata.StartTime.IsZero())

	// Test assistant-facing result
	assistantResult := result.AssistantFacing()
	assert.Contains(t, assistantResult, "Process is up and running")
	assert.Contains(t, assistantResult, bgResult.logPath)
}

// waitForLogContent polls a log file until the expected content is found or max retries is reached.
// It returns the file content when the expected content is found, or fails the test if not found within the retry limit.
// The interval parameter specifies the delay between polling attempts. Use 0 to default to 25ms.
func waitForLogContent(t *testing.T, logPath, expectedContent string, maxRetries int, interval time.Duration) []byte {
	t.Helper()

	if interval == 0 {
		interval = 25 * time.Millisecond
	}

	for i := 0; i < maxRetries; i++ {
		time.Sleep(interval)
		content, err := os.ReadFile(logPath)
		if err == nil && strings.Contains(string(content), expectedContent) {
			return content
		}
	}

	require.Failf(t, "Content not found", "Should find '%s' in log file %s within reasonable time (%d retries, %v interval)", expectedContent, logPath, maxRetries, interval)
	return nil // unreachable, but needed for compilation
}
