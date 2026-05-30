package extensions

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSlashCommandsConvertsCommandsAndRecipes(t *testing.T) {
	converted := SlashCommands([]Command{
		{Registration: CommandRegistration{Name: "/doctor"}},
		{Registration: CommandRegistration{Name: "review", Description: "Review code", Kind: "recipe"}},
		{Registration: CommandRegistration{Name: "   ", Description: "ignored"}},
	})

	require.Len(t, converted, 2)
	assert.Equal(t, "doctor", converted[0].Name)
	assert.Equal(t, "Run the doctor extension command", converted[0].Description)
	assert.Equal(t, "arguments (optional)", converted[0].Hint)
	assert.Equal(t, "/doctor arguments (optional)", converted[0].Placeholder)
	assert.Equal(t, "review", converted[1].Name)
	assert.Equal(t, "Review code", converted[1].Description)
	assert.Equal(t, "additional instructions (optional)", converted[1].Hint)
	assert.Equal(t, "/review additional instructions (optional)", converted[1].Placeholder)
}

func TestRuntimeSlashCommandsHandlesNilRuntime(t *testing.T) {
	var runtime *Runtime
	assert.Nil(t, runtime.SlashCommands())
}

func TestMatchingCommandsMatchesNamesAndAliases(t *testing.T) {
	runtime := EmptyRuntime()
	runtime.commands = []Command{
		{Registration: CommandRegistration{Name: "doctor", Aliases: []string{"/doc"}}},
		{Registration: CommandRegistration{Name: "review", Aliases: []string{"/inspect"}}},
	}

	matches := runtime.matchingCommands("/doc")
	require.Len(t, matches, 1)
	assert.Equal(t, "doctor", matches[0].Registration.Name)
	assert.Empty(t, runtime.matchingCommands(""))
	assert.Empty(t, runtime.matchingCommands("missing"))
}

func TestBuildCommandInputParsesFlagsTextAndInvocation(t *testing.T) {
	input, invocation := buildCommandInput(
		"/review target=main draft=true focus tests",
		"review",
		"target=main draft=true focus tests",
	)

	assert.Equal(t, map[string]any{
		"target": "main",
		"draft":  "true",
		"text":   "focus tests",
	}, input)
	assert.Equal(t, "/review target=main draft=true focus tests", invocation.Raw)
	assert.Equal(t, "review", invocation.CommandName)
	assert.Equal(t, []string{"target=main", "draft=true", "focus", "tests"}, invocation.Args)
	assert.Equal(t, map[string]any{"target": "main", "draft": "true"}, invocation.Flags)
}
