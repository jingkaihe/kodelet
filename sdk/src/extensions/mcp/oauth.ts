import { createHash, randomBytes } from "node:crypto";
import { createServer, type IncomingMessage, type ServerResponse } from "node:http";
import { mkdir, open, readFile, rename, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn } from "node:child_process";

import type { OAuthClientInformationMixed, OAuthClientMetadata, OAuthTokens } from "@modelcontextprotocol/sdk/shared/auth.js";
import type { OAuthClientProvider, OAuthDiscoveryState } from "@modelcontextprotocol/sdk/client/auth.js";

import type { MCPOAuthConfig, MCPOAuthGlobalConfig } from "./config.js";

const defaultCallbackPath = "/mcp/oauth/callback";
const defaultCallbackTimeoutMs = 2 * 60 * 1000;

export interface MCPOAuthProviderOptions {
  serverName: string;
  serverUrl: string;
  config?: MCPOAuthConfig;
  globalConfig?: MCPOAuthGlobalConfig;
}

interface StoredCredentials {
  tokens?: OAuthTokens;
  clientInformation?: OAuthClientInformationMixed;
  codeVerifier?: string;
  discoveryState?: OAuthDiscoveryState;
  authServerMetadataUrl?: string;
}

interface CallbackResult {
  code?: string;
  state?: string;
  issuer?: string;
  error?: string;
  errorDescription?: string;
}

interface CallbackServer {
  redirectUrl: string;
  wait(): Promise<CallbackResult>;
  close(): Promise<void>;
}

export class KodeletMCPOAuthProvider implements OAuthClientProvider {
  private readonly store: OAuthCredentialStore;
  private readonly config: MCPOAuthConfig;
  private callback?: CallbackServer;
  private pendingAuth?: Promise<CallbackResult>;
  private pendingState?: string;

  constructor(private readonly options: MCPOAuthProviderOptions) {
    this.config = expandOAuthConfig({ ...(options.globalConfig ?? {}), ...(options.config ?? {}) });
    this.store = new OAuthCredentialStore(options.serverName, options.serverUrl);
  }

  get redirectUrl(): string | URL | undefined {
    return this.config.flow === "device" ? undefined : (this.callback?.redirectUrl ?? this.config.redirect_uri ?? loopbackRedirectUrl());
  }

  get clientMetadata(): OAuthClientMetadata {
    const redirectUrl = this.redirectUrl;
    return {
      client_name: this.clientName(),
      redirect_uris: redirectUrl ? [String(redirectUrl)] : [],
      token_endpoint_auth_method: this.config.client_secret ? "client_secret_post" : "none",
      grant_types: this.config.flow === "device" ? ["urn:ietf:params:oauth:grant-type:device_code"] : ["authorization_code", "refresh_token"],
      response_types: this.config.flow === "device" ? [] : ["code"],
      ...(this.config.scopes?.length ? { scope: uniqueStrings(this.config.scopes).join(" ") } : {}),
    };
  }

  async state(): Promise<string> {
    this.pendingState = randomUrlSafeString(32);
    return this.pendingState;
  }

  async clientInformation(): Promise<OAuthClientInformationMixed | undefined> {
    if (this.config.client_id) {
      return {
        client_id: this.config.client_id,
        ...(this.config.client_secret ? { client_secret: this.config.client_secret } : {}),
      };
    }
    return (await this.store.load()).clientInformation;
  }

  async saveClientInformation(clientInformation: OAuthClientInformationMixed): Promise<void> {
    const stored = await this.store.load();
    stored.clientInformation = clientInformation;
    await this.store.save(stored);
  }

  async tokens(): Promise<OAuthTokens | undefined> {
    return (await this.store.load()).tokens;
  }

  async saveTokens(tokens: OAuthTokens): Promise<void> {
    const stored = await this.store.load();
    stored.tokens = tokens;
    await this.store.save(stored);
  }

  async redirectToAuthorization(authorizationUrl: URL): Promise<void> {
    if (!oauthInteractiveAllowed(this.config)) {
      throw new Error(`MCP server ${JSON.stringify(this.options.serverName)} requires OAuth authorization; run in an interactive session or set oauth.interactive to allow browser authorization`);
    }

    if (!this.callback) {
      this.callback = await startCallbackServer(this.config.redirect_uri, this.config.callback_timeout);
    }
    const callback = this.callback;
    this.pendingAuth = (async () => {
      const result = await callback.wait();
      if (result.error) {
        throw new Error(`OAuth authorization failed: ${oauthErrorMessage(result.error, result.errorDescription)}`);
      }
      if (this.pendingState && result.state !== this.pendingState) {
        throw new Error("invalid OAuth state parameter, possible CSRF attack");
      }
      if (!result.code) {
        throw new Error("OAuth authorization callback did not include an authorization code");
      }
      return result;
    })();

    await promptAuthorization(this.options.serverName, authorizationUrl, this.config.open_browser);
  }

  async prepare(): Promise<void> {
    if (this.config.flow === "device") {
      throw new Error("MCP OAuth device flow is not supported by the SDK MCP extension yet");
    }
    if (!this.callback) {
      this.callback = await startCallbackServer(this.config.redirect_uri, this.config.callback_timeout);
    }
  }

  async saveCodeVerifier(codeVerifier: string): Promise<void> {
    const stored = await this.store.load();
    stored.codeVerifier = codeVerifier;
    await this.store.save(stored);
  }

  async codeVerifier(): Promise<string> {
    const codeVerifier = (await this.store.load()).codeVerifier;
    if (!codeVerifier) {
      throw new Error("No OAuth code verifier saved");
    }
    return codeVerifier;
  }

  async addClientAuthentication(_headers: Headers, params: URLSearchParams): Promise<void> {
    const clientInformation = await this.clientInformation();
    if (clientInformation?.client_id && !params.has("client_id")) {
      params.set("client_id", clientInformation.client_id);
    }
    if (clientInformation?.client_secret && !params.has("client_secret")) {
      params.set("client_secret", clientInformation.client_secret);
    }
  }

  async saveDiscoveryState(discoveryState: OAuthDiscoveryState): Promise<void> {
    const configuredMetadataUrl = this.config.auth_server_metadata_url;
    const stored = await this.store.load();
    delete stored.authServerMetadataUrl;
    stored.discoveryState = configuredMetadataUrl
      ? await discoveryStateForMetadataURL(configuredMetadataUrl)
      : discoveryState;
    await this.store.save(stored);
  }

  async discoveryState(): Promise<OAuthDiscoveryState | undefined> {
    if (this.config.auth_server_metadata_url) {
      return discoveryStateForMetadataURL(this.config.auth_server_metadata_url);
    }
    const stored = await this.store.load();
    if (stored.discoveryState) {
      return stored.discoveryState;
    }
    if (stored.authServerMetadataUrl) {
      return discoveryStateForMetadataURL(stored.authServerMetadataUrl);
    }
    return undefined;
  }

  async invalidateCredentials(scope: "all" | "client" | "tokens" | "verifier" | "discovery"): Promise<void> {
    if (scope === "all") {
      await this.store.clear();
      return;
    }
    const stored = await this.store.load();
    if (scope === "client") {
      delete stored.clientInformation;
    } else if (scope === "tokens") {
      delete stored.tokens;
    } else if (scope === "verifier") {
      delete stored.codeVerifier;
    } else if (scope === "discovery") {
      delete stored.discoveryState;
    }
    await this.store.save(stored);
  }

  async waitForAuthorizationCode(): Promise<string> {
    if (!this.pendingAuth || !this.callback) {
      throw new Error("OAuth authorization was not started");
    }
    const result = await this.pendingAuth;
    if (!result.code) {
      throw new Error("OAuth authorization callback did not include an authorization code");
    }
    return result.code;
  }

  async close(): Promise<void> {
    await this.callback?.close();
    this.callback = undefined;
  }

  private clientName(): string {
    return this.options.serverName.trim() ? `kodelet-${this.options.serverName.trim()}` : "kodelet";
  }
}

class OAuthCredentialStore {
  readonly path: string;

  constructor(serverName: string, serverUrl: string) {
    this.path = path.join(os.homedir(), ".kodelet", "mcp", "oauth", safeServerTokenFilename(serverName, serverUrl));
  }

  async load(): Promise<StoredCredentials> {
    try {
      const content = await readFile(this.path, "utf8");
      const parsed = JSON.parse(content) as unknown;
      return normalizeStoredCredentials(parsed);
    } catch (error) {
      if (isNodeError(error) && error.code === "ENOENT") {
        return {};
      }
      throw error;
    }
  }

  async save(credentials: StoredCredentials): Promise<void> {
    await mkdir(path.dirname(this.path), { recursive: true, mode: 0o700 });
    const tmpPath = `${this.path}.tmp`;
    const file = await open(tmpPath, "w", 0o600);
    try {
      await file.writeFile(`${JSON.stringify(credentials, null, 2)}\n`, "utf8");
    } finally {
      await file.close();
    }
    await rename(tmpPath, this.path);
  }

  async clear(): Promise<void> {
    await rm(this.path, { force: true });
  }
}

async function startCallbackServer(configuredRedirectUri: string | undefined, timeout: string | number | undefined): Promise<CallbackServer> {
  const configured = configuredRedirectUri ? new URL(configuredRedirectUri) : undefined;
  if (configured && (configured.protocol !== "http:" || !isLoopbackHost(configured.hostname))) {
    throw new Error("MCP OAuth redirect_uri must be an http loopback URL");
  }

  const callbackPath = configured?.pathname && configured.pathname !== "/" ? configured.pathname : defaultCallbackPath;
  const requestedPort = configured?.port ? Number.parseInt(configured.port, 10) : 0;
  const host = configured?.hostname ?? "127.0.0.1";
  let settled = false;
  let resolveResult: (result: CallbackResult) => void;
  const resultPromise = new Promise<CallbackResult>((resolve) => {
    resolveResult = resolve;
  });

  const server = createServer((req: IncomingMessage, res: ServerResponse) => {
    if (!req.url) {
      res.writeHead(404).end();
      return;
    }
    const requestUrl = new URL(req.url, `http://${req.headers.host ?? `${host}:0`}`);
    if (requestUrl.pathname !== callbackPath) {
      res.writeHead(404).end();
      return;
    }
    const result: CallbackResult = {
      code: requestUrl.searchParams.get("code") ?? undefined,
      state: requestUrl.searchParams.get("state") ?? undefined,
      issuer: requestUrl.searchParams.get("iss") ?? undefined,
      error: requestUrl.searchParams.get("error") ?? undefined,
      errorDescription: requestUrl.searchParams.get("error_description") ?? undefined,
    };
    if (!settled) {
      settled = true;
      resolveResult(result);
    }
    res.writeHead(200, { "Content-Type": "text/html; charset=utf-8" });
    res.end("<!doctype html><html><body><h1>Authorization complete</h1><p>You can close this window and return to Kodelet.</p><script>window.close();</script></body></html>");
  });

  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(requestedPort, host, () => {
      server.off("error", reject);
      resolve();
    });
  });

  const address = server.address();
  if (!address || typeof address === "string") {
    throw new Error("failed to start MCP OAuth callback server");
  }
  const redirectUrl = configured ? redirectURLWithActualPort(configured, address.port) : `http://${host}:${address.port}${callbackPath}`;

  return {
    redirectUrl,
    async wait() {
      return await withTimeout(resultPromise, parseTimeoutMs(timeout), "timed out waiting for OAuth callback");
    },
    async close() {
      await new Promise<void>((resolve, reject) => {
        server.close((error) => (error ? reject(error) : resolve()));
      }).catch((error) => {
        if (!isNodeError(error) || error.code !== "ERR_SERVER_NOT_RUNNING") {
          throw error;
        }
      });
    },
  };
}

async function promptAuthorization(serverName: string, authorizationUrl: URL, openBrowser: boolean | undefined): Promise<void> {
  const name = serverDisplayName(serverName);
  const shouldOpen = openBrowser ?? true;
  if (shouldOpen) {
    process.stderr.write(`${name} requires OAuth authorization. Opening your browser...\n`);
    await openBrowserURL(authorizationUrl.toString()).catch((error) => {
      process.stderr.write(`Could not open browser automatically: ${errorMessage(error)}\n`);
    });
  } else {
    process.stderr.write(`${name} requires OAuth authorization.\n`);
  }
  process.stderr.write(`If your browser did not open, visit this URL:\n\n  ${authorizationUrl.toString()}\n\n`);
}

async function openBrowserURL(url: string): Promise<void> {
  const command = process.platform === "darwin" ? "open" : process.platform === "win32" ? "cmd" : "xdg-open";
  const args = process.platform === "win32" ? ["/c", "start", "", url] : [url];
  await new Promise<void>((resolve, reject) => {
    const child = spawn(command, args, { detached: true, stdio: "ignore" });
    child.once("error", reject);
    child.once("spawn", () => {
      child.unref();
      resolve();
    });
  });
}

async function fetchAuthServerMetadata(metadataUrl: string): Promise<OAuthDiscoveryState["authorizationServerMetadata"]> {
  const response = await fetch(metadataUrl, { headers: { Accept: "application/json", "MCP-Protocol-Version": "2025-03-26" } });
  if (!response.ok) {
    throw new Error(`GET ${metadataUrl} failed with status ${response.status}: ${await response.text()}`);
  }
  return await response.json() as OAuthDiscoveryState["authorizationServerMetadata"];
}

async function discoveryStateForMetadataURL(metadataUrl: string): Promise<OAuthDiscoveryState> {
  const metadata = await fetchAuthServerMetadata(metadataUrl);
  return {
    authorizationServerUrl: metadata?.issuer ?? metadataUrl,
    authorizationServerMetadata: metadata,
  };
}

function expandOAuthConfig(config: MCPOAuthConfig): MCPOAuthConfig {
  return {
    ...config,
    client_id: expandConfigValue(config.client_id),
    client_secret: expandConfigValue(config.client_secret),
    flow: normalizeOAuthFlow(expandConfigValue(config.flow)),
    redirect_uri: expandConfigValue(config.redirect_uri),
    auth_server_metadata_url: expandConfigValue(config.auth_server_metadata_url),
    device_auth_endpoint: expandConfigValue(config.device_auth_endpoint),
    scopes: config.scopes?.map((scope) => expandConfigValue(scope)).filter((scope): scope is string => Boolean(scope)),
  };
}

function expandConfigValue(value: string | undefined): string | undefined {
  const trimmed = value?.trim();
  if (!trimmed) {
    return undefined;
  }
  return trimmed.replace(/\$\{([^}]+)\}|\$([A-Za-z_][A-Za-z0-9_]*)/g, (_match, braced: string | undefined, bare: string | undefined) => process.env[braced ?? bare ?? ""] ?? "");
}

function normalizeOAuthFlow(flow: string | undefined): string | undefined {
  const normalized = flow?.trim().toLowerCase().replaceAll("_", "-");
  if (normalized === "device" || normalized === "device-code") {
    return "device";
  }
  return undefined;
}

function normalizeStoredCredentials(value: unknown): StoredCredentials {
  if (!isRecord(value)) {
    return {};
  }
  const credentials: StoredCredentials = {};
  if (isRecord(value.tokens) && typeof value.tokens.access_token === "string") {
    credentials.tokens = value.tokens as OAuthTokens;
  } else if (isRecord(value.token) && typeof value.token.access_token === "string") {
    credentials.tokens = legacyTokenToOAuthTokens(value.token);
  }
  if (isRecord(value.clientInformation) && typeof value.clientInformation.client_id === "string") {
    credentials.clientInformation = value.clientInformation as OAuthClientInformationMixed;
  } else if (typeof value.client_id === "string") {
    credentials.clientInformation = {
      client_id: value.client_id,
      ...(typeof value.client_secret === "string" ? { client_secret: value.client_secret } : {}),
    };
  }
  if (typeof value.codeVerifier === "string") {
    credentials.codeVerifier = value.codeVerifier;
  }
  if (isRecord(value.discoveryState) && typeof value.discoveryState.authorizationServerUrl === "string") {
    credentials.discoveryState = value.discoveryState as unknown as OAuthDiscoveryState;
  }
  if (typeof value.auth_server_metadata_url === "string") {
    credentials.authServerMetadataUrl = value.auth_server_metadata_url;
  } else if (typeof value.authServerMetadataUrl === "string") {
    credentials.authServerMetadataUrl = value.authServerMetadataUrl;
  }
  return credentials;
}

function legacyTokenToOAuthTokens(token: Record<string, unknown>): OAuthTokens {
  return {
    access_token: String(token.access_token),
    token_type: typeof token.token_type === "string" && token.token_type ? token.token_type : "Bearer",
    ...(typeof token.refresh_token === "string" ? { refresh_token: token.refresh_token } : {}),
    ...(typeof token.expires_in === "number" ? { expires_in: token.expires_in } : {}),
    ...(typeof token.scope === "string" ? { scope: token.scope } : {}),
  };
}

function oauthInteractiveAllowed(config: MCPOAuthConfig): boolean {
  const raw = config.interactive;
  if (typeof raw === "boolean") {
    return raw;
  }
  const mode = String(raw ?? "auto").trim().toLowerCase();
  if (["always", "true", "enabled", "on", "yes"].includes(mode)) return true;
  if (["never", "false", "disabled", "off", "no"].includes(mode)) return false;
  return Boolean(process.stderr.isTTY || process.stdin.isTTY);
}

function redirectURLWithActualPort(configured: URL, port: number): string {
  const actual = new URL(configured.toString());
  actual.port = String(port);
  return actual.toString();
}

function parseTimeoutMs(value: string | number | undefined): number {
  if (typeof value === "number" && Number.isFinite(value) && value > 0) {
    return value;
  }
  const raw = String(value ?? "").trim();
  if (!raw) return defaultCallbackTimeoutMs;
  const match = raw.match(/^(\d+(?:\.\d+)?)(ms|s|m|h)?$/i);
  if (!match) return defaultCallbackTimeoutMs;
  const amount = Number.parseFloat(match[1] ?? "0");
  const unit = (match[2] ?? "ms").toLowerCase();
  const multiplier = unit === "h" ? 3_600_000 : unit === "m" ? 60_000 : unit === "s" ? 1000 : 1;
  return Math.max(1, Math.floor(amount * multiplier));
}

async function withTimeout<T>(promise: Promise<T>, timeoutMs: number, message: string): Promise<T> {
  let timer: NodeJS.Timeout | undefined;
  try {
    return await Promise.race([
      promise,
      new Promise<T>((_, reject) => {
        timer = setTimeout(() => reject(new Error(message)), timeoutMs);
      }),
    ]);
  } finally {
    if (timer) clearTimeout(timer);
  }
}

function loopbackRedirectUrl(): string {
  return `http://127.0.0.1:0${defaultCallbackPath}`;
}

function isLoopbackHost(hostname: string): boolean {
  return hostname === "localhost" || hostname === "127.0.0.1" || hostname === "::1" || hostname === "[::1]";
}

function safeServerTokenFilename(serverName: string, serverUrl: string): string {
  const safe = (serverName.trim() || "server").toLowerCase().replace(/[^a-z0-9_-]+/g, "_").replace(/^_+|_+$/g, "") || "server";
  const hash = createHash("sha256").update(serverUrl).digest("hex").slice(0, 12);
  return `${safe}-${hash}.json`;
}

function randomUrlSafeString(size: number): string {
  return randomBytes(size).toString("base64url");
}

function serverDisplayName(serverName: string): string {
  return serverName.trim() ? `MCP server ${JSON.stringify(serverName.trim())}` : "MCP server";
}

function uniqueStrings(values: string[]): string[] {
  return [...new Set(values.map((value) => value.trim()).filter(Boolean))].sort();
}

function oauthErrorMessage(code: string | undefined, description: string | undefined): string {
  if (!description) return code || "unknown_error";
  if (!code) return description;
  return `${code} - ${description}`;
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function isNodeError(error: unknown): error is NodeJS.ErrnoException {
  return error instanceof Error && "code" in error;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
