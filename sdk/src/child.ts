import { randomUUID } from "node:crypto";
import { setTimeout as delay } from "node:timers/promises";
import type { HostRPCClient } from "./context.js";
import { executionOptionsSchema, type ExecutionOptions } from "./execution.js";
import type { BackgroundTaskLease } from "./types.js";

export interface ChildRequest {
  profile: string;
  message: string;
  /** Stable admission identity. Reusing it with different input is rejected. */
  requestId?: string;
  options?: ExecutionOptions;
  systemPrompt?: string;
  cwd?: string;
  /** Acquire in the parent tool and keep until all retained work has finished. */
  lease?: BackgroundTaskLease;
}

export interface ChildEvent {
  sequence: number;
  kind: string;
  text?: string;
  toolName?: string;
  toolCallId?: string;
}

export interface ChildResult {
  conversationId: string;
  runId: string;
  parentConversationId: string;
  parentRunId: string;
  extensionId: string;
  profile: string;
  done: boolean;
  cancelled?: boolean;
  error?: string;
  output?: string;
  events?: ChildEvent[];
}

export interface ChildClient {
  start(request: ChildRequest): Promise<ChildExecution>;
}

/** A separately persisted daemon execution, not an ACP subprocess. */
export class ChildExecution {
  readonly conversationId: string;
  readonly runId: string;
  private after = 0;
  constructor(private result: ChildResult, private call: (method: string, params: unknown) => Promise<unknown>, private leaseId?: string) {
    this.conversationId = result.conversationId;
    this.runId = result.runId;
  }
  async read(): Promise<ChildResult> {
    this.result = parseResult(await this.call("kodelet.child.read", { childId: this.conversationId, leaseId: this.leaseId, after: this.after }));
    return this.result;
  }
  async cancel(): Promise<void> {
    await this.call("kodelet.child.cancel", { childId: this.conversationId, leaseId: this.leaseId });
  }
  async wait(options: { signal?: AbortSignal; onEvent?: (event: ChildEvent) => void | Promise<void> } = {}): Promise<ChildResult> {
    for (;;) {
      if (options.signal?.aborted) {
        await this.cancel();
        throw options.signal.reason ?? new Error("Child wait canceled");
      }
      for (const event of this.result.events ?? []) {
        if (event.sequence > this.after) {
          await options.onEvent?.(event);
          this.after = event.sequence;
        }
      }
      if (this.result.done) {
        if (this.result.error || this.result.cancelled) throw new Error(this.result.error ?? "Child canceled");
        return this.result;
      }
      await delay(50);
      await this.read();
    }
  }
}

function parseResult(value: unknown): ChildResult {
  const result = value as ChildResult | undefined;
  if (!result || typeof result.conversationId !== "string" || !result.conversationId || typeof result.runId !== "string" || !result.runId || typeof result.done !== "boolean") {
    throw new Error("Invalid central child execution response");
  }
  return result;
}

export function createChildClient(client: HostRPCClient | undefined): ChildClient {
  const retained = new Set<string>();
  return {
    async start(request) {
      if (!client) throw new Error("Central child execution requires an authenticated runner host; no local fallback is available");
      if (request.lease && !request.lease.id) throw new Error("Child execution requires a real runner background lease");
      const { lease, ...input } = request;
      const leaseId = lease?.id;
      const persistent = (method: string, params: unknown) => {
        if (client.requestPersistent) return client.requestPersistent(method, params);
        if (client.persistent) return client.persistent.request(method, params);
        throw new Error("Retained child execution requires persistent host RPC");
      };
      const active = (method: string, params: unknown) => client.request(method, params);
      const payload = { ...input, requestId: input.requestId ?? randomUUID(), leaseId,
        ...(input.options === undefined ? {} : { options: executionOptionsSchema.parse(input.options) }) };
      // First admission binds a live tool to the retained lease. Later work may
      // use that same explicit, bounded lease without extending its expiry.
      const result = parseResult(await (leaseId && retained.has(leaseId) ? persistent : active)("kodelet.child.start", payload));
      if (leaseId) retained.add(leaseId);
      return new ChildExecution(result, leaseId ? persistent : active, leaseId);
    },
  };
}
