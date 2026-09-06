import { randomUUID } from "node:crypto";
import { setTimeout as delay } from "node:timers/promises";
import { z } from "zod";
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
  /** Fork the active parent's history, or start fresh (the default). */
  contextMode?: "fresh" | "fork";
  /** Continue an owned child conversation with a new run and request ID. */
  resume?: string;
  /** Acquire in the parent tool and keep until all retained work has finished. */
  lease?: BackgroundTaskLease;
}

export interface ChildEvent {
  sequence: number;
  kind: string;
  text?: string;
  toolName?: string;
  toolCallId?: string;
  /** Raw tool input JSON; bounded host progress may truncate it. */
  input?: string;
  toolOutput?: string;
  /** Final tool-result status; absence means unknown, not success. */
  success?: boolean;
  error?: string;
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

export interface ChildSteerResult {
  outcome: "injected" | "promptRequired";
  reason?: "noRunningTurn";
}

const identitySchema = z.string().min(1).max(128).refine((value) => value.trim() !== "" && !value.includes("\0"), "Identity must be nonempty and contain no null bytes");
const messageSchema = z.string().min(1).max(512 * 1024).refine((value) => value.trim() !== "", "Message must be nonempty");
const requestSchema = z.strictObject({
  profile: identitySchema,
  message: messageSchema,
  requestId: identitySchema.optional(),
  options: executionOptionsSchema.optional(),
  systemPrompt: z.string().max(256 * 1024).optional(),
  cwd: z.string().min(1).max(8192).refine((value) => value.trim() !== "" && !value.includes("\0")).optional(),
  contextMode: z.enum(["fresh", "fork"]).optional(),
  resume: identitySchema.optional(),
}).refine((value) => !(value.resume && value.contextMode === "fork"), "Cannot combine resume with fork context");

/** A separately persisted daemon execution, not an ACP subprocess. */
export class ChildExecution {
  readonly conversationId: string;
  readonly runId: string;
  private after = 0;
  constructor(private result: ChildResult, private call: (method: string, params: unknown) => Promise<unknown>, private leaseId?: string) {
    parseResult(result);
    this.conversationId = result.conversationId;
    this.runId = result.runId;
  }
  private params() {
    return { childId: this.conversationId, childRunId: this.runId, ...(this.leaseId === undefined ? {} : { leaseId: this.leaseId }) };
  }
  async read(): Promise<ChildResult> {
    const result = parseResult(await this.call("kodelet.child.read", { ...this.params(), after: this.after }));
    if (result.conversationId !== this.conversationId || result.runId !== this.runId) throw new Error("Child response does not match this execution");
    this.result = result;
    return this.result;
  }
  async cancel(): Promise<void> {
    await this.call("kodelet.child.cancel", this.params());
  }
  /** Queue guidance for this exact run; never start another turn automatically. */
  async steer(message: string, options: { requestId?: string } = {}): Promise<ChildSteerResult> {
    messageSchema.parse(message);
    const { requestId } = z.strictObject({ requestId: identitySchema.optional() }).parse(options);
    const result = await this.call("kodelet.child.steer", { ...this.params(), message, requestId: requestId ?? randomUUID() });
    return z.strictObject({ outcome: z.enum(["injected", "promptRequired"]), reason: z.literal("noRunningTurn").optional() }).parse(result);
  }
  async wait(options: { signal?: AbortSignal; onEvent?: (event: ChildEvent) => void | Promise<void> } = {}): Promise<ChildResult> {
    const { signal } = options;
    let onAbort: (() => void) | undefined;
    try {
      signal?.throwIfAborted();
      if (!signal) return await this.waitForResult(options);
      const aborted = new Promise<never>((_resolve, reject) => {
        onAbort = () => reject(signal.reason ?? new Error("Child wait canceled"));
        signal.addEventListener("abort", onAbort, { once: true });
      });
      // The host RPC client cannot abort an in-flight read. Race the whole poll
      // loop so cancellation still reaches the exact child while that read waits.
      return await Promise.race([this.waitForResult(options), aborted]);
    } catch (error) {
      if (signal?.aborted) {
        await this.cancel();
        throw signal.reason ?? error;
      }
      throw error;
    } finally {
      if (onAbort) signal?.removeEventListener("abort", onAbort);
    }
  }
  private async waitForResult(options: { signal?: AbortSignal; onEvent?: (event: ChildEvent) => void | Promise<void> }): Promise<ChildResult> {
    for (;;) {
      options.signal?.throwIfAborted();
      for (const event of this.result.events ?? []) {
        if (event.sequence > this.after) {
          await options.onEvent?.(event);
          options.signal?.throwIfAborted();
          this.after = event.sequence;
        }
      }
      if (this.result.done) {
        if (this.result.error || this.result.cancelled) throw new Error(this.result.error ?? "Child canceled");
        return this.result;
      }
      await delay(50, undefined, { signal: options.signal });
      await this.read();
    }
  }
}

function parseResult(value: unknown): ChildResult {
  const result = value as ChildResult | undefined;
  if (!result || !identitySchema.safeParse(result.conversationId).success || !identitySchema.safeParse(result.runId).success || typeof result.done !== "boolean") {
    throw new Error("Invalid response from the delegated task");
  }
  return result;
}

export function createChildClient(client: HostRPCClient | undefined): ChildClient {
  const retained = new Set<string>();
  return {
    async start(request) {
      if (!client) throw new Error("Delegated tasks require an authenticated runner connection");
      const { lease, ...input } = request;
      if (lease !== undefined && (!lease || !identitySchema.safeParse(lease.id).success)) throw new Error("Delegated tasks require a valid runner background task lease");
      const leaseId = lease?.id;
      const persistent = (method: string, params: unknown) => {
        // Unlike UI requests, retained child RPC must outlive a handler even
        // when it starts before that handler has returned.
        if (client.persistent) return client.persistent.request(method, params);
        if (client.requestPersistent) return client.requestPersistent(method, params);
        throw new Error("Continuing a background child task requires a persistent runner connection");
      };
      const active = (method: string, params: unknown) => client.request(method, params);
      const validated = requestSchema.parse(input);
      const payload = { ...validated, requestId: validated.requestId ?? randomUUID(), ...(leaseId === undefined ? {} : { leaseId }) };
      // First admission binds a live tool to the retained lease. Later work may
      // use that same explicit, bounded lease without extending its expiry.
      // Fork still requires originating-tool authority on every submission.
      const result = parseResult(await (leaseId && retained.has(leaseId) && validated.contextMode !== "fork" ? persistent : active)("kodelet.child.start", payload));
      if (validated.resume !== undefined && result.conversationId !== validated.resume) throw new Error("Child response does not match the resumed conversation");
      if (leaseId) retained.add(leaseId);
      return new ChildExecution(result, leaseId ? persistent : active, leaseId);
    },
  };
}
