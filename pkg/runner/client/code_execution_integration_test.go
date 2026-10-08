package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// codeWire records actual JSON-RPC frames in both directions, not just mocks of
// the tool API. The relay has no access to the runner's child execution results.
type codeWire struct {
	mu     sync.Mutex
	frames [][]byte
}

func (w *codeWire) relay(source, destination *websocket.Conn) {
	defer source.Close()
	defer destination.Close()
	for {
		kind, body, err := source.ReadMessage()
		if err != nil {
			return
		}
		w.mu.Lock()
		w.frames = append(w.frames, body)
		w.mu.Unlock()
		if err := destination.WriteMessage(kind, body); err != nil {
			return
		}
	}
}

func (w *codeWire) snapshot() [][]byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([][]byte(nil), w.frames...)
}

type codeLoopback struct {
	registry *runnerregistry.Registry
	service  *Service
	peer     *protocol.Peer
	identity runnerregistry.UIRequestIdentity
	manifest runnerpayload.Manifest
	wire     *codeWire
}

func newCodeLoopback(t *testing.T) *codeLoopback {
	t.Helper()
	workspace := t.TempDir()
	registry, err := runnerregistry.New(t.Context(), runnerregistry.Options{
		HeartbeatInterval: time.Hour, HeartbeatTimeout: 2 * time.Hour,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, registry.Close()) })
	wire := &codeWire{}
	upgrader := websocket.Upgrader{Subprotocols: []string{protocol.Subprotocol}}
	dialer := websocket.Dialer{Subprotocols: []string{protocol.Subprotocol}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		if r.URL.Path == "/relay" {
			central, response, err := dialer.DialContext(r.Context(), "ws://"+r.Host+"/central", nil)
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}
			if err != nil {
				_ = conn.Close()
				return
			}
			done := make(chan struct{})
			go func() { defer close(done); wire.relay(conn, central) }()
			wire.relay(central, conn)
			<-done
			return
		}
		session := runnerregistry.NewSession(registry, nil)
		peer, err := protocol.NewPeer(conn, protocol.PeerConfig{
			RequestPrefix: "central", Handler: session, Notifications: session,
		})
		if err != nil {
			_ = conn.Close()
			return
		}
		session.Attach(peer)
		if err := peer.Start(r.Context()); err != nil {
			_ = peer.Close()
			return
		}
		<-peer.Done()
		session.Detach(peer.Err())
	}))
	t.Cleanup(server.Close)
	runtime := extensions.EmptyRuntime()
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	service := newRegisteredTestService(t, workspace, ServiceOptions{
		RuntimeProvider: staticRuntimeProvider{runtime: runtime},
		ConfigLoader: func(string) (llmtypes.Config, error) {
			return llmtypes.Config{CodeMode: "only", AllowedTools: []string{"code_execute", "file_read", "web_fetch"}}, nil
		},
	})
	conn, response, err := dialer.DialContext(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http")+"/relay", nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	require.NoError(t, err)
	peer, err := protocol.NewPeer(conn, protocol.PeerConfig{
		RequestPrefix: "runner", Handler: service, Notifications: service,
	})
	require.NoError(t, err)
	service.Attach(peer)
	require.NoError(t, peer.Start(t.Context()))
	t.Cleanup(func() { require.NoError(t, peer.Close()) })
	var registration protocol.RegisterResult
	require.NoError(t, peer.Call(t.Context(), protocol.MethodRunnerRegister, protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version},
		Host:             protocol.Host{InstanceID: "code-host", Hostname: "runner", OS: "linux", Arch: "amd64"},
		Workspace:        protocol.Workspace{Path: workspace, Name: "code workspace"},
	}, &registration))
	require.NoError(t, service.SetRegistration(registration))
	require.NoError(t, peer.Notify(t.Context(), protocol.MethodRunnerHeartbeat, protocol.HeartbeatParams{
		RunnerID: registration.RunnerID, Generation: registration.Generation, State: protocol.RunnerStateIdle,
	}))
	require.Eventually(t, func() bool {
		runner, ok := registry.Runner(registration.RunnerID)
		return ok && runner.Status == runnerregistry.RunnerStatusIdle
	}, time.Second, time.Millisecond)
	manifest, err := registry.OpenRun(t.Context(), registration.RunnerID, protocol.RunOpenParams{
		RunID: "code-run", ConversationID: "code-conversation", CodeExecution: true,
	})
	require.NoError(t, err)
	runner, ok := registry.Runner(registration.RunnerID)
	require.True(t, ok)
	return &codeLoopback{
		registry: registry, service: service, peer: peer, manifest: manifest, wire: wire,
		identity: runnerregistry.UIRequestIdentity{RunnerID: runner.ID, Generation: runner.Generation, ConnectionID: runner.ConnectionID},
	}
}

func TestRunnerCodeLoopbackVMAndChildHelper(t *testing.T) {
	loop := newCodeLoopback(t)
	const secret = "runner-local-intermediate-never-on-the-wire"
	filePath := filepath.Join(loop.manifest.WorkingDirectory, "private.txt")
	require.NoError(t, os.WriteFile(filePath, []byte(secret), 0o600))
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<p>document for extraction</p>"))
	}))
	t.Cleanup(page.Close)
	var helperCalls atomic.Int32
	ctx := tooltypes.ContextWithModelHelper(t.Context(), func(_ context.Context, request tooltypes.ModelHelperRequest) (string, error) {
		helperCalls.Add(1)
		assert.Equal(t, page.URL, request.URL)
		assert.Contains(t, request.Content, "document for extraction")
		return "extracted selection", nil
	})
	webInput := mustJSON(t, map[string]string{"url": page.URL, "prompt": "extract"})
	direct, err := loop.registry.ExecuteTool(ctx, runnerpayload.ToolExecuteParams{
		RunID: "code-run", ToolCallID: "direct", Name: "web_fetch", Input: webInput,
	}, nil)
	require.NoError(t, err)
	require.True(t, direct.Result.Structured.Success)
	assert.Contains(t, direct.Result.AssistantFacing, "extracted selection")
	code := `
const [page, matches, description, file, web] = await Promise.all([
  catalog.list(), catalog.search("file read"), catalog.describe("file_read"),
  tools.file_read(` + string(mustJSON(t, map[string]string{"file_path": filePath})) + `),
  tools.web_fetch(` + string(webInput) + `)
]);
return {names: page.tools.map(t => t.name), found: matches.tools[0].name,
  schema: description.inputSchema.type, lines: file.data.lines.length,
  dataAvailable: file.data !== null, extracted: web.text.includes("extracted selection")};`
	var updateMu sync.Mutex
	var updates []runnerpayload.ToolUpdateParams
	result, err := loop.registry.ExecuteTool(ctx, runnerpayload.ToolExecuteParams{
		RunID: "code-run", ToolCallID: "parent", Name: "code_execute",
		Input:          mustJSON(t, map[string]string{"code": code}),
		ManifestDigest: loop.manifest.Digest, CallableTools: new([]string{"file_read", "web_fetch"}),
	}, func(update runnerpayload.ToolUpdateParams) {
		updateMu.Lock()
		defer updateMu.Unlock()
		updates = append(updates, update)
	})
	require.NoError(t, err)
	require.True(t, result.Result.Structured.Success, result.Result.AssistantFacing)
	metadata, ok := result.Result.Structured.Metadata.(tooltypes.CodeExecutionMetadata)
	require.True(t, ok)
	require.Len(t, metadata.Items, 1)
	require.Len(t, metadata.Calls, 2)
	assert.JSONEq(t, `{"names":["file_read","web_fetch"],"found":"file_read","schema":"object","lines":1,"dataAvailable":true,"extracted":true}`, string(metadata.Items[0].Value))
	assert.EqualValues(t, 2, helperCalls.Load(), "direct and nested web_fetch both use the central helper")
	updateMu.Lock()
	for _, update := range updates {
		assert.Equal(t, "parent", update.ToolCallID)
		assert.Equal(t, "code_execute", update.Result.Structured.ToolName)
	}
	updateMu.Unlock()
	var begins, ends, helpers int
	for _, frame := range loop.wire.snapshot() {
		assert.NotContains(t, string(frame), secret, "unselected file contents must never cross the transport")
		var message protocol.Message
		require.NoError(t, json.Unmarshal(frame, &message))
		switch message.Method {
		case protocol.MethodToolChildBegin, protocol.MethodToolChildEnd:
			var ownership map[string]any
			require.NoError(t, json.Unmarshal(message.Params, &ownership))
			assert.Len(t, ownership, 4, "ownership RPC contains IDs and name only")
			assert.Equal(t, "parent", ownership["parentToolCallId"])
			if message.Method == protocol.MethodToolChildBegin {
				begins++
			} else {
				ends++
			}
		case runnerpayload.MethodModelHelperExecute:
			helpers++
		case protocol.MethodToolUpdate:
			var update runnerpayload.ToolUpdateParams
			require.NoError(t, json.Unmarshal(message.Params, &update))
			assert.Equal(t, "parent", update.ToolCallID)
		}
	}
	assert.Equal(t, 2, begins)
	assert.Equal(t, begins, ends)
	assert.Equal(t, 2, helpers)
	for _, child := range metadata.Calls {
		_, _, err := loop.registry.ArtifactToolContext(loop.identity, "code-run", child.CallID)
		require.Error(t, err, "completed child authority must be revoked")
		var ignored runnerpayload.ModelHelperResult
		err = loop.peer.Call(t.Context(), runnerpayload.MethodModelHelperExecute, runnerpayload.ModelHelperParams{
			RunID: "code-run", ToolCallID: child.CallID,
			Request: tooltypes.ModelHelperRequest{Operation: tooltypes.ModelHelperWebFetchExtract, URL: page.URL, Prompt: "extract"},
		}, &ignored)
		require.Error(t, err, "helper must not be usable after child completion")
	}
	result, err = loop.registry.ExecuteTool(t.Context(), runnerpayload.ToolExecuteParams{
		RunID: "code-run", ToolCallID: "empty", Name: "code_execute", ManifestDigest: loop.manifest.Digest,
		CallableTools: new([]string{}), Input: json.RawMessage(`{"code":"return await catalog.list()"}`),
	}, nil)
	require.NoError(t, err)
	require.True(t, result.Result.Structured.Success)
	empty := result.Result.Structured.Metadata.(tooltypes.CodeExecutionMetadata)
	assert.Empty(t, empty.Calls)
	require.Len(t, empty.Items, 1)
	assert.JSONEq(t, `{"tools":[]}`, string(empty.Items[0].Value))
}

func TestRunnerCodeLoopbackCancellationAndCleanup(t *testing.T) {
	for _, mode := range []string{"parent", "run", "disconnect", "close"} {
		t.Run(mode, func(t *testing.T) {
			loop := newCodeLoopback(t)
			page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				_, _ = w.Write([]byte("<p>blocked extraction</p>"))
			}))
			t.Cleanup(page.Close)
			started, stopped := make(chan struct{}), make(chan struct{})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			ctx = tooltypes.ContextWithModelHelper(ctx, func(ctx context.Context, _ tooltypes.ModelHelperRequest) (string, error) {
				close(started)
				defer close(stopped)
				<-ctx.Done()
				return "", ctx.Err()
			})
			loop.service.mu.Lock()
			run := loop.service.runs["code-run"]
			loop.service.mu.Unlock()
			done := make(chan struct{})
			input := mustJSON(t, map[string]string{"code": `return await tools.web_fetch(` + string(mustJSON(t, map[string]string{"url": page.URL, "prompt": "extract"})) + `)`})
			go func() {
				defer close(done)
				_, _ = loop.registry.ExecuteTool(ctx, runnerpayload.ToolExecuteParams{
					RunID: "code-run", ToolCallID: "parent", Name: "code_execute", Input: input,
					ManifestDigest: loop.manifest.Digest, CallableTools: new([]string{"web_fetch"}),
				}, nil)
			}()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("nested model helper did not start")
			}
			var child runnerpayload.ToolChildParams
			for _, frame := range loop.wire.snapshot() {
				var message protocol.Message
				require.NoError(t, json.Unmarshal(frame, &message))
				if message.Method == protocol.MethodToolChildBegin {
					require.NoError(t, json.Unmarshal(message.Params, &child))
				}
			}
			require.NotEmpty(t, child.ToolCallID)
			childCtx, _, err := loop.registry.ArtifactToolContext(loop.identity, "code-run", child.ToolCallID)
			require.NoError(t, err, "acknowledged child gets independent artifact authority")
			switch mode {
			case "parent":
				cancel()
			case "run":
				require.NoError(t, loop.registry.CancelRun(t.Context(), "code-run", "cancel test"))
			case "disconnect":
				require.NoError(t, loop.peer.Close())
			case "close":
				require.NoError(t, loop.registry.CloseRun(t.Context(), "code-run", runnerregistry.RunStatusCanceled, nil))
			}
			for _, signal := range []<-chan struct{}{done, stopped, childCtx.Done()} {
				select {
				case <-signal:
				case <-time.After(5 * time.Second):
					t.Fatal("code parent/child cleanup did not finish")
				}
			}
			drained := make(chan struct{})
			go func() { run.ops.Wait(); close(drained) }()
			select {
			case <-drained:
			case <-time.After(5 * time.Second):
				t.Fatal("nested run operations leaked after cancellation")
			}
			_, _, err = loop.registry.ArtifactToolContext(loop.identity, "code-run", child.ToolCallID)
			require.Error(t, err)
		})
	}
}
