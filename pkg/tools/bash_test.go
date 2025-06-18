package tools

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

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
		Command:     "sleep 1",
		Timeout:     1,
	}
	params, _ := json.Marshal(input)

	result := tool.Execute(context.Background(), NewBasicState(context.TODO()), string(params))
	assert.Contains(t, result.GetError(), "Command timed out after 1 seconds")
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

	input := BashInput{
		Description: "Background echo test",
		Command:     "echo 'background process' && sleep 0.5 && echo 'done'",
		Timeout:     0, // Background processes must have timeout=0
		Background:  true,
	}
	params, _ := json.Marshal(input)

	state := NewBasicState(context.TODO())
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

	// Wait a bit for the process to write to log file
	time.Sleep(400 * time.Millisecond)

	content, err := os.ReadFile(bgResult.logPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "background process")
	assert.NotContains(t, string(content), "done")

	time.Sleep(200 * time.Millisecond)

	content, err = os.ReadFile(bgResult.logPath)
	require.NoError(t, err)
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
