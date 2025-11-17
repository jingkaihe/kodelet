// Package rpc provides an RPC server for code execution to call MCP tools.
package rpc

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/pkg/errors"
)

// MCPRPCServer provides an RPC endpoint for code execution to call MCP tools
type MCPRPCServer struct {
	mcpManager *tools.MCPManager
	listener   net.Listener
	server     *http.Server
	socketPath string
}

// MCPRPCRequest represents a request to call an MCP tool
type MCPRPCRequest struct {
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments"`
}

// MCPRPCResponse represents the response from an MCP tool call
type MCPRPCResponse struct {
	Content           []map[string]any `json:"content"`
	StructuredContent any              `json:"structuredContent,omitempty"`
	IsError           bool             `json:"isError"`
}

// NewMCPRPCServer creates a new MCP RPC server
func NewMCPRPCServer(mcpManager *tools.MCPManager, socketPath string) (*MCPRPCServer, error) {
	// Remove existing socket file if it exists
	if err := os.RemoveAll(socketPath); err != nil {
		return nil, errors.Wrap(err, "failed to remove existing socket")
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create unix socket listener")
	}

	s := &MCPRPCServer{
		mcpManager: mcpManager,
		listener:   listener,
		socketPath: socketPath,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleMCPCall)

	s.server = &http.Server{
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	return s, nil
}

// handleMCPCall handles an RPC call to an MCP tool
func (s *MCPRPCServer) handleMCPCall(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req MCPRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logger.G(ctx).WithError(err).Error("failed to decode RPC request")
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	logger.G(ctx).WithField("tool", req.Tool).Debug("handling MCP RPC call")

	// Find the tool
	mcpTools, err := s.mcpManager.ListMCPTools(ctx)
	if err != nil {
		logger.G(ctx).WithError(err).Error("failed to list MCP tools")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var targetTool *tools.MCPTool
	for i, tool := range mcpTools {
		if tool.MCPToolName() == req.Tool {
			targetTool = &mcpTools[i]
			break
		}
	}

	if targetTool == nil {
		logger.G(ctx).WithField("tool", req.Tool).Error("tool not found")
		http.Error(w, "tool not found", http.StatusNotFound)
		return
	}

	// Execute the tool
	response, err := targetTool.CallMCPServer(ctx, req.Arguments)
	if err != nil {
		logger.G(ctx).WithError(err).Error("failed to call MCP tool")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.G(ctx).WithError(err).Error("failed to encode response")
	}

	logger.G(ctx).WithField("tool", req.Tool).WithField("error", response.IsError).Debug("MCP RPC call completed")
}

// Start starts the RPC server
func (s *MCPRPCServer) Start(ctx context.Context) error {
	logger.G(ctx).WithField("socket", s.socketPath).Info("Starting MCP RPC server")
	if err := s.server.Serve(s.listener); err != nil && err != http.ErrServerClosed {
		return errors.Wrap(err, "RPC server failed")
	}
	return nil
}

// Shutdown gracefully shuts down the RPC server
func (s *MCPRPCServer) Shutdown(ctx context.Context) error {
	logger.G(ctx).Info("Shutting down MCP RPC server")
	if err := s.server.Shutdown(ctx); err != nil {
		return errors.Wrap(err, "failed to shutdown RPC server")
	}
	// Clean up socket file
	if err := os.RemoveAll(s.socketPath); err != nil {
		logger.G(ctx).WithError(err).Warn("failed to remove socket file")
	}
	return nil
}

// SocketPath returns the path to the Unix socket
func (s *MCPRPCServer) SocketPath() string {
	return s.socketPath
}
