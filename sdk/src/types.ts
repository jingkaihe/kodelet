import type { z } from "zod";

export type Awaitable<T> = T | Promise<T>;
export type AnyZodSchema = z.ZodTypeAny;
export type InferInput<Schema> = Schema extends z.ZodTypeAny ? z.infer<Schema> : Record<string, unknown>;

export interface ExtensionMetadata {
  name?: string;
  version?: string;
}

export interface ToolExecutionResult {
  content: string;
  data?: Record<string, unknown>;
  error?: string;
}

export type CommandAction = "pass" | "respond" | "runAgent";

export type CommandResult =
  | { action: "pass" }
  | { action: "respond"; response: string }
  | { action: "runAgent"; prompt: string; recipeName?: string };

export type CommandKind = "command" | "recipe";

export interface CommandInvocation {
  raw: string;
  commandName: string;
  args: string[];
  flags: Record<string, string | boolean | string[]>;
}

export interface BaseCallContext {
  sessionId?: string;
  conversationId?: string;
  cwd?: string;
  provider?: string;
  model?: string;
  profile?: string;
  recipeName?: string;
  invokedBy?: string;
}

export interface StorageContext {
  dataDir: string;
  readText(path: string): Promise<string | undefined>;
  writeText(path: string, content: string): Promise<void>;
  readJson<T = unknown>(path: string): Promise<T | undefined>;
  writeJson(path: string, value: unknown): Promise<void>;
}

export interface PathContext {
  resolveWorkspacePath(path: string): string;
  relativeToWorkspace(path: string): string;
}

export interface FileInfo {
  name: string;
  path: string;
  type: "file" | "dir" | "other";
}

export interface FileSystemContext {
  exists(path: string): Promise<boolean>;
  readText(path: string): Promise<string>;
  writeText(path: string, content: string): Promise<void>;
  list(path: string): Promise<FileInfo[]>;
}

export interface ExecResult {
  stdout: string;
  stderr: string;
  exitCode: number;
}

export interface ProcessContext {
  exec(command: string, args?: string[], opts?: { cwd?: string; timeoutMs?: number }): Promise<ExecResult>;
  spawn(command: string, args?: string[], opts?: { cwd?: string; detach?: boolean }): Promise<void>;
}

export interface EnvContext {
  get(name: string): string | undefined;
}

export interface LogContext {
  debug(message: string, fields?: Record<string, unknown>): void;
  info(message: string, fields?: Record<string, unknown>): void;
  warn(message: string, fields?: Record<string, unknown>): void;
  error(message: string, fields?: Record<string, unknown>): void;
}

export interface SharedContext extends Required<Pick<BaseCallContext, "cwd">>, Omit<BaseCallContext, "cwd"> {
  storage: StorageContext;
  path: PathContext;
  fs: FileSystemContext;
  process: ProcessContext;
  env: EnvContext;
  log: LogContext;
}

export interface ToolContext extends SharedContext {}

export interface CommandContext extends SharedContext {
  input: CommandInvocation;
}

export interface EventContext extends SharedContext {}

export interface ToolRegistration<Schema extends AnyZodSchema = AnyZodSchema> {
  name: string;
  description: string;
  inputSchema: Schema;
  execute(input: InferInput<Schema>, ctx: ToolContext): Awaitable<ToolExecutionResult | string>;
}

export interface CommandRegistration<Schema extends AnyZodSchema | undefined = undefined> {
  name: string;
  aliases?: string[];
  description: string;
  inputSchema?: Schema;
  kind?: CommandKind;
  execute(input: InferInput<Schema>, ctx: CommandContext): Awaitable<CommandResult | undefined>;
}

export type EventName =
  | "session.start"
  | "resources.discover"
  | "user.message"
  | "agent.init"
  | "agent.start"
  | "turn.start"
  | "tool.call"
  | "tool.result"
  | "turn.end"
  | "agent.end"
  | "session.end"
  | (string & {});

export interface EmptyEvent {}

export interface ToolCallEvent {
  tool: {
    name: string;
    callId?: string;
    input: unknown;
  };
}

export interface ToolResultEvent {
  tool: {
    name: string;
    callId?: string;
    input: unknown;
    output: unknown;
  };
}

export interface UserMessageEvent {
  message: string;
}

export interface AgentInitEvent {
  systemPrompt?: string;
}

export interface TurnEndEvent {
  response: string;
  turnNumber?: number;
}

export interface AgentEndEvent {
  messages?: unknown[];
}

export interface EventPayloadMap {
  "session.start": EmptyEvent;
  "resources.discover": EmptyEvent;
  "tool.call": ToolCallEvent;
  "tool.result": ToolResultEvent;
  "user.message": UserMessageEvent;
  "agent.init": AgentInitEvent;
  "agent.start": EmptyEvent;
  "turn.start": { turnNumber?: number };
  "turn.end": TurnEndEvent;
  "agent.end": AgentEndEvent;
  "session.end": EmptyEvent;
}

export type EventPayload<Name extends EventName> = Name extends keyof EventPayloadMap
  ? EventPayloadMap[Name]
  : Record<string, unknown>;

export type ExtensionEvent<Name extends EventName> = EventPayload<Name> & {
  id: string;
  event: Name;
};

export interface EventBlock {
  reason: string;
}

export interface EventResult {
  input?: unknown;
  output?: unknown;
  block?: EventBlock;
  message?: string;
  followUpMessages?: string[];
  systemPrompt?: {
    append?: string;
    prepend?: string;
    replace?: string;
  };
  resources?: unknown;
}

export interface EventSubscriptionOptions {
  priority?: number;
}

export type EventHandler<Name extends EventName = EventName> = (
  event: ExtensionEvent<Name>,
  ctx: EventContext,
) => Awaitable<EventResult | void>;

export interface ExtensionAPI {
  setMetadata(metadata: ExtensionMetadata): void;
  registerTool<Schema extends AnyZodSchema>(registration: ToolRegistration<Schema>): void;
  registerCommand<Schema extends AnyZodSchema | undefined = undefined>(
    registration: CommandRegistration<Schema>,
  ): void;
  on<Name extends EventName>(event: Name, handler: EventHandler<Name>): void;
  on<Name extends EventName>(event: Name, options: EventSubscriptionOptions, handler: EventHandler<Name>): void;
}

export type ExtensionEntrypoint = (ext: ExtensionAPI) => Awaitable<void>;

export interface InitializeParams {
  protocolVersion: string;
  kodelet?: Record<string, unknown>;
  extension: {
    id: string;
    config?: Record<string, unknown>;
    cwd?: string;
    dataDir?: string;
  };
  capabilities?: Record<string, unknown>;
}

export interface InitializeResult {
  name: string;
  version?: string;
  tools: Array<{
    name: string;
    description: string;
    inputSchema: Record<string, unknown>;
  }>;
  commands: Array<{
    name: string;
    aliases?: string[];
    description: string;
    inputSchema?: Record<string, unknown>;
    kind?: CommandKind;
  }>;
  subscriptions: Array<{
    event: string;
    priority?: number;
  }>;
}

export interface ExecuteToolParams {
  name: string;
  input: unknown;
  context?: BaseCallContext;
}

export interface ExecuteCommandParams {
  name: string;
  input?: Record<string, unknown>;
  context?: BaseCallContext;
  invocation: CommandInvocation;
}

export interface HandleEventParams<Name extends EventName = EventName> {
  id: string;
  event: Name;
  payload?: EventPayload<Name>;
  context?: BaseCallContext;
}
