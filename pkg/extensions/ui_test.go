package extensions

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTerminalUIInputBrokerUnavailableWhenNonInteractive(t *testing.T) {
	broker := NewTerminalUIInputBroker(strings.NewReader("answer\n"), &bytes.Buffer{})

	result, err := broker.Input(context.Background(), UIInputRequest{Title: "Question"})

	require.NoError(t, err)
	assert.Equal(t, UIInputStatusUnavailable, result.Status)
	assert.Contains(t, result.Reason, "terminal input is not available")
}

func TestTerminalUIInputBrokerPromptsForInput(t *testing.T) {
	var out bytes.Buffer
	broker := interactiveTerminalBroker("\n", &out)

	result, err := broker.Input(context.Background(), UIInputRequest{
		Title:            "Birthday?",
		HelpText:         "Use a date",
		Message:          "This is optional",
		Placeholder:      "January 1, 1990",
		DefaultValue:     "January 1, 2000",
		SubmitButtonText: "Send",
		Required:         true,
	})

	require.NoError(t, err)
	assert.Equal(t, UIInputStatusSubmitted, result.Status)
	assert.Equal(t, "January 1, 2000", result.Value)
	output := out.String()
	assert.Contains(t, output, "? Birthday?")
	assert.Contains(t, output, "Use a date")
	assert.Contains(t, output, "This is optional")
	assert.Contains(t, output, "Hint: January 1, 1990")
	assert.Contains(t, output, "Default: January 1, 2000")
	assert.Contains(t, output, "Send> ")
}

func TestTerminalUIInputBrokerDismissesRequiredEmptyInput(t *testing.T) {
	broker := interactiveTerminalBroker("\n", &bytes.Buffer{})

	result, err := broker.Input(context.Background(), UIInputRequest{Title: "Required", Required: true})

	require.NoError(t, err)
	assert.Equal(t, UIInputStatusDismissed, result.Status)
}

func TestTerminalUIInputBrokerConfirmSelectAndNotify(t *testing.T) {
	var out bytes.Buffer
	broker := interactiveTerminalBroker("yes\n2\n", &out)

	confirm, err := broker.Confirm(context.Background(), UIConfirmRequest{
		Title:             "Allow bash?",
		Message:           "A command is pending",
		ConfirmButtonText: "Allow",
		CancelButtonText:  "Deny",
	})
	require.NoError(t, err)
	assert.Equal(t, UIInputStatusSubmitted, confirm.Status)
	assert.True(t, confirm.Confirmed)
	assert.Equal(t, "true", confirm.Value)

	selection, err := broker.Select(context.Background(), UISelectRequest{
		Title:            "Pick food",
		Message:          "Choose one",
		Options:          []string{"Pasta", "Pizza", "Focaccia"},
		SubmitButtonText: "Pick",
	})
	require.NoError(t, err)
	assert.Equal(t, UIInputStatusSubmitted, selection.Status)
	assert.Equal(t, "Pizza", selection.Value)

	notification, err := broker.Notify(context.Background(), UINotifyRequest{Title: "Ready", Message: "Done"})
	require.NoError(t, err)
	assert.Equal(t, UIInputStatusSubmitted, notification.Status)

	output := out.String()
	assert.Contains(t, output, "? Allow bash?")
	assert.Contains(t, output, "Allow/Deny> ")
	assert.Contains(t, output, "? Pick food")
	assert.Contains(t, output, "2. Pizza")
	assert.Contains(t, output, "Pick> ")
	assert.Contains(t, output, "! Ready")
	assert.Contains(t, output, "Done")
}

func TestTerminalUIInputBrokerConfirmDismissAndSelectFreeform(t *testing.T) {
	broker := interactiveTerminalBroker("no\nSomething else\n", &bytes.Buffer{})

	confirm, err := broker.Confirm(context.Background(), UIConfirmRequest{Title: "Allow?"})
	require.NoError(t, err)
	assert.Equal(t, UIInputStatusDismissed, confirm.Status)
	assert.False(t, confirm.Confirmed)
	assert.Equal(t, "false", confirm.Value)

	selection, err := broker.Select(context.Background(), UISelectRequest{Title: "Pick", Options: []string{"A", "B"}})
	require.NoError(t, err)
	assert.Equal(t, UIInputStatusSubmitted, selection.Status)
	assert.Equal(t, "Something else", selection.Value)
}

func TestTerminalUIInputBrokerReturnsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	broker := interactiveTerminalBroker("answer\n", &bytes.Buffer{})

	_, err := broker.Input(ctx, UIInputRequest{Title: "Question"})

	require.ErrorIs(t, err, context.Canceled)
}

func interactiveTerminalBroker(input string, out *bytes.Buffer) *TerminalUIInputBroker {
	broker := NewTerminalUIInputBroker(strings.NewReader(input), out)
	broker.Interactive = true
	return broker
}
