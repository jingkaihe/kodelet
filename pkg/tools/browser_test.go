package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/browser"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeBrowserController struct {
	scope           browser.Scope
	method, stopped string
	params          json.RawMessage
	response        json.RawMessage
	err             error
}

func (b *fakeBrowserController) Open(_ context.Context, scope browser.Scope) (browser.Info, error) {
	b.scope = scope
	return browser.Info{SessionID: "session", ConversationID: scope.ConversationID, CWD: scope.CWD}, b.err
}

func (b *fakeBrowserController) Command(_ context.Context, scope browser.Scope, method string, params json.RawMessage) (json.RawMessage, error) {
	b.scope, b.method, b.params = scope, method, params
	return b.response, b.err
}

func (b *fakeBrowserController) Stop(scope browser.Scope, id string) error {
	b.scope, b.stopped = scope, id
	return b.err
}

func TestBrowserToolValidation(t *testing.T) {
	tool := NewBrowserTool(nil)
	assert.Equal(t, "browser", tool.Name())
	assert.NotNil(t, tool.GenerateSchema())
	assert.Contains(t, tool.Description(), "shared")
	for _, input := range []string{
		`{"action":"open"}`, `{"action":"navigate","url":"http://localhost:1234/abc"}`,
		`{"action":"navigate","url":"https://example.com"}`, `{"action":"navigate","url":"about:blank"}`,
		`{"action":"evaluate","expression":"document.title"}`, `{"action":"screenshot","path":"page.png"}`,
		`{"action":"stop","sessionId":"session"}`,
	} {
		assert.NoError(t, tool.ValidateInput(nil, input), input)
	}
	for _, input := range []string{
		`{`, `{}`, `{"action":"unknown"}`, `{"action":"navigate"}`, `{"action":"navigate","url":"javascript:alert(1)"}`,
		`{"action":"navigate","url":"file:///etc/passwd"}`, `{"action":"navigate","url":"https://user:password@example.com"}`,
		`{"action":"evaluate","expression":" "}`, `{"action":"screenshot","path":"page.html"}`, `{"action":"stop"}`,
	} {
		assert.Error(t, tool.ValidateInput(nil, input), input)
		assert.True(t, tool.Execute(t.Context(), nil, input).IsError(), input)
	}
	assert.True(t, tool.Execute(t.Context(), nil, `{"action":"open"}`).IsError())
	attrs, err := tool.TracingKVs(`{"action":"evaluate","expression":"secret"}`)
	require.NoError(t, err)
	require.Len(t, attrs, 1)
	assert.Equal(t, "action", string(attrs[0].Key))
	_, err = tool.TracingKVs(`{`)
	assert.Error(t, err)
}

func TestBrowserToolActionsUseTrustedConversationScope(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	state := NewBasicState(t.Context(), WithWorkingDirectory(t.TempDir()))
	ctx := ContextWithToolContext(t.Context(), ToolContext{ConversationID: "conversation", WorkingDir: "/ignored-context-directory"})
	scope := browser.Scope{ConversationID: "conversation", CWD: state.WorkingDirectory()}
	controller := &fakeBrowserController{response: json.RawMessage(`{"result":{"value":"Ready"}}`)}
	tool := NewBrowserTool(controller)
	result := tool.Execute(ctx, state, `{"action":"open","conversationId":"forged","cwd":"/forged"}`)
	require.False(t, result.IsError(), result.GetError())
	assert.Contains(t, result.GetResult(), `"sessionId":"session"`)
	assert.Contains(t, result.GetResult(), `"conversationId":"conversation"`)
	assert.Equal(t, scope, controller.scope)
	assert.Equal(t, "browser", result.StructuredData().ToolName)
	assert.Equal(t, tooltypes.BrowserMetadata{Action: "open", SessionID: "session", Output: result.GetResult()}, result.StructuredData().Metadata)
	assert.Empty(t, result.(tooltypes.MultiModalToolResult).ContentParts())

	// A new turn context retains the same trusted scope; input cannot override it.
	ctx = ContextWithConversationID(t.Context(), scope.ConversationID)
	controller.scope = browser.Scope{}
	result = tool.Execute(ctx, state, `{"action":"navigate","url":"http://localhost:1234","conversationId":"forged"}`)
	require.False(t, result.IsError(), result.GetError())
	assert.Equal(t, scope, controller.scope)
	assert.Equal(t, "Page.navigate", controller.method)
	assert.JSONEq(t, `{"url":"http://localhost:1234"}`, string(controller.params))
	assert.Equal(t, tooltypes.BrowserMetadata{Action: "navigate", URL: "http://localhost:1234", Output: result.GetResult()}, result.StructuredData().Metadata)
	result = tool.Execute(ctx, state, `{"action":"evaluate","expression":"document.title"}`)
	require.False(t, result.IsError(), result.GetError())
	assert.Equal(t, "Runtime.evaluate", controller.method)
	assert.JSONEq(t, `{"expression":"document.title","returnByValue":true,"awaitPromise":true,"timeout":25000}`, string(controller.params))
	assert.Contains(t, result.GetResult(), "Ready")
	assert.Equal(t, tooltypes.BrowserMetadata{Action: "evaluate", Expression: "document.title", Output: result.GetResult()}, result.StructuredData().Metadata)
	assert.Empty(t, controller.stopped, "normal actions must not close the shared browser")

	controller.scope = browser.Scope{}
	result = tool.Execute(ctx, state, `{"action":"stop","sessionId":"session","conversationId":"forged"}`)
	require.False(t, result.IsError(), result.GetError())
	assert.Equal(t, scope, controller.scope)
	assert.Equal(t, "session", controller.stopped)
	assert.Equal(t, tooltypes.BrowserMetadata{Action: "stop", SessionID: "session", Output: result.GetResult()}, result.StructuredData().Metadata)
	for _, response := range []string{`invalid`, `{"errorText":"net::ERR_CONNECTION_REFUSED"}`, `{"exceptionDetails":{"text":"ReferenceError"}}`} {
		controller.response = json.RawMessage(response)
		result = tool.Execute(ctx, state, `{"action":"evaluate","expression":"bad()"}`)
		assert.True(t, result.IsError(), response)
		assert.False(t, result.StructuredData().Success)
		assert.Equal(t, tooltypes.BrowserMetadata{Action: "evaluate", Expression: "bad()"}, result.StructuredData().Metadata, "failed actions must retain displayable input")
	}
	controller.err = errors.New("browser exited")
	for _, input := range []string{`{"action":"open"}`, `{"action":"stop","sessionId":"session"}`, `{"action":"evaluate","expression":"1"}`} {
		assert.Contains(t, tool.Execute(ctx, state, input).GetError(), "browser exited")
	}
}

func TestBrowserToolRejectsMissingConversationContext(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	state := NewBasicState(t.Context(), WithWorkingDirectory(t.TempDir()))
	controller := &fakeBrowserController{}
	tool := NewBrowserTool(controller)
	for _, ctx := range []context.Context{t.Context(), ContextWithToolContext(t.Context(), ToolContext{ConversationID: " \t", WorkingDir: state.WorkingDirectory()})} {
		for _, input := range []string{
			`{"action":"open","conversationId":"forged"}`,
			`{"action":"evaluate","expression":"1","conversationId":"forged"}`,
			`{"action":"screenshot","path":"page.png","conversationId":"forged"}`,
			`{"action":"stop","sessionId":"session","conversationId":"forged"}`,
		} {
			result := tool.Execute(ctx, state, input)
			require.True(t, result.IsError())
			assert.Contains(t, result.GetError(), "conversation context")
			assert.Equal(t, fakeBrowserController{}, *controller, "missing context must not call the browser controller")
		}
	}
}

func TestBrowserToolScreenshot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := ContextWithConversationID(t.Context(), "conversation")
	workspace := t.TempDir()
	source := filepath.Join(t.TempDir(), "source.png")
	writeTestPNG(t, source, 16, 12)
	png, err := os.ReadFile(source)
	require.NoError(t, err)
	response, err := json.Marshal(map[string]string{"data": base64.StdEncoding.EncodeToString(png)})
	require.NoError(t, err)
	controller := &fakeBrowserController{response: response}
	tool := NewBrowserTool(controller)
	state := NewBasicState(t.Context(), WithWorkingDirectory(workspace), WithLLMConfig(llmtypes.Config{Model: "gpt-5", Provider: "openai"}))
	result := tool.Execute(ctx, state, `{"action":"screenshot","path":"page.png"}`)
	require.False(t, result.IsError(), result.GetError())
	assert.Equal(t, browser.Scope{ConversationID: "conversation", CWD: workspace}, controller.scope)
	assert.Equal(t, "Page.captureScreenshot", controller.method)
	structured := result.StructuredData()
	assert.Equal(t, "browser", structured.ToolName)
	assert.Equal(t, tooltypes.BrowserMetadata{Action: "screenshot", Path: filepath.Join(workspace, "page.png"), Output: result.GetResult()}, structured.Metadata)
	require.Len(t, structured.Attachments, 1)
	assert.Equal(t, filepath.Join(workspace, "page.png"), structured.Attachments[0].Path)
	require.Len(t, result.(tooltypes.MultiModalToolResult).ContentParts(), 1)
	assert.NotContains(t, result.GetResult(), base64.StdEncoding.EncodeToString(png))
	encoded, err := json.Marshal(structured)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), base64.StdEncoding.EncodeToString(png))
	var restored tooltypes.StructuredToolResult
	require.NoError(t, json.Unmarshal(encoded, &restored))
	assert.Equal(t, structured.Metadata, restored.Metadata, "browser action survives runner transport and saved history")
	assert.Equal(t, structured.Attachments, restored.Attachments)

	result = tool.Execute(ctx, state, `{"action":"screenshot","path":"page.png"}`)
	assert.True(t, result.IsError(), "existing screenshot must not be overwritten")
	actual, err := os.ReadFile(filepath.Join(workspace, "page.png"))
	require.NoError(t, err)
	assert.Equal(t, png, actual)
	for _, data := range []string{"", "???", base64.StdEncoding.EncodeToString([]byte("not an image"))} {
		controller.response, err = json.Marshal(map[string]string{"data": data})
		require.NoError(t, err)
		assert.True(t, tool.Execute(ctx, state, `{"action":"screenshot","path":"invalid.png"}`).IsError())
		assert.NoFileExists(t, filepath.Join(workspace, "invalid.png"))
	}
}
