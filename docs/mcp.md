# Model Context Protocol (MCP) Configuration

This document explains how to configure Model Context Protocol (MCP) servers in Kodelet.

## Overview

MCP (Model Context Protocol) allows Kodelet to connect to external servers that provide additional functionality through tools. These servers can be stdio-based (command line applications) or server-sent events (SSE) based (web servers).

## Configuration Structure

MCP servers are configured in the `config.yaml` file under the `mcp` section:

```yaml
mcp:
  servers:
    server_name_1:
      # server 1 configuration...
    server_name_2:
      # server 2 configuration...
```

## Server Types

### Stdio Servers

Stdio servers communicate with Kodelet through standard input/output. They can be local binaries or containerized applications.

**Configuration Options:**

- `command`: The command to execute
- `args`: List of arguments to pass to the command
- `tool_white_list`: (Optional) List of allowed tools from this server

**Example:**

```yaml
mcp:
  servers:
    fs:
      command: "docker"
      args: ["run", "-i", "--rm", "mcp/filesystem", "/"]
      tool_white_list: ["list_directory"]
    time:
      command: "docker"
      args: ["run", "-i", "--rm", "mcp/time"]
```

### SSE Servers

SSE servers communicate with Kodelet through Server-Sent Events (SSE).

**Configuration Options:**

- `base_url`: Base URL for the server
- `headers`: (Optional) HTTP headers for requests
- `tool_white_list`: (Optional) List of allowed tools from this server

## Tool Whitelisting

The `tool_white_list` parameter allows you to restrict which tools from a server can be used by Kodelet. If not specified, all tools provided by the server will be available.

## Docker-based MCP Servers

For containerized MCP servers:

1. Ensure Docker is installed on your system
2. Configure the server with appropriate Docker command and arguments
3. The container must implement the MCP protocol

Example with a filesystem server that provides access to your root directory:

```yaml
mcp:
  servers:
    fs:
      command: "docker"
      args: ["run", "-i", "--rm", "mcp/filesystem", "/"]
```


## Troubleshooting

If you encounter issues with MCP servers:

1. Verify the server is running and accessible
2. Check if the tool names match between the server and your configuration
3. For Docker-based servers, ensure the image exists and is correctly configured
4. For HTTP servers, verify the base URL and authentication credentials
