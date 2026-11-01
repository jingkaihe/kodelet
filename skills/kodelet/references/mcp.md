# Kodelet MCP extension

The MCP integration runs in the native Go executable `kodelet-extension-mcp`, separate from the Kodelet core. MCP servers become normal extension-registered tools.

## Installation

`kodelet-extension-mcp` is bundled with RPM/DEB packages and standalone installs, and discovered on the runner's PATH by default. If your Homebrew formula does not include it, install the matching `kodelet-extension-mcp-<os>-<arch>` release asset as `kodelet-extension-mcp` on PATH. Node.js is only needed for Node-based MCP servers.

Verify discovery with `kodelet extension inspect mcp`.

**Migrating from npm:** After installing the native extension, remove `~/.kodelet/plugins/kodelet@mcp` but keep MCP configuration and OAuth credentials. Update MCP entries in extension allow/deny rules to the absolute executable path of `kodelet-extension-mcp`.

## Configuration

Servers initialize concurrently, with a 150-second budget per server for connecting, OAuth authorization, and tool discovery. Listing tools is additionally limited to 30 seconds. A server that fails or exceeds these limits is skipped without discarding tools from healthy servers. Successful connections remain open after startup; tool calls retain their separate 10-minute timeout.

The extension reads MCP server configuration from JSON files, not from Kodelet's core `config.yaml` or `kodelet-config.yaml`:

1. `~/.kodelet/mcp.json`
2. `./mcp.json`

Both files use the standard `mcpServers` shape. The repository-local file overrides global servers by server name. See the [FastMCP MCP JSON configuration reference](https://gofastmcp.com/integrations/mcp-json-configuration) for the common `mcpServers` format.

A missing, empty, or null `mcpServers` map contributes no overrides; it does not clear globally configured servers.

After adding or changing MCP servers or tool filters in `mcp.json`, run `/reload` in the conversation or have the agent call `reload_resources` to refresh available tools. Do the same after installing or upgrading the extension.

Example:

```json
{
  "oauth": {
    "interactive": "auto",
    "open_browser": true,
    "callback_timeout": "2m"
  },
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/allowed/files"],
      "tool_white_list": ["list_directory"]
    },
    "remote_http": {
      "type": "http",
      "url": "https://example.com/mcp",
      "headers": {
        "Authorization": "Bearer token"
      },
      "oauth": {
        "client_id": "${MCP_CLIENT_ID}",
        "client_secret": "${MCP_CLIENT_SECRET}",
        "scopes": ["mcp.read", "mcp.write"],
        "redirect_uri": "http://127.0.0.1:1456/mcp/oauth/callback",
        "auth_server_metadata_url": "https://auth.example.com/.well-known/oauth-authorization-server"
      },
      "tool_white_list": ["tool1", "tool2"]
    },
    "remote_sse": {
      "type": "sse",
      "url": "http://localhost:8000/sse",
      "tool_white_list": ["tool1", "tool2"]
    }
  }
}
```

## Supported fields

Standard stdio server fields:

- `command`: executable to launch.
- `args`: arguments passed to the executable.
- `env`: environment variables for the server process. String values support `$VAR` and `${VAR}` environment expansion.

Kodelet extension fields:

- `type`: `stdio`, `http`, or `sse`. If omitted, servers with `url` default to `http`; otherwise they default to `stdio`.
- `url`: remote HTTP/SSE MCP server URL.
- `headers`: headers for remote HTTP/SSE MCP endpoint requests. String values support `$VAR` and `${VAR}` environment expansion. They are not sent to OAuth discovery or token endpoints, and an OAuth bearer token takes precedence over a configured `Authorization` value.
- `tool_white_list`: optional list of MCP tool names to expose. If omitted, all server tools are exposed.
- `oauth`: per-server OAuth hints for remote HTTP/SSE servers.

For compatibility, `server_type` is accepted as an alias for `type`, and `envs` is accepted as an alias for `env`.

## OAuth

Remote HTTP/SSE OAuth is triggered automatically when the MCP server returns an OAuth Bearer challenge. The extension uses a browser authorization-code flow with a loopback callback and stores credentials under:

```text
~/.kodelet/mcp/oauth/
```

Top-level `oauth` values apply to all remote servers:

- `interactive`: `auto`, `always`, `never`, or a boolean. `auto` allows browser authorization when stdin/stderr is a TTY.
- `open_browser`: whether to try opening the browser automatically. The authorization URL is always printed to stderr.
- `callback_timeout`: callback wait time such as `30000`, `30s`, or `2m`.

Per-server `oauth` values can provide provider-specific hints:

- `client_id`
- `client_secret`
- `scopes`
- `redirect_uri`
- `auth_server_metadata_url`

String values support `$VAR` and `${VAR}` environment expansion.

To force reauthorization for a server, remove its cached credentials from `~/.kodelet/mcp/oauth/`.

Device-code OAuth is not implemented. Browser authorization runs on the runner; a remote or headless runner may need manual browser access or port forwarding for its loopback callback.

Interactive logins run one at a time within an MCP extension process, preventing overlapping browser prompts and conflicts between servers sharing a fixed callback port. The callback timeout starts when a login gets its turn. Separate extension processes do not share this coordination.

During startup, waiting for another login and completing browser authorization must fit within the server's remaining 150-second startup budget (and the shorter tool-listing timeout if challenged while listing tools). Setting `callback_timeout` higher does not extend these limits. This leaves time to report healthy servers before Kodelet's three-minute extension initialization deadline.

OAuth endpoints must use HTTPS, except for HTTP loopback endpoints used for local development. Callback URLs must be HTTP loopback URLs. MCP redirects and SSE message endpoints must stay on the configured server's origin.
