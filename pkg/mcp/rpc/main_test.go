package rpc

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func TestMain(m *testing.M) {
	if maybeServeTestMCPServer() {
		os.Exit(0)
	}

	os.Exit(m.Run())
}

func maybeServeTestMCPServer() bool {
	serverKind := os.Getenv(testMCPServerEnv)
	if serverKind == "" {
		return false
	}

	mcpServer := server.NewMCPServer("test-"+serverKind, "1.0.0")

	switch serverKind {
	case "filesystem":
		mcpServer.AddTool(
			mcp.NewTool("list_directory", mcp.WithString("path", mcp.Required())),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				path, _ := req.GetArguments()["path"].(string)
				if path == "" {
					return mcp.NewToolResultError("path is required"), nil
				}

				entries, err := os.ReadDir(path)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}

				lines := make([]string, 0, len(entries))
				for _, entry := range entries {
					prefix := "[FILE]"
					if entry.IsDir() {
						prefix = "[DIR]"
					}
					lines = append(lines, prefix+" "+entry.Name())
				}

				return mcp.NewToolResultText(strings.Join(lines, "\n")), nil
			},
		)
	case "time":
		mcpServer.AddTool(
			mcp.NewTool("get_current_time", mcp.WithString("timezone", mcp.Required())),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				timezone, _ := req.GetArguments()["timezone"].(string)
				if timezone == "" {
					return mcp.NewToolResultError("timezone is required"), nil
				}

				return mcp.NewToolResultText(fmt.Sprintf("current time in %s is 2024-01-01T00:00:00Z", timezone)), nil
			},
		)

		mcpServer.AddTool(
			mcp.NewTool(
				"convert_time",
				mcp.WithString("source_timezone", mcp.Required()),
				mcp.WithString("time", mcp.Required()),
				mcp.WithString("target_timezone", mcp.Required()),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				args := req.GetArguments()
				sourceTimezone, _ := args["source_timezone"].(string)
				timeValue, _ := args["time"].(string)
				targetTimezone, _ := args["target_timezone"].(string)
				if sourceTimezone == "" || timeValue == "" || targetTimezone == "" {
					return mcp.NewToolResultError("source_timezone, time, and target_timezone are required"), nil
				}

				return mcp.NewToolResultText(fmt.Sprintf("%s in %s is %s in %s", timeValue, sourceTimezone, timeValue, targetTimezone)), nil
			},
		)
	default:
		fmt.Fprintf(os.Stderr, "unknown test MCP server %q\n", serverKind)
		os.Exit(1)
	}

	if err := server.ServeStdio(mcpServer); err != nil {
		fmt.Fprintf(os.Stderr, "failed to serve test MCP server: %v\n", err)
		os.Exit(1)
	}

	return true
}
