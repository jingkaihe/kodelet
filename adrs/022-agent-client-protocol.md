# ADR 022: Agent Client Protocol (ACP) Integration

## Status
Proposed

## Context

### Background

The **Agent Client Protocol (ACP)** is an emerging standard for communication between AI coding agents and client applications (IDEs, text editors, or other UIs). It is maintained by [Zed Industries](https://zed.dev) and [JetBrains](https://jetbrains.com), enabling seamless integration of AI agents into development workflows.

Kodelet currently operates as a standalone CLI tool with its own interactive modes (`chat`, `run`, `watch`). While effective for terminal-based workflows, this limits integration with modern IDEs that want to embed AI coding assistance directly into their editing experience.

### Problem Statement

1. **IDE Integration Gap**: Kodelet cannot be easily embedded into IDEs like Zed, JetBrains, or VS Code as a coding agent
2. **Fragmented Ecosystem**: Different IDEs require different integration approaches without a standardized protocol
3. **User Experience**: Users must context-switch between IDE and terminal to use Kodelet
4. **Feature Parity**: IDE integrations may miss features available in the native CLI

### Goals

1. Implement ACP agent mode in Kodelet via `kodelet acp` command
2. Enable Kodelet to operate as a subprocess of any ACP-compatible client
3. Expose existing Kodelet capabilities (tools, skills, conversation persistence) through ACP
4. Maintain feature parity with CLI modes where applicable
5. Support session persistence for conversation continuity

### Non-Goals

1. Implementing an ACP client (Kodelet is an agent, not a client)
2. Building IDE-specific integrations beyond the ACP protocol
3. Supporting deprecated SSE transport initially
4. Implementing draft/unstable ACP features (e.g., `session/list`) until they are part of the stable protocol

## Decision

Implement the **Agent Client Protocol** in Kodelet as a new command `kodelet acp` that runs Kodelet in agent mode, communicating over stdio using JSON-RPC 2.0.

### Protocol Overview

ACP uses JSON-RPC 2.0 with two message types:
- **Methods**: Request-response pairs expecting a result or error
- **Notifications**: One-way messages without responses

Communication flows through three phases:
1. **Initialization**: Version negotiation and capability exchange
2. **Session Setup**: Create or resume conversation sessions
3. **Prompt Turn**: User prompts → Agent processing → Streaming updates → Completion

### Transport

Kodelet will implement the **stdio transport**:
- Client launches Kodelet as a subprocess
- Kodelet reads JSON-RPC messages from stdin
- Kodelet writes JSON-RPC messages to stdout
- Messages are newline-delimited (`\n`)
- Logging goes to stderr (optional, configurable)

## Architecture Overview

### High-Level Design

```
┌─────────────────────────────────────────────────────────────────┐
│                        ACP Client (IDE)                         │
│   ┌─────────────┐  ┌─────────────┐  ┌─────────────────────┐    │
│   │   Editor    │  │   UI/UX     │  │  Permission Dialog  │    │
│   └─────────────┘  └─────────────┘  └─────────────────────┘    │
└──────────────────────────┬──────────────────────────────────────┘
                           │ stdio (JSON-RPC 2.0)
                           ▼
┌─────────────────────────────────────────────────────────────────┐
│                    Kodelet ACP Agent                            │
│   ┌─────────────────────────────────────────────────────────┐  │
│   │                    ACP Server                           │  │
│   │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  │  │
│   │  │  Initialize  │  │   Session    │  │   Prompt     │  │  │
│   │  │   Handler    │  │   Manager    │  │   Handler    │  │  │
│   │  └──────────────┘  └──────────────┘  └──────────────┘  │  │
│   └─────────────────────────────────────────────────────────┘  │
│   ┌─────────────────────────────────────────────────────────┐  │
│   │                  Existing Kodelet Core                  │  │
│   │  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌────────┐  │  │
│   │  │   LLM    │  │  Tools   │  │  Skills  │  │Convers-│  │  │
│   │  │  Thread  │  │  System  │  │  System  │  │ations  │  │  │
│   │  └──────────┘  └──────────┘  └──────────┘  └────────┘  │  │
│   └─────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
```

### Package Structure

```
pkg/acp/
├── server.go           # Main ACP server implementation
├── transport.go        # Stdio transport handler
├── handlers/
│   ├── initialize.go   # Initialize method handler
│   ├── authenticate.go # Authentication handler
│   ├── session.go      # Session management (new, load, cancel)
│   ├── prompt.go       # Prompt turn handler
│   └── mode.go         # Session mode handler
├── client/
│   ├── interface.go    # Client interface for agent→client calls
│   ├── permission.go   # Permission request handling
│   ├── filesystem.go   # fs/read_text_file, fs/write_text_file
│   └── terminal.go     # Terminal operations
├── types/
│   ├── messages.go     # JSON-RPC message types
│   ├── session.go      # Session types
│   ├── content.go      # Content block types
│   └── capabilities.go # Capability definitions
├── session/
│   ├── manager.go      # Session lifecycle management
│   └── state.go        # Per-session state
└── bridge/
    ├── tools.go        # Bridge kodelet tools to ACP tool calls
    └── updates.go      # Convert kodelet events to session/update
```

### Core Types

```go
// pkg/acp/types/messages.go
package types

// ProtocolVersion is the ACP protocol version (major only)
const ProtocolVersion = 1

// JSON-RPC 2.0 base types
type Request struct {
    JSONRPC string          `json:"jsonrpc"`
    ID      *RequestID      `json:"id,omitempty"`
    Method  string          `json:"method"`
    Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
    JSONRPC string          `json:"jsonrpc"`
    ID      *RequestID      `json:"id"`
    Result  json.RawMessage `json:"result,omitempty"`
    Error   *Error          `json:"error,omitempty"`
}

type Notification struct {
    JSONRPC string          `json:"jsonrpc"`
    Method  string          `json:"method"`
    Params  json.RawMessage `json:"params,omitempty"`
}

type Error struct {
    Code    int             `json:"code"`
    Message string          `json:"message"`
    Data    json.RawMessage `json:"data,omitempty"`
}

// RequestID can be string or integer
type RequestID struct {
    String *string
    Int    *int64
}
```

```go
// pkg/acp/types/capabilities.go
package types

// AgentCapabilities advertised during initialization
type AgentCapabilities struct {
    LoadSession       bool                `json:"loadSession,omitempty"`
    PromptCapabilities *PromptCapabilities `json:"promptCapabilities,omitempty"`
    MCPCapabilities   *MCPCapabilities    `json:"mcpCapabilities,omitempty"`
    SessionCapabilities *SessionCapabilities `json:"sessionCapabilities,omitempty"`
    Meta              map[string]any      `json:"_meta,omitempty"`
}

type PromptCapabilities struct {
    Image           bool `json:"image,omitempty"`
    Audio           bool `json:"audio,omitempty"`
    EmbeddedContext bool `json:"embeddedContext,omitempty"`
}

type MCPCapabilities struct {
    HTTP bool `json:"http,omitempty"`
    SSE  bool `json:"sse,omitempty"`
}

type SessionCapabilities struct {
    SetMode bool `json:"setMode,omitempty"`
}

// ClientCapabilities received during initialization
type ClientCapabilities struct {
    FS       *FSCapabilities `json:"fs,omitempty"`
    Terminal bool            `json:"terminal,omitempty"`
    Meta     map[string]any  `json:"_meta,omitempty"`
}

type FSCapabilities struct {
    ReadTextFile  bool `json:"readTextFile,omitempty"`
    WriteTextFile bool `json:"writeTextFile,omitempty"`
}
```

```go
// pkg/acp/types/session.go
package types

type SessionID string

type NewSessionRequest struct {
    CWD        string      `json:"cwd"`
    MCPServers []MCPServer `json:"mcpServers"`
    Meta       map[string]any `json:"_meta,omitempty"`
}

type NewSessionResponse struct {
    SessionID SessionID        `json:"sessionId"`
    Modes     *SessionModeState `json:"modes,omitempty"`
    Meta      map[string]any    `json:"_meta,omitempty"`
}

type PromptRequest struct {
    SessionID SessionID      `json:"sessionId"`
    Prompt    []ContentBlock `json:"prompt"`
    Meta      map[string]any `json:"_meta,omitempty"`
}

type PromptResponse struct {
    StopReason StopReason     `json:"stopReason"`
    Meta       map[string]any `json:"_meta,omitempty"`
}

type StopReason string

const (
    StopReasonEndTurn         StopReason = "end_turn"
    StopReasonMaxTokens       StopReason = "max_tokens"
    StopReasonMaxTurnRequests StopReason = "max_turn_requests"
    StopReasonRefusal         StopReason = "refusal"
    StopReasonCancelled       StopReason = "cancelled"
)
```

```go
// pkg/acp/types/content.go
package types

// ContentBlock represents different content types in prompts and responses
type ContentBlock struct {
    Type        string          `json:"type"`
    Text        string          `json:"text,omitempty"`
    Data        string          `json:"data,omitempty"`       // Base64 for image/audio
    MimeType    string          `json:"mimeType,omitempty"`
    URI         string          `json:"uri,omitempty"`
    Name        string          `json:"name,omitempty"`
    Resource    *EmbeddedResource `json:"resource,omitempty"`
    Annotations *Annotations    `json:"annotations,omitempty"`
    Meta        map[string]any  `json:"_meta,omitempty"`
}

// ContentBlockType constants
const (
    ContentTypeText         = "text"
    ContentTypeImage        = "image"
    ContentTypeAudio        = "audio"
    ContentTypeResource     = "resource"
    ContentTypeResourceLink = "resource_link"
)

// SessionUpdate represents updates sent to the client
type SessionUpdate struct {
    SessionUpdate string          `json:"sessionUpdate"`
    // Fields vary by update type - use json.RawMessage or specific types
}

// SessionUpdateType constants
const (
    UpdateAgentMessageChunk  = "agent_message_chunk"
    UpdateUserMessageChunk   = "user_message_chunk"
    UpdateThoughtChunk       = "thought_chunk"
    UpdateToolCall           = "tool_call"
    UpdateToolCallUpdate     = "tool_call_update"
    UpdatePlan               = "plan"
    UpdateAvailableCommands  = "available_commands"
    UpdateModeChange         = "mode_change"
)
```

### ACP Server Implementation

```go
// pkg/acp/server.go
package acp

import (
    "bufio"
    "context"
    "encoding/json"
    "io"
    "os"
    "sync"

    "github.com/jingkaihe/kodelet/pkg/acp/handlers"
    "github.com/jingkaihe/kodelet/pkg/acp/session"
    "github.com/jingkaihe/kodelet/pkg/acp/types"
    "github.com/jingkaihe/kodelet/pkg/logger"
    "github.com/pkg/errors"
)

// Server implements the ACP agent server
type Server struct {
    input  io.Reader
    output io.Writer
    
    mu             sync.Mutex
    initialized    bool
    clientCaps     *types.ClientCapabilities
    sessionManager *session.Manager
    
    handlers map[string]Handler
    ctx      context.Context
    cancel   context.CancelFunc
}

// Handler processes a specific method
type Handler interface {
    Handle(ctx context.Context, req *types.Request) (*types.Response, error)
}

// NewServer creates a new ACP server
func NewServer(opts ...Option) *Server {
    ctx, cancel := context.WithCancel(context.Background())
    
    s := &Server{
        input:    os.Stdin,
        output:   os.Stdout,
        handlers: make(map[string]Handler),
        ctx:      ctx,
        cancel:   cancel,
    }
    
    for _, opt := range opts {
        opt(s)
    }
    
    s.registerHandlers()
    return s
}

func (s *Server) registerHandlers() {
    s.handlers["initialize"] = handlers.NewInitializeHandler(s)
    s.handlers["authenticate"] = handlers.NewAuthenticateHandler(s)
    s.handlers["session/new"] = handlers.NewSessionHandler(s)
    s.handlers["session/load"] = handlers.NewLoadSessionHandler(s)
    s.handlers["session/prompt"] = handlers.NewPromptHandler(s)
    s.handlers["session/set_mode"] = handlers.NewSetModeHandler(s)
    // session/cancel is a notification, handled separately
}

// Run starts the server event loop
func (s *Server) Run() error {
    scanner := bufio.NewScanner(s.input)
    scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024) // 10MB max message
    
    for scanner.Scan() {
        select {
        case <-s.ctx.Done():
            return s.ctx.Err()
        default:
        }
        
        line := scanner.Bytes()
        if len(line) == 0 {
            continue
        }
        
        if err := s.handleMessage(line); err != nil {
            logger.G(s.ctx).WithError(err).Error("Failed to handle message")
        }
    }
    
    return scanner.Err()
}

func (s *Server) handleMessage(data []byte) error {
    // Determine if request or notification
    var msg struct {
        ID     *json.RawMessage `json:"id"`
        Method string           `json:"method"`
    }
    
    if err := json.Unmarshal(data, &msg); err != nil {
        return s.sendError(nil, -32700, "Parse error", nil)
    }
    
    // Notification (no ID)
    if msg.ID == nil {
        return s.handleNotification(msg.Method, data)
    }
    
    // Request (has ID)
    return s.handleRequest(data)
}

func (s *Server) handleRequest(data []byte) error {
    var req types.Request
    if err := json.Unmarshal(data, &req); err != nil {
        return s.sendError(nil, -32700, "Parse error", nil)
    }
    
    handler, ok := s.handlers[req.Method]
    if !ok {
        return s.sendError(req.ID, -32601, "Method not found", nil)
    }
    
    resp, err := handler.Handle(s.ctx, &req)
    if err != nil {
        return s.sendError(req.ID, -32603, err.Error(), nil)
    }
    
    return s.send(resp)
}

func (s *Server) handleNotification(method string, data []byte) error {
    switch method {
    case "session/cancel":
        var notif struct {
            Params struct {
                SessionID types.SessionID `json:"sessionId"`
            } `json:"params"`
        }
        if err := json.Unmarshal(data, &notif); err != nil {
            return err
        }
        return s.sessionManager.Cancel(notif.Params.SessionID)
    default:
        logger.G(s.ctx).WithField("method", method).Warn("Unknown notification")
        return nil
    }
}

// SendUpdate sends a session/update notification to the client
func (s *Server) SendUpdate(sessionID types.SessionID, update types.SessionUpdate) error {
    notif := types.Notification{
        JSONRPC: "2.0",
        Method:  "session/update",
        Params:  mustMarshal(map[string]any{
            "sessionId": sessionID,
            "update":    update,
        }),
    }
    return s.send(notif)
}

// CallClient makes a request to the client (for permission, fs, terminal)
func (s *Server) CallClient(ctx context.Context, method string, params any) (json.RawMessage, error) {
    // Implementation for client method calls
    // ...
}

func (s *Server) send(v any) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    data, err := json.Marshal(v)
    if err != nil {
        return errors.Wrap(err, "failed to marshal response")
    }
    
    _, err = s.output.Write(append(data, '\n'))
    return err
}

func (s *Server) sendError(id *types.RequestID, code int, message string, data any) error {
    resp := types.Response{
        JSONRPC: "2.0",
        ID:      id,
        Error: &types.Error{
            Code:    code,
            Message: message,
        },
    }
    if data != nil {
        resp.Error.Data = mustMarshal(data)
    }
    return s.send(resp)
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown() {
    s.cancel()
}
```

### Initialize Handler

```go
// pkg/acp/handlers/initialize.go
package handlers

import (
    "context"
    "encoding/json"

    "github.com/jingkaihe/kodelet/pkg/acp/types"
    "github.com/jingkaihe/kodelet/pkg/version"
)

type InitializeHandler struct {
    server ServerInterface
}

func NewInitializeHandler(s ServerInterface) *InitializeHandler {
    return &InitializeHandler{server: s}
}

func (h *InitializeHandler) Handle(ctx context.Context, req *types.Request) (*types.Response, error) {
    var params types.InitializeRequest
    if err := json.Unmarshal(req.Params, &params); err != nil {
        return nil, err
    }
    
    // Store client capabilities
    h.server.SetClientCapabilities(params.ClientCapabilities)
    
    // Negotiate protocol version
    protocolVersion := types.ProtocolVersion
    if params.ProtocolVersion < protocolVersion {
        protocolVersion = params.ProtocolVersion
    }
    
    // Build agent capabilities
    agentCaps := types.AgentCapabilities{
        LoadSession: true, // Support session persistence
        PromptCapabilities: &types.PromptCapabilities{
            Image:           true,  // Kodelet supports images
            Audio:           false, // Not yet supported
            EmbeddedContext: true,  // Support embedded resources
        },
        MCPCapabilities: &types.MCPCapabilities{
            HTTP: true,  // Support HTTP MCP servers
            SSE:  false, // Deprecated
        },
        SessionCapabilities: &types.SessionCapabilities{
            SetMode: false, // Initially not supporting modes
        },
    }
    
    result := types.InitializeResponse{
        ProtocolVersion:   protocolVersion,
        AgentCapabilities: agentCaps,
        AgentInfo: &types.Implementation{
            Name:    "kodelet",
            Title:   "Kodelet",
            Version: version.Version,
        },
        AuthMethods: []types.AuthMethod{}, // No auth required
    }
    
    h.server.SetInitialized(true)
    
    return &types.Response{
        JSONRPC: "2.0",
        ID:      req.ID,
        Result:  mustMarshal(result),
    }, nil
}
```

### Prompt Handler

```go
// pkg/acp/handlers/prompt.go
package handlers

import (
    "context"
    "encoding/json"

    "github.com/jingkaihe/kodelet/pkg/acp/bridge"
    "github.com/jingkaihe/kodelet/pkg/acp/types"
    "github.com/jingkaihe/kodelet/pkg/llm"
    llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

type PromptHandler struct {
    server ServerInterface
}

func NewPromptHandler(s ServerInterface) *PromptHandler {
    return &PromptHandler{server: s}
}

func (h *PromptHandler) Handle(ctx context.Context, req *types.Request) (*types.Response, error) {
    var params types.PromptRequest
    if err := json.Unmarshal(req.Params, &params); err != nil {
        return nil, err
    }
    
    session, err := h.server.GetSession(params.SessionID)
    if err != nil {
        return nil, err
    }
    
    // Convert ACP content blocks to kodelet message format
    userMessage := bridge.ContentBlocksToMessage(params.Prompt)
    
    // Create streaming handler that sends session/update notifications
    streamHandler := &ACPStreamHandler{
        server:    h.server,
        sessionID: params.SessionID,
    }
    
    // Execute the prompt using kodelet's LLM thread
    stopReason, err := session.Thread.Run(ctx, userMessage, streamHandler)
    if err != nil {
        // Check if cancelled
        if session.IsCancelled() {
            return makePromptResponse(req.ID, types.StopReasonCancelled), nil
        }
        return nil, err
    }
    
    return makePromptResponse(req.ID, bridge.ToACPStopReason(stopReason)), nil
}

// ACPStreamHandler implements llm.Handler to bridge kodelet streaming to ACP updates
type ACPStreamHandler struct {
    server    ServerInterface
    sessionID types.SessionID
}

func (h *ACPStreamHandler) OnTextDelta(text string) error {
    return h.server.SendUpdate(h.sessionID, types.SessionUpdate{
        SessionUpdate: types.UpdateAgentMessageChunk,
        Content: types.ContentBlock{
            Type: types.ContentTypeText,
            Text: text,
        },
    })
}

func (h *ACPStreamHandler) OnToolUse(toolID, toolName string, input json.RawMessage) error {
    return h.server.SendUpdate(h.sessionID, types.SessionUpdate{
        SessionUpdate: types.UpdateToolCall,
        ToolCallID:    toolID,
        Title:         toolName,
        Kind:          bridge.ToACPToolKind(toolName),
        Status:        types.ToolStatusPending,
        RawInput:      input,
    })
}

func (h *ACPStreamHandler) OnToolResult(toolID string, result any, err error) error {
    status := types.ToolStatusCompleted
    if err != nil {
        status = types.ToolStatusFailed
    }
    
    return h.server.SendUpdate(h.sessionID, types.SessionUpdate{
        SessionUpdate: types.UpdateToolCallUpdate,
        ToolCallID:    toolID,
        Status:        status,
        Content:       bridge.ToACPToolContent(result),
    })
}

func (h *ACPStreamHandler) OnThinking(text string) error {
    return h.server.SendUpdate(h.sessionID, types.SessionUpdate{
        SessionUpdate: types.UpdateThoughtChunk,
        Content: types.ContentBlock{
            Type: types.ContentTypeText,
            Text: text,
        },
    })
}
```

### Client Interface for Agent→Client Calls

```go
// pkg/acp/client/interface.go
package client

import (
    "context"

    "github.com/jingkaihe/kodelet/pkg/acp/types"
)

// Client interface for making calls from agent to client
type Client interface {
    // RequestPermission requests user permission for a tool call
    RequestPermission(ctx context.Context, sessionID types.SessionID, toolCall types.ToolCall, options []types.PermissionOption) (*types.PermissionOutcome, error)
    
    // ReadTextFile reads a file via the client (if capability available)
    ReadTextFile(ctx context.Context, sessionID types.SessionID, path string, line, limit *int) (string, error)
    
    // WriteTextFile writes a file via the client (if capability available)
    WriteTextFile(ctx context.Context, sessionID types.SessionID, path, content string) error
    
    // CreateTerminal creates a terminal for command execution
    CreateTerminal(ctx context.Context, sessionID types.SessionID, command string, args []string, cwd string, env map[string]string) (string, error)
    
    // TerminalOutput gets terminal output
    TerminalOutput(ctx context.Context, sessionID types.SessionID, terminalID string) (*types.TerminalOutput, error)
    
    // WaitForTerminalExit waits for terminal to complete
    WaitForTerminalExit(ctx context.Context, sessionID types.SessionID, terminalID string) (*types.TerminalExitStatus, error)
    
    // KillTerminal kills a running terminal
    KillTerminal(ctx context.Context, sessionID types.SessionID, terminalID string) error
    
    // ReleaseTerminal releases terminal resources
    ReleaseTerminal(ctx context.Context, sessionID types.SessionID, terminalID string) error
}
```

### Bridge: Kodelet Tools to ACP

```go
// pkg/acp/bridge/tools.go
package bridge

import (
    "github.com/jingkaihe/kodelet/pkg/acp/types"
    tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// ToACPToolKind maps kodelet tool names to ACP tool kinds
func ToACPToolKind(toolName string) types.ToolKind {
    switch toolName {
    case "file_read", "grep_tool", "glob_tool":
        return types.ToolKindRead
    case "file_write", "file_edit":
        return types.ToolKindEdit
    case "bash":
        return types.ToolKindExecute
    case "web_fetch":
        return types.ToolKindFetch
    case "thinking":
        return types.ToolKindThink
    case "subagent":
        return types.ToolKindSearch
    default:
        return types.ToolKindOther
    }
}

// ToACPToolContent converts kodelet tool result to ACP content blocks
func ToACPToolContent(result tooltypes.ToolResult) []types.ToolCallContent {
    if result.IsError() {
        return []types.ToolCallContent{{
            Type: "content",
            Content: types.ContentBlock{
                Type: types.ContentTypeText,
                Text: result.GetError(),
            },
        }}
    }
    
    structured := result.StructuredData()
    
    // Handle file edits as diffs
    if editMeta, ok := structured.Metadata.(*tooltypes.FileEditMetadata); ok {
        return []types.ToolCallContent{{
            Type:    "diff",
            Path:    editMeta.FilePath,
            OldText: editMeta.OldText,
            NewText: editMeta.NewText,
        }}
    }
    
    // Default: text content
    return []types.ToolCallContent{{
        Type: "content",
        Content: types.ContentBlock{
            Type: types.ContentTypeText,
            Text: result.GetResult(),
        },
    }}
}

// ToACPStopReason converts kodelet stop reasons to ACP
func ToACPStopReason(reason string) types.StopReason {
    switch reason {
    case "end_turn", "stop":
        return types.StopReasonEndTurn
    case "max_tokens":
        return types.StopReasonMaxTokens
    case "cancelled":
        return types.StopReasonCancelled
    default:
        return types.StopReasonEndTurn
    }
}

// ContentBlocksToMessage converts ACP content blocks to kodelet message
func ContentBlocksToMessage(blocks []types.ContentBlock) string {
    // Implementation to extract text and handle embedded resources
    // ...
}
```

### CLI Command

```go
// cmd/kodelet/acp.go
package main

import (
    "github.com/jingkaihe/kodelet/pkg/acp"
    "github.com/spf13/cobra"
)

var acpCmd = &cobra.Command{
    Use:   "acp",
    Short: "Run kodelet as an ACP agent",
    Long: `Run kodelet as an Agent Client Protocol (ACP) agent.

This mode allows kodelet to be embedded in ACP-compatible clients like
Zed, JetBrains IDEs, or any other ACP client. Communication happens
over stdio using JSON-RPC 2.0.

Example:
  # Launch as subprocess from an IDE
  kodelet acp
  
  # With custom configuration
  kodelet acp --model claude-sonnet-4-5-20250929`,
    RunE: runACP,
}

func init() {
    rootCmd.AddCommand(acpCmd)
    
    acpCmd.Flags().String("model", "", "LLM model to use")
    acpCmd.Flags().Bool("no-skills", false, "Disable agentic skills")
    acpCmd.Flags().Bool("no-hooks", false, "Disable lifecycle hooks")
}

func runACP(cmd *cobra.Command, args []string) error {
    ctx := cmd.Context()
    
    // Load configuration
    config, err := loadConfig(cmd)
    if err != nil {
        return err
    }
    
    // Create ACP server
    server := acp.NewServer(
        acp.WithConfig(config),
        acp.WithContext(ctx),
    )
    
    // Run the server (blocks until stdin closes)
    return server.Run()
}
```

## Implementation Design

### Permission Handling Strategy

ACP allows agents to request user permission before executing tools. Kodelet will implement a configurable permission strategy:

```go
// pkg/acp/permission/strategy.go
package permission

type Strategy interface {
    ShouldRequestPermission(toolName string, input any) bool
    GetPermissionOptions(toolName string) []types.PermissionOption
}

// DefaultStrategy implements reasonable defaults
type DefaultStrategy struct {
    // Tools that always require permission
    alwaysAsk map[string]bool
    // Tools that can use remembered preferences
    preferences map[string]string // tool -> "allow_always" | "reject_always"
}

func (s *DefaultStrategy) ShouldRequestPermission(toolName string, input any) bool {
    // Check remembered preferences
    if pref, ok := s.preferences[toolName]; ok {
        return pref != "allow_always"
    }
    
    // Default: request permission for write operations
    switch toolName {
    case "file_write", "file_edit", "bash":
        return true
    default:
        return false
    }
}
```

### Session Persistence Integration

ACP sessions integrate with kodelet's existing conversation persistence:

```go
// pkg/acp/session/manager.go
package session

import (
    "github.com/jingkaihe/kodelet/pkg/acp/types"
    "github.com/jingkaihe/kodelet/pkg/conversations"
    "github.com/jingkaihe/kodelet/pkg/llm"
)

type Manager struct {
    store    conversations.Store
    sessions map[types.SessionID]*Session
}

type Session struct {
    ID             types.SessionID
    ConversationID string
    Thread         *llm.Thread
    CWD            string
    MCPServers     []types.MCPServer
    cancelled      bool
}

func (m *Manager) NewSession(req types.NewSessionRequest) (*Session, error) {
    // Create new conversation in store
    conv, err := m.store.CreateConversation(req.CWD)
    if err != nil {
        return nil, err
    }
    
    // Create LLM thread
    thread, err := llm.NewThread(/* config */)
    if err != nil {
        return nil, err
    }
    
    session := &Session{
        ID:             types.SessionID(conv.ID),
        ConversationID: conv.ID,
        Thread:         thread,
        CWD:            req.CWD,
        MCPServers:     req.MCPServers,
    }
    
    m.sessions[session.ID] = session
    return session, nil
}

func (m *Manager) LoadSession(req types.LoadSessionRequest) (*Session, error) {
    // Load existing conversation
    conv, err := m.store.GetConversation(string(req.SessionID))
    if err != nil {
        return nil, err
    }
    
    // Reconstruct thread with history
    thread, err := llm.NewThreadFromConversation(conv)
    if err != nil {
        return nil, err
    }
    
    session := &Session{
        ID:             req.SessionID,
        ConversationID: conv.ID,
        Thread:         thread,
        CWD:            req.CWD,
        MCPServers:     req.MCPServers,
    }
    
    m.sessions[session.ID] = session
    return session, nil
}
```

### MCP Server Integration

When clients provide MCP servers in session setup, kodelet connects to them:

```go
// pkg/acp/mcp/connector.go
package mcp

import (
    "github.com/jingkaihe/kodelet/pkg/acp/types"
    mcp "github.com/mark3labs/mcp-go/mcp"
)

type Connector struct {
    servers map[string]*mcp.Client
}

func (c *Connector) ConnectServers(servers []types.MCPServer) error {
    for _, server := range servers {
        client, err := c.connect(server)
        if err != nil {
            return err
        }
        c.servers[server.Name] = client
    }
    return nil
}

func (c *Connector) connect(server types.MCPServer) (*mcp.Client, error) {
    switch server.Type {
    case "", "stdio":
        return c.connectStdio(server)
    case "http":
        return c.connectHTTP(server)
    default:
        return nil, errors.Errorf("unsupported transport: %s", server.Type)
    }
}
```

## Implementation Phases

### Phase 1: Core Protocol Infrastructure (Week 1-2)
- [x] Create `pkg/acp/` package structure
- [x] Implement JSON-RPC message types and parsing
- [x] Implement stdio transport with newline-delimited messages
- [x] Create ACP server skeleton with handler registration
- [x] Add `kodelet acp` command

### Phase 2: Initialization & Session Management (Week 2-3)
- [x] Implement `initialize` handler with capability negotiation
- [x] Implement `session/new` handler
- [x] Implement `session/load` handler with conversation replay
- [x] Implement `session/cancel` notification handler
- [x] Integrate with existing conversation persistence

### Phase 3: Prompt Turn Implementation (Week 3-4)
- [x] Implement `session/prompt` handler
- [x] Create bridge from kodelet tools to ACP tool calls
- [x] Implement `session/update` notification streaming
- [x] Handle tool call lifecycle (pending → in_progress → completed)
- [ ] Implement `session/request_permission` for write operations

### Phase 4: Client Capabilities Integration (Week 4-5)
- [ ] Implement client RPC call mechanism
- [ ] Integrate `fs/read_text_file` and `fs/write_text_file` with kodelet tools
- [ ] Implement `terminal/*` methods for bash tool integration
- [ ] Add capability checks before using client features

### Phase 5: MCP Server Support (Week 5-6)
- [x] Implement MCP server connection for stdio transport
- [x] Implement MCP server connection for HTTP transport
- [x] Expose MCP tools through kodelet's tool system
- [x] Handle MCP server lifecycle management

### Phase 6: Testing & Documentation (Week 6-7)
- [x] Write unit tests for all handlers
- [ ] Write integration tests with mock client
- [ ] Create end-to-end tests with sample interactions
- [x] Write documentation for IDE integration
- [ ] Update AGENTS.md and MANUAL.md

## Testing Strategy

### Unit Tests
1. **Message parsing**: JSON-RPC request/response/notification parsing
2. **Handler tests**: Each handler with various inputs
3. **Bridge tests**: Tool result conversion, content block mapping
4. **Session tests**: Session lifecycle, cancellation, persistence

### Integration Tests
```go
// pkg/acp/server_test.go
func TestACPServer_FullConversation(t *testing.T) {
    // Create pipes for stdin/stdout
    clientIn, serverOut := io.Pipe()
    serverIn, clientOut := io.Pipe()
    
    server := acp.NewServer(
        acp.WithInput(serverIn),
        acp.WithOutput(serverOut),
    )
    
    go server.Run()
    
    // Send initialize
    send(clientOut, types.Request{
        JSONRPC: "2.0",
        ID:      intID(0),
        Method:  "initialize",
        Params:  mustMarshal(types.InitializeRequest{
            ProtocolVersion: 1,
            ClientCapabilities: types.ClientCapabilities{
                FS: &types.FSCapabilities{
                    ReadTextFile:  true,
                    WriteTextFile: true,
                },
                Terminal: true,
            },
        }),
    })
    
    // Verify response
    resp := receive(clientIn)
    assert.Equal(t, 1, resp.Result.ProtocolVersion)
    assert.True(t, resp.Result.AgentCapabilities.LoadSession)
    
    // Continue with session/new, session/prompt, etc.
}
```

### Mock Client for E2E Testing
```go
// pkg/acp/testing/mock_client.go
type MockClient struct {
    server     *acp.Server
    received   []types.Notification
    permissions map[string]string
}

func (c *MockClient) HandlePermissionRequest(req types.RequestPermissionRequest) types.RequestPermissionResponse {
    if perm, ok := c.permissions[req.ToolCall.ToolCallID]; ok {
        return types.RequestPermissionResponse{
            Outcome: types.PermissionOutcome{
                Outcome:  "selected",
                OptionID: perm,
            },
        }
    }
    // Default: allow once
    return types.RequestPermissionResponse{
        Outcome: types.PermissionOutcome{
            Outcome:  "selected",
            OptionID: "allow-once",
        },
    }
}
```

## Configuration

### config.yaml additions

```yaml
# ACP-specific configuration
acp:
  # Permission strategy for tool calls
  permissions:
    # Tools that always require explicit permission
    always_ask:
      - bash
      - file_write
      - file_edit
    
    # Tools that can be auto-allowed
    auto_allow:
      - file_read
      - grep_tool
      - glob_tool
  
  # Whether to use client file system when available
  prefer_client_fs: true
  
  # Whether to use client terminal when available
  prefer_client_terminal: true
```

## Documentation

### docs/ACP.md (new file)

```markdown
# Agent Client Protocol (ACP) Integration

## Overview

Kodelet implements the Agent Client Protocol (ACP) to integrate with 
ACP-compatible IDEs like Zed and JetBrains editors.

## Quick Start

To run kodelet as an ACP agent:

```bash
kodelet acp
```

This starts kodelet in agent mode, reading JSON-RPC messages from stdin
and writing responses to stdout.

## IDE Integration

### Zed

Add to your Zed settings:

```json
{
  "agent": {
    "command": "kodelet",
    "args": ["acp"]
  }
}
```

### JetBrains

Configure in Settings → Tools → AI Coding Agent:
- Command: `kodelet`
- Arguments: `acp`

## Capabilities

Kodelet advertises the following ACP capabilities:

- **loadSession**: Resume previous conversations
- **promptCapabilities.image**: Support image inputs
- **promptCapabilities.embeddedContext**: Inline file contents
- **mcpCapabilities.http**: Connect to HTTP MCP servers

## Session Persistence

ACP sessions are stored as kodelet conversations and can be resumed.
The session ID corresponds to the conversation ID.

## Tools

All kodelet tools are exposed through ACP tool calls:
- file_read, file_write, file_edit
- bash, grep_tool, glob_tool
- web_fetch, image_recognition
- subagent, thinking

## MCP Servers

Clients can provide MCP servers during session setup. Kodelet will
connect to these servers and expose their tools alongside built-in tools.
```

## Security Considerations

1. **Permission Model**: Write operations require explicit permission by default
2. **Path Validation**: All file paths are validated to be within the session CWD or allowed directories
3. **Command Restrictions**: Bash tool restrictions apply in ACP mode
4. **MCP Server Trust**: Only connect to MCP servers provided by the trusted client

## Conclusion

The ACP integration extends Kodelet's reach beyond the terminal to modern IDEs while maintaining feature parity and security. The design:

1. **Follows ACP Specification**: Full compliance with protocol version 1
2. **Leverages Existing Infrastructure**: Reuses LLM thread, tools, skills, and conversation persistence
3. **Maintains Security**: Permission model and existing restrictions carry over
4. **Enables Future Growth**: Session modes, custom capabilities via `_meta`

The implementation provides a clean bridge between Kodelet's powerful CLI capabilities and the IDE-integrated development experience that modern developers expect.

## References

- [Agent Client Protocol Specification](https://agentclientprotocol.com)
- [ACP GitHub Repository](https://github.com/agentclientprotocol/agent-client-protocol)
- [JSON-RPC 2.0 Specification](https://www.jsonrpc.org/specification)
- [Model Context Protocol](https://modelcontextprotocol.io)
