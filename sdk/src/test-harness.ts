import { createExtensionHost } from "./api.js";
import { runWithHostRPCClient, type HostRPCClient } from "./context.js";
import type {
  CommandResult,
  EventName,
  EventResult,
  ExecuteCommandParams,
  ExecuteShortcutParams,
  ExecuteToolParams,
  ExtensionEntrypoint,
  HandleEventParams,
  InitializeParams,
  InitializeResult,
  ShortcutResult,
  ToolExecutionResult,
} from "./types.js";

export interface ExtensionTestHarness {
  initialize(params?: Partial<InitializeParams>): InitializeResult;
  executeTool(params: ExecuteToolParams): Promise<ToolExecutionResult>;
  executeCommand(params: ExecuteCommandParams): Promise<CommandResult>;
  executeShortcut(params: ExecuteShortcutParams): Promise<ShortcutResult | void>;
  handleEvent<Name extends EventName>(params: HandleEventParams<Name>): Promise<EventResult>;
}

export async function createTestHarness(
  entrypoint: ExtensionEntrypoint,
  hostRPCClient?: HostRPCClient,
): Promise<ExtensionTestHarness> {
  const host = await createExtensionHost(entrypoint);
  let initialized = false;
  const defaultInit: InitializeParams = {
    protocolVersion: "2026-05-30",
    kodelet: { version: "test" },
    extension: {
      id: "test-extension",
      cwd: process.cwd(),
      dataDir: "",
      config: {},
    },
    capabilities: { toolUpdates: true, shortcuts: { submit: true } },
  };

  const ensureInitialized = () => {
    if (!initialized) {
      host.initialize(defaultInit);
      initialized = true;
    }
  };

  return {
    initialize(params) {
      initialized = true;
      return host.initialize({
        ...defaultInit,
        ...params,
        extension: {
          ...defaultInit.extension,
          ...params?.extension,
        },
      });
    },
    async executeTool(params) {
      ensureInitialized();
      return await runWithHostRPCClient(hostRPCClient, () => host.executeTool(params));
    },
    async executeCommand(params) {
      ensureInitialized();
      return await runWithHostRPCClient(hostRPCClient, () => host.executeCommand(params));
    },
    async executeShortcut(params) {
      ensureInitialized();
      return await runWithHostRPCClient(hostRPCClient, () => host.executeShortcut(params));
    },
    async handleEvent(params) {
      ensureInitialized();
      return await runWithHostRPCClient(hostRPCClient, () => host.handleEvent(params));
    },
  };
}
