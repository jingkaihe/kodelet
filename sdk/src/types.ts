import type { z } from "zod";

export type Awaitable<T> = T | Promise<T>;
export type AnyZodSchema = z.ZodTypeAny;
export type JSONSchema = Record<string, unknown>;
export type ToolInputSchema = AnyZodSchema | JSONSchema;
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

export interface ToolUpdateRequest {
  content: string;
  data?: Record<string, unknown>;
}

export type CommandAction = "pass" | "respond" | "runAgent";

export type CommandPassResult = { action: "pass" };
export type CommandRespondResult = { action: "respond"; response: string };
export type CommandRunAgentResult = { action: "runAgent"; prompt: string; recipeName?: string };

export type CommandResult =
  | CommandPassResult
  | CommandRespondResult
  | CommandRunAgentResult;

export type CommandKind = "command" | "recipe";
export type CommandFlagValue = string | boolean | string[];

export interface CommandInvocation {
  raw: string;
  commandName: string;
  args: string[];
  flags: Record<string, CommandFlagValue>;
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

export interface ProcessExecOptions {
  cwd?: string;
  timeoutMs?: number;
}

export interface ProcessSpawnOptions {
  cwd?: string;
  detach?: boolean;
}

export interface ProcessContext {
  exec(command: string, args?: string[], opts?: ProcessExecOptions): Promise<ExecResult>;
  spawn(command: string, args?: string[], opts?: ProcessSpawnOptions): Promise<void>;
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

export interface UIInputRequest {
  title: string;
  id?: string;
  helpText?: string;
  message?: string;
  placeholder?: string;
  defaultValue?: string;
  submitButtonText?: string;
  cancelButtonText?: string;
  required?: boolean;
  secret?: boolean;
}

export interface UIConfirmRequest {
  title: string;
  id?: string;
  message?: string;
  confirmButtonText?: string;
  cancelButtonText?: string;
}

export interface UISelectRequest {
  title: string;
  id?: string;
  message?: string;
  options: string[];
  submitButtonText?: string;
  cancelButtonText?: string;
}

export interface UINotifyRequest {
  title?: string;
  message: string;
}

export type UIInputStatus = "submitted" | "dismissed" | "timeout" | "unavailable";

export interface UIInputResponse {
  status: UIInputStatus;
  value?: string;
  confirmed?: boolean;
  reason?: string;
}

export interface UIContext {
  input(request: UIInputRequest): Promise<string | undefined>;
  confirm(request: UIConfirmRequest): Promise<boolean>;
  select(request: UISelectRequest): Promise<string | undefined>;
  notify(request: string | UINotifyRequest): Promise<void>;
}

export interface SharedContext extends Required<Pick<BaseCallContext, "cwd">>, Omit<BaseCallContext, "cwd"> {
  signal: AbortSignal;
  storage: StorageContext;
  path: PathContext;
  fs: FileSystemContext;
  process: ProcessContext;
  env: EnvContext;
  log: LogContext;
  ui: UIContext;
}

export interface ToolContext extends SharedContext {
  update(content: string, data?: Record<string, unknown>): Promise<void>;
}

export interface CommandContext extends SharedContext {
  input: CommandInvocation;
}

export interface EventContext extends SharedContext {}

export interface ToolRegistration<Schema extends ToolInputSchema = ToolInputSchema> {
  name: string;
  description: string;
  inputSchema: Schema;
  timeoutInSec?: number;
  execute(input: InferInput<Schema>, ctx: ToolContext): Awaitable<ToolExecutionResult | string>;
}

export interface CommandRegistration<Schema extends AnyZodSchema | undefined = undefined> {
  name: string;
  aliases?: string[];
  description: string;
  inputSchema?: Schema;
  kind?: CommandKind;
  timeoutInSec?: number;
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
  | "tool.update"
  | "tool.result"
  | "turn.end"
  | "agent.end"
  | "session.end"
  | (string & {});

export interface EmptyEventPayload {}

export interface ToolCallDetails {
  name: string;
  callId?: string;
  input: unknown;
}

export interface ToolResultDetails extends ToolCallDetails {
  output: unknown;
}

export interface ToolCallEventPayload {
  tool: ToolCallDetails;
}

export interface ToolResultEventPayload {
  tool: ToolResultDetails;
}

export interface UserMessageEventPayload {
  message: string;
}

export interface AgentInitEventPayload {
  systemPrompt?: string;
}

export interface TurnStartEventPayload {
  turnNumber?: number;
}

export interface TurnEndEventPayload {
  response: string;
  turnNumber?: number;
}

export interface AgentEndEventPayload {
  messages?: unknown[];
}

export interface EventPayloadMap {
  "session.start": EmptyEventPayload;
  "resources.discover": EmptyEventPayload;
  "tool.call": ToolCallEventPayload;
  "tool.update": ToolResultEventPayload;
  "tool.result": ToolResultEventPayload;
  "user.message": UserMessageEventPayload;
  "agent.init": AgentInitEventPayload;
  "agent.start": EmptyEventPayload;
  "turn.start": TurnStartEventPayload;
  "turn.end": TurnEndEventPayload;
  "agent.end": AgentEndEventPayload;
  "session.end": EmptyEventPayload;
}

export type EventPayload<Name extends EventName> = Name extends keyof EventPayloadMap
  ? EventPayloadMap[Name]
  : Record<string, unknown>;

export type ExtensionEvent<Name extends EventName> = EventPayload<Name> & {
  id: string;
  event: Name;
};

export type EmptyEvent = ExtensionEvent<"session.start" | "resources.discover" | "agent.start" | "session.end">;
export type SessionStartEvent = ExtensionEvent<"session.start">;
export type ResourcesDiscoverEvent = ExtensionEvent<"resources.discover">;
export type UserMessageEvent = ExtensionEvent<"user.message">;
export type AgentInitEvent = ExtensionEvent<"agent.init">;
export type AgentStartEvent = ExtensionEvent<"agent.start">;
export type TurnStartEvent = ExtensionEvent<"turn.start">;
export type ToolCallEvent = ExtensionEvent<"tool.call">;
export type ToolUpdateEvent = ExtensionEvent<"tool.update">;
export type ToolResultEvent = ExtensionEvent<"tool.result">;
export type TurnEndEvent = ExtensionEvent<"turn.end">;
export type AgentEndEvent = ExtensionEvent<"agent.end">;
export type SessionEndEvent = ExtensionEvent<"session.end">;

export interface EventBlock {
  reason: string;
}

export interface SystemPromptPatch {
  append?: string;
  prepend?: string;
  replace?: string;
}

export interface ToolListPatch {
  disable?: string[];
  enable?: string[];
}

export interface EventResult {
  input?: unknown;
  output?: unknown;
  block?: EventBlock;
  message?: string;
  followUpMessages?: string[];
  systemPrompt?: SystemPromptPatch;
  tools?: ToolListPatch;
  resources?: unknown;
}

export interface EventSubscriptionOptions {
  priority?: number;
  timeoutInSec?: number;
}

export type EventHandler<Name extends EventName = EventName> = (
  event: ExtensionEvent<Name>,
  ctx: EventContext,
) => Awaitable<EventResult | void>;

export interface ExtensionAPI {
  setMetadata(metadata: ExtensionMetadata): void;
  registerTool<Schema extends ToolInputSchema>(registration: ToolRegistration<Schema>): void;
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
    timeoutInSec?: number;
  }>;
  commands: Array<{
    name: string;
    aliases?: string[];
    description: string;
    inputSchema?: Record<string, unknown>;
    kind?: CommandKind;
    timeoutInSec?: number;
  }>;
  subscriptions: Array<{
    event: string;
    priority?: number;
    timeoutInSec?: number;
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
