package controlplane

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImageArtifactRunnerRoundTrip(t *testing.T) {
	config := embeddedRunnerTestConfig(t)
	config.PublicBaseURL = "https://images.example/kodelet"
	// A valid image larger than a runner RPC message exercises the binary channel.
	img := image.NewNRGBA(image.Rect(0, 0, 1100, 1100))
	_, err := rand.New(rand.NewSource(1)).Read(img.Pix)
	require.NoError(t, err)
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, img))
	require.Greater(t, encoded.Len(), 4*1024*1024)
	path := filepath.Join(config.EmbeddedRunner.Workspace, "generated.png")
	require.NoError(t, os.WriteFile(path, encoded.Bytes(), 0o600))
	config.EmbeddedRunner.ServiceOptions.EnvironmentFactory = func(
		cwd string,
		runtime *extensions.Runtime,
	) agentenv.Environment {
		local := agentenv.NewLocalEnvironment(cwd, runtime)
		return &embeddedTestEnvironment{
			Environment: local,
			execute: func(
				ctx context.Context,
				request agentenv.ToolRequest,
				updates agentenv.ToolUpdateSink,
			) (agentenv.ToolExecution, error) {
				if request.Name != "generate_test_image" {
					return local.ExecuteTool(ctx, request, updates)
				}
				imagePath := path
				if request.Input == `{"missing":true}` {
					imagePath += ".missing"
				}
				result := tooltypes.BaseToolResult{Result: "Generated test image"}
				structured := result.StructuredData()
				structured.ToolName = request.Name
				structured.Attachments = []tooltypes.ToolAttachment{
					{
						Type: "image",
						Path: imagePath,
						Alt:  "Generated test pattern",
					},
				}
				return agentenv.ToolExecution{
					Input:            request.Input,
					Result:           result,
					StructuredResult: structured,
				}, nil
			},
		}
	}
	server, _, stop := startEmbeddedRunnerTestServer(t, config, "127.0.0.1:0")
	require.Eventually(t, func() bool {
		return server.EmbeddedRunnerStatus().Ready
	}, 5*time.Second, 10*time.Millisecond)
	runnerID := server.EmbeddedRunnerStatus().RunnerID
	_, err = server.runnerRegistry.OpenRun(t.Context(), runnerID, protocol.RunOpenParams{
		RunID:          "image-run",
		ConversationID: "image-conversation",
	})
	require.NoError(t, err)
	controller := artifactController{
		RemoteController: server.runnerRegistry,
		server:           server,
		conversationID:   "image-conversation",
		config:           llmtypes.Config{Provider: "openai", Model: "gpt-4.1"},
	}
	generated, err := controller.ExecuteTool(t.Context(), runnerpayload.ToolExecuteParams{
		RunID:      "image-run",
		ToolCallID: "generate",
		Name:       "generate_test_image",
		Input:      json.RawMessage(`{}`),
	}, nil)
	require.NoError(t, err)
	require.Empty(t, generated.Result.Error)
	require.Len(t, generated.Result.Structured.Attachments, 1)
	attachment := generated.Result.Structured.Attachments[0]
	require.Empty(t, attachment.Error)
	assert.Empty(t, attachment.Path)
	assert.NotEqual(t, attachment.ArtifactID, attachment.ShortCode)
	assert.Equal(t, "https://images.example/kodelet/i/"+attachment.ShortCode, attachment.ViewURL)
	assert.Equal(t, int64(encoded.Len()), attachment.Size)
	assert.Contains(t, generated.Result.AssistantFacing, attachment.ArtifactID)
	assert.Contains(t, generated.Result.AssistantFacing, "Image URL: "+attachment.ViewURL)
	assert.Contains(t, generated.Result.AssistantFacing, "Use this URL when linking the image")
	assert.NotContains(t, generated.Result.AssistantFacing, path)
	assert.NotContains(t, generated.Result.AssistantFacing, "file://")
	assert.Empty(t, generated.Result.ContentParts, "a user-visible image must not automatically enter model context")

	// The model-facing URL follows current server configuration, never an extension-supplied URL.
	server.config.PublicBaseURL = ""
	relative := runnerpayload.ToolResult{
		Structured: tooltypes.StructuredToolResult{
			Attachments: []tooltypes.ToolAttachment{
				{
					Type:       "image",
					ArtifactID: attachment.ArtifactID,
					ViewURL:    "file://" + path,
				},
			},
		},
	}
	controller.normalizeAttachments(t.Context(), &relative)
	assert.Contains(t, relative.AssistantFacing, "Image URL: /i/"+attachment.ShortCode)
	assert.NotContains(t, relative.AssistantFacing, path)
	server.config.PublicBaseURL = "https://images.example/kodelet"

	input, err := json.Marshal(map[string]string{"artifactId": attachment.ArtifactID})
	require.NoError(t, err)
	viewed, err := controller.ExecuteTool(t.Context(), runnerpayload.ToolExecuteParams{
		RunID:      "image-run",
		ToolCallID: "view",
		Name:       "view_image",
		Input:      input,
	}, nil)
	require.NoError(t, err)
	require.Empty(t, viewed.Result.Error)
	require.Len(t, viewed.Result.ContentParts, 1)
	assert.True(t, strings.HasPrefix(viewed.Result.ContentParts[0].ImageURL, "data:image/png;base64,"))
	assert.Empty(t, viewed.Result.ContentParts[0].ArtifactID, "artifact bytes must be resolved before provider conversion")
	require.Len(t, viewed.Result.Structured.Attachments, 1)
	assert.Equal(t, attachment.ArtifactID, viewed.Result.Structured.Attachments[0].ArtifactID)
	assert.Contains(t, viewed.Result.AssistantFacing, "Image URL: "+attachment.ViewURL)
	_, err = server.runnerRegistry.OpenRun(t.Context(), runnerID, protocol.RunOpenParams{
		RunID:          "other-image-run",
		ConversationID: "other-image-conversation",
	})
	require.NoError(t, err)
	otherController := artifactController{
		RemoteController: server.runnerRegistry,
		server:           server,
		conversationID:   "other-image-conversation",
		config:           controller.config,
	}
	denied, err := otherController.ExecuteTool(t.Context(), runnerpayload.ToolExecuteParams{
		RunID:      "other-image-run",
		ToolCallID: "view-other",
		Name:       "view_image",
		Input:      input,
	}, nil)
	require.NoError(t, err)
	assert.Contains(t, denied.Result.Error, "not available in this conversation")
	assert.Empty(t, denied.Result.ContentParts)
	require.NoError(t, server.runnerRegistry.CloseRun(t.Context(), "other-image-run", protocol.RunStatusSucceeded, nil))

	failed, err := controller.ExecuteTool(t.Context(), runnerpayload.ToolExecuteParams{
		RunID:      "image-run",
		ToolCallID: "missing",
		Name:       "generate_test_image",
		Input:      json.RawMessage(`{"missing":true}`),
	}, nil)
	require.NoError(t, err)
	assert.True(t, failed.Result.Structured.Success, "image transfer failure must preserve the successful tool result")
	assert.Contains(t, failed.Result.AssistantFacing, "Generated test image")
	require.Len(t, failed.Result.Structured.Attachments, 1)
	assert.NotEmpty(t, failed.Result.Structured.Attachments[0].Error)
	assert.Empty(t, failed.Result.Structured.Attachments[0].ArtifactID)

	// A registered runner cannot resolve artifacts outside an active tool call.
	runner, found := server.runnerRegistry.Runner(runnerID)
	require.True(t, found)
	raw, err := json.Marshal(runnerpayload.ArtifactRequest{
		RunID:      "image-run",
		ToolCallID: "view",
		ArtifactID: attachment.ArtifactID,
	})
	require.NoError(t, err)
	_, rpcErr := server.HandleRunnerArtifactRequest(t.Context(), runnerregistry.UIRequestIdentity{
		RunnerID:     runnerID,
		ConnectionID: runner.ConnectionID,
		Generation:   runner.Generation,
	}, runnerpayload.MethodArtifactResolve, raw)
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeStale, rpcErr.Code)

	// History stores IDs, not the advertised host. A restarted server can choose a new host.
	store, err := conversations.GetConversationStore(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, store.Close())
	})
	require.NoError(t, store.Save(t.Context(), convtypes.ConversationRecord{
		ID:          "image-conversation",
		Provider:    "anthropic",
		RawMessages: json.RawMessage(`[]`),
		ToolResults: map[string]tooltypes.StructuredToolResult{"generate": generated.Result.Structured},
	}))
	saved, err := server.conversationService.GetConversation(t.Context(), "image-conversation")
	require.NoError(t, err)
	assert.Empty(t, saved.ToolResults["generate"].Attachments[0].ViewURL)
	server.config.PublicBaseURL = "https://new.example"
	assert.Equal(
		t,
		"https://new.example/i/"+attachment.ShortCode,
		server.decorateImageAttachments(saved.ToolResults["generate"]).Attachments[0].ViewURL,
	)

	imagePath := "/i/" + attachment.ShortCode
	for _, test := range []struct {
		path, token string
		status      int
	}{
		{imagePath, "", http.StatusUnauthorized},
		{imagePath, "web-secret", http.StatusOK},
		{"/api/conversations/other/artifacts/" + attachment.ArtifactID, "web-secret", http.StatusNotFound},
		{"/api/conversations/image-conversation/artifacts/" + attachment.ArtifactID, "web-secret", http.StatusOK},
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		if test.token != "" {
			request.Header.Set("Authorization", "Bearer "+test.token)
		}
		server.router.ServeHTTP(response, request)
		assert.Equal(t, test.status, response.Code)
		if test.status == http.StatusOK {
			assert.Equal(t, encoded.Bytes(), response.Body.Bytes())
			assert.Equal(t, "image/png", response.Header().Get("Content-Type"))
			assert.Contains(t, response.Header().Get("Content-Disposition"), "inline;")
			assert.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
		}
	}
	// Consumed tickets cannot be replayed, even with otherwise valid web credentials.
	server.artifactUploads.mu.Lock()
	var token string
	for value := range server.artifactUploads.tickets {
		token = value
		break
	}
	server.artifactUploads.mu.Unlock()
	require.NotEmpty(t, token)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, runnerpayload.ArtifactUploadPath, bytes.NewReader(encoded.Bytes()))
	request.Header.Set("Authorization", "Bearer "+token)
	server.router.ServeHTTP(response, request)
	assert.Equal(t, http.StatusUnauthorized, response.Code)
	require.NoError(t, server.runnerRegistry.CloseRun(t.Context(), "image-run", protocol.RunStatusSucceeded, nil))
	stop()

	// The runner and the source image can disappear without invalidating the artifact.
	require.NoError(t, os.Remove(path))
	config.EmbeddedRunner = nil
	reopened, err := NewServer(t.Context(), config, testFrontendHandler())
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, reopened.Close())
	})
	request = httptest.NewRequest(http.MethodGet, imagePath+"?download=1", nil)
	request.Header.Set("Authorization", "Bearer web-secret")
	response = httptest.NewRecorder()
	reopened.router.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Header().Get("Content-Disposition"), "attachment;")
	actual, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	assert.Equal(t, encoded.Bytes(), actual)
}

func TestImageArtifactUploadCancellationInterruptsStalledBody(t *testing.T) {
	config := embeddedRunnerTestConfig(t)
	started := make(chan struct{})
	config.EmbeddedRunner.ServiceOptions.EnvironmentFactory = func(
		cwd string,
		runtime *extensions.Runtime,
	) agentenv.Environment {
		return &embeddedTestEnvironment{
			Environment: agentenv.NewLocalEnvironment(cwd, runtime),
			execute: func(
				ctx context.Context,
				_ agentenv.ToolRequest,
				_ agentenv.ToolUpdateSink,
			) (agentenv.ToolExecution, error) {
				close(started)
				<-ctx.Done()
				return agentenv.ToolExecution{}, ctx.Err()
			},
		}
	}
	server, endpoint, _ := startEmbeddedRunnerTestServer(t, config, "127.0.0.1:0")
	require.Eventually(t, func() bool {
		return server.EmbeddedRunnerStatus().Ready
	}, 5*time.Second, 10*time.Millisecond)
	runnerID := server.EmbeddedRunnerStatus().RunnerID
	_, err := server.runnerRegistry.OpenRun(t.Context(), runnerID, protocol.RunOpenParams{
		RunID:          "stalled-run",
		ConversationID: "stalled-conversation",
	})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := server.runnerRegistry.ExecuteTool(ctx, runnerpayload.ToolExecuteParams{
			RunID:      "stalled-run",
			ToolCallID: "generate",
			Name:       "generate_test_image",
			Input:      json.RawMessage(`{}`),
		}, nil)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "tool did not start")
	}
	runner, found := server.runnerRegistry.Runner(runnerID)
	require.True(t, found)
	raw, err := json.Marshal(runnerpayload.ArtifactRequest{
		RunID:      "stalled-run",
		ToolCallID: "generate",
		Attachment: tooltypes.ToolAttachment{Type: "image"},
	})
	require.NoError(t, err)
	value, rpcErr := server.HandleRunnerArtifactRequest(t.Context(), runnerregistry.UIRequestIdentity{
		RunnerID:     runnerID,
		ConnectionID: runner.ConnectionID,
		Generation:   runner.Generation,
	}, runnerpayload.MethodArtifactUpload, raw)
	require.Nil(t, rpcErr)
	grant, ok := value.(runnerpayload.ArtifactUploadGrant)
	require.True(t, ok)
	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(endpoint, "http://"), time.Second)
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))
	// Leave a short HTTP/1 body incomplete without closing the client socket.
	_, err = fmt.Fprintf(
		conn,
		"PUT %s HTTP/1.1\r\nHost: test\r\nAuthorization: Bearer %s\r\nContent-Length: 16\r\n\r\nx",
		runnerpayload.ArtifactUploadPath,
		grant.Token,
	)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		server.artifactUploads.mu.Lock()
		defer server.artifactUploads.mu.Unlock()
		return server.artifactUploads.tickets[grant.Token].used
	}, time.Second, time.Millisecond)
	cancel()
	response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodPut})
	require.NoError(t, err, "canceling the tool must unblock the upload without closing the client socket")
	defer response.Body.Close()
	assert.Equal(t, http.StatusBadRequest, response.StatusCode)
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(time.Second):
		require.FailNow(t, "canceled tool did not finish")
	}
}
