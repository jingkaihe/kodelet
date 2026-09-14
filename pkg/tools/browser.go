package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/browser"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"
)

// BrowserController is the runner-owned browser resource shared with the conversation's Web UI.
type BrowserController interface {
	Open(context.Context, browser.Scope) (browser.Info, error)
	Command(context.Context, browser.Scope, string, json.RawMessage) (json.RawMessage, error)
	Stop(browser.Scope, string) error
}

// BrowserTool is installed only in environments with an explicitly enabled browser.
type BrowserTool struct{ controller BrowserController }

// NewBrowserTool attaches agent actions to an existing runner-owned manager.
func NewBrowserTool(controller BrowserController) *BrowserTool {
	return &BrowserTool{controller: controller}
}

// BrowserInput describes an operation on the current conversation's shared page.
type BrowserInput struct {
	Action     string `json:"action" jsonschema:"enum=open,enum=navigate,enum=evaluate,enum=screenshot,enum=stop,description=Operation on the conversation browser shared with the human within this conversation"`
	URL        string `json:"url,omitempty" jsonschema:"description=HTTP or HTTPS URL for navigate. Use localhost to access local HTTP services. Navigation does not wait for application readiness."`
	Expression string `json:"expression,omitempty" jsonschema:"description=JavaScript expression for evaluate. Can inspect the DOM or interact with the page. Promises are awaited."`
	Path       string `json:"path,omitempty" jsonschema:"description=New PNG output path for screenshot, relative to the workspace or absolute. Existing files are not overwritten."`
	SessionID  string `json:"sessionId,omitempty" jsonschema:"description=Session ID returned by open; required for explicit stop."`
}

func (*BrowserTool) Name() string { return "browser" }

func (*BrowserTool) GenerateSchema() *jsonschema.Schema { return GenerateSchema[BrowserInput]() }

func (*BrowserTool) Description() string {
	return `Use the shared browser to collaborate visually with the human: show your work, demonstrate an issue, inspect the page they are discussing, or capture screenshots for feedback. You both see and interact with the same live page.

Prefer dedicated browser automation tools, such as Playwright, when available, for automated testing, repetitive interactions, or multi-step workflows that do not need the shared page. Do not assume those tools share this session.

Notes:
- Use localhost to access local HTTP services. Use evaluate for readiness checks, focused inspection, and small interactions.
- Leave the result open for the human to review unless asked to close it.
- This is a development browser, separate from the human’s personal browser. Start app servers separately using terminal or bash; this tool does not publish apps to the internet.`
}

func (*BrowserTool) ValidateInput(_ tooltypes.State, parameters string) error {
	var input BrowserInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return errors.Wrap(err, "invalid browser input")
	}
	switch input.Action {
	case "open":
	case "navigate":
		if input.URL == "about:blank" {
			return nil
		}
		u, err := url.Parse(input.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
			return errors.New("navigate requires an HTTP/HTTPS URL without userinfo, or about:blank")
		}
	case "evaluate":
		if strings.TrimSpace(input.Expression) == "" {
			return errors.New("evaluate requires an expression")
		}
	case "screenshot":
		if strings.TrimSpace(input.Path) == "" || !strings.EqualFold(filepath.Ext(input.Path), ".png") {
			return errors.New("screenshot requires a new .png output path")
		}
	case "stop":
		if strings.TrimSpace(input.SessionID) == "" {
			return errors.New("stop requires the sessionId returned by open")
		}
	default:
		return errors.New("unsupported browser action")
	}
	return nil
}

type browserToolResult struct {
	tooltypes.ToolResult
	input BrowserInput
}

func (r browserToolResult) StructuredData() tooltypes.StructuredToolResult {
	data := r.ToolResult.StructuredData()
	data.ToolName = "browser"
	metadata := tooltypes.BrowserMetadata{
		Action: r.input.Action, URL: r.input.URL, Expression: r.input.Expression,
		Path: r.input.Path, SessionID: r.input.SessionID, Output: r.GetResult(),
	}
	if r.input.Action == "open" && data.Success {
		var info browser.Info
		if json.Unmarshal([]byte(metadata.Output), &info) == nil {
			metadata.SessionID = info.SessionID
		}
	}
	if r.input.Action == "screenshot" {
		var image tooltypes.ViewImageMetadata
		if tooltypes.ExtractMetadata(data.Metadata, &image) && image.Path != "" {
			metadata.Path = image.Path
		}
	}
	data.Metadata = metadata
	return data
}

func (r browserToolResult) ContentParts() []tooltypes.ToolResultContentPart {
	if rich, ok := r.ToolResult.(tooltypes.MultiModalToolResult); ok {
		return rich.ContentParts()
	}
	return nil
}

func (t *BrowserTool) Execute(ctx context.Context, state tooltypes.State, parameters string) tooltypes.ToolResult {
	result, err := t.execute(ctx, state, parameters)
	if err != nil {
		result = tooltypes.BaseToolResult{Error: err.Error()}
	}
	var input BrowserInput
	_ = json.Unmarshal([]byte(parameters), &input)
	return browserToolResult{ToolResult: result, input: input}
}

func (t *BrowserTool) execute(ctx context.Context, state tooltypes.State, parameters string) (tooltypes.ToolResult, error) {
	if err := t.ValidateInput(state, parameters); err != nil {
		return nil, err
	}
	if t.controller == nil || state == nil {
		return nil, errors.New("runner browser is unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var input BrowserInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return nil, errors.Wrap(err, "invalid browser input")
	}
	cwd := state.WorkingDirectory()
	scope := browser.Scope{ConversationID: ToolContextFromContext(ctx).ConversationID, CWD: cwd}
	if scope.ConversationID == "" {
		return nil, errors.New("browser requires a conversation context")
	}
	if input.Action == "open" {
		info, err := t.controller.Open(ctx, scope)
		if err != nil {
			return nil, err
		}
		data, err := json.Marshal(info)
		return tooltypes.BaseToolResult{Result: string(data)}, err
	}
	if input.Action == "stop" {
		if err := t.controller.Stop(scope, input.SessionID); err != nil {
			return nil, err
		}
		return tooltypes.BaseToolResult{Result: "Conversation browser stopped."}, nil
	}
	var method string
	var params any
	switch input.Action {
	case "navigate":
		method, params = "Page.navigate", map[string]any{"url": input.URL}
	case "evaluate":
		method, params = "Runtime.evaluate", map[string]any{"expression": input.Expression, "returnByValue": true, "awaitPromise": true, "timeout": 25000}
	case "screenshot":
		method, params = "Page.captureScreenshot", map[string]any{"format": "png"}
	}
	payload, err := json.Marshal(params)
	if err != nil {
		return nil, errors.Wrap(err, "failed to encode browser command")
	}
	response, err := t.controller.Command(ctx, scope, method, payload)
	if err != nil {
		return nil, err
	}
	var result struct {
		ErrorText        string          `json:"errorText"`
		ExceptionDetails json.RawMessage `json:"exceptionDetails"`
		Data             string          `json:"data"`
	}
	if err := json.Unmarshal(response, &result); err != nil {
		return nil, errors.Wrap(err, "invalid browser response")
	}
	if result.ErrorText != "" {
		return nil, errors.New(result.ErrorText)
	}
	if len(result.ExceptionDetails) != 0 && string(result.ExceptionDetails) != "null" {
		return nil, errors.New("JavaScript evaluation failed: " + truncateMiddleByBytesEstimate(string(result.ExceptionDetails), 4096, false))
	}
	if input.Action != "screenshot" {
		return tooltypes.BaseToolResult{Result: truncateMiddleByBytesEstimate(string(response), 64*1024, false)}, nil
	}
	if len(result.Data) > 16*1024*1024 {
		return nil, errors.New("browser screenshot exceeds 16 MiB encoded limit")
	}
	image, err := base64.StdEncoding.DecodeString(result.Data)
	if err != nil || len(image) == 0 {
		return nil, errors.New("browser returned an invalid screenshot")
	}
	path := input.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create browser screenshot")
	}
	_, writeErr := file.Write(image)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return nil, errors.New("failed to save browser screenshot")
	}
	viewInput, _ := json.Marshal(ViewImageInput{Path: path})
	view := NewViewImageTool("", "").Execute(ctx, state, string(viewInput))
	if view.IsError() {
		_ = os.Remove(path)
	}
	return view, nil
}

func (*BrowserTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	var input BrowserInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return nil, err
	}
	return []attribute.KeyValue{attribute.String("action", input.Action)}, nil
}
