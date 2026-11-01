import nodeProcess from "node:process";

import { createExtensionHost, type ExtensionHost } from "./api.js";
import { HostRPCError, runWithHostRPCClient, type HostRPCClient } from "./context.js";
import type { ExtensionEntrypoint, HandleEventParams } from "./types.js";

const hostDisconnectShutdownTimeoutMs = 1000;

interface JsonRpcRequest {
  jsonrpc: "2.0";
  id?: number | string | null;
  parentId?: number | string;
  method: string;
  params?: unknown;
}

interface JsonRpcResponse {
  jsonrpc: "2.0";
  id: number | string | null;
  result?: unknown;
  error?: {
    code: number;
    message: string;
    data?: unknown;
  };
}

export type ExtensionRPCMessage = JsonRpcRequest | JsonRpcResponse;

export interface ExtensionRuntimeTransport {
  send(message: ExtensionRPCMessage): Promise<void>;
  /** Return undefined to forward the request to the runner instead. */
  request?(method: string, params: unknown, signal: AbortSignal): Promise<unknown> | undefined;
}

interface PendingRequest {
  request?: ActiveRequest;
  resolve(value: unknown): void;
  reject(error: Error): void;
}

type SessionEndHandler = (params: HandleEventParams<"session.end">, signal?: AbortSignal) => Promise<unknown>;

class ActiveRequest {
  readonly controller = new AbortController();
  readonly cancelled: Promise<never>;
  active = true;
  private rejectCancellation!: (error: Error) => void;

  constructor(readonly id: number | string) {
    this.cancelled = new Promise<never>((_, reject) => {
      this.rejectCancellation = reject;
    });
  }

  cancel(error: Error): void {
    if (!this.active) {
      return;
    }
    this.active = false;
    this.controller.abort(error);
    this.rejectCancellation(error);
  }

  finish(): void {
    const wasActive = this.active;
    this.active = false;
    if (wasActive) {
      this.controller.abort(new Error("Extension request completed"));
    }
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

class RuntimeHostRPCClient implements HostRPCClient {
  private nextId = 0;
  private pending = new Map<number, PendingRequest>();
  private notificationHandlers = new Set<(method: string, params: unknown) => void>();
  private readonly controller = new AbortController();

  constructor(private readonly transport: ExtensionRuntimeTransport, private readonly fail: (error: Error) => void) {}

  send(message: ExtensionRPCMessage): void {
    if (this.controller.signal.aborted) return;
    void Promise.resolve().then(() => {
      if (!this.controller.signal.aborted) return this.transport.send(message);
    }).catch((error) => this.fail(asError(error)));
  }

  request(method: string, params?: unknown): Promise<unknown> {
    return this.requestFor(undefined, method, params);
  }

  notify(method: string, params?: unknown): Promise<void> {
    this.controller.signal.throwIfAborted();
    return this.transport.send({ jsonrpc: "2.0", method, params }).catch((error) => {
      this.fail(asError(error));
      throw error;
    });
  }

  onNotification(handler: (method: string, params: unknown) => void): () => void {
    this.notificationHandlers.add(handler);
    return () => this.notificationHandlers.delete(handler);
  }

  handleNotification(method: string, params: unknown): void {
    for (const handler of this.notificationHandlers) {
      handler(method, params);
    }
  }

  requestFor(request: ActiveRequest | undefined, method: string, params?: unknown): Promise<unknown> {
    this.controller.signal.throwIfAborted();
    if (request && !request.active) {
      throw new Error("Extension request is no longer active");
    }
    const id = ++this.nextId;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject, request });
      void Promise.resolve().then(async () => {
        if (!this.pending.has(id)) return;
        const local = this.transport.request?.(method, params, request?.controller.signal ?? this.controller.signal);
        if (local !== undefined) {
          this.handleResponse({ jsonrpc: "2.0", id, result: await local });
        } else {
          try {
            await this.transport.send({ jsonrpc: "2.0", id, parentId: request?.id, method, params });
          } catch (error) {
            this.fail(asError(error));
          }
        }
      }).catch((error) => {
        this.pending.get(id)?.reject(asError(error));
        this.pending.delete(id);
      });
    });
  }

  close(error: Error): void {
    this.controller.abort(error);
    for (const pending of this.pending.values()) pending.reject(error);
    this.pending.clear();
    this.notificationHandlers.clear();
  }

  finishRequest(request: ActiveRequest, error = new Error("Extension request completed")): void {
    request.finish();
    for (const [reverseId, pending] of this.pending) {
      if (pending.request === request) {
        this.pending.delete(reverseId);
        pending.reject(error);
      }
    }
  }

  handleResponse(response: JsonRpcResponse): boolean {
    if (typeof response.id !== "number") {
      return false;
    }
    const pending = this.pending.get(response.id);
    if (!pending) {
      return false;
    }
    this.pending.delete(response.id);
    if (response.error) {
      pending.reject(new HostRPCError(response.error));
    } else {
      pending.resolve(response.result);
    }
    return true;
  }
}

export async function runExtension(entrypoint: ExtensionEntrypoint | { default: ExtensionEntrypoint }): Promise<void> {
  const resolvedEntrypoint = typeof entrypoint === "function" ? entrypoint : entrypoint.default;
  const host = await createExtensionHost(resolvedEntrypoint);
  runStdioServer(host);
}

/** The same extension dispatcher used by stdio and session-scoped ACP relays. */
export class ExtensionRuntime {
  private readonly hostClient: RuntimeHostRPCClient;
  private readonly activeRequests = new Map<number | string, ActiveRequest>();
  private readonly handleSessionEnd: SessionEndHandler;
  private closed = false;
  private closePromise?: Promise<void>;

  constructor(private readonly host: ExtensionHost, transport: ExtensionRuntimeTransport) {
    this.hostClient = new RuntimeHostRPCClient(transport, (error) => { void this.close(error); });
    this.handleSessionEnd = createSessionEndHandler(host);
  }

  receive(message: ExtensionRPCMessage): void {
    if (this.closed) throw new Error("Extension runtime is closed");
    handleMessage(this.host, this.hostClient, this.activeRequests, this.handleSessionEnd, message);
  }

  close(error = new Error("Extension host disconnected")): Promise<void> {
    if (this.closePromise) return this.closePromise;
    this.closed = true;
    this.hostClient.close(error);
    for (const request of this.activeRequests.values()) {
      request.cancel(error);
      this.hostClient.finishRequest(request, error);
    }
    this.activeRequests.clear();
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    const timeout = new Promise<void>((resolve) => {
      timer = setTimeout(() => {
        controller.abort(new Error("Extension shutdown timed out after host disconnect"));
        resolve();
      }, hostDisconnectShutdownTimeoutMs);
    });
    const cleanup = runWithHostRPCClient(this.hostClient, () => this.handleSessionEnd(
      { id: "host-disconnected", event: "session.end", context: {} }, controller.signal,
    )).then(() => undefined, () => undefined);
    this.closePromise = Promise.race([cleanup, timeout]).finally(() => {
      clearTimeout(timer);
      controller.abort(error);
    });
    return this.closePromise;
  }
}

function runStdioServer(host: ExtensionHost): void {
  let buffer: Buffer<ArrayBufferLike> = Buffer.alloc(0);
  const runtime = new ExtensionRuntime(host, { send: writeMessageAsync });
  const shutdownAfterHostDisconnect = (): void => {
    void runtime.close().finally(() => nodeProcess.exit(0));
  };

  nodeProcess.stdin.on("data", (chunk: Buffer) => {
    buffer = Buffer.concat([buffer, chunk]);
    while (true) {
      const frame = tryReadFrame(buffer);
      if (!frame) {
        break;
      }
      buffer = frame.remaining;
      let message: ExtensionRPCMessage;
      try {
        message = JSON.parse(frame.payload.toString("utf8")) as ExtensionRPCMessage;
      } catch (error) {
        void writeMessageAsync({ jsonrpc: "2.0", id: null, error: { code: -32700, message: errorMessage(error) } });
        continue;
      }
      runtime.receive(message);
    }
  });
  nodeProcess.stdin.once("end", shutdownAfterHostDisconnect);
  nodeProcess.stdin.once("close", shutdownAfterHostDisconnect);
  nodeProcess.stdin.once("error", shutdownAfterHostDisconnect);
  nodeProcess.stdin.resume();
}

function createSessionEndHandler(host: ExtensionHost): SessionEndHandler {
  let execution: Promise<unknown> | undefined;
  return (params, signal) => {
    execution ??= host.handleEvent(params, signal);
    return execution;
  };
}

function handleMessage(
  host: ExtensionHost,
  hostClient: RuntimeHostRPCClient,
  activeRequests: Map<number | string, ActiveRequest>,
  handleSessionEnd: SessionEndHandler,
  message: ExtensionRPCMessage,
): void {
  const request = message as JsonRpcRequest;
  if (!request.method) {
    // Duplex IDs are independent: even unmatched responses are not requests.
    hostClient.handleResponse(request as JsonRpcResponse);
    return;
  }

  if (request.method === "$/cancelRequest" && (request.id === undefined || request.id === null)) {
    const params = request.params;
    if (isRecord(params) && (typeof params.id === "number" || typeof params.id === "string")) {
      const error = new Error("Extension request cancelled");
      const request = activeRequests.get(params.id);
      if (request) {
        request.cancel(error);
        hostClient.finishRequest(request, error);
      }
    }
    return;
  }

  if (request.id === undefined || request.id === null) {
    hostClient.handleNotification(request.method, request.params);
    return;
  }

  startRequest(host, hostClient, activeRequests, handleSessionEnd, request);
}

function startRequest(
  host: ExtensionHost,
  hostClient: RuntimeHostRPCClient,
  activeRequests: Map<number | string, ActiveRequest>,
  handleSessionEnd: SessionEndHandler,
  request: JsonRpcRequest,
): void {
  const requestId = request.id as number | string;
  const previous = activeRequests.get(requestId);
  if (previous) {
    const reusedError = new Error("Extension request id was reused");
    previous.cancel(reusedError);
    hostClient.finishRequest(previous, reusedError);
  }
  const active = new ActiveRequest(requestId);
  activeRequests.set(requestId, active);
  const requestClient: HostRPCClient = {
    request: (method, params) => hostClient.requestFor(active, method, params),
    requestPersistent: async (method, params) => {
      if (active.active) {
        return await hostClient.requestFor(active, method, params);
      }
      return await hostClient.request(method, params);
    },
    persistent: hostClient,
  };

  const execution = runWithHostRPCClient(requestClient, () => dispatch(host, request, active.controller.signal, handleSessionEnd));
  void Promise.race([execution, active.cancelled])
    .then((result) => {
      const shouldRespond = active.active;
      hostClient.finishRequest(active);
      if (shouldRespond) {
        hostClient.send({ jsonrpc: "2.0", id: requestId, result: result ?? null });
      }
    })
    .catch((error) => {
      const shouldRespond = active.active;
      hostClient.finishRequest(active);
      if (shouldRespond) {
        hostClient.send({ jsonrpc: "2.0", id: requestId, error: { code: error instanceof HostRPCError ? error.code : -32000, message: errorMessage(error) } });
      }
    })
    .finally(() => {
      hostClient.finishRequest(active);
      if (activeRequests.get(requestId) === active) {
        activeRequests.delete(requestId);
      }
    });
}

async function dispatch(
  host: ExtensionHost,
  request: JsonRpcRequest,
  signal: AbortSignal,
  handleSessionEnd: SessionEndHandler,
): Promise<unknown> {
  switch (request.method) {
    case "extension.initialize":
      return host.initialize(request.params as never);
    case "extension.tool.execute":
      return await host.executeTool(request.params as never, signal);
    case "extension.command.execute":
      return await host.executeCommand(request.params as never, signal);
    case "extension.shortcut.execute":
      return (await host.executeShortcut(request.params as never, signal)) ?? null;
    case "extension.event.handle": {
      if (isRecord(request.params) && request.params.event === "session.end") {
        return await handleSessionEnd(request.params as unknown as HandleEventParams<"session.end">, signal);
      }
      return await host.handleEvent(request.params as never, signal);
    }
    default:
      throw new HostRPCError({ code: -32601, message: `Unknown JSON-RPC method: ${request.method}` });
  }
}

function tryReadFrame(buffer: Buffer): { payload: Buffer; remaining: Buffer } | undefined {
  const headerEnd = buffer.indexOf("\r\n\r\n");
  const fallbackHeaderEnd = headerEnd === -1 ? buffer.indexOf("\n\n") : -1;
  const separatorIndex = headerEnd === -1 ? fallbackHeaderEnd : headerEnd;
  if (separatorIndex === -1) {
    return undefined;
  }

  const separatorLength = headerEnd === -1 ? 2 : 4;
  const header = buffer.subarray(0, separatorIndex).toString("ascii");
  const contentLength = parseContentLength(header);
  const payloadStart = separatorIndex + separatorLength;
  const payloadEnd = payloadStart + contentLength;
  if (buffer.length < payloadEnd) {
    return undefined;
  }
  return {
    payload: buffer.subarray(payloadStart, payloadEnd),
    remaining: buffer.subarray(payloadEnd),
  };
}

function parseContentLength(header: string): number {
  for (const line of header.split(/\r?\n/)) {
    const [key, value] = line.split(":", 2);
    if (key?.trim().toLowerCase() === "content-length") {
      const parsed = Number.parseInt(value?.trim() ?? "", 10);
      if (Number.isFinite(parsed) && parsed >= 0) {
        return parsed;
      }
    }
  }
  throw new Error("Missing Content-Length header");
}

function writeMessageAsync(message: JsonRpcRequest | JsonRpcResponse): Promise<void> {
  const payload = JSON.stringify(message);
  const frame = `Content-Length: ${Buffer.byteLength(payload, "utf8")}\r\n\r\n${payload}`;
  return new Promise((resolve, reject) => {
    nodeProcess.stdout.write(frame, (error) => {
      if (error) {
        reject(error);
      } else {
        resolve();
      }
    });
  });
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function asError(error: unknown): Error {
  return error instanceof Error ? error : new Error(String(error));
}
