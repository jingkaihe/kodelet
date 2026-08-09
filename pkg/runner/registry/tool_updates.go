package registry

import (
	"strings"
	"sync"

	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	"github.com/pkg/errors"
)

type updateSink func(runnerpayload.ToolUpdateParams)

type toolUpdateSubscription struct {
	sink      updateSink
	requestID string
	sequence  uint64
}

// toolUpdateRouter owns transient tool-stream correlation independently from
// runner identity and durable run state. Its callbacks are invoked without its
// mutex held.
type toolUpdateRouter struct {
	mu            sync.Mutex
	subscriptions map[string]*toolUpdateSubscription
}

func newToolUpdateRouter() *toolUpdateRouter {
	return &toolUpdateRouter{subscriptions: make(map[string]*toolUpdateSubscription)}
}

func (r *toolUpdateRouter) subscribe(runID, toolCallID string, sink updateSink) (func(), error) {
	if r == nil || sink == nil {
		return func() {}, nil
	}
	key := toolUpdateKey(runID, toolCallID)
	r.mu.Lock()
	if r.subscriptions[key] != nil {
		r.mu.Unlock()
		return nil, errors.New("tool call id is already active for this run")
	}
	subscription := &toolUpdateSubscription{sink: sink}
	r.subscriptions[key] = subscription
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		if r.subscriptions[key] == subscription {
			delete(r.subscriptions, key)
		}
		r.mu.Unlock()
	}, nil
}

func (r *toolUpdateRouter) setRequestID(runID, toolCallID, requestID string) {
	if r == nil {
		return
	}
	key := toolUpdateKey(runID, toolCallID)
	r.mu.Lock()
	if subscription := r.subscriptions[key]; subscription != nil {
		subscription.requestID = requestID
	}
	r.mu.Unlock()
}

func (r *toolUpdateRouter) deliver(params runnerpayload.ToolUpdateParams) error {
	if r == nil {
		return errors.New("tool update router is unavailable")
	}
	key := toolUpdateKey(params.RunID, params.ToolCallID)
	r.mu.Lock()
	subscription := r.subscriptions[key]
	if subscription == nil {
		r.mu.Unlock()
		return errors.New("tool update has no active subscriber")
	}
	if params.RequestID == "" || params.RequestID != subscription.requestID {
		r.mu.Unlock()
		return errors.New("tool update request id is stale")
	}
	if params.Sequence == 0 || params.Sequence <= subscription.sequence {
		r.mu.Unlock()
		return errors.New("tool update sequence is stale")
	}
	subscription.sequence = params.Sequence
	sink := subscription.sink
	r.mu.Unlock()
	sink(params)
	return nil
}

func (r *toolUpdateRouter) clearRun(runID string) {
	if r == nil {
		return
	}
	prefix := strings.TrimSpace(runID) + "\x00"
	r.mu.Lock()
	for key := range r.subscriptions {
		if strings.HasPrefix(key, prefix) {
			delete(r.subscriptions, key)
		}
	}
	r.mu.Unlock()
}

func toolUpdateKey(runID, toolCallID string) string {
	return strings.TrimSpace(runID) + "\x00" + strings.TrimSpace(toolCallID)
}
