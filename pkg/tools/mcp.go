package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/hashicorp/go-multierror"
	"github.com/invopop/jsonschema"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/jingkaihe/kodelet/pkg/version"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"go.opentelemetry.io/otel/attribute"
	"gopkg.in/yaml.v2"
)

var (
	_ tooltypes.Tool = &MCPTool{}
)

type MCPServerType string

const (
	MCPServerTypeStdio MCPServerType = "stdio"
	MCPServerTypeSSE   MCPServerType = "sse"
)

type MCPServerConfig struct {
	ServerType    MCPServerType     `json:"server_type" yaml:"server_type"`         // stdio or sse
	Command       string            `json:"command" yaml:"command"`                 // stdio: command to start the server
	Args          []string          `json:"args" yaml:"args"`                       // stdio: arguments to pass to the server
	Envs          map[string]string `json:"envs" yaml:"envs"`                       // stdio: environment variables to set
	BaseURL       string            `json:"base_url" yaml:"base_url"`               // sse: base URL of the server
	Headers       map[string]string `json:"headers" yaml:"headers"`                 // sse: headers to send to the server
	ToolWhiteList []string          `json:"tool_white_list" yaml:"tool_white_list"` // sse: tool white list
}

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

type MCPManager struct {
	clients map[string]*client.Client

	whiteList map[string][]string
}

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

func (m *MCPManager) Initialize(ctx context.Context) error {
	now := time.Now()
	logrus.WithField("time", now).Debug("initializing mcp manager")
	var initClient = func(c *client.Client) error {
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
	logrus.WithField("time", time.Since(now)).Debug("mcp manager initialized")
	return nil
}

func (m *MCPManager) Close(ctx context.Context) error {
	for name, client := range m.clients {
		err := client.Close()
		if err != nil {
			logrus.WithField("name", name).WithError(err).Error("failed to close mcp client")
		}
	}
	return nil
}

func (m *MCPManager) ListMCPTools(ctx context.Context) ([]MCPTool, error) {
	now := time.Now()
	logrus.WithField("time", now).Debug("listing mcp tools")
	var listTools = func(c *client.Client, serverName string) ([]mcp.Tool, error) {
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
					tools = append(tools, *NewMCPTool(c, tool))
				}
				mu.Unlock()
			}
		}(c, name)
	}
	wg.Wait()
	if multiErr != nil {
		return nil, multiErr
	}
	logrus.WithField("time", time.Since(now)).Debug("mcp tools listed")
	return tools, nil
}

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
		return MCPConfig{}, fmt.Errorf("failed to open config file: %w", err)
	}
	defer f.Close()

	var c config
	if err := yaml.NewDecoder(f).Decode(&c); err != nil {
		return MCPConfig{}, fmt.Errorf("failed to decode config file: %w", err)
	}
	return c.MCP, nil
}

// CreateMCPManagerFromViper creates a new MCPManager from Viper configuration
func CreateMCPManagerFromViper(ctx context.Context) (*MCPManager, error) {
	// Load configuration from Viper
	config, err := LoadMCPConfigFromViper()
	if err != nil {
		return nil, fmt.Errorf("failed to load MCP servers config: %w", err)
	}

	// Create the manager
	manager, err := NewMCPManager(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create MCP manager: %w", err)
	}

	// Initialize the manager
	if err := manager.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize MCP manager: %w", err)
	}

	return manager, nil
}

type MCPTool struct {
	client             *client.Client
	mcpToolInputSchema mcp.ToolInputSchema
	mcpToolName        string
	mcpToolDescription string
}

func NewMCPTool(client *client.Client, tool mcp.Tool) *MCPTool {
	return &MCPTool{
		client:             client,
		mcpToolInputSchema: tool.InputSchema,
		mcpToolName:        tool.GetName(),
		mcpToolDescription: tool.Description,
	}
}
func (t *MCPTool) Name() string {
	return fmt.Sprintf("mcp_%s", t.mcpToolName)
}

func (t *MCPTool) Description() string {
	return t.mcpToolDescription
}
func (t *MCPTool) GenerateSchema() *jsonschema.Schema {
	b, err := t.mcpToolInputSchema.MarshalJSON()
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

func (t *MCPTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	return nil, nil
}

func (t *MCPTool) ValidateInput(state tooltypes.State, parameters string) error {
	return nil
}

func (t *MCPTool) Execute(ctx context.Context, state tooltypes.State, parameters string) tooltypes.ToolResult {
	var input map[string]any
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return tooltypes.BaseToolResult{
			Error: err.Error(),
		}
	}

	req := mcp.CallToolRequest{}
	req.Params.Arguments = input
	req.Params.Name = t.mcpToolName
	result, err := t.client.CallTool(ctx, req)
	if err != nil {
		return tooltypes.BaseToolResult{
			Error: err.Error(),
		}
	}
	content := ""
	for _, c := range result.Content {
		if v, ok := c.(mcp.TextContent); ok {
			content += v.Text
		} else {
			content += fmt.Sprintf("%v", c)
		}
	}
	return tooltypes.BaseToolResult{
		Result: content,
	}
}
