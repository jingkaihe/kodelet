// Package mcp provides Model Context Protocol integration for kodelet.
package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/mcp/codegen"
	"github.com/jingkaihe/kodelet/pkg/mcp/rpc"
	"github.com/jingkaihe/kodelet/pkg/mcp/runtime"
	"github.com/jingkaihe/kodelet/pkg/tools"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
)

// ExecutionSetup contains the result of setting up MCP code execution mode
type ExecutionSetup struct {
	RPCServer *rpc.MCPRPCServer
	StateOpts []tools.BasicStateOption
}

// GetSocketPath returns the absolute path to the MCP socket file for a given session.
// Each session gets its own socket to prevent conflicts when multiple kodelet instances
// run concurrently.
func GetSocketPath(sessionID string) (string, error) {
	workspaceDir := viper.GetString("mcp.code_execution.workspace_dir")
	if workspaceDir == "" {
		workspaceDir = ".kodelet/mcp"
	}
	socketPath := filepath.Join(workspaceDir, fmt.Sprintf("mcp-%s.sock", sessionID))
	return filepath.Abs(socketPath)
}

// fileExists checks if a file exists
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ErrDirectMode is returned when MCP is configured for direct mode instead of code execution mode
var ErrDirectMode = errors.New("MCP configured for direct mode")

// SetupExecutionMode sets up MCP code execution mode and returns the necessary components.
// Returns ErrDirectMode if execution mode is not "code" or mcpManager is nil.
// The sessionID is used to create a unique socket path for this session, preventing conflicts
// when multiple kodelet instances run concurrently.
func SetupExecutionMode(ctx context.Context, mcpManager *tools.MCPManager, sessionID string) (*ExecutionSetup, error) {
	executionMode := viper.GetString("mcp.execution_mode")
	if executionMode != "code" {
		return nil, ErrDirectMode
	}

	workspaceDir := viper.GetString("mcp.code_execution.workspace_dir")
	if workspaceDir == "" {
		workspaceDir = ".kodelet/mcp"
	}

	// Generate MCP tool files if needed
	regenerateOnStartup := viper.GetBool("mcp.code_execution.regenerate_on_startup")
	clientTSPath := filepath.Join(workspaceDir, "client.ts")

	if regenerateOnStartup || !fileExists(clientTSPath) {
		logger.G(ctx).Info("Generating MCP tool TypeScript API...")
		generator := codegen.NewMCPCodeGenerator(mcpManager, workspaceDir)
		if err := generator.Generate(ctx); err != nil {
			return nil, errors.Wrap(err, "failed to generate MCP tool code")
		}
	}

	// Get socket path for this session
	socketPath, err := GetSocketPath(sessionID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to resolve socket path")
	}

	// Create RPC server
	rpcServer, err := rpc.NewMCPRPCServer(mcpManager, socketPath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create MCP RPC server")
	}

	// Start RPC server in background
	go func() {
		if err := rpcServer.Start(ctx); err != nil {
			logger.G(ctx).WithError(err).Error("MCP RPC server failed")
		}
	}()

	// Check if Node.js is available
	if err := runtime.CheckAvailability(ctx); err != nil {
		return nil, errors.Wrap(err, "mcp runtime is not available")
	}

	// Create Node runtime and code_execution tool
	nodeRuntime := runtime.NewNodeRuntime(workspaceDir, socketPath)
	codeExecTool := tools.NewCodeExecutionTool(nodeRuntime)

	// Add code_execution tool instead of MCP tools
	stateOpts := []tools.BasicStateOption{tools.WithExtraMCPTools([]tooltypes.Tool{codeExecTool})}
	logger.G(ctx).Debug("MCP code execution mode enabled")

	return &ExecutionSetup{
		RPCServer: rpcServer,
		StateOpts: stateOpts,
	}, nil
}
