package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/jingkaihe/kodelet/sdk/go/extension"
)

var (
	errInvalidFrame        = errors.New("invalid or unsupported inline extension frame")
	errInlineRuntimeClosed = errors.New("inline extension runtime is closed")
)

// relay hosts inline extension runtimes for frames relayed by kodelet acp.
// Each runner run and extension gets its own runtime, created by the first
// extension.initialize frame and never restarted or replayed.
type relay struct {
	conn       *acpConn
	extensions map[string]*extension.Extension
	ids        []string
	ui         *UIHandlers

	mu       sync.Mutex
	closed   bool
	runtimes map[runtimeKey]*inlineRuntime

	closeOnce sync.Once
	closeDone chan struct{}
}

type runtimeKey struct {
	runID       string
	extensionID string
}

type inlineRuntime struct {
	mu      sync.Mutex
	runtime *extension.Runtime
	closed  bool
	once    sync.Once
	done    chan struct{}
}

func newRelay(conn *acpConn, extensions []*extension.Extension, ui *UIHandlers) *relay {
	r := &relay{
		conn:       conn,
		extensions: map[string]*extension.Extension{},
		ui:         ui,
		runtimes:   map[runtimeKey]*inlineRuntime{},
		closeDone:  make(chan struct{}),
	}
	for i, ext := range extensions {
		id := fmt.Sprintf("inline-%d", i+1)
		r.extensions[id] = ext
		r.ids = append(r.ids, id)
	}
	return r
}

type extensionFrame struct {
	SessionID   *string         `json:"sessionId"`
	RunID       *string         `json:"runId"`
	ExtensionID *string         `json:"extensionId"`
	Message     json.RawMessage `json:"message"`
	Close       *bool           `json:"close"`
}

// accept validates a frame and returns the dispatch to run after the frame is
// acknowledged.
func (r *relay) accept(params json.RawMessage) (func(), error) {
	r.conn.mu.Lock()
	supported := r.conn.sessionExtensions
	r.conn.mu.Unlock()
	var frame extensionFrame
	if !supported || json.Unmarshal(params, &frame) != nil ||
		frame.SessionID == nil || *frame.SessionID == "" ||
		frame.RunID == nil || *frame.RunID == "" ||
		frame.ExtensionID == nil || r.extensions[*frame.ExtensionID] == nil {
		return nil, errInvalidFrame
	}
	closing := frame.Close != nil && *frame.Close
	if closing && frame.Message != nil {
		return nil, errInvalidFrame
	}
	var message map[string]json.RawMessage
	if !closing && !validExtensionMessage(frame.Message, &message) {
		return nil, errInvalidFrame
	}

	r.conn.mu.Lock()
	if r.conn.sessionID != "" && r.conn.sessionID != *frame.SessionID {
		r.conn.mu.Unlock()
		return nil, errors.New("inline extension frame belongs to a different ACP session")
	}
	r.conn.sessionID = *frame.SessionID
	r.conn.mu.Unlock()

	key := runtimeKey{runID: *frame.RunID, extensionID: *frame.ExtensionID}
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.runtimes[key]
	if closing {
		if state == nil {
			return nil, errors.New("unknown inline extension runtime")
		}
		state.mu.Lock()
		state.closed = true
		state.mu.Unlock()
		return func() { go r.closeRuntime(state, nil) }, nil
	}
	if r.closed {
		return nil, errInlineRuntimeClosed
	}
	if state != nil && state.isClosed() {
		return nil, errors.New("inline extension runtime is closed; callbacks cannot be replayed")
	}
	method := rawString(message["method"])
	initializing := method == "extension.initialize"
	if initializing && !hasExtensionIdentity(message["params"]) {
		return nil, errors.New("inline extension initialize requires an extension identity")
	}
	if state == nil {
		if !initializing || !hasID(message["id"]) {
			return nil, errors.New("an inline extension runtime must start with extension.initialize")
		}
		state = r.newRuntime(*frame.SessionID, key)
		r.runtimes[key] = state
	} else if initializing {
		return nil, errors.New("inline extension runtime cannot be initialized again")
	}
	payload := []byte(frame.Message)
	if initializing && r.ui != nil {
		payload = r.withLocalUICapabilities(message)
	}
	return func() {
		state.mu.Lock()
		runtime, closed := state.runtime, state.closed
		state.mu.Unlock()
		if closed || runtime == nil {
			return
		}
		_ = runtime.Receive(payload)
	}, nil
}

func (r *relay) newRuntime(sessionID string, key runtimeKey) *inlineRuntime {
	state := &inlineRuntime{done: make(chan struct{})}
	transport := extension.TransportFunc(func(ctx context.Context, message []byte) error {
		if state.isClosed() {
			return errInlineRuntimeClosed
		}
		_, err := r.conn.request(ctx, extensionFrameMethod, map[string]any{
			"sessionId":   sessionID,
			"runId":       key.runID,
			"extensionId": key.extensionID,
			"message":     json.RawMessage(message),
		})
		if err != nil {
			go r.closeRuntime(state, err)
		}
		return err
	})
	state.runtime = extension.NewRuntime(r.extensions[key.extensionID], transport, &extension.RuntimeOptions{LocalRequest: r.localRequest})
	return state
}

func (s *inlineRuntime) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (r *relay) closeRuntime(state *inlineRuntime, cause error) {
	state.once.Do(func() {
		state.mu.Lock()
		state.closed = true
		runtime := state.runtime
		state.mu.Unlock()
		if runtime != nil {
			runtime.Close(cause)
		}
		// Retain only a tombstone, not every completed run's callbacks.
		state.mu.Lock()
		state.runtime = nil
		state.mu.Unlock()
		close(state.done)
	})
	<-state.done
}

// closeAll closes every inline runtime and rejects later frames. Concurrent
// callers wait for the same cleanup.
func (r *relay) closeAll(cause error) {
	r.closeOnce.Do(func() {
		r.mu.Lock()
		r.closed = true
		states := make([]*inlineRuntime, 0, len(r.runtimes))
		for _, state := range r.runtimes {
			states = append(states, state)
		}
		r.mu.Unlock()
		var wg sync.WaitGroup
		for _, state := range states {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r.closeRuntime(state, cause)
			}()
		}
		wg.Wait()
		close(r.closeDone)
	})
	<-r.closeDone
}

func (r *relay) withLocalUICapabilities(message map[string]json.RawMessage) []byte {
	var params map[string]any
	_ = json.Unmarshal(message["params"], &params)
	capabilities, _ := params["capabilities"].(map[string]any)
	if capabilities == nil {
		capabilities = map[string]any{}
	}
	ui, _ := capabilities["ui"].(map[string]any)
	if ui == nil {
		ui = map[string]any{}
	}
	ui["input"] = r.ui.Input != nil
	ui["confirm"] = r.ui.Confirm != nil
	ui["select"] = r.ui.Select != nil
	ui["notify"] = r.ui.Notify != nil
	capabilities["ui"] = ui
	params["capabilities"] = capabilities
	encoded, _ := json.Marshal(params)
	rewritten := make(map[string]json.RawMessage, len(message))
	for key, value := range message {
		rewritten[key] = value
	}
	rewritten["params"] = encoded
	data, _ := json.Marshal(rewritten)
	return data
}

// localRequest serves basic UI requests with the session's UI handlers.
func (r *relay) localRequest(ctx context.Context, method string, params json.RawMessage) (any, bool, error) {
	if r.ui == nil {
		return nil, false, nil
	}
	unavailable := map[string]any{"status": "unavailable"}
	switch method {
	case "kodelet.ui.input":
		if r.ui.Input == nil {
			return unavailable, true, nil
		}
		var request extension.InputRequest
		_ = json.Unmarshal(params, &request)
		value, ok, err := r.ui.Input(ctx, request)
		return textResponse(value, ok), true, err
	case "kodelet.ui.confirm":
		if r.ui.Confirm == nil {
			return unavailable, true, nil
		}
		var request extension.ConfirmRequest
		_ = json.Unmarshal(params, &request)
		confirmed, err := r.ui.Confirm(ctx, request)
		return map[string]any{"status": "submitted", "confirmed": confirmed}, true, err
	case "kodelet.ui.select":
		if r.ui.Select == nil {
			return unavailable, true, nil
		}
		var request extension.SelectRequest
		_ = json.Unmarshal(params, &request)
		value, ok, err := r.ui.Select(ctx, request)
		return textResponse(value, ok), true, err
	case "kodelet.ui.notify":
		if r.ui.Notify == nil {
			return unavailable, true, nil
		}
		var request extension.NotifyRequest
		_ = json.Unmarshal(params, &request)
		return map[string]any{"status": "submitted"}, true, r.ui.Notify(ctx, request)
	default:
		return nil, false, nil
	}
}

func textResponse(value string, ok bool) map[string]any {
	if !ok {
		return map[string]any{"status": "dismissed"}
	}
	return map[string]any{"status": "submitted", "value": value}
}

func validExtensionMessage(raw json.RawMessage, message *map[string]json.RawMessage) bool {
	if json.Unmarshal(raw, message) != nil || *message == nil {
		return false
	}
	fields := *message
	if rawString(fields["jsonrpc"]) != "2.0" {
		return false
	}
	if id, present := fields["id"]; present && string(id) != "null" {
		var value any
		if json.Unmarshal(id, &value) != nil {
			return false
		}
		switch value.(type) {
		case float64, string:
		default:
			return false
		}
	}
	_, hasResult := fields["result"]
	_, hasError := fields["error"]
	if methodRaw, present := fields["method"]; present {
		var method string
		return json.Unmarshal(methodRaw, &method) == nil && method != "" && !hasResult && !hasError
	}
	return hasID(fields["id"]) && hasResult != hasError
}

func rawString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}

func hasID(raw json.RawMessage) bool {
	return len(raw) > 0 && string(raw) != "null"
}

func hasExtensionIdentity(raw json.RawMessage) bool {
	var params struct {
		Extension *struct {
			ID any `json:"id"`
		} `json:"extension"`
	}
	if json.Unmarshal(raw, &params) != nil || params.Extension == nil {
		return false
	}
	id, ok := params.Extension.ID.(string)
	return ok && id != ""
}
