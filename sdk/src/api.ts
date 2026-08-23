import { z } from "zod";

import { createCommandContext, createEventContext, createShortcutContext, createToolContext } from "./context.js";
import type {
  AnyZodSchema,
  BaseCallContext,
  CommandRegistration,
  CommandResult,
  EventHandler,
  EventName,
  EventResult,
  EventSubscriptionOptions,
  ExecuteCommandParams,
  ExecuteShortcutParams,
  ExecuteToolParams,
  ExtensionAPI,
  ExtensionEntrypoint,
  ExtensionEvent,
  ExtensionMetadata,
  HandleEventParams,
  InitializeParams,
  InitializeResult,
  ShortcutOptions,
  ToolExecutionResult,
  ToolInputSchema,
  ToolRegistration,
} from "./types.js";

interface RegisteredTool {
  registration: ToolRegistration<ToolInputSchema>;
  inputSchema: Record<string, unknown>;
  parseInput(input: unknown): Promise<unknown>;
}

interface RegisteredCommand {
  registration: CommandRegistration<AnyZodSchema | undefined>;
  inputSchema?: Record<string, unknown>;
}

interface RegisteredShortcut {
  key: string;
  options: ShortcutOptions;
}

interface RegisteredEventHandler {
  event: EventName;
  priority: number;
  timeoutInSec?: number;
  order: number;
  handler: EventHandler<EventName>;
}

export class ExtensionHost implements ExtensionAPI {
  private metadata: ExtensionMetadata = {};
  private tools = new Map<string, RegisteredTool>();
  private commands = new Map<string, RegisteredCommand>();
  private shortcuts = new Map<string, RegisteredShortcut>();
  private handlers: RegisteredEventHandler[] = [];
  private order = 0;
  private initParams?: InitializeParams;

  setMetadata(metadata: ExtensionMetadata): void {
    this.metadata = { ...this.metadata, ...metadata };
  }

  registerTool<Schema extends ToolInputSchema>(registration: ToolRegistration<Schema>): void {
    if (this.tools.has(registration.name)) {
      throw new Error(`Duplicate extension tool registration: ${registration.name}`);
    }
    const inputSchema = registration.inputSchema;
    this.tools.set(registration.name, {
      registration: registration as ToolRegistration<ToolInputSchema>,
      inputSchema: isZodSchema(inputSchema) ? zodSchemaToJsonSchema(inputSchema) : inputSchema,
      parseInput: isZodSchema(inputSchema)
        ? (input) => inputSchema.parseAsync(input)
        : async (input) => input,
    });
  }

  registerCommand<Schema extends AnyZodSchema | undefined = undefined>(registration: CommandRegistration<Schema>): void {
    const primaryName = normalizeCommandName(registration.name);
    const names = [primaryName, ...(registration.aliases ?? []).map(normalizeCommandName).filter((name) => name && name !== primaryName)];
    if (new Set(names).size !== names.length) {
      throw new Error(`Duplicate extension command registration: ${registration.name}`);
    }
    for (const name of names) {
      if (this.commands.has(name)) {
        throw new Error(`Duplicate extension command registration: ${name}`);
      }
    }
    this.commands.set(normalizeCommandName(registration.name), {
      registration: registration as CommandRegistration<AnyZodSchema | undefined>,
      inputSchema: registration.inputSchema ? zodSchemaToJsonSchema(registration.inputSchema) : undefined,
    });
  }

  registerShortcut(shortcut: string, options: ShortcutOptions): void {
    const key = normalizeShortcutKey(shortcut);
    if (this.shortcuts.has(key)) {
      throw new Error(`Duplicate extension shortcut registration: ${key}`);
    }
    this.shortcuts.set(key, { key, options });
  }

  on<Name extends EventName>(event: Name, handler: EventHandler<Name>): void;
  on<Name extends EventName>(event: Name, options: EventSubscriptionOptions, handler: EventHandler<Name>): void;
  on<Name extends EventName>(
    event: Name,
    optionsOrHandler: EventSubscriptionOptions | EventHandler<Name>,
    maybeHandler?: EventHandler<Name>,
  ): void {
    const options = typeof optionsOrHandler === "function" ? {} : optionsOrHandler;
    const handler = typeof optionsOrHandler === "function" ? optionsOrHandler : maybeHandler;
    if (!handler) {
      throw new Error(`Missing handler for extension event ${event}`);
    }
    this.handlers.push({
      event,
      priority: options.priority ?? 0,
      timeoutInSec: options.timeoutInSec,
      order: this.order++,
      handler: handler as EventHandler<EventName>,
    });
  }

  initialize(params: InitializeParams): InitializeResult {
    this.initParams = params;
    return {
      name: this.metadata.name ?? params.extension.id,
      version: this.metadata.version,
      tools: [...this.tools.values()].map(({ registration, inputSchema }) => ({
        name: registration.name,
        description: registration.description,
        inputSchema,
        ...optionalTimeout(registration.timeoutInSec),
      })),
      commands: [...this.commands.values()].map(({ registration, inputSchema }) => ({
        name: registration.name,
        aliases: registration.aliases,
        description: registration.description,
        inputSchema,
        kind: registration.kind,
        ...optionalTimeout(registration.timeoutInSec),
      })),
      shortcuts: [...this.shortcuts.values()].map(({ key, options }) => ({
        key,
        ...(options.description === undefined ? {} : { description: options.description }),
      })),
      subscriptions: this.subscriptions(),
    };
  }

  async executeTool(params: ExecuteToolParams, signal?: AbortSignal): Promise<ToolExecutionResult> {
    const tool = this.tools.get(params.name);
    if (!tool) {
      throw new Error(`Unknown extension tool: ${params.name}`);
    }
    const input = await tool.parseInput(params.input);
    const result = await tool.registration.execute(input as never, createToolContext(this.initParams, params.context, signal));
    if (typeof result === "string") {
      return { content: result };
    }
    return result;
  }

  async executeCommand(params: ExecuteCommandParams, signal?: AbortSignal): Promise<CommandResult> {
    const command = this.commands.get(normalizeCommandName(params.name));
    if (!command) {
      throw new Error(`Unknown extension command: ${params.name}`);
    }

    let input: unknown = params.input ?? {};
    if (command.registration.inputSchema) {
      const parsed = await command.registration.inputSchema.safeParseAsync(input);
      if (!parsed.success) {
        return { action: "pass" };
      }
      input = parsed.data;
    }

    const result = await command.registration.execute(
      input as never,
      createCommandContext(this.initParams, params.context, params.invocation, signal),
    );
    return result ?? { action: "pass" };
  }

  async executeShortcut(params: ExecuteShortcutParams, signal?: AbortSignal): Promise<void> {
    const key = normalizeShortcutKey(params.key);
    const shortcut = this.shortcuts.get(key);
    if (!shortcut) {
      throw new Error(`Unknown extension shortcut: ${key}`);
    }
    await shortcut.options.handler(createShortcutContext(this.initParams, params.context, signal));
  }

  async handleEvent<Name extends EventName>(params: HandleEventParams<Name>, signal?: AbortSignal): Promise<EventResult> {
    const handlers = this.handlers
      .filter((handler) => handler.event === params.event)
      .sort((a, b) => b.priority - a.priority || a.order - b.order);

    const payload = clonePayload(params.payload ?? {}) as Record<string, unknown>;
    const event = Object.assign(payload, {
      id: params.id,
      event: params.event,
    }) as ExtensionEvent<Name>;
    const ctx = createEventContext(this.initParams, params.context as BaseCallContext | undefined, signal);
    const aggregate: EventResult = {};

    for (const entry of handlers) {
      const result = await entry.handler(event as ExtensionEvent<EventName>, ctx);
      if (!result) {
        continue;
      }
      if (result.input !== undefined) {
        aggregate.input = result.input;
        setNestedToolField(event as unknown as Record<string, unknown>, "input", result.input);
      }
      if (result.output !== undefined) {
        aggregate.output = result.output;
        setNestedToolField(event as unknown as Record<string, unknown>, "output", result.output);
      }
      if (result.message !== undefined) {
        aggregate.message = result.message;
      }
      if (result.systemPrompt !== undefined) {
        aggregate.systemPrompt = result.systemPrompt;
      }
      if (result.tools !== undefined) {
        aggregate.tools = mergeToolPatch(aggregate.tools, result.tools);
      }
      if (result.followUpMessages !== undefined) {
        aggregate.followUpMessages = [...(aggregate.followUpMessages ?? []), ...result.followUpMessages];
      }
      if (result.resources !== undefined) {
        aggregate.resources = result.resources;
      }
      if (result.block) {
        aggregate.block = result.block;
        return aggregate;
      }
    }

    return aggregate;
  }

  private subscriptions(): InitializeResult["subscriptions"] {
    const byEvent = new Map<string, { priority: number; timeoutInSec?: number }>();
    for (const handler of this.handlers) {
      const previous = byEvent.get(handler.event);
      if (previous === undefined) {
        byEvent.set(handler.event, { priority: handler.priority, timeoutInSec: handler.timeoutInSec });
        continue;
      }
      byEvent.set(handler.event, {
        priority: Math.max(previous.priority, handler.priority),
        timeoutInSec: mergeTimeoutInSec(previous.timeoutInSec, handler.timeoutInSec),
      });
    }
    return [...byEvent.entries()].map(([event, options]) => ({
      event,
      priority: options.priority,
      ...optionalTimeout(options.timeoutInSec),
    }));
  }
}

function optionalTimeout(timeoutInSec: number | undefined): { timeoutInSec?: number } {
  return timeoutInSec === undefined ? {} : { timeoutInSec };
}

function mergeTimeoutInSec(current: number | undefined, next: number | undefined): number | undefined {
  if (current === 0 || next === 0) {
    return 0;
  }
  if (current === undefined) {
    return next;
  }
  if (next === undefined) {
    return current;
  }
  return Math.max(current, next);
}

export function defineExtension(entrypoint: ExtensionEntrypoint): ExtensionEntrypoint {
  return entrypoint;
}

export async function createExtensionHost(entrypoint: ExtensionEntrypoint): Promise<ExtensionHost> {
  const host = new ExtensionHost();
  await entrypoint(host);
  return host;
}

export function zodSchemaToJsonSchema(schema: AnyZodSchema): Record<string, unknown> {
  const converter = (z as unknown as { toJSONSchema?: (schema: AnyZodSchema, options?: Record<string, unknown>) => unknown })
    .toJSONSchema;
  if (typeof converter === "function") {
    const converted = converter(schema, { target: "draft-7", unrepresentable: "any" });
    if (isRecord(converted)) {
      return converted;
    }
  }
  return { type: "object", additionalProperties: true };
}

function isZodSchema(schema: ToolInputSchema): schema is AnyZodSchema {
  return "_zod" in schema && typeof schema.parseAsync === "function";
}

function clonePayload<T>(payload: T): T {
  return JSON.parse(JSON.stringify(payload)) as T;
}

function setNestedToolField(event: Record<string, unknown>, field: "input" | "output", value: unknown): void {
  const tool = event.tool;
  if (isRecord(tool)) {
    tool[field] = value;
  }
}

function mergeToolPatch(
  current: EventResult["tools"] | undefined,
  next: EventResult["tools"],
): EventResult["tools"] {
  if (!next) {
    return current;
  }
  return {
    disable: [...(current?.disable ?? []), ...(next.disable ?? [])],
    enable: [...(current?.enable ?? []), ...(next.enable ?? [])],
  };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function normalizeCommandName(name: string): string {
  return name.trim().replace(/^\/+/, "");
}

function normalizeShortcutKey(shortcut: string): string {
  const value = shortcut.trim().toLowerCase();
  if (!value) {
    throw new Error("Extension shortcut key is required");
  }
  if (/\s/.test(value)) {
    throw new Error(`Invalid extension shortcut: ${shortcut}`);
  }

  const modifierAliases: Record<string, string> = {
    control: "ctrl",
    option: "alt",
  };
  const parts = value.split("+");
  if (parts.some((part) => !part)) {
    throw new Error(`Invalid extension shortcut: ${shortcut}`);
  }

  const modifiers = new Set<string>();
  let base = "";
  for (const rawPart of parts) {
    const part = modifierAliases[rawPart] ?? rawPart;
    if (part === "ctrl" || part === "alt") {
      if (modifiers.has(part)) {
        throw new Error(`Invalid extension shortcut: ${shortcut}`);
      }
      modifiers.add(part);
      continue;
    }
    if (part === "shift") {
      throw new Error(`Unsupported extension shortcut modifier: ${rawPart}`);
    }
    if (part === "cmd" || part === "command" || part === "meta" || part === "super") {
      throw new Error(`Unsupported extension shortcut modifier: ${rawPart}`);
    }
    if (base) {
      throw new Error(`Invalid extension shortcut: ${shortcut}`);
    }
    base = part;
  }
  if (!base) {
    throw new Error(`Invalid extension shortcut: ${shortcut}`);
  }

  const ctrl = modifiers.has("ctrl");
  const alt = modifiers.has("alt");
  const functionKey = /^f(?:[1-9]|1[0-2])$/.test(base);
  if (functionKey) {
    if (ctrl || alt) {
      throw new Error(`Unsupported extension shortcut: ${shortcut}; function keys must not use modifiers`);
    }
    return base;
  }
  if (!ctrl && !alt) {
    throw new Error(
      `Unsupported extension shortcut: ${shortcut}; use ctrl+letter, alt+letter-or-digit, ctrl+alt+letter, or f1 through f12`,
    );
  }

  const asciiLetter = /^[a-z]$/.test(base);
  const asciiDigit = /^[0-9]$/.test(base);
  if (!asciiLetter && !(alt && !ctrl && asciiDigit)) {
    throw new Error(`Unsupported extension shortcut key: ${shortcut}`);
  }
  if (ctrl && (base === "i" || base === "m")) {
    const terminalKey = base === "i" ? "tab" : "enter";
    throw new Error(`Unsupported extension shortcut: ${shortcut}; terminals report ctrl+${base} as ${terminalKey}`);
  }

  const orderedModifiers = ["ctrl", "alt"].filter((modifier) => modifiers.has(modifier));
  return [...orderedModifiers, base].join("+");
}
