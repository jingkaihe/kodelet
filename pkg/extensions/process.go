package extensions

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/jingkaihe/kodelet/pkg/osutil"
	"github.com/pkg/errors"
)

// Process is a running extension subprocess.
type Process struct {
	Extension Extension
	cmd       *exec.Cmd
	client    *rpcClient
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	timeout   time.Duration
}

// StartProcess starts an extension subprocess and initializes its JSON-RPC client.
func StartProcess(ctx context.Context, ext Extension, timeout time.Duration) (*Process, error) {
	cmd := exec.CommandContext(ctx, ext.ExecPath)
	cmd.Dir = ext.Dir
	cmd.Stderr = os.Stderr
	osutil.SetProcessGroup(cmd)
	osutil.SetProcessGroupKill(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, errors.Wrap(err, "failed to create extension stdin")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, errors.Wrap(err, "failed to create extension stdout")
	}
	if err := cmd.Start(); err != nil {
		return nil, errors.Wrap(err, "failed to start extension process")
	}

	return &Process{
		Extension: ext,
		cmd:       cmd,
		client:    newRPCClient(stdout, stdin, timeout),
		stdin:     stdin,
		stdout:    stdout,
		timeout:   timeout,
	}, nil
}

// Initialize initializes the extension process and returns its registrations.
func (p *Process) Initialize(ctx context.Context, cwd string) (*InitializeResult, error) {
	params := initializeParams{
		ProtocolVersion: protocolVersion,
		Kodelet: map[string]any{
			"version": "dev",
		},
		Extension: initializeExtensionInfo{
			ID:      p.Extension.ID,
			Config:  map[string]any{},
			CWD:     cwd,
			DataDir: "",
		},
		Capabilities: map[string]any{
			"tools":    true,
			"commands": true,
			"events": []string{
				"session.start",
				"resources.discover",
				"user.message",
				"agent.init",
				"agent.start",
				"turn.start",
				"tool.call",
				"tool.result",
				"turn.end",
				"agent.end",
				"session.end",
			},
		},
	}

	var result InitializeResult
	if err := p.client.call(ctx, "extension.initialize", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ExecuteTool invokes an extension-provided tool.
func (p *Process) ExecuteTool(ctx context.Context, name string, input json.RawMessage, callContext ExtensionCallContext) (*ToolExecutionResult, error) {
	params := executeToolParams{Name: name, Input: input, Context: callContext}
	var result ToolExecutionResult
	if err := p.client.call(ctx, "extension.tool.execute", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ExecuteCommand invokes an extension-provided command over JSON-RPC.
func (p *Process) ExecuteCommand(ctx context.Context, name string, input map[string]any, invocation CommandInvocation, callContext ExtensionCallContext) (*CommandResult, error) {
	params := executeCommandParams{Name: name, Input: input, Invocation: invocation, Context: callContext}
	var result CommandResult
	if err := p.client.call(ctx, "extension.command.execute", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// HandleEvent invokes an extension event handler.
func (p *Process) HandleEvent(ctx context.Context, eventID string, eventName string, payload any, callContext ExtensionCallContext) (*EventResult, error) {
	params := eventParams{ID: eventID, Event: eventName, Context: callContext, Payload: payload}
	var result EventResult
	if err := p.client.call(ctx, "extension.event.handle", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Close terminates the extension process.
func (p *Process) Close() error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	_ = p.stdin.Close()
	_ = p.stdout.Close()
	_ = p.cmd.Process.Kill()
	_, err := p.cmd.Process.Wait()
	return err
}
