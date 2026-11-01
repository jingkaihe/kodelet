package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jingkaihe/kodelet/sdk/go/extension"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pkg/errors"
)

const (
	toolTimeout          = 10 * time.Minute
	serverStartupTimeout = 150 * time.Second
	listToolsTimeout     = 30 * time.Second
)

func registerMCP(ctx context.Context, ext *extension.Extension, config mcpConfig, cwd string, startupTimeout time.Duration) func() {
	names := slices.Sorted(maps.Keys(config.Servers))
	initialized := make([]struct {
		session *mcp.ClientSession
		tools   []*mcp.Tool
		close   func()
	}, len(names))
	var group sync.WaitGroup
	for i, name := range names {
		group.Go(func() {
			startupCtx, cancelStartup := context.WithTimeout(ctx, startupTimeout)
			defer cancelStartup()
			serverCtx, cancelServer := context.WithCancel(ctx)
			// SSE retains the connection context. Detach the startup deadline
			// after initialization so a healthy connection remains usable.
			stop := context.AfterFunc(startupCtx, cancelServer)
			defer stop()
			server := config.Servers[name]
			session, closeServer, err := connectServer(serverCtx, name, server, config.OAuth, cwd)
			if err != nil {
				cancelServer()
				slog.Warn("failed to initialize MCP server", "server", name, "error", err)
				return
			}
			var tools []*mcp.Tool
			listCtx, cancelList := context.WithTimeout(startupCtx, listToolsTimeout)
			for tool, listErr := range session.Tools(listCtx, nil) {
				if listErr != nil {
					err = listErr
					break
				}
				if len(server.ToolWhiteList) == 0 || slices.Contains(server.ToolWhiteList, tool.Name) {
					tools = append(tools, tool)
				}
			}
			cancelList()
			stop()
			if err == nil {
				err = startupCtx.Err()
			}
			if err != nil {
				cancelServer()
				closeServer()
				slog.Warn("failed to list MCP tools", "server", name, "error", err)
				return
			}
			initialized[i].session = session
			initialized[i].tools = tools
			initialized[i].close = func() {
				defer cancelServer()
				closeServer()
			}
		})
	}
	group.Wait()
	var closers []func()
	for i, server := range initialized {
		if server.session == nil {
			continue
		}
		closers = append(closers, server.close)
		for _, tool := range server.tools {
			registerTool(ext, server.session, names[i], tool)
		}
	}
	closeServers := sync.OnceFunc(func() {
		var group sync.WaitGroup
		for _, closeServer := range closers {
			group.Go(closeServer)
		}
		group.Wait()
	})
	extension.On(ext, extension.EventSessionEnd, func(context.Context, extension.SessionEndEvent, *extension.EventContext) (extension.EventResult, error) {
		closeServers()
		return extension.EventResult{}, nil
	}, extension.WithTimeout(10*time.Second))
	return closeServers
}

func connectServer(ctx context.Context, name string, config serverConfig, globalOAuth oauthConfig, cwd string) (*mcp.ClientSession, func(), error) {
	kind, err := config.transportType()
	if err != nil {
		return nil, nil, err
	}
	serverCtx, cancel := context.WithCancel(ctx)
	cleanup := cancel
	var transport mcp.Transport
	if kind == "stdio" {
		if config.Command == "" {
			cancel()
			return nil, nil, errors.New("command is required for stdio server")
		}
		cmd := exec.CommandContext(serverCtx, config.Command, config.Args...)
		cmd.Dir = cwd
		cmd.Stderr = os.Stderr
		cmd.Env = os.Environ()
		env := config.Env
		if env == nil {
			env = config.Envs
		}
		for _, key := range slices.Sorted(maps.Keys(env)) {
			cmd.Env = append(cmd.Env, key+"="+os.ExpandEnv(env[key]))
		}
		transport = &mcp.CommandTransport{Command: cmd, TerminateDuration: time.Second}
	} else {
		endpoint, err := url.Parse(config.URL)
		if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
			cancel()
			return nil, nil, errors.Errorf("invalid MCP server URL %q", config.URL)
		}
		oauth, err := config.oauthConfig(globalOAuth)
		if err != nil {
			cancel()
			return nil, nil, err
		}
		client, closeAuth, err := newOAuthClient(name, config.URL, oauth)
		if err != nil {
			cancel()
			return nil, nil, err
		}
		cleanup = func() { cancel(); closeAuth() }
		client.Transport = &headerTransport{base: client.Transport, endpoint: endpoint, headers: config.Headers}
		if kind == "sse" {
			transport = &mcp.SSEClientTransport{Endpoint: config.URL, HTTPClient: client}
		} else {
			transport = &mcp.StreamableClientTransport{Endpoint: config.URL, HTTPClient: client}
		}
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "kodelet", Version: version}, nil)
	session, err := client.Connect(serverCtx, transport, &mcp.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return session, func() {
		defer cleanup()
		if err := session.Close(); err != nil {
			slog.Warn("failed to close MCP server", "server", name, "error", err)
		}
	}, nil
}

// Headers apply to the endpoint and same-origin SSE JSON-RPC POSTs, never to
// OAuth discovery/token requests (which use a separate client).
type headerTransport struct {
	base     http.RoundTripper
	endpoint *url.URL
	headers  map[string]string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	sameOrigin := req.URL.Scheme == t.endpoint.Scheme && req.URL.Host == t.endpoint.Host
	if sameOrigin && (req.URL.Path == t.endpoint.Path || req.Method == http.MethodPost) {
		req = req.Clone(req.Context())
		for key, value := range t.headers {
			if req.Header.Get(key) == "" {
				req.Header.Set(key, os.ExpandEnv(value))
			}
		}
	}
	return t.base.RoundTrip(req)
}

func registerTool(ext *extension.Extension, session *mcp.ClientSession, server string, tool *mcp.Tool) {
	inputSchema, _ := tool.InputSchema.(map[string]any)
	outputSchema, _ := tool.OutputSchema.(map[string]any)
	description := strings.TrimSpace(tool.Description)
	if description == "" {
		description = strings.TrimSpace(tool.Title)
	}
	if description == "" {
		description = tool.Name
	}
	extension.RegisterTool(ext, extension.Tool[map[string]any]{
		Name:         "mcp__" + server + "_" + tool.Name,
		Description:  description,
		InputSchema:  inputSchema,
		OutputSchema: outputSchema,
		Group:        "mcp/" + server,
		Timeout:      toolTimeout,
		Execute: func(ctx context.Context, input map[string]any, _ *extension.ToolContext) (extension.ToolResult, error) {
			ctx, cancel := context.WithTimeout(ctx, toolTimeout)
			defer cancel()
			start := time.Now()
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool.Name, Arguments: input})
			if err != nil {
				return extension.ToolResult{}, err
			}
			return convertResult(server, tool.Name, input, result, time.Since(start)), nil
		},
	})
}

func convertResult(server, tool string, input map[string]any, result *mcp.CallToolResult, duration time.Duration) extension.ToolResult {
	output := extension.ToolResult{StructuredContent: result.StructuredContent}
	blocks := make([]map[string]string, 0, len(result.Content))
	var text strings.Builder
	for _, content := range result.Content {
		var kind, value string
		switch block := content.(type) {
		case *mcp.TextContent:
			kind, value = "text", block.Text
		case *mcp.ImageContent:
			kind, value = "image", "[image:"+block.MIMEType+"]"
			output.Attachments = append(output.Attachments, extension.ImageData(block.Data, block.MIMEType))
		case *mcp.EmbeddedResource:
			encoded, _ := json.MarshalIndent(block.Resource, "", "  ")
			kind, value = "resource", string(encoded)
		default:
			encoded, _ := json.MarshalIndent(content, "", "  ")
			kind, value = "unknown", string(encoded)
		}
		blocks = append(blocks, map[string]string{"type": kind, "text": value})
		text.WriteString(value)
	}
	output.Content = text.String()
	if result.IsError {
		output.Error = output.Content
		if output.Error == "" {
			output.Error = "MCP tool " + server + "." + tool + " returned an error"
		}
	}
	output.Data = map[string]any{
		"kind": "mcp", "mcpToolName": tool, "serverName": server,
		"parameters": input, "content": blocks, "contentText": output.Content,
		"executionTimeMs": duration.Milliseconds(),
	}
	return output
}
