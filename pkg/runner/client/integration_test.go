package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type forkCallbackEnvironment struct {
	agentenv.Environment
}

func (e *forkCallbackEnvironment) ExecuteTool(ctx context.Context, request agentenv.ToolRequest, updates agentenv.ToolUpdateSink) (agentenv.ToolExecution, error) {
	if request.Name != "fork_callback" {
		return e.Environment.ExecuteTool(ctx, request, updates)
	}
	forker, ok := tools.ToolContextFromContext(ctx).MetadataStore.(llmtypes.ConversationForker)
	if !ok {
		return agentenv.ToolExecution{}, llmtypes.ErrConversationForkUnavailable
	}
	conversationID, err := forker.ForkConversation(ctx)
	if err != nil {
		return agentenv.ToolExecution{}, err
	}
	result := tooltypes.BaseToolResult{Result: conversationID}
	structured := result.StructuredData()
	structured.ToolName = request.Name
	return agentenv.ToolExecution{Input: request.Input, Result: result, StructuredResult: structured}, nil
}

type integrationConversationForker struct{}

func (*integrationConversationForker) GetMetadata() map[string]any { return nil }

func (*integrationConversationForker) SetMetadataValue(string, any) {}

func (*integrationConversationForker) ForkConversation(context.Context) (string, error) {
	return "conversation-child", nil
}

func TestRunnerServiceRoundTripsThroughSymmetricWebsocketProtocol(t *testing.T) {
	workspace := t.TempDir()
	filePath := filepath.Join(workspace, "wire.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("over the wire\n"), 0o600))

	registry, err := runnerregistry.New(t.Context(), runnerregistry.Options{
		HeartbeatInterval: time.Hour,
		HeartbeatTimeout:  2 * time.Hour,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, registry.Close()) })

	upgrader := websocket.Upgrader{
		Subprotocols: []string{protocol.Subprotocol},
		CheckOrigin:  func(*http.Request) bool { return true },
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		session := runnerregistry.NewSession(registry, nil)
		peer, err := protocol.NewPeer(conn, protocol.PeerConfig{
			RequestPrefix: "server",
			Handler:       session,
			Notifications: session,
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
	service, err := NewService(t.Context(), workspace, ServiceOptions{
		RuntimeProvider: staticRuntimeProvider{runtime: runtime},
		ConfigLoader: func(string) (llmtypes.Config, error) {
			return llmtypes.Config{AllowedTools: []string{"file_read"}}, nil
		},
		EnvironmentFactory: func(workingDirectory string, runtime *extensions.Runtime) agentenv.Environment {
			return &forkCallbackEnvironment{Environment: agentenv.NewLocalEnvironment(workingDirectory, runtime)}
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })

	dialer := websocket.Dialer{Subprotocols: []string{protocol.Subprotocol}}
	conn, response, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if response != nil && response.Body != nil {
		t.Cleanup(func() { _ = response.Body.Close() })
	}
	require.NoError(t, err)
	peer, err := protocol.NewPeer(conn, protocol.PeerConfig{
		RequestPrefix: "runner",
		Handler:       service,
		Notifications: service,
	})
	require.NoError(t, err)
	service.Attach(peer)
	require.NoError(t, peer.Start(t.Context()))
	t.Cleanup(func() { require.NoError(t, peer.Close()) })

	var registration protocol.RegisterResult
	require.NoError(t, peer.Call(t.Context(), protocol.MethodRunnerRegister, protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version},
		Host: protocol.Host{
			InstanceID: "host-1",
			Hostname:   "runner-host",
			OS:         "linux",
			Arch:       "amd64",
		},
		Workspace: protocol.Workspace{Path: workspace, Name: filepath.Base(workspace)},
	}, &registration))
	require.NoError(t, service.SetRegistration(registration))
	require.NoError(t, peer.Notify(t.Context(), protocol.MethodRunnerHeartbeat, protocol.HeartbeatParams{
		RunnerID:   registration.RunnerID,
		Generation: registration.Generation,
		State:      protocol.RunnerStateIdle,
	}))
	require.Eventually(t, func() bool {
		runner, ok := registry.Runner(registration.RunnerID)
		return ok && runner.Status == runnerregistry.RunnerStatusIdle
	}, time.Second, 10*time.Millisecond)

	manifest, err := registry.OpenRun(t.Context(), registration.RunnerID, protocol.RunOpenParams{
		RunID:             "run-wire",
		ConversationID:    "conversation-wire",
		ReservedToolNames: []string{"get_goal", "update_goal", "read_conversation"},
	})
	require.NoError(t, err)
	assert.Equal(t, "run-wire", manifest.RunID)
	assert.Contains(t, manifestToolNames(manifest), "file_read")
	require.NotNil(t, manifest.Config.SystemInformation)
	assert.Equal(t, goruntime.GOOS, manifest.Config.SystemInformation.Platform)
	assert.NotEmpty(t, manifest.Config.SystemInformation.OSVersion)
	assert.Regexp(t, `^\d{4}-\d{2}-\d{2}$`, manifest.Config.SystemInformation.Date)

	var lifecycle runnerpayload.LifecycleDispatchResult
	require.NoError(t, registry.CallRun(t.Context(), "run-wire", protocol.MethodLifecycleDispatch, runnerpayload.LifecycleDispatchParams{
		RunID:        "run-wire",
		Event:        runnerpayload.LifecycleAgentInit,
		SystemPrompt: "wire prompt",
		AllowedTools: []string{"file_read"},
	}, &lifecycle))
	assert.Equal(t, "wire prompt", lifecycle.SystemPrompt)

	result, err := registry.ExecuteTool(t.Context(), runnerpayload.ToolExecuteParams{
		RunID:      "run-wire",
		ToolCallID: "tool-wire",
		Name:       "file_read",
		Input:      json.RawMessage(`{"file_path":"` + filePath + `","offset":1,"line_limit":10}`),
	}, nil)
	require.NoError(t, err)
	assert.Contains(t, result.Result.AssistantFacing, "over the wire")
	assert.True(t, result.Result.Structured.Success)

	forker := &integrationConversationForker{}
	forkCtx := tools.ContextWithToolContext(t.Context(), tools.ToolContext{
		ConversationID: "conversation-wire",
		MetadataStore:  forker,
	})
	result, err = registry.ExecuteTool(forkCtx, runnerpayload.ToolExecuteParams{
		RunID:      "run-wire",
		ToolCallID: "tool-fork",
		Name:       "fork_callback",
		Input:      json.RawMessage(`{}`),
	}, nil)
	require.NoError(t, err)
	assert.Contains(t, result.Result.AssistantFacing, "conversation-child")
	childRunnerID, ok := registry.RunnerForConversation("conversation-child")
	require.True(t, ok)
	assert.Equal(t, registration.RunnerID, childRunnerID)

	var lateFork runnerpayload.ConversationForkResult
	err = peer.Call(t.Context(), protocol.MethodConversationFork, runnerpayload.ConversationForkParams{
		RunID: "run-wire", ToolCallID: "tool-fork",
	}, &lateFork)
	var rpcErr *protocol.RPCError
	require.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, protocol.ErrorCodeUnavailable, rpcErr.Code)

	require.NoError(t, registry.CloseRun(t.Context(), "run-wire", runnerregistry.RunStatusSucceeded, nil))
	run, ok := registry.Run("run-wire")
	require.True(t, ok)
	assert.Equal(t, runnerregistry.RunStatusSucceeded, run.Status)
}
