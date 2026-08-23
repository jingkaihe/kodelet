package extensions

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/osutil"
	kodelettools "github.com/jingkaihe/kodelet/pkg/tools"
	conversationtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
)

// Process is a running extension subprocess.
type Process struct {
	Extension    Extension
	cmd          *exec.Cmd
	client       *rpcClient
	stdin        io.WriteCloser
	stdout       io.ReadCloser
	config       Config
	workspaceCWD string
	runtimeCtx   context.Context
	lifecycleMu  sync.Mutex
	mu           sync.Mutex
	uiMu         sync.RWMutex
	closed       bool
	shutdown     bool
	disabled     bool
	failures     int
	uiHost       ExtensionUIHost
	uiSource     *processExtensionUISource
}

var extensionProcessGeneration atomic.Uint64

const (
	workspaceCWDEnvKey         = "KODELET_EXTENSION_WORKSPACE_CWD"
	extensionInitializeTimeout = 3 * time.Minute
	extensionProcessWaitDelay  = time.Second
	maxProcessFailures         = 3
	restartBackoffBase         = 100 * time.Millisecond
	restartBackoffMax          = 2 * time.Second
)

// StartProcess starts an extension subprocess and initializes its JSON-RPC client.
func StartProcess(ctx context.Context, ext Extension, config Config, workspaceCWD string) (*Process, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	p := &Process{
		Extension:    ext,
		config:       config,
		workspaceCWD: workspaceCWD,
		runtimeCtx:   ctx,
		closed:       true,
	}
	if err := p.start(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Process) start() error {
	runtimeCtx := p.runtimeCtx
	if runtimeCtx == nil {
		runtimeCtx = context.Background()
	}
	cmd := exec.CommandContext(runtimeCtx, p.Extension.ExecPath)
	cmd.Dir = p.Extension.Dir
	cmd.Env = extensionProcessEnv(p.workspaceCWD)
	// Keep extension diagnostics on the host's configured log sink so a
	// full-screen UI can redirect them without replacing the process stderr.
	cmd.Stderr = newExtensionStderrWriter(runtimeCtx, p.Extension.ID, logger.G(runtimeCtx).Logger.Out)
	osutil.SetProcessGroup(cmd)
	cmd.WaitDelay = extensionProcessWaitDelay

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return errors.Wrap(err, "failed to create extension stdin")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return errors.Wrap(err, "failed to create extension stdout")
	}
	if err := cmd.Start(); err != nil {
		return errors.Wrap(err, "failed to start extension process")
	}

	client := newRPCClient(stdout, stdin)
	generation := extensionProcessGeneration.Add(1)
	source := &processExtensionUISource{
		process: p,
		client:  client,
		owner:   UIExtensionOwner{ExtensionID: p.Extension.ID, Generation: generation},
	}
	client.setHostRequestHandler(source)
	client.setTerminalHandler(func(error) { p.failClientGeneration(client) })
	p.cmd = cmd
	p.client = client
	p.uiSource = source
	p.stdin = stdin
	p.stdout = stdout
	p.closed = false
	return nil
}

func extensionProcessEnv(workspaceCWD string) []string {
	env := os.Environ()
	if strings.TrimSpace(workspaceCWD) == "" {
		return env
	}
	entry := workspaceCWDEnvKey + "=" + workspaceCWD
	for i, existing := range env {
		if strings.HasPrefix(existing, workspaceCWDEnvKey+"=") {
			env[i] = entry
			return env
		}
	}
	return append(env, entry)
}

func (p *Process) ensureRunning(ctx context.Context) error {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()

	p.mu.Lock()

	if p.disabled {
		p.mu.Unlock()
		return errors.Errorf("extension %s disabled after repeated failures", p.Extension.ID)
	}
	if p.shutdown {
		p.mu.Unlock()
		return errors.Errorf("extension %s is shut down", p.Extension.ID)
	}
	if !p.closed {
		p.mu.Unlock()
		return nil
	}

	backoff := restartBackoffBase
	for range max(0, p.failures-1) {
		backoff *= 2
		if backoff >= restartBackoffMax {
			backoff = restartBackoffMax
			break
		}
	}
	if backoff > restartBackoffMax {
		backoff = restartBackoffMax
	}
	if backoff > 0 {
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			p.mu.Unlock()
			return ctx.Err()
		case <-timer.C:
		}
	}

	if err := p.start(); err != nil {
		p.recordFailureLocked()
		p.mu.Unlock()
		return err
	}
	client := p.client
	source := p.uiSource
	cwd := p.workspaceCWD
	p.mu.Unlock()

	initCtx, cancel := context.WithTimeout(ctx, extensionInitializeTimeout)
	_, err := p.initialize(initCtx, cwd, client, source)
	cancel()

	p.mu.Lock()
	if err != nil {
		var closedClient *rpcClient
		if p.client == client && !p.closed {
			closedClient, _ = p.closeProcessLocked()
			p.recordFailureLocked()
		}
		p.mu.Unlock()
		closedClient.waitIncomingRequests()
		return err
	}
	if p.client == client && !p.closed {
		p.failures = 0
	}
	p.mu.Unlock()
	return nil
}

func (p *Process) recordFailureLocked() {
	if p.shutdown || p.disabled {
		return
	}
	p.failures++
	if p.failures >= maxProcessFailures {
		p.disabled = true
	}
}

// Initialize initializes the extension process and returns its registrations.
func (p *Process) Initialize(ctx context.Context, cwd string) (*InitializeResult, error) {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()

	p.mu.Lock()
	p.workspaceCWD = cwd
	client := p.client
	source := p.uiSource
	p.mu.Unlock()
	return p.initialize(ctx, cwd, client, source)
}

func (p *Process) initialize(ctx context.Context, cwd string, client *rpcClient, source *processExtensionUISource) (*InitializeResult, error) {
	if source != nil {
		source.setHostContext(ctx)
	}
	uiHost, hasExtensionUI := ExtensionUIHostFromContext(ctx)
	if hasExtensionUI {
		p.uiMu.Lock()
		p.uiHost = uiHost
		p.uiMu.Unlock()
	}
	p.uiMu.RLock()
	activeUIHost := p.uiHost
	hasExtensionUI = activeUIHost != nil
	_, hasExtensionTranscript := activeUIHost.(ExtensionUITranscriptHost)
	p.uiMu.RUnlock()

	dataDir, err := extensionDataDir(p.Extension.ID)
	if err != nil {
		return nil, err
	}
	params := initializeParams{
		ProtocolVersion: protocolVersion,
		Kodelet: map[string]any{
			"version": "dev",
		},
		Extension: initializeExtensionInfo{
			ID:      p.Extension.ID,
			Config:  map[string]any{},
			CWD:     cwd,
			DataDir: dataDir,
		},
		Capabilities: map[string]any{
			"tools":       true,
			"toolUpdates": true,
			"commands":    true,
			"conversations": map[string]any{
				"fork": true,
			},
			"ui": map[string]any{
				"input":      true,
				"confirm":    true,
				"select":     true,
				"notify":     true,
				"widgets":    hasExtensionUI,
				"surfaces":   hasExtensionUI,
				"transcript": hasExtensionTranscript,
			},
			"events": []string{
				"session.start",
				"resources.discover",
				"user.message",
				"agent.init",
				"agent.start",
				"turn.start",
				"tool.call",
				"tool.update",
				"tool.result",
				"turn.end",
				"agent.end",
				"session.end",
			},
		},
	}

	var result InitializeResult
	if client == nil || source == nil {
		return nil, errors.Errorf("extension %s is not running", p.Extension.ID)
	}
	if err := client.callWithHostHandler(ctx, "extension.initialize", params, &result, source); err != nil {
		return nil, err
	}
	return &result, nil
}

func extensionDataDir(extensionID string) (string, error) {
	basePath, err := conversationtypes.GetDefaultBasePath()
	if err == nil {
		basePath = osutil.CanonicalizePath(basePath)
	} else if home, homeErr := os.UserHomeDir(); homeErr == nil {
		basePath = osutil.CanonicalizePath(filepath.Join(home, ".kodelet"))
	} else {
		return "", errors.Wrap(err, "failed to resolve extension data directory")
	}
	dataDir := filepath.Join(basePath, "extensions", "data", safeDataDirName(extensionID))
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", errors.Wrap(err, "failed to create extension data directory")
	}
	return osutil.CanonicalizePath(dataDir), nil
}

func safeDataDirName(extensionID string) string {
	extensionID = strings.TrimSpace(extensionID)
	if extensionID == "" {
		return "extension"
	}
	var builder strings.Builder
	for _, char := range extensionID {
		switch {
		case char >= 'a' && char <= 'z':
			builder.WriteRune(char)
		case char >= 'A' && char <= 'Z':
			builder.WriteRune(char)
		case char >= '0' && char <= '9':
			builder.WriteRune(char)
		case char == '.', char == '-', char == '_', char == '@':
			builder.WriteRune(char)
		default:
			builder.WriteRune('_')
		}
	}
	name := strings.Trim(builder.String(), "._")
	if name == "" {
		return "extension"
	}
	return name
}

// ExecuteTool invokes an extension-provided tool.
func (p *Process) ExecuteTool(ctx context.Context, name string, input json.RawMessage, callContext ExtensionCallContext) (*ToolExecutionResult, error) {
	return p.executeTool(ctx, name, input, callContext, nil)
}

// ExecuteToolStreaming invokes an extension-provided tool and forwards transient updates.
func (p *Process) ExecuteToolStreaming(ctx context.Context, name string, input json.RawMessage, callContext ExtensionCallContext, onUpdate func(ToolExecutionResult)) (*ToolExecutionResult, error) {
	return p.executeTool(ctx, name, input, callContext, onUpdate)
}

func (p *Process) executeTool(ctx context.Context, name string, input json.RawMessage, callContext ExtensionCallContext, onUpdate func(ToolExecutionResult)) (*ToolExecutionResult, error) {
	if err := p.ensureRunning(ctx); err != nil {
		return nil, err
	}
	client, source := p.rpcSession()
	if client == nil || source == nil {
		return nil, errors.Errorf("extension %s is not running", p.Extension.ID)
	}

	callContext = extensionCallContextWithUIScope(ctx, callContext)
	params := executeToolParams{Name: name, Input: input, Context: callContext}
	var result ToolExecutionResult
	handler := toolExecutionHostHandler{
		source:      source,
		onUpdate:    onUpdate,
		extensionID: p.Extension.ID,
		toolName:    name,
	}
	if err := client.callWithHostHandler(ctx, "extension.tool.execute", params, &result, handler); err != nil {
		if shouldRestartAfterCallError(err) {
			p.failClientGeneration(client)
		}
		return nil, err
	}
	return &result, nil
}

type toolExecutionHostHandler struct {
	source      *processExtensionUISource
	onUpdate    func(ToolExecutionResult)
	extensionID string
	toolName    string
}

func (h toolExecutionHostHandler) HandleRPCRequest(ctx context.Context, method string, params json.RawMessage) (any, *rpcError) {
	if method == ConversationForkMethod {
		toolContext := kodelettools.ToolContextFromContext(ctx)
		forker, ok := toolContext.MetadataStore.(llmtypes.ConversationForker)
		if !ok {
			return nil, &rpcError{Code: conversationForkUnavailableCode, Message: "live conversation forking is unavailable for this tool call"}
		}
		if strings.TrimSpace(h.extensionID) != "" && strings.TrimSpace(h.toolName) != "" {
			ctx = conversationtypes.ContextWithConversationForkInitiator(ctx, conversationtypes.ConversationForkInitiator{
				Type:        conversationtypes.ConversationForkInitiatorTypeExtensionTool,
				ExtensionID: h.extensionID,
				ToolName:    h.toolName,
			})
		}
		conversationID, err := forker.ForkConversation(ctx)
		if err != nil {
			if errors.Is(err, llmtypes.ErrConversationForkUnavailable) {
				return nil, &rpcError{Code: conversationForkUnavailableCode, Message: err.Error()}
			}
			return nil, &rpcError{Code: -32000, Message: err.Error()}
		}
		if strings.TrimSpace(conversationID) == "" {
			return nil, &rpcError{Code: -32000, Message: "live conversation fork returned an empty conversation ID"}
		}
		return conversationForkResult{ConversationID: conversationID}, nil
	}

	if method != "kodelet.tool.update" {
		return h.source.HandleRPCRequest(ctx, method, params)
	}

	var update ToolExecutionResult
	if err := json.Unmarshal(params, &update); err != nil {
		return nil, &rpcError{Code: -32602, Message: err.Error()}
	}
	if h.onUpdate != nil {
		h.onUpdate(update)
	}
	return map[string]any{"accepted": true}, nil
}

// ExecuteCommand invokes an extension-provided command over JSON-RPC.
func (p *Process) ExecuteCommand(ctx context.Context, name string, input map[string]any, invocation CommandInvocation, callContext ExtensionCallContext) (*CommandResult, error) {
	if err := p.ensureRunning(ctx); err != nil {
		return nil, err
	}
	client, source := p.rpcSession()
	if client == nil || source == nil {
		return nil, errors.Errorf("extension %s is not running", p.Extension.ID)
	}

	callContext = extensionCallContextWithUIScope(ctx, callContext)
	params := executeCommandParams{Name: name, Input: input, Invocation: invocation, Context: callContext}
	var result CommandResult
	if err := client.callWithHostHandler(ctx, "extension.command.execute", params, &result, source); err != nil {
		if shouldRestartAfterCallError(err) {
			p.failClientGeneration(client)
		}
		return nil, err
	}
	return &result, nil
}

// ExecuteShortcut invokes an extension-provided keyboard shortcut over JSON-RPC.
func (p *Process) ExecuteShortcut(ctx context.Context, key string, callContext ExtensionCallContext) error {
	if err := p.ensureRunning(ctx); err != nil {
		return err
	}
	client, source := p.rpcSession()
	if client == nil || source == nil {
		return errors.Errorf("extension %s is not running", p.Extension.ID)
	}

	callContext = extensionCallContextWithUIScope(ctx, callContext)
	params := executeShortcutParams{Key: key, Context: callContext}
	if err := client.callWithHostHandler(ctx, "extension.shortcut.execute", params, nil, source); err != nil {
		if shouldRestartAfterCallError(err) {
			p.failClientGeneration(client)
		}
		return err
	}
	return nil
}

// HandleEvent invokes an extension event handler.
func (p *Process) HandleEvent(ctx context.Context, eventID string, eventName string, payload any, callContext ExtensionCallContext) (*EventResult, error) {
	if err := p.ensureRunning(ctx); err != nil {
		return nil, err
	}
	client, source := p.rpcSession()
	if client == nil || source == nil {
		return nil, errors.Errorf("extension %s is not running", p.Extension.ID)
	}

	callContext = extensionCallContextWithUIScope(ctx, callContext)
	params := eventParams{ID: eventID, Event: eventName, Context: callContext, Payload: payload}
	var result EventResult
	if err := client.callWithHostHandler(ctx, "extension.event.handle", params, &result, source); err != nil {
		if shouldRestartAfterCallError(err) {
			p.failClientGeneration(client)
		}
		return nil, err
	}
	return &result, nil
}

func extensionCallContextWithUIScope(ctx context.Context, callContext ExtensionCallContext) ExtensionCallContext {
	if strings.TrimSpace(callContext.UIScopeID) == "" {
		callContext.UIScopeID = ExtensionUIScopeFromContext(ctx)
	}
	return callContext
}

func (p *Process) HandleRPCRequest(ctx context.Context, method string, params json.RawMessage) (any, *rpcError) {
	_, source := p.rpcSession()
	return p.handleRPCRequest(ctx, source, method, params)
}

func (p *Process) handleRPCRequest(ctx context.Context, source UIExtensionSource, method string, params json.RawMessage) (any, *rpcError) {
	if isPersistentExtensionUIRequest(method) && !hasExplicitExtensionUIScope(params) {
		ctx = ContextWithExtensionUIImplicitScope(ctx)
	}
	switch method {
	case UIWidgetSetMethod:
		var request UIWidgetSetRequest
		if err := json.Unmarshal(params, &request); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		return p.handleExtensionUIRequest(func(host ExtensionUIHost) (UIFrameResponse, error) {
			return host.SetWidget(ctx, source, request)
		})
	case UIWidgetFrameMethod:
		var request UIWidgetFrameRequest
		if err := json.Unmarshal(params, &request); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		return p.handleExtensionUIRequest(func(host ExtensionUIHost) (UIFrameResponse, error) {
			return host.UpdateWidget(ctx, source, request)
		})
	case UIWidgetRemoveMethod:
		var request UIWidgetRemoveRequest
		if err := json.Unmarshal(params, &request); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		return p.handleExtensionUIRequest(func(host ExtensionUIHost) (UIFrameResponse, error) {
			return host.RemoveWidget(ctx, source, request)
		})
	case UITranscriptAppendMethod:
		var request UITranscriptAppendRequest
		if err := json.Unmarshal(params, &request); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		p.uiMu.RLock()
		host, ok := p.uiHost.(ExtensionUITranscriptHost)
		p.uiMu.RUnlock()
		if !ok {
			return UITranscriptAppendResponse{Reason: "extension transcript is not available"}, nil
		}
		response, err := host.AppendTranscript(ctx, source, request)
		if err != nil {
			return nil, &rpcError{Code: -32000, Message: err.Error()}
		}
		return response, nil
	case UISurfaceOpenMethod:
		var request UISurfaceOpenRequest
		if err := json.Unmarshal(params, &request); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		return p.handleExtensionUIRequest(func(host ExtensionUIHost) (UIFrameResponse, error) {
			return host.OpenSurface(ctx, source, request)
		})
	case UISurfaceFrameMethod:
		var request UISurfaceFrameRequest
		if err := json.Unmarshal(params, &request); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		return p.handleExtensionUIRequest(func(host ExtensionUIHost) (UIFrameResponse, error) {
			return host.UpdateSurface(ctx, source, request)
		})
	case UISurfaceCloseMethod:
		var request UISurfaceCloseRequest
		if err := json.Unmarshal(params, &request); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		return p.handleExtensionUIRequest(func(host ExtensionUIHost) (UIFrameResponse, error) {
			return host.CloseSurface(ctx, source, request)
		})
	case "kodelet.ui.input":
		var request UIInputRequest
		if err := json.Unmarshal(params, &request); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if request.ID == "" {
			request.ID = NewUIInputRequestID()
		}
		broker, ok := UIInputBrokerFromContext(ctx)
		if !ok {
			return UIInputResponse{Status: UIInputStatusUnavailable, Reason: "ui input is not available"}, nil
		}
		response, err := broker.Input(ctx, request)
		if err != nil {
			return nil, &rpcError{Code: -32000, Message: err.Error()}
		}
		if response.Status == "" {
			response.Status = UIInputStatusDismissed
		}
		return response, nil
	case "kodelet.ui.confirm":
		var request UIConfirmRequest
		if err := json.Unmarshal(params, &request); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if request.ID == "" {
			request.ID = NewUIInputRequestID()
		}
		broker, ok := UIConfirmBrokerFromContext(ctx)
		if !ok {
			return UIInputResponse{Status: UIInputStatusUnavailable, Reason: "ui confirm is not available"}, nil
		}
		response, err := broker.Confirm(ctx, request)
		if err != nil {
			return nil, &rpcError{Code: -32000, Message: err.Error()}
		}
		if response.Status == "" {
			response.Status = UIInputStatusDismissed
		}
		return response, nil
	case "kodelet.ui.select":
		var request UISelectRequest
		if err := json.Unmarshal(params, &request); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if request.ID == "" {
			request.ID = NewUIInputRequestID()
		}
		broker, ok := UISelectBrokerFromContext(ctx)
		if !ok {
			return UIInputResponse{Status: UIInputStatusUnavailable, Reason: "ui select is not available"}, nil
		}
		response, err := broker.Select(ctx, request)
		if err != nil {
			return nil, &rpcError{Code: -32000, Message: err.Error()}
		}
		if response.Status == "" {
			response.Status = UIInputStatusDismissed
		}
		return response, nil
	case "kodelet.ui.notify":
		var request UINotifyRequest
		if err := json.Unmarshal(params, &request); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		broker, ok := UINotifyBrokerFromContext(ctx)
		if !ok {
			return UIInputResponse{Status: UIInputStatusUnavailable, Reason: "ui notify is not available"}, nil
		}
		response, err := broker.Notify(ctx, request)
		if err != nil {
			return nil, &rpcError{Code: -32000, Message: err.Error()}
		}
		if response.Status == "" {
			response.Status = UIInputStatusSubmitted
		}
		return response, nil
	default:
		return nil, &rpcError{Code: -32601, Message: "host request method not found"}
	}
}

func (p *Process) handleRPCNotification(source UIExtensionSource, method string, params json.RawMessage) {
	switch method {
	case UIWidgetFrameMethod, UISurfaceFrameMethod:
		ctx := context.Background()
		if processSource, ok := source.(*processExtensionUISource); ok {
			ctx = processSource.hostContext()
		}
		_, _ = p.handleRPCRequest(ctx, source, method, params)
	}
}

func (p *Process) handleExtensionUIRequest(handle func(ExtensionUIHost) (UIFrameResponse, error)) (any, *rpcError) {
	p.uiMu.RLock()
	host := p.uiHost
	p.uiMu.RUnlock()
	if host == nil {
		return UIFrameResponse{Reason: "extension UI is not available"}, nil
	}
	response, err := handle(host)
	if err != nil {
		return nil, &rpcError{Code: -32000, Message: err.Error()}
	}
	return response, nil
}

func (p *Process) rpcSession() (*rpcClient, *processExtensionUISource) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.disabled || p.shutdown {
		return nil, nil
	}
	return p.client, p.uiSource
}

type processExtensionUISource struct {
	process          *Process
	client           *rpcClient
	owner            UIExtensionOwner
	hostCtxMu        sync.RWMutex
	hostCtx          context.Context
	hostCancel       context.CancelFunc
	notifyMu         sync.Mutex
	notify           map[string]*orderedExtensionUINotifications
	notifyLifecycles map[string]uint64
}

func (s *processExtensionUISource) setHostContext(ctx context.Context) {
	if s == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	hostCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.hostCtxMu.Lock()
	previousCancel := s.hostCancel
	s.hostCtx = hostCtx
	s.hostCancel = cancel
	s.hostCtxMu.Unlock()
	if previousCancel != nil {
		previousCancel()
	}
}

func (s *processExtensionUISource) hostContext() context.Context {
	if s == nil {
		return context.Background()
	}
	s.hostCtxMu.RLock()
	ctx := s.hostCtx
	s.hostCtxMu.RUnlock()
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func (s *processExtensionUISource) cancelHostContext() {
	if s == nil {
		return
	}
	s.hostCtxMu.Lock()
	cancel := s.hostCancel
	s.hostCancel = nil
	s.hostCtx = nil
	s.hostCtxMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

type orderedExtensionUINotifications struct {
	next    uint64
	sending bool
	pending map[uint64]extensionUINotification
}

type extensionUINotification struct {
	method string
	params any
}

func (s *processExtensionUISource) HandleRPCRequest(ctx context.Context, method string, params json.RawMessage) (any, *rpcError) {
	if !s.current() {
		return nil, &rpcError{Code: -32000, Message: "extension process generation is no longer active"}
	}
	ctx = ContextWithUIExtensionOwner(ctx, s.owner)
	return s.process.handleRPCRequest(ctx, s, method, params)
}

func (s *processExtensionUISource) HandleRPCNotification(method string, params json.RawMessage) {
	if s.current() {
		s.process.handleRPCNotification(s, method, params)
	}
}

func (s *processExtensionUISource) ExtensionUIOwner() UIExtensionOwner {
	if s == nil {
		return UIExtensionOwner{}
	}
	return s.owner
}

func (s *processExtensionUISource) NotifyExtensionUI(_ context.Context, method string, params any) error {
	return s.notifyExtensionUI(method, params, 0, false)
}

// NotifyExtensionUISurfaceEvent sends an event only if its surface lifecycle is current.
func (s *processExtensionUISource) NotifyExtensionUISurfaceEvent(_ context.Context, lifecycle uint64, method string, params any) error {
	return s.notifyExtensionUI(method, params, lifecycle, true)
}

func (s *processExtensionUISource) notifyExtensionUI(method string, params any, lifecycle uint64, lifecycleScoped bool) error {
	if !s.current() {
		return errors.New("extension process generation is no longer active")
	}
	key, sequence, ordered := extensionUIEventSequence(method, params)
	if !ordered {
		return s.client.notify(method, params)
	}

	s.notifyMu.Lock()
	if lifecycleScoped && (s.notifyLifecycles == nil || s.notifyLifecycles[key] != lifecycle) {
		s.notifyMu.Unlock()
		return nil
	}
	if s.notify == nil {
		s.notify = map[string]*orderedExtensionUINotifications{}
	}
	state := s.notify[key]
	if state == nil {
		state = &orderedExtensionUINotifications{next: 1, pending: map[uint64]extensionUINotification{}}
		s.notify[key] = state
	}
	if sequence < state.next {
		s.notifyMu.Unlock()
		return nil
	}
	if _, exists := state.pending[sequence]; !exists {
		state.pending[sequence] = extensionUINotification{method: method, params: params}
	}
	_, ready := state.pending[state.next]
	start := !state.sending && ready
	if start {
		state.sending = true
	}
	s.notifyMu.Unlock()
	if start {
		go s.sendOrderedExtensionUINotifications(key)
	}
	return nil
}

func (s *processExtensionUISource) sendOrderedExtensionUINotifications(key string) {
	for {
		s.notifyMu.Lock()
		state := s.notify[key]
		if state == nil {
			s.notifyMu.Unlock()
			return
		}
		notification, ok := state.pending[state.next]
		if !ok {
			state.sending = false
			s.notifyMu.Unlock()
			return
		}
		delete(state.pending, state.next)
		state.next++
		if !s.current() {
			state.sending = false
			s.notifyMu.Unlock()
			return
		}
		if err := s.client.notify(notification.method, notification.params); err != nil {
			state.sending = false
			s.notifyMu.Unlock()
			return
		}
		s.notifyMu.Unlock()
	}
}

// PrepareUISurfaceEventLifecycle resets event ordering for an accepted lifecycle.
func (s *processExtensionUISource) PrepareUISurfaceEventLifecycle(scopeID, id string, lifecycle uint64) {
	if s == nil || strings.TrimSpace(id) == "" || lifecycle == 0 {
		return
	}
	key := extensionUISurfaceEventKey(scopeID, id)
	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()
	if s.notify != nil {
		delete(s.notify, key)
	}
	if s.notifyLifecycles == nil {
		s.notifyLifecycles = map[string]uint64{}
	}
	s.notifyLifecycles[key] = lifecycle
}

func extensionUIEventSequence(method string, params any) (string, uint64, bool) {
	switch method {
	case UISurfaceInputMethod:
		switch value := params.(type) {
		case UISurfaceInputNotification:
			return extensionUISurfaceEventKey(value.ScopeID, value.ID), value.Sequence, value.ID != "" && value.Sequence > 0
		case *UISurfaceInputNotification:
			if value != nil {
				return extensionUISurfaceEventKey(value.ScopeID, value.ID), value.Sequence, value.ID != "" && value.Sequence > 0
			}
		}
	case UISurfaceResizeMethod:
		switch value := params.(type) {
		case UISurfaceResizeNotification:
			return extensionUISurfaceEventKey(value.ScopeID, value.ID), value.Sequence, value.ID != "" && value.Sequence > 0
		case *UISurfaceResizeNotification:
			if value != nil {
				return extensionUISurfaceEventKey(value.ScopeID, value.ID), value.Sequence, value.ID != "" && value.Sequence > 0
			}
		}
	}
	return "", 0, false
}

func extensionUISurfaceEventKey(scopeID, id string) string {
	return strings.TrimSpace(scopeID) + "\x00" + id
}

func (s *processExtensionUISource) current() bool {
	if s == nil || s.process == nil || s.client == nil {
		return false
	}
	p := s.process
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.closed && !p.disabled && !p.shutdown && p.client == s.client && p.uiSource == s
}

func shouldRestartAfterCallError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return !strings.Contains(err.Error(), "extension rpc error")
}

func (p *Process) failClientGeneration(failedClient *rpcClient) {
	p.mu.Lock()
	if p.closed || p.shutdown || p.disabled || p.client != failedClient {
		p.mu.Unlock()
		return
	}
	closedClient, _ := p.closeProcessLocked()
	// A failed call means this process generation is no longer usable even if
	// it disappeared before cmd/process state was fully populated.
	p.recordFailureLocked()
	p.mu.Unlock()
	closedClient.waitIncomingRequests()
}

// Close terminates the extension process.
func (p *Process) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	p.shutdown = true
	closedClient, err := p.closeProcessLocked()
	p.mu.Unlock()
	closedClient.waitIncomingRequests()
	return err
}

func (p *Process) closeProcessLocked() (*rpcClient, error) {
	client := p.client
	if p.uiSource != nil {
		p.uiSource.cancelHostContext()
	}
	client.stopIncomingRequests()
	if p.closed {
		return client, nil
	}
	p.closed = true
	owner := UIExtensionOwner{}
	if p.uiSource != nil {
		owner = p.uiSource.owner
	}
	p.uiMu.RLock()
	uiHost := p.uiHost
	p.uiMu.RUnlock()
	if uiHost != nil && owner.Generation != 0 {
		uiHost.CleanupExtensionUI(owner)
	}
	if p.cmd == nil || p.cmd.Process == nil {
		return client, nil
	}
	forceKillErr := osutil.ForceKillProcessGroup(p.cmd)
	if forceKillErr != nil {
		if err := p.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return client, errors.Wrapf(forceKillErr, "failed to force kill extension process group; direct process kill also failed: %v", err)
		}
	}
	_ = p.stdin.Close()
	_ = p.stdout.Close()
	err := p.cmd.Wait()
	if forceKillErr != nil {
		return client, errors.Wrap(forceKillErr, "failed to force kill extension process group")
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return client, nil
	}
	if err != nil && strings.Contains(err.Error(), "process already finished") {
		return client, nil
	}
	return client, err
}
