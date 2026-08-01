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
  ToolUpdateRequest,
  UIConfirmRequest,
  UIFrameLine,
  UIInputRequest,
  UINotifyRequest,
  UITranscriptAppendRequest,
  UISelectRequest,
  UISurface,
  UISurfaceInputEvent,
  UISurfaceResizeEvent,
} from "./types.js";

const execFileAsync = promisify(execFile);

export interface HostRPCClient {
  request(method: string, params?: unknown): Promise<unknown>;
  requestPersistent?(method: string, params?: unknown): Promise<unknown>;
  notify?(method: string, params?: unknown): void | Promise<void>;
  onNotification?(handler: (method: string, params: unknown) => void): () => void;
  persistent?: HostRPCClient;
}

let activeHostRPCClient: HostRPCClient | undefined;
const hostRPCClientStorage = new AsyncLocalStorage<HostRPCClient | undefined>();
const widgetSequencesByClient = new WeakMap<HostRPCClient, Map<string, number>>();
const surfaceSequencesByClient = new WeakMap<HostRPCClient, Map<string, number>>();
const activeSurfacesByClient = new WeakMap<HostRPCClient, Map<string, UISurfaceHandle>>();
const notificationClients = new WeakSet<HostRPCClient>();

export function setActiveHostRPCClient(client: HostRPCClient | undefined): void {
  activeHostRPCClient = client;
  ensurePersistentNotificationRouting(client);
}

export async function runWithHostRPCClient<T>(client: HostRPCClient | undefined, fn: () => Promise<T>): Promise<T> {
  return await hostRPCClientStorage.run(client, fn);
}

function currentHostRPCClient(): HostRPCClient | undefined {
  return hostRPCClientStorage.getStore() ?? activeHostRPCClient;
}

export function createToolContext(
  init: InitializeParams | undefined,
  context: BaseCallContext = {},
  signal: AbortSignal = new AbortController().signal,
): ToolContext {
  const client = currentHostRPCClient();
  return {
    ...createSharedContext(init, context, signal, client),
    async update(content: string, data?: Record<string, unknown>) {
      if (!toolUpdatesSupported(init)) {
        return;
      }
      if (!client) {
        return;
      }
      const payload: ToolUpdateRequest = { content };
      if (data !== undefined) {
        payload.data = data;
      }
      await client.request("kodelet.tool.update", payload);
    },
  };
}

export function createCommandContext(
  init: InitializeParams | undefined,
  context: BaseCallContext = {},
  invocation: CommandInvocation,
  signal: AbortSignal = new AbortController().signal,
): CommandContext {
  const client = currentHostRPCClient();
  return {
    ...createSharedContext(init, context, signal, client),
    input: invocation,
  };
}

export function createEventContext(
  init: InitializeParams | undefined,
  context: BaseCallContext = {},
  signal: AbortSignal = new AbortController().signal,
): EventContext {
  return createSharedContext(init, context, signal, currentHostRPCClient());
}

function createSharedContext(
  init: InitializeParams | undefined,
  context: BaseCallContext,
  signal: AbortSignal,
  client: HostRPCClient | undefined,
): SharedContext {
  const cwd = path.resolve(context.cwd ?? init?.extension.cwd ?? nodeProcess.cwd());
  const dataDir = path.resolve(
    init?.extension.dataDir || path.join(os.homedir(), ".kodelet", "extensions", "data", init?.extension.id ?? "extension"),
  );
  const log = createLogger(init?.extension.id);
  const persistentClient = client?.persistent ?? client;
  const uiScopeId = context.uiScopeId?.trim() ?? "";

  const requestPersistentUI = async (method: string, params?: unknown): Promise<unknown> => {
    const scopedParams = withUIScope(params, uiScopeId);
    if (client?.requestPersistent) {
      return await client.requestPersistent(method, scopedParams);
    }
    if (!persistentClient) {
      throw new Error("Persistent extension UI is not available in this host");
    }
    return await persistentClient.request(method, scopedParams);
  };

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
    signal,
    sessionId: context.sessionId,
    conversationId: context.conversationId,
    uiScopeId: uiScopeId || undefined,
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
        if (!client) {
          return false;
        }
        const result = await client.request("kodelet.ui.confirm", request);
        return isRecord(result) && result.status === "submitted" && result.confirmed === true;
      },
      async select(request: UISelectRequest) {
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
        if (!client) {
          return;
        }
        const payload = typeof request === "string" ? { message: request } : request;
        await client.request("kodelet.ui.notify", payload);
      },
      async appendTranscript(request: string | UITranscriptAppendRequest) {
        if (!extensionUISupported(init, "transcript") || !persistentClient) {
          return;
        }
        const payload = typeof request === "string" ? { message: request } : request;
        await requestPersistentUI("kodelet.ui.transcript.append", payload);
      },
      async setWidget(id, lines, options) {
        if (!extensionUISupported(init, "widgets") || !persistentClient) {
          return;
        }
        const objectID = validateUIObjectID(id);
        const sequence = nextClientSequence(widgetSequencesByClient, persistentClient, uiObjectKey(uiScopeId, objectID));
        if (lines === undefined) {
          await requestPersistentUI("kodelet.ui.widget.remove", { id: objectID, sequence });
          return;
        }
        await requestPersistentUI("kodelet.ui.widget.set", {
          id: objectID,
          placement: options?.placement ?? "aboveComposer",
          frame: { sequence, lines },
        });
      },
      async openSurface(options) {
        if (!extensionUISupported(init, "surfaces") || !persistentClient) {
          throw new Error("Interactive extension surfaces are not available in this host");
        }
        const { id: requestedID, initialLines = [], ...surfaceOptions } = options;
        const id = validateUIObjectID(requestedID);
        const activeSurfaces = surfacesForClient(persistentClient);
        const routingKey = uiObjectKey(uiScopeId, id);
        const existing = activeSurfaces.get(routingKey);
        if (existing) {
          throw new Error(
            `Interactive surface "${id}" is already open, opening, or closing; close it before reusing the ID`,
          );
        }
        const surface = new UISurfaceHandle(id, uiScopeId, persistentClient);
        activeSurfaces.set(routingKey, surface);
        ensurePersistentNotificationRouting(persistentClient);
        try {
          const response = await requestPersistentUI("kodelet.ui.surface.open", {
            id,
            options: surfaceOptions,
            frame: { sequence: surface.nextSequence(), lines: initialLines },
          });
          if (isRecord(response) && response.accepted === false) {
            throw new Error(typeof response.reason === "string" ? response.reason : "The host rejected the interactive surface");
          }
        } catch (error) {
          if (activeSurfaces.get(routingKey) === surface) {
            activeSurfaces.delete(routingKey);
          }
          throw error;
        }
        surface.activate();
        return surface;
      },
    },
  };
}

class UISurfaceHandle implements UISurface {
  private closed = false;
  private closing: Promise<void> | undefined;
  private active = false;
  private pendingLines: UIFrameLine[] | undefined;
  private frameScheduled = false;
  private frameInFlight = false;
  private latestEventSequence = 0;
  private inputHandlers = new Set<(event: UISurfaceInputEvent) => void>();
  private pendingFocusEvent: UISurfaceInputEvent | undefined;
  private resizeHandlers = new Set<(event: UISurfaceResizeEvent) => void>();
  private pendingResizeEvent: UISurfaceResizeEvent | undefined;
  private currentSize: { width: number; height: number } | undefined;

  constructor(
    readonly id: string,
    private readonly scopeId: string,
    private readonly client: HostRPCClient,
  ) {}

  get size(): { width: number; height: number } | undefined {
    return this.currentSize ? { ...this.currentSize } : undefined;
  }

  nextSequence(): number {
    return nextClientSequence(surfaceSequencesByClient, this.client, this.routingKey());
  }

  activate(): void {
    this.active = true;
  }

  update(lines: UIFrameLine[]): void {
    if (this.closed || this.closing) {
      return;
    }
    this.pendingLines = lines;
    this.scheduleFrameFlush();
  }

  private scheduleFrameFlush(): void {
    if (this.closed || this.closing || this.frameScheduled || this.frameInFlight || this.pendingLines === undefined) {
      return;
    }
    this.frameScheduled = true;
    queueMicrotask(() => {
      this.frameScheduled = false;
      void this.flushFrame();
    });
  }

  async close(): Promise<void> {
    if (this.closed) {
      return;
    }
    if (this.closing) {
      return await this.closing;
    }
    const operation = this.closeOnce();
    this.closing = operation;
    try {
      await operation;
    } finally {
      if (this.closing === operation) {
        this.closing = undefined;
      }
      this.scheduleFrameFlush();
    }
  }

  private async closeOnce(): Promise<void> {
    if (this.active) {
      const response = await this.client.request("kodelet.ui.surface.close", withUIScope({
        id: this.id,
        sequence: this.nextSequence(),
      }, this.scopeId));
      if (isRecord(response) && response.accepted === false) {
        throw new Error(typeof response.reason === "string" ? response.reason : "The host rejected the surface close");
      }
    }
    this.closed = true;
    this.pendingLines = undefined;
    this.inputHandlers.clear();
    this.pendingFocusEvent = undefined;
    this.resizeHandlers.clear();
    this.pendingResizeEvent = undefined;
    const activeSurfaces = surfacesForClient(this.client);
    if (activeSurfaces.get(this.routingKey()) === this) {
      activeSurfaces.delete(this.routingKey());
    }
  }

  onInput(handler: (event: UISurfaceInputEvent) => void): () => void {
    this.inputHandlers.add(handler);
    const pendingFocusEvent = this.pendingFocusEvent;
    this.pendingFocusEvent = undefined;
    if (pendingFocusEvent) {
      handler(pendingFocusEvent);
    }
    return () => this.inputHandlers.delete(handler);
  }

  onResize(handler: (event: UISurfaceResizeEvent) => void): () => void {
    this.resizeHandlers.add(handler);
    const pendingResizeEvent = this.pendingResizeEvent;
    this.pendingResizeEvent = undefined;
    if (pendingResizeEvent) {
      handler(pendingResizeEvent);
    }
    return () => this.resizeHandlers.delete(handler);
  }

  handleNotification(method: string, params: unknown): void {
    if (
      this.closed ||
      !isRecord(params) ||
      params.id !== this.id ||
      normalizedUIScope(params.scopeId) !== this.scopeId ||
      typeof params.sequence !== "number"
    ) {
      return;
    }
    if (params.sequence <= this.latestEventSequence) {
      return;
    }
    this.latestEventSequence = params.sequence;
    if (method === "extension.ui.surface.input" && isSurfaceInputEvent(params)) {
      if (this.inputHandlers.size === 0) {
        if (!this.active && (params.kind === "focus" || params.kind === "blur")) {
          this.pendingFocusEvent = params;
        }
        return;
      }
      for (const handler of this.inputHandlers) {
        handler(params);
      }
      return;
    }
    if (method === "extension.ui.surface.resize" && typeof params.width === "number" && typeof params.height === "number") {
      const event: UISurfaceResizeEvent = { sequence: params.sequence, width: params.width, height: params.height };
      this.currentSize = { width: event.width, height: event.height };
      if (this.resizeHandlers.size === 0) {
        this.pendingResizeEvent = this.active ? undefined : event;
        return;
      }
      for (const handler of this.resizeHandlers) {
        handler(event);
      }
    }
  }

  private async flushFrame(): Promise<void> {
    if (this.closed || this.closing || this.frameInFlight) {
      return;
    }
    const lines = this.pendingLines;
    this.pendingLines = undefined;
    if (lines === undefined) {
      return;
    }
    this.frameInFlight = true;
    const params = withUIScope({ id: this.id, frame: { sequence: this.nextSequence(), lines } }, this.scopeId);
    try {
      if (this.client.notify) {
        await this.client.notify("kodelet.ui.surface.frame", params);
      } else {
        await this.client.request("kodelet.ui.surface.frame", params);
      }
    } catch {
      // The host connection is already gone; process cleanup removes the surface.
    } finally {
      this.frameInFlight = false;
      this.scheduleFrameFlush();
    }
  }

  private routingKey(): string {
    return uiObjectKey(this.scopeId, this.id);
  }
}

function ensurePersistentNotificationRouting(client: HostRPCClient | undefined): void {
  if (!client?.onNotification || notificationClients.has(client)) {
    return;
  }
  notificationClients.add(client);
  client.onNotification((method, params) => {
    if (!isRecord(params) || typeof params.id !== "string") {
      return;
    }
    surfacesForClient(client).get(uiObjectKey(normalizedUIScope(params.scopeId), params.id))?.handleNotification(method, params);
  });
}

function surfacesForClient(client: HostRPCClient): Map<string, UISurfaceHandle> {
  let surfaces = activeSurfacesByClient.get(client);
  if (!surfaces) {
    surfaces = new Map<string, UISurfaceHandle>();
    activeSurfacesByClient.set(client, surfaces);
  }
  return surfaces;
}

function nextClientSequence(
  sequencesByClient: WeakMap<HostRPCClient, Map<string, number>>,
  client: HostRPCClient,
  id: string,
): number {
  let sequences = sequencesByClient.get(client);
  if (!sequences) {
    sequences = new Map<string, number>();
    sequencesByClient.set(client, sequences);
  }
  const sequence = (sequences.get(id) ?? 0) + 1;
  sequences.set(id, sequence);
  return sequence;
}

function withUIScope(params: unknown, scopeId: string): unknown {
  if (!isRecord(params)) {
    return params;
  }
  return { ...params, scopeId };
}

function normalizedUIScope(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function uiObjectKey(scopeId: string, id: string): string {
  return `${scopeId}\0${id}`;
}

function validateUIObjectID(id: string): string {
  if (id.trim() === "") {
    throw new Error("Extension UI id is required");
  }
  if (id !== id.trim()) {
    throw new Error("Extension UI id must not have leading or trailing whitespace");
  }
  if (new TextEncoder().encode(id).length > 128) {
    throw new Error("Extension UI id is too long");
  }
  return id;
}

function extensionUISupported(init: InitializeParams | undefined, feature: "widgets" | "surfaces" | "transcript"): boolean {
  const ui = init?.capabilities?.ui;
  return isRecord(ui) && ui[feature] === true;
}

function isSurfaceInputEvent(value: Record<string, unknown>): value is Record<string, unknown> & UISurfaceInputEvent {
  return value.kind === "key" || value.kind === "mouse" || value.kind === "focus" || value.kind === "blur";
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

function toolUpdatesSupported(init: InitializeParams | undefined): boolean {
  const capabilities = init?.capabilities;
  if (!capabilities) {
    return false;
  }
  if (capabilities.toolUpdates === true) {
    return true;
  }
  return isRecord(capabilities.tools) && capabilities.tools.updates === true;
}
