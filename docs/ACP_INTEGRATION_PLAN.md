# ACP Integration Plan for Kodelet

## Executive Summary

This document provides a detailed plan for integrating the Agent Client Protocol (ACP) into kodelet's existing architecture. The integration leverages existing components while adding a new protocol layer for IDE communication.

## Current Architecture Analysis

### Key Components to Integrate With

```
┌─────────────────────────────────────────────────────────────────────────┐
│                        EXISTING KODELET ARCHITECTURE                     │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  cmd/kodelet/run.go         ──┐                                         │
│  cmd/kodelet/chat.go           ├── Entry points (CLI commands)          │
│  cmd/kodelet/serve.go       ──┘                                         │
│                                                                         │
│  pkg/llm/thread.go          ── Thread factory (multi-provider)          │
│  pkg/llm/anthropic/         ──┐                                         │
│  pkg/llm/openai/              ├── LLM provider implementations          │
│  pkg/llm/google/            ──┘                                         │
│                                                                         │
│  pkg/types/llm/thread.go    ── Thread interface                         │
│  pkg/types/llm/handler.go   ── MessageHandler interface                 │
│                                                                         │
│  pkg/tools/state.go         ── Tool state management                    │
│  pkg/tools/*.go             ── Tool implementations                     │
│                                                                         │
│  pkg/conversations/         ── Session persistence                      │
│  pkg/skills/                ── Agentic skills                           │
│  pkg/hooks/                 ── Lifecycle hooks                          │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

### Component Mapping: ACP → Kodelet

| ACP Concept | Kodelet Equivalent | Notes |
|-------------|-------------------|-------|
| Session | `llmtypes.Thread` + `ConversationStore` | Thread manages LLM state, store handles persistence |
| Prompt | `Thread.SendMessage()` | User message to agent |
| session/update | `MessageHandler` callbacks | Text, tool use, tool result, thinking events |
| Tool Call | `tooltypes.Tool.Execute()` | Existing tool system |
| Tool Result | `tooltypes.ToolResult` | Structured results with metadata |
| Content Block | `anthropic.ContentBlockParamUnion` | Provider-specific, needs abstraction |
| Capabilities | `llmtypes.Config` + `tooltypes.State` | Features determined by config |

### Existing Interfaces to Leverage

#### 1. Thread Interface (`pkg/types/llm/thread.go`)

```go
type Thread interface {
    SetState(s tooltypes.State)
    GetState() tooltypes.State
    AddUserMessage(ctx context.Context, message string, imagePaths ...string)
    SendMessage(ctx context.Context, message string, handler MessageHandler, opt MessageOpt) (finalOutput string, err error)
    GetUsage() Usage
    GetConversationID() string
    SetConversationID(id string)
    SaveConversation(ctx context.Context, summarise bool) error
    IsPersisted() bool
    EnablePersistence(ctx context.Context, enabled bool)
    Provider() string
    GetMessages() ([]Message, error)
    GetConfig() Config
    NewSubAgent(ctx context.Context, config Config) Thread
}
```

**ACP Integration:** Use directly - Thread.SendMessage() with a custom ACPMessageHandler

#### 2. MessageHandler Interface (`pkg/types/llm/handler.go`)

```go
type MessageHandler interface {
    HandleText(text string)
    HandleToolUse(toolName string, input string)
    HandleToolResult(toolName string, result string)
    HandleThinking(thinking string)
    HandleDone()
}

type StreamingMessageHandler interface {
    MessageHandler
    HandleTextDelta(delta string)
    HandleThinkingStart()
    HandleThinkingDelta(delta string)
    HandleContentBlockEnd()
}
```

**ACP Integration:** Create `ACPMessageHandler` implementing `StreamingMessageHandler` that sends `session/update` notifications

#### 3. Tool Interface (`pkg/types/tools/types.go`)

```go
type Tool interface {
    GenerateSchema() *jsonschema.Schema
    Name() string
    Description() string
    ValidateInput(state State, parameters string) error
    Execute(ctx context.Context, state State, parameters string) ToolResult
    TracingKVs(parameters string) ([]attribute.KeyValue, error)
}
```

**ACP Integration:** Tools execute normally - bridge their results to ACP tool call updates

---

## Integration Architecture

### New Package Structure

```
pkg/acp/
├── server.go              # Main ACP server (stdio JSON-RPC handler)
├── types.go               # ACP protocol types
├── handlers/
│   ├── initialize.go      # initialize method handler
│   ├── session.go         # session/new, session/load handlers
│   ├── prompt.go          # session/prompt handler
│   └── cancel.go          # session/cancel notification handler
├── bridge/
│   ├── handler.go         # ACPMessageHandler (bridges MessageHandler → ACP updates)
│   ├── content.go         # Content block conversion
│   └── tools.go           # Tool result to ACP tool call conversion
├── client/
│   ├── rpc.go             # Agent→Client RPC calls
│   ├── permission.go      # session/request_permission
│   ├── filesystem.go      # fs/read_text_file, fs/write_text_file
│   └── terminal.go        # terminal/* methods
└── session/
    ├── manager.go         # Session lifecycle management
    └── state.go           # Per-session state (wraps Thread + State)

cmd/kodelet/
└── acp.go                 # New CLI command
```

---

## Detailed Integration Steps

### Phase 1: Protocol Types and Server Skeleton

#### 1.1 Create ACP Types (`pkg/acp/types.go`)

```go
package acp

import "encoding/json"

// Protocol version
const ProtocolVersion = 1

// JSON-RPC types
type Request struct {
    JSONRPC string          `json:"jsonrpc"`
    ID      json.RawMessage `json:"id,omitempty"` // Can be string or int
    Method  string          `json:"method"`
    Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
    JSONRPC string          `json:"jsonrpc"`
    ID      json.RawMessage `json:"id"`
    Result  json.RawMessage `json:"result,omitempty"`
    Error   *RPCError       `json:"error,omitempty"`
}

type Notification struct {
    JSONRPC string          `json:"jsonrpc"`
    Method  string          `json:"method"`
    Params  json.RawMessage `json:"params,omitempty"`
}

type RPCError struct {
    Code    int             `json:"code"`
    Message string          `json:"message"`
    Data    json.RawMessage `json:"data,omitempty"`
}

// Session types
type SessionID string

// Capabilities
type AgentCapabilities struct {
    LoadSession        bool                 `json:"loadSession,omitempty"`
    PromptCapabilities *PromptCapabilities  `json:"promptCapabilities,omitempty"`
    MCPCapabilities    *MCPCapabilities     `json:"mcpCapabilities,omitempty"`
}

type PromptCapabilities struct {
    Image           bool `json:"image,omitempty"`
    Audio           bool `json:"audio,omitempty"`
    EmbeddedContext bool `json:"embeddedContext,omitempty"`
}

type ClientCapabilities struct {
    FS       *FSCapabilities `json:"fs,omitempty"`
    Terminal bool            `json:"terminal,omitempty"`
}

type FSCapabilities struct {
    ReadTextFile  bool `json:"readTextFile,omitempty"`
    WriteTextFile bool `json:"writeTextFile,omitempty"`
}

// Content blocks (aligned with MCP spec)
type ContentBlock struct {
    Type     string `json:"type"` // text, image, audio, resource, resource_link
    Text     string `json:"text,omitempty"`
    Data     string `json:"data,omitempty"`     // base64 for image/audio
    MimeType string `json:"mimeType,omitempty"`
    URI      string `json:"uri,omitempty"`
    Name     string `json:"name,omitempty"`
    // ... other fields
}

// Session update types
type SessionUpdate struct {
    SessionUpdate string `json:"sessionUpdate"` // discriminator
    // Fields depend on type - use json.RawMessage in practice
}

// Tool call types
type ToolCallStatus string
const (
    ToolStatusPending    ToolCallStatus = "pending"
    ToolStatusInProgress ToolCallStatus = "in_progress"
    ToolStatusCompleted  ToolCallStatus = "completed"
    ToolStatusFailed     ToolCallStatus = "failed"
)

type ToolKind string
const (
    ToolKindRead    ToolKind = "read"
    ToolKindEdit    ToolKind = "edit"
    ToolKindExecute ToolKind = "execute"
    ToolKindSearch  ToolKind = "search"
    ToolKindFetch   ToolKind = "fetch"
    ToolKindThink   ToolKind = "think"
    ToolKindOther   ToolKind = "other"
)

type StopReason string
const (
    StopReasonEndTurn   StopReason = "end_turn"
    StopReasonMaxTokens StopReason = "max_tokens"
    StopReasonCancelled StopReason = "cancelled"
    StopReasonRefusal   StopReason = "refusal"
)
```

#### 1.2 Create ACP Server (`pkg/acp/server.go`)

```go
package acp

import (
    "bufio"
    "context"
    "encoding/json"
    "io"
    "os"
    "sync"

    "github.com/jingkaihe/kodelet/pkg/acp/session"
    "github.com/jingkaihe/kodelet/pkg/logger"
    llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
    "github.com/pkg/errors"
)

type Server struct {
    input  io.Reader
    output io.Writer
    
    mu             sync.Mutex
    initialized    bool
    clientCaps     *ClientCapabilities
    sessionManager *session.Manager
    config         llmtypes.Config
    
    ctx    context.Context
    cancel context.CancelFunc
    
    // Pending RPC requests to client (agent→client calls)
    pendingRequests map[string]chan json.RawMessage
    pendingMu       sync.Mutex
    nextRequestID   int64
}

type Option func(*Server)

func WithInput(r io.Reader) Option {
    return func(s *Server) { s.input = r }
}

func WithOutput(w io.Writer) Option {
    return func(s *Server) { s.output = w }
}

func WithConfig(config llmtypes.Config) Option {
    return func(s *Server) { s.config = config }
}

func NewServer(opts ...Option) *Server {
    ctx, cancel := context.WithCancel(context.Background())
    
    s := &Server{
        input:           os.Stdin,
        output:          os.Stdout,
        ctx:             ctx,
        cancel:          cancel,
        pendingRequests: make(map[string]chan json.RawMessage),
    }
    
    for _, opt := range opts {
        opt(s)
    }
    
    s.sessionManager = session.NewManager(s.config)
    return s
}

func (s *Server) Run() error {
    scanner := bufio.NewScanner(s.input)
    // Allow large messages (up to 10MB)
    scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)
    
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
    // First, check if this is a response to a pending agent→client request
    var probe struct {
        ID     json.RawMessage `json:"id"`
        Method string          `json:"method"`
        Result json.RawMessage `json:"result"`
        Error  *RPCError       `json:"error"`
    }
    if err := json.Unmarshal(data, &probe); err != nil {
        return s.sendError(nil, -32700, "Parse error", nil)
    }
    
    // If it has result/error but no method, it's a response to our request
    if probe.Method == "" && (probe.Result != nil || probe.Error != nil) {
        return s.handleResponse(probe.ID, probe.Result, probe.Error)
    }
    
    // Otherwise it's a request or notification from client
    if probe.ID == nil || string(probe.ID) == "null" {
        return s.handleNotification(probe.Method, data)
    }
    
    return s.handleRequest(data)
}

func (s *Server) handleRequest(data []byte) error {
    var req Request
    if err := json.Unmarshal(data, &req); err != nil {
        return s.sendError(nil, -32700, "Parse error", nil)
    }
    
    switch req.Method {
    case "initialize":
        return s.handleInitialize(&req)
    case "authenticate":
        return s.handleAuthenticate(&req)
    case "session/new":
        return s.handleSessionNew(&req)
    case "session/load":
        return s.handleSessionLoad(&req)
    case "session/prompt":
        return s.handleSessionPrompt(&req)
    case "session/set_mode":
        return s.handleSetMode(&req)
    default:
        return s.sendError(req.ID, -32601, "Method not found", nil)
    }
}

func (s *Server) handleNotification(method string, data []byte) error {
    switch method {
    case "session/cancel":
        var params struct {
            SessionID SessionID `json:"sessionId"`
        }
        // Parse and extract params
        var notif Notification
        if err := json.Unmarshal(data, &notif); err != nil {
            return err
        }
        if err := json.Unmarshal(notif.Params, &params); err != nil {
            return err
        }
        return s.sessionManager.Cancel(params.SessionID)
    default:
        logger.G(s.ctx).WithField("method", method).Warn("Unknown notification")
        return nil
    }
}

func (s *Server) handleResponse(id json.RawMessage, result json.RawMessage, err *RPCError) error {
    idStr := string(id)
    
    s.pendingMu.Lock()
    ch, ok := s.pendingRequests[idStr]
    if ok {
        delete(s.pendingRequests, idStr)
    }
    s.pendingMu.Unlock()
    
    if !ok {
        logger.G(s.ctx).WithField("id", idStr).Warn("Response for unknown request")
        return nil
    }
    
    if err != nil {
        // Signal error through channel
        ch <- nil
        return nil
    }
    
    ch <- result
    return nil
}

// SendUpdate sends a session/update notification to the client
func (s *Server) SendUpdate(sessionID SessionID, update interface{}) error {
    params := map[string]interface{}{
        "sessionId": sessionID,
        "update":    update,
    }
    
    return s.sendNotification("session/update", params)
}

// CallClient makes an RPC call to the client and waits for response
func (s *Server) CallClient(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
    s.pendingMu.Lock()
    s.nextRequestID++
    id := s.nextRequestID
    idStr := fmt.Sprintf("%d", id)
    ch := make(chan json.RawMessage, 1)
    s.pendingRequests[idStr] = ch
    s.pendingMu.Unlock()
    
    // Send request
    if err := s.sendRequest(id, method, params); err != nil {
        s.pendingMu.Lock()
        delete(s.pendingRequests, idStr)
        s.pendingMu.Unlock()
        return nil, err
    }
    
    // Wait for response
    select {
    case <-ctx.Done():
        s.pendingMu.Lock()
        delete(s.pendingRequests, idStr)
        s.pendingMu.Unlock()
        return nil, ctx.Err()
    case result := <-ch:
        if result == nil {
            return nil, errors.New("client returned error")
        }
        return result, nil
    }
}

func (s *Server) sendRequest(id int64, method string, params interface{}) error {
    req := map[string]interface{}{
        "jsonrpc": "2.0",
        "id":      id,
        "method":  method,
    }
    if params != nil {
        req["params"] = params
    }
    return s.send(req)
}

func (s *Server) sendNotification(method string, params interface{}) error {
    notif := map[string]interface{}{
        "jsonrpc": "2.0",
        "method":  method,
    }
    if params != nil {
        notif["params"] = params
    }
    return s.send(notif)
}

func (s *Server) sendResult(id json.RawMessage, result interface{}) error {
    resp := map[string]interface{}{
        "jsonrpc": "2.0",
        "id":      id,
        "result":  result,
    }
    return s.send(resp)
}

func (s *Server) sendError(id json.RawMessage, code int, message string, data interface{}) error {
    resp := map[string]interface{}{
        "jsonrpc": "2.0",
        "id":      id,
        "error": map[string]interface{}{
            "code":    code,
            "message": message,
        },
    }
    if data != nil {
        resp["error"].(map[string]interface{})["data"] = data
    }
    return s.send(resp)
}

func (s *Server) send(v interface{}) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    data, err := json.Marshal(v)
    if err != nil {
        return errors.Wrap(err, "failed to marshal message")
    }
    
    _, err = s.output.Write(append(data, '\n'))
    return err
}

func (s *Server) Shutdown() {
    s.cancel()
}
```

### Phase 2: Session Management

#### 2.1 Session Manager (`pkg/acp/session/manager.go`)

**Key Integration Point:** Wraps `llmtypes.Thread` with ACP session semantics

```go
package session

import (
    "context"
    "sync"

    "github.com/jingkaihe/kodelet/pkg/acp"
    "github.com/jingkaihe/kodelet/pkg/conversations"
    "github.com/jingkaihe/kodelet/pkg/llm"
    "github.com/jingkaihe/kodelet/pkg/mcp"
    "github.com/jingkaihe/kodelet/pkg/skills"
    "github.com/jingkaihe/kodelet/pkg/tools"
    llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
    "github.com/pkg/errors"
)

type Session struct {
    ID             acp.SessionID
    Thread         llmtypes.Thread
    State          *tools.BasicState
    CWD            string
    MCPServers     []acp.MCPServer
    cancelFunc     context.CancelFunc
    cancelled      bool
    mu             sync.Mutex
}

func (s *Session) Cancel() {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.cancelled = true
    if s.cancelFunc != nil {
        s.cancelFunc()
    }
}

func (s *Session) IsCancelled() bool {
    s.mu.Lock()
    defer s.mu.Unlock()
    return s.cancelled
}

type Manager struct {
    config   llmtypes.Config
    sessions map[acp.SessionID]*Session
    store    conversations.ConversationStore
    mu       sync.RWMutex
}

func NewManager(config llmtypes.Config) *Manager {
    ctx := context.Background()
    store, _ := conversations.GetConversationStore(ctx)
    
    return &Manager{
        config:   config,
        sessions: make(map[acp.SessionID]*Session),
        store:    store,
    }
}

func (m *Manager) NewSession(ctx context.Context, req acp.NewSessionRequest) (*Session, error) {
    // Create LLM thread using existing factory
    thread, err := llm.NewThread(m.config)
    if err != nil {
        return nil, errors.Wrap(err, "failed to create LLM thread")
    }
    
    // Set up state with tools - reuse run.go pattern
    var stateOpts []tools.BasicStateOption
    stateOpts = append(stateOpts, tools.WithLLMConfig(m.config))
    stateOpts = append(stateOpts, tools.WithMainTools())
    
    // Initialize skills
    discoveredSkills, skillsEnabled := skills.Initialize(ctx, m.config)
    stateOpts = append(stateOpts, tools.WithSkillTool(discoveredSkills, skillsEnabled))
    
    // Handle MCP servers from client
    if len(req.MCPServers) > 0 {
        mcpManager, err := m.connectMCPServers(ctx, req.MCPServers)
        if err != nil {
            // Log but don't fail - MCP is optional
        } else if mcpManager != nil {
            stateOpts = append(stateOpts, tools.WithMCPTools(mcpManager))
        }
    }
    
    state := tools.NewBasicState(ctx, stateOpts...)
    thread.SetState(state)
    
    // Enable persistence (session ID = conversation ID)
    thread.EnablePersistence(ctx, true)
    
    session := &Session{
        ID:         acp.SessionID(thread.GetConversationID()),
        Thread:     thread,
        State:      state,
        CWD:        req.CWD,
        MCPServers: req.MCPServers,
    }
    
    m.mu.Lock()
    m.sessions[session.ID] = session
    m.mu.Unlock()
    
    return session, nil
}

func (m *Manager) LoadSession(ctx context.Context, req acp.LoadSessionRequest) (*Session, error) {
    // Load conversation from store
    record, err := m.store.Load(ctx, string(req.SessionID))
    if err != nil {
        return nil, errors.Wrap(err, "failed to load conversation")
    }
    
    // Create thread and restore state
    thread, err := llm.NewThread(m.config)
    if err != nil {
        return nil, errors.Wrap(err, "failed to create LLM thread")
    }
    
    // Set conversation ID to resume
    thread.SetConversationID(record.ID)
    
    // Set up state
    var stateOpts []tools.BasicStateOption
    stateOpts = append(stateOpts, tools.WithLLMConfig(m.config))
    stateOpts = append(stateOpts, tools.WithMainTools())
    
    discoveredSkills, skillsEnabled := skills.Initialize(ctx, m.config)
    stateOpts = append(stateOpts, tools.WithSkillTool(discoveredSkills, skillsEnabled))
    
    state := tools.NewBasicState(ctx, stateOpts...)
    thread.SetState(state)
    thread.EnablePersistence(ctx, true)
    
    session := &Session{
        ID:         req.SessionID,
        Thread:     thread,
        State:      state,
        CWD:        req.CWD,
        MCPServers: req.MCPServers,
    }
    
    m.mu.Lock()
    m.sessions[session.ID] = session
    m.mu.Unlock()
    
    return session, nil
}

func (m *Manager) GetSession(id acp.SessionID) (*Session, error) {
    m.mu.RLock()
    defer m.mu.RUnlock()
    
    session, ok := m.sessions[id]
    if !ok {
        return nil, errors.Errorf("session not found: %s", id)
    }
    return session, nil
}

func (m *Manager) Cancel(id acp.SessionID) error {
    session, err := m.GetSession(id)
    if err != nil {
        return err
    }
    session.Cancel()
    return nil
}

func (m *Manager) connectMCPServers(ctx context.Context, servers []acp.MCPServer) (*tools.MCPManager, error) {
    // Convert ACP MCP server definitions to kodelet's format
    // and connect using existing MCP infrastructure
    // ... implementation
    return nil, nil
}
```

### Phase 3: Message Handler Bridge

#### 3.1 ACP Message Handler (`pkg/acp/bridge/handler.go`)

**Key Integration Point:** Implements `StreamingMessageHandler` to bridge kodelet events → ACP notifications

```go
package bridge

import (
    "encoding/json"
    "sync"
    "sync/atomic"

    "github.com/jingkaihe/kodelet/pkg/acp"
    llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
    tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// Ensure ACPMessageHandler implements StreamingMessageHandler
var _ llmtypes.StreamingMessageHandler = (*ACPMessageHandler)(nil)

type ACPMessageHandler struct {
    server    *acp.Server
    sessionID acp.SessionID
    
    // Track current tool call for status updates
    currentToolID   string
    currentToolName string
    toolIDCounter   int64
    toolMu          sync.Mutex
}

func NewACPMessageHandler(server *acp.Server, sessionID acp.SessionID) *ACPMessageHandler {
    return &ACPMessageHandler{
        server:    server,
        sessionID: sessionID,
    }
}

// HandleText sends complete text as agent_message_chunk
func (h *ACPMessageHandler) HandleText(text string) {
    h.server.SendUpdate(h.sessionID, map[string]interface{}{
        "sessionUpdate": "agent_message_chunk",
        "content": map[string]interface{}{
            "type": "text",
            "text": text,
        },
    })
}

// HandleTextDelta sends streaming text deltas
func (h *ACPMessageHandler) HandleTextDelta(delta string) {
    h.server.SendUpdate(h.sessionID, map[string]interface{}{
        "sessionUpdate": "agent_message_chunk",
        "content": map[string]interface{}{
            "type": "text",
            "text": delta,
        },
    })
}

// HandleToolUse creates a new tool_call update
func (h *ACPMessageHandler) HandleToolUse(toolName string, input string) {
    h.toolMu.Lock()
    toolID := fmt.Sprintf("call_%d", atomic.AddInt64(&h.toolIDCounter, 1))
    h.currentToolID = toolID
    h.currentToolName = toolName
    h.toolMu.Unlock()
    
    var rawInput json.RawMessage
    if input != "" {
        rawInput = json.RawMessage(input)
    }
    
    h.server.SendUpdate(h.sessionID, map[string]interface{}{
        "sessionUpdate": "tool_call",
        "toolCallId":    toolID,
        "title":         toolName,
        "kind":          ToACPToolKind(toolName),
        "status":        "pending",
        "rawInput":      rawInput,
    })
    
    // Immediately mark as in_progress
    h.server.SendUpdate(h.sessionID, map[string]interface{}{
        "sessionUpdate": "tool_call_update",
        "toolCallId":    toolID,
        "status":        "in_progress",
    })
}

// HandleToolResult sends tool_call_update with result
func (h *ACPMessageHandler) HandleToolResult(toolName string, result string) {
    h.toolMu.Lock()
    toolID := h.currentToolID
    h.toolMu.Unlock()
    
    h.server.SendUpdate(h.sessionID, map[string]interface{}{
        "sessionUpdate": "tool_call_update",
        "toolCallId":    toolID,
        "status":        "completed",
        "content": []map[string]interface{}{
            {
                "type": "content",
                "content": map[string]interface{}{
                    "type": "text",
                    "text": result,
                },
            },
        },
    })
}

// HandleThinking sends thought_chunk
func (h *ACPMessageHandler) HandleThinking(thinking string) {
    h.server.SendUpdate(h.sessionID, map[string]interface{}{
        "sessionUpdate": "thought_chunk",
        "content": map[string]interface{}{
            "type": "text",
            "text": thinking,
        },
    })
}

func (h *ACPMessageHandler) HandleThinkingStart() {
    // Nothing to send - client will see thought_chunk
}

func (h *ACPMessageHandler) HandleThinkingDelta(delta string) {
    h.server.SendUpdate(h.sessionID, map[string]interface{}{
        "sessionUpdate": "thought_chunk",
        "content": map[string]interface{}{
            "type": "text",
            "text": delta,
        },
    })
}

func (h *ACPMessageHandler) HandleContentBlockEnd() {
    // Nothing specific needed
}

func (h *ACPMessageHandler) HandleDone() {
    // Nothing to send - prompt response signals completion
}

// ToACPToolKind maps kodelet tool names to ACP tool kinds
func ToACPToolKind(toolName string) acp.ToolKind {
    switch toolName {
    case "file_read", "grep_tool", "glob_tool":
        return acp.ToolKindRead
    case "file_write", "file_edit":
        return acp.ToolKindEdit
    case "bash", "code_execution":
        return acp.ToolKindExecute
    case "web_fetch":
        return acp.ToolKindFetch
    case "thinking":
        return acp.ToolKindThink
    case "subagent":
        return acp.ToolKindSearch
    default:
        return acp.ToolKindOther
    }
}
```

### Phase 4: Request Handlers

#### 4.1 Initialize Handler

```go
// In pkg/acp/server.go

func (s *Server) handleInitialize(req *Request) error {
    var params struct {
        ProtocolVersion    int                `json:"protocolVersion"`
        ClientCapabilities ClientCapabilities `json:"clientCapabilities"`
        ClientInfo         *Implementation    `json:"clientInfo"`
    }
    if err := json.Unmarshal(req.Params, &params); err != nil {
        return s.sendError(req.ID, -32602, "Invalid params", nil)
    }
    
    // Store client capabilities
    s.clientCaps = &params.ClientCapabilities
    
    // Negotiate version
    version := ProtocolVersion
    if params.ProtocolVersion < version {
        version = params.ProtocolVersion
    }
    
    result := map[string]interface{}{
        "protocolVersion": version,
        "agentCapabilities": AgentCapabilities{
            LoadSession: true,
            PromptCapabilities: &PromptCapabilities{
                Image:           true,  // Kodelet supports images
                Audio:           false,
                EmbeddedContext: true,
            },
            MCPCapabilities: &MCPCapabilities{
                HTTP: true,
                SSE:  false,
            },
        },
        "agentInfo": map[string]interface{}{
            "name":    "kodelet",
            "title":   "Kodelet",
            "version": version.Version,
        },
        "authMethods": []interface{}{}, // No auth required
    }
    
    s.initialized = true
    return s.sendResult(req.ID, result)
}
```

#### 4.2 Session/Prompt Handler

```go
func (s *Server) handleSessionPrompt(req *Request) error {
    var params struct {
        SessionID SessionID      `json:"sessionId"`
        Prompt    []ContentBlock `json:"prompt"`
    }
    if err := json.Unmarshal(req.Params, &params); err != nil {
        return s.sendError(req.ID, -32602, "Invalid params", nil)
    }
    
    session, err := s.sessionManager.GetSession(params.SessionID)
    if err != nil {
        return s.sendError(req.ID, -32603, err.Error(), nil)
    }
    
    // Convert content blocks to text message
    // (support text, embedded resources, images)
    message, images := bridge.ContentBlocksToMessage(params.Prompt)
    
    // Create cancellable context
    ctx, cancel := context.WithCancel(s.ctx)
    session.mu.Lock()
    session.cancelFunc = cancel
    session.cancelled = false
    session.mu.Unlock()
    
    // Create ACP handler that sends session/update notifications
    handler := bridge.NewACPMessageHandler(s, params.SessionID)
    
    // Execute using existing thread
    _, err = session.Thread.SendMessage(ctx, message, handler, llmtypes.MessageOpt{
        PromptCache: true,
        Images:      images,
        MaxTurns:    50,
    })
    
    // Determine stop reason
    var stopReason StopReason
    if session.IsCancelled() {
        stopReason = StopReasonCancelled
    } else if err != nil {
        // Check error type
        stopReason = StopReasonEndTurn // or appropriate error handling
    } else {
        stopReason = StopReasonEndTurn
    }
    
    result := map[string]interface{}{
        "stopReason": stopReason,
    }
    return s.sendResult(req.ID, result)
}
```

### Phase 5: CLI Command

#### 5.1 ACP Command (`cmd/kodelet/acp.go`)

```go
package main

import (
    "github.com/jingkaihe/kodelet/pkg/acp"
    "github.com/jingkaihe/kodelet/pkg/llm"
    "github.com/jingkaihe/kodelet/pkg/logger"
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
  
  # With custom model
  kodelet acp --model claude-sonnet-4-5-20250929
  
  # Disable skills
  kodelet acp --no-skills`,
    RunE: runACP,
}

func init() {
    rootCmd.AddCommand(acpCmd)
    
    acpCmd.Flags().String("model", "", "LLM model to use")
    acpCmd.Flags().String("provider", "", "LLM provider (anthropic, openai, google)")
    acpCmd.Flags().Bool("no-skills", false, "Disable agentic skills")
    acpCmd.Flags().Bool("no-hooks", false, "Disable lifecycle hooks")
}

func runACP(cmd *cobra.Command, args []string) error {
    ctx := cmd.Context()
    
    // Configure logging to stderr (stdout is for JSON-RPC)
    logger.SetLogOutput(os.Stderr)
    logger.SetLogLevel("error") // Minimal logging in ACP mode
    
    // Load configuration
    config, err := llm.GetConfigFromViperWithCmd(cmd)
    if err != nil {
        return err
    }
    
    // Apply CLI flags
    if noSkills, _ := cmd.Flags().GetBool("no-skills"); noSkills {
        if config.Skills == nil {
            config.Skills = &llmtypes.SkillsConfig{}
        }
        config.Skills.Enabled = false
    }
    
    if noHooks, _ := cmd.Flags().GetBool("no-hooks"); noHooks {
        config.NoHooks = true
    }
    
    // Create and run ACP server
    server := acp.NewServer(
        acp.WithConfig(config),
    )
    
    return server.Run()
}
```

---

## File Changes Summary

### New Files

| File | Purpose |
|------|---------|
| `pkg/acp/server.go` | Main ACP server with JSON-RPC handling |
| `pkg/acp/types.go` | ACP protocol type definitions |
| `pkg/acp/session/manager.go` | Session lifecycle management |
| `pkg/acp/session/state.go` | Per-session state wrapper |
| `pkg/acp/bridge/handler.go` | ACPMessageHandler implementation |
| `pkg/acp/bridge/content.go` | Content block conversion |
| `pkg/acp/bridge/tools.go` | Tool result mapping |
| `pkg/acp/client/rpc.go` | Agent→Client RPC infrastructure |
| `pkg/acp/client/permission.go` | Permission request handling |
| `pkg/acp/client/filesystem.go` | File system client methods |
| `pkg/acp/client/terminal.go` | Terminal client methods |
| `cmd/kodelet/acp.go` | CLI command |
| `docs/ACP.md` | Documentation |

### Modified Files

| File | Changes |
|------|---------|
| `cmd/kodelet/main.go` | Import and register `acpCmd` |
| `pkg/logger/logger.go` | Add `SetLogOutput()` function (if not exists) |
| `AGENTS.md` | Add ACP documentation reference |
| `docs/MANUAL.md` | Add `kodelet acp` command documentation |

---

## Testing Strategy

### Unit Tests

1. **JSON-RPC parsing** (`pkg/acp/server_test.go`)
2. **Session management** (`pkg/acp/session/manager_test.go`)
3. **Message handler bridge** (`pkg/acp/bridge/handler_test.go`)
4. **Content block conversion** (`pkg/acp/bridge/content_test.go`)

### Integration Tests

```go
// pkg/acp/integration_test.go

func TestACPFullConversation(t *testing.T) {
    // Create pipes for stdin/stdout
    clientToServer, serverIn := io.Pipe()
    serverOut, serverToClient := io.Pipe()
    
    server := acp.NewServer(
        acp.WithInput(serverIn),
        acp.WithOutput(serverOut),
        acp.WithConfig(testConfig),
    )
    
    go server.Run()
    
    // Test initialize
    sendRequest(clientToServer, Request{
        JSONRPC: "2.0",
        ID:      json.RawMessage(`1`),
        Method:  "initialize",
        Params:  json.RawMessage(`{"protocolVersion":1}`),
    })
    
    resp := readResponse(serverToClient)
    assert.Equal(t, 1, resp.Result.ProtocolVersion)
    
    // Test session/new
    // Test session/prompt with updates
    // Test cancellation
}
```

### Mock Client

```go
// pkg/acp/testing/mock_client.go

type MockClient struct {
    received   []Notification
    permissions map[string]string
}

func (c *MockClient) HandlePermission(req PermissionRequest) PermissionResponse {
    // Auto-approve or use configured permissions
}
```

---

## Implementation Timeline

| Phase | Duration | Components |
|-------|----------|------------|
| **Phase 1** | Week 1 | Types, Server skeleton, JSON-RPC handling |
| **Phase 2** | Week 1-2 | Session manager, Initialize handler |
| **Phase 3** | Week 2 | ACPMessageHandler bridge |
| **Phase 4** | Week 2-3 | session/new, session/prompt, session/cancel |
| **Phase 5** | Week 3 | CLI command, basic testing |
| **Phase 6** | Week 3-4 | Client methods (permission, fs, terminal) |
| **Phase 7** | Week 4 | session/load, MCP server support |
| **Phase 8** | Week 4-5 | Integration tests, documentation |

---

## Risk Mitigation

### Potential Issues

1. **Streaming Granularity**: Kodelet's handlers may batch content differently than ACP expects
   - **Mitigation**: Use `StreamingMessageHandler` with fine-grained deltas

2. **Tool ID Mapping**: Kodelet uses internal tool IDs that differ from ACP's
   - **Mitigation**: Generate ACP-compatible IDs in the bridge

3. **Cancellation Timing**: Race conditions between cancel and tool execution
   - **Mitigation**: Use mutex-protected cancel flag, check before each operation

4. **MCP Server Lifecycle**: Client-provided MCP servers may need different handling
   - **Mitigation**: Phase MCP support after core functionality works

---

## Success Criteria

1. ✅ `kodelet acp` runs and accepts JSON-RPC over stdio
2. ✅ Initialize handshake with capability negotiation
3. ✅ Create new sessions with tool execution
4. ✅ Stream updates for text, thinking, and tool calls
5. ✅ Session cancellation works correctly
6. ✅ Session persistence and loading
7. ✅ Permission requests for destructive operations
8. ✅ Integration with at least one IDE (Zed or JetBrains)
