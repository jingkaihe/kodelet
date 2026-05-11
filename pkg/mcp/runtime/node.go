// Package runtime provides code execution runtime implementations.
package runtime

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"github.com/pkg/errors"
)

// NodeRuntime provides TypeScript/JavaScript execution via Node.js with tsx
type NodeRuntime struct {
	workspaceDir string
	rpcTransport string
	socketPath   string
	endpointURL  string
	bearerToken  string
}

// NewNodeRuntime creates a new Node.js runtime
func NewNodeRuntime(workspaceDir string, socketPath string) *NodeRuntime {
	return NewNodeRuntimeWithRPC(workspaceDir, "unix", socketPath, "", "")
}

// NewNodeRuntimeWithRPC creates a new Node.js runtime with explicit MCP RPC configuration.
func NewNodeRuntimeWithRPC(workspaceDir, rpcTransport, socketPath, endpointURL, bearerToken string) *NodeRuntime {
	return &NodeRuntime{
		workspaceDir: workspaceDir,
		rpcTransport: rpcTransport,
		socketPath:   socketPath,
		endpointURL:  endpointURL,
		bearerToken:  bearerToken,
	}
}

// Execute runs a TypeScript/JavaScript file using tsx.
// codePath can be absolute or relative to workspaceDir.
func (n *NodeRuntime) Execute(ctx context.Context, codePath string) (string, error) {
	// npx tsx will auto-install if needed
	cmd := exec.CommandContext(ctx, "npx", "tsx", codePath)
	cmd.Dir = n.workspaceDir

	cmd.Env = n.env()

	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), errors.Wrap(err, "code execution failed")
	}

	return string(output), nil
}

func (n *NodeRuntime) env() []string {
	env := make([]string, 0, len(os.Environ())+4)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "MCP_RPC_") {
			continue
		}
		env = append(env, entry)
	}

	if n.rpcTransport != "" {
		env = append(env, "MCP_RPC_TRANSPORT="+n.rpcTransport)
	}
	if n.socketPath != "" {
		env = append(env, "MCP_RPC_SOCKET="+n.socketPath)
	}
	if n.endpointURL != "" {
		env = append(env, "MCP_RPC_URL="+n.endpointURL)
	}
	if n.bearerToken != "" {
		env = append(env, "MCP_RPC_TOKEN="+n.bearerToken)
	}

	return env
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
