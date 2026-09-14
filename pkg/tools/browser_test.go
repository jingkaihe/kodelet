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
	cwd, method, stopped string
	params               json.RawMessage
	response             json.RawMessage
	err                  error
}

func (b *fakeBrowserController) Open(_ context.Context, cwd string) (browser.Info, error) {
	b.cwd = cwd
	return browser.Info{SessionID: "session", CWD: cwd}, b.err
}

func (b *fakeBrowserController) Command(_ context.Context, cwd, method string, params json.RawMessage) (json.RawMessage, error) {
	b.cwd, b.method, b.params = cwd, method, params
	return b.response, b.err
}

func (b *fakeBrowserController) Stop(cwd, id string) error {
	b.cwd, b.stopped = cwd, id
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

func TestBrowserToolActionsUseWorkspace(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	state := NewBasicState(t.Context(), WithWorkingDirectory(t.TempDir()))
	controller := &fakeBrowserController{response: json.RawMessage(`{"result":{"value":"Ready"}}`)}
	tool := NewBrowserTool(controller)
	result := tool.Execute(t.Context(), state, `{"action":"open"}`)
	require.False(t, result.IsError(), result.GetError())
	assert.Contains(t, result.GetResult(), `"sessionId":"session"`)
	assert.Equal(t, state.WorkingDirectory(), controller.cwd)
	assert.Equal(t, "browser", result.StructuredData().ToolName)
	assert.Empty(t, result.(tooltypes.MultiModalToolResult).ContentParts())

	result = tool.Execute(t.Context(), state, `{"action":"navigate","url":"http://localhost:1234"}`)
	require.False(t, result.IsError(), result.GetError())
	assert.Equal(t, "Page.navigate", controller.method)
	assert.JSONEq(t, `{"url":"http://localhost:1234"}`, string(controller.params))
	result = tool.Execute(t.Context(), state, `{"action":"evaluate","expression":"document.title"}`)
	require.False(t, result.IsError(), result.GetError())
	assert.Equal(t, "Runtime.evaluate", controller.method)
	assert.JSONEq(t, `{"expression":"document.title","returnByValue":true,"awaitPromise":true,"timeout":25000}`, string(controller.params))
	assert.Contains(t, result.GetResult(), "Ready")
	assert.Empty(t, controller.stopped, "normal actions must not close the shared browser")

	result = tool.Execute(t.Context(), state, `{"action":"stop","sessionId":"session"}`)
	require.False(t, result.IsError(), result.GetError())
	assert.Equal(t, "session", controller.stopped)
	for _, response := range []string{`invalid`, `{"errorText":"net::ERR_CONNECTION_REFUSED"}`, `{"exceptionDetails":{"text":"ReferenceError"}}`} {
		controller.response = json.RawMessage(response)
		result = tool.Execute(t.Context(), state, `{"action":"evaluate","expression":"bad()"}`)
		assert.True(t, result.IsError(), response)
		assert.False(t, result.StructuredData().Success)
	}
	controller.err = errors.New("browser exited")
	for _, input := range []string{`{"action":"open"}`, `{"action":"stop","sessionId":"session"}`, `{"action":"evaluate","expression":"1"}`} {
		assert.Contains(t, tool.Execute(t.Context(), state, input).GetError(), "browser exited")
	}
}

func TestBrowserToolScreenshot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
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
	result := tool.Execute(t.Context(), state, `{"action":"screenshot","path":"page.png"}`)
	require.False(t, result.IsError(), result.GetError())
	assert.Equal(t, "Page.captureScreenshot", controller.method)
	structured := result.StructuredData()
	assert.Equal(t, "browser", structured.ToolName)
	require.Len(t, structured.Attachments, 1)
	assert.Equal(t, filepath.Join(workspace, "page.png"), structured.Attachments[0].Path)
	require.Len(t, result.(tooltypes.MultiModalToolResult).ContentParts(), 1)
	assert.NotContains(t, result.GetResult(), base64.StdEncoding.EncodeToString(png))

	result = tool.Execute(t.Context(), state, `{"action":"screenshot","path":"page.png"}`)
	assert.True(t, result.IsError(), "existing screenshot must not be overwritten")
	actual, err := os.ReadFile(filepath.Join(workspace, "page.png"))
	require.NoError(t, err)
	assert.Equal(t, png, actual)
	for _, data := range []string{"", "???", base64.StdEncoding.EncodeToString([]byte("not an image"))} {
		controller.response, err = json.Marshal(map[string]string{"data": data})
		require.NoError(t, err)
		assert.True(t, tool.Execute(t.Context(), state, `{"action":"screenshot","path":"invalid.png"}`).IsError())
		assert.NoFileExists(t, filepath.Join(workspace, "invalid.png"))
	}
}
