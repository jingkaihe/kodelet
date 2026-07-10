import assert from "node:assert/strict";
import { mkdir, mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { loadMCPConfig } from "./extensions/mcp/config.js";
import { KodeletMCPOAuthProvider } from "./extensions/mcp/oauth.js";

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
