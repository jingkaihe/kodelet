import assert from "node:assert/strict";
import { spawn as spawnProcess } from "node:child_process";
import { EventEmitter } from "node:events";
import { Readable, Writable } from "node:stream";
import test from "node:test";

import { Client, Profile, defineExtension, type ToolContext } from "./index.js";
import type { SpawnFunction, SpawnedProcess, ToolUpdateData } from "./agent.js";

interface JsonRPCRequest {
  jsonrpc?: "2.0";
  id?: number | string | null;
  parentId?: number | string;
  method?: string;
  params?: unknown;
  result?: unknown;
  error?: { code?: number; message?: string };
}

interface FakeACPProcessOptions {
  sessionId?: string;
  onRequest?(request: JsonRPCRequest, process: FakeACPProcess): boolean;
  onPrompt?(request: JsonRPCRequest, process: FakeACPProcess): Promise<void> | void;
  steerResult?: unknown;
  steeringSupported?: boolean;
  sessionExtensionsVersion?: number;
}

class FakeACPProcess extends EventEmitter implements SpawnedProcess {
  stdin: Writable;
  stdout = new Readable({ read() {} });
  stderr = new Readable({ read() {} });
  requests: JsonRPCRequest[] = [];
  private inputBuffer = "";
  private closed = false;
  private nextServerId = 0;
  private readonly serverPending = new Map<string, { resolve(value: unknown): void; reject(error: Error): void }>();
  responses: JsonRPCRequest[] = [];

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

  requestClient(method: string, params?: unknown): Promise<unknown> {
    const id = `server-${++this.nextServerId}`;
    return new Promise((resolve, reject) => {
      this.serverPending.set(id, { resolve, reject });
      this.write({ jsonrpc: "2.0", id, method, params });
    });
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
    if (!request.method && typeof request.id === "string") {
      this.responses.push(request);
      const pending = this.serverPending.get(request.id);
      this.serverPending.delete(request.id);
      if (request.error) pending?.reject(new Error(request.error.message));
      else pending?.resolve(request.result);
      return;
    }
    if (!request.method || request.id === undefined || request.id === null) {
      return;
    }
    this.requests.push(request);
    this.handleRequest(request);
  }

  private handleRequest(request: JsonRPCRequest): void {
    if (this.options.onRequest?.(request, this)) {
      return;
    }
    switch (request.method) {
      case "initialize":
        this.respond(request.id, {
          protocolVersion: 1,
          agentCapabilities: {},
          authMethods: [],
          _meta: { ...(this.options.steeringSupported === false ? {} : { steering: { supported: true } }),
            ...(this.options.sessionExtensionsVersion === undefined ? {} : { sessionExtensions: { version: this.options.sessionExtensionsVersion } }) },
        });
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
      case "_session/steering":
        this.respond(request.id, this.options.steerResult ?? { outcome: "injected" });
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
    setImmediate(() => {
      this.emit("error", error);
      this.emit("close", -1, null);
    });
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

test("Session sends typed daemon flags without temporary config", async () => {
  const calls: Array<{ args: string[]; env?: NodeJS.ProcessEnv }> = [];
  const process = new FakeACPProcess({ sessionId: "conv-profile" });
  const spawn: SpawnFunction = (_command, args, options) => {
    calls.push({ args, env: options.env });
    return process;
  };

  const client = new Client({ spawn });
  const session = await client.createSession({
    profile: {
      name: "openai",
      provider: "openai",
      model: "gpt-5.5",
      allowed_tools: ["sdk_echo"],
    },
  });

  assert.equal(calls[0]?.env?.KODELET_CONFIG_FILE_MODE, undefined);
  assert.equal(calls[0]?.env?.KODELET_CONFIG_FILE, undefined);
  assert.deepEqual(calls[0]?.args, ["acp", "--provider=openai", "--model=gpt-5.5", '--allowed-tools="sdk_echo"']);
  assert.equal((process.requests[1].params as { _meta?: unknown })._meta, undefined);

  await session.close();
});

test("Inline remote options do not rewrite the client's environment", async () => {
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
    assert.equal(calls[0]?.env?.KODELET_MODEL, "ambient-model");
    assert.equal(calls[0]?.env?.KODELET_PROVIDER, "explicit-provider");
    assert.equal(calls[0]?.env?.KODELET_CONFIG_FILE_MODE, undefined);
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
  assert.equal(calls[0]?.cwd, process.cwd());
  assert.deepEqual(calls[0]?.args, ["acp", "--max-turns=2", "--profile=work"]);
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

test("ACP handles large replay and live messages from a real subprocess", { timeout: 5000 }, async (t) => {
  const text = "Large output 🙂 ".repeat(20000);
  let child: ReturnType<typeof spawnProcess> | undefined;
  const client = new Client({
    spawn: (_command, _args, options) => {
      child = spawnProcess(process.execPath, ["-e", `
        const readline = require("node:readline");
        const text = "Large output 🙂 ".repeat(20000);
        const emit = message => process.stdout.write(JSON.stringify(message) + "\\n");
        const update = () => emit({ jsonrpc: "2.0", method: "session/update", params: {
          sessionId: "large-child", update: { sessionUpdate: "agent_message_chunk", content: { type: "text", text } }
        }});
        readline.createInterface({ input: process.stdin }).on("line", line => {
          const request = JSON.parse(line);
          if (request.method === "session/load" || request.method === "session/prompt") update();
          emit({ jsonrpc: "2.0", id: request.id, result: request.method === "initialize"
            ? { protocolVersion: 1, padding: text } : { stopReason: "end_turn" } });
        });
      `], options);
      return child;
    },
  });
  t.after(() => client.close());
  const session = await client.createSession({ resume: "large-child" });
  const result = await session.runAndWait({ message: "continue" });
  assert.equal(result.content, text);
  await client.close();
  assert.notEqual(child?.signalCode ?? child?.exitCode, null);
});

test("ACP preserves UTF-8 characters split across stdout chunks", async () => {
  const child = new FakeACPProcess({
    async onPrompt(_request, child) {
      const message = Buffer.from(`${JSON.stringify({ method: "session/update", params: {
        sessionId: "conv-1", update: { sessionUpdate: "agent_message_chunk", content: { type: "text", text: "🙂漢字" } },
      } })}\n`);
      for (const byte of message) {
        child.stdout.push(Buffer.from([byte]));
        await new Promise<void>((resolve) => setImmediate(resolve));
      }
    },
  });
  const client = new Client({ spawn: () => child });
  try {
    const session = await client.createSession();
    assert.equal((await session.runAndWait({ message: "continue" })).content, "🙂漢字");
  } finally {
    await client.close();
  }
});

test("ACP accepts individual live messages above 16 MiB", { timeout: 5000 }, async () => {
  const text = "x".repeat(17 * 1024 * 1024);
  const client = new Client({ spawn: () => new FakeACPProcess({
    onPrompt(_request, child) {
      child.notify("session/update", { sessionId: "conv-1", update: {
        sessionUpdate: "agent_message_chunk", content: { type: "text", text },
      } });
    },
  }) });
  try {
    const session = await client.createSession();
    assert.equal((await session.runAndWait({ message: "continue" })).content, text);
  } finally {
    await client.close();
  }
});

for (const ending of ["", "\n"]) {
  test(`ACP rejects oversized ${ending ? "complete" : "unterminated"} messages`, { timeout: 5000 }, async () => {
    const child = new FakeACPProcess({
      onRequest(request, child) {
        if (request.method !== "session/load") return false;
        child.stdout.push("x".repeat(64 * 1024 * 1024 + 1) + ending);
        return true;
      },
    });
    const client = new Client({ spawn: () => child });
    await assert.rejects(() => client.createSession({ resume: "saved-child" }), /ACP stdout message exceeds/);
    await client.close();
  });
}

for (const name of ["stdin", "stdout", "stderr"] as const) {
  test(`ACP ${name} errors reject startup without unhandled stream errors`, { timeout: 3000 }, async () => {
    const child = new FakeACPProcess({
      onRequest(_request, child) {
        setImmediate(() => child[name].destroy(new Error("broken pipe")));
        return true;
      },
    });
    const client = new Client({ spawn: () => child });
    await assert.rejects(() => client.createSession(), new RegExp(`ACP ${name} failed: broken pipe`));
  });
}

test("ACP stdout failure rejects both an active prompt and steering request", { timeout: 3000 }, async () => {
  const child = new FakeACPProcess({
    onRequest(request) {
      return request.method === "session/prompt" || request.method === "_session/steering";
    },
  });
  const client = new Client({ spawn: () => child });
  const session = await client.createSession();
  const prompt = assert.rejects(session.runAndWait({ message: "keep running" }), /broken stdout/);
  const steering = assert.rejects(session.steer("focus"), /broken stdout/);
  child.stdout.destroy(new Error("broken stdout"));
  await Promise.all([prompt, steering]);
  await client.close();
});

test("ACP rejects unexpected stdout EOF while the child is still alive", { timeout: 3000 }, async () => {
  const client = new Client({ spawn: () => new FakeACPProcess({
    onRequest(_request, child) {
      child.stdout.push(null);
      return true;
    },
  }) });
  await assert.rejects(() => client.createSession(), /ACP stdout ended/);
});

test("ACP synchronous stdin write failures reject the pending request", { timeout: 3000 }, async () => {
  const child = new FakeACPProcess();
  child.stdin.write = () => { throw new Error("write failed"); };
  const client = new Client({ spawn: () => child });
  await assert.rejects(() => client.createSession(), /write failed/);
});

test("ACP close rejects pending work and kills a child that ignores SIGTERM", { timeout: 5000 }, async (t) => {
  let child: ReturnType<typeof spawnProcess> | undefined;
  const client = new Client({ spawn: (_command, _args, options) => {
    child = spawnProcess(process.execPath, ["-e", `
      process.on("SIGTERM", () => {});
      require("node:readline").createInterface({ input: process.stdin }).on("line", line => {
        const request = JSON.parse(line);
        if (request.method === "session/prompt") return;
        process.stdout.write(JSON.stringify({ jsonrpc: "2.0", id: request.id, result: { sessionId: "stubborn-child" } }) + "\\n");
      });
    `], options);
    return child;
  } });
  t.after(() => child?.kill("SIGKILL"));
  const session = await client.createSession();
  const pending = assert.rejects(session.runAndWait({ message: "wait forever" }), /process closed/);
  await Promise.all([session.close(), session.close(), pending]);
  assert.equal(child?.signalCode, "SIGKILL");
  await client.close();
});

test("ACP close reports incomplete cleanup and allows retry after exit", { timeout: 5000 }, async () => {
  const child = new FakeACPProcess();
  const signals: Array<NodeJS.Signals | number | undefined> = [];
  child.kill = (signal?: NodeJS.Signals | number) => { signals.push(signal); return false; };
  const client = new Client({ spawn: () => child });
  const session = await client.createSession();
  await assert.rejects(() => session.close(), /cleanup is incomplete/);
  assert.deepEqual(signals, ["SIGTERM", "SIGKILL"]);
  child.emit("close", 0, null);
  child.stdout.push(null);
  child.stderr.push(null);
  await client.close();
});

test("Session steers an active run", async () => {
  let releasePrompt!: () => void;
  const processes: FakeACPProcess[] = [];
  const spawn: SpawnFunction = () => {
    const process = new FakeACPProcess({
      onPrompt() {
        return new Promise<void>((resolve) => {
          releasePrompt = resolve;
          queueMicrotask(() => promptReady());
        });
      },
    });
    processes.push(process);
    return process;
  };
  let promptReady!: () => void;
  const active = new Promise<void>((resolve) => {
    promptReady = resolve;
  });

  const client = new Client({ spawn });
  const session = await client.createSession();
  const run = session.runAndWait({ message: "inspect" });
  await active;

  await assert.rejects(() => session.steer("   "), /non-empty/);
  assert.deepEqual(await session.steer("  focus on locking  "), {
    outcome: "injected",
  });
  assert.deepEqual(
    processes[0]?.requests.filter((request) => request.method === "_session/steering").map((request) => request.params),
    [{
      sessionId: "conv-1",
      prompt: [{ type: "text", text: "focus on locking" }],
      _meta: { steering: { idleBehavior: "promptRequired" } },
    }],
  );

  releasePrompt();
  await run;
  await assert.rejects(() => session.steer("late"), /without an active run/);
  await session.close();
});

test("Session rejects malformed steering responses", async () => {
  let releasePrompt!: () => void;
  let promptReady!: () => void;
  const active = new Promise<void>((resolve) => {
    promptReady = resolve;
  });
  const client = new Client({
    spawn: () =>
      new FakeACPProcess({
        steerResult: { outcome: "unknown" },
        onPrompt() {
          promptReady();
          return new Promise<void>((resolve) => {
            releasePrompt = resolve;
          });
        },
      }),
  });
  const session = await client.createSession();
  const run = session.runAndWait({ message: "inspect" });
  await active;
  await assert.rejects(() => session.steer("focus"), /Invalid _session\/steering response/);
  releasePrompt();
  await run;
  await client.close();
});

test("Session requires the ACP steering capability", async () => {
  let releasePrompt!: () => void;
  let promptReady!: () => void;
  const active = new Promise<void>((resolve) => {
    promptReady = resolve;
  });
  const process = new FakeACPProcess({
    steeringSupported: false,
    onPrompt() {
      promptReady();
      return new Promise<void>((resolve) => {
        releasePrompt = resolve;
      });
    },
  });
  const client = new Client({ spawn: () => process });
  const session = await client.createSession();
  const run = session.runAndWait({ message: "inspect" });
  await active;

  await assert.rejects(() => session.steer("focus"), /does not advertise session steering support/);
  assert.equal(process.requests.some((request) => request.method === "_session/steering"), false);

  releasePrompt();
  await run;
  await client.close();
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

test("Session rejects unsupported bridge transport values and daemon settings before spawning", async () => {
  let spawned = false;
  const client = new Client({ spawn: () => { spawned = true; return new FakeACPProcess(); } });
  await assert.rejects(client.createSession({ extensionTransport: "socket" as never }), /extensionTransport must be unix or tcp/);
  await assert.rejects(client.createSession({ profile: { openai: { api_key_env_var: "LOCAL_KEY" } } }));
  await assert.rejects(client.createSession({ profile: { sysprompt: "/client/prompt.md" } }));
  await assert.rejects(client.createSession({ options: null as never }));
  assert.equal(spawned, false);
});

test("Session accepts empty extensions and undefined bridge/UI options", async () => {
  const process = new FakeACPProcess();
  const client = new Client({ spawn: () => process });
  try {
    const session = await client.createSession({ extensions: [], extensionTransport: undefined, ui: undefined });
    const response = await session.runAndWait({ message: "hello" });

    assert.equal(response.conversationId, "conv-1");
    assert.equal(response.exitCode, 0);
    assert.deepEqual(process.requests.map((request) => request.method), ["initialize", "session/new", "session/prompt"]);
  } finally {
    await client.close();
  }
});

test("Remote session flags preserve runner paths and named profile selection", async () => {
  const processes: FakeACPProcess[] = [];
  const args: string[][] = [];
  const client = new Client({ server: "http://daemon", runner: "runner-one", spawn: (_command, flags) => {
    args.push(flags);
    const process = new FakeACPProcess(); processes.push(process); return process;
  } });
  await client.createSession({ cwd: "/only/on/runner", profile: "work", environmentProfile: "locked", options: { allowedTools: [] } });
  assert.deepEqual(args[0], ["acp", "--server", "http://daemon", "--runner", "runner-one", "--allowed-tools=", "--profile=work", "--runner-profile=locked"]);
  assert.deepEqual(processes[0].requests[1].params, { cwd: "/only/on/runner" });
  await client.createSession({ resume: "existing", cwd: "/stored/path", options: { noTools: false, enableFSSearchTools: true, allowedCommands: ['echo "a,b"'] } });
  assert.deepEqual(args[1].slice(5), ["--no-tools=false", '--allowed-commands="echo ""a,b"""', "--enable-fs-search-tools=true"]);
  assert.equal(processes[1].requests[1].method, "session/load");
  assert.deepEqual(processes[1].requests[1].params, { sessionId: "existing", cwd: "/stored/path" });
  await client.close();
});

interface RelayFrame {
  sessionId: string;
  runId: string;
  extensionId: string;
  message?: JsonRPCRequest;
  close?: boolean;
}

class InlineRelay {
  readonly process: FakeACPProcess;
  readonly frames: RelayFrame[] = [];
  hostRequest?: (message: JsonRPCRequest, frame: RelayFrame) => Promise<unknown> | unknown;
  private nextId = 0;
  private readonly pending = new Map<string, { resolve(value: any): void; reject(error: Error): void }>();

  constructor(readonly sessionId = "conv-inline") {
    this.process = new FakeACPProcess({ sessionId, sessionExtensionsVersion: 1, onRequest: (request, child) => {
      if (request.method !== "kodelet/extensionFrame") return false;
      const frame = request.params as RelayFrame;
      this.frames.push(frame);
      child.stdout.push(`${JSON.stringify({ jsonrpc: "2.0", id: request.id, result: {} })}\n`);
      const message = frame.message!;
      if (!message.method) {
        const key = this.key(frame);
        const pending = this.pending.get(key);
        this.pending.delete(key);
        if (message.error) pending?.reject(new Error(message.error.message));
        else pending?.resolve(message.result);
      } else if (message.id !== undefined && this.hostRequest) {
        void Promise.resolve().then(() => this.hostRequest!(message, frame)).then(
          (result) => this.send({ jsonrpc: "2.0", id: message.id, result }, frame),
          (error) => this.send({ jsonrpc: "2.0", id: message.id, error: { code: -32000, message: String(error) } }, frame),
        ).catch(() => undefined);
      }
      return true;
    } });
  }

  send(message: JsonRPCRequest, route: Partial<RelayFrame> = {}): Promise<unknown> {
    return this.process.requestClient("kodelet/extensionFrame", { sessionId: this.sessionId, runId: "run-1", extensionId: "inline-1", ...route, message });
  }

  beginCall(method: string, params: unknown, route: Partial<RelayFrame> = {}) {
    const id = ++this.nextId;
    const frame = { sessionId: this.sessionId, runId: "run-1", extensionId: "inline-1", ...route, message: { jsonrpc: "2.0" as const, id, method, params } };
    const result = new Promise<any>((resolve, reject) => this.pending.set(this.key(frame), { resolve, reject }));
    const accepted = this.send(frame.message, frame);
    return { id, accepted, result };
  }

  async call(method: string, params: unknown, route: Partial<RelayFrame> = {}): Promise<any> {
    const { accepted, result } = this.beginCall(method, params, route);
    await accepted;
    return result;
  }

  initialize(route: Partial<RelayFrame> = {}): Promise<any> {
    return this.call("extension.initialize", {
      protocolVersion: "2026-05-30", extension: { id: `session:${route.extensionId ?? "inline-1"}`, cwd: "/runner/workspace" },
      capabilities: { toolUpdates: true, conversations: { fork: true }, ui: { confirm: false, widgets: false } },
    }, route);
  }

  close(route: Partial<RelayFrame> = {}): Promise<unknown> {
    return this.process.requestClient("kodelet/extensionFrame", { sessionId: this.sessionId, runId: "run-1", extensionId: "inline-1", ...route, close: true });
  }

  private key(frame: RelayFrame): string {
    return JSON.stringify([frame.sessionId, frame.runId, frame.extensionId, frame.message?.id]);
  }
}

for (const version of [undefined, 2]) {
  test(`Inline extensions fail fast and close incompatible ACP version ${version}`, async () => {
    const child = new FakeACPProcess({ sessionExtensionsVersion: version });
    let invoked = false, exited = false;
    child.once("close", () => { exited = true; });
    const client = new Client({ spawn: () => child });
    await assert.rejects(client.createSession({ extensions: [() => { invoked = true; }] }), /sessionExtensions version 1.*update Kodelet/);
    assert.equal(invoked, false);
    assert.equal(exited, true);
    assert.deepEqual(child.requests.map(({ method }) => method), ["initialize"]);
  });
}

test("Inline extensions negotiate deterministic IDs for new and resumed sessions without launch configuration", async () => {
  const children: FakeACPProcess[] = [];
  const args: string[][] = [];
  const client = new Client({ spawn: (_command, flags) => {
    args.push(flags);
    const child = new FakeACPProcess({ sessionExtensionsVersion: 1 });
    children.push(child);
    return child;
  } });
  let invoked = false;
  const extensions = [defineExtension(() => { invoked = true; }), defineExtension(() => {})];
  try {
    await client.createSession({ extensions, extensionTransport: "unix", ui: {}, cwd: "/runner/path" });
    await client.createSession({ extensions, extensionTransport: "tcp", resume: "saved", cwd: "/runner/path" });
    for (const child of children) {
      assert.deepEqual((child.requests[0].params as any).clientCapabilities._meta, { sessionExtensions: { version: 1 } });
      assert.deepEqual((child.requests[1].params as any)._meta, { sessionExtensions: { version: 1, extensionIds: ["inline-1", "inline-2"] } });
    }
    assert.equal(children[1].requests[1].method, "session/load");
    assert.deepEqual(args, [["acp"], ["acp"]]);
    assert.equal(invoked, false, "entrypoints are lazy until runner initialization");
  } finally { await client.close(); }
});

test("Inline callbacks ACK before dispatch, preserve nested RPC parent IDs, updates, local UI, and errors", { timeout: 5000 }, async (t) => {
  const relay = new InlineRelay();
  let calls = 0, confirms = 0, toolContext: ToolContext | undefined;
  const client = new Client({ spawn: () => relay.process });
  t.after(() => client.close());
  const session = await client.createSession({ extensions: [api => {
    api.registerTool({ name: "echo", description: "Echo", inputSchema: { type: "object" }, async execute(input, ctx) {
      calls++;
      toolContext = ctx;
      assert.ok(relay.process.responses.some(response => response.id === "server-2" && !response.error), "tool frame must already be ACKed");
      if ((input as { fail?: boolean }).fail) throw new Error("callback boom");
      assert.equal(await ctx.ui.confirm({ message: "Confirm?" }), true);
      await ctx.ui.notify("hello");
      const [, fork] = await Promise.all([ctx.update("progress"), ctx.forkConversation({ name: "branch" })]);
      return `${ctx.conversationId}:${fork}:${calls}`;
    } });
  }], ui: { confirm: () => { confirms++; return true; }, notify: () => {} } });
  relay.hostRequest = (message) => {
    if (message.method === "kodelet.tool.update") return {};
    if (message.method === "kodelet.conversation.fork") return { conversationId: "forked" };
    throw new Error(`Unexpected host method ${message.method}`);
  };
  assert.equal((await relay.initialize()).tools[0].name, "echo");
  assert.deepEqual(await relay.call("extension.tool.execute", { name: "echo", input: {}, context: { conversationId: session.id } }), { content: "conv-inline:forked:1" });
  const hostRequests = relay.frames.filter(frame => frame.message?.method);
  assert.deepEqual(hostRequests.map(frame => frame.message?.method), ["kodelet.tool.update", "kodelet.conversation.fork"]);
  assert.ok(hostRequests.every(frame => frame.message?.parentId === 2));
  assert.equal(confirms, 1);
  assert.equal(toolContext?.signal.aborted, true);
  await assert.rejects(toolContext!.update("late"), /no longer active/);
  await assert.rejects(relay.call("extension.tool.execute", { name: "echo", input: { fail: true } }), /callback boom/);
  await assert.rejects(relay.call("unknown.extension.method", {}), /Unknown JSON-RPC method/);
  assert.equal(relay.frames.at(-1)?.message?.error?.code, -32601);
  assert.equal(calls, 2);
});

test("Inline hosts are isolated per session, run, and extension while retaining original closure callbacks", { timeout: 5000 }, async (t) => {
  const relays = [new InlineRelay("session-a"), new InlineRelay("session-b")];
  let spawnIndex = 0, registrations = 0, callbacks = 0, ended = 0;
  const client = new Client({ spawn: () => relays[spawnIndex++].process });
  t.after(() => client.close());
  const extension = defineExtension(api => {
    registrations++;
    let localCount = 0;
    api.on("session.end", () => { ended++; });
    api.registerTool({ name: "count", description: "Counter", inputSchema: {}, execute() { callbacks++; return `${++localCount}:${callbacks}`; } });
  });
  await client.createSession({ extensions: [extension, extension] });
  await client.createSession({ extensions: [extension] });
  await Promise.all([relays[0].initialize(), relays[0].initialize({ extensionId: "inline-2" }), relays[1].initialize()]);
  assert.equal(registrations, 3);
  const params = { name: "count", input: {} };
  assert.deepEqual(await relays[0].call("extension.tool.execute", params), { content: "1:1" });
  assert.deepEqual(await relays[0].call("extension.tool.execute", params), { content: "2:2" });
  assert.deepEqual(await relays[0].call("extension.tool.execute", params, { extensionId: "inline-2" }), { content: "1:3" });
  assert.deepEqual(await relays[1].call("extension.tool.execute", params), { content: "1:4" });
  await relays[0].close();
  await relays[0].initialize({ runId: "run-2" });
  assert.deepEqual(await relays[0].call("extension.tool.execute", params, { runId: "run-2" }), { content: "1:5" });
  assert.equal(registrations, 4);
  await client.close();
  assert.equal(ended, 4);
});

test("Inline relay rejects unknown sessions, extension IDs, malformed frames, replay, and non-initialize creation", { timeout: 5000 }, async (t) => {
  const relay = new InlineRelay();
  let registered = 0;
  const client = new Client({ spawn: () => relay.process });
  t.after(() => client.close());
  await client.createSession({ extensions: [() => { registered++; }] });
  const execute = { jsonrpc: "2.0" as const, id: 1, method: "extension.tool.execute", params: {} };
  await assert.rejects(relay.send(execute), /must start with extension.initialize/);
  await assert.rejects(relay.send(execute, { extensionId: "inline-2" }), /Invalid or unsupported/);
  await assert.rejects(relay.send(execute, { sessionId: "other" }), /different ACP session/);
  await assert.rejects(relay.send({ jsonrpc: "2.0", id: 1, method: "extension.initialize", params: { extension: {} } }), /requires an extension identity/);
  await assert.rejects(relay.process.requestClient("kodelet/extensionFrame", { sessionId: relay.sessionId, runId: "run-1", extensionId: "inline-1", message: null }), /Invalid or unsupported/);
  assert.equal(registered, 0);
  await relay.initialize();
  await assert.rejects(relay.send({ jsonrpc: "2.0", id: 3, method: "extension.initialize", params: { extension: { id: "inline-1" } } }), /cannot be initialized again/);
  await relay.close();
  await assert.rejects(relay.send(execute), /cannot be replayed/);
  assert.equal(registered, 1);
  await assert.rejects(relay.process.requestClient("unknown/clientMethod", {}), /Unsupported client RPC/);
  assert.equal(relay.process.responses.at(-1)?.error?.code, -32601);
});

for (const cause of ["cancel", "relay close", "session close", "process failure"] as const) {
  test(`Inline ${cause} aborts callbacks and rejects pending reverse calls without late output`, { timeout: 5000 }, async (t) => {
    const relay = new InlineRelay();
    let ctx!: ToolContext;
    let started!: () => void, stopped!: () => void;
    const startedPromise = new Promise<void>(resolve => { started = resolve; });
    const stoppedPromise = new Promise<void>(resolve => { stopped = resolve; });
    let reverseError = "", ended = 0;
    const client = new Client({ spawn: () => relay.process });
    t.after(() => client.close());
    const session = await client.createSession({ extensions: [api => {
      api.on("session.end", () => { ended++; });
      api.registerTool({ name: "wait", description: "Wait", inputSchema: {}, async execute(_input, context) {
        ctx = context;
        try {
          const pending = ctx.update("waiting");
          started();
          await pending;
        } catch (error) { reverseError = String(error); }
        finally { stopped(); }
        return "late result";
      } });
    }] });
    await relay.initialize();
    const call = relay.beginCall("extension.tool.execute", { name: "wait", input: {} });
    await call.accepted;
    await startedPromise;
    if (cause === "cancel") await relay.send({ jsonrpc: "2.0", method: "$/cancelRequest", params: { id: call.id } });
    else if (cause === "relay close") await relay.close();
    else if (cause === "session close") await session.close();
    else relay.process.stdout.emit("error", new Error("broken relay"));
    await stoppedPromise;
    assert.equal(ctx.signal.aborted, true);
    assert.match(reverseError, /cancelled|disconnected|closed|broken relay/);
    await assert.rejects(ctx.update("stale"), /active|disconnected|closed|broken relay/);
    await new Promise(resolve => setImmediate(resolve));
    assert.ok(!relay.frames.some(frame => !frame.message?.method && frame.message?.id === call.id), "cancelled execution must not send late results");
    await session.close();
    assert.equal(ended, 1);
  });
}

test("Inline entrypoint failures are returned as raw errors and never restarted", { timeout: 5000 }, async (t) => {
  const relay = new InlineRelay();
  let registrations = 0;
  const client = new Client({ spawn: () => relay.process });
  t.after(() => client.close());
  await client.createSession({ extensions: [() => { registrations++; throw new Error("registration boom"); }] });
  await assert.rejects(relay.initialize(), /registration boom/);
  await assert.rejects(relay.send({ jsonrpc: "2.0", id: 2, method: "extension.initialize", params: { extension: { id: "inline-1" } } }), /cannot be replayed/);
  assert.equal(registrations, 1);
});

test("Inline ACK and close do not wait for an asynchronous entrypoint", { timeout: 5000 }, async (t) => {
  const relay = new InlineRelay();
  let release!: () => void, started!: () => void;
  const registration = new Promise<void>(resolve => { release = resolve; });
  const registering = new Promise<void>(resolve => { started = resolve; });
  let ended!: () => void;
  const cleanup = new Promise<void>(resolve => { ended = resolve; });
  const client = new Client({ spawn: () => relay.process });
  t.after(() => client.close());
  const session = await client.createSession({ extensions: [async api => {
    started();
    await registration;
    api.on("session.end", () => { ended(); });
  }] });
  assert.deepEqual(await relay.send({ jsonrpc: "2.0", id: 1, method: "extension.initialize", params: { extension: { id: "session:inline-1" } } }), {});
  await registering;
  await relay.close();
  await session.close();
  release();
  await cleanup;
  assert.equal(relay.frames.length, 0, "closed initialization must not send a late response");
});

test("Inline local UI cancellation settles pending calls even when a handler ignores its signal", { timeout: 5000 }, async (t) => {
  const relay = new InlineRelay();
  let entered!: () => void, stopped!: () => void, release!: (value: boolean) => void;
  const interacting = new Promise<void>(resolve => { entered = resolve; });
  const finished = new Promise<void>(resolve => { stopped = resolve; });
  const interaction = new Promise<boolean>(resolve => { release = resolve; });
  let uiSignal: AbortSignal | undefined;
  const client = new Client({ spawn: () => relay.process });
  t.after(() => client.close());
  const session = await client.createSession({ extensions: [api => {
    api.registerTool({ name: "confirm", description: "Confirm", inputSchema: {}, async execute(_input, ctx) {
      try { await ctx.ui.confirm({ message: "Wait" }); }
      finally { stopped(); }
      return "late UI result";
    } });
  }], ui: { confirm: (_request, signal) => { uiSignal = signal; entered(); return interaction; } } });
  await relay.initialize();
  const call = relay.beginCall("extension.tool.execute", { name: "confirm", input: {} });
  await call.accepted;
  await interacting;
  await relay.send({ jsonrpc: "2.0", method: "$/cancelRequest", params: { id: call.id } });
  await finished;
  assert.equal(uiSignal?.aborted, true);
  release(true);
  await session.close();
  assert.equal(relay.frames.length, 1, "local UI must not relay requests or cancelled results");
});

test("Inline UI overrides preserve runner capability gates and return dismissal/unavailable values", { timeout: 5000 }, async (t) => {
  const relay = new InlineRelay();
  const client = new Client({ spawn: () => relay.process });
  t.after(() => client.close());
  await client.createSession({ extensions: [api => {
    api.registerTool({ name: "ui", description: "UI", inputSchema: {}, async execute(_input, ctx) {
      assert.equal(await ctx.ui.input({ message: "Dismiss" }), undefined);
      assert.equal(await ctx.ui.select({ message: "Select", options: ["one"] }), "one");
      assert.equal(await ctx.ui.confirm({ message: "Unavailable" }), false);
      await ctx.ui.notify("Unavailable");
      await ctx.ui.setWidget("not-enabled", ["hidden"]);
      await ctx.update("not-enabled");
      await assert.rejects(ctx.forkConversation(), /not supported/);
      return "ok";
    } });
  }], ui: { input: () => undefined, select: () => "one" } });
  await relay.call("extension.initialize", {
    protocolVersion: "2026-05-30", extension: { id: "session:inline-1" },
    capabilities: { toolUpdates: false, conversations: { fork: false }, ui: { widgets: false } },
  });
  assert.deepEqual(await relay.call("extension.tool.execute", { name: "ui", input: {} }), { content: "ok" });
  assert.equal(relay.frames.filter(frame => frame.message?.method).length, 0);
});

test("Inline cleanup runs session.end once and is bounded when the callback does not finish", { timeout: 5000 }, async (t) => {
  const relay = new InlineRelay();
  let ended = 0;
  let cleanupSignal: AbortSignal | undefined;
  const client = new Client({ spawn: () => relay.process });
  t.after(() => client.close());
  const session = await client.createSession({ extensions: [api => {
    api.on("session.end", async (_event, ctx) => {
      ended++;
      cleanupSignal = ctx.signal;
      await new Promise(() => {});
    });
  }] });
  await relay.initialize();
  await relay.close();
  await session.close();
  assert.equal(ended, 1);
  assert.equal(cleanupSignal?.aborted, true);
});
