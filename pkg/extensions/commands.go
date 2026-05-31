package extensions

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/slashcommands"
	"github.com/pkg/errors"
)

const (
	// CommandActionPass means the command declined handling and routing should continue.
	CommandActionPass = "pass"
	// CommandActionRespond means the command handled the prompt with a direct UI response.
	CommandActionRespond = "respond"
	// CommandActionRunAgent means the command produced a replacement prompt for the agent.
	CommandActionRunAgent = "runAgent"
)

// RoutedCommandResult is returned by TryCommand.
type RoutedCommandResult struct {
	Matched      bool
	CommandName  string
	ExtensionID  string
	Action       string
	Response     string
	Prompt       string
	RecipeName   string
	Display      string
	Registration CommandRegistration
}

// SlashCommands returns extension command registrations in the shared slash
// command shape used by CLI/ACP/web command discovery surfaces.
func (r *Runtime) SlashCommands() []slashcommands.Command {
	if r == nil {
		return nil
	}
	return SlashCommands(r.Commands())
}

// SlashCommands converts extension command registrations to slash commands.
func SlashCommands(commands []Command) []slashcommands.Command {
	converted := make([]slashcommands.Command, 0, len(commands))
	for _, command := range commands {
		registration := command.Registration
		name := normalizeCommandName(registration.Name)
		if name == "" {
			continue
		}
		description := strings.TrimSpace(registration.Description)
		if description == "" {
			description = "Run the " + name + " extension command"
		}
		hint := commandHint(registration)
		converted = append(converted, slashcommands.Command{
			Name:        name,
			Description: description,
			Hint:        hint,
			Placeholder: "/" + name + " " + hint,
		})
	}
	return converted
}

func commandHint(registration CommandRegistration) string {
	argsHint := schemaArgumentsHint(registration.InputSchema)
	if registration.Kind == "recipe" {
		if argsHint != "" {
			return argsHint + " additional instructions"
		}
		return "additional instructions (optional)"
	}
	if argsHint != "" {
		return argsHint
	}
	return "arguments (optional)"
}

func schemaArgumentsHint(schema map[string]any) string {
	properties, ok := schema["properties"].(map[string]any)
	if !ok || len(properties) == 0 {
		return ""
	}

	keys := make([]string, 0, len(properties))
	for key := range properties {
		if strings.TrimSpace(key) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		property, _ := properties[key].(map[string]any)
		if value, ok := property["default"]; ok {
			parts = append(parts, fmt.Sprintf("%s=%s", key, schemaDefaultHint(value)))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=<value>", key))
	}
	if len(parts) == 0 {
		return ""
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func schemaDefaultHint(value any) string {
	switch typed := value.(type) {
	case string:
		if strings.ContainsAny(typed, " \t\n\r") {
			return fmt.Sprintf("%q", typed)
		}
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return fmt.Sprint(typed)
	}
}

// TryCommand routes a parsed slash command through extension command registrations.
func (r *Runtime) TryCommand(ctx context.Context, rawPrompt, commandName, args string, callContext ExtensionCallContext) (*RoutedCommandResult, error) {
	if r == nil {
		return &RoutedCommandResult{}, nil
	}

	for _, command := range r.matchingCommands(commandName) {
		input, invocation := buildCommandInput(rawPrompt, commandName, args)
		callCtx, cancel := contextWithOptionalDuration(ctx, commandTimeout(command.Registration))
		result, err := command.Process.ExecuteCommand(callCtx, command.Registration.Name, input, invocation, callContext)
		cancel()
		if err != nil {
			return nil, errors.Wrapf(err, "failed to execute extension command %s", command.Registration.Name)
		}
		if result == nil || result.Action == "" || result.Action == CommandActionPass {
			continue
		}
		routed := &RoutedCommandResult{
			Matched:      true,
			CommandName:  command.Registration.Name,
			ExtensionID:  command.ExtensionID,
			Action:       result.Action,
			Response:     result.Response,
			Prompt:       result.Prompt,
			RecipeName:   result.RecipeName,
			Display:      slashcommands.BuildDisplay(commandName, args),
			Registration: command.Registration,
		}
		if routed.RecipeName == "" && command.Registration.Kind == "recipe" {
			routed.RecipeName = command.Registration.Name
		}
		return routed, nil
	}

	return &RoutedCommandResult{}, nil
}

func commandTimeout(registration CommandRegistration) time.Duration {
	return timeoutInSecDuration(registration.TimeoutInSec)
}

func (r *Runtime) matchingCommands(commandName string) []Command {
	commandName = normalizeCommandName(commandName)
	if commandName == "" {
		return nil
	}

	var matches []Command
	for _, command := range r.Commands() {
		registration := command.Registration
		if commandNameMatches(registration.Name, commandName) {
			matches = append(matches, command)
			continue
		}
		for _, alias := range registration.Aliases {
			if commandNameMatches(alias, commandName) {
				matches = append(matches, command)
				break
			}
		}
	}
	return matches
}

func commandNameMatches(candidate, commandName string) bool {
	return normalizeCommandName(candidate) == normalizeCommandName(commandName)
}

func normalizeCommandName(name string) string {
	return strings.TrimPrefix(strings.TrimSpace(name), "/")
}

func buildCommandInput(rawPrompt, commandName, args string) (map[string]any, CommandInvocation) {
	flags, additionalText := slashcommands.ParseArgs(args)
	input := make(map[string]any, len(flags)+1)
	for key, value := range flags {
		input[key] = value
	}
	if strings.TrimSpace(additionalText) != "" {
		input["text"] = additionalText
	}

	argList := []string{}
	if strings.TrimSpace(args) != "" {
		argList = strings.Fields(args)
	}

	flagValues := make(map[string]any, len(flags))
	for key, value := range flags {
		flagValues[key] = value
	}

	return input, CommandInvocation{
		Raw:         rawPrompt,
		CommandName: commandName,
		Args:        argList,
		Flags:       flagValues,
	}
}
