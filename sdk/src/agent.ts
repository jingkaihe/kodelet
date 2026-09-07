import { spawn as spawnProcess, type SpawnOptions } from "node:child_process";
import { EventEmitter } from "node:events";
import { StringDecoder } from "node:string_decoder";

import { executionArgs, executionOptionsSchema, remoteExecutionOptions, type ExecutionOptions } from "./execution.js";
import type {
  ExtensionEntrypoint,
  UIConfirmRequest,
  UIInputRequest,
  UINotifyRequest,
  UISelectRequest,
} from "./types.js";

const ACP_PROTOCOL_VERSION = 1;
const ACP_MESSAGE_LIMIT = 64 * 1024 * 1024;

export type BridgeTransport = "unix" | "tcp";

export type ProfileValue = string | number | boolean | string[] | number[] | boolean[] | ProfileObject | undefined;
export interface ProfileObject {
  [key: string]: ProfileValue;
}

export interface ProfileInput extends ProfileObject {
  name?: string;
  provider?: string;
  /** @deprecated Use provider. Accepted for compatibility with early SDK examples. */
  profiler?: string;
}

export interface AgentUIHandlers {
  input?(request: UIInputRequest, signal?: AbortSignal): Promise<string | undefined> | string | undefined;
  confirm?(request: UIConfirmRequest, signal?: AbortSignal): Promise<boolean> | boolean;
  select?(request: UISelectRequest, signal?: AbortSignal): Promise<string | undefined> | string | undefined;
  notify?(request: UINotifyRequest, signal?: AbortSignal): Promise<void> | void;
}

export interface ClientOptions {
  /** Explicit daemon endpoint and registered runner; never use a local fallback. */
  server?: string;
  runner?: string;
  /** Kodelet executable to launch. Defaults to `kodelet`. */
  command?: string;
  /** Default working directory for sessions. Defaults to process.cwd(). */
  cwd?: string;
  /** Extra environment variables for spawned kodelet processes. */
  env?: NodeJS.ProcessEnv;
  /** Test seam for spawning kodelet. */
  spawn?: SpawnFunction;
}

export interface CreateSessionOptions {
  options?: ExecutionOptions;
  environmentProfile?: string;
  /** Named profile, inline profile, or omitted to use the default Kodelet config. */
  profile?: string | Profile | ProfileInput;
  /** @deprecated Unsupported remotely. Install extensions on the selected runner. */
  extensions?: ExtensionEntrypoint[];
  /** Kept for API compatibility. ACP emits chunks as JSON-RPC session/update notifications. */
  streaming?: boolean;
  /** Working directory for the agent. Defaults to the client cwd or process.cwd(). */
  cwd?: string;
  /** Existing Kodelet conversation ID to resume. */
  resume?: string;
  /** Maximum agentic turns for each run. 0/undefined means Kodelet default. */
  maxTurns?: number;
  /** @deprecated Unsupported by this ACP adapter. Retained for API compatibility. */
  ui?: AgentUIHandlers;
  /** @deprecated Unsupported remotely. Retained for API compatibility; any supplied value is rejected. */
  extensionTransport?: BridgeTransport;
}

export interface RunOptions {
  message: string;
  images?: string[];
  /** ACP configures max turns when the session process starts; per-run overrides are not supported. */
  maxTurns?: number;
  signal?: AbortSignal;
}

export interface AgentResponse {
  content: string;
  conversationId?: string;
  /** Run event log; transient tool.update snapshots are coalesced by toolCallId. */
  events: AgentStreamEvent[];
  exitCode: number;
  stopReason?: string;
}

export type SessionSteeringOutcome = "injected" | "startedNewTurn" | "promptRequired" | "failed";

export interface SessionSteerResult {
  outcome: SessionSteeringOutcome;
  reason?: string;
}

export interface AgentStreamEvent<T = unknown> {
  type: string;
  data: T;
  conversationId?: string;
  raw?: unknown;
}

export interface AssistantMessageDeltaData {
  deltaContent: string;
}

export interface AssistantMessageData {
  content: string;
}

export interface AssistantThinkingDeltaData {
  deltaContent: string;
}

export interface ToolCallData {
  toolName: string;
  input: unknown;
  rawInput?: string;
  toolCallId?: string;
}

export interface ToolResultData {
  toolName: string;
  result: string;
  toolCallId?: string;
  status?: string;
}

export type ToolUpdateData = ToolResultData;

export interface SessionEventMap {
  "agent.start": AgentStreamEvent<{ message: string }>;
  "agent.end": AgentStreamEvent<AgentResponse>;
  "assistant.message_delta": AgentStreamEvent<AssistantMessageDeltaData>;
  "assistant.message": AgentStreamEvent<AssistantMessageData>;
  "assistant.thinking_start": AgentStreamEvent<Record<string, never>>;
  "assistant.thinking_delta": AgentStreamEvent<AssistantThinkingDeltaData>;
  "assistant.thinking_end": AgentStreamEvent<Record<string, never>>;
  "assistant.content_end": AgentStreamEvent<Record<string, never>>;
  "user.message": AgentStreamEvent<{ content: string }>;
  "tool.call": AgentStreamEvent<ToolCallData>;
  "tool.update": AgentStreamEvent<ToolUpdateData>;
  "tool.result": AgentStreamEvent<ToolResultData>;
  "agent.output": AgentStreamEvent<{ line: string }>;
  "agent.error": AgentStreamEvent<{ message: string }>;
  event: AgentStreamEvent;
}

export interface SpawnedProcess extends EventEmitter {
  stdin?: NodeJS.WritableStream | null;
  stdout?: NodeJS.ReadableStream | null;
  stderr?: NodeJS.ReadableStream | null;
  kill(signal?: NodeJS.Signals | number): boolean;
}

export type SpawnFunction = (command: string, args: string[], options: SpawnOptions) => SpawnedProcess;

interface ResolvedProfile {
  args: string[];
  config?: ProfileInput;
}

interface JsonRPCMessage {
  jsonrpc?: "2.0";
  id?: number | string | null;
  method?: string;
  params?: unknown;
  result?: unknown;
  error?: { code: number; message: string; data?: unknown };
}

interface PendingRPCRequest {
  resolve(value: unknown): void;
  reject(error: Error): void;
}

interface ACPContentBlock {
  type: string;
  text?: string;
  data?: string;
  mimeType?: string;
  uri?: string;
  name?: string;
  resource?: {
    uri: string;
    mimeType?: string;
    text?: string;
    blob?: string;
  };
  _meta?: Record<string, unknown>;
}

interface ACPToolCallUpdate {
  sessionUpdate?: string;
  toolCallId?: string;
  toolName?: string;
  title?: string;
  kind?: string;
  status?: string;
  rawInput?: unknown;
  content?: unknown[];
}

export class Profile {
  readonly name?: string;
  readonly config: ProfileInput;

  constructor(config: string | ProfileInput) {
    if (typeof config === "string") {
      this.name = config;
      this.config = { name: config };
      return;
    }

    const normalized = { ...config };
    if (normalized.provider === undefined && typeof normalized.profiler === "string") {
      normalized.provider = normalized.profiler;
    }
    delete normalized.profiler;

    this.name = typeof normalized.name === "string" ? normalized.name : undefined;
    this.config = normalized;
  }

  static named(name: string): Profile {
    return new Profile(name);
  }

  isNamedOnly(): boolean {
    return Object.keys(this.config).every((key) => key === "name");
  }

  toLaunchConfig(): ResolvedProfile {
    if (this.name && this.isNamedOnly()) {
      return { args: ["--profile", this.name] };
    }

    return {
      args: [],
      config: this.config,
    };
  }
}

export class AgentRunError extends Error {
  readonly code: number | null;
  readonly signal: NodeJS.Signals | null;
  readonly stderr: string;

  constructor(message: string, opts: { code: number | null; signal: NodeJS.Signals | null; stderr: string }) {
    super(message);
    this.name = "AgentRunError";
    this.code = opts.code;
    this.signal = opts.signal;
    this.stderr = opts.stderr;
  }
}

class RPCError extends Error {
  readonly code: number;
  readonly data?: unknown;

  constructor(error: { code: number; message: string; data?: unknown }) {
    super(error.message);
    this.name = "RPCError";
    this.code = error.code;
    this.data = error.data;
  }
}

export class Client {
  private readonly command: string;
  private readonly cwd: string;
  private readonly env: NodeJS.ProcessEnv;
  private readonly spawn: SpawnFunction;
  private readonly sessions = new Set<Session>();
  private readonly endpointArgs: string[];

  constructor(options: ClientOptions = {}) {
    this.command = options.command ?? "kodelet";
    this.cwd = options.cwd ?? process.cwd();
    this.endpointArgs = [...(options.server ? ["--server", options.server] : []), ...(options.runner ? ["--runner", options.runner] : [])];
    this.env = options.env ?? {};
    this.spawn = options.spawn ?? ((command, args, spawnOptions) => spawnProcess(command, args, spawnOptions) as SpawnedProcess);
  }

  async createSession(options: CreateSessionOptions = {}): Promise<Session> {
    if (options.extensions?.length || options.extensionTransport !== undefined) {
      throw new Error("Inline executable extensions are not supported by server sessions; install the extension on the runner and use ctx.children for delegated execution");
    }
    if (options.ui !== undefined) throw new Error("Inline extension UI handlers are not supported by this ACP adapter");
    const cwd = options.cwd ?? this.cwd;
    const profile = normalizeProfile(options.profile);
    const inline = profile && !profile.isNamedOnly() ? remoteExecutionOptions(profile.config) : {};
    const overrides = options.options === undefined ? {} : executionOptionsSchema.parse(options.options);
    const execution = executionOptionsSchema.parse({ ...inline, ...overrides, ...(options.maxTurns === undefined ? {} : { maxTurns: options.maxTurns }) });
    let rpc: ACPRPCClient | undefined;

    try {
      const env = cleanEnv(this._baseEnv());
      const args = ["acp", ...this.endpointArgs, ...executionArgs(execution),
        ...(profile?.name && profile.isNamedOnly() ? [`--profile=${profile.name}`] : []),
        ...(options.environmentProfile ? [`--runner-profile=${options.environmentProfile}`] : [])];
      rpc = new ACPRPCClient(this._spawn(args, { cwd: process.cwd(), env, stdio: ["pipe", "pipe", "pipe"] }));
      await rpc.initialize();
      const sessionID = options.resume ? await rpc.loadSession(options.resume, cwd) : await rpc.createSession(cwd);
      const session = new Session(this, {
        ...options,
        cwd,
        profile,
        sessionID,
        rpc,
      });
      this.sessions.add(session);
      return session;
    } catch (error) {
      await rpc?.close();
      throw error;
    }
  }

  async close(): Promise<void> {
    await Promise.all([...this.sessions].map((session) => session.close()));
    this.sessions.clear();
  }

  _spawn(args: string[], options: SpawnOptions): SpawnedProcess {
    return this.spawn(this.command, args, options);
  }

  _baseEnv(): NodeJS.ProcessEnv {
    return { ...process.env, ...this.env };
  }

  _deleteSession(session: Session): void {
    this.sessions.delete(session);
  }
}

interface SessionInternalOptions extends CreateSessionOptions {
  cwd: string;
  profile?: Profile;
  sessionID: string;
  rpc: ACPRPCClient;
}

export class Session extends EventEmitter {
  readonly cwd: string;
  private readonly client: Client;
  private readonly rpc: ACPRPCClient;
  private readonly maxTurns?: number;
  private conversationId: string;
  private closed = false;
  private closePromise?: Promise<void>;
  private running = false;

  constructor(client: Client, options: SessionInternalOptions) {
    super();
    this.client = client;
    this.cwd = options.cwd;
    this.rpc = options.rpc;
    this.maxTurns = options.maxTurns;
    this.conversationId = options.sessionID;
  }

  get id(): string {
    return this.conversationId;
  }

  on<Name extends keyof SessionEventMap>(eventName: Name, listener: (event: SessionEventMap[Name]) => void): this;
  on(eventName: string | symbol, listener: (...args: any[]) => void): this {
    return super.on(eventName, listener);
  }

  once<Name extends keyof SessionEventMap>(eventName: Name, listener: (event: SessionEventMap[Name]) => void): this;
  once(eventName: string | symbol, listener: (...args: any[]) => void): this {
    return super.once(eventName, listener);
  }

  off<Name extends keyof SessionEventMap>(eventName: Name, listener: (event: SessionEventMap[Name]) => void): this;
  off(eventName: string | symbol, listener: (...args: any[]) => void): this {
    return super.off(eventName, listener);
  }

  async runAndWait(options: RunOptions): Promise<AgentResponse> {
    if (this.closed) {
      throw new Error("Cannot run a closed Kodelet session");
    }
    if (this.running) {
      throw new Error("Cannot run a Kodelet session while another run is in progress");
    }
    if (options.maxTurns !== undefined && options.maxTurns !== this.maxTurns) {
      throw new Error("Per-run maxTurns is not supported by the RPC transport; set maxTurns in createSession instead");
    }
    throwIfAlreadyAborted(options.signal);

    this.running = true;
    const events: AgentStreamEvent[] = [];
    const assistantChunks: string[] = [];
    const thinkingActive = { value: false };
    const toolNames = new Map<string, string>();
    const unsubscribe = this.rpc.onNotification((method, params) => {
      if (method !== "session/update") {
        return;
      }
      this.handleSessionUpdate(params, events, assistantChunks, thinkingActive, toolNames);
    });
    const abort = () => this.rpc.cancelSession(this.conversationId);
    options.signal?.addEventListener("abort", abort, { once: true });

    this.emitSDKEvent("agent.start", { message: options.message }, events);
    this.emitSDKEvent("user.message", { content: options.message }, events);

    try {
      const result = await this.rpc.prompt(this.conversationId, buildPromptBlocks(options));
      if (thinkingActive.value) {
        thinkingActive.value = false;
        this.emitSDKEvent("assistant.thinking_end", {}, events);
      }
      this.emitSDKEvent("assistant.content_end", {}, events);
      const content = assistantChunks.join("");
      if (content !== "") {
        this.emitSDKEvent("assistant.message", { content }, events);
      }
      const response: AgentResponse = {
        content,
        conversationId: this.conversationId,
        events,
        exitCode: 0,
        stopReason: result.stopReason,
      };
      this.emitSDKEvent("agent.end", { ...response, events: [...events] }, events);
      return response;
    } catch (error) {
      this.emitSDKEvent("agent.error", { message: errorMessage(error) }, events);
      throw error;
    } finally {
      options.signal?.removeEventListener("abort", abort);
      unsubscribe();
      this.running = false;
    }
  }

  async steer(message: string): Promise<SessionSteerResult> {
    if (this.closed) {
      throw new Error("Cannot steer a closed Kodelet session");
    }
    if (!this.running) {
      throw new Error("Cannot steer a Kodelet session without an active run");
    }
    const normalized = message.trim();
    if (normalized === "") {
      throw new Error("Steering message must be a non-empty string");
    }
    return await this.rpc.steerSession(this.conversationId, normalized);
  }

  async close(): Promise<void> {
    this.closed = true;
    this.closePromise ??= (async () => {
      await this.rpc.close();
      this.client._deleteSession(this);
    })().catch((error) => {
      this.closePromise = undefined;
      throw error;
    });
    await this.closePromise;
  }

  private handleSessionUpdate(
    params: unknown,
    events: AgentStreamEvent[],
    assistantChunks: string[],
    thinkingActive: { value: boolean },
    toolNames: Map<string, string>,
  ): void {
    if (!isRecord(params)) {
      return;
    }
    const sessionId = stringField(params, "sessionId");
    if (sessionId !== this.conversationId) {
      return;
    }
    const update = params.update;
    if (!isRecord(update)) {
      return;
    }

    switch (stringField(update, "sessionUpdate")) {
      case "agent_message_chunk": {
        const content = textFromACPContent(update.content);
        if (content !== "") {
          assistantChunks.push(content);
          this.emitSDKEvent("assistant.message_delta", { deltaContent: content }, events, update);
        }
        break;
      }
      case "agent_thought_chunk": {
        if (!thinkingActive.value) {
          thinkingActive.value = true;
          this.emitSDKEvent("assistant.thinking_start", {}, events, update);
        }
        const content = textFromACPContent(update.content);
        if (content !== "") {
          this.emitSDKEvent("assistant.thinking_delta", { deltaContent: content }, events, update);
        }
        break;
      }
      case "tool_call": {
        const tool = update as ACPToolCallUpdate;
        const toolCallId = tool.toolCallId;
        const toolName = toolNameFromUpdate(tool);
        if (toolCallId && toolName) {
          toolNames.set(toolCallId, toolName);
        }
        this.emitSDKEvent(
          "tool.call",
          {
            toolName,
            input: tool.rawInput,
            rawInput: typeof tool.rawInput === "string" ? tool.rawInput : JSON.stringify(tool.rawInput ?? null),
            toolCallId,
          },
          events,
          update,
        );
        break;
      }
      case "tool_call_update": {
        const tool = update as ACPToolCallUpdate;
        const toolCallId = tool.toolCallId;
        const data = {
          toolName: (toolCallId && toolNames.get(toolCallId)) || toolNameFromUpdate(tool),
          result: toolContentToText(tool.content),
          toolCallId,
          status: tool.status,
        };
        if (tool.status === "in_progress" && tool.content !== undefined) {
          this.emitToolUpdateEvent(data, events, update);
          break;
        }
        if (tool.status === "in_progress") {
          break;
        }
        if (tool.status !== "completed" && tool.status !== "failed") {
          this.emitSDKEvent("event", update, events, update);
          break;
        }
        this.emitSDKEvent(
          "tool.result",
          data,
          events,
          update,
        );
        break;
      }
      default:
        this.emitSDKEvent("event", update, events, update);
    }
  }

  private emitSDKEvent<T>(type: string, data: T, events: AgentStreamEvent[], raw?: unknown): AgentStreamEvent<T> {
    const event: AgentStreamEvent<T> = { type, data, conversationId: this.conversationId, raw };
    events.push(event);
    this.emit(type, event);
    if (type !== "event") {
      this.emit("event", event);
    }
    return event;
  }

  private emitToolUpdateEvent(data: ToolUpdateData, events: AgentStreamEvent[], raw?: unknown): AgentStreamEvent<ToolUpdateData> {
    const event: AgentStreamEvent<ToolUpdateData> = { type: "tool.update", data, conversationId: this.conversationId, raw };
    const existingIndex = data.toolCallId === undefined ? -1 : events.findIndex((candidate) => {
      if (candidate.type !== "tool.update" || !isRecord(candidate.data)) {
        return false;
      }
      return candidate.data.toolCallId === data.toolCallId;
    });
    if (existingIndex >= 0) {
      events.splice(existingIndex, 1);
    }
    events.push(event);
    this.emit("tool.update", event);
    this.emit("event", event);
    return event;
  }
}

class ACPRPCClient {
  private nextId = 0;
  private pending = new Map<number, PendingRPCRequest>();
  private readonly stdoutBuffer = new LineBuffer();
  private readonly stderrChunks: string[] = [];
  private readonly notificationHandlers = new Set<(method: string, params: unknown) => void>();
  private closed = false;
  private steeringSupported = false;
  private exited = false;
  private readonly processClosed: Promise<void>;
  private closePromise?: Promise<void>;

  constructor(private readonly child: SpawnedProcess) {
    if (!child.stdin) {
      throw new Error("kodelet acp process did not expose stdin");
    }
    this.processClosed = new Promise((resolve) => {
      child.once("close", () => {
        this.exited = true;
        resolve();
      });
    });
    child.stdout?.on("data", (chunk: Buffer | string) => {
      if (this.closed) {
        return;
      }
      try {
        this.stdoutBuffer.push(chunk);
        for (const line of this.stdoutBuffer.drainLines()) {
          this.handleLine(line);
          if (this.closed) {
            break;
          }
        }
      } catch (error) {
        this.failTransport(error instanceof Error ? error : new Error(String(error)));
      }
    });
    child.stderr?.on("data", (chunk: Buffer | string) => {
      this.stderrChunks.push(String(chunk));
    });
    for (const [name, stream] of [["stdin", child.stdin], ["stdout", child.stdout], ["stderr", child.stderr]] as const) {
      stream?.on("error", (error: Error) => this.failTransport(new Error(`ACP ${name} failed: ${error.message}`, { cause: error })));
    }
    child.stdout?.once("end", () => this.failTransport(new Error("ACP stdout ended before the client closed the session")));
    child.stdout?.once("close", () => this.failTransport(new Error("ACP stdout closed before the client closed the session")));
    child.once("error", (error: Error) => this.failTransport(error));
    child.once("close", (code: number | null, signal: NodeJS.Signals | null) => {
      this.closed = true;
      const message = this.stderrChunks.join("").trim() || `kodelet acp exited with status ${code ?? "unknown"}${signal ? ` (${signal})` : ""}`;
      this.rejectPending(new AgentRunError(message, { code, signal, stderr: this.stderrChunks.join("") }));
    });
  }

  async initialize(): Promise<void> {
    const result = await this.request("initialize", {
      protocolVersion: ACP_PROTOCOL_VERSION,
      clientCapabilities: {
        terminal: true,
        fs: { readTextFile: false, writeTextFile: false },
      },
      clientInfo: { name: "kodelet-sdk", title: "Kodelet SDK" },
    });
    this.steeringSupported = isRecord(result)
      && isRecord(result._meta)
      && isRecord(result._meta.steering)
      && result._meta.steering.supported === true;
  }

  async createSession(cwd: string): Promise<string> {
    const result = await this.request("session/new", { cwd });
    if (!isRecord(result) || typeof result.sessionId !== "string") {
      throw new Error("Invalid session/new response from kodelet acp");
    }
    return result.sessionId;
  }

  async loadSession(sessionId: string, cwd: string): Promise<string> {
    await this.request("session/load", { sessionId, cwd });
    return sessionId;
  }

  async prompt(sessionId: string, prompt: ACPContentBlock[]): Promise<{ stopReason?: string }> {
    const result = await this.request("session/prompt", { sessionId, prompt });
    if (!isRecord(result)) {
      return {};
    }
    return { stopReason: stringField(result, "stopReason") };
  }

  async steerSession(sessionId: string, message: string): Promise<SessionSteerResult> {
    if (!this.steeringSupported) {
      throw new Error("kodelet acp does not advertise session steering support");
    }
    const result = await this.request("_session/steering", {
      sessionId,
      prompt: [{ type: "text", text: message }],
      _meta: { steering: { idleBehavior: "promptRequired" } },
    });
    if (!isRecord(result) || !isSessionSteeringOutcome(result.outcome)) {
      throw new Error("Invalid _session/steering response from kodelet acp");
    }
    if (result.reason !== undefined && result.reason !== null && typeof result.reason !== "string") {
      throw new Error("Invalid _session/steering response from kodelet acp");
    }
    const reason = stringField(result, "reason");
    if (result.outcome === "promptRequired" && reason !== "noRunningTurn") {
      throw new Error("Invalid _session/steering response from kodelet acp");
    }
    return reason === undefined ? { outcome: result.outcome } : { outcome: result.outcome, reason };
  }

  cancelSession(sessionId: string): void {
    this.notify("session/cancel", { sessionId });
  }

  onNotification(handler: (method: string, params: unknown) => void): () => void {
    this.notificationHandlers.add(handler);
    return () => this.notificationHandlers.delete(handler);
  }

  async close(): Promise<void> {
    this.closed = true;
    this.rejectPending(new Error("kodelet acp process closed"));
    this.closePromise ??= (async () => {
      if (this.exited) {
        return;
      }
      this.child.kill("SIGTERM");
      if (await this.waitForExit()) {
        return;
      }
      this.child.kill("SIGKILL");
      if (!await this.waitForExit()) {
        throw new Error("kodelet acp process did not close after SIGKILL; cleanup is incomplete");
      }
    })().catch((error) => {
      this.closePromise = undefined;
      throw error;
    });
    await this.closePromise;
  }

  private async waitForExit(): Promise<boolean> {
    let timer: ReturnType<typeof setTimeout> | undefined;
    try {
      return await Promise.race([
        this.processClosed.then(() => true),
        new Promise<boolean>((resolve) => { timer = setTimeout(() => resolve(false), 1000); }),
      ]);
    } finally {
      clearTimeout(timer);
    }
  }

  private failTransport(error: Error): void {
    if (this.closed) {
      return;
    }
    this.closed = true;
    this.rejectPending(error);
    // Stop a child blocked on a broken/full pipe even if its caller does not
    // immediately close the session. close() still awaits actual process exit.
    this.child.kill("SIGKILL");
  }

  private request(method: string, params?: unknown): Promise<unknown> {
    if (this.closed) {
      throw new Error("kodelet acp process is closed");
    }
    const id = ++this.nextId;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      this.write({ jsonrpc: "2.0", id, method, params });
    });
  }

  private notify(method: string, params?: unknown): void {
    if (!this.closed) {
      this.write({ jsonrpc: "2.0", method, params });
    }
  }

  private write(message: JsonRPCMessage): void {
    try {
      this.child.stdin?.write(`${JSON.stringify(message)}\n`);
    } catch (error) {
      this.failTransport(error instanceof Error ? error : new Error(String(error)));
    }
  }

  private rejectPending(error: Error): void {
    for (const pending of this.pending.values()) {
      pending.reject(error);
    }
    this.pending.clear();
  }

  private handleLine(line: string): void {
    const trimmed = line.trim();
    if (!trimmed) {
      return;
    }

    let message: JsonRPCMessage;
    try {
      message = JSON.parse(trimmed) as JsonRPCMessage;
    } catch {
      for (const handler of this.notificationHandlers) {
        handler("$/stdout", { line });
      }
      return;
    }

    if (message.method && message.id !== undefined && message.id !== null) {
      this.respondToServerRequest(message);
      return;
    }
    if (message.method) {
      for (const handler of this.notificationHandlers) {
        handler(message.method, message.params);
      }
      return;
    }
    if (typeof message.id !== "number") {
      return;
    }
    const pending = this.pending.get(message.id);
    if (!pending) {
      return;
    }
    this.pending.delete(message.id);
    if (message.error) {
      pending.reject(new RPCError(message.error));
      return;
    }
    pending.resolve(message.result);
  }

  private respondToServerRequest(message: JsonRPCMessage): void {
    this.write({
      jsonrpc: "2.0",
      id: message.id,
      error: { code: -32601, message: `Unsupported client RPC method: ${message.method}` },
    });
  }
}

class LineBuffer {
  private buffer = "";
  private bytes = 0;
  private readonly decoder = new StringDecoder("utf8");

  push(chunk: Buffer | string): void {
    const text = this.decoder.write(typeof chunk === "string" ? Buffer.from(chunk) : chunk);
    this.buffer += text;
    this.bytes += Buffer.byteLength(text);
  }

  drainLines(): string[] {
    const lines: string[] = [];
    while (true) {
      const index = this.buffer.indexOf("\n");
      if (index === -1) {
        this.checkLimit(this.bytes);
        return lines;
      }
      const line = this.buffer.slice(0, index);
      const bytes = Buffer.byteLength(line);
      this.checkLimit(bytes);
      lines.push(line.replace(/\r$/, ""));
      this.buffer = this.buffer.slice(index + 1);
      this.bytes -= bytes + 1;
    }
  }

  private checkLimit(bytes: number): void {
    if (bytes > ACP_MESSAGE_LIMIT) {
      this.buffer = "";
      this.bytes = 0;
      throw new Error(`ACP stdout message exceeds ${ACP_MESSAGE_LIMIT} byte limit`);
    }
  }
}

function normalizeProfile(profile: CreateSessionOptions["profile"]): Profile | undefined {
  if (profile === undefined) {
    return undefined;
  }
  return profile instanceof Profile ? profile : new Profile(profile);
}

function buildPromptBlocks(options: RunOptions): ACPContentBlock[] {
  const prompt: ACPContentBlock[] = [{ type: "text", text: options.message }];
  for (const image of options.images ?? []) {
    prompt.push(imageToContentBlock(image));
  }
  return prompt;
}

function throwIfAlreadyAborted(signal?: AbortSignal): void {
  if (!signal?.aborted) {
    return;
  }
  signal.throwIfAborted();
  const error = new Error("The operation was aborted");
  error.name = "AbortError";
  throw error;
}

function imageToContentBlock(image: string): ACPContentBlock {
  const match = image.match(/^data:([^;,]+);base64,(.*)$/);
  if (match) {
    return { type: "image", mimeType: match[1], data: match[2] };
  }
  return { type: "image", uri: image };
}

function textFromACPContent(content: unknown): string {
  if (!isRecord(content)) {
    return "";
  }
  if (typeof content.text === "string") {
    return content.text;
  }
  if (isRecord(content.resource) && typeof content.resource.text === "string") {
    return content.resource.text;
  }
  return "";
}

function toolNameFromUpdate(update: ACPToolCallUpdate): string {
  if (typeof update.toolName === "string" && update.toolName.trim() !== "") {
    return update.toolName;
  }
  return "";
}

function toolContentToText(content: unknown): string {
  if (!Array.isArray(content)) {
    return "";
  }
  const parts: string[] = [];
  for (const item of content) {
    if (!isRecord(item)) {
      continue;
    }
    if (item.type === "content" && isRecord(item.content)) {
      const text = textFromACPContent(item.content);
      if (text !== "") {
        parts.push(text);
      }
      continue;
    }
    if (typeof item.path === "string") {
      parts.push(item.path);
    }
    if (typeof item.newText === "string") {
      parts.push(item.newText);
    }
  }
  return parts.join("\n");
}

function cleanEnv(env: NodeJS.ProcessEnv): NodeJS.ProcessEnv {
  return Object.fromEntries(Object.entries(env).filter((entry): entry is [string, string] => entry[1] !== undefined));
}

function stringField(record: Record<string, unknown>, key: string): string | undefined {
  const value = record[key];
  return typeof value === "string" ? value : undefined;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isSessionSteeringOutcome(value: unknown): value is SessionSteeringOutcome {
  return value === "injected"
    || value === "startedNewTurn"
    || value === "promptRequired"
    || value === "failed";
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
