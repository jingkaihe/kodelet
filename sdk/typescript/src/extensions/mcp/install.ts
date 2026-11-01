#!/usr/bin/env node

import { chmod, mkdir, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const pluginName = "kodelet@mcp";
const extensionName = "mcp";

await installMCPPlugin();

export async function installMCPPlugin(): Promise<void> {
  if (skipInstall()) {
    return;
  }

  const home = os.homedir();
  if (!home) {
    process.stderr.write("kodelet MCP plugin install skipped: home directory is unavailable\n");
    return;
  }

  const packageRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "..", "..");
  const runner = path.join(packageRoot, "dist", "bin", "kodelet-extension-node.js");
  const entrypoint = path.join(packageRoot, "dist", "extensions", "mcp", "index.js");
  const extensionDir = path.join(home, ".kodelet", "plugins", pluginName, "extensions", extensionName);
  const executable = path.join(extensionDir, "kodelet-extension-mcp");

  await mkdir(extensionDir, { recursive: true });
  await writeFile(executable, extensionWrapper(runner, entrypoint), "utf8");
  await chmod(executable, 0o755);
}

function skipInstall(): boolean {
  const value = process.env.KODELET_SKIP_MCP_PLUGIN_INSTALL;
  return value === "1" || value?.toLowerCase() === "true";
}

function extensionWrapper(runner: string, entrypoint: string): string {
  return `#!/usr/bin/env sh
exec node ${shellQuote(runner)} ${shellQuote(entrypoint)} "$@"
`;
}

function shellQuote(value: string): string {
  return `'${value.replaceAll("'", "'\\''")}'`;
}
