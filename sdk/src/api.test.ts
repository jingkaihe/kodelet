import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdtemp, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { createTestHarness, defineExtension, renderTemplate, z } from "./index.js";

test("registers tools, commands, events and executes handlers", async () => {
  const extension = defineExtension((ext) => {
    ext.setMetadata({ name: "weather", version: "0.1.0" });

    const WeatherInput = z.object({ location: z.string() });
    ext.registerTool({
      name: "get_weather",
      description: "Get weather",
      inputSchema: WeatherInput,
      timeoutInSec: 600,
      execute(input) {
        return {
          content: `Weather for ${input.location}`,
          data: { location: input.location },
        };
      },
    });

    const DoctorInput = z.object({ verbose: z.boolean().default(false) });
    ext.registerCommand({
      name: "doctor",
      aliases: ["/doctor"],
      description: "Inspect extension health",
      inputSchema: DoctorInput,
      timeoutInSec: 30,
      async execute(input, ctx) {
        return {
          action: "respond",
          response: `${ctx.input.commandName}: ${input.verbose ? "healthy" : "ok"}`,
        };
      },
    });

    ext.on("tool.call", { priority: 10, timeoutInSec: 5 }, async (event) => {
      if (event.tool.name === "get_weather") {
        return { input: { location: "Paris" } };
      }
      return undefined;
    });

    ext.on("agent.end", () => ({ followUpMessages: ["inspect tests"] }));
  });

  const harness = await createTestHarness(extension);
  const init = harness.initialize({ extension: { id: "weather", cwd: process.cwd() } });
  assert.equal(init.name, "weather");
  assert.equal(init.version, "0.1.0");
  assert.equal(init.tools[0]?.name, "get_weather");
  assert.equal(init.tools[0]?.timeoutInSec, 600);
  assert.equal(init.tools[0]?.inputSchema.type, "object");
  assert.equal(init.commands[0]?.name, "doctor");
  assert.equal(init.commands[0]?.timeoutInSec, 30);
  assert.deepEqual(init.subscriptions, [
    { event: "tool.call", priority: 10, timeoutInSec: 5 },
    { event: "agent.end", priority: 0 },
  ]);

  const toolResult = await harness.executeTool({ name: "get_weather", input: { location: "London" } });
  assert.equal(toolResult.content, "Weather for London");
  assert.deepEqual(toolResult.data, { location: "London" });

  const commandResult = await harness.executeCommand({
    name: "doctor",
    input: { verbose: true },
    invocation: { raw: "/doctor verbose=true", commandName: "doctor", args: ["verbose=true"], flags: { verbose: "true" } },
  });
  assert.deepEqual(commandResult, { action: "respond", response: "doctor: healthy" });

  const eventResult = await harness.handleEvent({
    id: "evt_1",
    event: "tool.call",
    payload: { tool: { name: "get_weather", input: { location: "London" } } },
  });
  assert.deepEqual(eventResult, { input: { location: "Paris" } });

  const agentEndResult = await harness.handleEvent({
    id: "evt_2",
    event: "agent.end",
    payload: { messages: [{ role: "assistant", content: "done" }] },
  });
  assert.deepEqual(agentEndResult, { followUpMessages: ["inspect tests"] });
});

test("command validation can pass to the next route", async () => {
  const extension = defineExtension((ext) => {
    ext.registerCommand({
      name: "review",
      description: "Review code",
      inputSchema: z.object({ target: z.string() }),
      async execute(input) {
        return { action: "runAgent", prompt: `Review ${input.target}` };
      },
    });
  });

  const harness = await createTestHarness(extension);
  const result = await harness.executeCommand({
    name: "review",
    input: {},
    invocation: { raw: "/review", commandName: "review", args: [], flags: {} },
  });
  assert.deepEqual(result, { action: "pass" });
});

test("preserves explicit zero timeout and merges event timeout options", async () => {
  const extension = defineExtension((ext) => {
    ext.registerTool({
      name: "forever_tool",
      description: "Tool with no timeout",
      inputSchema: z.object({}),
      timeoutInSec: 0,
      execute() {
        return "ok";
      },
    });

    ext.registerCommand({
      name: "forever_command",
      description: "Command with no timeout",
      timeoutInSec: 0,
      execute() {
        return { action: "respond", response: "ok" };
      },
    });

    ext.on("tool.result", { priority: 1, timeoutInSec: 2 }, async () => undefined);
    ext.on("tool.result", { priority: 3, timeoutInSec: 0 }, async () => undefined);
    ext.on("agent.end", { timeoutInSec: 4 }, async () => undefined);
    ext.on("agent.end", { timeoutInSec: 6 }, async () => undefined);
  });

  const harness = await createTestHarness(extension);
  const init = harness.initialize({ extension: { id: "timeouts", cwd: process.cwd() } });

  assert.equal(init.tools[0]?.timeoutInSec, 0);
  assert.equal(init.commands[0]?.timeoutInSec, 0);
  assert.deepEqual(
    init.subscriptions.sort((a, b) => a.event.localeCompare(b.event)),
    [
      { event: "agent.end", priority: 0, timeoutInSec: 6 },
      { event: "tool.result", priority: 3, timeoutInSec: 0 },
    ],
  );
});

test("agent.init can patch the system prompt and tool list", async () => {
  const extension = defineExtension((ext) => {
    ext.on("agent.init", () => ({
      systemPrompt: { append: "Use safe tools only." },
      tools: { disable: ["bash"], enable: ["get_weather"] },
    }));
  });

  const harness = await createTestHarness(extension);
  const result = await harness.handleEvent({
    id: "evt_agent_init",
    event: "agent.init",
    payload: { systemPrompt: "base" },
  });

  assert.deepEqual(result, {
    systemPrompt: { append: "Use safe tools only." },
    tools: { disable: ["bash"], enable: ["get_weather"] },
  });
});

test("renders Mustache templates", () => {
  assert.equal(renderTemplate("Review {{target}} with {{focus}}", { target: "main", focus: "correctness" }), "Review main with correctness");
});

test("command context includes workspace, storage, env and process helpers", async () => {
  const workspace = await mkdtemp(path.join(os.tmpdir(), "kodelet-sdk-workspace-"));
  const dataDir = await mkdtemp(path.join(os.tmpdir(), "kodelet-sdk-data-"));
  await writeFile(path.join(workspace, "README.md"), "hello", "utf8");

  const extension = defineExtension((ext) => {
    ext.registerCommand({
      name: "open",
      description: "Open a path",
      inputSchema: z.object({ path: z.string().optional() }),
      async execute(input, ctx) {
        const target = ctx.path.resolveWorkspacePath(input.path ?? ".");
        const exists = await ctx.fs.exists(target);
        await ctx.storage.writeJson("state.json", { target: ctx.path.relativeToWorkspace(target) });
        const execResult = await ctx.process.exec(process.execPath, ["-e", "process.stdout.write('ok')"]);
        return {
          action: "respond",
          response: `${exists}:${ctx.path.relativeToWorkspace(target)}:${execResult.stdout}`,
        };
      },
    });
  });

  const harness = await createTestHarness(extension);
  harness.initialize({ extension: { id: "ctx", cwd: workspace, dataDir } });
  const result = await harness.executeCommand({
    name: "open",
    input: { path: "README.md" },
    context: { cwd: workspace },
    invocation: { raw: "/open README.md", commandName: "open", args: ["README.md"], flags: {} },
  });
  assert.deepEqual(result, { action: "respond", response: "true:README.md:ok" });
});

test("tool context can request host UI input", async () => {
  const extension = defineExtension((ext) => {
    ext.registerTool({
      name: "ask",
      description: "Ask for input",
      inputSchema: z.object({}),
      async execute(_, ctx) {
        const answer = await ctx.ui.input({
          title: "Pick one",
          helpText: "1. A\n2. B",
          submitButtonText: "Select",
        });
        return answer ? `answer=${answer}` : "dismissed";
      },
    });
  });

  const requests: Array<{ method: string; params?: unknown }> = [];
  const harness = await createTestHarness(extension, {
    async request(method, params) {
      requests.push({ method, params });
      return { status: "submitted", value: "2" };
    },
  });

  const result = await harness.executeTool({ name: "ask", input: {} });
  assert.deepEqual(result, { content: "answer=2" });
  assert.equal(requests[0]?.method, "kodelet.ui.input");
  assert.deepEqual(requests[0]?.params, {
    title: "Pick one",
    helpText: "1. A\n2. B",
    submitButtonText: "Select",
  });
});

test("runtime serves JSON-RPC over stdio", async (t) => {
  const extensionFile = path.join(await mkdtemp(path.join(os.tmpdir(), "kodelet-sdk-rpc-")), "extension.ts");
  await writeFile(
    extensionFile,
    `
      import { defineExtension, runExtension, z } from ${JSON.stringify(path.resolve("src/index.ts"))};

      runExtension(defineExtension((ext) => {
        ext.registerTool({
          name: "echo",
          description: "Echo text",
          inputSchema: z.object({ text: z.string() }),
          execute(input) {
            return { content: input.text.toUpperCase() };
          },
        });
      }));
    `,
    "utf8",
  );

  const child = spawn(process.execPath, ["--import", "tsx", extensionFile], {
    cwd: process.cwd(),
    stdio: ["pipe", "pipe", "pipe"],
  });
  t.after(() => child.kill());

  const client = new RpcTestClient(child.stdout, child.stdin);
  const init = await client.call("extension.initialize", {
    protocolVersion: "2026-05-30",
    kodelet: { version: "test" },
    extension: { id: "rpc", cwd: process.cwd(), dataDir: "" },
    capabilities: {},
  });
  assert.equal(init.name, "rpc");
  assert.equal(init.tools[0].name, "echo");

  const result = await client.call("extension.tool.execute", {
    name: "echo",
    input: { text: "hello" },
    context: { conversationId: "conv-rpc", cwd: process.cwd() },
  });
  assert.deepEqual(result, { content: "HELLO" });
});

test("runtime supports extension-initiated host RPC", async (t) => {
  const extensionFile = path.join(await mkdtemp(path.join(os.tmpdir(), "kodelet-sdk-host-rpc-")), "extension.ts");
  await writeFile(
    extensionFile,
    `
      import { defineExtension, runExtension, z } from ${JSON.stringify(path.resolve("src/index.ts"))};

      runExtension(defineExtension((ext) => {
        ext.registerTool({
          name: "ask",
          description: "Ask user",
          inputSchema: z.object({}),
          async execute(_, ctx) {
            const answer = await ctx.ui.input({ title: "Choose" });
            return { content: answer ?? "none" };
          },
        });
      }));
    `,
    "utf8",
  );

  const child = spawn(process.execPath, ["--import", "tsx", extensionFile], {
    cwd: process.cwd(),
    stdio: ["pipe", "pipe", "pipe"],
  });
  t.after(() => child.kill());

  const client = new RpcTestClient(child.stdout, child.stdin);
  const init = await client.call("extension.initialize", {
    protocolVersion: "2026-05-30",
    kodelet: { version: "test" },
    extension: { id: "rpc-ui", cwd: process.cwd(), dataDir: "" },
    capabilities: { ui: { input: true } },
  });
  assert.equal(init.tools[0].name, "ask");

  const result = await client.call("extension.tool.execute", {
    name: "ask",
    input: {},
    context: { conversationId: "conv-rpc", cwd: process.cwd() },
  });
  assert.deepEqual(client.hostRequests.map((request) => request.method), ["kodelet.ui.input"]);
  assert.deepEqual(result, { content: "from-host" });
});

class RpcTestClient {
  private buffer = Buffer.alloc(0);
  private nextId = 0;
  private waiters = new Map<number, { resolve(value: any): void; reject(error: Error): void }>();
  hostRequests: Array<{ id: number | string; method: string; params?: unknown }> = [];

  constructor(stdout: NodeJS.ReadableStream, private stdin: NodeJS.WritableStream) {
    stdout.on("data", (chunk: Buffer) => {
      this.buffer = Buffer.concat([this.buffer, chunk]);
      this.drain();
    });
  }

  call(method: string, params: unknown): Promise<any> {
    const id = ++this.nextId;
    const payload = JSON.stringify({ jsonrpc: "2.0", id, method, params });
    this.stdin.write(`Content-Length: ${Buffer.byteLength(payload)}\r\n\r\n${payload}`);
    return new Promise((resolve, reject) => {
      this.waiters.set(id, { resolve, reject });
    });
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
      const response = JSON.parse(this.buffer.subarray(start, end).toString("utf8"));
      this.buffer = this.buffer.subarray(end);
      if (response.method) {
        this.hostRequests.push(response);
        const result = response.method === "kodelet.ui.input" ? { status: "submitted", value: "from-host" } : undefined;
        const payload = JSON.stringify({ jsonrpc: "2.0", id: response.id, result });
        this.stdin.write(`Content-Length: ${Buffer.byteLength(payload)}\r\n\r\n${payload}`);
        continue;
      }
      const waiter = this.waiters.get(response.id);
      if (!waiter) {
        continue;
      }
      this.waiters.delete(response.id);
      if (response.error) {
        waiter.reject(new Error(response.error.message));
      } else {
        waiter.resolve(response.result);
      }
    }
  }
}
