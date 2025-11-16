package tools

import (
	"context"
	"encoding/json"
	"fmt"
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

// MCPServerType represents the type of MCP server
type MCPServerType string

const (
	// MCPServerTypeStdio represents a stdio-based MCP server
	MCPServerTypeStdio MCPServerType = "stdio"
	// MCPServerTypeSSE represents an SSE-based MCP server
	MCPServerTypeSSE MCPServerType = "sse"
)

// MCPServerConfig holds the configuration for an MCP server
type MCPServerConfig struct {
	ServerType    MCPServerType     `json:"server_type" yaml:"server_type"`         // stdio or sse
	Command       string            `json:"command" yaml:"command"`                 // stdio: command to start the server
	Args          []string          `json:"args" yaml:"args"`                       // stdio: arguments to pass to the server
	Envs          map[string]string `json:"envs" yaml:"envs"`                       // stdio: environment variables to set
	BaseURL       string            `json:"base_url" yaml:"base_url"`               // sse: base URL of the server
	Headers       map[string]string `json:"headers" yaml:"headers"`                 // sse: headers to send to the server
	ToolWhiteList []string          `json:"tool_white_list" yaml:"tool_white_list"` // sse: tool white list
}

// MCPConfig holds the configuration for all MCP servers
type MCPConfig struct {
	Servers map[string]MCPServerConfig `json:"servers"`
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

	switch config.ServerType {
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
	default:
		return nil, errors.New("invalid server type")
	}
}

// MCPManager manages MCP clients and tools
type MCPManager struct {
	clients map[string]*client.Client

	whiteList map[string][]string
}

// NewMCPManager creates a new MCP manager with the given configuration
func NewMCPManager(config MCPConfig) (*MCPManager, error) {
	clients := &MCPManager{
		clients:   make(map[string]*client.Client),
		whiteList: make(map[string][]string),
	}
	for name, config := range config.Servers {
		client, err := newMCPClient(config)
		if err != nil {
			return nil, err
		}
		clients.clients[name] = client
		clients.whiteList[name] = config.ToolWhiteList
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
	for name, client := range m.clients {
		err := client.Close()
		if err != nil {
			logger.G(ctx).WithField("name", name).WithError(err).Error("failed to close mcp client")
		}
	}
	return nil
}

// ListMCPTools lists all available MCP tools from all clients
func (m *MCPManager) ListMCPTools(ctx context.Context) ([]MCPTool, error) {
	now := time.Now()
	logger.G(ctx).WithField("time", now).Debug("listing mcp tools")
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
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		multiErr error
		tools    []MCPTool
	)
	wg.Add(len(m.clients))
	for name, c := range m.clients {
		go func(c *client.Client, serverName string) {
			defer wg.Done()
			toolsResult, err := listTools(c, serverName)
			if err != nil {
				mu.Lock()
				multiErr = multierror.Append(multiErr, err)
				mu.Unlock()
			} else {
				mu.Lock()
				for _, tool := range toolsResult {
					tools = append(tools, *NewMCPTool(c, tool, serverName))
				}
				mu.Unlock()
			}
		}(c, name)
	}
	wg.Wait()
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
func CreateMCPManagerFromViper(ctx context.Context) (*MCPManager, error) {
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
		return nil, errors.Wrap(err, "failed to initialize MCP manager")
	}

	return manager, nil
}

// MCPTool wraps an MCP tool with its client
type MCPTool struct {
	client              *client.Client
	mcpToolInputSchema  mcp.ToolInputSchema
	mcpToolOutputSchema mcp.ToolOutputSchema
	mcpToolName         string
	mcpToolDescription  string
	serverName          string
}

// NewMCPTool creates a new MCP tool wrapper
func NewMCPTool(client *client.Client, tool mcp.Tool, serverName string) *MCPTool {
	return &MCPTool{
		client:              client,
		mcpToolInputSchema:  tool.InputSchema,
		mcpToolOutputSchema: tool.OutputSchema,
		mcpToolName:         tool.GetName(),
		mcpToolDescription:  tool.Description,
		serverName:          serverName,
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
	return fmt.Sprintf("mcp_%s", t.mcpToolName)
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

// GenerateOutputSchema generates a JSON schema for the tool's output type
func (t *MCPTool) GenerateOutputSchema() *jsonschema.Schema {
	b, err := json.Marshal(t.mcpToolOutputSchema)
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
	req := mcp.CallToolRequest{}
	req.Params.Arguments = input
	req.Params.Name = t.mcpToolName
	result, err := t.client.CallTool(ctx, req)
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
