package extensions

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"sync"

	"github.com/jingkaihe/kodelet/pkg/browser"
)

const (
	BrowserAcquireMethod = "kodelet.browser.acquire"
	BrowserReleaseMethod = "kodelet.browser.release"
	maxBrowserToolLeases = 8
)

// BrowserAcquirer is installed only for an authorized runner tool invocation.
// The host, never extension-supplied parameters, selects the conversation scope.
type BrowserAcquirer func(context.Context) (browser.Connection, func(), error)

type browserAcquirerContextKey struct{}

// ContextWithBrowserAcquirer grants access to the current conversation browser.
func ContextWithBrowserAcquirer(ctx context.Context, acquire BrowserAcquirer) context.Context {
	return context.WithValue(ctx, browserAcquirerContextKey{}, acquire)
}

// BrowserAcquirerFromContext returns the invocation's explicitly granted access.
func BrowserAcquirerFromContext(ctx context.Context) BrowserAcquirer {
	acquire, _ := ctx.Value(browserAcquirerContextKey{}).(BrowserAcquirer)
	return acquire
}

type browserAcquireResult struct {
	LeaseID string `json:"leaseId"`
	browser.Connection
}

// browserToolCall owns direct-CDP lifetime leases, not the extension's sockets.
// All leases end with the tool invocation, including failed or canceled calls.
type browserToolCall struct {
	ctx     context.Context
	cancel  context.CancelFunc
	acquire BrowserAcquirer
	mu      sync.Mutex
	leases  map[string]func()
}

func newBrowserToolCall(ctx context.Context, acquire BrowserAcquirer) *browserToolCall {
	ctx, cancel := context.WithCancel(ctx)
	return &browserToolCall{ctx: ctx, cancel: cancel, acquire: acquire, leases: make(map[string]func())}
}

func (b *browserToolCall) close() {
	b.cancel()
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, release := range b.leases {
		if release != nil {
			release()
			b.leases[id] = nil
		}
	}
}

func (b *browserToolCall) request(method string, params json.RawMessage) (any, *rpcError) {
	if b == nil || b.acquire == nil {
		return nil, &rpcError{Code: -32000, Message: "browser acquisition requires an authorized runner-local extension tool"}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.ctx.Err(); err != nil {
		return nil, &rpcError{Code: -32000, Message: "browser tool invocation has ended"}
	}
	if method == BrowserReleaseMethod {
		var input struct {
			LeaseID string `json:"leaseId"`
		}
		if err := json.Unmarshal(params, &input); err != nil || input.LeaseID == "" {
			return nil, &rpcError{Code: -32602, Message: "browser release requires a leaseId"}
		}
		release, ok := b.leases[input.LeaseID]
		if !ok {
			return nil, &rpcError{Code: -32602, Message: "browser lease does not belong to this invocation"}
		}
		if release != nil {
			release()
			b.leases[input.LeaseID] = nil
		}
		return map[string]bool{"released": true}, nil
	}
	var input map[string]json.RawMessage
	if len(params) != 0 {
		if err := json.Unmarshal(params, &input); err != nil || len(input) != 0 {
			return nil, &rpcError{Code: -32602, Message: "browser acquire accepts no parameters"}
		}
	}
	// Keep released entries to make release idempotent and bound acquisition churn.
	if len(b.leases) >= maxBrowserToolLeases {
		return nil, &rpcError{Code: -32000, Message: "browser acquisition limit reached for this tool invocation"}
	}
	connection, release, err := b.acquire(b.ctx)
	if err != nil {
		return nil, &rpcError{Code: -32000, Message: err.Error()}
	}
	if b.ctx.Err() != nil {
		release()
		return nil, &rpcError{Code: -32000, Message: "browser tool invocation has ended"}
	}
	id := rand.Text()
	b.leases[id] = release
	return browserAcquireResult{LeaseID: id, Connection: connection}, nil
}

type invalidBrowserParentHandler struct{}

func (invalidBrowserParentHandler) HandleRPCRequest(_ context.Context, _ string, _ json.RawMessage) (any, *rpcError) {
	return nil, &rpcError{Code: -32602, Message: "browser acquisition and release require a valid tool parentId"}
}
