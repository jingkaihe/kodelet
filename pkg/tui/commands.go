package tui

import (
	"strings"
)

// Command represents a chat command
type Command string

const (
	CommandHelp        Command = "help"
	CommandClear       Command = "clear"
	CommandBash        Command = "bash"
	CommandAddImage    Command = "add-image"
	CommandRemoveImage Command = "remove-image"
)

// GetAvailableCommands returns the list of available slash commands
func GetAvailableCommands() []string {
	return []string{
		"/bash",
		"/add-image",
		"/remove-image",
		"/help",
		"/clear",
	}
}

// ParseCommand parses a user input and returns the command name, arguments, and whether it's a valid command
func ParseCommand(input string) (command string, args string, isCommand bool) {
	input = strings.TrimSpace(input)

	if !strings.HasPrefix(input, "/") {
		return "", "", false
	}

	parts := strings.SplitN(input, " ", 2)
	commandName := strings.TrimPrefix(parts[0], "/")

	var arguments string
	if len(parts) > 1 {
		arguments = parts[1]
	}

	validCommands := map[string]bool{
		string(CommandHelp):        true,
		string(CommandClear):       true,
		string(CommandBash):        true,
		string(CommandAddImage):    true,
		string(CommandRemoveImage): true,
	}

	if !validCommands[commandName] {
		return "", "", false
	}

	return commandName, arguments, true
}

// GetHelpText returns the help text for keyboard shortcuts and commands
func GetHelpText() string {
	return `╔══════════════════════════════════════════════════════════╗
║                    KODELET CHAT HELP                     ║
╚══════════════════════════════════════════════════════════╝

KEYBOARD SHORTCUTS
   Ctrl+C (twice)    → Quit the chat
   Ctrl+S            → Send message
   Ctrl+H            → Show this help
   Ctrl+L            → Clear screen
   PageUp/PageDown   → Scroll history
   Up/Down           → Navigate suggestions

AVAILABLE COMMANDS
   /bash [command]            → Execute bash command and include result
   /add-image [path]          → Add an image to the chat
   /remove-image [path]       → Remove an image from the chat
   /help                      → Show this help message
   /clear                     → Clear the screen

TIP: Start typing "/" to see command suggestions!`
}

// IsCommandComplete checks if the current input is a complete command
// (i.e., starts with a known command prefix)
func IsCommandComplete(input string, commands []string) bool {
	for _, cmd := range commands {
		if strings.HasPrefix(input, cmd) {
			return true
		}
	}
	return false
}
