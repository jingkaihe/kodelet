package controlplane

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	runnerclient "github.com/jingkaihe/kodelet/pkg/runner/client"
	"github.com/jingkaihe/kodelet/pkg/runner/localstate"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	"github.com/jingkaihe/kodelet/pkg/runner/registry"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A real stdio process, including reverse RPC and retained background work.
// Reads are multiplexed so capability/closed notifications can interleave with
// a prompt response, exactly as they do with an SDK extension.
func TestNativeUIReleaseExtensionProcess(t *testing.T) {
	if os.Getenv("KODELET_NATIVE_UI_RELEASE_HELPER") != "1" {
		return
	}
	var output, state sync.Mutex
	var nextID, widgetSequence atomic.Uint64
	var workspace, scope string
	pending := make(map[string]chan nativeReleaseMessage)
	write := func(value any) {
		data, _ := json.Marshal(value)
		output.Lock()
		_, _ = fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(data), data)
		output.Unlock()
	}
	call := func(parent json.RawMessage, method string, params any) nativeReleaseMessage {
		id := nextID.Add(1) + 1000
		key, _ := json.Marshal(id)
		ch := make(chan nativeReleaseMessage, 1)
		state.Lock()
		pending[string(key)] = ch
		state.Unlock()
		write(map[string]any{"jsonrpc": "2.0", "id": id, "parentId": parent, "method": method, "params": params})
		return <-ch
	}
	log := func(method string, value any) {
		state.Lock()
		defer state.Unlock()
		data, _ := json.Marshal(map[string]any{"method": method, "value": value})
		file, err := os.OpenFile(filepath.Join(workspace, "native-events.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err == nil {
			_, _ = fmt.Fprintln(file, string(data))
			_ = file.Close()
		}
	}
	handle := func(request nativeReleaseMessage) {
		var result any = map[string]any{}
		state.Lock()
		cwd, conversation := workspace, scope
		state.Unlock()
		switch request.Method {
		case "extension.initialize":
			var params struct {
				Extension struct{ CWD string } `json:"extension"`
			}
			_ = json.Unmarshal(request.Params, &params)
			state.Lock()
			workspace = params.Extension.CWD
			state.Unlock()
			result = extensions.InitializeResult{Name: "native-release", Version: "1", Tools: []extensions.ToolRegistration{{Name: "native_release", Description: "Exercise real UI transport", InputSchema: map[string]any{"type": "object"}}}, Subscriptions: []extensions.Subscription{{Event: extensions.EventSessionStart}}}
		case "extension.event.handle":
			var params struct {
				Event   string                          `json:"event"`
				Context extensions.ExtensionCallContext `json:"context"`
			}
			_ = json.Unmarshal(request.Params, &params)
			if params.Event == extensions.EventSessionStart {
				state.Lock()
				scope = params.Context.ConversationID
				state.Unlock()
				response := call(request.ID, extensions.BackgroundTaskAcquireMethod, extensions.BackgroundTaskAcquireRequest{Description: "UI release gate worker"})
				log("lease", response)
				_ = os.WriteFile(filepath.Join(cwd, "native-worker.pid"), []byte(strconv.Itoa(os.Getpid())), 0o600)
			}
		case "extension.tool.execute":
			var params struct {
				Input struct {
					Method  string          `json:"method"`
					Request json.RawMessage `json:"request"`
				} `json:"input"`
			}
			_ = json.Unmarshal(request.Params, &params)
			response := call(request.ID, params.Input.Method, params.Input.Request)
			result = extensions.ToolExecutionResult{Content: string(response.Result), Error: string(response.Error)}
		case "extension.ui.surface.closed":
			log(request.Method, request.Params)
			// No creating tool/session request remains. A background widget may
			// still update, but a surface must not recover transparently.
			response := call(nil, "kodelet.ui.widget.set", extensions.UIWidgetSetRequest{ScopeID: conversation, ID: "worker", Frame: extensions.UIFrame{Sequence: widgetSequence.Add(1)}})
			log("background-widget", response)
			response = call(nil, "kodelet.ui.surface.open", extensions.UISurfaceOpenRequest{ScopeID: conversation, ID: "late", Frame: extensions.UIFrame{Sequence: 1}})
			log("closed-open", response)
		case "kodelet.ui.capabilities", extensions.UISurfaceInputMethod, extensions.UISurfaceResizeMethod:
			log(request.Method, request.Params)
		}
		if len(request.ID) > 0 {
			write(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		}
	}
	reader := bufio.NewReader(os.Stdin)
	for {
		request, err := readNativeReleaseMessage(reader)
		if err != nil {
			os.Exit(0)
		}
		if request.Method != "" {
			go handle(request)
			continue
		}
		state.Lock()
		ch := pending[string(request.ID)]
		delete(pending, string(request.ID))
		state.Unlock()
		if ch != nil {
			ch <- request
		}
	}
}

type nativeReleaseMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}

func readNativeReleaseMessage(reader *bufio.Reader) (nativeReleaseMessage, error) {
	var message nativeReleaseMessage
	length := 0
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return message, err
		}
		if strings.TrimSpace(line) == "" {
			break
		}
		if value, found := strings.CutPrefix(line, "Content-Length:"); found {
			length, err = strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return message, err
			}
		}
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(reader, data); err != nil {
		return message, err
	}
	err := json.Unmarshal(data, &message)
	return message, err
}

type nativeReleasePrompt struct {
	request extensions.UIInputRequest
	answer  chan extensions.UIInputResponse
}

type nativeReleasePrompts chan nativeReleasePrompt

func (p nativeReleasePrompts) Input(ctx context.Context, request extensions.UIInputRequest) (extensions.UIInputResponse, error) {
	answer := make(chan extensions.UIInputResponse, 1)
	select {
	case p <- nativeReleasePrompt{request, answer}:
	case <-ctx.Done():
		return extensions.UIInputResponse{}, ctx.Err()
	}
	select {
	case response := <-answer:
		return response, nil
	case <-ctx.Done():
		return extensions.UIInputResponse{}, ctx.Err()
	}
}

func (p nativeReleasePrompts) Confirm(ctx context.Context, request extensions.UIConfirmRequest) (extensions.UIInputResponse, error) {
	return p.Input(ctx, extensions.UIInputRequest{ID: request.ID, Title: request.Title})
}

func (p nativeReleasePrompts) Select(ctx context.Context, request extensions.UISelectRequest) (extensions.UIInputResponse, error) {
	return p.Input(ctx, extensions.UIInputRequest{ID: request.ID, Title: request.Title})
}

func (nativeReleasePrompts) Notify(context.Context, extensions.UINotifyRequest) (extensions.UIInputResponse, error) {
	return extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted}, nil
}

// Headless native host exercises the production ChatRunner adapter. The actual
// terminal renderer is covered separately by TestRemoteNativeUIUsesExistingRendererAndFencesCleanup.
type nativeReleaseHost struct {
	*webExtensionUIHost
	mu         sync.Mutex
	surfaces   map[extensions.UIExtensionOwner]extensions.UIExtensionSource
	opened     chan extensions.UIExtensionSource
	frames     chan extensions.UISurfaceFrameRequest
	transcript chan extensions.UITranscriptAppendRequest
}

func (h *nativeReleaseHost) ExtensionUIHostCapabilities(context.Context) extensions.ExtensionUIHostCapabilities {
	return extensions.ExtensionUIHostCapabilities{Widgets: true, Surfaces: true, Transcript: true}
}

func (h *nativeReleaseHost) OpenSurface(_ context.Context, source extensions.UIExtensionSource, request extensions.UISurfaceOpenRequest) (extensions.UIFrameResponse, error) {
	h.mu.Lock()
	h.surfaces[source.ExtensionUIOwner()] = source
	h.mu.Unlock()
	h.opened <- source
	return extensions.UIFrameResponse{Accepted: true, LatestSequence: request.Frame.Sequence}, nil
}

func (h *nativeReleaseHost) UpdateSurface(_ context.Context, _ extensions.UIExtensionSource, request extensions.UISurfaceFrameRequest) (extensions.UIFrameResponse, error) {
	h.frames <- request
	return extensions.UIFrameResponse{Accepted: true, LatestSequence: request.Frame.Sequence}, nil
}

func (h *nativeReleaseHost) CloseSurface(_ context.Context, source extensions.UIExtensionSource, request extensions.UISurfaceCloseRequest) (extensions.UIFrameResponse, error) {
	h.mu.Lock()
	delete(h.surfaces, source.ExtensionUIOwner())
	h.mu.Unlock()
	return extensions.UIFrameResponse{Accepted: true, LatestSequence: request.Sequence}, nil
}

func (h *nativeReleaseHost) CleanupExtensionUI(owner extensions.UIExtensionOwner) {
	h.mu.Lock()
	delete(h.surfaces, owner)
	h.mu.Unlock()
	h.webExtensionUIHost.CleanupExtensionUI(owner)
}

func (h *nativeReleaseHost) AppendTranscript(_ context.Context, _ extensions.UIExtensionSource, request extensions.UITranscriptAppendRequest) (extensions.UITranscriptAppendResponse, error) {
	h.transcript <- request
	return extensions.UITranscriptAppendResponse{Accepted: true}, nil
}

func nativeReleaseNext[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		require.FailNow(t, "native UI transport did not make bounded progress")
		var zero T
		return zero
	}
}

func nativeReleasePost(t *testing.T, endpoint, clientID, path string, body any) int {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint+"/api/conversations/native-release/"+path, bytes.NewReader(data))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer web-secret")
	request.Header.Set(chat.ClientIDHeader, clientID)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	return response.StatusCode
}

func nativeReleaseBrowser(t *testing.T, endpoint, message string) <-chan chat.ChatEvent {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	method, path := http.MethodGet, "/api/conversations/native-release/stream"
	var body io.Reader
	if message != "" {
		method, path = http.MethodPost, "/api/chat"
		data, err := json.Marshal(chat.ChatRequest{ConversationID: "native-release", Message: message, ClientCapabilities: &chat.ChatClientCapabilities{InteractiveUI: true, PersistentWidgets: true}})
		require.NoError(t, err)
		body = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint+path, body)
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer web-secret")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(chat.ClientIDHeader, "browser")
	request.Header.Set(chat.UICapabilitiesHeader, "interactive,widgets")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode)
	events := make(chan chat.ChatEvent, 64)
	go func() {
		defer response.Body.Close()
		decoder := json.NewDecoder(response.Body)
		for {
			var event chat.ChatEvent
			if err := decoder.Decode(&event); err != nil {
				return
			}
			select {
			case events <- event:
			case <-ctx.Done():
				return
			}
		}
	}()
	t.Cleanup(cancel)
	return events
}

func TestNativeUIReleaseRunnerProcess(t *testing.T) {
	endpoint := os.Getenv("KODELET_NATIVE_UI_RUNNER_SERVER")
	if endpoint == "" {
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	loader, err := runnerclient.NewWorkspaceConfigLoader(map[string]any{"extensions": map[string]any{"enabled": true}, "skills": map[string]any{"enabled": false}})
	require.NoError(t, err)
	store, err := localstate.NewStoreAt(os.Getenv("KODELET_NATIVE_UI_RUNNER_STORE"))
	require.NoError(t, err)
	runner, err := runnerclient.NewRunner(ctx, runnerclient.RunnerConfig{Server: endpoint, Workspace: os.Getenv("KODELET_NATIVE_UI_RUNNER_WORKSPACE"), Store: store, ServiceOptions: runnerclient.ServiceOptions{WorkspaceConfigLoader: loader}})
	require.NoError(t, err)
	require.NoError(t, runner.Run(ctx))
}

func startNativeReleaseRunnerProcess(t *testing.T, server *Server, endpoint, workspace string) string {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	command := exec.Command(executable, "-test.run", "^TestNativeUIReleaseRunnerProcess$")
	command.Env = append(os.Environ(), "KODELET_NATIVE_UI_RUNNER_SERVER="+endpoint, "KODELET_NATIVE_UI_RUNNER_WORKSPACE="+workspace, "KODELET_NATIVE_UI_RUNNER_STORE="+t.TempDir())
	output, err := os.Create(filepath.Join(t.TempDir(), "runner.log"))
	require.NoError(t, err)
	command.Stdout, command.Stderr = output, output
	require.NoError(t, command.Start())
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	t.Cleanup(func() {
		_ = command.Process.Signal(os.Interrupt)
		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(7 * time.Second):
			_ = command.Process.Kill()
			<-done
			assert.Fail(t, "standalone release runner did not stop")
		}
		assert.NoError(t, output.Close())
	})
	var id string
	require.Eventually(t, func() bool {
		for _, runner := range server.runnerRegistry.Runners() {
			if runner.Host.PID == command.Process.Pid && runner.Connected && runner.Status == registry.RunnerStatusIdle {
				id = runner.ID
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond)
	return id
}

func TestNativeUIReleaseAcrossRunnerPlacements(t *testing.T) {
	for _, placement := range []string{"embedded", "standalone"} {
		t.Run(placement, func(t *testing.T) {
			config := embeddedRunnerTestConfig(t)
			t.Setenv("HOME", t.TempDir())
			workspace, settings := config.EmbeddedRunner.Workspace, config.EmbeddedRunner.Settings
			settings["extensions"] = map[string]any{"enabled": true}
			directory := filepath.Join(workspace, ".kodelet", "extensions")
			require.NoError(t, os.MkdirAll(directory, 0o700))
			executable, err := os.Executable()
			require.NoError(t, err)
			script := fmt.Sprintf("#!/bin/sh\nKODELET_NATIVE_UI_RELEASE_HELPER=1 exec %q -test.run '^TestNativeUIReleaseExtensionProcess$'\n", executable)
			require.NoError(t, os.WriteFile(filepath.Join(directory, "kodelet-extension-native-release"), []byte(script), 0o700))
			if placement == "standalone" {
				config.EmbeddedRunner = nil
			}
			server, endpoint, stop := startEmbeddedRunnerTestServer(t, config, "127.0.0.1:0")
			var runnerID string
			if placement == "embedded" {
				require.Eventually(t, func() bool { return server.EmbeddedRunnerStatus().Ready }, 5*time.Second, 10*time.Millisecond)
				runnerID = server.EmbeddedRunnerStatus().RunnerID
			} else {
				runnerID = startNativeReleaseRunnerProcess(t, server, endpoint, workspace)
			}
			store, err := conversations.GetConversationStore(t.Context())
			require.NoError(t, err)
			defer store.Close()
			record := convtypes.NewConversationRecord("native-release")
			record.Provider, record.CWD, record.RawMessages = "anthropic", workspace, json.RawMessage(`[]`)
			require.NoError(t, store.Save(t.Context(), record))
			opened, finish := make(chan string, 2), make(chan struct{}, 2)
			server.chatRunner = &mockChatRunner{runFunc: func(ctx context.Context, request ChatRequest, sink ChatEventSink) (string, error) {
				runID := ctx.Value(turnRunIDKey{}).(string)
				capabilities := protocol.ClientCapabilities{}
				if request.ClientCapabilities != nil {
					capabilities.InteractiveUI = request.ClientCapabilities.InteractiveUI
					capabilities.PersistentWidgets = request.ClientCapabilities.PersistentWidgets
					capabilities.PersistentSurfaces = request.ClientCapabilities.PersistentSurfaces
				}
				_, err := server.runnerRegistry.OpenRun(ctx, runnerID, protocol.RunOpenParams{RunID: runID, ConversationID: request.ConversationID, CWD: workspace, ClientCapabilities: capabilities})
				if err != nil {
					return request.ConversationID, err
				}
				opened <- runID
				_ = sink.Send(chat.ChatEvent{Kind: "text-delta", ConversationID: request.ConversationID, Delta: "UI transport gate"})
				select {
				case <-finish:
				case <-ctx.Done():
				}
				return request.ConversationID, server.runnerRegistry.CloseRun(context.WithoutCancel(ctx), runID, registry.RunStatusSucceeded, nil)
			}}
			host := &nativeReleaseHost{webExtensionUIHost: newWebExtensionUIHost(nil), surfaces: make(map[extensions.UIExtensionOwner]extensions.UIExtensionSource), opened: make(chan extensions.UIExtensionSource, 8), frames: make(chan extensions.UISurfaceFrameRequest, 8), transcript: make(chan extensions.UITranscriptAppendRequest, 8)}
			prompts := make(nativeReleasePrompts, 8)
			nativeCtx := extensions.ContextWithUIInputBroker(extensions.ContextWithExtensionUIHost(t.Context(), host), prompts)
			native, err := chat.NewControlPlaneChatRunner(endpoint, "web-secret", runnerID)
			require.NoError(t, err)
			nativeDone := make(chan error, 2)
			firstCtx, detachFirst := context.WithCancel(nativeCtx)
			t.Cleanup(detachFirst)
			go func() {
				_, err := native.Run(firstCtx, chat.ChatRequest{ConversationID: record.ID, Message: "first UI turn"}, &recordingChatSink{})
				nativeDone <- err
			}()
			runID := nativeReleaseNext(t, opened)
			broker := server.uiInputBrokerForRun(record.ID)
			require.NotNil(t, broker)
			var nativeID string
			require.Eventually(t, func() bool {
				broker.mu.Lock()
				defer broker.mu.Unlock()
				if broker.owner != nil {
					nativeID = broker.owner.clientID
				}
				return nativeID != ""
			}, time.Second, time.Millisecond)
			browser := nativeReleaseBrowser(t, endpoint, "")
			invoke := func(method string, request any) <-chan runnerpayload.ToolExecuteResult {
				result := make(chan runnerpayload.ToolExecuteResult, 1)
				data := mustRunnerJSON(t, map[string]any{"method": method, "request": request})
				params := runnerpayload.ToolExecuteParams{RunID: runID, ToolCallID: convtypes.GenerateID(), Name: "native_release", Input: data}
				go func() {
					response, err := server.runnerRegistry.ExecuteTool(t.Context(), params, nil)
					assert.NoError(t, err)
					result <- response
				}()
				return result
			}
			accepted := func(method string, request any) {
				t.Helper()
				response := nativeReleaseNext(t, invoke(method, request))
				require.Empty(t, response.Result.Error)
				require.Contains(t, response.Result.AssistantFacing, `"accepted":true`)
			}
			waitClosed := func() {
				t.Helper()
				require.Eventually(t, func() bool { host.mu.Lock(); defer host.mu.Unlock(); return len(host.surfaces) == 0 }, 5*time.Second, time.Millisecond)
			}
			accepted("kodelet.ui.surface.open", extensions.UISurfaceOpenRequest{ID: "canvas", Frame: extensions.UIFrame{Sequence: 1}})
			first := nativeReleaseNext(t, host.opened)
			input := extensions.UISurfaceInputNotification{ScopeID: record.ID, ID: "canvas", Sequence: 1, Kind: extensions.UISurfaceInputKey, Key: "enter"}
			require.NoError(t, first.NotifyExtensionUI(t.Context(), extensions.UISurfaceInputMethod, input))
			require.NoError(t, first.NotifyExtensionUI(t.Context(), extensions.UISurfaceResizeMethod, extensions.UISurfaceResizeNotification{ScopeID: record.ID, ID: "canvas", Sequence: 2, Width: 100, Height: 40}))
			accepted("kodelet.ui.surface.frame", extensions.UISurfaceFrameRequest{ID: "canvas", Frame: extensions.UIFrame{Sequence: 2}})
			assert.EqualValues(t, 2, nativeReleaseNext(t, host.frames).Frame.Sequence)
			accepted("kodelet.ui.transcript.append", extensions.UITranscriptAppendRequest{Message: "native transcript"})
			assert.Equal(t, "native transcript", nativeReleaseNext(t, host.transcript).Message)
			promptResult := invoke("kodelet.ui.input", extensions.UIInputRequest{Title: "native owner"})
			prompt := nativeReleaseNext(t, prompts)
			answer := extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted, Value: "native answer"}
			assert.Equal(t, http.StatusNotFound, nativeReleasePost(t, endpoint, "browser", "ui-input/"+prompt.request.ID, answer))
			prompt.answer <- answer
			assert.Contains(t, nativeReleaseNext(t, promptResult).Result.AssistantFacing, "native answer")
			for len(browser) > 0 {
				event := <-browser
				assert.Nil(t, event.UIInput, "observer must not receive native prompts")
				assert.Nil(t, event.UIPersistent, "observer must not receive interactive surfaces")
			}
			promptResult = invoke("kodelet.ui.input", extensions.UIInputRequest{Title: "dismiss on disconnect"})
			stalePrompt := nativeReleaseNext(t, prompts)
			detachFirst()
			require.Error(t, nativeReleaseNext(t, nativeDone))
			assert.Contains(t, nativeReleaseNext(t, promptResult).Result.AssistantFacing, "dismissed")
			waitClosed()
			require.Error(t, first.NotifyExtensionUI(t.Context(), extensions.UISurfaceInputMethod, input))
			assert.Equal(t, http.StatusNotFound, nativeReleasePost(t, endpoint, nativeID, "ui-input/"+stalePrompt.request.ID, answer))
			assert.Equal(t, http.StatusNotFound, nativeReleasePost(t, endpoint, "browser", "ui-input/"+stalePrompt.request.ID, answer))
			require.Eventually(t, func() bool { broker.mu.Lock(); defer broker.mu.Unlock(); return broker.owner == nil }, 5*time.Second, time.Millisecond)
			assert.True(t, server.isActiveChat(record.ID), "owner disconnect must not cancel admitted execution")

			// Reconnecting the original client only observes the ongoing turn.
			streamCtx, detach := context.WithCancel(nativeCtx)
			t.Cleanup(detach)
			streamDone := make(chan error, 1)
			go func() { streamDone <- native.StreamConversation(streamCtx, record.ID, &recordingChatSink{}) }()
			require.Eventually(t, func() bool {
				server.chatSubscribersMu.Lock()
				defer server.chatSubscribersMu.Unlock()
				return len(server.chatSubscribers[record.ID]) == 2
			}, 5*time.Second, time.Millisecond)
			broker.mu.Lock()
			assert.Nil(t, broker.owner, "neither the browser observer nor the reconnected submitter inherits ownership")
			broker.mu.Unlock()
			unavailable := nativeReleaseNext(t, invoke("kodelet.ui.input", extensions.UIInputRequest{Title: "unattended"}))
			assert.Contains(t, unavailable.Result.AssistantFacing, "unavailable")
			unavailable = nativeReleaseNext(t, invoke("kodelet.ui.surface.open", extensions.UISurfaceOpenRequest{ID: "canvas", Frame: extensions.UIFrame{Sequence: 3}}))
			assert.Contains(t, unavailable.Result.AssistantFacing, `"accepted":false`)
			detach()
			_ = nativeReleaseNext(t, streamDone)
			finish <- struct{}{}
			for {
				event := nativeReleaseNext(t, browser)
				assert.Nil(t, event.UIInput, "observer must not inherit prompts after the submitter disconnects")
				assert.Nil(t, event.UIPersistent, "observer must not inherit native surfaces")
				if event.Kind == "done" {
					break
				}
			}
			assert.False(t, server.isActiveChat(record.ID))

			// A browser owns its next submitted turn, without native capabilities.
			browserTurn := nativeReleaseBrowser(t, endpoint, "browser UI turn")
			runID = nativeReleaseNext(t, opened)
			broker = server.uiInputBrokerForRun(record.ID)
			require.NotNil(t, broker)
			broker.mu.Lock()
			owner := broker.owner
			broker.mu.Unlock()
			require.NotNil(t, owner)
			assert.Equal(t, "browser", owner.clientID)
			assert.False(t, nativeCapabilities(owner.ctx).PersistentSurfaces)
			promptResult = invoke("kodelet.ui.input", extensions.UIInputRequest{Title: "browser submitted prompt"})
			var browserPrompt *chat.UIInputEvent
			for browserPrompt == nil {
				browserPrompt = nativeReleaseNext(t, browserTurn).UIInput
			}
			assert.Equal(t, http.StatusNotFound, nativeReleasePost(t, endpoint, nativeID, "ui-input/"+browserPrompt.ID, answer))
			assert.Equal(t, http.StatusOK, nativeReleasePost(t, endpoint, "browser", "ui-input/"+browserPrompt.ID, answer))
			assert.Contains(t, nativeReleaseNext(t, promptResult).Result.AssistantFacing, "native answer")
			unavailable = nativeReleaseNext(t, invoke("kodelet.ui.surface.open", extensions.UISurfaceOpenRequest{ID: "canvas", Frame: extensions.UIFrame{Sequence: 4}}))
			assert.Contains(t, unavailable.Result.AssistantFacing, `"accepted":false`)
			unavailable = nativeReleaseNext(t, invoke("kodelet.ui.transcript.append", extensions.UITranscriptAppendRequest{Message: "unavailable browser transcript"}))
			assert.Contains(t, unavailable.Result.AssistantFacing, `"accepted":false`)
			finish <- struct{}{}
			for nativeReleaseNext(t, browserTurn).Kind != "done" {
			}
			assert.False(t, server.isActiveChat(record.ID))

			// The next native submission restores its own capabilities on activation.
			go func() {
				_, err := native.Run(nativeCtx, chat.ChatRequest{ConversationID: record.ID, Message: "next native UI turn"}, &recordingChatSink{})
				nativeDone <- err
			}()
			runID = nativeReleaseNext(t, opened)
			broker = server.uiInputBrokerForRun(record.ID)
			require.NotNil(t, broker)
			broker.mu.Lock()
			owner = broker.owner
			broker.mu.Unlock()
			require.NotNil(t, owner)
			assert.Equal(t, nativeID, owner.clientID)
			assert.True(t, nativeCapabilities(owner.ctx).PersistentSurfaces)
			accepted("kodelet.ui.surface.open", extensions.UISurfaceOpenRequest{ID: "canvas", Frame: extensions.UIFrame{Sequence: 5}})
			finalSource := nativeReleaseNext(t, host.opened)
			finish <- struct{}{}
			require.NoError(t, nativeReleaseNext(t, nativeDone))
			waitClosed()
			require.Error(t, finalSource.NotifyExtensionUI(t.Context(), extensions.UISurfaceResizeMethod, extensions.UISurfaceResizeNotification{ScopeID: record.ID, ID: "canvas", Sequence: 3, Width: 80, Height: 24}))
			pidData, err := os.ReadFile(filepath.Join(workspace, "native-worker.pid"))
			require.NoError(t, err)
			pid, err := strconv.Atoi(string(pidData))
			require.NoError(t, err)
			require.NoError(t, syscall.Kill(pid, 0), "explicit lease, not interactive UI, retains the worker")
			require.Eventually(t, func() bool { _, widgets := server.extensionUI.Snapshot(record.ID); return len(widgets) == 1 }, 5*time.Second, time.Millisecond)
			require.Eventually(t, func() bool {
				data, _ := os.ReadFile(filepath.Join(workspace, "native-events.log"))
				return strings.Contains(string(data), "native surfaces require an active execution")
			}, 5*time.Second, time.Millisecond, "retained extension cannot open a surface after run completion")
			_, widgets := server.extensionUI.Snapshot(record.ID)
			require.Len(t, widgets, 1)
			for {
				event := nativeReleaseNext(t, browser)
				assert.Nil(t, event.UIInput, "the observer stream must not receive another connection's prompts")
				assert.Nil(t, event.UIPersistent, "browser observer must never receive native output")
				if event.UIWidget != nil && event.UIWidget.Frame.Sequence == widgets[0].Frame.Sequence {
					assert.Equal(t, "worker", event.UIWidget.ID)
					assert.False(t, event.UIWidget.Removed)
					break
				}
			}
			data, err := os.ReadFile(filepath.Join(workspace, "native-events.log"))
			require.NoError(t, err)
			assert.Contains(t, string(data), extensions.UISurfaceInputMethod)
			assert.Contains(t, string(data), extensions.UISurfaceResizeMethod)
			assert.Contains(t, string(data), "extension.ui.surface.closed")
			assert.Contains(t, string(data), "background-widget")
			stop()
			require.Eventually(t, func() bool { return syscall.Kill(pid, 0) == syscall.ESRCH }, 5*time.Second, 10*time.Millisecond)
		})
	}
}
