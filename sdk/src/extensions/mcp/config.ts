import { readFile } from "node:fs/promises";
import path from "node:path";

export type MCPServerType = "stdio" | "sse" | "http" | "";

export interface MCPOAuthGlobalConfig {
  interactive?: string | boolean;
  open_browser?: boolean;
  callback_timeout?: string | number;
}

export interface MCPOAuthConfig extends MCPOAuthGlobalConfig {
  client_id?: string;
  client_secret?: string;
  flow?: string;
  scopes?: string[];
  redirect_uri?: string;
  auth_server_metadata_url?: string;
  device_auth_endpoint?: string;
}

export interface MCPServerConfig {
  type?: MCPServerType | string;
  server_type?: MCPServerType | string;
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  envs?: Record<string, string>;
  url?: string;
  headers?: Record<string, string>;
  oauth?: MCPOAuthConfig;
  tool_white_list?: string[];
}

export interface MCPConfig {
  oauth?: MCPOAuthGlobalConfig;
  mcpServers?: Record<string, MCPServerConfig>;
}

export async function loadMCPConfig(cwd = process.cwd()): Promise<MCPConfig> {
  const merged: MCPConfig = {};
  const home = process.env.HOME || process.env.USERPROFILE;
  if (home) {
    mergeConfig(merged, await readJsonIfExists(path.join(home, ".kodelet", "mcp.json")));
  }
  mergeConfig(merged, await readJsonIfExists(path.join(cwd, "mcp.json")));
  return merged;
}

async function readJsonIfExists(filePath: string): Promise<Record<string, unknown> | undefined> {
  try {
    const content = await readFile(filePath, "utf8");
    const parsed = JSON.parse(content) as unknown;
    return isRecord(parsed) ? parsed : undefined;
  } catch (error) {
    if (isNodeError(error) && error.code === "ENOENT") {
      return undefined;
    }
    throw error;
  }
}

function mergeConfig(target: MCPConfig, source: Record<string, unknown> | undefined): void {
  if (!source) {
    return;
  }

  if (isRecord(source.mcpServers)) {
    target.mcpServers = {
      ...(target.mcpServers ?? {}),
      ...(source.mcpServers as Record<string, MCPServerConfig>),
    };
  }
  if (isRecord(source.oauth)) {
    target.oauth = {
      ...(target.oauth ?? {}),
      ...(source.oauth as MCPOAuthGlobalConfig),
    };
  }
}

export function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isNodeError(error: unknown): error is NodeJS.ErrnoException {
  return error instanceof Error && "code" in error;
}
