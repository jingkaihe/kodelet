import { spawn as spawnProcess, type SpawnOptions } from "node:child_process";
import { EventEmitter } from "node:events";
import { chmod, mkdtemp, rm, writeFile } from "node:fs/promises";
import { createServer, type Server, type Socket } from "node:net";
import os from "node:os";
import path from "node:path";

import { createExtensionHost, type ExtensionHost } from "./api.js";
import { runWithHostRPCClient, type HostRPCClient } from "./context.js";
import type {
  ExtensionEntrypoint,
  UIConfirmRequest,
  UIInputRequest,
  UINotifyRequest,
  UISelectRequest,
} from "./types.js";

const ACP_PROTOCOL_VERSION = 1;

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
  input?(request: UIInputRequest): Promise<string | undefined> | string | undefined;
  confirm?(request: UIConfirmRequest): Promise<boolean> | boolean;
  select?(request: UISelectRequest): Promise<string | undefined> | string | undefined;
  notify?(request: UINotifyRequest): Promise<void> | void;
}

export interface ClientOptions {
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
  /** Named profile, inline profile, or omitted to use the default Kodelet config. */
  profile?: string | Profile | ProfileInput;
  /** In-process extension entrypoints to expose to Kodelet for this session. */
  extensions?: ExtensionEntrypoint[];
  /** Kept for API compatibility. ACP emits chunks as JSON-RPC session/update notifications. */
  streaming?: boolean;
  /** Working directory for the agent. Defaults to the client cwd or process.cwd(). */
  cwd?: string;
  /** Existing Kodelet conversation ID to resume. */
  resume?: string;
  /** Maximum agentic turns for each run. 0/undefined means Kodelet default. */
  maxTurns?: number;
  /** SDK-provided UI handlers for in-process extensions. */
  ui?: AgentUIHandlers;
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
  events: AgentStreamEvent[];
  exitCode: number;
  stopReason?: string;
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

interface LaunchConfig {
  args: string[];
  env: NodeJS.ProcessEnv;
  tempConfig?: TempConfig;
  configFileMode?: ConfigFileMode;
}

type ConfigFileMode = "merge" | "isolated";

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

  constructor(options: ClientOptions = {}) {
    this.command = options.command ?? "kodelet";
    this.cwd = path.resolve(options.cwd ?? process.cwd());
    this.env = options.env ?? {};
    this.spawn = options.spawn ?? ((command, args, spawnOptions) => spawnProcess(command, args, spawnOptions) as SpawnedProcess);
  }

  async createSession(options: CreateSessionOptions = {}): Promise<Session> {
    const bridge = options.extensions?.length
      ? await InMemoryExtensionBridge.create(options.extensions, { ui: options.ui })
      : undefined;
    const cwd = path.resolve(options.cwd ?? this.cwd);
    const profile = normalizeProfile(options.profile);
    let launch: LaunchConfig | undefined;
    let rpc: ACPRPCClient | undefined;

    try {
      launch = await buildLaunchConfig(profile, bridge);
      const env = cleanEnv({
        ...this._baseEnv({ isolateKodeletEnv: launch.configFileMode === "isolated" }),
        ...launch.env,
      });
      const args = [...launch.args, "acp", ...acpServerArgs(options)];
      rpc = new ACPRPCClient(this._spawn(args, { cwd, env, stdio: ["pipe", "pipe", "pipe"] }));
      await rpc.initialize();
      const sessionID = options.resume ? await rpc.loadSession(options.resume, cwd) : await rpc.createSession(cwd);
      const session = new Session(this, {
        ...options,
        cwd,
        profile,
        sessionID,
        rpc,
        extensionBridge: bridge,
        tempConfig: launch.tempConfig,
      });
      this.sessions.add(session);
      return session;
    } catch (error) {
      await rpc?.close();
      await bridge?.close();
      await launch?.tempConfig?.close();
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

  _baseEnv(options: { isolateKodeletEnv?: boolean } = {}): NodeJS.ProcessEnv {
    const env = { ...process.env };
    if (options.isolateKodeletEnv) {
      for (const key of Object.keys(env)) {
        if (key.startsWith("KODELET_")) {
          delete env[key];
        }
      }
    }
    return { ...env, ...this.env };
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
  extensionBridge?: InMemoryExtensionBridge;
  tempConfig?: TempConfig;
}

export class Session extends EventEmitter {
  readonly cwd: string;
  private readonly client: Client;
  private readonly rpc: ACPRPCClient;
  private readonly maxTurns?: number;
  private readonly extensionBridge?: InMemoryExtensionBridge;
  private readonly tempConfig?: TempConfig;
  private conversationId: string;
  private closed = false;
  private running = false;

  constructor(client: Client, options: SessionInternalOptions) {
    super();
    this.client = client;
    this.cwd = options.cwd;
    this.rpc = options.rpc;
    this.maxTurns = options.maxTurns;
    this.conversationId = options.sessionID;
    this.extensionBridge = options.extensionBridge;
    this.tempConfig = options.tempConfig;
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

  async close(): Promise<void> {
    if (this.closed) {
      return;
    }
    this.closed = true;
    await this.rpc.close();
    await this.extensionBridge?.close();
    await this.tempConfig?.close();
    this.client._deleteSession(this);
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
        if (tool.status !== "completed" && tool.status !== "failed") {
          return;
        }
        const toolCallId = tool.toolCallId;
        this.emitSDKEvent(
          "tool.result",
          {
            toolName: (toolCallId && toolNames.get(toolCallId)) || toolNameFromUpdate(tool),
            result: toolContentToText(tool.content),
            toolCallId,
            status: tool.status,
          },
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
}

class ACPRPCClient {
  private nextId = 0;
  private pending = new Map<number, PendingRPCRequest>();
  private readonly stdoutBuffer = new LineBuffer();
  private readonly stderrChunks: string[] = [];
  private readonly notificationHandlers = new Set<(method: string, params: unknown) => void>();
  private closed = false;

  constructor(private readonly child: SpawnedProcess) {
    if (!child.stdin) {
      throw new Error("kodelet acp process did not expose stdin");
    }
    child.stdout?.on("data", (chunk: Buffer | string) => {
      this.stdoutBuffer.push(String(chunk));
      for (const line of this.stdoutBuffer.drainLines()) {
        this.handleLine(line);
      }
    });
    child.stderr?.on("data", (chunk: Buffer | string) => {
      this.stderrChunks.push(String(chunk));
    });
    child.once("close", (code: number | null, signal: NodeJS.Signals | null) => {
      this.closed = true;
      const message = this.stderrChunks.join("").trim() || `kodelet acp exited with status ${code ?? "unknown"}${signal ? ` (${signal})` : ""}`;
      for (const pending of this.pending.values()) {
        pending.reject(new AgentRunError(message, { code, signal, stderr: this.stderrChunks.join("") }));
      }
      this.pending.clear();
    });
  }

  async initialize(): Promise<void> {
    await this.request("initialize", {
      protocolVersion: ACP_PROTOCOL_VERSION,
      clientCapabilities: {
        terminal: true,
        fs: { readTextFile: false, writeTextFile: false },
      },
      clientInfo: { name: "kodelet-sdk", title: "Kodelet SDK" },
    });
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

  cancelSession(sessionId: string): void {
    this.notify("session/cancel", { sessionId });
  }

  onNotification(handler: (method: string, params: unknown) => void): () => void {
    this.notificationHandlers.add(handler);
    return () => this.notificationHandlers.delete(handler);
  }

  async close(): Promise<void> {
    if (this.closed) {
      return;
    }
    this.closed = true;
    this.child.kill("SIGTERM");
    await new Promise<void>((resolve) => {
      this.child.once("close", () => resolve());
      setTimeout(resolve, 1000).unref?.();
    });
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
    this.child.stdin?.write(`${JSON.stringify(message)}\n`);
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

class InMemoryExtensionBridge {
  private constructor(
    private readonly rootDir: string,
    private readonly servers: ExtensionSocketServer[],
  ) {}

  static async create(entrypoints: ExtensionEntrypoint[], options: { ui?: AgentUIHandlers } = {}): Promise<InMemoryExtensionBridge> {
    const rootDir = await mkdtemp(path.join(os.tmpdir(), "kodelet-sdk-extensions-"));
    const servers: ExtensionSocketServer[] = [];

    try {
      for (const [index, entrypoint] of entrypoints.entries()) {
        const id = `sdk-${index + 1}`;
        const socketPath = extensionSocketPath(rootDir, id);
        const host = await createExtensionHost(entrypoint);
        const server = new ExtensionSocketServer(host, socketPath, options.ui);
        await server.listen();
        servers.push(server);

        const executablePath = path.join(rootDir, `kodelet-extension-${id}`);
        await writeFile(executablePath, extensionBridgeExecutable(socketPath), "utf8");
        await chmod(executablePath, 0o755);
      }
    } catch (error) {
      await Promise.allSettled(servers.map((server) => server.close()));
      await rm(rootDir, { recursive: true, force: true });
      throw error;
    }

    return new InMemoryExtensionBridge(rootDir, servers);
  }

  config(): Record<string, unknown> {
    return {
      enabled: true,
      local_dir: this.rootDir,
      allow: [this.rootDir],
    };
  }

  async close(): Promise<void> {
    await Promise.allSettled(this.servers.map((server) => server.close()));
    await rm(this.rootDir, { recursive: true, force: true });
  }
}

class TempConfig {
  private constructor(private readonly rootDir: string, readonly path: string) {}

  static async create(config: Record<string, unknown>): Promise<TempConfig> {
    const rootDir = await mkdtemp(path.join(os.tmpdir(), "kodelet-sdk-config-"));
    const configPath = path.join(rootDir, "kodelet-config.json");
    await writeFile(configPath, `${JSON.stringify(config, null, 2)}\n`, "utf8");
    return new TempConfig(rootDir, configPath);
  }

  async close(): Promise<void> {
    await rm(this.rootDir, { recursive: true, force: true });
  }
}

class ExtensionSocketServer implements HostRPCClient {
  private server?: Server;
  private socket?: Socket;
  private nextId = 0;
  private pending = new Map<number, PendingRPCRequest>();

  constructor(
    private readonly host: ExtensionHost,
    private readonly socketPath: string,
    private readonly ui?: AgentUIHandlers,
  ) {}

  async listen(): Promise<void> {
    await rm(this.socketPath, { force: true });
    this.server = createServer((socket) => {
      this.socket = socket;
      const reader = new FrameReader();
      socket.on("data", (chunk) => {
        for (const payload of reader.push(chunk)) {
          void this.handlePayload(payload, socket);
        }
      });
      socket.on("close", () => {
        if (this.socket === socket) {
          this.socket = undefined;
        }
      });
    });

    await new Promise<void>((resolve, reject) => {
      this.server?.once("error", reject);
      this.server?.listen(this.socketPath, () => {
        this.server?.off("error", reject);
        resolve();
      });
    });
  }

  async close(): Promise<void> {
    for (const pending of this.pending.values()) {
      pending.reject(new Error("Extension bridge closed"));
    }
    this.pending.clear();
    this.socket?.destroy();
    await new Promise<void>((resolve) => {
      if (!this.server) {
        resolve();
        return;
      }
      this.server.close(() => resolve());
    });
    await rm(this.socketPath, { force: true });
  }

  async request(method: string, params?: unknown): Promise<unknown> {
    const localUIResponse = await this.tryHandleLocalUIRequest(method, params);
    if (localUIResponse.handled) {
      return localUIResponse.result;
    }

    if (!this.socket) {
      throw new Error("Extension bridge is not connected");
    }

    const id = ++this.nextId;
    writeFrame(this.socket, JSON.stringify({ jsonrpc: "2.0", id, method, params }));
    return await new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
    });
  }

  private async handlePayload(payload: Buffer, socket: Socket): Promise<void> {
    let message: JsonRPCMessage;
    try {
      message = JSON.parse(payload.toString("utf8")) as JsonRPCMessage;
    } catch (error) {
      writeFrame(socket, JSON.stringify({ jsonrpc: "2.0", id: null, error: { code: -32700, message: errorMessage(error) } }));
      return;
    }

    if (!message.method && message.id !== undefined) {
      this.handleResponse(message);
      return;
    }

    if (!message.method || message.id === undefined || message.id === null) {
      return;
    }

    try {
      const result = await runWithHostRPCClient(this, () => this.dispatch(message));
      writeFrame(socket, JSON.stringify({ jsonrpc: "2.0", id: message.id, result }));
    } catch (error) {
      writeFrame(socket, JSON.stringify({ jsonrpc: "2.0", id: message.id, error: { code: -32000, message: errorMessage(error) } }));
    }
  }

  private handleResponse(response: JsonRPCMessage): void {
    if (typeof response.id !== "number") {
      return;
    }
    const pending = this.pending.get(response.id);
    if (!pending) {
      return;
    }
    this.pending.delete(response.id);
    if (response.error) {
      pending.reject(new Error(response.error.message));
      return;
    }
    pending.resolve(response.result);
  }

  private async dispatch(message: JsonRPCMessage): Promise<unknown> {
    switch (message.method) {
      case "extension.initialize":
        return this.host.initialize(message.params as never);
      case "extension.tool.execute":
        return await this.host.executeTool(message.params as never);
      case "extension.command.execute":
        return await this.host.executeCommand(message.params as never);
      case "extension.event.handle":
        return await this.host.handleEvent(message.params as never);
      case "$/cancelRequest":
        return undefined;
      default:
        throw new Error(`Unknown JSON-RPC method: ${message.method}`);
    }
  }

  private async tryHandleLocalUIRequest(method: string, params: unknown): Promise<{ handled: boolean; result?: unknown }> {
    switch (method) {
      case "kodelet.ui.input": {
        if (!this.ui?.input) {
          return { handled: true, result: unavailableUI("ui input is not available") };
        }
        const value = await this.ui.input(params as UIInputRequest);
        return { handled: true, result: value === undefined ? dismissedUI() : { status: "submitted", value } };
      }
      case "kodelet.ui.confirm": {
        if (!this.ui?.confirm) {
          return { handled: true, result: unavailableUI("ui confirm is not available") };
        }
        const confirmed = await this.ui.confirm(params as UIConfirmRequest);
        return { handled: true, result: { status: "submitted", confirmed } };
      }
      case "kodelet.ui.select": {
        if (!this.ui?.select) {
          return { handled: true, result: unavailableUI("ui select is not available") };
        }
        const value = await this.ui.select(params as UISelectRequest);
        return { handled: true, result: value === undefined ? dismissedUI() : { status: "submitted", value } };
      }
      case "kodelet.ui.notify": {
        if (!this.ui?.notify) {
          return { handled: true, result: unavailableUI("ui notify is not available") };
        }
        await this.ui.notify(params as UINotifyRequest);
        return { handled: true, result: { status: "submitted" } };
      }
      default:
        return { handled: false };
    }
  }
}

class FrameReader {
  private buffer: Buffer = Buffer.alloc(0);

  push(chunk: Buffer): Buffer[] {
    this.buffer = Buffer.concat([this.buffer, chunk]);
    const frames: Buffer[] = [];
    while (true) {
      const frame = tryReadFrame(this.buffer);
      if (!frame) {
        return frames;
      }
      frames.push(frame.payload);
      this.buffer = frame.remaining;
    }
  }
}

class LineBuffer {
  private buffer = "";

  push(chunk: string): void {
    this.buffer += chunk;
  }

  drainLines(): string[] {
    const lines: string[] = [];
    while (true) {
      const index = this.buffer.indexOf("\n");
      if (index === -1) {
        return lines;
      }
      lines.push(this.buffer.slice(0, index).replace(/\r$/, ""));
      this.buffer = this.buffer.slice(index + 1);
    }
  }
}

function normalizeProfile(profile: CreateSessionOptions["profile"]): Profile | undefined {
  if (profile === undefined) {
    return undefined;
  }
  return profile instanceof Profile ? profile : new Profile(profile);
}

function acpServerArgs(options: CreateSessionOptions): string[] {
  const args: string[] = [];
  if (options.maxTurns !== undefined && options.maxTurns > 0) {
    args.push("--max-turns", String(options.maxTurns));
  }
  return args;
}

async function buildLaunchConfig(profile: Profile | undefined, bridge: InMemoryExtensionBridge | undefined): Promise<LaunchConfig> {
  const resolved = profile?.toLaunchConfig();
  const profileConfig = resolved?.config;
  const config = pruneUndefined({
    ...(profileConfig ?? {}),
    ...(profileConfig && profileConfig.profile === undefined ? { profile: "default" } : {}),
    ...(bridge ? { extensions: bridge.config() } : {}),
  });

  if (Object.keys(config).length === 0) {
    return { args: resolved?.args ?? [], env: {} };
  }

  const tempConfig = await TempConfig.create(config);
  const configFileMode: ConfigFileMode = profileConfig ? "isolated" : "merge";
  return {
    args: resolved?.args ?? [],
    env: {
      KODELET_CONFIG_FILE: tempConfig.path,
      KODELET_CONFIG_FILE_MODE: configFileMode,
    },
    tempConfig,
    configFileMode,
  };
}

function pruneUndefined(value: Record<string, unknown>): Record<string, unknown> {
  const result: Record<string, unknown> = {};
  for (const [key, item] of Object.entries(value)) {
    if (item === undefined) {
      continue;
    }
    if (isPlainObject(item)) {
      result[key] = pruneUndefined(item as Record<string, unknown>);
      continue;
    }
    result[key] = item;
  }
  return result;
}

function buildPromptBlocks(options: RunOptions): ACPContentBlock[] {
  const prompt: ACPContentBlock[] = [{ type: "text", text: options.message }];
  for (const image of options.images ?? []) {
    prompt.push(imageToContentBlock(image));
  }
  return prompt;
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
  return "";
}

function toolNameFromUpdate(update: ACPToolCallUpdate): string {
  if (typeof update.title === "string" && update.title.trim() !== "") {
    return update.title;
  }
  if (typeof update.kind === "string" && update.kind.trim() !== "") {
    return update.kind;
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

function isPlainObject(value: unknown): value is ProfileObject {
  return typeof value === "object" && value !== null && !Array.isArray(value);
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

function writeFrame(socket: Socket, payload: string): void {
  socket.write(`Content-Length: ${Buffer.byteLength(payload, "utf8")}\r\n\r\n${payload}`);
}

function extensionSocketPath(rootDir: string, id: string): string {
  if (process.platform === "win32") {
    return `\\\\.\\pipe\\kodelet-sdk-${process.pid}-${Date.now()}-${id}`;
  }
  return path.join(rootDir, `${id}.sock`);
}

function extensionBridgeExecutable(socketPath: string): string {
  return `#!/usr/bin/env node
const net = require("node:net");
const process = require("node:process");
const SOCKET_PATH = ${JSON.stringify(socketPath)};

let stdinBuffer = Buffer.alloc(0);
let socketBuffer = Buffer.alloc(0);
const socket = net.createConnection(SOCKET_PATH);

socket.on("data", (chunk) => {
  socketBuffer = Buffer.concat([socketBuffer, chunk]);
  while (true) {
    const frame = tryReadFrame(socketBuffer);
    if (!frame) break;
    socketBuffer = frame.remaining;
    writeFrame(process.stdout, frame.payload);
  }
});

socket.on("error", (error) => {
  process.stderr.write(JSON.stringify({ level: "error", message: "kodelet SDK extension bridge failed", error: error.message }) + "\\n");
  process.exit(1);
});

socket.on("close", () => process.exit(0));

process.stdin.on("data", (chunk) => {
  stdinBuffer = Buffer.concat([stdinBuffer, chunk]);
  while (true) {
    const frame = tryReadFrame(stdinBuffer);
    if (!frame) break;
    stdinBuffer = frame.remaining;
    writeFrame(socket, frame.payload);
  }
});
process.stdin.resume();

function tryReadFrame(buffer) {
  const headerEnd = buffer.indexOf("\\r\\n\\r\\n");
  const fallbackHeaderEnd = headerEnd === -1 ? buffer.indexOf("\\n\\n") : -1;
  const separatorIndex = headerEnd === -1 ? fallbackHeaderEnd : headerEnd;
  if (separatorIndex === -1) return undefined;
  const separatorLength = headerEnd === -1 ? 2 : 4;
  const header = buffer.subarray(0, separatorIndex).toString("ascii");
  const contentLength = parseContentLength(header);
  const payloadStart = separatorIndex + separatorLength;
  const payloadEnd = payloadStart + contentLength;
  if (buffer.length < payloadEnd) return undefined;
  return { payload: buffer.subarray(payloadStart, payloadEnd), remaining: buffer.subarray(payloadEnd) };
}

function parseContentLength(header) {
  for (const line of header.split(/\\r?\\n/)) {
    const [key, value] = line.split(":", 2);
    if (key && key.trim().toLowerCase() === "content-length") {
      const parsed = Number.parseInt((value || "").trim(), 10);
      if (Number.isFinite(parsed) && parsed >= 0) return parsed;
    }
  }
  throw new Error("Missing Content-Length header");
}

function writeFrame(stream, payload) {
  stream.write("Content-Length: " + Buffer.byteLength(payload) + "\\r\\n\\r\\n");
  stream.write(payload);
}
`;
}

function unavailableUI(reason: string): { status: string; reason: string } {
  return { status: "unavailable", reason };
}

function dismissedUI(): { status: string } {
  return { status: "dismissed" };
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
