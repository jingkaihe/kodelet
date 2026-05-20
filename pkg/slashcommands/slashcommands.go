// Package slashcommands provides recipe-backed slash command parsing and expansion.
package slashcommands

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/fragments"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/pkg/errors"
)

const additionalInstructionsHeader = "\n\n---\n\nAdditional instructions:\n"

// Command describes an available slash command backed by a recipe.
type Command struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Hint        string `json:"hint,omitempty"`
}

// Expansion is the result of rendering a slash command.
type Expansion struct {
	Command      string
	Prompt       string
	Display      string
	Arguments    map[string]string
	Instructions string
}

// Parse parses a slash command from text. The command name is everything after
// the leading slash up to the first space. Names may contain slashes.
func Parse(text string) (command string, args string, found bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return "", "", false
	}

	text = strings.TrimPrefix(text, "/")
	parts := strings.SplitN(text, " ", 2)
	command = parts[0]
	if command == "" {
		return "", "", false
	}
	if len(parts) > 1 {
		args = parts[1]
	}
	return command, args, true
}

// ParseArgs parses key=value pairs and additional text from an arguments string.
//
// Grammar:
//
//	args           = *(key_value / word) [additional_text]
//	key_value      = key "=" value
//	key            = 1*non_space_non_eq
//	value          = quoted_value / unquoted_value
//	quoted_value   = DQUOTE *non_dquote DQUOTE
//	unquoted_value = *non_space
//	word           = 1*non_space (collected as additional_text)
func ParseArgs(args string) (kvArgs map[string]string, additionalText string) {
	kvArgs = make(map[string]string)
	if args == "" {
		return kvArgs, ""
	}

	var textParts []string
	i := 0
	for i < len(args) {
		i = skipSpaces(args, i)
		if i >= len(args) {
			break
		}

		keyEnd := findKeyEnd(args, i)

		if keyEnd < len(args) && args[keyEnd] == '=' {
			key := args[i:keyEnd]
			value, nextPos := parseValue(args, keyEnd+1)
			kvArgs[key] = value
			i = nextPos
		} else {
			wordEnd := findWordEnd(args, i)
			textParts = append(textParts, args[i:wordEnd])
			i = wordEnd
		}
	}

	return kvArgs, strings.Join(textParts, " ")
}

func skipSpaces(s string, i int) int {
	for i < len(s) && s[i] == ' ' {
		i++
	}
	return i
}

func findKeyEnd(s string, start int) int {
	i := start
	for i < len(s) && s[i] != '=' && s[i] != ' ' {
		i++
	}
	return i
}

func findWordEnd(s string, start int) int {
	i := start
	for i < len(s) && s[i] != ' ' {
		i++
	}
	return i
}

func parseValue(s string, start int) (value string, nextPos int) {
	if start >= len(s) {
		return "", start
	}

	if s[start] == '"' {
		return parseQuotedValue(s, start+1)
	}
	return parseUnquotedValue(s, start)
}

func parseQuotedValue(s string, start int) (value string, nextPos int) {
	end := start
	for end < len(s) && s[end] != '"' {
		end++
	}
	if end < len(s) {
		return s[start:end], end + 1
	}
	return s[start:end], end
}

func parseUnquotedValue(s string, start int) (value string, nextPos int) {
	end := findWordEnd(s, start)
	return s[start:end], end
}

// List returns available slash commands from the fragment/recipe system.
func List(ctx context.Context, processor *fragments.Processor) []Command {
	if processor == nil {
		return nil
	}

	frags, err := processor.ListFragmentsWithMetadata()
	if err != nil {
		logger.G(ctx).WithError(err).Warn("Failed to list fragments for slash commands")
		return nil
	}

	commands := make([]Command, 0, len(frags))
	for _, frag := range frags {
		name := frag.ID
		description := frag.Metadata.Description
		if description == "" {
			description = "Run the " + frag.ID + " recipe"
		}

		commands = append(commands, Command{
			Name:        name,
			Description: description,
			Hint:        BuildCommandHint(frag.Metadata.Arguments),
		})
	}

	return commands
}

// Expand renders a slash command into the full prompt sent to the model and a
// compact display string for user-facing conversation renderers.
func Expand(ctx context.Context, processor *fragments.Processor, command, args string) (*Expansion, error) {
	if processor == nil {
		return nil, errors.New("slash commands are unavailable")
	}

	kvArgs, additionalText := ParseArgs(args)
	fragment, err := processor.LoadFragment(ctx, &fragments.Config{
		FragmentName: command,
		Arguments:    kvArgs,
	})
	if err != nil {
		return nil, errors.Wrapf(err, "unknown recipe '/%s'. Available recipes: %s", command, AvailableRecipeNames(ctx, processor))
	}

	var promptBuilder strings.Builder
	promptBuilder.WriteString(fragment.Content)
	if additionalText != "" {
		promptBuilder.WriteString(additionalInstructionsHeader)
		promptBuilder.WriteString(additionalText)
	}

	return &Expansion{
		Command:      command,
		Prompt:       promptBuilder.String(),
		Display:      BuildDisplay(command, args),
		Arguments:    kvArgs,
		Instructions: additionalText,
	}, nil
}

// BuildDisplay returns the compact user-facing slash invocation.
func BuildDisplay(command, args string) string {
	display := "/" + strings.TrimSpace(command)
	if trimmedArgs := strings.TrimSpace(args); trimmedArgs != "" {
		display += " " + trimmedArgs
	}
	return display
}

// AvailableRecipeNames returns a comma-separated list of available recipe names.
func AvailableRecipeNames(ctx context.Context, processor *fragments.Processor) string {
	commands := List(ctx, processor)
	if len(commands) == 0 {
		return "(none available)"
	}

	names := make([]string, len(commands))
	for i, cmd := range commands {
		names[i] = "/" + cmd.Name
	}
	return strings.Join(names, ", ")
}

// BuildCommandHint builds a hint string for a recipe based on its arguments.
func BuildCommandHint(arguments map[string]fragments.ArgumentMeta) string {
	if len(arguments) == 0 {
		return "additional instructions (optional)"
	}

	keys := make([]string, 0, len(arguments))
	for k := range arguments {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, key := range keys {
		argMeta := arguments[key]
		if argMeta.Default != "" {
			parts = append(parts, fmt.Sprintf("%s=%s", key, argMeta.Default))
		} else {
			parts = append(parts, fmt.Sprintf("%s=<value>", key))
		}
	}

	return fmt.Sprintf("[%s] additional instructions", strings.Join(parts, " "))
}
