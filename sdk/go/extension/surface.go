package extension

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
)

// SurfaceSize is the rendered size of a surface.
type SurfaceSize struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// SurfaceMouseEvent describes a mouse event.
type SurfaceMouseEvent struct {
	X      int    `json:"x"`
	Y      int    `json:"y"`
	Button string `json:"button"`
	Action string `json:"action"`
	Shift  bool   `json:"shift,omitempty"`
	Alt    bool   `json:"alt,omitempty"`
	Ctrl   bool   `json:"ctrl,omitempty"`
}

// SurfaceInputEvent is user input routed to a surface. Kind is "key",
// "mouse", "focus", or "blur".
type SurfaceInputEvent struct {
	Sequence int                `json:"sequence"`
	Kind     string             `json:"kind"`
	Key      string             `json:"key,omitempty"`
	Text     string             `json:"text,omitempty"`
	Alt      bool               `json:"alt,omitempty"`
	Shift    bool               `json:"shift,omitempty"`
	Ctrl     bool               `json:"ctrl,omitempty"`
	Mouse    *SurfaceMouseEvent `json:"mouse,omitempty"`
}

// SurfaceResizeEvent reports a new surface size.
type SurfaceResizeEvent struct {
	Sequence int `json:"sequence"`
	Width    int `json:"width"`
	Height   int `json:"height"`
}

// Surface is an open interactive surface. Event handlers run in order on the
// runtime's notification goroutine; long-running work there delays later events.
type Surface struct {
	id      string
	scopeID string
	rt      *Runtime

	mu                  sync.Mutex
	closed              bool
	active              bool
	openSequence        int
	closing             *closeOperation
	pending             []FrameLine
	hasPending          bool
	frameInFlight       bool
	latestEventSequence float64
	nextHandler         int
	inputHandlers       map[int]func(SurfaceInputEvent)
	pendingFocus        *SurfaceInputEvent
	resizeHandlers      map[int]func(SurfaceResizeEvent)
	pendingResize       *SurfaceResizeEvent
	closeHandlers       map[int]func()
	size                *SurfaceSize
}

type closeOperation struct {
	done chan struct{}
	err  error
}

func newSurface(id, scopeID string, rt *Runtime) *Surface {
	return &Surface{
		id:             id,
		scopeID:        scopeID,
		rt:             rt,
		inputHandlers:  map[int]func(SurfaceInputEvent){},
		resizeHandlers: map[int]func(SurfaceResizeEvent){},
		closeHandlers:  map[int]func(){},
	}
}

// ID returns the surface ID.
func (s *Surface) ID() string {
	return s.id
}

// Size returns the last reported size, if any.
func (s *Surface) Size() (SurfaceSize, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.size == nil {
		return SurfaceSize{}, false
	}
	return *s.size, true
}

func (s *Surface) key() string {
	return uiObjectKey(s.scopeID, s.id)
}

func (s *Surface) nextSequenceLocked() int {
	sequence := s.rt.nextSequence("surface", s.key())
	if s.openSequence == 0 {
		s.openSequence = sequence
	}
	return sequence
}

func (s *Surface) activate() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errSurfaceClosedWhileOpening
	}
	s.active = true
	return nil
}

// Update replaces the surface contents. Frames are coalesced: one is in
// flight until the transport has written it, and only the latest pending
// frame is sent next.
func (s *Surface) Update(lines []FrameLine) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.closing != nil {
		return
	}
	if lines == nil {
		lines = []FrameLine{}
	}
	s.pending = append([]FrameLine(nil), lines...)
	s.hasPending = true
	s.scheduleLocked()
}

func (s *Surface) scheduleLocked() {
	if s.closed || s.closing != nil || s.frameInFlight || !s.hasPending {
		return
	}
	s.frameInFlight = true
	go s.flush()
}

func (s *Surface) flush() {
	for {
		s.mu.Lock()
		if s.closed || s.closing != nil || !s.hasPending {
			s.frameInFlight = false
			s.mu.Unlock()
			return
		}
		lines := s.pending
		s.pending, s.hasPending = nil, false
		sequence := s.nextSequenceLocked()
		s.mu.Unlock()
		params, err := withUIScope(map[string]any{"id": s.id, "frame": map[string]any{"sequence": sequence, "lines": lines}}, s.scopeID)
		if err == nil {
			// The host connection may already be gone; runtime cleanup removes the surface.
			_ = s.rt.notify("kodelet.ui.surface.frame", params)
		}
	}
}

// Close closes the surface. A failed close keeps ownership and can be retried.
func (s *Surface) Close(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	if operation := s.closing; operation != nil {
		s.mu.Unlock()
		select {
		case <-operation.done:
			return operation.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	operation := &closeOperation{done: make(chan struct{})}
	s.closing = operation
	active := s.active
	sequence := 0
	if active {
		sequence = s.nextSequenceLocked()
	}
	s.mu.Unlock()

	var err error
	if active {
		var params map[string]any
		params, err = withUIScope(map[string]any{"id": s.id, "sequence": sequence}, s.scopeID)
		if err == nil {
			var raw json.RawMessage
			raw, err = s.rt.call(ctx, nil, "kodelet.ui.surface.close", params)
			if err == nil {
				err = rejection(raw, "the host rejected the surface close")
			}
		}
	}
	if err == nil {
		runAll(s.dispose())
	}
	s.mu.Lock()
	if s.closing == operation {
		s.closing = nil
	}
	operation.err = err
	close(operation.done)
	s.scheduleLocked()
	s.mu.Unlock()
	return err
}

// dispose marks the surface closed and returns its close handlers, which the
// caller runs outside the lock.
func (s *Surface) dispose() []func() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.pending, s.hasPending = nil, false
	s.inputHandlers = map[int]func(SurfaceInputEvent){}
	s.pendingFocus = nil
	s.resizeHandlers = map[int]func(SurfaceResizeEvent){}
	s.pendingResize = nil
	handlers := make([]func(), 0, len(s.closeHandlers))
	for id := 0; id < s.nextHandler; id++ {
		if handler, ok := s.closeHandlers[id]; ok {
			handlers = append(handlers, handler)
		}
	}
	s.closeHandlers = map[int]func(){}
	s.mu.Unlock()
	s.rt.unregisterSurface(s.key(), s)
	return handlers
}

func runAll(handlers []func()) {
	for _, handler := range handlers {
		handler()
	}
}

// OnInput registers an input handler and returns a function that removes it.
// A focus or blur event that arrived while opening is delivered immediately.
func (s *Surface) OnInput(handler func(SurfaceInputEvent)) func() {
	s.mu.Lock()
	id := s.nextHandler
	s.nextHandler++
	s.inputHandlers[id] = handler
	pending := s.pendingFocus
	s.pendingFocus = nil
	s.mu.Unlock()
	if pending != nil {
		handler(*pending)
	}
	return func() {
		s.mu.Lock()
		delete(s.inputHandlers, id)
		s.mu.Unlock()
	}
}

// OnResize registers a resize handler and returns a function that removes it.
// A resize that arrived while opening is delivered immediately.
func (s *Surface) OnResize(handler func(SurfaceResizeEvent)) func() {
	s.mu.Lock()
	id := s.nextHandler
	s.nextHandler++
	s.resizeHandlers[id] = handler
	pending := s.pendingResize
	s.pendingResize = nil
	s.mu.Unlock()
	if pending != nil {
		handler(*pending)
	}
	return func() {
		s.mu.Lock()
		delete(s.resizeHandlers, id)
		s.mu.Unlock()
	}
}

// OnClose registers a handler called once when the surface closes, or
// immediately if it is already closed. It returns a function that removes it.
func (s *Surface) OnClose(handler func()) func() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		handler()
		return func() {}
	}
	id := s.nextHandler
	s.nextHandler++
	s.closeHandlers[id] = handler
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.closeHandlers, id)
		s.mu.Unlock()
	}
}

type surfaceNotification struct {
	ID           string             `json:"id"`
	ScopeID      any                `json:"scopeId"`
	OpenSequence *float64           `json:"openSequence"`
	Sequence     *float64           `json:"sequence"`
	Kind         string             `json:"kind"`
	Key          string             `json:"key"`
	Text         string             `json:"text"`
	Alt          bool               `json:"alt"`
	Shift        bool               `json:"shift"`
	Ctrl         bool               `json:"ctrl"`
	Mouse        *SurfaceMouseEvent `json:"mouse"`
	Width        *float64           `json:"width"`
	Height       *float64           `json:"height"`
}

func normalizedUIScope(value any) string {
	scope, _ := value.(string)
	return strings.TrimSpace(scope)
}

// handleNotification applies a host event in arrival order and returns the
// user callbacks to run, so the read loop never blocks on handler code.
func (s *Surface) handleNotification(method string, n surfaceNotification) func() {
	s.mu.Lock()
	if method == "extension.ui.surface.closed" && n.ID == s.id && normalizedUIScope(n.ScopeID) == s.scopeID &&
		n.OpenSequence != nil && *n.OpenSequence == float64(s.openSequence) {
		s.mu.Unlock()
		handlers := s.dispose()
		return func() { runAll(handlers) }
	}
	defer s.mu.Unlock()
	if s.closed || n.ID != s.id || normalizedUIScope(n.ScopeID) != s.scopeID || n.Sequence == nil || *n.Sequence <= s.latestEventSequence {
		return nil
	}
	s.latestEventSequence = *n.Sequence
	switch method {
	case "extension.ui.surface.input":
		if n.Kind != "key" && n.Kind != "mouse" && n.Kind != "focus" && n.Kind != "blur" {
			return nil
		}
		event := SurfaceInputEvent{
			Sequence: int(*n.Sequence), Kind: n.Kind, Key: n.Key, Text: n.Text,
			Alt: n.Alt, Shift: n.Shift, Ctrl: n.Ctrl, Mouse: n.Mouse,
		}
		if len(s.inputHandlers) == 0 {
			if !s.active && (n.Kind == "focus" || n.Kind == "blur") {
				s.pendingFocus = &event
			}
			return nil
		}
		handlers := orderedHandlers(s.inputHandlers, s.nextHandler)
		return func() {
			for _, handler := range handlers {
				handler(event)
			}
		}
	case "extension.ui.surface.resize":
		if n.Width == nil || n.Height == nil {
			return nil
		}
		event := SurfaceResizeEvent{Sequence: int(*n.Sequence), Width: int(*n.Width), Height: int(*n.Height)}
		s.size = &SurfaceSize{Width: event.Width, Height: event.Height}
		if len(s.resizeHandlers) == 0 {
			s.pendingResize = nil
			if !s.active {
				s.pendingResize = &event
			}
			return nil
		}
		handlers := orderedHandlers(s.resizeHandlers, s.nextHandler)
		return func() {
			for _, handler := range handlers {
				handler(event)
			}
		}
	default:
		return nil
	}
}

func orderedHandlers[T any](handlers map[int]func(T), limit int) []func(T) {
	ordered := make([]func(T), 0, len(handlers))
	for id := 0; id < limit; id++ {
		if handler, ok := handlers[id]; ok {
			ordered = append(ordered, handler)
		}
	}
	return ordered
}
