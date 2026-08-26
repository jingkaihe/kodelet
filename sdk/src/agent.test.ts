import assert from "node:assert/strict";
import { spawn as spawnProcess } from "node:child_process";
import { EventEmitter } from "node:events";
import { mkdtemp, readFile, readdir, stat } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { Readable, Writable } from "node:stream";
import test from "node:test";

import { Client, ConversationForkUnavailableError, Profile, defineExtension, z } from "./index.js";
import type { SpawnFunction, SpawnedProcess, ToolUpdateData } from "./agent.js";

interface JsonRPCRequest {
  jsonrpc?: "2.0";
  id?: number | string | null;
  parentId?: number | string;
  method?: string;
  params?: unknown;
  result?: unknown;
  error?: { message?: string };
}

interface FakeACPProcessOptions {
  sessionId?: string;
  onPrompt?(request: JsonRPCRequest, process: FakeACPProcess): Promise<void> | void;
}

class FakeACPProcess extends EventEmitter implements SpawnedProcess {
  stdin: Writable;
  stdout = new Readable({ read() {} });
  stderr = new Readable({ read() {} });
  requests: JsonRPCRequest[] = [];
  private inputBuffer = "";
  private closed = false;

  constructor(private readonly options: FakeACPProcessOptions = {}) {
    super();
    this.stdin = new Writable({
      write: (chunk, _encoding, callback) => {
        try {
          this.handleInput(Buffer.isBuffer(chunk) ? chunk.toString("utf8") : String(chunk));
          callback();
        } catch (error) {
          callback(error instanceof Error ? error : new Error(String(error)));
        }
      },
    });
  }

  kill(): boolean {
    if (!this.closed) {
      this.closed = true;
      setImmediate(() => {
        this.stdout.push(null);
        this.stderr.push(null);
        this.emit("close", 0, null);
      });
    }
    return true;
  }

  notify(method: string, params?: unknown): void {
    this.write({ jsonrpc: "2.0", method, params });
  }

  private handleInput(chunk: string): void {
    this.inputBuffer += chunk;
    while (true) {
      const index = this.inputBuffer.indexOf("\n");
      if (index === -1) {
        return;
      }
      const line = this.inputBuffer.slice(0, index).replace(/\r$/, "");
      this.inputBuffer = this.inputBuffer.slice(index + 1);
      this.handleLine(line);
    }
  }

  private handleLine(line: string): void {
    if (!line.trim()) {
      return;
    }
    const request = JSON.parse(line) as JsonRPCRequest;
    if (!request.method || request.id === undefined || request.id === null) {
      return;
    }
    this.requests.push(request);
    this.handleRequest(request);
  }

  private handleRequest(request: JsonRPCRequest): void {
    switch (request.method) {
      case "initialize":
        this.respond(request.id, { protocolVersion: 1, agentCapabilities: {}, authMethods: [] });
        return;
      case "session/new":
        this.respond(request.id, { sessionId: this.options.sessionId ?? "conv-1" });
        return;
      case "session/load":
        this.respond(request.id, {});
        return;
      case "session/prompt":
        void Promise.resolve(this.options.onPrompt?.(request, this)).then(
          () => this.respond(request.id, { stopReason: "end_turn" }),
          (error) => this.respondError(request.id, error instanceof Error ? error.message : String(error)),
        );
        return;
      default:
        this.respondError(request.id, `Unexpected method: ${request.method}`);
    }
  }

  private respond(id: JsonRPCRequest["id"], result: unknown): void {
    this.write({ jsonrpc: "2.0", id, result });
  }

  private respondError(id: JsonRPCRequest["id"], message: string): void {
    this.write({ jsonrpc: "2.0", id, error: { code: -32601, message } });
  }

  private write(message: Record<string, unknown>): void {
    this.stdout.push(`${JSON.stringify(message)}\n`);
  }
}

class FailingSpawnProcess extends EventEmitter implements SpawnedProcess {
  stdin = new Writable({
    write(_chunk, _encoding, callback) {
      callback();
    },
  });
  stdout = new Readable({ read() {} });
  stderr = new Readable({ read() {} });

  constructor(error: Error) {
    super();
    setImmediate(() => this.emit("error", error));
  }

  kill(): boolean {
    return true;
  }
}

test("Profile maps early profiler spelling and nested OpenAI config to launch config", () => {
  const profile = new Profile({
    name: "openai",
    profiler: "openai",
    model: "gpt-5.5",
    max_tokens: 128000,
    reasoning_effort: "xhigh",
    weak_model: "gpt-5.4-mini",
    enable_fs_search_tools: true,
    openai: {
      api_mode: "responses",
      platform: "codex",
      service_tier: "fast",
    },
  });

  const launch = profile.toLaunchConfig();
  assert.deepEqual(launch.args, []);
  assert.deepEqual(launch.config, {
    name: "openai",
    provider: "openai",
    model: "gpt-5.5",
    max_tokens: 128000,
    reasoning_effort: "xhigh",
    weak_model: "gpt-5.4-mini",
    enable_fs_search_tools: true,
    openai: {
      api_mode: "responses",
      platform: "codex",
      service_tier: "fast",
    },
  });
});

test("Session writes inline profile to temporary override config", async () => {
  const calls: Array<{ args: string[]; env?: NodeJS.ProcessEnv }> = [];
  const spawn: SpawnFunction = (_command, args, options) => {
    calls.push({ args, env: options.env });
    return new FakeACPProcess({ sessionId: "conv-profile" });
  };

  const client = new Client({ spawn });
  const session = await client.createSession({
    profile: {
      name: "openai",
      provider: "openai",
      model: "gpt-5.5",
      allowed_tools: ["sdk_echo"],
      openai: {
        api_mode: "responses",
        service_tier: "fast",
      },
    },
  });

  assert.equal(calls[0]?.env?.KODELET_CONFIG_FILE_MODE, "isolated");
  assert.equal(calls[0]?.env?.KODELET_MODEL, undefined);
  const configPath = calls[0]?.env?.KODELET_CONFIG_FILE;
  assert.ok(configPath);
  const config = JSON.parse(await readFile(configPath, "utf8")) as Record<string, unknown>;
  assert.deepEqual(calls[0]?.args, ["acp"]);
  assert.deepEqual(config, {
    profile: "default",
    name: "openai",
    provider: "openai",
    model: "gpt-5.5",
    allowed_tools: ["sdk_echo"],
    openai: {
      api_mode: "responses",
      service_tier: "fast",
    },
  });

  await session.close();
  await assert.rejects(() => stat(configPath));
});

test("Inline profile isolation filters ambient Kodelet environment variables", async () => {
  const calls: Array<{ env?: NodeJS.ProcessEnv }> = [];
  const spawn: SpawnFunction = (_command, _args, options) => {
    calls.push({ env: options.env });
    return new FakeACPProcess({ sessionId: "conv-env" });
  };

  const original = process.env.KODELET_MODEL;
  process.env.KODELET_MODEL = "ambient-model";
  try {
    const client = new Client({ spawn, env: { KODELET_PROVIDER: "explicit-provider" } });
    await client.createSession({ profile: { provider: "openai", model: "inline-model" } });
    assert.equal(calls[0]?.env?.KODELET_MODEL, undefined);
    assert.equal(calls[0]?.env?.KODELET_PROVIDER, "explicit-provider");
    assert.equal(calls[0]?.env?.KODELET_CONFIG_FILE_MODE, "isolated");
    await client.close();
  } finally {
    if (original === undefined) {
      delete process.env.KODELET_MODEL;
    } else {
      process.env.KODELET_MODEL = original;
    }
  }
});

test("Session runs kodelet ACP JSON-RPC and emits typed stream events", async () => {
  const calls: Array<{ command: string; args: string[]; env?: NodeJS.ProcessEnv; cwd?: string }> = [];
  const processes: FakeACPProcess[] = [];
  const spawn: SpawnFunction = (command, args, options) => {
    calls.push({ command, args, env: options.env, cwd: options.cwd as string | undefined });
    const process = new FakeACPProcess({
      onPrompt(_request, child) {
        child.notify("session/update", {
          sessionId: "conv-1",
          update: { sessionUpdate: "agent_thought_chunk", content: { type: "text", text: "checking" } },
        });
        child.notify("session/update", {
          sessionId: "conv-1",
          update: { sessionUpdate: "agent_message_chunk", content: { type: "text", text: "forty" } },
        });
        child.notify("session/update", {
          sessionId: "conv-1",
          update: { sessionUpdate: "agent_message_chunk", content: { type: "text", text: " two" } },
        });
        child.notify("session/update", {
          sessionId: "conv-1",
          update: {
            sessionUpdate: "tool_call",
            toolCallId: "call-1",
            toolName: "file_read",
            title: "Read: /tmp/example.txt",
            kind: "read",
            rawInput: { file_path: "/tmp/example.txt" },
          },
        });
        child.notify("session/update", {
          sessionId: "conv-1",
          update: {
            sessionUpdate: "tool_call_update",
            toolCallId: "call-1",
            status: "in_progress",
          },
        });
        child.notify("session/update", {
          sessionId: "conv-1",
          update: {
            sessionUpdate: "tool_call_update",
            toolCallId: "call-1",
            status: "in_progress",
            content: [
              {
                type: "content",
                content: { type: "text", text: "partial file contents" },
              },
            ],
          },
        });
        child.notify("session/update", {
          sessionId: "conv-1",
          update: {
            sessionUpdate: "tool_call_update",
            toolCallId: "call-1",
            status: "in_progress",
            content: [
              {
                type: "content",
                content: { type: "text", text: "complete partial file contents" },
              },
            ],
          },
        });
        child.notify("session/update", {
          sessionId: "conv-1",
          update: {
            sessionUpdate: "tool_call_update",
            toolCallId: "call-1",
            status: "completed",
            content: [
              {
                type: "content",
                content: {
                  type: "resource",
                  resource: {
                    uri: "file:///tmp/example.txt",
                    mimeType: "text/plain",
                    text: "1 | hello",
                  },
                },
              },
            ],
          },
        });
      },
    });
    processes.push(process);
    return process;
  };

  const client = new Client({ command: "kodelet-test", cwd: "/workspace", spawn });
  const session = await client.createSession({ streaming: true, profile: "work", maxTurns: 2 });
  const deltas: string[] = [];
  const thoughts: string[] = [];
  session.on("assistant.message_delta", (event) => deltas.push(event.data.deltaContent));
  session.on("assistant.thinking_delta", (event) => thoughts.push(event.data.deltaContent));
  let toolName = "";
  const toolUpdates: string[] = [];
  let toolResult = "";
  session.on("tool.call", (event) => {
    toolName = event.data.toolName;
  });
  session.on("tool.result", (event) => {
    toolResult = event.data.result;
  });
  session.on("tool.update", (event) => {
    toolUpdates.push(event.data.result);
  });

  const response = await session.runAndWait({ message: "meaning?", images: ["diagram.png"] });

  assert.equal(response.content, "forty two");
  assert.equal(response.conversationId, "conv-1");
  assert.deepEqual(deltas, ["forty", " two"]);
  assert.deepEqual(thoughts, ["checking"]);
  assert.equal(toolName, "file_read");
  assert.deepEqual(toolUpdates, ["partial file contents", "complete partial file contents"]);
  assert.equal(toolResult, "1 | hello");
  const recordedToolUpdates = response.events.filter((event) => event.type === "tool.update");
  assert.equal(recordedToolUpdates.length, 1);
  assert.equal((recordedToolUpdates[0]?.data as ToolUpdateData).result, "complete partial file contents");
  assert.equal(response.stopReason, "end_turn");
  assert.equal(session.id, "conv-1");
  assert.equal(calls[0]?.command, "kodelet-test");
  assert.equal(calls[0]?.cwd, "/workspace");
  assert.deepEqual(calls[0]?.args, ["--profile", "work", "acp", "--max-turns", "2"]);
  assert.equal(calls[0]?.env?.KODELET_CONFIG_FILE, undefined);
  assert.deepEqual(processes[0]?.requests.map((request) => request.method), ["initialize", "session/new", "session/prompt"]);
  assert.deepEqual((processes[0]?.requests[1]?.params as { cwd: string }).cwd, "/workspace");
  assert.deepEqual((processes[0]?.requests[2]?.params as { sessionId: string; prompt: unknown[] }).prompt, [
    { type: "text", text: "meaning?" },
    { type: "image", uri: "diagram.png" },
  ]);

  await client.close();
});

test("Client rejects child spawn failures without crashing the process", async () => {
  const spawn: SpawnFunction = () => new FailingSpawnProcess(new Error("spawn failed"));
  const client = new Client({ spawn });

  await assert.rejects(() => client.createSession(), /spawn failed/);
});

test("Session rejects already-aborted run signals without starting a run", async () => {
  const processes: FakeACPProcess[] = [];
  const spawn: SpawnFunction = () => {
    const process = new FakeACPProcess();
    processes.push(process);
    return process;
  };
  const client = new Client({ spawn });
  const session = await client.createSession();
  const emittedEvents: string[] = [];
  session.on("event", (event) => emittedEvents.push(event.type));

  const controller = new AbortController();
  const abortReason = new Error("cancelled before run");
  controller.abort(abortReason);

  await assert.rejects(
    () => session.runAndWait({ message: "hello", signal: controller.signal }),
    (error) => error === abortReason,
  );

  assert.deepEqual(emittedEvents, []);
  assert.deepEqual(processes[0]?.requests.map((request) => request.method), ["initialize", "session/new"]);

  await client.close();
});

test("Session exposes in-process extensions through a temporary JSON-RPC bridge", async () => {
  const workspace = await mkdtemp(path.join(os.tmpdir(), "kodelet-agent-sdk-test-"));
  const calls: Array<{ args: string[]; env?: NodeJS.ProcessEnv }> = [];
  const spawn: SpawnFunction = (_command, args, options) => {
    calls.push({ args, env: options.env });
    return new FakeACPProcess({
      sessionId: "conv-ext",
      onPrompt(_request, child) {
        child.notify("session/update", {
          sessionId: "conv-ext",
          update: { sessionUpdate: "agent_message_chunk", content: { type: "text", text: "done" } },
        });
      },
    });
  };

  const extension = defineExtension((ext) => {
    ext.setMetadata({ name: "workspace" });
    ext.registerTool({
      name: "ask_user_question",
      description: "Ask a question",
      inputSchema: z.object({ question: z.string(), options: z.array(z.string()) }),
      async execute(input, ctx) {
        const selected = await ctx.ui.select({ title: input.question, options: input.options });
        return selected ?? "dismissed";
      },
    });
  });

  const client = new Client({ cwd: workspace, spawn });
  const session = await client.createSession({
    extensions: [extension],
    ui: {
      select(request) {
        return request.options[0];
      },
    },
  });
  await session.runAndWait({ message: "hello" });

  const env = calls[0]?.env ?? {};
  assert.equal(env.KODELET_CONFIG_FILE_MODE, "merge");
  assert.ok(env.KODELET_CONFIG_FILE);
  const config = JSON.parse(await readFile(env.KODELET_CONFIG_FILE as string, "utf8")) as {
    extensions?: { enabled?: boolean; local_dir?: string; allow?: string[] };
  };
  assert.equal(config.extensions?.enabled, true);
  const extensionRoot = config.extensions?.local_dir;
  assert.ok(extensionRoot);
  assert.deepEqual(config.extensions?.allow, [extensionRoot]);
  const info = await stat(extensionRoot);
  assert.equal(info.isDirectory(), true);
  const extensionExecutables = (await readdir(extensionRoot)).filter((entry) => entry.startsWith("kodelet-extension-"));
  assert.equal(extensionExecutables.length, 1);
  assert.match(extensionExecutables[0], /^kodelet-extension-sdk-[0-9a-f]{16}-1$/);
  assert.notEqual(extensionExecutables[0], "kodelet-extension-sdk-1");
  assert.deepEqual(calls[0]?.args, ["acp"]);

  await client.close();
  await assert.rejects(() => stat(env.KODELET_CONFIG_FILE as string));
  await assert.rejects(() => stat(extensionRoot));
});

test("Session can expose in-process extensions over a TCP bridge", { timeout: 5000 }, async (t) => {
  const workspace = await mkdtemp(path.join(os.tmpdir(), "kodelet-agent-sdk-tcp-test-"));
  const calls: Array<{ env?: NodeJS.ProcessEnv }> = [];
  const spawn: SpawnFunction = (_command, _args, options) => {
    calls.push({ env: options.env });
    return new FakeACPProcess({ sessionId: "conv-tcp" });
  };

  const selectedValues: string[] = [];
  let resizeHandledResolve!: () => void;
  const resizeHandled = new Promise<void>((resolve) => {
    resizeHandledResolve = resolve;
  });
  let started = 0;
  let releaseBoth!: () => void;
  const bothStarted = new Promise<void>((resolve) => {
    releaseBoth = resolve;
  });
  const extension = defineExtension((ext) => {
    ext.setMetadata({ name: "workspace" });
    ext.registerTool({
      name: "ask_user_question",
      description: "Ask a question",
      inputSchema: z.object({ question: z.string(), options: z.array(z.string()) }),
      async execute(input, ctx) {
        started += 1;
        if (started === 2) releaseBoth();
        await bothStarted;
        await ctx.update(`Waiting for ${input.question}`, { step: 1 });
        await ctx.ui.setWidget("selection", [input.question]);
        const selected = await ctx.ui.select({ title: input.question, options: input.options });
        selectedValues.push(selected ?? "");
        return selected ?? "dismissed";
      },
    });
    ext.registerTool({
      name: "fork_context",
      description: "Fork the current conversation",
      inputSchema: z.object({}),
      async execute(_input, ctx) {
        try {
          await ctx.forkConversation({ name: "Delegated task" });
          return "unexpected";
        } catch (error) {
          if (error instanceof ConversationForkUnavailableError) {
            return "unavailable";
          }
          throw error;
        }
      },
    });
    ext.registerCommand({
      name: "surface",
      description: "Open a persistent surface",
      async execute(_input, ctx) {
        const surface = await ctx.ui.openSurface({ id: "game", initialLines: ["loading"] });
        surface.onResize((event) => {
          void ctx.ui.appendTranscript({ title: "Resized", message: String(event.width) }).then(resizeHandledResolve);
        });
        return { action: "respond", response: "opened" };
      },
    });
    ext.registerCommand({
      name: "once",
      description: "Send one detached persistent request",
      execute(_input, ctx) {
        void ctx.ui.appendTranscript("once").catch(() => undefined);
        return { action: "respond", response: "done" };
      },
    });
    ext.registerShortcut("ctrl+alt+r", {
      description: "Run review command",
      handler() {
        return { action: "submit", message: "/review" };
      },
    });
  });

  const client = new Client({ cwd: workspace, spawn });
  const session = await client.createSession({
    extensions: [extension],
    extensionTransport: "tcp",
    ui: {
      select(request) {
        return request.options[1];
      },
    },
  });

  const env = calls[0]?.env ?? {};
  const config = JSON.parse(await readFile(env.KODELET_CONFIG_FILE as string, "utf8")) as {
    extensions?: { local_dir?: string };
  };
  const extensionRoot = config.extensions?.local_dir;
  assert.ok(extensionRoot);
  const executable = path.join(
    extensionRoot,
    (await readdir(extensionRoot)).find((entry) => entry.startsWith("kodelet-extension-")) ?? "",
  );
  const executableText = await readFile(executable, "utf8");
  assert.match(executableText, /"transport":"tcp"/);
  assert.match(executableText, /"host":"127\.0\.0\.1"/);
  assert.match(executableText, /"port":\d+/);

  const child = spawnProcess(executable, { stdio: ["pipe", "pipe", "pipe"] });
  t.after(() => child.kill());
  assert.ok(child.stdin);
  assert.ok(child.stdout);
  const bridge = new BridgeTestClient(child.stdout, child.stdin);

  const init = await bridge.call("extension.initialize", {
    protocolVersion: "2026-05-30",
    kodelet: { version: "test" },
    extension: { id: "workspace", cwd: workspace, dataDir: "" },
    capabilities: {
      conversations: { fork: true },
      toolUpdates: true,
      shortcuts: { submit: true },
      ui: { widgets: true, surfaces: true, transcript: true },
    },
  });
  assert.equal((init as { name?: string }).name, "workspace");
  assert.deepEqual((init as { shortcuts?: unknown }).shortcuts, [
    { key: "ctrl+alt+r", description: "Run review command" },
  ]);

  bridge.setRespondToHostRequests(false);
  const forkCall = bridge.beginCall("extension.tool.execute", {
    name: "fork_context",
    input: {},
    context: { cwd: workspace, conversationId: "conversation-fork" },
  });
  await bridge.waitForHostRequests(1);
  const forkRequest = bridge.hostRequests[0];
  assert.equal(forkRequest?.method, "kodelet.conversation.fork");
  assert.equal(forkRequest?.parentId, forkCall.id);
  assert.deepEqual(forkRequest?.params, { name: "Delegated task" });
  bridge.respondHostError(forkRequest!.id!, { code: -32004, message: "fork unavailable" });
  assert.deepEqual(await forkCall.response, { content: "unavailable" });
  bridge.setRespondToHostRequests(true);
  bridge.hostRequests.length = 0;

  const shortcutResult = await bridge.call("extension.shortcut.execute", {
    key: "ctrl+alt+r",
    context: { cwd: workspace, uiScopeId: "conversation-shortcut" },
  });
  assert.deepEqual(shortcutResult, { action: "submit", message: "/review" });

  const first = bridge.beginCall("extension.tool.execute", {
    name: "ask_user_question",
    input: { question: "First", options: ["A", "B"] },
    context: { cwd: workspace, uiScopeId: "conversation-first" },
  });
  const second = bridge.beginCall("extension.tool.execute", {
    name: "ask_user_question",
    input: { question: "Second", options: ["A", "B"] },
    context: { cwd: workspace, uiScopeId: "conversation-second" },
  });
  assert.deepEqual(await Promise.all([first.response, second.response]), [{ content: "B" }, { content: "B" }]);
  assert.deepEqual(selectedValues.sort(), ["B", "B"]);

  assert.equal(bridge.hostRequests.length, 4);
  const expectedParents = new Map([
    ["First", first.id],
    ["Second", second.id],
  ]);
  for (const request of bridge.hostRequests) {
    let label: string;
    if (request.method === "kodelet.tool.update") {
      label = (request.params as { content: string }).content.replace("Waiting for ", "");
    } else {
      assert.equal(request.method, "kodelet.ui.widget.set");
      label = (request.params as { frame: { lines: string[] } }).frame.lines[0];
    }
    assert.equal(request.parentId, expectedParents.get(label));
  }
  assert.deepEqual(
    bridge.hostRequests
      .filter((request) => request.method === "kodelet.ui.widget.set")
      .map((request) => (request.params as { frame: { sequence: number } }).frame.sequence)
      .sort((left, right) => left - right),
    [1, 1],
  );
  assert.deepEqual(
    bridge.hostRequests
      .filter((request) => request.method === "kodelet.ui.widget.set")
      .map((request) => (request.params as { scopeId: string }).scopeId)
      .sort(),
    ["conversation-first", "conversation-second"],
  );

  const surfaceCall = bridge.beginCall("extension.command.execute", {
    name: "surface",
    input: {},
    invocation: { raw: "/surface", commandName: "surface", args: [], flags: {} },
    context: { cwd: workspace, uiScopeId: "conversation-surface" },
  });
  assert.deepEqual(await surfaceCall.response, { action: "respond", response: "opened" });
  const openRequest = bridge.hostRequests.find((request) => request.method === "kodelet.ui.surface.open");
  assert.equal(openRequest?.parentId, surfaceCall.id);

  bridge.notify("extension.ui.surface.resize", { scopeId: "conversation-surface", id: "game", sequence: 1, width: 60, height: 18 });
  await resizeHandled;
  const transcriptRequest = bridge.hostRequests.find((request) => request.method === "kodelet.ui.transcript.append");
  assert.equal(transcriptRequest?.parentId, undefined);
  assert.deepEqual(transcriptRequest?.params, { scopeId: "conversation-surface", title: "Resized", message: "60" });

  const requestCountBeforeDetached = bridge.hostRequests.length;
  bridge.setRespondToHostRequests(false);
  const detached = bridge.beginCall("extension.command.execute", {
    name: "once",
    input: {},
    invocation: { raw: "/once", commandName: "once", args: [], flags: {} },
    context: { cwd: workspace, uiScopeId: "conversation-once" },
  });
  assert.deepEqual(await detached.response, { action: "respond", response: "done" });
  await new Promise((resolve) => setTimeout(resolve, 25));
  const detachedRequests = bridge.hostRequests.slice(requestCountBeforeDetached);
  assert.equal(detachedRequests.length, 1);
  assert.equal(detachedRequests[0]?.parentId, detached.id);
  assert.deepEqual(detachedRequests[0]?.params, { scopeId: "conversation-once", message: "once" });

  await client.close();
});

test("In-process extension bridge cancellation is connection-scoped", async (t) => {
  const workspace = await mkdtemp(path.join(os.tmpdir(), "kodelet-agent-sdk-cancel-test-"));
  const calls: Array<{ env?: NodeJS.ProcessEnv }> = [];
  const spawn: SpawnFunction = (_command, _args, options) => {
    calls.push({ env: options.env });
    return new FakeACPProcess({ sessionId: "conv-cancel" });
  };

  let cancelStartedResolve!: () => void;
  let cancelAbortedResolve!: () => void;
  let disconnectStartedResolve!: () => void;
  let disconnectAbortedResolve!: () => void;
  let uiStartedResolve!: () => void;
  let uiAbortedResolve!: () => void;
  const notifications: string[] = [];
  const cancelStarted = new Promise<void>((resolve) => { cancelStartedResolve = resolve; });
  const cancelAborted = new Promise<void>((resolve) => { cancelAbortedResolve = resolve; });
  const disconnectStarted = new Promise<void>((resolve) => { disconnectStartedResolve = resolve; });
  const disconnectAborted = new Promise<void>((resolve) => { disconnectAbortedResolve = resolve; });
  const uiStarted = new Promise<void>((resolve) => { uiStartedResolve = resolve; });
  const uiAborted = new Promise<void>((resolve) => { uiAbortedResolve = resolve; });

  const extension = defineExtension((ext) => {
    ext.setMetadata({ name: "cancellable" });
    ext.registerTool({
      name: "wait_for_cancel",
      description: "Wait for cancellation",
      inputSchema: z.object({ mode: z.enum(["cancel", "disconnect", "detached", "finish_abort", "quick", "ui"]) }),
      async execute(input, ctx) {
        if (input.mode === "quick") {
          return "quick result";
        }
        if (input.mode === "detached") {
          setTimeout(() => {
            void ctx.ui.notify({ message: "late notification" }).catch(() => undefined);
          }, 20);
          return "detached result";
        }
        if (input.mode === "ui") {
          await ctx.ui.notify({ message: "blocking notification" });
          return "ui result";
        }
        if (input.mode === "finish_abort") {
          ctx.signal.addEventListener("abort", () => {
            void ctx.ui.notify({ message: "completion notification" }).catch(() => undefined);
          }, { once: true });
          return "finish result";
        }
        if (input.mode === "cancel") {
          cancelStartedResolve();
        } else {
          disconnectStartedResolve();
        }
        await new Promise<void>((resolve) => {
          if (ctx.signal.aborted) {
            resolve();
            return;
          }
          ctx.signal.addEventListener("abort", () => resolve(), { once: true });
        });
        if (input.mode === "cancel") {
          cancelAbortedResolve();
        } else {
          disconnectAbortedResolve();
        }
        await ctx.update(`stale ${input.mode}`);
        return `late ${input.mode}`;
      },
    });
  });

  const client = new Client({ cwd: workspace, spawn });
  await client.createSession({
    extensions: [extension],
    extensionTransport: "tcp",
    ui: {
      async notify(request, signal) {
        if (request.message !== "blocking notification") {
          notifications.push(request.message);
          return;
        }
        uiStartedResolve();
        await new Promise<void>((resolve) => {
          if (signal?.aborted) {
            resolve();
            return;
          }
          signal?.addEventListener("abort", () => resolve(), { once: true });
        });
        uiAbortedResolve();
      },
    },
  });
  const config = JSON.parse(await readFile(calls[0]?.env?.KODELET_CONFIG_FILE as string, "utf8")) as {
    extensions?: { local_dir?: string };
  };
  const extensionRoot = config.extensions?.local_dir;
  assert.ok(extensionRoot);
  const executable = path.join(
    extensionRoot,
    (await readdir(extensionRoot)).find((entry) => entry.startsWith("kodelet-extension-")) ?? "",
  );

  const child = spawnProcess(executable, { stdio: ["pipe", "pipe", "pipe"] });
  assert.ok(child.stdin);
  assert.ok(child.stdout);
  const bridge = new BridgeTestClient(child.stdout, child.stdin);
  await bridge.call("extension.initialize", {
    extension: { id: "cancellable", cwd: workspace },
    capabilities: { toolUpdates: true },
  });

  const detached = await bridge.call("extension.tool.execute", {
    name: "wait_for_cancel",
    input: { mode: "detached" },
  });
  assert.deepEqual(detached, { content: "detached result" });
  await new Promise((resolve) => setTimeout(resolve, 50));
  assert.deepEqual(notifications, []);

  const finishAbort = await bridge.call("extension.tool.execute", {
    name: "wait_for_cancel",
    input: { mode: "finish_abort" },
  });
  assert.deepEqual(finishAbort, { content: "finish result" });
  await new Promise((resolve) => setTimeout(resolve, 20));
  assert.deepEqual(notifications, []);

  const uiCall = bridge.beginCall("extension.tool.execute", {
    name: "wait_for_cancel",
    input: { mode: "ui" },
  });
  void uiCall.response.catch(() => undefined);
  await uiStarted;
  bridge.notify("$/cancelRequest", { id: uiCall.id });
  await uiAborted;

  const cancelled = bridge.beginCall("extension.tool.execute", {
    name: "wait_for_cancel",
    input: { mode: "cancel" },
  });
  void cancelled.response.catch(() => undefined);
  await cancelStarted;
  bridge.notify("$/cancelRequest", { id: cancelled.id });
  await cancelAborted;

  const reused = await bridge.callWithId(cancelled.id, "extension.tool.execute", {
    name: "wait_for_cancel",
    input: { mode: "quick" },
  });
  assert.deepEqual(reused, { content: "quick result" });

  const disconnected = bridge.beginCall("extension.tool.execute", {
    name: "wait_for_cancel",
    input: { mode: "disconnect" },
  });
  void disconnected.response.catch(() => undefined);
  await disconnectStarted;
  child.kill();
  await new Promise<void>((resolve) => child.once("close", () => resolve()));
  await disconnectAborted;
  assert.deepEqual(bridge.hostRequests, []);

  const replacement = spawnProcess(executable, { stdio: ["pipe", "pipe", "pipe"] });
  t.after(() => replacement.kill());
  assert.ok(replacement.stdin);
  assert.ok(replacement.stdout);
  const replacementBridge = new BridgeTestClient(replacement.stdout, replacement.stdin);
  await replacementBridge.call("extension.initialize", {
    extension: { id: "cancellable", cwd: workspace },
    capabilities: { toolUpdates: true },
  });
  const result = await replacementBridge.call("extension.tool.execute", {
    name: "wait_for_cancel",
    input: { mode: "quick" },
  });
  assert.deepEqual(result, { content: "quick result" });
  assert.deepEqual(replacementBridge.hostRequests, []);

  await client.close();
});

class BridgeTestClient {
  private buffer = Buffer.alloc(0);
  private nextId = 0;
  private waiters = new Map<number, { resolve(value: unknown): void; reject(error: Error): void }>();
  hostRequests: JsonRPCRequest[] = [];
  private respondToHostRequests = true;

  constructor(stdout: NodeJS.ReadableStream, private stdin: NodeJS.WritableStream) {
    stdout.on("data", (chunk: Buffer) => {
      this.buffer = Buffer.concat([this.buffer, chunk]);
      this.drain();
    });
  }

  call(method: string, params: unknown): Promise<unknown> {
    return this.beginCall(method, params).response;
  }

  beginCall(method: string, params: unknown): { id: number; response: Promise<unknown> } {
    const id = ++this.nextId;
    return this.beginCallWithId(id, method, params);
  }

  callWithId(id: number, method: string, params: unknown): Promise<unknown> {
    return this.beginCallWithId(id, method, params).response;
  }

  private beginCallWithId(id: number, method: string, params: unknown): { id: number; response: Promise<unknown> } {
    this.nextId = Math.max(this.nextId, id);
    const payload = JSON.stringify({ jsonrpc: "2.0", id, method, params });
    this.stdin.write(`Content-Length: ${Buffer.byteLength(payload)}\r\n\r\n${payload}`);
    const response = new Promise<unknown>((resolve, reject) => {
      this.waiters.set(id, { resolve, reject });
    });
    return { id, response };
  }

  notify(method: string, params: unknown): void {
    const payload = JSON.stringify({ jsonrpc: "2.0", method, params });
    this.stdin.write(`Content-Length: ${Buffer.byteLength(payload)}\r\n\r\n${payload}`);
  }

  async waitForHostRequests(count: number): Promise<void> {
    while (this.hostRequests.length < count) {
      await new Promise((resolve) => setTimeout(resolve, 5));
    }
  }

  respondHostError(id: number | string, error: { code: number; message: string; data?: unknown }): void {
    const payload = JSON.stringify({ jsonrpc: "2.0", id, error });
    this.stdin.write(`Content-Length: ${Buffer.byteLength(payload)}\r\n\r\n${payload}`);
  }

  setRespondToHostRequests(enabled: boolean): void {
    this.respondToHostRequests = enabled;
  }

  private drain(): void {
    while (true) {
      const headerEnd = this.buffer.indexOf("\r\n\r\n");
      if (headerEnd === -1) {
        return;
      }
      const header = this.buffer.subarray(0, headerEnd).toString("ascii");
      const match = /Content-Length:\s*(\d+)/i.exec(header);
      if (!match) {
        throw new Error("missing Content-Length");
      }
      const length = Number.parseInt(match[1], 10);
      const start = headerEnd + 4;
      const end = start + length;
      if (this.buffer.length < end) {
        return;
      }
      const response = JSON.parse(this.buffer.subarray(start, end).toString("utf8")) as JsonRPCRequest;
      this.buffer = this.buffer.subarray(end);
      if (response.method && response.id !== undefined && response.id !== null) {
        this.hostRequests.push(response);
        if (!this.respondToHostRequests) {
          continue;
        }
        const payload = JSON.stringify({ jsonrpc: "2.0", id: response.id, result: { accepted: true } });
        this.stdin.write(`Content-Length: ${Buffer.byteLength(payload)}\r\n\r\n${payload}`);
        continue;
      }
      if (typeof response.id !== "number") {
        continue;
      }
      const waiter = this.waiters.get(response.id);
      if (!waiter) {
        continue;
      }
      this.waiters.delete(response.id);
      if (response.error) {
        waiter.reject(new Error(response.error.message ?? "JSON-RPC error"));
      } else {
        waiter.resolve(response.result);
      }
    }
  }
}
