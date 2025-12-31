package client

import (
	"context"
	"encoding/json"

	"github.com/pkg/errors"
)

// TerminalExecuteRequest parameters for terminal/execute
type TerminalExecuteRequest struct {
	Command string `json:"command"`
	CWD     string `json:"cwd,omitempty"`
}

// TerminalExecuteResponse from terminal/execute
type TerminalExecuteResponse struct {
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

// TerminalExecute executes a command in the client's terminal.
// This delegates command execution to the client, which may show the
// command in its UI or apply additional security restrictions.
func (c *Client) TerminalExecute(ctx context.Context, command, cwd string) (*TerminalExecuteResponse, error) {
	if !c.HasTerminalCapability() {
		return nil, errors.New("client does not support terminal execution")
	}

	params := TerminalExecuteRequest{
		Command: command,
		CWD:     cwd,
	}

	result, err := c.caller.CallClient(ctx, "terminal/execute", params)
	if err != nil {
		return nil, errors.Wrap(err, "failed to execute command in terminal")
	}

	var resp TerminalExecuteResponse
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, errors.Wrap(err, "failed to parse terminal response")
	}

	return &resp, nil
}

// TerminalShowRequest parameters for terminal/show
type TerminalShowRequest struct {
	Text string `json:"text"`
}

// TerminalShow displays text in the client's terminal without executing it.
// This is useful for showing commands that should be run manually by the user.
func (c *Client) TerminalShow(ctx context.Context, text string) error {
	if !c.HasTerminalCapability() {
		return errors.New("client does not support terminal")
	}

	params := TerminalShowRequest{Text: text}

	_, err := c.caller.CallClient(ctx, "terminal/show", params)
	if err != nil {
		return errors.Wrap(err, "failed to show text in terminal")
	}

	return nil
}
