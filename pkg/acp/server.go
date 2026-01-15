// Package acp implements the Agent Client Protocol (ACP) server for kodelet.
// ACP enables kodelet to be embedded in ACP-compatible clients like Zed or
// JetBrains IDEs using JSON-RPC 2.0 over stdio.
package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	"github.com/jingkaihe/kodelet/pkg/acp/session"
	"github.com/jingkaihe/kodelet/pkg/fragments"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/version"
	pkgerrors "github.com/pkg/errors"
)

const (
	additionalInstructionsHeader = "\n\n---\n\nAdditional instructions:\n"
)

// Server implements the ACP agent server.
// The server handles JSON-RPC 2.0 messages concurrently - responses may arrive
// out-of-order relative to requests (which is allowed by JSON-RPC spec).
type Server struct {
	input  io.Reader
	output io.Writer

	// outputMu protects concurrent writes to output
	outputMu sync.Mutex

	// initialized is set after successful initialize handshake
	initialized atomic.Bool

	// clientCapsMu protects clientCaps
	clientCapsMu sync.RWMutex
	clientCaps   *acptypes.ClientCapabilities

	sessionManager    *session.Manager
	config            *ServerConfig
	fragmentProcessor *fragments.Processor

	ctx    context.Context
	cancel context.CancelFunc

	// wg tracks in-flight request handlers for graceful shutdown
	wg sync.WaitGroup

	pendingRequests map[string]chan json.RawMessage
	pendingMu       sync.Mutex
	nextRequestID   int64

	// activePrompts tracks in-flight prompt requests for async processing.
	// Only one prompt can be active per session at a time.
	activePrompts   map[acptypes.SessionID]context.CancelFunc
	activePromptsMu sync.Mutex
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
		activePrompts:   make(map[acptypes.SessionID]context.CancelFunc),
		config:          &ServerConfig{},
	}

	for _, opt := range opts {
		opt(s)
	}

	s.sessionManager = session.NewManager(s.config.Provider, s.config.Model, s.config.MaxTokens, s.config.NoSkills, s.config.NoHooks)

	fp, err := fragments.NewFragmentProcessor()
	if err != nil {
		logger.G(ctx).WithError(err).Warn("Failed to create fragment processor for slash commands")
	}
	s.fragmentProcessor = fp

	return s
}

// Run starts the server event loop.
// Requests are processed concurrently, so responses may arrive out-of-order.
// The method blocks until the input stream is closed or the context is cancelled.
func (s *Server) Run() error {
	scanner := bufio.NewScanner(s.input)
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)

	for scanner.Scan() {
		select {
		case <-s.ctx.Done():
			// Wait for in-flight requests before returning
			s.wg.Wait()
			return s.ctx.Err()
		default:
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		// Copy buffer before spawning goroutine - scanner reuses the buffer
		data := append([]byte(nil), line...)

		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			if err := s.handleMessage(data); err != nil {
				logger.G(s.ctx).WithError(err).Error("Failed to handle message")
			}
		}()
	}

	// Wait for in-flight requests before returning
	s.wg.Wait()
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

		// Mark session as cancelled first (best-effort, ignore errors since
		// cancellation is idempotent and the session may not exist yet)
		_ = s.sessionManager.Cancel(params.SessionID)

		// Then cancel the active prompt context (triggers immediate cancellation)
		s.activePromptsMu.Lock()
		if cancelFn, ok := s.activePrompts[params.SessionID]; ok {
			cancelFn()
			delete(s.activePrompts, params.SessionID)
		}
		s.activePromptsMu.Unlock()

		return nil
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

	s.clientCapsMu.Lock()
	s.clientCaps = &params.ClientCapabilities
	s.clientCapsMu.Unlock()

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

	s.initialized.Store(true)
	return s.sendResult(req.ID, result)
}

func (s *Server) handleAuthenticate(req *acptypes.Request) error {
	return s.sendResult(req.ID, map[string]any{})
}

func (s *Server) handleSessionNew(req *acptypes.Request) error {
	if !s.initialized.Load() {
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

	if err := s.sendResult(req.ID, result); err != nil {
		return err
	}

	return s.sendAvailableCommands(sess.ID)
}

func (s *Server) handleSessionLoad(req *acptypes.Request) error {
	if !s.initialized.Load() {
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
	if err := s.sendResult(req.ID, result); err != nil {
		return err
	}

	return s.sendAvailableCommands(params.SessionID)
}

func (s *Server) handleSessionPrompt(req *acptypes.Request) error {
	if !s.initialized.Load() {
		return s.sendError(req.ID, acptypes.ErrCodeInternalError, "Not initialized", nil)
	}

	var params acptypes.PromptRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.sendError(req.ID, acptypes.ErrCodeInvalidParams, "Invalid params", nil)
	}

	// Create cancellable context and register atomically
	promptCtx, promptCancel := context.WithCancel(s.ctx)

	s.activePromptsMu.Lock()
	if _, exists := s.activePrompts[params.SessionID]; exists {
		s.activePromptsMu.Unlock()
		promptCancel()
		return s.sendError(req.ID, acptypes.ErrCodeInternalError, "prompt already in progress for this session", nil)
	}
	s.activePrompts[params.SessionID] = promptCancel
	s.activePromptsMu.Unlock()

	defer func() {
		promptCancel()
		s.activePromptsMu.Lock()
		delete(s.activePrompts, params.SessionID)
		s.activePromptsMu.Unlock()
	}()

	sess, err := s.sessionManager.GetSession(params.SessionID)
	if err != nil {
		return s.sendError(req.ID, acptypes.ErrCodeInternalError, err.Error(), nil)
	}

	prompt := params.Prompt
	var invokedRecipe string
	if command, args, found := parseSlashCommand(params.Prompt); found && s.fragmentProcessor != nil {
		transformedPrompt, err := s.transformSlashCommandPrompt(command, args, params.Prompt)
		if err != nil {
			return s.sendError(req.ID, acptypes.ErrCodeInvalidParams, err.Error(), nil)
		}
		prompt = transformedPrompt
		invokedRecipe = command
	}

	stopReason, err := sess.HandlePrompt(promptCtx, prompt, s, session.HandlePromptOpts{InvokedRecipe: invokedRecipe})
	if err != nil {
		if sess.IsCancelled() || errors.Is(err, context.Canceled) {
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

// transformSlashCommandPrompt transforms a slash command into a prompt with recipe content
func (s *Server) transformSlashCommandPrompt(command, args string, originalPrompt []acptypes.ContentBlock) ([]acptypes.ContentBlock, error) {
	kvArgs, additionalText := parseSlashCommandArgs(args)

	config := &fragments.Config{
		FragmentName: command,
		Arguments:    kvArgs,
	}

	fragment, err := s.fragmentProcessor.LoadFragment(s.ctx, config)
	if err != nil {
		return nil, pkgerrors.Wrapf(err, "unknown recipe '/%s'. Available recipes: %s", command, s.getAvailableRecipeNames())
	}

	var promptBuilder strings.Builder
	promptBuilder.WriteString(fragment.Content)

	if additionalText != "" {
		promptBuilder.WriteString(additionalInstructionsHeader)
		promptBuilder.WriteString(additionalText)
	}

	var newPrompt []acptypes.ContentBlock

	newPrompt = append(newPrompt, acptypes.ContentBlock{
		Type: acptypes.ContentTypeText,
		Text: promptBuilder.String(),
	})

	for _, block := range originalPrompt {
		if block.Type == acptypes.ContentTypeText {
			continue
		}
		newPrompt = append(newPrompt, block)
	}

	return newPrompt, nil
}

func (s *Server) handleSetMode(req *acptypes.Request) error {
	return s.sendError(req.ID, acptypes.ErrCodeMethodNotFound, "session/set_mode not supported", nil)
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
		// Drain channel in case handleResponse is concurrently sending
		select {
		case <-ch:
		default:
		}
		return nil, ctx.Err()
	case result := <-ch:
		if result == nil {
			return nil, pkgerrors.New("client returned error")
		}
		return result, nil
	}
}

// GetClientCapabilities returns the client capabilities
func (s *Server) GetClientCapabilities() *acptypes.ClientCapabilities {
	s.clientCapsMu.RLock()
	defer s.clientCapsMu.RUnlock()
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
	data, err := json.Marshal(v)
	if err != nil {
		return pkgerrors.Wrap(err, "failed to marshal message")
	}

	s.outputMu.Lock()
	_, err = s.output.Write(append(data, '\n'))
	s.outputMu.Unlock()
	return err
}

// getAvailableRecipeNames returns a comma-separated list of available recipe names for error messages
func (s *Server) getAvailableRecipeNames() string {
	commands := s.getAvailableCommands()
	if len(commands) == 0 {
		return "(none available)"
	}

	names := make([]string, len(commands))
	for i, cmd := range commands {
		names[i] = "/" + cmd.Name
	}
	return strings.Join(names, ", ")
}

// getAvailableCommands returns available slash commands from the fragment/recipe system
func (s *Server) getAvailableCommands() []acptypes.AvailableCommand {
	if s.fragmentProcessor == nil {
		return nil
	}

	frags, err := s.fragmentProcessor.ListFragmentsWithMetadata()
	if err != nil {
		logger.G(s.ctx).WithError(err).Warn("Failed to list fragments for slash commands")
		return nil
	}

	var commands []acptypes.AvailableCommand
	for _, frag := range frags {
		name := frag.ID
		description := frag.Metadata.Description
		if description == "" {
			description = "Run the " + frag.ID + " recipe"
		}

		cmd := acptypes.AvailableCommand{
			Name:        name,
			Description: description,
			Input: &acptypes.AvailableCommandInput{
				Hint: buildCommandHint(frag.Metadata.Defaults),
			},
		}
		commands = append(commands, cmd)
	}

	return commands
}

// sendAvailableCommands sends the available_commands_update notification for a session
func (s *Server) sendAvailableCommands(sessionID acptypes.SessionID) error {
	commands := s.getAvailableCommands()
	if len(commands) == 0 {
		return nil
	}

	update := acptypes.AvailableCommandsUpdate{
		SessionUpdate:     acptypes.UpdateAvailableCommands,
		AvailableCommands: commands,
	}

	return s.SendUpdate(sessionID, update)
}

// parseSlashCommand parses a slash command from prompt content.
// The command name is everything after the leading "/" up to the first space.
// Recipe names may contain slashes (e.g., "github/pr" from "/github/pr").
// Returns the command name, any arguments after the command, and whether a command was found.
func parseSlashCommand(prompt []acptypes.ContentBlock) (command string, args string, found bool) {
	for _, block := range prompt {
		if block.Type != acptypes.ContentTypeText {
			continue
		}

		text := strings.TrimSpace(block.Text)
		if !strings.HasPrefix(text, "/") {
			continue
		}

		text = strings.TrimPrefix(text, "/")
		parts := strings.SplitN(text, " ", 2)
		command = parts[0]
		if command == "" {
			continue
		}
		if len(parts) > 1 {
			args = parts[1]
		}
		return command, args, true
	}

	return "", "", false
}

// parseSlashCommandArgs parses key=value pairs and additional text from an arguments string.
//
// Grammar:
//
//	args           = *(key_value / word) [additional_text]
//	key_value      = key "=" value
//	key            = 1*non_space_non_eq
//	value          = quoted_value / unquoted_value
//	quoted_value   = DQUOTE *non_dquote DQUOTE
//	unquoted_value = *non_space
//	word           = 1*non_space (collected as additional_text)
//
// Examples:
//
//	"target=main"                    -> {"target": "main"}, ""
//	"target=main fix the bug"        -> {"target": "main"}, "fix the bug"
//	`title="my feature" draft=true`  -> {"title": "my feature", "draft": "true"}, ""
func parseSlashCommandArgs(args string) (kvArgs map[string]string, additionalText string) {
	kvArgs = make(map[string]string)
	if args == "" {
		return kvArgs, ""
	}

	var textParts []string
	i := 0
	for i < len(args) {
		i = skipSpaces(args, i)
		if i >= len(args) {
			break
		}

		keyEnd := findKeyEnd(args, i)

		if keyEnd < len(args) && args[keyEnd] == '=' {
			key := args[i:keyEnd]
			value, nextPos := parseValue(args, keyEnd+1)
			kvArgs[key] = value
			i = nextPos
		} else {
			wordEnd := findWordEnd(args, i)
			textParts = append(textParts, args[i:wordEnd])
			i = wordEnd
		}
	}

	return kvArgs, strings.Join(textParts, " ")
}

func skipSpaces(s string, i int) int {
	for i < len(s) && s[i] == ' ' {
		i++
	}
	return i
}

func findKeyEnd(s string, start int) int {
	i := start
	for i < len(s) && s[i] != '=' && s[i] != ' ' {
		i++
	}
	return i
}

func findWordEnd(s string, start int) int {
	i := start
	for i < len(s) && s[i] != ' ' {
		i++
	}
	return i
}

func parseValue(s string, start int) (value string, nextPos int) {
	if start >= len(s) {
		return "", start
	}

	if s[start] == '"' {
		return parseQuotedValue(s, start+1)
	}
	return parseUnquotedValue(s, start)
}

func parseQuotedValue(s string, start int) (value string, nextPos int) {
	end := start
	for end < len(s) && s[end] != '"' {
		end++
	}
	if end < len(s) {
		return s[start:end], end + 1
	}
	return s[start:end], end
}

func parseUnquotedValue(s string, start int) (value string, nextPos int) {
	end := findWordEnd(s, start)
	return s[start:end], end
}

// buildCommandHint builds a hint string for a recipe based on its defaults
func buildCommandHint(defaults map[string]string) string {
	if len(defaults) == 0 {
		return "additional instructions (optional)"
	}

	keys := make([]string, 0, len(defaults))
	for k := range defaults {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", key, defaults[key]))
	}

	return fmt.Sprintf("[%s] additional instructions", strings.Join(parts, " "))
}

// Shutdown gracefully shuts down the server.
// It cancels the context and waits for in-flight requests to complete.
func (s *Server) Shutdown() {
	s.cancel()
	s.wg.Wait()
}
