package agent

import (
	"encoding/json"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/sdk/go/internal/jsonrpc"
	"github.com/stretchr/testify/require"
)

// fakeProcess connects the client to an in-memory ACP server.
type fakeProcess struct {
	stdinReader  *io.PipeReader
	stdinWriter  *io.PipeWriter
	stdoutReader *io.PipeReader
	stdoutWriter *io.PipeWriter
	done         chan struct{}
	once         sync.Once
	exit         processExit
	terminated   atomic.Int32
	killed       atomic.Int32
	ignoreTerm   bool
}

func newFakeProcess() *fakeProcess {
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	return &fakeProcess{
		stdinReader: stdinReader, stdinWriter: stdinWriter,
		stdoutReader: stdoutReader, stdoutWriter: stdoutWriter,
		done: make(chan struct{}),
	}
}

func (p *fakeProcess) Write(data []byte) (int, error) { return p.stdinWriter.Write(data) }
func (p *fakeProcess) Stdout() io.ReadCloser          { return p.stdoutReader }
func (p *fakeProcess) Done() <-chan struct{}          { return p.done }
func (p *fakeProcess) Exit() processExit              { return p.exit }

func (p *fakeProcess) Terminate() error {
	p.terminated.Add(1)
	if !p.ignoreTerm {
		p.finish(processExit{Code: -1, Signal: "SIGTERM"})
	}
	return nil
}

func (p *fakeProcess) Kill() error {
	p.killed.Add(1)
	p.finish(processExit{Code: -1, Signal: "SIGKILL"})
	return nil
}

func (p *fakeProcess) finish(exit processExit) {
	p.once.Do(func() {
		p.exit = exit
		_ = p.stdoutWriter.Close()
		_ = p.stdinReader.Close()
		close(p.done)
	})
}

// fakeACP is a scriptable kodelet acp server.
type fakeACP struct {
	t       *testing.T
	proc    *fakeProcess
	command string
	args    []string
	env     []string

	writeMu sync.Mutex
	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan jsonrpc.Message

	meta      map[string]any
	sessionID string
	requests  chan jsonrpc.Message
	frames    chan json.RawMessage
	prompt    func(f *fakeACP, message jsonrpc.Message)
	handle    func(f *fakeACP, message jsonrpc.Message) bool
}

func newFakeACP(t *testing.T) *fakeACP {
	return &fakeACP{
		t:         t,
		proc:      newFakeProcess(),
		pending:   map[int64]chan jsonrpc.Message{},
		sessionID: "conv-1",
		meta: map[string]any{
			"steering":              map[string]any{"supported": true},
			"sessionExtensions":     map[string]any{"version": 1},
			"conversationHierarchy": map[string]any{"version": 1},
		},
		requests: make(chan jsonrpc.Message, 64),
		frames:   make(chan json.RawMessage, 64),
	}
}

// client returns a Client whose sessions connect to this fake.
func (f *fakeACP) client(opts ClientOptions) *Client {
	client := NewClient(opts)
	client.start = func(command string, args, env []string) (acpProcess, error) {
		f.command, f.args, f.env = command, args, env
		go f.serve()
		return f.proc, nil
	}
	f.t.Cleanup(func() { _ = client.Close() })
	return client
}

func (f *fakeACP) serve() {
	reader := jsonrpc.NewLineReader(f.proc.stdinReader, acpMessageLimit)
	for {
		line, err := reader.Read()
		if err != nil {
			return
		}
		var message jsonrpc.Message
		if json.Unmarshal(line, &message) != nil {
			continue
		}
		if message.Method == "" {
			id, _ := message.IntID()
			f.mu.Lock()
			ch := f.pending[id]
			delete(f.pending, id)
			f.mu.Unlock()
			if ch != nil {
				ch <- message
			}
			continue
		}
		// Run the hook before publishing so waiters see its recorded state.
		handled := f.handle != nil && f.handle(f, message)
		f.requests <- message
		if handled {
			continue
		}
		f.dispatch(message)
	}
}

func (f *fakeACP) dispatch(message jsonrpc.Message) {
	switch message.Method {
	case "initialize":
		f.respond(message.ID, map[string]any{"protocolVersion": 1, "_meta": f.meta})
	case "session/new":
		f.respond(message.ID, map[string]any{"sessionId": f.sessionID})
	case "session/load":
		f.respond(message.ID, map[string]any{})
	case "session/prompt":
		if f.prompt != nil {
			go f.prompt(f, message)
			return
		}
		f.respond(message.ID, map[string]any{"stopReason": "end_turn"})
	case "_session/steering":
		f.respond(message.ID, map[string]any{"outcome": "injected"})
	case extensionFrameMethod:
		var frame struct {
			Message json.RawMessage `json:"message"`
		}
		_ = json.Unmarshal(message.Params, &frame)
		f.frames <- frame.Message
		f.respond(message.ID, map[string]any{})
	}
}

func (f *fakeACP) write(message jsonrpc.Message) {
	data, err := json.Marshal(message)
	require.NoError(f.t, err)
	f.writeMu.Lock()
	defer f.writeMu.Unlock()
	_, _ = f.proc.stdoutWriter.Write(append(data, '\n'))
}

func (f *fakeACP) writeLine(line string) {
	f.writeMu.Lock()
	defer f.writeMu.Unlock()
	_, _ = f.proc.stdoutWriter.Write([]byte(line + "\n"))
}

func (f *fakeACP) respond(id json.RawMessage, result any) {
	encoded, err := json.Marshal(result)
	require.NoError(f.t, err)
	f.write(jsonrpc.NewResult(id, encoded))
}

func (f *fakeACP) respondError(id json.RawMessage, code int, message string) {
	f.write(jsonrpc.NewError(id, code, message))
}

func (f *fakeACP) update(update map[string]any) {
	params, err := json.Marshal(map[string]any{"sessionId": f.sessionID, "update": update})
	require.NoError(f.t, err)
	f.write(jsonrpc.NewNotification("session/update", params))
}

// call sends a server-to-client request and waits for the response.
func (f *fakeACP) call(method string, params any) jsonrpc.Message {
	f.t.Helper()
	encoded, err := json.Marshal(params)
	require.NoError(f.t, err)
	f.mu.Lock()
	f.nextID++
	id := f.nextID
	ch := make(chan jsonrpc.Message, 1)
	f.pending[id] = ch
	f.mu.Unlock()
	f.write(jsonrpc.NewRequest(jsonrpc.IntRaw(id), method, encoded))
	select {
	case message := <-ch:
		return message
	case <-time.After(5 * time.Second):
		f.t.Fatalf("timed out waiting for %s response", method)
		return jsonrpc.Message{}
	}
}

// frame delivers an inline extension message and returns the ACK.
func (f *fakeACP) frame(runID string, message string) jsonrpc.Message {
	f.t.Helper()
	return f.call(extensionFrameMethod, map[string]any{
		"sessionId": f.sessionID, "runId": runID, "extensionId": "inline-1", "message": json.RawMessage(message),
	})
}

// nextFrame returns the next extension message relayed by the client.
func (f *fakeACP) nextFrame() jsonrpc.Message {
	f.t.Helper()
	select {
	case raw := <-f.frames:
		var message jsonrpc.Message
		require.NoError(f.t, json.Unmarshal(raw, &message))
		return message
	case <-time.After(5 * time.Second):
		f.t.Fatal("timed out waiting for an extension frame")
		return jsonrpc.Message{}
	}
}

// nextRequest returns the next client request or notification with method.
func (f *fakeACP) nextRequest(method string) jsonrpc.Message {
	f.t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case message := <-f.requests:
			if message.Method == method {
				return message
			}
		case <-timeout:
			f.t.Fatalf("timed out waiting for %s", method)
			return jsonrpc.Message{}
		}
	}
}

// session starts a session from a default client.
func (f *fakeACP) session() *Session {
	f.t.Helper()
	session, err := f.client(ClientOptions{}).CreateSession(f.t.Context(), SessionOptions{})
	require.NoError(f.t, err)
	return session
}

// chunk streams assistant text.
func (f *fakeACP) chunk(text string) {
	f.update(map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": text}})
}

// toolCall reports that the agent called a tool.
func (f *fakeACP) toolCall(id, name string, input map[string]any) {
	f.update(map[string]any{"sessionUpdate": "tool_call", "toolCallId": id, "toolName": name, "rawInput": input})
}

// respondAfterCancel is a prompt handler that ends the turn once it is cancelled.
func respondAfterCancel(f *fakeACP, message jsonrpc.Message) {
	f.nextRequest("session/cancel")
	f.respond(message.ID, map[string]any{"stopReason": "cancelled"})
}

// busyOnToolCall subscribes a callback that spends d handling the first
// tool.call event. entered closes when it starts; finished reports its end.
func busyOnToolCall(session *Session, d time.Duration) (entered <-chan struct{}, finished *atomic.Bool) {
	started := make(chan struct{})
	done := &atomic.Bool{}
	var once sync.Once
	session.Subscribe(func(event Event) {
		if event.Type != EventToolCall {
			return
		}
		once.Do(func() {
			close(started)
			time.Sleep(d)
			done.Store(true)
		})
	})
	return started, done
}

func waitFor(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
	}
}
