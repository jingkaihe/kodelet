package agent

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jingkaihe/kodelet/sdk/go/internal/queue"
)

const (
	contextCompactedMetadataKey = "kodelet/contextCompacted"
	// cancelGrace bounds how long a cancelled run waits for the turn to settle.
	cancelGrace = 5 * time.Second
)

// Session is a conversation backed by one kodelet acp process. Runs are
// sequential; Steer may be called while a run is in progress.
type Session struct {
	client *Client
	conn   *acpConn
	id     string
	cwd    string

	mu              sync.Mutex
	closed          bool
	running         bool
	seenCompactions map[string]struct{}
	subscribers     map[int]func(Event)
	nextSubscriber  int

	events    *queue.Worker
	closeOnce sync.Once
	closeErr  error
}

type runState struct {
	mu        sync.Mutex
	events    []Event
	chunks    strings.Builder
	thinking  bool
	toolNames map[string]string
	listener  func(Event)
}

func newSession(client *Client, conn *acpConn, id, cwd string) *Session {
	return &Session{
		client:          client,
		conn:            conn,
		id:              id,
		cwd:             cwd,
		seenCompactions: map[string]struct{}{},
		subscribers:     map[int]func(Event){},
		events:          queue.NewWorker(),
	}
}

// ID returns the Kodelet conversation ID.
func (s *Session) ID() string {
	return s.id
}

// CWD returns the runner working directory.
func (s *Session) CWD() string {
	return s.cwd
}

// Subscribe registers a handler for every event of every run and returns a
// function that removes it. Handlers run in order on a dedicated goroutine;
// RunAndWait returns only after handlers have seen the run's events.
func (s *Session) Subscribe(handler func(Event)) func() {
	s.mu.Lock()
	id := s.nextSubscriber
	s.nextSubscriber++
	s.subscribers[id] = handler
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.subscribers, id)
		s.mu.Unlock()
	}
}

// RunAndWait sends a message and waits for the turn to finish. If ctx is
// cancelled, it asks Kodelet to cancel the turn, waits briefly for it to
// settle, and returns the context error.
func (s *Session) RunAndWait(ctx context.Context, opts RunOptions) (*Response, error) {
	return s.run(ctx, opts, nil)
}

// Stream sends a message and yields its events as they arrive. A non-nil
// error is yielded last if the run fails. Breaking out of the loop cancels
// the run.
func (s *Session) Stream(ctx context.Context, opts RunOptions) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		events := make(chan Event, 64)
		done := make(chan error, 1)
		go func() {
			_, err := s.run(ctx, opts, func(event Event) { events <- event })
			close(events)
			done <- err
		}()
		for event := range events {
			if !yield(event, nil) {
				cancel()
				for range events {
				}
				<-done
				return
			}
		}
		if err := <-done; err != nil {
			yield(Event{}, err)
		}
	}
}

func (s *Session) run(ctx context.Context, opts RunOptions, listener func(Event)) (*Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, errors.New("cannot run a closed Kodelet session")
	}
	if s.running {
		s.mu.Unlock()
		return nil, errors.New("cannot run a Kodelet session while another run is in progress")
	}
	s.running = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	state := &runState{toolNames: map[string]string{}, listener: listener}
	unsubscribe := s.conn.onNotification(func(method string, params json.RawMessage) {
		if method == "session/update" {
			s.handleUpdate(state, params)
		}
	})
	defer unsubscribe()
	s.emit(state, EventAgentStart, AgentStart{Message: opts.Message}, nil)
	s.emit(state, EventUserMessage, UserMessage{Content: opts.Message}, nil)

	type outcome struct {
		stopReason string
		err        error
	}
	settled := make(chan outcome, 1)
	go func() {
		stopReason, err := s.conn.prompt(context.Background(), s.id, promptBlocks(opts))
		settled <- outcome{stopReason, err}
	}()
	var result outcome
	select {
	case result = <-settled:
	case <-ctx.Done():
		s.conn.cancel(s.id)
		select {
		case <-settled:
		case <-time.After(cancelGrace):
		}
		unsubscribe()
		s.emit(state, EventAgentError, AgentError{Message: ctx.Err().Error()}, nil)
		s.flushEvents()
		return nil, ctx.Err()
	}
	unsubscribe()
	if result.err != nil {
		s.emit(state, EventAgentError, AgentError{Message: result.err.Error()}, nil)
		s.flushEvents()
		return nil, result.err
	}
	state.mu.Lock()
	thinking := state.thinking
	state.thinking = false
	content := state.chunks.String()
	state.mu.Unlock()
	if thinking {
		s.emit(state, EventThinkingEnd, nil, nil)
	}
	s.emit(state, EventContentEnd, nil, nil)
	if content != "" {
		s.emit(state, EventAssistantMessage, AssistantMessage{Content: content}, nil)
	}
	state.mu.Lock()
	response := &Response{
		Content:        content,
		ConversationID: s.id,
		Events:         append([]Event(nil), state.events...),
		StopReason:     result.stopReason,
	}
	state.mu.Unlock()
	s.emit(state, EventAgentEnd, *response, nil)
	state.mu.Lock()
	response.Events = append([]Event(nil), state.events...)
	state.mu.Unlock()
	s.flushEvents()
	return response, nil
}

// Steer sends guidance to the running turn.
func (s *Session) Steer(ctx context.Context, message string) (SteerResult, error) {
	s.mu.Lock()
	closed, running := s.closed, s.running
	s.mu.Unlock()
	if closed {
		return SteerResult{}, errors.New("cannot steer a closed Kodelet session")
	}
	if !running {
		return SteerResult{}, errors.New("cannot steer a Kodelet session without an active run")
	}
	normalized := strings.TrimSpace(message)
	if normalized == "" {
		return SteerResult{}, errors.New("steering message must be a non-empty string")
	}
	return s.conn.steer(ctx, s.id, normalized)
}

// Close stops the kodelet acp process and any inline extension callbacks.
func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		s.closeErr = s.conn.close()
		s.events.Stop()
		s.client.forget(s)
	})
	return s.closeErr
}

func (s *Session) emit(state *runState, eventType string, data any, raw json.RawMessage) {
	event := Event{Type: eventType, Data: data, ConversationID: s.id, Raw: raw}
	state.mu.Lock()
	if result, ok := data.(ToolResult); ok && eventType == EventToolUpdate && result.ToolCallID != "" {
		for i, existing := range state.events {
			if previous, ok := existing.Data.(ToolResult); ok && existing.Type == EventToolUpdate && previous.ToolCallID == result.ToolCallID {
				state.events = append(state.events[:i], state.events[i+1:]...)
				break
			}
		}
	}
	state.events = append(state.events, event)
	listener := state.listener
	state.mu.Unlock()
	s.events.Submit(func() {
		s.mu.Lock()
		subscribers := make([]func(Event), 0, len(s.subscribers))
		for id := 0; id < s.nextSubscriber; id++ {
			if subscriber, ok := s.subscribers[id]; ok {
				subscribers = append(subscribers, subscriber)
			}
		}
		s.mu.Unlock()
		for _, subscriber := range subscribers {
			subscriber(event)
		}
		if listener != nil {
			listener(event)
		}
	})
}

// flushEvents waits until subscribers have received every emitted event.
func (s *Session) flushEvents() {
	done := make(chan struct{})
	if s.events.Submit(func() { close(done) }) {
		select {
		case <-done:
		case <-s.events.Done():
		}
	}
}

type acpUpdate struct {
	SessionUpdate string                     `json:"sessionUpdate"`
	Content       json.RawMessage            `json:"content"`
	ToolCallID    string                     `json:"toolCallId"`
	ToolName      string                     `json:"toolName"`
	Status        string                     `json:"status"`
	RawInput      json.RawMessage            `json:"rawInput"`
	Meta          map[string]json.RawMessage `json:"_meta"`
}

func (s *Session) handleUpdate(state *runState, params json.RawMessage) {
	var envelope struct {
		SessionID string          `json:"sessionId"`
		Update    json.RawMessage `json:"update"`
	}
	if json.Unmarshal(params, &envelope) != nil || envelope.SessionID != s.id {
		return
	}
	var update acpUpdate
	if json.Unmarshal(envelope.Update, &update) != nil {
		return
	}
	raw := envelope.Update
	switch update.SessionUpdate {
	case "agent_message_chunk":
		if compaction, ok := compactionFromUpdate(update); ok {
			s.mu.Lock()
			_, seen := s.seenCompactions[compaction.Compaction.ID]
			s.seenCompactions[compaction.Compaction.ID] = struct{}{}
			s.mu.Unlock()
			if !seen {
				s.emit(state, EventContextCompacted, compaction, raw)
			}
			return
		}
		if text := textFromContent(update.Content); text != "" {
			state.mu.Lock()
			state.chunks.WriteString(text)
			state.mu.Unlock()
			s.emit(state, EventAssistantMessageDelta, AssistantMessageDelta{DeltaContent: text}, raw)
		}
	case "agent_thought_chunk":
		state.mu.Lock()
		started := !state.thinking
		state.thinking = true
		state.mu.Unlock()
		if started {
			s.emit(state, EventThinkingStart, nil, raw)
		}
		if text := textFromContent(update.Content); text != "" {
			s.emit(state, EventThinkingDelta, ThinkingDelta{DeltaContent: text}, raw)
		}
	case "tool_call":
		name := strings.TrimSpace(update.ToolName)
		if name != "" {
			name = update.ToolName
		}
		if update.ToolCallID != "" && name != "" {
			state.mu.Lock()
			state.toolNames[update.ToolCallID] = name
			state.mu.Unlock()
		}
		var input any
		_ = json.Unmarshal(update.RawInput, &input)
		rawInput, isString := input.(string)
		if !isString {
			encoded, _ := json.Marshal(input)
			rawInput = string(encoded)
		}
		s.emit(state, EventToolCall, ToolCall{ToolName: name, Input: input, RawInput: rawInput, ToolCallID: update.ToolCallID}, raw)
	case "tool_call_update":
		state.mu.Lock()
		name := state.toolNames[update.ToolCallID]
		state.mu.Unlock()
		if name == "" && strings.TrimSpace(update.ToolName) != "" {
			name = update.ToolName
		}
		result := ToolResult{ToolName: name, Result: toolContentText(update.Content), ToolCallID: update.ToolCallID, Status: update.Status}
		switch {
		case update.Status == "in_progress" && update.Content != nil:
			s.emit(state, EventToolUpdate, result, raw)
		case update.Status == "in_progress":
		case update.Status == "completed" || update.Status == "failed":
			s.emit(state, EventToolResult, result, raw)
		default:
			s.emit(state, EventOther, decodeObject(raw), raw)
		}
	default:
		s.emit(state, EventOther, decodeObject(raw), raw)
	}
}

func decodeObject(raw json.RawMessage) map[string]any {
	var value map[string]any
	_ = json.Unmarshal(raw, &value)
	return value
}

func compactionFromUpdate(update acpUpdate) (ContextCompacted, bool) {
	data, ok := update.Meta[contextCompactedMetadataKey]
	if !ok {
		return ContextCompacted{}, false
	}
	var payload struct {
		Compaction *struct {
			ID        *string `json:"id"`
			Method    *string `json:"method"`
			Summary   *string `json:"summary"`
			CreatedAt *string `json:"createdAt"`
		} `json:"compaction"`
		BeforeCurrentUser bool `json:"beforeCurrentUser"`
	}
	if json.Unmarshal(data, &payload) != nil || payload.Compaction == nil {
		return ContextCompacted{}, false
	}
	marker := payload.Compaction
	if marker.ID == nil || *marker.ID == "" || marker.Method == nil || (*marker.Method != "api" && *marker.Method != "summary") || marker.CreatedAt == nil {
		return ContextCompacted{}, false
	}
	compaction := ContextCompacted{
		Compaction:        CompactionMarker{ID: *marker.ID, Method: *marker.Method, CreatedAt: *marker.CreatedAt},
		BeforeCurrentUser: payload.BeforeCurrentUser,
	}
	if marker.Summary != nil {
		compaction.Compaction.Summary = *marker.Summary
	}
	return compaction, true
}

func textFromContent(raw json.RawMessage) string {
	var content struct {
		Text     *string `json:"text"`
		Resource *struct {
			Text *string `json:"text"`
		} `json:"resource"`
	}
	if json.Unmarshal(raw, &content) != nil {
		return ""
	}
	if content.Text != nil {
		return *content.Text
	}
	if content.Resource != nil && content.Resource.Text != nil {
		return *content.Resource.Text
	}
	return ""
}

func toolContentText(raw json.RawMessage) string {
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return ""
	}
	var parts []string
	for _, item := range items {
		var entry struct {
			Type    string          `json:"type"`
			Content json.RawMessage `json:"content"`
			Path    *string         `json:"path"`
			NewText *string         `json:"newText"`
		}
		if json.Unmarshal(item, &entry) != nil {
			continue
		}
		if entry.Type == "content" && entry.Content != nil {
			if text := textFromContent(entry.Content); text != "" {
				parts = append(parts, text)
			}
			continue
		}
		if entry.Path != nil {
			parts = append(parts, *entry.Path)
		}
		if entry.NewText != nil {
			parts = append(parts, *entry.NewText)
		}
	}
	return strings.Join(parts, "\n")
}

var dataURLPattern = regexp.MustCompile(`^data:([^;,]+);base64,(.*)$`)

func promptBlocks(opts RunOptions) []map[string]any {
	blocks := []map[string]any{{"type": "text", "text": opts.Message}}
	for _, image := range opts.Images {
		if match := dataURLPattern.FindStringSubmatch(image); match != nil {
			blocks = append(blocks, map[string]any{"type": "image", "mimeType": match[1], "data": match[2]})
			continue
		}
		blocks = append(blocks, map[string]any{"type": "image", "uri": image})
	}
	return blocks
}
