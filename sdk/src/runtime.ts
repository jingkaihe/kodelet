import nodeProcess from "node:process";

import { createExtensionHost, type ExtensionHost } from "./api.js";
import { runWithHostRPCClient, setActiveHostRPCClient, type HostRPCClient } from "./context.js";
import type { ExtensionEntrypoint } from "./types.js";

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
  };
}

interface PendingRequest {
  request?: ActiveRequest;
  resolve(value: unknown): void;
  reject(error: Error): void;
}

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

class StdioHostRPCClient implements HostRPCClient {
  private nextId = 0;
  private pending = new Map<number, PendingRequest>();

  request(method: string, params?: unknown): Promise<unknown> {
    return this.requestFor(undefined, method, params);
  }

  requestFor(request: ActiveRequest | undefined, method: string, params?: unknown): Promise<unknown> {
    if (request && !request.active) {
      throw new Error("Extension request is no longer active");
    }
    const id = ++this.nextId;
    writeMessage({ jsonrpc: "2.0", id, parentId: request?.id, method, params });
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject, request });
    });
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
      pending.reject(new Error(response.error.message));
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

function runStdioServer(host: ExtensionHost): void {
  let buffer: Buffer<ArrayBufferLike> = Buffer.alloc(0);
  const hostClient = new StdioHostRPCClient();
  const activeRequests = new Map<number | string, ActiveRequest>();
  setActiveHostRPCClient(hostClient);

  nodeProcess.stdin.on("data", (chunk: Buffer) => {
    buffer = Buffer.concat([buffer, chunk]);
    while (true) {
      const frame = tryReadFrame(buffer);
      if (!frame) {
        break;
      }
      buffer = frame.remaining;
      handleMessage(host, hostClient, activeRequests, frame.payload);
    }
  });
  nodeProcess.stdin.on("end", () => {
    for (const request of activeRequests.values()) {
      const error = new Error("Extension host disconnected");
      request.cancel(error);
      hostClient.finishRequest(request, error);
    }
    activeRequests.clear();
  });
  nodeProcess.stdin.resume();
}

function handleMessage(
  host: ExtensionHost,
  hostClient: StdioHostRPCClient,
  activeRequests: Map<number | string, ActiveRequest>,
  payload: Buffer,
): void {
  let request: JsonRpcRequest;
  try {
    request = JSON.parse(payload.toString("utf8")) as JsonRpcRequest;
  } catch (error) {
    writeResponse({ jsonrpc: "2.0", id: null, error: { code: -32700, message: errorMessage(error) } });
    return;
  }

  if (!request.method && hostClient.handleResponse(request as JsonRpcResponse)) {
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
    return;
  }

  startRequest(host, hostClient, activeRequests, request);
}

function startRequest(
  host: ExtensionHost,
  hostClient: StdioHostRPCClient,
  activeRequests: Map<number | string, ActiveRequest>,
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
  };

  const execution = runWithHostRPCClient(requestClient, () => dispatch(host, request, active.controller.signal));
  void Promise.race([execution, active.cancelled])
    .then((result) => {
      const shouldRespond = active.active;
      hostClient.finishRequest(active);
      if (shouldRespond) {
        writeResponse({ jsonrpc: "2.0", id: requestId, result });
      }
    })
    .catch((error) => {
      const shouldRespond = active.active;
      hostClient.finishRequest(active);
      if (shouldRespond) {
        writeResponse({ jsonrpc: "2.0", id: requestId, error: { code: -32000, message: errorMessage(error) } });
      }
    })
    .finally(() => {
      hostClient.finishRequest(active);
      if (activeRequests.get(requestId) === active) {
        activeRequests.delete(requestId);
      }
    });
}

async function dispatch(host: ExtensionHost, request: JsonRpcRequest, signal: AbortSignal): Promise<unknown> {
  switch (request.method) {
    case "extension.initialize":
      return host.initialize(request.params as never);
    case "extension.tool.execute":
      return await host.executeTool(request.params as never, signal);
    case "extension.command.execute":
      return await host.executeCommand(request.params as never, signal);
    case "extension.event.handle":
      return await host.handleEvent(request.params as never, signal);
    default:
      throw new Error(`Unknown JSON-RPC method: ${request.method}`);
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

function writeResponse(response: JsonRpcResponse): void {
  writeMessage(response);
}

function writeMessage(message: JsonRpcRequest | JsonRpcResponse): void {
  const payload = JSON.stringify(message);
  nodeProcess.stdout.write(`Content-Length: ${Buffer.byteLength(payload, "utf8")}\r\n\r\n${payload}`);
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
