# Kodelet MCP extension

The MCP integration is implemented as a Kodelet extension built with the TypeScript SDK. It is installed as the plugin `kodelet@mcp` and exposes the executable `kodelet-extension-mcp`.

The extension keeps MCP out of the Go core: MCP servers become normal extension-registered tools.

## Install from source

From the repository root:

```bash
(cd sdk && npm ci && npm run build)
node sdk/dist/extensions/mcp/install.js
```

The installer writes this executable wrapper:

```text
~/.kodelet/plugins/kodelet@mcp/extensions/mcp/kodelet-extension-mcp
```

That wrapper points at the built files in your current checkout:

```text
sdk/dist/bin/kodelet-extension-node.js
sdk/dist/extensions/mcp/index.js
```

After editing the extension source, rerun:

```bash
(cd sdk && npm run build)
```

You only need to rerun `node sdk/dist/extensions/mcp/install.js` if the checkout path moves or the generated wrapper is removed.

To verify discovery:

```bash
kodelet extension list
```

To remove the local install:

```bash
rm -rf ~/.kodelet/plugins/kodelet@mcp
```

The npm `postinstall` script also runs the installer when `dist/extensions/mcp/install.js` exists. For local source checkouts, `npm ci` usually runs before `dist` exists, so the explicit install command above is the reliable path. Set `KODELET_SKIP_MCP_PLUGIN_INSTALL=1` to skip automatic installation.

## Configuration

The extension reads MCP server configuration from JSON files, not from Kodelet's core `config.yaml` or `kodelet-config.yaml`:

1. `~/.kodelet/mcp.json`
2. `./mcp.json`

Both files use the standard `mcpServers` shape. The repository-local file overrides global servers by server name. See the [FastMCP MCP JSON configuration reference](https://gofastmcp.com/integrations/mcp-json-configuration) for the common `mcpServers` format.

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
- `env`: environment variables for the server process.

Kodelet extension fields:

- `type`: `stdio`, `http`, or `sse`. If omitted, servers with `url` default to `http`; otherwise they default to `stdio`.
- `url`: remote HTTP/SSE MCP server URL.
- `headers`: static headers for remote HTTP/SSE transports.
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

Device-code OAuth is not implemented in this extension yet.

## Development checks

```bash
(cd sdk && npm run typecheck && npm test && npm run build)
```
