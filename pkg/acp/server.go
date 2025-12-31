// Package acp implements the Agent Client Protocol (ACP) server for kodelet.
// ACP enables kodelet to be embedded in ACP-compatible clients like Zed or
// JetBrains IDEs using JSON-RPC 2.0 over stdio.
package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	"github.com/jingkaihe/kodelet/pkg/acp/session"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/version"
	"github.com/pkg/errors"
)

// Server implements the ACP agent server
type Server struct {
	input  io.Reader
	output io.Writer

	mu             sync.Mutex
	initialized    bool
	clientCaps     *acptypes.ClientCapabilities
	sessionManager *session.Manager
	config         *ServerConfig

	ctx    context.Context
	cancel context.CancelFunc

	pendingRequests map[string]chan json.RawMessage
	pendingMu       sync.Mutex
	nextRequestID   int64
}

// ServerConfig holds configuration for the ACP server
type ServerConfig struct {
	Provider  string
	Model     string
	MaxTokens int
	NoSkills  bool
	NoHooks   bool
}

// Option configures the server
type Option func(*Server)

// WithInput sets the input reader
func WithInput(r io.Reader) Option {
	return func(s *Server) { s.input = r }
}

// WithOutput sets the output writer
func WithOutput(w io.Writer) Option {
	return func(s *Server) { s.output = w }
}

// WithConfig sets the server configuration
func WithConfig(config *ServerConfig) Option {
	return func(s *Server) { s.config = config }
}

// WithContext sets the server context
func WithContext(ctx context.Context) Option {
	return func(s *Server) {
		s.ctx, s.cancel = context.WithCancel(ctx)
	}
}

// NewServer creates a new ACP server
func NewServer(opts ...Option) *Server {
	ctx, cancel := context.WithCancel(context.Background())

	s := &Server{
		input:           os.Stdin,
		output:          os.Stdout,
		ctx:             ctx,
		cancel:          cancel,
		pendingRequests: make(map[string]chan json.RawMessage),
		config:          &ServerConfig{},
	}

	for _, opt := range opts {
		opt(s)
	}

	s.sessionManager = session.NewManager(s.config.Provider, s.config.Model, s.config.MaxTokens, s.config.NoSkills, s.config.NoHooks)
	return s
}

// Run starts the server event loop
func (s *Server) Run() error {
	scanner := bufio.NewScanner(s.input)
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
	var probe struct {
		ID     json.RawMessage    `json:"id"`
		Method string             `json:"method"`
		Result json.RawMessage    `json:"result"`
		Error  *acptypes.RPCError `json:"error"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return s.sendError(nil, acptypes.ErrCodeParseError, "Parse error", nil)
	}

	if probe.Method == "" && (probe.Result != nil || probe.Error != nil) {
		return s.handleResponse(probe.ID, probe.Result, probe.Error)
	}

	if probe.ID == nil || string(probe.ID) == "null" {
		return s.handleNotification(probe.Method, data)
	}

	return s.handleRequest(data)
}

func (s *Server) handleRequest(data []byte) error {
	var req acptypes.Request
	if err := json.Unmarshal(data, &req); err != nil {
		return s.sendError(nil, acptypes.ErrCodeParseError, "Parse error", nil)
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
	case "session/list":
		return s.handleListSessions(&req)
	default:
		return s.sendError(req.ID, acptypes.ErrCodeMethodNotFound, "Method not found", nil)
	}
}

func (s *Server) handleNotification(method string, data []byte) error {
	switch method {
	case "session/cancel":
		var notif acptypes.Notification
		if err := json.Unmarshal(data, &notif); err != nil {
			return err
		}
		var params acptypes.CancelRequest
		if err := json.Unmarshal(notif.Params, &params); err != nil {
			return err
		}
		return s.sessionManager.Cancel(params.SessionID)
	default:
		logger.G(s.ctx).WithField("method", method).Warn("Unknown notification")
		return nil
	}
}

func (s *Server) handleResponse(id json.RawMessage, result json.RawMessage, rpcErr *acptypes.RPCError) error {
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

	if rpcErr != nil {
		ch <- nil
		return nil
	}

	ch <- result
	return nil
}

func (s *Server) handleInitialize(req *acptypes.Request) error {
	var params acptypes.InitializeRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.sendError(req.ID, acptypes.ErrCodeInvalidParams, "Invalid params", nil)
	}

	s.clientCaps = &params.ClientCapabilities

	protocolVersion := acptypes.ProtocolVersion
	if params.ProtocolVersion < protocolVersion {
		protocolVersion = params.ProtocolVersion
	}

	result := acptypes.InitializeResponse{
		ProtocolVersion: protocolVersion,
		AgentCapabilities: acptypes.AgentCapabilities{
			LoadSession: true,
			PromptCapabilities: &acptypes.PromptCapabilities{
				Image:           true,
				Audio:           false,
				EmbeddedContext: true,
			},
			MCPCapabilities: &acptypes.MCPCapabilities{
				HTTP: true,
				SSE:  false,
			},
			SessionCapabilities: &acptypes.SessionCapabilities{
				SetMode: false,
			},
		},
		AgentInfo: &acptypes.Implementation{
			Name:    "kodelet",
			Title:   "Kodelet",
			Version: version.Version,
		},
		AuthMethods: []acptypes.AuthMethod{},
	}

	s.initialized = true
	return s.sendResult(req.ID, result)
}

func (s *Server) handleAuthenticate(req *acptypes.Request) error {
	return s.sendResult(req.ID, map[string]any{})
}

func (s *Server) handleSessionNew(req *acptypes.Request) error {
	if !s.initialized {
		return s.sendError(req.ID, acptypes.ErrCodeInternalError, "Not initialized", nil)
	}

	var params acptypes.NewSessionRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.sendError(req.ID, acptypes.ErrCodeInvalidParams, "Invalid params", nil)
	}

	sess, err := s.sessionManager.NewSession(s.ctx, params)
	if err != nil {
		return s.sendError(req.ID, acptypes.ErrCodeInternalError, err.Error(), nil)
	}

	result := acptypes.NewSessionResponse{
		SessionID: sess.ID,
	}
	return s.sendResult(req.ID, result)
}

func (s *Server) handleSessionLoad(req *acptypes.Request) error {
	if !s.initialized {
		return s.sendError(req.ID, acptypes.ErrCodeInternalError, "Not initialized", nil)
	}

	var params acptypes.LoadSessionRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.sendError(req.ID, acptypes.ErrCodeInvalidParams, "Invalid params", nil)
	}

	_, err := s.sessionManager.LoadSession(s.ctx, params)
	if err != nil {
		return s.sendError(req.ID, acptypes.ErrCodeInternalError, err.Error(), nil)
	}

	result := acptypes.LoadSessionResponse{}
	return s.sendResult(req.ID, result)
}

func (s *Server) handleSessionPrompt(req *acptypes.Request) error {
	if !s.initialized {
		return s.sendError(req.ID, acptypes.ErrCodeInternalError, "Not initialized", nil)
	}

	var params acptypes.PromptRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.sendError(req.ID, acptypes.ErrCodeInvalidParams, "Invalid params", nil)
	}

	sess, err := s.sessionManager.GetSession(params.SessionID)
	if err != nil {
		return s.sendError(req.ID, acptypes.ErrCodeInternalError, err.Error(), nil)
	}

	stopReason, err := sess.HandlePrompt(s.ctx, params.Prompt, s)
	if err != nil {
		if sess.IsCancelled() {
			stopReason = acptypes.StopReasonCancelled
		} else {
			return s.sendError(req.ID, acptypes.ErrCodeInternalError, err.Error(), nil)
		}
	}

	result := acptypes.PromptResponse{
		StopReason: stopReason,
	}
	return s.sendResult(req.ID, result)
}

func (s *Server) handleSetMode(req *acptypes.Request) error {
	return s.sendError(req.ID, acptypes.ErrCodeMethodNotFound, "session/set_mode not supported", nil)
}

func (s *Server) handleListSessions(req *acptypes.Request) error {
	if !s.initialized {
		return s.sendError(req.ID, acptypes.ErrCodeInternalError, "Not initialized", nil)
	}

	sessions := s.sessionManager.ListSessions(s.ctx)

	result := acptypes.ListSessionsResponse{
		Sessions: sessions,
	}
	return s.sendResult(req.ID, result)
}

// SendUpdate sends a session/update notification to the client
func (s *Server) SendUpdate(sessionID acptypes.SessionID, update any) error {
	params := map[string]any{
		"sessionId": sessionID,
		"update":    update,
	}
	return s.sendNotification("session/update", params)
}

// CallClient makes an RPC call to the client and waits for response
func (s *Server) CallClient(ctx context.Context, method string, params any) (json.RawMessage, error) {
	s.pendingMu.Lock()
	s.nextRequestID++
	id := s.nextRequestID
	idStr := fmt.Sprintf("%d", id)
	ch := make(chan json.RawMessage, 1)
	s.pendingRequests[idStr] = ch
	s.pendingMu.Unlock()

	if err := s.sendRequest(id, method, params); err != nil {
		s.pendingMu.Lock()
		delete(s.pendingRequests, idStr)
		s.pendingMu.Unlock()
		return nil, err
	}

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

// GetClientCapabilities returns the client capabilities
func (s *Server) GetClientCapabilities() *acptypes.ClientCapabilities {
	return s.clientCaps
}

func (s *Server) sendRequest(id int64, method string, params any) error {
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		req["params"] = params
	}
	return s.send(req)
}

func (s *Server) sendNotification(method string, params any) error {
	notif := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		notif["params"] = params
	}
	return s.send(notif)
}

func (s *Server) sendResult(id json.RawMessage, result any) error {
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	}
	return s.send(resp)
}

func (s *Server) sendError(id json.RawMessage, code int, message string, _ any) error {
	errObj := map[string]any{
		"code":    code,
		"message": message,
	}

	resp := map[string]any{
		"jsonrpc": "2.0",
		"error":   errObj,
	}
	if id != nil {
		resp["id"] = id
	}
	return s.send(resp)
}

func (s *Server) send(v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.Marshal(v)
	if err != nil {
		return errors.Wrap(err, "failed to marshal message")
	}

	_, err = s.output.Write(append(data, '\n'))
	return err
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown() {
	s.cancel()
}
