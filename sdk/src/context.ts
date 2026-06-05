import { AsyncLocalStorage } from "node:async_hooks";
import { execFile, spawn as spawnProcess } from "node:child_process";
import { promises as fs } from "node:fs";
import os from "node:os";
import path from "node:path";
import nodeProcess from "node:process";
import { promisify } from "node:util";

import type {
  BaseCallContext,
  CommandContext,
  CommandInvocation,
  EventContext,
  ExecResult,
  FileInfo,
  InitializeParams,
  LogContext,
  SharedContext,
  ToolContext,
  UIConfirmRequest,
  UIInputRequest,
  UINotifyRequest,
  UISelectRequest,
} from "./types.js";

const execFileAsync = promisify(execFile);

export interface HostRPCClient {
  request(method: string, params?: unknown): Promise<unknown>;
}

let activeHostRPCClient: HostRPCClient | undefined;
const hostRPCClientStorage = new AsyncLocalStorage<HostRPCClient | undefined>();

export function setActiveHostRPCClient(client: HostRPCClient | undefined): void {
  activeHostRPCClient = client;
}

export async function runWithHostRPCClient<T>(client: HostRPCClient | undefined, fn: () => Promise<T>): Promise<T> {
  return await hostRPCClientStorage.run(client, fn);
}

function currentHostRPCClient(): HostRPCClient | undefined {
  return hostRPCClientStorage.getStore() ?? activeHostRPCClient;
}

export function createToolContext(init: InitializeParams | undefined, context: BaseCallContext = {}): ToolContext {
  return createSharedContext(init, context);
}

export function createCommandContext(
  init: InitializeParams | undefined,
  context: BaseCallContext = {},
  invocation: CommandInvocation,
): CommandContext {
  return {
    ...createSharedContext(init, context),
    input: invocation,
  };
}

export function createEventContext(init: InitializeParams | undefined, context: BaseCallContext = {}): EventContext {
  return createSharedContext(init, context);
}

function createSharedContext(init: InitializeParams | undefined, context: BaseCallContext): SharedContext {
  const cwd = path.resolve(context.cwd ?? init?.extension.cwd ?? nodeProcess.cwd());
  const dataDir = path.resolve(
    init?.extension.dataDir || path.join(os.homedir(), ".kodelet", "extensions", "data", init?.extension.id ?? "extension"),
  );
  const log = createLogger(init?.extension.id);

  const resolveWorkspacePath = (target: string): string => {
    const resolved = path.resolve(cwd, target || ".");
    if (!isPathInside(resolved, cwd)) {
      throw new Error(`Path escapes workspace: ${target}`);
    }
    return resolved;
  };

  const resolveStoragePath = (target: string): string => {
    const resolved = path.resolve(dataDir, target || ".");
    if (!isPathInside(resolved, dataDir)) {
      throw new Error(`Path escapes extension storage: ${target}`);
    }
    return resolved;
  };

  const resolveFsPath = (target: string): string => (path.isAbsolute(target) ? target : resolveWorkspacePath(target));

  return {
    sessionId: context.sessionId,
    conversationId: context.conversationId,
    cwd,
    provider: context.provider,
    model: context.model,
    profile: context.profile,
    recipeName: context.recipeName,
    invokedBy: context.invokedBy,
    storage: {
      dataDir,
      async readText(target) {
        try {
          return await fs.readFile(resolveStoragePath(target), "utf8");
        } catch (error) {
          if (isNotFound(error)) {
            return undefined;
          }
          throw error;
        }
      },
      async writeText(target, content) {
        const resolved = resolveStoragePath(target);
        await fs.mkdir(path.dirname(resolved), { recursive: true });
        await fs.writeFile(resolved, content, "utf8");
      },
      async readJson<T = unknown>(target: string): Promise<T | undefined> {
        const content = await this.readText(target);
        return content === undefined ? undefined : (JSON.parse(content) as T);
      },
      async writeJson(target, value) {
        await this.writeText(target, `${JSON.stringify(value, null, 2)}\n`);
      },
    },
    path: {
      resolveWorkspacePath,
      relativeToWorkspace(target) {
        const resolved = path.isAbsolute(target) ? target : path.resolve(cwd, target);
        return path.relative(cwd, resolved) || ".";
      },
    },
    fs: {
      async exists(target) {
        try {
          await fs.access(resolveFsPath(target));
          return true;
        } catch (error) {
          if (isNotFound(error)) {
            return false;
          }
          throw error;
        }
      },
      async readText(target) {
        return await fs.readFile(resolveFsPath(target), "utf8");
      },
      async writeText(target, content) {
        const resolved = resolveFsPath(target);
        await fs.mkdir(path.dirname(resolved), { recursive: true });
        await fs.writeFile(resolved, content, "utf8");
      },
      async list(target) {
        const resolved = resolveFsPath(target);
        const entries = await fs.readdir(resolved, { withFileTypes: true });
        return entries.map((entry): FileInfo => ({
          name: entry.name,
          path: path.join(resolved, entry.name),
          type: entry.isFile() ? "file" : entry.isDirectory() ? "dir" : "other",
        }));
      },
    },
    process: {
      async exec(command, args = [], opts = {}): Promise<ExecResult> {
        try {
          const result = await execFileAsync(command, args, {
            cwd: opts.cwd ? resolveWorkspacePath(opts.cwd) : cwd,
            timeout: opts.timeoutMs,
            encoding: "utf8",
            maxBuffer: 10 * 1024 * 1024,
          });
          return { stdout: result.stdout, stderr: result.stderr, exitCode: 0 };
        } catch (error) {
          const execError = error as { stdout?: string; stderr?: string; code?: number | string };
          return {
            stdout: execError.stdout ?? "",
            stderr: execError.stderr ?? "",
            exitCode: typeof execError.code === "number" ? execError.code : 1,
          };
        }
      },
      async spawn(command, args = [], opts = {}) {
        await new Promise<void>((resolve, reject) => {
          const child = spawnProcess(command, args, {
            cwd: opts.cwd ? resolveWorkspacePath(opts.cwd) : cwd,
            detached: opts.detach,
            stdio: opts.detach ? "ignore" : "inherit",
          });
          child.once("error", reject);
          if (opts.detach) {
            child.unref();
            child.once("spawn", resolve);
            return;
          }
          child.once("close", (code) => {
            if (code === 0) {
              resolve();
            } else {
              reject(new Error(`${command} exited with status ${code ?? "unknown"}`));
            }
          });
        });
      },
    },
    env: {
      get(name) {
        return nodeProcess.env[name];
      },
    },
    log,
    ui: {
      async input(request: UIInputRequest) {
        const client = currentHostRPCClient();
        if (!client) {
          return undefined;
        }
        const result = await client.request("kodelet.ui.input", request);
        if (isRecord(result) && result.status === "submitted" && typeof result.value === "string") {
          return result.value;
        }
        return undefined;
      },
      async confirm(request: UIConfirmRequest) {
        const client = currentHostRPCClient();
        if (!client) {
          return false;
        }
        const result = await client.request("kodelet.ui.confirm", request);
        return isRecord(result) && result.status === "submitted" && result.confirmed === true;
      },
      async select(request: UISelectRequest) {
        const client = currentHostRPCClient();
        if (!client) {
          return undefined;
        }
        const result = await client.request("kodelet.ui.select", request);
        if (isRecord(result) && result.status === "submitted" && typeof result.value === "string") {
          return result.value;
        }
        return undefined;
      },
      async notify(request: string | UINotifyRequest) {
        const client = currentHostRPCClient();
        if (!client) {
          return;
        }
        const payload = typeof request === "string" ? { message: request } : request;
        await client.request("kodelet.ui.notify", payload);
      },
    },
  };
}

function createLogger(extensionId: string | undefined): LogContext {
  const write = (level: string, message: string, fields?: Record<string, unknown>) => {
    const payload = {
      level,
      extension: extensionId,
      message,
      ...fields,
    };
    nodeProcess.stderr.write(`${JSON.stringify(payload)}\n`);
  };
  return {
    debug: (message, fields) => write("debug", message, fields),
    info: (message, fields) => write("info", message, fields),
    warn: (message, fields) => write("warn", message, fields),
    error: (message, fields) => write("error", message, fields),
  };
}

function isPathInside(target: string, parent: string): boolean {
  const relative = path.relative(parent, target);
  return relative === "" || (!relative.startsWith("..") && !path.isAbsolute(relative));
}

function isNotFound(error: unknown): boolean {
  return typeof error === "object" && error !== null && "code" in error && (error as { code?: string }).code === "ENOENT";
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
