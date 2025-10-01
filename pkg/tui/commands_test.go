package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCommand(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		wantCommand   string
		wantArgs      string
		wantIsCommand bool
	}{
		{
			name:          "help command",
			input:         "/help",
			wantCommand:   "help",
			wantArgs:      "",
			wantIsCommand: true,
		},
		{
			name:          "clear command",
			input:         "/clear",
			wantCommand:   "clear",
			wantArgs:      "",
			wantIsCommand: true,
		},
		{
			name:          "bash command with args",
			input:         "/bash ls -la",
			wantCommand:   "bash",
			wantArgs:      "ls -la",
			wantIsCommand: true,
		},
		{
			name:          "add-image command with path",
			input:         "/add-image /path/to/image.png",
			wantCommand:   "add-image",
			wantArgs:      "/path/to/image.png",
			wantIsCommand: true,
		},
		{
			name:          "remove-image command with path",
			input:         "/remove-image /path/to/image.png",
			wantCommand:   "remove-image",
			wantArgs:      "/path/to/image.png",
			wantIsCommand: true,
		},
		{
			name:          "not a command",
			input:         "just a message",
			wantCommand:   "",
			wantArgs:      "",
			wantIsCommand: false,
		},
		{
			name:          "slash but not a command",
			input:         "/unknown command",
			wantCommand:   "",
			wantArgs:      "",
			wantIsCommand: false,
		},
		{
			name:          "empty string",
			input:         "",
			wantCommand:   "",
			wantArgs:      "",
			wantIsCommand: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, args, isCommand := ParseCommand(tt.input)
			assert.Equal(t, tt.wantCommand, cmd)
			assert.Equal(t, tt.wantArgs, args)
			assert.Equal(t, tt.wantIsCommand, isCommand)
		})
	}
}

func TestGetHelpText(t *testing.T) {
	helpText := GetHelpText()
	
	require.NotEmpty(t, helpText)
	assert.Contains(t, helpText, "Keyboard Shortcuts:")
	assert.Contains(t, helpText, "Ctrl+C")
	assert.Contains(t, helpText, "/bash")
	assert.Contains(t, helpText, "/add-image")
	assert.Contains(t, helpText, "/remove-image")
	assert.Contains(t, helpText, "/help")
	assert.Contains(t, helpText, "/clear")
}

func TestGetAvailableCommands(t *testing.T) {
	commands := GetAvailableCommands()
	
	require.NotEmpty(t, commands)
	assert.Contains(t, commands, "/bash")
	assert.Contains(t, commands, "/add-image")
	assert.Contains(t, commands, "/remove-image")
	assert.Contains(t, commands, "/help")
	assert.Contains(t, commands, "/clear")
}

func TestIsCommandComplete(t *testing.T) {
	commands := GetAvailableCommands()
	
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "complete bash command",
			input:    "/bash ls",
			expected: true,
		},
		{
			name:     "incomplete command",
			input:    "/ba",
			expected: false,
		},
		{
			name:     "complete help command",
			input:    "/help",
			expected: true,
		},
		{
			name:     "not a command",
			input:    "hello",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsCommandComplete(tt.input, commands)
			assert.Equal(t, tt.expected, result)
		})
	}
}
