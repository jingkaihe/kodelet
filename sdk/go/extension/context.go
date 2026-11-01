package extension

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jingkaihe/kodelet/sdk/go/internal/jsonrpc"
)

// callEnv binds a call to its runtime, host client, and initialize snapshot.
type callEnv struct {
	rt     *Runtime
	client *requestClient
	init   *InitializeParams
}

func (e *callEnv) capabilities() map[string]any {
	if e.init == nil {
		return nil
	}
	return e.init.Capabilities
}

func (e *callEnv) callContext(wire callContextWire) CallContext {
	cwd := wire.CWD
	if cwd == "" && e.init != nil {
		cwd = e.init.Extension.CWD
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	cwd, _ = filepath.Abs(cwd)
	extensionID := ""
	dataDir := ""
	runnerID := ""
	if e.init != nil {
		extensionID = e.init.Extension.ID
		dataDir = e.init.Extension.DataDir
		runnerID = e.init.Extension.RunnerID
	}
	if dataDir == "" {
		home, _ := os.UserHomeDir()
		id := extensionID
		if id == "" {
			id = "extension"
		}
		dataDir = filepath.Join(home, ".kodelet", "extensions", "data", id)
	}
	dataDir, _ = filepath.Abs(dataDir)
	scope := strings.TrimSpace(wire.UIScopeID)
	return CallContext{
		CallInfo: CallInfo{
			SessionID:      wire.SessionID,
			RunnerID:       runnerID,
			ConversationID: wire.ConversationID,
			UIScopeID:      scope,
			CWD:            cwd,
			Provider:       wire.Provider,
			Model:          wire.Model,
			Profile:        wire.Profile,
			RecipeName:     wire.RecipeName,
			InvokedBy:      wire.InvokedBy,
		},
		Storage: &Storage{DataDir: dataDir},
		Log:     e.rt.logger(extensionID),
		UI:      &UI{env: e, scopeID: scope},
		env:     e,
	}
}

// CallContext is shared by tool, command, event, and shortcut handlers.
type CallContext struct {
	CallInfo
	// Storage persists extension data under the host-provided data directory.
	Storage *Storage
	// Log writes structured JSON diagnostics to stderr; interactive hosts may
	// surface warn and error entries as notifications.
	Log *slog.Logger
	// UI requests user interactions and persistent widgets or surfaces.
	UI *UI

	env *callEnv
}

// Capabilities returns the host capabilities from initialization. Treat the
// result as read-only.
func (c *CallContext) Capabilities() map[string]any {
	return c.env.capabilities()
}

// Config returns the extension configuration from initialization. Treat the
// result as read-only.
func (c *CallContext) Config() map[string]any {
	if c.env.init == nil {
		return nil
	}
	return c.env.init.Extension.Config
}

// ResolveWorkspacePath resolves target against the workspace and rejects
// paths that escape it.
func (c *CallContext) ResolveWorkspacePath(target string) (string, error) {
	if target == "" {
		target = "."
	}
	resolved := target
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(c.CWD, resolved)
	}
	resolved = filepath.Clean(resolved)
	if !isPathInside(resolved, c.CWD) {
		return "", fmt.Errorf("path escapes workspace: %s", target)
	}
	return resolved, nil
}

// RelativeToWorkspace returns target relative to the workspace.
func (c *CallContext) RelativeToWorkspace(target string) string {
	resolved := target
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(c.CWD, resolved)
	}
	relative, err := filepath.Rel(c.CWD, resolved)
	if err != nil || relative == "" {
		return "."
	}
	return relative
}

// AcquireBackgroundTask keeps the extension process alive after the current
// call returns until the lease is closed. Hosts that already have a persistent
// lifetime return a lease without an ID whose Close is a no-op.
func (c *CallContext) AcquireBackgroundTask(ctx context.Context, description string) (*BackgroundTaskLease, error) {
	if !capabilityTrue(c.env.capabilities(), "runtime", "backgroundTasks") {
		return nil, errors.New("background extension tasks are not available in this host")
	}
	var params any
	if normalized := strings.TrimSpace(description); normalized != "" {
		params = map[string]any{"description": normalized}
	}
	raw, err := c.env.client.request(ctx, "kodelet.runtime.background.acquire", params)
	if err != nil {
		var rpcErr *RPCError
		if errors.As(err, &rpcErr) && rpcErr.Code == jsonrpc.CodeMethodNotFound {
			return &BackgroundTaskLease{}, nil
		}
		return nil, err
	}
	var response map[string]any
	if err := json.Unmarshal(raw, &response); err != nil || response == nil {
		return nil, errors.New("invalid background task lease response from Kodelet host")
	}
	value, present := response["leaseId"]
	if !present || value == nil {
		return &BackgroundTaskLease{}, nil
	}
	leaseID, ok := value.(string)
	if !ok || strings.TrimSpace(leaseID) == "" {
		return nil, errors.New("background task lease response did not include a lease ID")
	}
	leaseID = strings.TrimSpace(leaseID)
	client := c.env.client
	return &BackgroundTaskLease{
		ID: leaseID,
		release: func(ctx context.Context) error {
			_, err := client.requestPersistent(ctx, "kodelet.runtime.background.release", map[string]any{"leaseId": leaseID})
			return err
		},
	}, nil
}

// BackgroundTaskLease holds the host-owned process lifetime for background work.
type BackgroundTaskLease struct {
	// ID is the host lease ID, or empty when the host needs no lease.
	ID string

	mu      sync.Mutex
	closed  bool
	release func(context.Context) error
}

// Close releases the lease. Repeated calls are harmless and a failed release
// may be retried.
func (l *BackgroundTaskLease) Close(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.release == nil {
		l.closed = true
		return nil
	}
	if err := l.release(ctx); err != nil {
		return err
	}
	l.closed = true
	return nil
}

// ToolContext is passed to tool handlers.
type ToolContext struct {
	CallContext
}

// Update publishes a transient progress update for the running tool call. It
// is a no-op when the host does not support tool updates.
func (t *ToolContext) Update(ctx context.Context, content string, data map[string]any) error {
	capabilities := t.env.capabilities()
	if !capabilityTrue(capabilities, "toolUpdates") && !capabilityTrue(capabilities, "tools", "updates") {
		return nil
	}
	payload := map[string]any{"content": content}
	if data != nil {
		payload["data"] = data
	}
	_, err := t.env.client.request(ctx, "kodelet.tool.update", payload)
	return err
}

// ForkOptions configures ForkConversation.
type ForkOptions struct {
	// Name is the user-facing name of the fork; empty keeps the source title.
	Name string
	// AsChild makes the fork a child of the calling conversation.
	AsChild bool
}

// ForkConversation forks the live conversation and returns the new ID.
// Errors for unsupported or unavailable hosts match ErrConversationForkUnavailable.
func (t *ToolContext) ForkConversation(ctx context.Context, opts ForkOptions) (string, error) {
	capabilities := t.env.capabilities()
	if !capabilityTrue(capabilities, "conversations", "fork") {
		return "", &ForkUnavailableError{Message: "live conversation forking is not supported by this Kodelet host"}
	}
	if opts.AsChild && !capabilityTrue(capabilities, "conversations", "hierarchy") {
		return "", errors.New("child conversation forks require conversation hierarchy support; update Kodelet and the selected runner")
	}
	params := map[string]any{}
	if strings.TrimSpace(opts.Name) != "" {
		params["name"] = opts.Name
	}
	if opts.AsChild {
		params["asChild"] = true
	}
	var request any
	if len(params) > 0 {
		request = params
	}
	raw, err := t.env.client.request(ctx, "kodelet.conversation.fork", request)
	if err != nil {
		var rpcErr *RPCError
		if errors.As(err, &rpcErr) && rpcErr.Code == conversationForkUnavailableCode {
			return "", &ForkUnavailableError{Message: rpcErr.Message}
		}
		return "", err
	}
	var response struct {
		ConversationID *string `json:"conversationId"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return "", errors.New("invalid conversation fork response from Kodelet host")
	}
	if response.ConversationID == nil || strings.TrimSpace(*response.ConversationID) == "" {
		return "", errors.New("conversation fork response did not include a conversation ID")
	}
	return strings.TrimSpace(*response.ConversationID), nil
}

// BrowserConnection is raw CDP access to the conversation's shared browser,
// for trusted runner-local extensions, scoped to the active tool call.
type BrowserConnection struct {
	LeaseID   string
	SessionID string
	// CDPURL is a runner-local endpoint, not a remotely accessible capability.
	CDPURL string
	// PageTargetID is the exact page target shared with the user.
	PageTargetID string

	mu       sync.Mutex
	released bool
	inflight *releaseOperation
	release  func(context.Context) error
}

type releaseOperation struct {
	done chan struct{}
	err  error
}

// AcquireBrowser acquires the conversation's shared browser for this tool
// call. Inline SDK extensions cannot acquire runner-local endpoints.
func (t *ToolContext) AcquireBrowser(ctx context.Context) (*BrowserConnection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if capabilityVersion(t.env.capabilities(), "browser", "version") != 1 {
		return nil, errors.New("browser acquisition is not supported by this Kodelet host")
	}
	client := t.env.client
	raw, err := client.request(ctx, "kodelet.browser.acquire", map[string]any{})
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var response struct {
		LeaseID      string `json:"leaseId"`
		SessionID    string `json:"sessionId"`
		CDPURL       string `json:"cdpUrl"`
		PageTargetID string `json:"pageTargetId"`
	}
	if err := json.Unmarshal(raw, &response); err != nil ||
		strings.TrimSpace(response.LeaseID) == "" || strings.TrimSpace(response.SessionID) == "" ||
		strings.TrimSpace(response.CDPURL) == "" || strings.TrimSpace(response.PageTargetID) == "" {
		return nil, errors.New("invalid browser acquisition response from Kodelet host")
	}
	leaseID := response.LeaseID
	return &BrowserConnection{
		LeaseID:      response.LeaseID,
		SessionID:    response.SessionID,
		CDPURL:       response.CDPURL,
		PageTargetID: response.PageTargetID,
		release: func(ctx context.Context) error {
			_, err := client.request(ctx, "kodelet.browser.release", map[string]any{"leaseId": leaseID})
			return err
		},
	}, nil
}

// Release releases the lifetime lease, not the automation client or Chrome.
// Concurrent calls share one request; a failed release can be retried while
// the tool call is active. The host also releases leases when the call ends.
func (b *BrowserConnection) Release(ctx context.Context) error {
	b.mu.Lock()
	if b.released {
		b.mu.Unlock()
		return nil
	}
	if operation := b.inflight; operation != nil {
		b.mu.Unlock()
		select {
		case <-operation.done:
			return operation.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	operation := &releaseOperation{done: make(chan struct{})}
	b.inflight = operation
	b.mu.Unlock()
	err := b.release(ctx)
	b.mu.Lock()
	b.inflight = nil
	b.released = err == nil
	operation.err = err
	close(operation.done)
	b.mu.Unlock()
	return err
}

// CommandContext is passed to command handlers.
type CommandContext struct {
	CallContext
	Invocation CommandInvocation
}

// EventContext is passed to event handlers.
type EventContext struct {
	CallContext
}

// ShortcutContext is passed to shortcut handlers.
type ShortcutContext struct {
	CallContext
}

// Storage reads and writes files under the extension data directory.
type Storage struct {
	DataDir string
}

// Path resolves name inside the data directory and rejects escapes.
func (s *Storage) Path(name string) (string, error) {
	if name == "" {
		name = "."
	}
	resolved := name
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(s.DataDir, resolved)
	}
	resolved = filepath.Clean(resolved)
	if !isPathInside(resolved, s.DataDir) {
		return "", fmt.Errorf("path escapes extension storage: %s", name)
	}
	return resolved, nil
}

// ReadText reads a file; ok is false if it does not exist.
func (s *Storage) ReadText(name string) (content string, ok bool, err error) {
	path, err := s.Path(name)
	if err != nil {
		return "", false, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return string(data), true, nil
}

// WriteText writes a file, creating parent directories.
func (s *Storage) WriteText(name, content string) error {
	path, err := s.Path(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// ReadJSON decodes a JSON file into v; ok is false if it does not exist.
func (s *Storage) ReadJSON(name string, v any) (ok bool, err error) {
	content, ok, err := s.ReadText(name)
	if err != nil || !ok {
		return ok, err
	}
	return true, json.Unmarshal([]byte(content), v)
}

// WriteJSON writes v as indented JSON.
func (s *Storage) WriteJSON(name string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return s.WriteText(name, string(data)+"\n")
}

func isPathInside(target, parent string) bool {
	relative, err := filepath.Rel(parent, target)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative))
}

type lockedWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(p)
}

func newLogger(output io.Writer, extensionID string) *slog.Logger {
	handler := slog.NewJSONHandler(output, &slog.HandlerOptions{
		Level: slog.LevelDebug,
		ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
			if len(groups) > 0 {
				return attr
			}
			switch attr.Key {
			case slog.TimeKey:
				return slog.Attr{}
			case slog.MessageKey:
				attr.Key = "message"
			case slog.LevelKey:
				if level, ok := attr.Value.Any().(slog.Level); ok {
					attr.Value = slog.StringValue(strings.ToLower(level.String()))
				}
			}
			return attr
		},
	})
	logger := slog.New(handler)
	if extensionID != "" {
		logger = logger.With("extension", extensionID)
	}
	return logger
}
