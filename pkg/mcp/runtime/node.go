// Package runtime provides code execution runtime implementations.
package runtime

import (
	"context"
	"os/exec"
	"strings"

	"github.com/pkg/errors"
)

// NodeRuntime provides TypeScript/JavaScript execution via Node.js with tsx
type NodeRuntime struct {
	workspaceDir string
	socketPath   string
}

// NewNodeRuntime creates a new Node.js runtime
func NewNodeRuntime(workspaceDir string, socketPath string) *NodeRuntime {
	return &NodeRuntime{
		workspaceDir: workspaceDir,
		socketPath:   socketPath,
	}
}

// Execute runs TypeScript code using tsx
func (n *NodeRuntime) Execute(ctx context.Context, code string) (string, error) {
	// Use tsx to execute TypeScript code from stdin
	// npx tsx will auto-install if needed
	cmd := exec.CommandContext(ctx, "npx", "tsx", "-")
	cmd.Dir = n.workspaceDir
	cmd.Stdin = strings.NewReader(code)

	// Set environment variable for MCP RPC socket
	cmd.Env = append(cmd.Env, "MCP_RPC_SOCKET="+n.socketPath)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), errors.Wrap(err, "code execution failed")
	}

	return string(output), nil
}

// Name returns the name of the runtime
func (n *NodeRuntime) Name() string {
	return "node-tsx"
}

// CheckAvailability checks if Node.js/npx is available
func CheckAvailability(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "npx", "--version")
	if err := cmd.Run(); err != nil {
		return errors.New("Node.js/npx is not available. Please install Node.js to use code execution mode")
	}
	return nil
}
