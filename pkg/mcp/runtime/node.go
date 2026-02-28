// Package runtime provides code execution runtime implementations.
package runtime

import (
	"context"
	"os"
	"os/exec"

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

// Execute runs a TypeScript/JavaScript file using tsx.
// codePath can be absolute or relative to workspaceDir.
func (n *NodeRuntime) Execute(ctx context.Context, codePath string) (string, error) {
	// npx tsx will auto-install if needed
	cmd := exec.CommandContext(ctx, "npx", "tsx", codePath)
	cmd.Dir = n.workspaceDir

	// Set environment variable for MCP RPC socket
	cmd.Env = append(os.Environ(), "MCP_RPC_SOCKET="+n.socketPath)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), errors.Wrap(err, "code execution failed")
	}

	return string(output), nil
}

// WorkspaceDir returns the runtime workspace directory.
func (n *NodeRuntime) WorkspaceDir() string {
	return n.workspaceDir
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
