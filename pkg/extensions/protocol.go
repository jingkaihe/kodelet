package extensions

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
)

const protocolVersion = "2026-05-30"

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type cancelRequestParams struct {
	ID int64 `json:"id"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ToolRegistration is returned by an extension during initialization.
type ToolRegistration struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// CommandRegistration is returned by an extension during initialization.
type CommandRegistration struct {
	Name        string         `json:"name"`
	Aliases     []string       `json:"aliases,omitempty"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
	Kind        string         `json:"kind,omitempty"`
}

// Subscription declares an event handler registered by an extension.
type Subscription struct {
	Event    string `json:"event"`
	Priority int    `json:"priority,omitempty"`
}

type initializeParams struct {
	ProtocolVersion string                  `json:"protocolVersion"`
	Kodelet         map[string]any          `json:"kodelet"`
	Extension       initializeExtensionInfo `json:"extension"`
	Capabilities    map[string]any          `json:"capabilities"`
}

type initializeExtensionInfo struct {
	ID      string         `json:"id"`
	Config  map[string]any `json:"config"`
	CWD     string         `json:"cwd"`
	DataDir string         `json:"dataDir"`
}

// InitializeResult is returned by extension.initialize.
type InitializeResult struct {
	Name          string                `json:"name"`
	Version       string                `json:"version,omitempty"`
	Tools         []ToolRegistration    `json:"tools,omitempty"`
	Commands      []CommandRegistration `json:"commands,omitempty"`
	Subscriptions []Subscription        `json:"subscriptions,omitempty"`
}

type executeToolParams struct {
	Name    string               `json:"name"`
	Input   json.RawMessage      `json:"input"`
	Context ExtensionCallContext `json:"context"`
}

type executeCommandParams struct {
	Name       string               `json:"name"`
	Input      map[string]any       `json:"input"`
	Context    ExtensionCallContext `json:"context"`
	Invocation CommandInvocation    `json:"invocation"`
}

type eventParams struct {
	ID      string               `json:"id"`
	Event   string               `json:"event"`
	Context ExtensionCallContext `json:"context"`
	Payload any                  `json:"payload"`
}

// ExtensionCallContext is passed to extension tool/event/command calls.
type ExtensionCallContext struct {
	SessionID      string `json:"sessionId,omitempty"`
	ConversationID string `json:"conversationId,omitempty"`
	CWD            string `json:"cwd,omitempty"`
	Provider       string `json:"provider,omitempty"`
	Model          string `json:"model,omitempty"`
	Profile        string `json:"profile,omitempty"`
	RecipeName     string `json:"recipeName,omitempty"`
	InvokedBy      string `json:"invokedBy,omitempty"`
}

// ToolExecutionResult is returned by extension.tool.execute.
type ToolExecutionResult struct {
	Content string         `json:"content"`
	Data    map[string]any `json:"data,omitempty"`
	Error   string         `json:"error,omitempty"`
}

// CommandInvocation describes the user prompt that invoked an extension command.
type CommandInvocation struct {
	Raw         string         `json:"raw"`
	CommandName string         `json:"commandName"`
	Args        []string       `json:"args"`
	Flags       map[string]any `json:"flags"`
}

// CommandResult is returned by extension.command.execute.
type CommandResult struct {
	Action     string `json:"action"`
	Response   string `json:"response,omitempty"`
	Prompt     string `json:"prompt,omitempty"`
	RecipeName string `json:"recipeName,omitempty"`
}

// EventResult is returned by extension.event.handle.
type EventResult struct {
	Input            json.RawMessage    `json:"input,omitempty"`
	Block            *EventBlock        `json:"block,omitempty"`
	Output           json.RawMessage    `json:"output,omitempty"`
	Message          *string            `json:"message,omitempty"`
	SystemPrompt     *SystemPromptPatch `json:"systemPrompt,omitempty"`
	Tools            *ToolListPatch     `json:"tools,omitempty"`
	FollowUpMessages []string           `json:"followUpMessages,omitempty"`
}

// EventBlock asks Kodelet to block a mutable/blocking event.
type EventBlock struct {
	Reason string `json:"reason"`
}

// SystemPromptPatch describes an agent.init system prompt mutation.
type SystemPromptPatch struct {
	Prepend *string `json:"prepend,omitempty"`
	Append  *string `json:"append,omitempty"`
	Replace *string `json:"replace,omitempty"`
}

// ToolListPatch describes an agent.init mutation to the tool allowlist.
type ToolListPatch struct {
	Disable []string `json:"disable,omitempty"`
	Enable  []string `json:"enable,omitempty"`
}

type rpcClient struct {
	reader  *bufio.Reader
	writer  io.Writer
	timeout time.Duration
	nextID  int64
	mu      sync.Mutex
}

func newRPCClient(reader io.Reader, writer io.Writer, timeout time.Duration) *rpcClient {
	if timeout == 0 {
		timeout = DefaultConfig().Timeout
	}
	return &rpcClient{
		reader:  bufio.NewReader(reader),
		writer:  writer,
		timeout: timeout,
	}
}

func (c *rpcClient) call(ctx context.Context, method string, params any, result any) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := ctx.Deadline(); !ok && c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	c.nextID++
	req := rpcRequest{JSONRPC: "2.0", ID: c.nextID, Method: method, Params: params}
	payload, err := json.Marshal(req)
	if err != nil {
		return errors.Wrap(err, "failed to marshal rpc request")
	}

	if err := writeFrame(c.writer, payload); err != nil {
		return err
	}

	respCh := make(chan rpcResponse, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, err := readResponse(c.reader)
		if err != nil {
			errCh <- err
			return
		}
		respCh <- resp
	}()

	select {
	case <-ctx.Done():
		_ = c.cancel(req.ID)
		return ctx.Err()
	case err := <-errCh:
		return err
	case resp := <-respCh:
		if resp.ID != req.ID {
			return errors.Errorf("unexpected rpc response id %d, want %d", resp.ID, req.ID)
		}
		if resp.Error != nil {
			return errors.Errorf("extension rpc error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		if result == nil || len(resp.Result) == 0 {
			return nil
		}
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return errors.Wrap(err, "failed to unmarshal rpc result")
		}
		return nil
	}
}

func (c *rpcClient) cancel(id int64) error {
	notif := rpcNotification{JSONRPC: "2.0", Method: "$/cancelRequest", Params: cancelRequestParams{ID: id}}
	payload, err := json.Marshal(notif)
	if err != nil {
		return errors.Wrap(err, "failed to marshal rpc cancel request")
	}
	return writeFrame(c.writer, payload)
}

func writeFrame(writer io.Writer, payload []byte) error {
	_, err := fmt.Fprintf(writer, "Content-Length: %d\r\n\r\n%s", len(payload), payload)
	return errors.Wrap(err, "failed to write rpc frame")
}

func readResponse(reader *bufio.Reader) (rpcResponse, error) {
	payload, err := readFrame(reader)
	if err != nil {
		return rpcResponse{}, err
	}
	var resp rpcResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return rpcResponse{}, errors.Wrap(err, "failed to unmarshal rpc response")
	}
	return resp, nil
}

func readFrame(reader *bufio.Reader) ([]byte, error) {
	contentLength := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, errors.Wrap(err, "failed to read rpc header")
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(key), "Content-Length") {
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, errors.Wrap(err, "invalid Content-Length header")
			}
			contentLength = parsed
		}
	}
	if contentLength < 0 {
		return nil, errors.New("missing Content-Length header")
	}
	payload := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, errors.Wrap(err, "failed to read rpc payload")
	}
	return payload, nil
}
