package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/invopop/jsonschema"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/jingkaihe/kodelet/pkg/version"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"
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
	ServerType    MCPServerType     `json:"server_type"`     // stdio or sse
	Command       string            `json:"command"`         // stdio: command to start the server
	Args          []string          `json:"args"`            // stdio: arguments to pass to the server
	Envs          map[string]string `json:"envs"`            // stdio: environment variables to set
	BaseURL       string            `json:"base_url"`        // sse: base URL of the server
	Headers       map[string]string `json:"headers"`         // sse: headers to send to the server
	ToolWhiteList []string          `json:"tool_white_list"` // sse: tool white list
}

type MCPServersConfig struct {
	Servers map[string]MCPServerConfig `json:"servers"`
}

func NewMCPClient(config MCPServerConfig) (*client.Client, error) {
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

func NewMCPManager(config MCPServersConfig) (*MCPManager, error) {
	clients := &MCPManager{
		clients:   make(map[string]*client.Client),
		whiteList: make(map[string][]string),
	}
	for name, config := range config.Servers {
		client, err := NewMCPClient(config)
		if err != nil {
			return nil, err
		}
		clients.clients[name] = client
		clients.whiteList[name] = config.ToolWhiteList
	}
	return clients, nil
}

func (m *MCPManager) Initialize(ctx context.Context) error {
	for name, client := range m.clients {
		logrus.WithField("name", name).Info("initializing mcp client")
		initReq := mcp.InitializeRequest{}
		initReq.Params.ClientInfo = mcp.Implementation{
			Name:    "kodelet",
			Version: version.Version,
		}
		initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
		err := client.Start(ctx)
		if err != nil {
			return err
		}
		_, err = client.Initialize(ctx, initReq)
		if err != nil {
			return err
		}
		logrus.WithField("name", name).Info("initialized mcp client")
	}
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
	tools := []MCPTool{}
	for name, client := range m.clients {
		logrus.WithField("name", name).Info("listing mcp tools")
		listToolResult, err := client.ListTools(ctx, mcp.ListToolsRequest{})
		if err != nil {
			return nil, err
		}
		for _, tool := range listToolResult.Tools {
			if toolWhiteListed(tool, m.whiteList[name]) {
				tools = append(tools, *NewMCPTool(client, tool))
			}
		}
	}
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
	var v any
	err = json.Unmarshal(b, &v)
	if err != nil {
		return nil
	}
	return GenerateSchema[any]()
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
		return tooltypes.ToolResult{
			Error: err.Error(),
		}
	}

	req := mcp.CallToolRequest{}
	req.Params.Arguments = input
	req.Params.Name = t.mcpToolName
	result, err := t.client.CallTool(ctx, req)
	if err != nil {
		return tooltypes.ToolResult{
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
	return tooltypes.ToolResult{
		Result: content,
	}
}
