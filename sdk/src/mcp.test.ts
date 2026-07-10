import assert from "node:assert/strict";
import { mkdir, mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
import http from "node:http";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import mcpExtension from "./extensions/mcp/index.js";
import { loadMCPConfig } from "./extensions/mcp/config.js";
import { KodeletMCPOAuthProvider } from "./extensions/mcp/oauth.js";
import { scopedMCPFetch } from "./extensions/mcp/register.js";
import { createExtensionHost } from "./api.js";

test("loads standard mcpServers from global and cwd mcp.json", async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), "kodelet-mcp-config-"));
  const oldHome = process.env.HOME;
  const oldUserProfile = process.env.USERPROFILE;
  try {
    const home = path.join(root, "home");
    const cwd = path.join(root, "workspace");
    await mkdir(path.join(home, ".kodelet"), { recursive: true });
    await mkdir(cwd, { recursive: true });

    process.env.HOME = home;
    delete process.env.USERPROFILE;

    await writeFile(
      path.join(home, ".kodelet", "mcp.json"),
      JSON.stringify({
        oauth: { interactive: "never", callback_timeout: "1s" },
        mcpServers: {
          global: { command: "global-cmd", args: ["--global"], env: { GLOBAL: "1" } },
          shared: { command: "global-shared" },
        },
      }),
      "utf8",
    );
    await writeFile(
      path.join(cwd, "mcp.json"),
      JSON.stringify({
        oauth: { open_browser: false },
        mcpServers: {
          shared: { command: "local-shared", args: ["--local"] },
          remote: { type: "http", url: "https://example.com/mcp", headers: { Authorization: "Bearer token" }, oauth: { client_id: "$MCP_TEST_CLIENT_ID" } },
        },
      }),
      "utf8",
    );

    const config = await loadMCPConfig(cwd);
    assert.deepEqual(config, {
      oauth: { interactive: "never", callback_timeout: "1s", open_browser: false },
      mcpServers: {
        global: { command: "global-cmd", args: ["--global"], env: { GLOBAL: "1" } },
        shared: { command: "local-shared", args: ["--local"] },
        remote: { type: "http", url: "https://example.com/mcp", headers: { Authorization: "Bearer token" }, oauth: { client_id: "$MCP_TEST_CLIENT_ID" } },
      },
    });
  } finally {
    restoreEnv("HOME", oldHome);
    restoreEnv("USERPROFILE", oldUserProfile);
    await rm(root, { recursive: true, force: true });
  }
});

test("loads local MCP config from extension workspace cwd env by default", async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), "kodelet-mcp-workspace-cwd-"));
  const oldHome = process.env.HOME;
  const oldUserProfile = process.env.USERPROFILE;
  const oldWorkspaceCWD = process.env.KODELET_EXTENSION_WORKSPACE_CWD;
  try {
    const home = path.join(root, "home");
    const workspace = path.join(root, "workspace");
    const extensionDir = path.join(root, "plugin", "extension");
    await mkdir(path.join(home, ".kodelet"), { recursive: true });
    await mkdir(workspace, { recursive: true });
    await mkdir(extensionDir, { recursive: true });

    process.env.HOME = home;
    delete process.env.USERPROFILE;
    process.env.KODELET_EXTENSION_WORKSPACE_CWD = workspace;

    await writeFile(
      path.join(workspace, "mcp.json"),
      JSON.stringify({
        mcpServers: {
          playwright: { command: "npx", args: ["-y", "@playwright/mcp@latest"] },
        },
      }),
      "utf8",
    );
    await writeFile(
      path.join(extensionDir, "mcp.json"),
      JSON.stringify({
        mcpServers: {
          wrong: { command: "wrong-cmd" },
        },
      }),
      "utf8",
    );

    const oldCwd = process.cwd();
    process.chdir(extensionDir);
    try {
      const config = await loadMCPConfig();
      assert.deepEqual(config, {
        mcpServers: {
          playwright: { command: "npx", args: ["-y", "@playwright/mcp@latest"] },
        },
      });
    } finally {
      process.chdir(oldCwd);
    }
  } finally {
    restoreEnv("HOME", oldHome);
    restoreEnv("USERPROFILE", oldUserProfile);
    restoreEnv("KODELET_EXTENSION_WORKSPACE_CWD", oldWorkspaceCWD);
    await rm(root, { recursive: true, force: true });
  }
});

test("MCP extension entrypoint loads config from workspace cwd env", async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), "kodelet-mcp-entrypoint-cwd-"));
  const oldHome = process.env.HOME;
  const oldUserProfile = process.env.USERPROFILE;
  const oldWorkspaceCWD = process.env.KODELET_EXTENSION_WORKSPACE_CWD;
  const oldEnvValue = process.env.MCP_TEST_ENV_VALUE;
  try {
    const home = path.join(root, "home");
    const workspace = path.join(root, "workspace");
    const extensionDir = path.join(root, "plugin", "extension");
    const fakeServer = path.join(workspace, "fake-mcp-server.mjs");
    await mkdir(path.join(home, ".kodelet"), { recursive: true });
    await mkdir(workspace, { recursive: true });
    await mkdir(extensionDir, { recursive: true });

    process.env.HOME = home;
    delete process.env.USERPROFILE;
    process.env.KODELET_EXTENSION_WORKSPACE_CWD = workspace;
    process.env.MCP_TEST_ENV_VALUE = "expanded-value";

    await writeFile(
      fakeServer,
      `process.stdin.resume();
let buffer = '';
process.stdin.on('data', (chunk) => {
  buffer += chunk.toString('utf8');
  while (true) {
    const newline = buffer.indexOf('\\n');
    if (newline === -1) return;
    const line = buffer.slice(0, newline).trim();
    buffer = buffer.slice(newline + 1);
    if (!line) continue;
    const request = JSON.parse(line);
    if (request.id === undefined) continue;
    let result = {};
    if (request.method === 'initialize') {
      result = { protocolVersion: request.params?.protocolVersion ?? '2024-11-05', capabilities: { tools: {} }, serverInfo: { name: 'fake', version: '1.0.0' } };
    } else if (request.method === 'tools/list') {
      result = { tools: [{ name: 'ping', title: 'Workspace Ping', inputSchema: {
        type: 'object',
        properties: {
          mode: { type: 'string', description: 'Ping mode', enum: ['fast', 'safe'] },
          target: { type: ['string', 'null'] }
        },
        additionalProperties: false,
        'x-mcp-extension': { enabled: true }
      } }] };
    } else if (request.method === 'tools/call') {
      result = { content: [{ type: 'text', text: JSON.stringify({ cwd: process.cwd(), bare: process.env.FROM_BARE, braced: process.env.FROM_BRACED, mixed: process.env.FROM_MIXED }) }] };
    }
    process.stdout.write(JSON.stringify({ jsonrpc: '2.0', id: request.id, result }) + '\\n');
  }
});
`,
      "utf8",
    );
    await writeFile(
      path.join(workspace, "mcp.json"),
      JSON.stringify({
        mcpServers: {
          workspace: {
            command: process.execPath,
            args: ["fake-mcp-server.mjs"],
            env: {
              FROM_BARE: "$MCP_TEST_ENV_VALUE",
              FROM_BRACED: "${MCP_TEST_ENV_VALUE}",
              FROM_MIXED: "prefix-${MCP_TEST_ENV_VALUE}-suffix",
            },
          },
        },
      }),
      "utf8",
    );

    const oldCwd = process.cwd();
    process.chdir(extensionDir);
    try {
      const host = await createExtensionHost(mcpExtension);
      const init = host.initialize({
        protocolVersion: "2024-11-05",
        extension: { id: "mcp", cwd: workspace, dataDir: path.join(root, "data") },
      });
      assert.deepEqual(init.tools.map((tool) => tool.name), ["mcp__workspace_ping"]);
      assert.equal(init.tools[0]?.description, "Workspace Ping");
      assert.deepEqual(init.tools[0]?.inputSchema, {
        type: "object",
        properties: {
          mode: { type: "string", description: "Ping mode", enum: ["fast", "safe"] },
          target: { type: ["string", "null"] },
        },
        additionalProperties: false,
        "x-mcp-extension": { enabled: true },
      });
      const result = await host.executeTool({ name: "mcp__workspace_ping", input: {}, context: { cwd: workspace } });
      assert.equal(result.content, JSON.stringify({ cwd: workspace, bare: "expanded-value", braced: "expanded-value", mixed: "prefix-expanded-value-suffix" }));
      await host.handleEvent({ id: "session-end", event: "session.end", context: { cwd: workspace } });
    } finally {
      process.chdir(oldCwd);
    }
  } finally {
    restoreEnv("HOME", oldHome);
    restoreEnv("USERPROFILE", oldUserProfile);
    restoreEnv("KODELET_EXTENSION_WORKSPACE_CWD", oldWorkspaceCWD);
    restoreEnv("MCP_TEST_ENV_VALUE", oldEnvValue);
    await rm(root, { recursive: true, force: true });
  }
});

test("MCP remote HTTP headers expand environment variables", async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), "kodelet-mcp-http-headers-"));
  const oldHome = process.env.HOME;
  const oldUserProfile = process.env.USERPROFILE;
  const oldWorkspaceCWD = process.env.KODELET_EXTENSION_WORKSPACE_CWD;
  const oldToken = process.env.MCP_TEST_HEADER_TOKEN;
  const receivedAuthHeaders: string[] = [];
  const terminatedSessionIDs: string[] = [];
  const callbackPortHolder = http.createServer((_req, res) => res.writeHead(204).end());

  const server = http.createServer((req, res) => {
    if (req.method === "DELETE") {
      receivedAuthHeaders.push(req.headers.authorization ?? "");
      terminatedSessionIDs.push(req.headers["mcp-session-id"]?.toString() ?? "");
      res.writeHead(200).end();
      return;
    }
    if (req.method !== "POST") {
      res.writeHead(405).end();
      return;
    }

    const authorization = req.headers.authorization ?? "";
    receivedAuthHeaders.push(authorization);
    if (authorization !== "Bearer expanded-token") {
      res.writeHead(400, { "content-type": "text/plain" }).end("Authorization header is badly formatted");
      return;
    }

    let body = "";
    req.setEncoding("utf8");
    req.on("data", (chunk) => {
      body += chunk;
    });
    req.on("end", () => {
      const message = JSON.parse(body) as { id?: string | number; method?: string; params?: Record<string, unknown> };
      if (message.id === undefined) {
        res.writeHead(202).end();
        return;
      }

      let result: Record<string, unknown> = {};
      if (message.method === "initialize") {
        result = { protocolVersion: message.params?.protocolVersion ?? "2024-11-05", capabilities: { tools: {} }, serverInfo: { name: "fake-http", version: "1.0.0" } };
      } else if (message.method === "tools/list") {
        result = { tools: [{ name: "ping", description: "Ping from HTTP config", inputSchema: { type: "object" } }] };
      }

      res.writeHead(200, {
        "content-type": "application/json",
        ...(message.method === "initialize" ? { "mcp-session-id": "session-123" } : {}),
      }).end(JSON.stringify({ jsonrpc: "2.0", id: message.id, result }));
    });
  });

  try {
    const home = path.join(root, "home");
    const workspace = path.join(root, "workspace");
    await mkdir(path.join(home, ".kodelet"), { recursive: true });
    await mkdir(workspace, { recursive: true });

    process.env.HOME = home;
    delete process.env.USERPROFILE;
    process.env.KODELET_EXTENSION_WORKSPACE_CWD = workspace;
    process.env.MCP_TEST_HEADER_TOKEN = "expanded-token";

    await new Promise<void>((resolve, reject) => {
      server.once("error", reject);
      server.listen(0, "127.0.0.1", () => {
        server.off("error", reject);
        resolve();
      });
    });
    const address = server.address();
    assert(address && typeof address === "object");
    await new Promise<void>((resolve, reject) => {
      callbackPortHolder.once("error", reject);
      callbackPortHolder.listen(0, "127.0.0.1", () => {
        callbackPortHolder.off("error", reject);
        resolve();
      });
    });
    const callbackAddress = callbackPortHolder.address();
    assert(callbackAddress && typeof callbackAddress === "object");

    await writeFile(
      path.join(workspace, "mcp.json"),
      JSON.stringify({
        mcpServers: {
          remote: {
            type: "http",
            url: `http://127.0.0.1:${address.port}/mcp`,
            headers: { Authorization: "Bearer ${MCP_TEST_HEADER_TOKEN}" },
            oauth: { redirect_uri: `http://127.0.0.1:${callbackAddress.port}/mcp/oauth/callback` },
          },
        },
      }),
      "utf8",
    );

    const host = await createExtensionHost(mcpExtension);
    const init = host.initialize({
      protocolVersion: "2024-11-05",
      extension: { id: "mcp", cwd: workspace, dataDir: path.join(root, "data") },
    });
    assert.deepEqual(init.tools.map((tool) => tool.name), ["mcp__remote_ping"]);
    assert(receivedAuthHeaders.length >= 2);
    assert(receivedAuthHeaders.every((header) => header === "Bearer expanded-token"));
    await host.handleEvent({ id: "session-end", event: "session.end", context: { cwd: workspace } });
    assert.deepEqual(terminatedSessionIDs, ["session-123"]);
    assert.equal(receivedAuthHeaders.at(-1), "Bearer expanded-token");
  } finally {
    await new Promise<void>((resolve, reject) => {
      server.close((error) => error ? reject(error) : resolve());
    }).catch(() => undefined);
    await new Promise<void>((resolve, reject) => {
      callbackPortHolder.close((error) => error ? reject(error) : resolve());
    }).catch(() => undefined);
    restoreEnv("HOME", oldHome);
    restoreEnv("USERPROFILE", oldUserProfile);
    restoreEnv("KODELET_EXTENSION_WORKSPACE_CWD", oldWorkspaceCWD);
    restoreEnv("MCP_TEST_HEADER_TOKEN", oldToken);
    await rm(root, { recursive: true, force: true });
  }
});

test("MCP configured headers are scoped and do not override OAuth", async () => {
  const requests: Array<{ url: string; headers: Record<string, string> }> = [];
  const baseFetch = async (url: string | URL, init?: RequestInit): Promise<Response> => {
    requests.push({ url: String(url), headers: Object.fromEntries(new Headers(init?.headers).entries()) });
    return new Response(null, { status: 204 });
  };
  const mcpUrl = new URL("https://mcp.example/rpc");
  const scopedFetch = scopedMCPFetch(mcpUrl, {
    Authorization: "Bearer configured-token",
    "X-MCP-Secret": "secret",
  }, baseFetch);

  await scopedFetch(mcpUrl, { headers: { Authorization: "Bearer oauth-token" } });
  await scopedFetch("https://auth.example/token", { method: "POST", body: "grant_type=authorization_code" });
  await scopedFetch("https://mcp.example/messages", {
    method: "POST",
    body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/call" }),
  });
  await scopedFetch("https://mcp.example/oauth/token", { method: "POST", body: "grant_type=refresh_token" });

  assert.equal(requests[0]?.headers.authorization, "Bearer oauth-token");
  assert.equal(requests[0]?.headers["x-mcp-secret"], "secret");
  assert.equal(requests[1]?.headers.authorization, undefined);
  assert.equal(requests[1]?.headers["x-mcp-secret"], undefined);
  assert.equal(requests[2]?.headers.authorization, "Bearer configured-token");
  assert.equal(requests[2]?.headers["x-mcp-secret"], "secret");
  assert.equal(requests[3]?.headers.authorization, undefined);
  assert.equal(requests[3]?.headers["x-mcp-secret"], undefined);
});

test("MCP OAuth callback listener is lazy and can be released after authorization", async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), "kodelet-mcp-oauth-callback-"));
  const oldHome = process.env.HOME;
  const oldUserProfile = process.env.USERPROFILE;
  const portProbe = http.createServer((_req, res) => res.writeHead(204).end());
  let provider: KodeletMCPOAuthProvider | undefined;
  try {
    process.env.HOME = root;
    delete process.env.USERPROFILE;

    await new Promise<void>((resolve, reject) => {
      portProbe.once("error", reject);
      portProbe.listen(0, "127.0.0.1", () => {
        portProbe.off("error", reject);
        resolve();
      });
    });
    const address = portProbe.address();
    assert(address && typeof address === "object");
    const redirectUri = `http://127.0.0.1:${address.port}/mcp/oauth/callback`;
    await new Promise<void>((resolve, reject) => portProbe.close((error) => error ? reject(error) : resolve()));

    provider = new KodeletMCPOAuthProvider({
      serverName: "callback",
      serverUrl: "https://mcp.example/mcp",
      config: {
        client_id: "configured-client",
        redirect_uri: redirectUri,
        interactive: true,
        open_browser: false,
        callback_timeout: "1s",
      },
    });

    const availableBeforeAuth = http.createServer((_req, res) => res.writeHead(204).end());
    await new Promise<void>((resolve, reject) => {
      availableBeforeAuth.once("error", reject);
      availableBeforeAuth.listen(address.port, "127.0.0.1", () => {
        availableBeforeAuth.off("error", reject);
        resolve();
      });
    });
    await new Promise<void>((resolve, reject) => availableBeforeAuth.close((error) => error ? reject(error) : resolve()));

    const state = await provider.state();
    assert.equal(String(provider.redirectUrl), redirectUri);
    await provider.redirectToAuthorization(new URL(`https://auth.example/authorize?state=${state}`));
    const response = await fetch(`${redirectUri}?code=authorization-code&state=${state}`);
    assert.equal(response.status, 200);
    assert.equal(await provider.waitForAuthorizationCode(), "authorization-code");
    await provider.close();

    await new Promise<void>((resolve, reject) => {
      portProbe.once("error", reject);
      portProbe.listen(address.port, "127.0.0.1", () => {
        portProbe.off("error", reject);
        resolve();
      });
    });
  } finally {
    await provider?.close().catch(() => undefined);
    await new Promise<void>((resolve, reject) => portProbe.close((error) => error ? reject(error) : resolve())).catch(() => undefined);
    restoreEnv("HOME", oldHome);
    restoreEnv("USERPROFILE", oldUserProfile);
    await rm(root, { recursive: true, force: true });
  }
});

test("MCP OAuth provider expands config and persists client information and tokens", async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), "kodelet-mcp-oauth-"));
  const oldHome = process.env.HOME;
  const oldUserProfile = process.env.USERPROFILE;
  const oldClientId = process.env.MCP_TEST_CLIENT_ID;
  try {
    process.env.HOME = root;
    delete process.env.USERPROFILE;
    process.env.MCP_TEST_CLIENT_ID = "configured-client";

    const provider = new KodeletMCPOAuthProvider({
      serverName: "Remote Server!",
      serverUrl: "https://mcp.example/mcp",
      globalConfig: { interactive: "never", callback_timeout: "1s" },
      config: { client_id: "$MCP_TEST_CLIENT_ID", client_secret: "${MCP_TEST_CLIENT_ID}-secret", scopes: ["read", "write"] },
    });

    assert.deepEqual(await provider.clientInformation(), {
      client_id: "configured-client",
      client_secret: "configured-client-secret",
    });
    assert.equal(provider.clientMetadata.scope, "read write");

    await provider.saveTokens({ access_token: "access-token", token_type: "Bearer", refresh_token: "refresh-token" });
    assert.deepEqual(await provider.tokens(), { access_token: "access-token", token_type: "Bearer", refresh_token: "refresh-token" });

    await provider.saveCodeVerifier("verifier");
    assert.equal(await provider.codeVerifier(), "verifier");

    const oauthDir = path.join(root, ".kodelet", "mcp", "oauth");
    const files = await readdir(oauthDir);
    assert.equal(files.length, 1);
    assert.match(files[0] ?? "", /^remote_server-[a-f0-9]{12}\.json$/);

    const stored = JSON.parse(await readFile(path.join(oauthDir, files[0] ?? ""), "utf8")) as Record<string, unknown>;
    assert.deepEqual(stored.tokens, { access_token: "access-token", token_type: "Bearer", refresh_token: "refresh-token" });
  } finally {
    restoreEnv("HOME", oldHome);
    restoreEnv("USERPROFILE", oldUserProfile);
    restoreEnv("MCP_TEST_CLIENT_ID", oldClientId);
    await rm(root, { recursive: true, force: true });
  }
});

test("MCP OAuth provider reads legacy core OAuth credential files", async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), "kodelet-mcp-oauth-legacy-"));
  const oldHome = process.env.HOME;
  const oldUserProfile = process.env.USERPROFILE;
  try {
    process.env.HOME = root;
    delete process.env.USERPROFILE;

    const tokenDir = path.join(root, ".kodelet", "mcp", "oauth");
    await mkdir(tokenDir, { recursive: true });
    await writeFile(
      path.join(tokenDir, "remote_server-b1b747a21dbf.json"),
      JSON.stringify({
        token: { access_token: "legacy-access", token_type: "Bearer", refresh_token: "legacy-refresh", scope: "read" },
        client_id: "legacy-client",
        client_secret: "legacy-secret",
        auth_server_metadata_url: "https://auth.example/.well-known/oauth-authorization-server",
      }),
      "utf8",
    );

    const provider = new KodeletMCPOAuthProvider({ serverName: "Remote Server!", serverUrl: "https://mcp.example/mcp" });
    assert.deepEqual(await provider.tokens(), {
      access_token: "legacy-access",
      token_type: "Bearer",
      refresh_token: "legacy-refresh",
      scope: "read",
    });
    assert.deepEqual(await provider.clientInformation(), { client_id: "legacy-client", client_secret: "legacy-secret" });
  } finally {
    restoreEnv("HOME", oldHome);
    restoreEnv("USERPROFILE", oldUserProfile);
    await rm(root, { recursive: true, force: true });
  }
});

test("returns empty MCP config when mcp.json files are absent", async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), "kodelet-mcp-empty-"));
  const oldHome = process.env.HOME;
  const oldUserProfile = process.env.USERPROFILE;
  try {
    const home = path.join(root, "home");
    const cwd = path.join(root, "workspace");
    await mkdir(cwd, { recursive: true });

    process.env.HOME = home;
    delete process.env.USERPROFILE;

    assert.deepEqual(await loadMCPConfig(cwd), {});
  } finally {
    restoreEnv("HOME", oldHome);
    restoreEnv("USERPROFILE", oldUserProfile);
    await rm(root, { recursive: true, force: true });
  }
});

function restoreEnv(name: string, value: string | undefined): void {
  if (value === undefined) {
    delete process.env[name];
  } else {
    process.env[name] = value;
  }
}
