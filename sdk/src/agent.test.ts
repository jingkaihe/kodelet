import assert from "node:assert/strict";
import { spawn as spawnProcess } from "node:child_process";
import { EventEmitter } from "node:events";
import { Readable, Writable } from "node:stream";
import test from "node:test";

import { Client, Profile, defineExtension } from "./index.js";
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
  onRequest?(request: JsonRPCRequest, process: FakeACPProcess): boolean;
  onPrompt?(request: JsonRPCRequest, process: FakeACPProcess): Promise<void> | void;
  steerResult?: unknown;
  steeringSupported?: boolean;
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
    if (this.options.onRequest?.(request, this)) {
      return;
    }
    switch (request.method) {
      case "initialize":
        this.respond(request.id, {
          protocolVersion: 1,
          agentCapabilities: {},
          authMethods: [],
          _meta: this.options.steeringSupported === false ? undefined : { steering: { supported: true } },
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

test("Session rejects inline extensions, bridge transports, and UI handlers before spawning any process", async () => {
  let spawned = false;
  let invoked = false;
  const client = new Client({ spawn: () => { spawned = true; return new FakeACPProcess(); } });
  await assert.rejects(client.createSession({ extensions: [defineExtension(() => { invoked = true; })] }), /install the extension on the runner/);
  await assert.rejects(client.createSession({ extensionTransport: "tcp" }), /Inline executable extensions/);
  await assert.rejects(client.createSession({ extensions: [], extensionTransport: "unix" }), /Inline executable extensions/);
  await assert.rejects(client.createSession({ ui: {} }), /UI handlers/);
  await assert.rejects(client.createSession({ ui: { notify() { invoked = true; } } }), /UI handlers/);
  await assert.rejects(client.createSession({ profile: { openai: { api_key_env_var: "LOCAL_KEY" } } }));
  await assert.rejects(client.createSession({ profile: { sysprompt: "/client/prompt.md" } }));
  await assert.rejects(client.createSession({ options: null as never }));
  assert.equal(spawned, false);
  assert.equal(invoked, false);
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
