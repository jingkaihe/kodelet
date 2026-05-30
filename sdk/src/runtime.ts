import nodeProcess from "node:process";

import { createExtensionHost, type ExtensionHost } from "./api.js";
import type { ExtensionEntrypoint } from "./types.js";

interface JsonRpcRequest {
  jsonrpc: "2.0";
  id?: number | string | null;
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

export async function runExtension(entrypoint: ExtensionEntrypoint | { default: ExtensionEntrypoint }): Promise<void> {
  const resolvedEntrypoint = typeof entrypoint === "function" ? entrypoint : entrypoint.default;
  const host = await createExtensionHost(resolvedEntrypoint);
  runStdioServer(host);
}

function runStdioServer(host: ExtensionHost): void {
  let buffer: Buffer<ArrayBufferLike> = Buffer.alloc(0);

  nodeProcess.stdin.on("data", (chunk: Buffer) => {
    buffer = Buffer.concat([buffer, chunk]);
    while (true) {
      const frame = tryReadFrame(buffer);
      if (!frame) {
        break;
      }
      buffer = frame.remaining;
      void handleRequest(host, frame.payload);
    }
  });
  nodeProcess.stdin.resume();
}

async function handleRequest(host: ExtensionHost, payload: Buffer): Promise<void> {
  let request: JsonRpcRequest;
  try {
    request = JSON.parse(payload.toString("utf8")) as JsonRpcRequest;
  } catch (error) {
    writeResponse({ jsonrpc: "2.0", id: null, error: { code: -32700, message: errorMessage(error) } });
    return;
  }

  if (request.id === undefined || request.id === null) {
    return;
  }

  try {
    const result = await dispatch(host, request);
    writeResponse({ jsonrpc: "2.0", id: request.id, result });
  } catch (error) {
    writeResponse({ jsonrpc: "2.0", id: request.id, error: { code: -32000, message: errorMessage(error) } });
  }
}

async function dispatch(host: ExtensionHost, request: JsonRpcRequest): Promise<unknown> {
  switch (request.method) {
    case "extension.initialize":
      return host.initialize(request.params as never);
    case "extension.tool.execute":
      return await host.executeTool(request.params as never);
    case "extension.command.execute":
      return await host.executeCommand(request.params as never);
    case "extension.event.handle":
      return await host.handleEvent(request.params as never);
    case "$/cancelRequest":
      return undefined;
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
  const payload = JSON.stringify(response);
  nodeProcess.stdout.write(`Content-Length: ${Buffer.byteLength(payload, "utf8")}\r\n\r\n${payload}`);
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
