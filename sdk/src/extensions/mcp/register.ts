import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { UnauthorizedError } from "@modelcontextprotocol/sdk/client/auth.js";
import { SSEClientTransport } from "@modelcontextprotocol/sdk/client/sse.js";
import { StdioClientTransport } from "@modelcontextprotocol/sdk/client/stdio.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";
import type { FetchLike } from "@modelcontextprotocol/sdk/shared/transport.js";
import type { Tool } from "@modelcontextprotocol/sdk/types.js";

import type { ExtensionAPI } from "../../types.js";
import type { MCPConfig, MCPOAuthGlobalConfig, MCPServerConfig } from "./config.js";
import { KodeletMCPOAuthProvider } from "./oauth.js";

type MCPTransport = StdioClientTransport | StreamableHTTPClientTransport | SSEClientTransport;

export const mcpToolTimeoutInSec = 600;
export const mcpToolRequestTimeoutMs = mcpToolTimeoutInSec * 1000;

interface TransportBundle {
  transport: MCPTransport;
  oauthProvider?: KodeletMCPOAuthProvider;
}

interface ConnectedServer {
  name: string;
  client: Client;
  transport: MCPTransport;
  whiteList: string[];
  oauthProvider?: KodeletMCPOAuthProvider;
  authorization?: Promise<void>;
  authorizationGeneration: number;
}

export async function registerMCP(ext: ExtensionAPI, config: MCPConfig): Promise<void> {
  const servers = config.mcpServers ?? {};
  const connectedServers: ConnectedServer[] = [];

  for (const [serverName, serverConfig] of Object.entries(servers).sort(([a], [b]) => a.localeCompare(b))) {
    try {
      const connected = await connectServer(serverName, serverConfig, config.oauth);
      connectedServers.push(connected);
      await registerServerTools(ext, connected);
    } catch (error) {
      process.stderr.write(
        `${JSON.stringify({ level: "warn", extension: "mcp", message: "failed to initialize MCP server", server: serverName, error: errorMessage(error) })}\n`,
      );
    }
  }

  ext.on("session.end", { timeoutInSec: 10 }, async () => {
    await Promise.allSettled(connectedServers.map((server) => closeConnectedServer(server)));
  });
}

async function closeConnectedServer(server: ConnectedServer): Promise<void> {
  if (server.transport instanceof StreamableHTTPClientTransport) {
    await Promise.allSettled([server.transport.terminateSession()]);
  }
  await Promise.allSettled([server.client.close(), server.oauthProvider?.close()]);
}

async function connectServer(serverName: string, config: MCPServerConfig, globalOAuth: MCPOAuthGlobalConfig | undefined): Promise<ConnectedServer> {
  const initial = buildTransport(serverName, config, globalOAuth);
  let activeTransport = initial.transport;

  let client = new Client({ name: "kodelet", version: "dev" });
  try {
    await client.connect(initial.transport);
  } catch (error) {
    if (!initial.oauthProvider || !isUnauthorizedError(error) || !supportsFinishAuth(initial.transport)) {
      await initial.oauthProvider?.close();
      throw error;
    }

    try {
      const authorizationCode = await initial.oauthProvider.waitForAuthorizationCode();
      await initial.transport.finishAuth(authorizationCode);
    } finally {
      await Promise.allSettled([initial.oauthProvider.close(), client.close()]);
    }

    client = new Client({ name: "kodelet", version: "dev" });
    const retry = buildTransport(serverName, config, globalOAuth, initial.oauthProvider);
    await client.connect(retry.transport);
    activeTransport = retry.transport;
  }

  return {
    name: serverName,
    client,
    transport: activeTransport,
    whiteList: config.tool_white_list ?? [],
    oauthProvider: initial.oauthProvider,
    authorizationGeneration: 0,
  };
}

function buildTransport(
  serverName: string,
  config: MCPServerConfig,
  globalOAuth: MCPOAuthGlobalConfig | undefined,
  oauthProvider?: KodeletMCPOAuthProvider,
): TransportBundle {
  const serverType = normalizeServerType(config);
  switch (serverType) {
    case "stdio": {
      if (!config.command) {
        throw new Error("command is required for stdio server");
      }
      return {
        transport: new StdioClientTransport({
          command: config.command,
          args: config.args ?? [],
          env: resolveEnv(config.env ?? config.envs),
          stderr: "inherit",
          cwd: extensionWorkspaceCWD(),
        }),
      };
    }
    case "sse": {
      if (!config.url) {
        throw new Error("url is required for sse server");
      }
      const provider = oauthProvider ?? new KodeletMCPOAuthProvider({ serverName, serverUrl: config.url, config: config.oauth, globalConfig: globalOAuth });
      const url = new URL(config.url);
      return { transport: new SSEClientTransport(url, {
        authProvider: provider,
        fetch: scopedMCPFetch(url, resolveConfigValues(config.headers)),
      }), oauthProvider: provider };
    }
    case "http": {
      if (!config.url) {
        throw new Error("url is required for http server");
      }
      const provider = oauthProvider ?? new KodeletMCPOAuthProvider({ serverName, serverUrl: config.url, config: config.oauth, globalConfig: globalOAuth });
      const url = new URL(config.url);
      return { transport: new StreamableHTTPClientTransport(url, {
        authProvider: provider,
        fetch: scopedMCPFetch(url, resolveConfigValues(config.headers)),
      }), oauthProvider: provider };
    }
  }
}

export function scopedMCPFetch(
  serverUrl: URL,
  configuredHeaders: Record<string, string> | undefined,
  baseFetch: FetchLike = fetch,
): FetchLike {
  const entries = Object.entries(configuredHeaders ?? {});
  if (entries.length === 0) {
    return baseFetch;
  }
  return async (url, init) => {
    if (!isMCPRequest(serverUrl, url, init)) {
      return await baseFetch(url, init);
    }
    const headers = new Headers(init?.headers);
    for (const [name, value] of entries) {
      if (!headers.has(name)) {
        headers.set(name, value);
      }
    }
    return await baseFetch(url, { ...init, headers });
  };
}

function isMCPRequest(serverUrl: URL, requestUrl: string | URL, init: RequestInit | undefined): boolean {
  const url = new URL(requestUrl);
  if (url.href === serverUrl.href) {
    return true;
  }
  if (url.origin !== serverUrl.origin || typeof init?.body !== "string") {
    return false;
  }
  try {
    const body = JSON.parse(init.body) as unknown;
    const messages = Array.isArray(body) ? body : [body];
    return messages.length > 0 && messages.every((message) => isRecord(message) && message.jsonrpc === "2.0");
  } catch {
    return false;
  }
}

function extensionWorkspaceCWD(): string {
  return process.env.KODELET_EXTENSION_WORKSPACE_CWD || process.cwd();
}

function supportsFinishAuth(transport: MCPTransport): transport is StreamableHTTPClientTransport | SSEClientTransport {
  return "finishAuth" in transport && typeof transport.finishAuth === "function";
}

function isUnauthorizedError(error: unknown): boolean {
  return error instanceof UnauthorizedError || (error instanceof Error && error.name === "UnauthorizedError");
}

function normalizeServerType(config: MCPServerConfig): "stdio" | "sse" | "http" {
  const raw = String(config.type ?? config.server_type ?? "").trim().toLowerCase();
  if (raw === "") {
    return config.url ? "http" : "stdio";
  }
  if (raw === "streamable_http" || raw === "streamable-http" || raw === "streamablehttp") {
    return "http";
  }
  if (raw === "stdio" || raw === "sse" || raw === "http") {
    return raw;
  }
  throw new Error(`invalid server type: ${config.type ?? config.server_type}`);
}

function resolveEnv(envs: Record<string, string> | undefined): Record<string, string> {
  const resolved: Record<string, string> = {};
  for (const [key, value] of Object.entries(process.env)) {
    if (value !== undefined) {
      resolved[key] = value;
    }
  }
  return { ...resolved, ...resolveConfigValues(envs) };
}

function resolveConfigValues(values: Record<string, string> | undefined): Record<string, string> | undefined {
  if (!values) {
    return undefined;
  }
  const resolved: Record<string, string> = {};
  for (const [key, value] of Object.entries(values)) {
    resolved[key] = expandEnvValue(value);
  }
  return resolved;
}

function expandEnvValue(value: string): string {
  return value.replace(/\$\{([^}]+)\}|\$([A-Za-z_][A-Za-z0-9_]*)/g, (_match, braced: string | undefined, bare: string | undefined) => process.env[braced ?? bare ?? ""] ?? "");
}

async function registerServerTools(ext: ExtensionAPI, server: ConnectedServer): Promise<void> {
  const result = await requestWithAuthorization(server, () => server.client.listTools());
  for (const tool of result.tools) {
    if (!toolWhiteListed(tool, server.whiteList)) {
      continue;
    }
    const toolName = extensionToolName(server.name, tool.name);
    ext.registerTool({
      name: toolName,
      description: tool.description?.trim() || tool.title?.trim() || tool.name,
      inputSchema: tool.inputSchema,
      timeoutInSec: mcpToolTimeoutInSec,
      async execute(input) {
        const start = Date.now();
        const result = await callServerTool(server, tool.name, input as Record<string, unknown>);
        if ("toolResult" in result) {
          const content = stringifyUnknown(result.toolResult);
          return {
            content,
            data: mcpData(server.name, tool.name, input as Record<string, unknown>, content, [{ type: "unknown", text: content }], Date.now() - start),
          };
        }

        const contentBlocks = result.content.map((block) => normalizeContentBlock(block));
        const contentText = contentBlocks.map((block) => block.text ?? "").join("");
        if (result.isError) {
          return {
            content: contentText,
            error: contentText || `MCP tool ${server.name}.${tool.name} returned an error`,
            data: mcpData(server.name, tool.name, input as Record<string, unknown>, contentText, contentBlocks, Date.now() - start),
          };
        }
        return {
          content: contentText,
          data: mcpData(server.name, tool.name, input as Record<string, unknown>, contentText, contentBlocks, Date.now() - start),
        };
      },
    });
  }
}

async function callServerTool(server: ConnectedServer, toolName: string, input: Record<string, unknown>) {
  return await requestWithAuthorization(server, () => requestServerTool(server, toolName, input));
}

async function requestWithAuthorization<Result>(server: ConnectedServer, request: () => Promise<Result>): Promise<Result> {
  const authorizationGeneration = server.authorizationGeneration;
  try {
    return await request();
  } catch (error) {
    if (!server.oauthProvider || !supportsFinishAuth(server.transport) || !isUnauthorizedError(error)) {
      throw error;
    }
    if (authorizationGeneration === server.authorizationGeneration) {
      await authorizeConnectedServer(server);
    } else if (server.authorization) {
      await server.authorization;
    }
    return await request();
  }
}

async function requestServerTool(server: ConnectedServer, toolName: string, input: Record<string, unknown>) {
  return await server.client.callTool(
    { name: toolName, arguments: input },
    undefined,
    { timeout: mcpToolRequestTimeoutMs },
  );
}

async function authorizeConnectedServer(server: ConnectedServer): Promise<void> {
  if (server.authorization) {
    return await server.authorization;
  }
  if (!server.oauthProvider || !supportsFinishAuth(server.transport)) {
    throw new Error(`MCP server ${JSON.stringify(server.name)} cannot complete OAuth authorization`);
  }
  const authorization = completeAuthorization(server.oauthProvider, server.transport).then(() => {
    server.authorizationGeneration++;
  });
  server.authorization = authorization;
  try {
    await authorization;
  } finally {
    if (server.authorization === authorization) {
      server.authorization = undefined;
    }
  }
}

async function completeAuthorization(
  provider: KodeletMCPOAuthProvider,
  transport: StreamableHTTPClientTransport | SSEClientTransport,
): Promise<void> {
  try {
    const authorizationCode = await provider.waitForAuthorizationCode();
    await transport.finishAuth(authorizationCode);
  } finally {
    await provider.close();
  }
}

function toolWhiteListed(tool: Tool, whiteList: string[]): boolean {
  return whiteList.length === 0 || whiteList.includes(tool.name);
}

function extensionToolName(serverName: string, toolName: string): string {
  return `mcp__${serverName}_${toolName}`;
}

function normalizeContentBlock(block: unknown): Record<string, string> {
  if (isRecord(block)) {
    if (block.type === "text" && typeof block.text === "string") {
      return { type: "text", text: block.text };
    }
    if (block.type === "image" && typeof block.data === "string") {
      return { type: "image", text: `[image:${typeof block.mimeType === "string" ? block.mimeType : "unknown"}]` };
    }
    if (block.type === "resource") {
      return { type: "resource", text: stringifyUnknown(block.resource) };
    }
  }
  return { type: "unknown", text: stringifyUnknown(block) };
}

function mcpData(
  serverName: string,
  toolName: string,
  parameters: Record<string, unknown>,
  contentText: string,
  content: Array<Record<string, string>>,
  executionTimeMs: number,
): Record<string, unknown> {
  return {
    kind: "mcp",
    mcpToolName: toolName,
    serverName,
    parameters,
    content,
    contentText,
    executionTimeMs,
  };
}

function stringifyUnknown(value: unknown): string {
  if (typeof value === "string") {
    return value;
  }
  return JSON.stringify(value, null, 2);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
