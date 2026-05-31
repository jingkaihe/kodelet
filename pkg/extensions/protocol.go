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

type rpcIncomingMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcHostRequestHandler interface {
	HandleRPCRequest(ctx context.Context, method string, params json.RawMessage) (any, *rpcError)
}

// ToolRegistration is returned by an extension during initialization.
type ToolRegistration struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"inputSchema"`
	TimeoutInSec *float64       `json:"timeoutInSec,omitempty"`
}

// CommandRegistration is returned by an extension during initialization.
type CommandRegistration struct {
	Name         string         `json:"name"`
	Aliases      []string       `json:"aliases,omitempty"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"inputSchema,omitempty"`
	Kind         string         `json:"kind,omitempty"`
	TimeoutInSec *float64       `json:"timeoutInSec,omitempty"`
}

// Subscription declares an event handler registered by an extension.
type Subscription struct {
	Event        string   `json:"event"`
	Priority     int      `json:"priority,omitempty"`
	TimeoutInSec *float64 `json:"timeoutInSec,omitempty"`
}

type initializeParams struct {
	ProtocolVersion string                  `json:"protocolVersion"`
	Kodelet         map[string]any          `json:"kodelet"`
	Extension       initializeExtensionInfo `json:"extension"`
	Capabilities    map[string]any          `json:"capabilities"`
}

type uiInputCapability struct {
	Input bool `json:"input"`
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
	reader *bufio.Reader
	writer io.Writer
	nextID int64
	mu     sync.Mutex
}

func newRPCClient(reader io.Reader, writer io.Writer) *rpcClient {
	return &rpcClient{
		reader: bufio.NewReader(reader),
		writer: writer,
	}
}

func (c *rpcClient) call(ctx context.Context, method string, params any, result any) error {
	return c.callWithHostHandler(ctx, method, params, result, nil)
}

func (c *rpcClient) callWithHostHandler(ctx context.Context, method string, params any, result any, handler rpcHostRequestHandler) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.nextID++
	req := rpcRequest{JSONRPC: "2.0", ID: c.nextID, Method: method, Params: params}
	payload, err := json.Marshal(req)
	if err != nil {
		return errors.Wrap(err, "failed to marshal rpc request")
	}

	if err := writeFrame(c.writer, payload); err != nil {
		return err
	}

	messageCh := make(chan rpcIncomingMessage, 1)
	errCh := make(chan error, 1)
	startRead := func() {
		go func() {
			msg, err := readIncomingMessage(c.reader)
			if err != nil {
				errCh <- err
				return
			}
			messageCh <- msg
		}()
	}
	startRead()

	for {
		select {
		case <-ctx.Done():
			_ = c.cancel(req.ID)
			return ctx.Err()
		case err := <-errCh:
			return err
		case msg := <-messageCh:
			messageCh = make(chan rpcIncomingMessage, 1)
			errCh = make(chan error, 1)
			if msg.Method != "" {
				if err := c.handleIncomingRequest(ctx, msg, handler); err != nil {
					return err
				}
				startRead()
				continue
			}

			resp, err := incomingResponse(msg)
			if err != nil {
				return err
			}
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
}

func (c *rpcClient) handleIncomingRequest(ctx context.Context, msg rpcIncomingMessage, handler rpcHostRequestHandler) error {
	if len(msg.ID) == 0 || string(msg.ID) == "null" {
		return nil
	}

	var response rpcResponse
	response.JSONRPC = "2.0"
	if err := json.Unmarshal(msg.ID, &response.ID); err != nil {
		response.Error = &rpcError{Code: -32600, Message: "invalid request id"}
		return c.writeResponse(response)
	}

	if handler == nil {
		response.Error = &rpcError{Code: -32601, Message: "host request method not found"}
		return c.writeResponse(response)
	}

	result, rpcErr := handler.HandleRPCRequest(ctx, msg.Method, msg.Params)
	if rpcErr != nil {
		response.Error = rpcErr
		return c.writeResponse(response)
	}
	if result != nil {
		payload, err := json.Marshal(result)
		if err != nil {
			response.Error = &rpcError{Code: -32603, Message: err.Error()}
			return c.writeResponse(response)
		}
		response.Result = payload
	}
	return c.writeResponse(response)
}

func (c *rpcClient) writeResponse(response rpcResponse) error {
	payload, err := json.Marshal(response)
	if err != nil {
		return errors.Wrap(err, "failed to marshal rpc response")
	}
	return writeFrame(c.writer, payload)
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
	msg, err := readIncomingMessage(reader)
	if err != nil {
		return rpcResponse{}, err
	}
	return incomingResponse(msg)
}

func readIncomingMessage(reader *bufio.Reader) (rpcIncomingMessage, error) {
	payload, err := readFrame(reader)
	if err != nil {
		return rpcIncomingMessage{}, err
	}
	var msg rpcIncomingMessage
	if err := json.Unmarshal(payload, &msg); err != nil {
		return rpcIncomingMessage{}, errors.Wrap(err, "failed to unmarshal rpc response")
	}
	return msg, nil
}

func incomingResponse(msg rpcIncomingMessage) (rpcResponse, error) {
	var resp rpcResponse
	resp.JSONRPC = msg.JSONRPC
	resp.Result = msg.Result
	resp.Error = msg.Error
	if len(msg.ID) == 0 {
		return rpcResponse{}, errors.New("missing rpc response id")
	}
	if err := json.Unmarshal(msg.ID, &resp.ID); err != nil {
		return rpcResponse{}, errors.Wrap(err, "failed to unmarshal rpc response id")
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
