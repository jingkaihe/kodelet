package extensions

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeShortcutKey(t *testing.T) {
	for _, test := range []struct {
		name     string
		input    string
		expected string
	}{
		{name: "canonical", input: "ctrl+r", expected: "ctrl+r"},
		{name: "modifier aliases and order", input: " Option+Control+R ", expected: "ctrl+alt+r"},
		{name: "option alias", input: "option+p", expected: "alt+p"},
		{name: "alt digit", input: "ALT+5", expected: "alt+5"},
		{name: "function key", input: "F12", expected: "f12"},
	} {
		t.Run(test.name, func(t *testing.T) {
			actual, err := NormalizeShortcutKey(test.input)
			require.NoError(t, err)
			assert.Equal(t, test.expected, actual)
		})
	}

	for _, input := range []string{
		"",
		"r",
		"shift+r",
		"ctrl+shift+r",
		"cmd+r",
		"ctrl+r extra",
		"ctrl+ctrl+r",
		"f13",
		"alt+f5",
		"ctrl+1",
		"ctrl+up",
		"alt+space",
		"ctrl+unknown",
		"ctrl+é",
	} {
		t.Run("invalid "+input, func(t *testing.T) {
			_, err := NormalizeShortcutKey(input)
			require.Error(t, err)
		})
	}
}

func TestNormalizeShortcutKeyRejectsTerminalAliases(t *testing.T) {
	for _, test := range []struct {
		input       string
		terminalKey string
	}{
		{input: "ctrl+i", terminalKey: "tab"},
		{input: "ctrl+m", terminalKey: "enter"},
		{input: "ctrl+alt+i", terminalKey: "tab"},
		{input: "alt+ctrl+m", terminalKey: "enter"},
	} {
		t.Run(test.input, func(t *testing.T) {
			_, err := NormalizeShortcutKey(test.input)
			require.Error(t, err)
			assert.ErrorContains(t, err, "terminals report")
			assert.ErrorContains(t, err, test.terminalKey)
		})
	}
}

func TestRuntimeShortcutRegistrationUsesLastExtensionAndReportsConflict(t *testing.T) {
	runtime := EmptyRuntime()
	sink := newRecordingDiagnosticSink()
	ctx := ContextWithDiagnosticSink(context.Background(), sink)

	require.NoError(t, runtime.register(ctx, &Process{Extension: Extension{ID: "first"}}, &InitializeResult{
		Shortcuts: []ShortcutRegistration{{Key: "ctrl+r", Description: "First action"}},
	}))
	require.NoError(t, runtime.register(ctx, &Process{Extension: Extension{ID: "second"}}, &InitializeResult{
		Shortcuts: []ShortcutRegistration{{Key: "control+r", Description: "Second action"}},
	}))

	assert.Equal(t, []Shortcut{{Key: "ctrl+r", Description: "Second action", ExtensionID: "second"}}, runtime.Shortcuts())
	diagnostic := receiveDiagnostic(t, sink.ch)
	assert.Equal(t, DiagnosticLevelWarning, diagnostic.Level)
	assert.Equal(t, "second", diagnostic.Extension)
	assert.Contains(t, diagnostic.Message, "conflicts with extension first")
}

func TestRuntimeSkipsInvalidShortcutRegistration(t *testing.T) {
	runtime := EmptyRuntime()
	sink := newRecordingDiagnosticSink()
	ctx := ContextWithDiagnosticSink(context.Background(), sink)

	require.NoError(t, runtime.register(ctx, &Process{Extension: Extension{ID: "bad"}}, &InitializeResult{
		Shortcuts: []ShortcutRegistration{{Key: "ctrl+i", Description: "Unavailable"}},
	}))

	assert.Empty(t, runtime.Shortcuts())
	diagnostic := receiveDiagnostic(t, sink.ch)
	assert.Contains(t, diagnostic.Message, "terminals report ctrl+i as tab")
}
