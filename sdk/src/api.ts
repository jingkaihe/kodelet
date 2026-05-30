import { z } from "zod";

import { createCommandContext, createEventContext, createToolContext } from "./context.js";
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
  ExecuteToolParams,
  ExtensionAPI,
  ExtensionEntrypoint,
  ExtensionEvent,
  ExtensionMetadata,
  HandleEventParams,
  InitializeParams,
  InitializeResult,
  ToolExecutionResult,
  ToolRegistration,
} from "./types.js";

interface RegisteredTool {
  registration: ToolRegistration<AnyZodSchema>;
  inputSchema: Record<string, unknown>;
}

interface RegisteredCommand {
  registration: CommandRegistration<AnyZodSchema | undefined>;
  inputSchema?: Record<string, unknown>;
}

interface RegisteredEventHandler {
  event: EventName;
  priority: number;
  order: number;
  handler: EventHandler<EventName>;
}

export class ExtensionHost implements ExtensionAPI {
  private metadata: ExtensionMetadata = {};
  private tools = new Map<string, RegisteredTool>();
  private commands = new Map<string, RegisteredCommand>();
  private handlers: RegisteredEventHandler[] = [];
  private order = 0;
  private initParams?: InitializeParams;

  setMetadata(metadata: ExtensionMetadata): void {
    this.metadata = { ...this.metadata, ...metadata };
  }

  registerTool<Schema extends AnyZodSchema>(registration: ToolRegistration<Schema>): void {
    if (this.tools.has(registration.name)) {
      throw new Error(`Duplicate extension tool registration: ${registration.name}`);
    }
    this.tools.set(registration.name, {
      registration: registration as ToolRegistration<AnyZodSchema>,
      inputSchema: zodSchemaToJsonSchema(registration.inputSchema),
    });
  }

  registerCommand<Schema extends AnyZodSchema | undefined = undefined>(registration: CommandRegistration<Schema>): void {
    if (this.commands.has(registration.name)) {
      throw new Error(`Duplicate extension command registration: ${registration.name}`);
    }
    this.commands.set(registration.name, {
      registration: registration as CommandRegistration<AnyZodSchema | undefined>,
      inputSchema: registration.inputSchema ? zodSchemaToJsonSchema(registration.inputSchema) : undefined,
    });
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
      })),
      commands: [...this.commands.values()].map(({ registration, inputSchema }) => ({
        name: registration.name,
        aliases: registration.aliases,
        description: registration.description,
        inputSchema,
        kind: registration.kind,
      })),
      subscriptions: this.subscriptions(),
    };
  }

  async executeTool(params: ExecuteToolParams): Promise<ToolExecutionResult> {
    const tool = this.tools.get(params.name);
    if (!tool) {
      throw new Error(`Unknown extension tool: ${params.name}`);
    }
    const input = await tool.registration.inputSchema.parseAsync(params.input);
    const result = await tool.registration.execute(input, createToolContext(this.initParams, params.context));
    if (typeof result === "string") {
      return { content: result };
    }
    return result;
  }

  async executeCommand(params: ExecuteCommandParams): Promise<CommandResult> {
    const command = this.commands.get(params.name);
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
      createCommandContext(this.initParams, params.context, params.invocation),
    );
    return result ?? { action: "pass" };
  }

  async handleEvent<Name extends EventName>(params: HandleEventParams<Name>): Promise<EventResult> {
    const handlers = this.handlers
      .filter((handler) => handler.event === params.event)
      .sort((a, b) => b.priority - a.priority || a.order - b.order);

    const payload = clonePayload(params.payload ?? {}) as Record<string, unknown>;
    const event = Object.assign(payload, {
      id: params.id,
      event: params.event,
    }) as ExtensionEvent<Name>;
    const ctx = createEventContext(this.initParams, params.context as BaseCallContext | undefined);
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
    const byEvent = new Map<string, number>();
    for (const handler of this.handlers) {
      const previous = byEvent.get(handler.event);
      if (previous === undefined || handler.priority > previous) {
        byEvent.set(handler.event, handler.priority);
      }
    }
    return [...byEvent.entries()].map(([event, priority]) => ({ event, priority }));
  }
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

function clonePayload<T>(payload: T): T {
  return JSON.parse(JSON.stringify(payload)) as T;
}

function setNestedToolField(event: Record<string, unknown>, field: "input" | "output", value: unknown): void {
  const tool = event.tool;
  if (isRecord(tool)) {
    tool[field] = value;
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
