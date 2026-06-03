import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { mkdtemp, stat } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { Readable, Writable } from "node:stream";
import test from "node:test";

import { Client, Profile, defineExtension, z } from "./index.js";
import type { SpawnFunction, SpawnedProcess } from "./agent.js";

interface JsonRPCRequest {
  jsonrpc?: "2.0";
  id?: number | string | null;
  method?: string;
  params?: unknown;
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

test("Profile maps early profiler spelling and nested OpenAI config to launch env", () => {
  const profile = new Profile({
    name: "openai",
    profiler: "openai",
    model: "gpt-5.5",
    max_tokens: 128000,
    reasoning_effort: "xhigh",
    weak_model: "gpt-5.4-mini",
    disable_fs_search_tools: true,
    openai: {
      api_mode: "responses",
      platform: "codex",
      service_tier: "fast",
    },
  });

  const launch = profile.toLaunchConfig();
  assert.deepEqual(launch.args, [
    "--provider",
    "openai",
    "--model",
    "gpt-5.5",
    "--max-tokens",
    "128000",
    "--weak-model",
    "gpt-5.4-mini",
    "--reasoning-effort",
    "xhigh",
    "--disable-fs-search-tools",
  ]);
  assert.equal(launch.env.KODELET_OPENAI_API_MODE, "responses");
  assert.equal(launch.env.KODELET_OPENAI_PLATFORM, "codex");
  assert.equal(launch.env.KODELET_OPENAI_SERVICE_TIER, "fast");
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
            title: "bash",
            kind: "execute",
            rawInput: { command: "pwd" },
          },
        });
        child.notify("session/update", {
          sessionId: "conv-1",
          update: {
            sessionUpdate: "tool_call_update",
            toolCallId: "call-1",
            status: "completed",
            content: [{ type: "content", content: { type: "text", text: "/tmp" } }],
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
  let toolResult = "";
  session.on("tool.call", (event) => {
    toolName = event.data.toolName;
  });
  session.on("tool.result", (event) => {
    toolResult = event.data.result;
  });

  const response = await session.runAndWait({ message: "meaning?", images: ["diagram.png"] });

  assert.equal(response.content, "forty two");
  assert.equal(response.conversationId, "conv-1");
  assert.deepEqual(deltas, ["forty", " two"]);
  assert.deepEqual(thoughts, ["checking"]);
  assert.equal(toolName, "bash");
  assert.equal(toolResult, "/tmp");
  assert.equal(response.stopReason, "end_turn");
  assert.equal(session.id, "conv-1");
  assert.equal(calls[0]?.command, "kodelet-test");
  assert.equal(calls[0]?.cwd, "/workspace");
  assert.deepEqual(calls[0]?.args, ["--profile", "work", "acp", "--max-turns", "2"]);
  assert.deepEqual(processes[0]?.requests.map((request) => request.method), ["initialize", "session/new", "session/prompt"]);
  assert.deepEqual((processes[0]?.requests[1]?.params as { cwd: string }).cwd, "/workspace");
  assert.deepEqual((processes[0]?.requests[2]?.params as { sessionId: string; prompt: unknown[] }).prompt, [
    { type: "text", text: "meaning?" },
    { type: "image", uri: "diagram.png" },
  ]);

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
  assert.equal(env.KODELET_EXTENSIONS_ENABLED, "true");
  assert.equal(env.KODELET_EXTENSIONS_ALLOW, env.KODELET_EXTENSIONS_LOCAL_DIR);
  assert.ok(env.KODELET_EXTENSIONS_LOCAL_DIR);
  const info = await stat(env.KODELET_EXTENSIONS_LOCAL_DIR as string);
  assert.equal(info.isDirectory(), true);
  assert.deepEqual(calls[0]?.args, ["acp"]);

  await client.close();
});
