package extensions

import (
	"context"
	"strings"

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

// TryCommand routes a parsed slash command through extension command registrations.
func (r *Runtime) TryCommand(ctx context.Context, rawPrompt, commandName, args string, callContext ExtensionCallContext) (*RoutedCommandResult, error) {
	if r == nil {
		return &RoutedCommandResult{}, nil
	}

	for _, command := range r.matchingCommands(commandName) {
		input, invocation := buildCommandInput(rawPrompt, commandName, args)
		timeout := r.eventTimeout("command.execute")
		callCtx := ctx
		cancel := func() {}
		if timeout > 0 {
			var cancelFunc context.CancelFunc
			callCtx, cancelFunc = context.WithTimeout(ctx, timeout)
			cancel = cancelFunc
		}
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

func (r *Runtime) matchingCommands(commandName string) []Command {
	commandName = strings.TrimPrefix(strings.TrimSpace(commandName), "/")
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
	candidate = strings.TrimPrefix(strings.TrimSpace(candidate), "/")
	return candidate == commandName
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
