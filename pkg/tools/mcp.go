package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-multierror"
	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/logger"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/jingkaihe/kodelet/pkg/version"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
	"go.opentelemetry.io/otel/attribute"
	"gopkg.in/yaml.v2"
)

var _ tooltypes.Tool = &MCPTool{}

// ErrMCPDisabled is returned when MCP is disabled via configuration
var ErrMCPDisabled = errors.New("MCP is disabled via configuration")

// MCPServerType represents the type of MCP server
type MCPServerType string

const (
	// MCPServerTypeStdio represents a stdio-based MCP server
	MCPServerTypeStdio MCPServerType = "stdio"
	// MCPServerTypeSSE represents an SSE-based MCP server
	MCPServerTypeSSE MCPServerType = "sse"
	// MCPServerTypeHTTP represents a streamable HTTP-based MCP server
	MCPServerTypeHTTP MCPServerType = "http"
)

// MCPServerConfig holds the configuration for an MCP server
type MCPServerConfig struct {
	ServerType    MCPServerType     `json:"server_type" yaml:"server_type"`         // stdio, sse (deprecated), or http (streamable HTTP)
	Command       string            `json:"command" yaml:"command"`                 // stdio: command to start the server
	Args          []string          `json:"args" yaml:"args"`                       // stdio: arguments to pass to the server
	Envs          map[string]string `json:"envs" yaml:"envs"`                       // stdio: environment variables to set
	BaseURL       string            `json:"base_url" yaml:"base_url"`               // http/sse: base URL of the server
	Headers       map[string]string `json:"headers" yaml:"headers"`                 // http/sse: headers to send to the server
	ToolWhiteList []string          `json:"tool_white_list" yaml:"tool_white_list"` // optional tool white list
}

func normalizeMCPServerType(serverType MCPServerType) MCPServerType {
	switch strings.ToLower(strings.TrimSpace(string(serverType))) {
	case "":
		return ""
	case string(MCPServerTypeStdio):
		return MCPServerTypeStdio
	case string(MCPServerTypeSSE):
		return MCPServerTypeSSE
	case string(MCPServerTypeHTTP), "streamable_http", "streamable-http", "streamablehttp":
		return MCPServerTypeHTTP
	default:
		return MCPServerType(strings.ToLower(strings.TrimSpace(string(serverType))))
	}
}

// MCPConfig holds the configuration for all MCP servers
type MCPConfig struct {
	Servers map[string]MCPServerConfig `json:"servers"`
}

type authenticatedStreamableHTTPTransport struct {
	inner       *transport.StreamableHTTP
	serverURL   string
	headers     map[string]string
	headerFunc  transport.HTTPHeaderFunc
	protocolMu  sync.RWMutex
	protocolVer string
}

func newAuthenticatedStreamableHTTPTransport(
	serverURL string,
	headers map[string]string,
) (*authenticatedStreamableHTTPTransport, error) {
	inner, err := transport.NewStreamableHTTP(serverURL, transport.WithHTTPHeaders(headers))
	if err != nil {
		return nil, err
	}

	return &authenticatedStreamableHTTPTransport{
		inner:      inner,
		serverURL:  serverURL,
		headers:    mapsClone(headers),
		headerFunc: nil,
	}, nil
}

func (t *authenticatedStreamableHTTPTransport) Start(ctx context.Context) error {
	return t.inner.Start(ctx)
}

func (t *authenticatedStreamableHTTPTransport) SendRequest(
	ctx context.Context,
	request transport.JSONRPCRequest,
) (*transport.JSONRPCResponse, error) {
	return t.inner.SendRequest(ctx, request)
}

func (t *authenticatedStreamableHTTPTransport) SendNotification(
	ctx context.Context,
	notification mcp.JSONRPCNotification,
) error {
	return t.inner.SendNotification(ctx, notification)
}

func (t *authenticatedStreamableHTTPTransport) SetNotificationHandler(
	handler func(mcp.JSONRPCNotification),
) {
	t.inner.SetNotificationHandler(handler)
}

func (t *authenticatedStreamableHTTPTransport) Close() error {
	var closeErr error
	if len(t.headers) > 0 || t.headerFunc != nil {
		closeErr = t.closeWithHeaders()
	}

	if err := t.inner.Close(); err != nil {
		closeErr = multierror.Append(closeErr, err)
	}

	return closeErr
}

func (t *authenticatedStreamableHTTPTransport) GetSessionId() string {
	return t.inner.GetSessionId()
}

func (t *authenticatedStreamableHTTPTransport) SetProtocolVersion(version string) {
	t.protocolMu.Lock()
	t.protocolVer = version
	t.protocolMu.Unlock()

	t.inner.SetProtocolVersion(version)
}

func (t *authenticatedStreamableHTTPTransport) SetRequestHandler(handler transport.RequestHandler) {
	t.inner.SetRequestHandler(handler)
}

func (t *authenticatedStreamableHTTPTransport) closeWithHeaders() error {
	sessionID := t.inner.GetSessionId()
	if strings.TrimSpace(sessionID) == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, t.serverURL, nil)
	if err != nil {
		return errors.Wrap(err, "failed to create authenticated MCP close request")
	}

	req.Header.Set(transport.HeaderKeySessionID, sessionID)

	t.protocolMu.RLock()
	protocolVersion := t.protocolVer
	t.protocolMu.RUnlock()
	if protocolVersion != "" {
		req.Header.Set(transport.HeaderKeyProtocolVersion, protocolVersion)
	}

	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	if t.headerFunc != nil {
		for k, v := range t.headerFunc(ctx) {
			req.Header.Set(k, v)
		}
	}

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return errors.Wrap(err, "failed to send authenticated MCP close request")
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= http.StatusBadRequest {
		if len(body) == 0 {
			return errors.Errorf("authenticated MCP close request failed with status %d", resp.StatusCode)
		}
		return errors.Errorf("authenticated MCP close request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	return nil
}

func mapsClone(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}

	cloned := make(map[string]string, len(headers))
	for k, v := range headers {
		cloned[k] = v
	}
	return cloned
}

func newMCPClient(config MCPServerConfig) (*client.Client, error) {
	if config.ServerType == "" {
		if config.BaseURL != "" {
			config.ServerType = MCPServerTypeSSE
		} else if config.Command != "" {
			config.ServerType = MCPServerTypeStdio
		} else {
			return nil, errors.New("server_type is required")
		}
	}

	switch normalizeMCPServerType(config.ServerType) {
	case MCPServerTypeStdio:
		if config.Command == "" {
			return nil, errors.New("command is required for stdio server")
		}
		envArgs := []string{}
		for k, v := range config.Envs {
			if strings.HasPrefix(v, "$") {
				v = os.Getenv(strings.TrimPrefix(v, "$"))
			}
			envArgs = append(envArgs, fmt.Sprintf("%s=%s", k, v))
		}
		tp := transport.NewStdio(config.Command, envArgs, config.Args...)
		return client.NewClient(tp), nil
	case MCPServerTypeSSE:
		if config.BaseURL == "" {
			return nil, errors.New("base_url is required for sse server")
		}
		tp, err := transport.NewSSE(config.BaseURL, transport.WithHeaders(config.Headers))
		if err != nil {
			return nil, err
		}
		return client.NewClient(tp), nil
	case MCPServerTypeHTTP:
		if config.BaseURL == "" {
			return nil, errors.New("base_url is required for http server")
		}
		tp, err := newAuthenticatedStreamableHTTPTransport(config.BaseURL, config.Headers)
		if err != nil {
			return nil, err
		}
		return client.NewClient(tp), nil
	default:
		return nil, errors.New("invalid server type")
	}
}

// MCPManager manages MCP clients and tools
type MCPManager struct {
	clients map[string]*client.Client

	whiteList map[string][]string
	owned     map[string]bool
}

// NewMCPManager creates a new MCP manager with the given configuration
func NewMCPManager(config MCPConfig) (*MCPManager, error) {
	clients := &MCPManager{
		clients:   make(map[string]*client.Client),
		whiteList: make(map[string][]string),
		owned:     make(map[string]bool),
	}
	for name, config := range config.Servers {
		client, err := newMCPClient(config)
		if err != nil {
			return nil, err
		}
		clients.clients[name] = client
		clients.whiteList[name] = config.ToolWhiteList
		clients.owned[name] = true
	}
	return clients, nil
}

// Initialize initializes all MCP clients
func (m *MCPManager) Initialize(ctx context.Context) error {
	now := time.Now()
	logger.G(ctx).WithField("time", now).Debug("initializing mcp manager")
	initClient := func(c *client.Client) error {
		initReq := mcp.InitializeRequest{}
		initReq.Params.ClientInfo = mcp.Implementation{
			Name:    "kodelet",
			Version: version.Version,
		}
		initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
		err := c.Start(ctx)
		if err != nil {
			return err
		}
		_, err = c.Initialize(ctx, initReq)
		return err
	}
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		multiErr error
	)
	wg.Add(len(m.clients))
	for _, c := range m.clients {
		go func(c *client.Client) {
			defer wg.Done()
			err := initClient(c)
			if err != nil {
				mu.Lock()
				multiErr = multierror.Append(multiErr, err)
				mu.Unlock()
			}
		}(c)
	}
	wg.Wait()
	logger.G(ctx).WithField("time", time.Since(now)).Debug("mcp manager initialized")
	return nil
}

// Close closes all MCP clients
func (m *MCPManager) Close(ctx context.Context) error {
	var multiErr error
	for name, client := range m.clients {
		if !m.owned[name] {
			continue
		}
		err := client.Close()
		if err != nil {
			multiErr = multierror.Append(multiErr, err)
			logger.G(ctx).WithField("name", name).WithError(err).Error("failed to close mcp client")
		}
	}
	return multiErr
}

// Merge adds all clients from another MCPManager into this one.
// If a client with the same name already exists, it is skipped.
func (m *MCPManager) Merge(other *MCPManager) {
	if other == nil {
		return
	}
	for name, client := range other.clients {
		if _, exists := m.clients[name]; !exists {
			m.clients[name] = client
			m.whiteList[name] = other.whiteList[name]
			m.owned[name] = other.owned[name]
		}
	}
}

// Clone returns a shallow, non-owning copy of the manager so callers can compose
// per-session views without mutating or closing the shared configured MCP manager.
func (m *MCPManager) Clone() *MCPManager {
	if m == nil {
		return nil
	}

	clone := &MCPManager{
		clients:   make(map[string]*client.Client, len(m.clients)),
		whiteList: make(map[string][]string, len(m.whiteList)),
		owned:     make(map[string]bool, len(m.owned)),
	}

	for name, c := range m.clients {
		clone.clients[name] = c
		clone.owned[name] = false
	}
	for name, whiteList := range m.whiteList {
		clone.whiteList[name] = slices.Clone(whiteList)
	}

	return clone
}

// ListMCPToolsIter iterates over all MCP tools from all servers and calls the iter function for each server.
func (m *MCPManager) ListMCPToolsIter(
	ctx context.Context,
	iter func(serverName string, client *client.Client, tools []mcp.Tool),
	errHandler ...func(err error),
) {
	listTools := func(c *client.Client, serverName string) ([]mcp.Tool, error) {
		listToolResult, err := c.ListTools(ctx, mcp.ListToolsRequest{})
		if err != nil {
			return nil, err
		}
		tools := []mcp.Tool{}
		for _, tool := range listToolResult.Tools {
			if toolWhiteListed(tool, m.whiteList[serverName]) {
				tools = append(tools, tool)
			}
		}
		return tools, nil
	}

	for name, c := range m.clients {
		tools, err := listTools(c, name)
		if err != nil {
			if len(errHandler) > 0 {
				errHandler[0](err)
				continue
			}
			logger.G(ctx).WithField("name", name).WithError(err).Error("failed to list mcp tools")
		} else {
			iter(name, c, tools)
		}
	}
}

// ListMCPTools lists all available MCP tools from all clients
func (m *MCPManager) ListMCPTools(ctx context.Context) ([]MCPTool, error) {
	now := time.Now()
	logger.G(ctx).WithField("time", now).Debug("listing mcp tools")
	defer func() {
		logger.G(ctx).WithField("time", time.Since(now)).Debug("mcp tools listed")
	}()

	var (
		multiErr error
		tools    []MCPTool
	)

	m.ListMCPToolsIter(ctx, func(serverName string, c *client.Client, mcpTools []mcp.Tool) {
		for _, tool := range mcpTools {
			tools = append(tools, *NewMCPTool(c, tool, serverName))
		}
	}, func(err error) {
		multiErr = multierror.Append(multiErr, err)
	})

	if multiErr != nil {
		return nil, multiErr
	}
	logger.G(ctx).WithField("time", time.Since(now)).Debug("mcp tools listed")
	return tools, nil
}

// GetMCPClient gets the MCP client by name
func (m *MCPManager) GetMCPClient(clientName string) (*client.Client, error) {
	client, ok := m.clients[clientName]
	if !ok {
		return nil, errors.New("client not found")
	}
	return client, nil
}

func toolWhiteListed(tool mcp.Tool, whiteList []string) bool {
	return len(whiteList) == 0 || slices.Contains(whiteList, tool.GetName())
}

// LoadMCPConfigFromViper loads MCP servers configuration from Viper
func LoadMCPConfigFromViper() (MCPConfig, error) {
	type config struct {
		MCP MCPConfig `yaml:"mcp"`
	}
	filename := viper.ConfigFileUsed()
	if filename == "" {
		return MCPConfig{}, nil
	}
	f, err := os.Open(filename)
	if err != nil {
		return MCPConfig{}, errors.Wrap(err, "failed to open config file")
	}
	defer f.Close()

	var c config
	if err := yaml.NewDecoder(f).Decode(&c); err != nil {
		return MCPConfig{}, errors.Wrap(err, "failed to decode config file")
	}
	return c.MCP, nil
}

// CreateMCPManagerFromViper creates a new MCPManager from Viper configuration
// Returns ErrMCPDisabled if MCP is disabled via configuration
func CreateMCPManagerFromViper(ctx context.Context) (*MCPManager, error) {
	// Check if MCP is disabled via config (mcp.enabled defaults to true)
	if viper.IsSet("mcp.enabled") && !viper.GetBool("mcp.enabled") {
		return nil, ErrMCPDisabled
	}

	// Load configuration from Viper
	config, err := LoadMCPConfigFromViper()
	if err != nil {
		return nil, errors.Wrap(err, "failed to load MCP servers config")
	}

	// Create the manager
	manager, err := NewMCPManager(config)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create MCP manager")
	}

	// Initialize the manager
	if err := manager.Initialize(ctx); err != nil {
		_ = manager.Close(ctx)
		return nil, errors.Wrap(err, "failed to initialize MCP manager")
	}

	return manager, nil
}

// MCPTool wraps an MCP tool with its client
type MCPTool struct {
	client             *client.Client
	mcpToolInputSchema mcp.ToolInputSchema
	mcpToolName        string
	mcpToolDescription string
	serverName         string
}

// NewMCPTool creates a new MCP tool wrapper
func NewMCPTool(client *client.Client, tool mcp.Tool, serverName string) *MCPTool {
	return &MCPTool{
		client:             client,
		mcpToolInputSchema: tool.InputSchema,
		mcpToolName:        tool.GetName(),
		mcpToolDescription: tool.Description,
		serverName:         serverName,
	}
}

// ServerName returns the name of the MCP server this tool belongs to
func (t *MCPTool) ServerName() string {
	return t.serverName
}

// MCPToolName returns the original MCP tool name (without the "mcp_" prefix)
func (t *MCPTool) MCPToolName() string {
	return t.mcpToolName
}

// MCPToolResult represents the result of an MCP tool execution
type MCPToolResult struct {
	toolName      string
	mcpToolName   string
	serverName    string
	parameters    map[string]any
	content       []tooltypes.MCPContent
	contentText   string
	executionTime time.Duration
	result        string
	err           string
}

// GetResult returns the tool output
func (r *MCPToolResult) GetResult() string {
	return r.result
}

// GetError returns the error message
func (r *MCPToolResult) GetError() string {
	return r.err
}

// IsError returns true if the result contains an error
func (r *MCPToolResult) IsError() bool {
	return r.err != ""
}

// AssistantFacing returns the string representation for the AI assistant
func (r *MCPToolResult) AssistantFacing() string {
	return tooltypes.StringifyToolResult(r.result, r.err)
}

// StructuredData returns structured metadata about the MCP tool execution
func (r *MCPToolResult) StructuredData() tooltypes.StructuredToolResult {
	result := tooltypes.StructuredToolResult{
		ToolName:  r.toolName,
		Success:   !r.IsError(),
		Timestamp: time.Now(),
	}

	if r.IsError() {
		result.Error = r.GetError()
		return result
	}

	result.Metadata = &tooltypes.MCPToolMetadata{
		MCPToolName:   r.mcpToolName,
		ServerName:    r.serverName,
		Parameters:    r.parameters,
		Content:       r.content,
		ContentText:   r.contentText,
		ExecutionTime: r.executionTime,
	}

	return result
}

// Name returns the name of the tool
func (t *MCPTool) Name() string {
	return fmt.Sprintf("mcp__%s_%s", t.serverName, t.mcpToolName)
}

// Description returns the description of the tool
func (t *MCPTool) Description() string {
	return t.mcpToolDescription
}

// GenerateSchema generates the JSON schema for the tool's input parameters
func (t *MCPTool) GenerateSchema() *jsonschema.Schema {
	b, err := json.Marshal(t.mcpToolInputSchema)
	if err != nil {
		return nil
	}

	var schema *jsonschema.Schema
	err = json.Unmarshal(b, &schema)
	if err != nil {
		return nil
	}
	return schema
}

// TracingKVs returns tracing key-value pairs for observability
func (t *MCPTool) TracingKVs(_ string) ([]attribute.KeyValue, error) {
	return nil, nil
}

// ValidateInput validates the input parameters for the tool
func (t *MCPTool) ValidateInput(_ tooltypes.State, _ string) error {
	return nil
}

// callMCPServer calls the MCP server with the given input arguments
func (t *MCPTool) callMCPServer(ctx context.Context, input map[string]any) (*mcp.CallToolResult, error) {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = input
	req.Params.Name = t.mcpToolName
	return t.client.CallTool(ctx, req)
}

// Execute runs the MCP tool and returns the result
func (t *MCPTool) Execute(ctx context.Context, _ tooltypes.State, parameters string) tooltypes.ToolResult {
	var input map[string]any
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return &MCPToolResult{
			toolName:    t.Name(),
			mcpToolName: t.mcpToolName,
			err:         err.Error(),
		}
	}

	startTime := time.Now()
	result, err := t.callMCPServer(ctx, input)
	executionTime := time.Since(startTime)

	if err != nil {
		return &MCPToolResult{
			toolName:    t.Name(),
			mcpToolName: t.mcpToolName,
			err:         err.Error(),
		}
	}

	// Extract content for both formats
	content := ""
	var mcpContents []tooltypes.MCPContent

	for _, c := range result.Content {
		if v, ok := c.(mcp.TextContent); ok {
			content += v.Text
			mcpContents = append(mcpContents, tooltypes.MCPContent{
				Type: "text",
				Text: v.Text,
			})
		} else {
			// Handle other content types
			content += fmt.Sprintf("%v", c)
			mcpContents = append(mcpContents, tooltypes.MCPContent{
				Type: "unknown",
				Text: fmt.Sprintf("%v", c),
			})
		}
	}

	return &MCPToolResult{
		toolName:      t.Name(),
		mcpToolName:   t.mcpToolName,
		parameters:    input,
		content:       mcpContents,
		contentText:   content,
		executionTime: executionTime,
		result:        content,
	}
}
