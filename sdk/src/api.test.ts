import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdtemp, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { createTestHarness, defineExtension, renderTemplate, z, type JSONSchema } from "./index.js";

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

test("registerTool preserves raw JSON Schema and passes input through", async () => {
  const inputSchema = {
    $schema: "https://json-schema.org/draft/2020-12/schema",
    type: "object",
    description: "A constrained search request",
    properties: {
      mode: {
        description: "Search strategy",
        enum: ["fast", "thorough"],
      },
      query: {
        type: "string",
        pattern: "^[a-z]+$",
        minLength: 3,
      },
      limit: {
        anyOf: [
          { type: "integer", minimum: 1, maximum: 10 },
          { const: "all" },
        ],
      },
    },
    required: ["mode", "query"],
    additionalProperties: false,
  } satisfies JSONSchema;

  const extension = defineExtension((ext) => {
    ext.registerTool({
      name: "raw_schema",
      description: "Use a raw JSON Schema",
      inputSchema,
      execute(input) {
        return JSON.stringify(input);
      },
    });
  });

  const harness = await createTestHarness(extension);
  const init = harness.initialize({ extension: { id: "raw-schema", cwd: process.cwd() } });
  assert.deepEqual(init.tools[0]?.inputSchema, inputSchema);

  const valid = { mode: "fast", query: "code", limit: 3 };
  assert.equal((await harness.executeTool({ name: "raw_schema", input: valid })).content, JSON.stringify(valid));
  const unconstrained = { mode: "server-validates", extra: true };
  assert.equal((await harness.executeTool({ name: "raw_schema", input: unconstrained })).content, JSON.stringify(unconstrained));
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
    ext.on("tool.update", { priority: 2, timeoutInSec: 1 }, async () => undefined);
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
      { event: "tool.update", priority: 2, timeoutInSec: 1 },
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

test("tool context can request host UI interactions", async () => {
  const extension = defineExtension((ext) => {
    ext.registerTool({
      name: "ask",
      description: "Ask for UI interactions",
      inputSchema: z.object({}),
      async execute(_, ctx) {
        await ctx.update("Working", { step: 1 });
        const answer = await ctx.ui.input({
          title: "Pick one",
          helpText: "1. A\n2. B",
          submitButtonText: "Select",
        });
        const confirmed = await ctx.ui.confirm({ title: "Allow?", message: "A tool call incoming" });
        const selection = await ctx.ui.select({ title: "Food", options: ["Pasta", "Pizza"] });
        await ctx.ui.notify("Done");
        return `answer=${answer};confirmed=${confirmed};selection=${selection}`;
      },
    });
  });

  const requests: Array<{ method: string; params?: unknown }> = [];
  const harness = await createTestHarness(extension, {
    async request(method, params) {
      requests.push({ method, params });
      if (method === "kodelet.ui.confirm") {
        return { status: "submitted", confirmed: true };
      }
      if (method === "kodelet.ui.select") {
        return { status: "submitted", value: "Pizza" };
      }
      return { status: "submitted", value: "2" };
    },
  });

  const result = await harness.executeTool({ name: "ask", input: {} });
  assert.deepEqual(result, { content: "answer=2;confirmed=true;selection=Pizza" });
  assert.deepEqual(requests.map((request) => request.method), [
    "kodelet.tool.update",
    "kodelet.ui.input",
    "kodelet.ui.confirm",
    "kodelet.ui.select",
    "kodelet.ui.notify",
  ]);
  assert.deepEqual(requests[0]?.params, { content: "Working", data: { step: 1 } });
  assert.deepEqual(requests[1]?.params, {
    title: "Pick one",
    helpText: "1. A\n2. B",
    submitButtonText: "Select",
  });
  assert.deepEqual(requests[2]?.params, { title: "Allow?", message: "A tool call incoming" });
  assert.deepEqual(requests[3]?.params, { title: "Food", options: ["Pasta", "Pizza"] });
  assert.deepEqual(requests[4]?.params, { message: "Done" });
});

test("tool updates are ignored when the host does not advertise support", async () => {
  const extension = defineExtension((ext) => {
    ext.registerTool({
      name: "stream",
      description: "Stream progress",
      inputSchema: z.object({}),
      async execute(_input, ctx) {
        await ctx.update("Working", { step: 1 });
        return "done";
      },
    });
  });
  const requests: string[] = [];
  const harness = await createTestHarness(extension, {
    async request(method) {
      requests.push(method);
      return { accepted: true };
    },
  });

  harness.initialize({ capabilities: {} });
  assert.deepEqual(await harness.executeTool({ name: "stream", input: {} }), { content: "done" });
  assert.deepEqual(requests, []);
});

test("widgets use sequences and surfaces route host events", async () => {
  let openedSurface: any;
  const inputEvents: unknown[] = [];
  const resizeEvents: unknown[] = [];
  const requests: Array<{ method: string; params?: unknown }> = [];
  const notificationHandlers = new Set<(method: string, params: unknown) => void>();
  const host = {
    async request(method: string, params?: unknown) {
      requests.push({ method, params });
      return { accepted: true, latestSequence: 1 };
    },
    onNotification(handler: (method: string, params: unknown) => void) {
      notificationHandlers.add(handler);
      return () => notificationHandlers.delete(handler);
    },
  };

  const extension = defineExtension((ext) => {
    ext.registerCommand({
      name: "ui",
      description: "Open extension UI",
      async execute(_input, ctx) {
        await ctx.ui.setWidget("status", [
          "ready",
          { spans: [{ text: "green", style: { foreground: "#00ff00", bold: true } }] },
        ]);
        await ctx.ui.setWidget("status", ["updated"], { placement: "belowComposer" });
        await ctx.ui.setWidget("status", undefined);
        await ctx.ui.appendTranscript({ title: "Saved", message: "./drawing.png" });
        openedSurface = await ctx.ui.openSurface({
          id: "game",
          initialLines: ["loading"],
          width: "75%",
          maxHeight: "95%",
          anchor: "center",
        });
        openedSurface.onInput((event: unknown) => inputEvents.push(event));
        openedSurface.onResize((event: unknown) => resizeEvents.push(event));
        return { action: "respond", response: "opened" };
      },
    });
  });

  const harness = await createTestHarness(extension, host);
  harness.initialize({ capabilities: { ui: { widgets: true, surfaces: true, transcript: true } } });
  await harness.executeCommand({
    name: "ui",
    invocation: { raw: "/ui", commandName: "ui", args: [], flags: {} },
  });

  assert.deepEqual(requests.slice(0, 5), [
    {
      method: "kodelet.ui.widget.set",
      params: {
        id: "status",
        placement: "aboveComposer",
        frame: {
          sequence: 1,
          lines: ["ready", { spans: [{ text: "green", style: { foreground: "#00ff00", bold: true } }] }],
        },
      },
    },
    {
      method: "kodelet.ui.widget.set",
      params: { id: "status", placement: "belowComposer", frame: { sequence: 2, lines: ["updated"] } },
    },
    { method: "kodelet.ui.widget.remove", params: { id: "status", sequence: 3 } },
    { method: "kodelet.ui.transcript.append", params: { title: "Saved", message: "./drawing.png" } },
    {
      method: "kodelet.ui.surface.open",
      params: {
        id: "game",
        options: { width: "75%", maxHeight: "95%", anchor: "center" },
        frame: { sequence: 1, lines: ["loading"] },
      },
    },
  ]);

  for (const handler of notificationHandlers) {
    handler("extension.ui.surface.resize", { id: "game", sequence: 1, width: 80, height: 20 });
    handler("extension.ui.surface.input", { id: "game", sequence: 2, kind: "key", key: "q", text: "q" });
    handler("extension.ui.surface.resize", { id: "game", sequence: 1, width: 1, height: 1 });
  }
  assert.deepEqual(openedSurface.size, { width: 80, height: 20 });
  assert.deepEqual(resizeEvents, [{ sequence: 1, width: 80, height: 20 }]);
  assert.deepEqual(inputEvents, [{ id: "game", sequence: 2, kind: "key", key: "q", text: "q" }]);

  await openedSurface.close();
  assert.deepEqual(requests.at(-1), {
    method: "kodelet.ui.surface.close",
    params: { id: "game", sequence: 2 },
  });
});

test("surface IDs reject overlapping ownership until the active handle closes", async () => {
  let openedSurface: any;
  let releaseFirstOpen: ((response: { accepted: boolean }) => void) | undefined;
  let releaseFirstClose: ((response: { accepted: boolean }) => void) | undefined;
  let openRequests = 0;
  let closeRequests = 0;
  const requests: Array<{ method: string; params?: unknown }> = [];
  const host = {
    async request(method: string, params?: unknown) {
      requests.push({ method, params });
      if (method === "kodelet.ui.surface.open" && openRequests++ === 0) {
        return await new Promise<{ accepted: boolean }>((resolve) => {
          releaseFirstOpen = resolve;
        });
      }
      if (method === "kodelet.ui.surface.close" && closeRequests++ === 0) {
        return await new Promise<{ accepted: boolean }>((resolve) => {
          releaseFirstClose = resolve;
        });
      }
      return { accepted: true };
    },
  };
  const extension = defineExtension((ext) => {
    ext.registerCommand({
      name: "exclusive-surface",
      description: "Enforce exclusive surface IDs",
      async execute(_input, ctx) {
        const firstOpen = ctx.ui.openSurface({ id: "singleton" });
        await assert.rejects(ctx.ui.openSurface({ id: "singleton" }), /already open, opening, or closing/);
        releaseFirstOpen?.({ accepted: true });
        openedSurface = await firstOpen;
        await assert.rejects(ctx.ui.openSurface({ id: "singleton" }), /already open, opening, or closing/);
        const firstClose = openedSurface.close();
        await assert.rejects(ctx.ui.openSurface({ id: "singleton" }), /already open, opening, or closing/);
        releaseFirstClose?.({ accepted: true });
        await firstClose;
        openedSurface = await ctx.ui.openSurface({ id: "singleton" });
        return { action: "respond", response: "opened" };
      },
    });
  });

  const harness = await createTestHarness(extension, host);
  harness.initialize({ capabilities: { ui: { surfaces: true } } });
  await harness.executeCommand({
    name: "exclusive-surface",
    invocation: { raw: "/exclusive-surface", commandName: "exclusive-surface", args: [], flags: {} },
  });

  assert.deepEqual(
    requests.map((request) => request.method),
    ["kodelet.ui.surface.open", "kodelet.ui.surface.close", "kodelet.ui.surface.open"],
  );
  assert.deepEqual(
    requests.map((request) => {
      const params = request.params as { sequence?: number; frame?: { sequence?: number } };
      return params.sequence ?? params.frame?.sequence;
    }),
    [1, 2, 3],
  );
  await openedSurface.close();
  assert.deepEqual(requests.at(-1), {
    method: "kodelet.ui.surface.close",
    params: { id: "singleton", sequence: 4 },
  });
});

test("surface presentation keeps at most one frame in flight and one latest pending frame", async () => {
  let openedSurface: any;
  const notifications: Array<{ method: string; params?: unknown }> = [];
  const releaseNotifications: Array<() => void> = [];
  const notificationHandlers = new Set<(method: string, params: unknown) => void>();
  const host = {
    async request() {
      return { accepted: true };
    },
    notify(method: string, params?: unknown) {
      notifications.push({ method, params });
      return new Promise<void>((resolve) => releaseNotifications.push(resolve));
    },
    onNotification(handler: (method: string, params: unknown) => void) {
      notificationHandlers.add(handler);
      return () => notificationHandlers.delete(handler);
    },
  };
  const extension = defineExtension((ext) => {
    ext.registerCommand({
      name: "bounded-ui",
      description: "Open a bounded presentation surface",
      async execute(_input, ctx) {
        openedSurface = await ctx.ui.openSurface({ id: "bounded" });
        return { action: "respond", response: "opened" };
      },
    });
  });

  const harness = await createTestHarness(extension, host);
  harness.initialize({ capabilities: { ui: { surfaces: true } } });
  await harness.executeCommand({
    name: "bounded-ui",
    invocation: { raw: "/bounded-ui", commandName: "bounded-ui", args: [], flags: {} },
  });

  openedSurface.update(["frame 1"]);
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(notifications.length, 1);

  openedSurface.update(["frame 2"]);
  openedSurface.update(["frame 3"]);
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(notifications.length, 1, "a blocked transport must not accumulate writes");

  releaseNotifications.shift()?.();
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(notifications, [
    {
      method: "kodelet.ui.surface.frame",
      params: { id: "bounded", frame: { sequence: 2, lines: ["frame 1"] } },
    },
    {
      method: "kodelet.ui.surface.frame",
      params: { id: "bounded", frame: { sequence: 3, lines: ["frame 3"] } },
    },
  ]);

  releaseNotifications.shift()?.();
  await new Promise((resolve) => setImmediate(resolve));
  await openedSurface.close();
});

test("surface routing retains initial events until open listeners attach", async () => {
  let openedSurface: any;
  let unsubscribeInput: (() => void) | undefined;
  let unsubscribeResize: (() => void) | undefined;
  const inputEvents: unknown[] = [];
  const resizeEvents: unknown[] = [];
  const notificationHandlers = new Set<(method: string, params: unknown) => void>();
  const host = {
    async request(method: string) {
      if (method === "kodelet.ui.surface.open") {
        for (const handler of notificationHandlers) {
          handler("extension.ui.surface.resize", { id: "early", sequence: 1, width: 72, height: 18 });
          handler("extension.ui.surface.input", { id: "early", sequence: 2, kind: "key", key: "x", text: "x" });
          handler("extension.ui.surface.input", { id: "early", sequence: 3, kind: "focus" });
          handler("extension.ui.surface.input", {
            id: "early",
            sequence: 4,
            kind: "mouse",
            mouse: { x: 1, y: 1, button: "none", action: "motion" },
          });
        }
      }
      return { accepted: true };
    },
    onNotification(handler: (method: string, params: unknown) => void) {
      notificationHandlers.add(handler);
      return () => notificationHandlers.delete(handler);
    },
  };
  const extension = defineExtension((ext) => {
    ext.registerCommand({
      name: "early-resize",
      description: "Open a surface that receives an immediate resize",
      async execute(_input, ctx) {
        openedSurface = await ctx.ui.openSurface({ id: "early" });
        unsubscribeResize = openedSurface.onResize((event: unknown) => resizeEvents.push(event));
        unsubscribeInput = openedSurface.onInput((event: unknown) => inputEvents.push(event));
        return { action: "respond", response: "opened" };
      },
    });
  });

  const harness = await createTestHarness(extension, host);
  harness.initialize({ capabilities: { ui: { surfaces: true } } });
  await harness.executeCommand({
    name: "early-resize",
    invocation: { raw: "/early-resize", commandName: "early-resize", args: [], flags: {} },
  });

  assert.deepEqual(openedSurface.size, { width: 72, height: 18 });
  assert.deepEqual(resizeEvents, [{ sequence: 1, width: 72, height: 18 }]);
  assert.deepEqual(inputEvents, [{ id: "early", sequence: 3, kind: "focus" }]);
  unsubscribeResize?.();
  unsubscribeInput?.();
  for (const handler of notificationHandlers) {
    handler("extension.ui.surface.input", { id: "early", sequence: 5, kind: "key", key: "x", text: "x" });
    handler("extension.ui.surface.input", { id: "early", sequence: 6, kind: "blur" });
    handler("extension.ui.surface.resize", { id: "early", sequence: 7, width: 73, height: 19 });
  }
  const replayedEvents: unknown[] = [];
  openedSurface.onInput((event: unknown) => replayedEvents.push(event));
  assert.deepEqual(replayedEvents, []);
  const replayedResizeEvents: unknown[] = [];
  openedSurface.onResize((event: unknown) => replayedResizeEvents.push(event));
  assert.deepEqual(replayedResizeEvents, []);
  await openedSurface.close();
});

test("UI object IDs reject surrounding whitespace before routing or sequencing", async () => {
  const requests: Array<{ method: string; params?: unknown }> = [];
  const extension = defineExtension((ext) => {
    ext.registerCommand({
      name: "validated-ui",
      description: "Validate UI object identifiers",
      async execute(_input, ctx) {
        await assert.rejects(ctx.ui.setWidget(" status ", ["invalid"]), /leading or trailing whitespace/);
        await ctx.ui.setWidget("status", ["valid"]);
        await assert.rejects(ctx.ui.openSurface({ id: " game " }), /leading or trailing whitespace/);
        const surface = await ctx.ui.openSurface({ id: "game" });
        await surface.close();
        return { action: "respond", response: "validated" };
      },
    });
  });
  const harness = await createTestHarness(extension, {
    async request(method, params) {
      requests.push({ method, params });
      return { accepted: true };
    },
  });
  harness.initialize({ capabilities: { ui: { widgets: true, surfaces: true } } });

  await harness.executeCommand({
    name: "validated-ui",
    invocation: { raw: "/validated-ui", commandName: "validated-ui", args: [], flags: {} },
  });

  assert.deepEqual(
    requests.map((request) => request.method),
    ["kodelet.ui.widget.set", "kodelet.ui.surface.open", "kodelet.ui.surface.close"],
  );
  assert.deepEqual(
    requests.map((request) => {
      const params = request.params as { sequence?: number; frame?: { sequence?: number } };
      return params.sequence ?? params.frame?.sequence;
    }),
    [1, 1, 2],
  );
});

test("persistent UI APIs are capability gated", async () => {
  const requests: string[] = [];
  const extension = defineExtension((ext) => {
    ext.registerCommand({
      name: "ui",
      description: "Try extension UI",
      async execute(_input, ctx) {
        await ctx.ui.setWidget("status", ["ignored"]);
        await ctx.ui.appendTranscript("ignored");
        await assert.rejects(ctx.ui.openSurface({ id: "missing" }), /not available/);
        return { action: "respond", response: "done" };
      },
    });
  });
  const harness = await createTestHarness(extension, {
    async request(method) {
      requests.push(method);
      return {};
    },
  });
  harness.initialize({ capabilities: {} });

  await harness.executeCommand({
    name: "ui",
    invocation: { raw: "/ui", commandName: "ui", args: [], flags: {} },
  });

  assert.deepEqual(requests, []);
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
            await ctx.update("Working", { step: 1 });
            const answer = await ctx.ui.input({ title: "Choose" });
            const confirmed = await ctx.ui.confirm({ title: "Allow?" });
            const selection = await ctx.ui.select({ title: "Food", options: ["Pasta", "Pizza"] });
            await ctx.ui.notify("Done");
            return { content: [answer ?? "none", confirmed, selection ?? "none"].join(":") };
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
    capabilities: { toolUpdates: true, ui: { input: true } },
  });
  assert.equal(init.tools[0].name, "ask");

  const result = await client.call("extension.tool.execute", {
    name: "ask",
    input: {},
    context: { conversationId: "conv-rpc", cwd: process.cwd() },
  });
  assert.deepEqual(client.hostRequests.map((request) => request.method), [
    "kodelet.tool.update",
    "kodelet.ui.input",
    "kodelet.ui.confirm",
    "kodelet.ui.select",
    "kodelet.ui.notify",
  ]);
  assert.deepEqual(client.hostRequests.map((request) => request.parentId), [2, 2, 2, 2, 2]);
  assert.deepEqual(client.hostRequests[0]?.params, { content: "Working", data: { step: 1 } });
  assert.deepEqual(result, { content: "from-host:true:Pizza" });
});

test("runtime keeps interactive surfaces alive after the opening command returns", async (t) => {
  const extensionFile = path.join(await mkdtemp(path.join(os.tmpdir(), "kodelet-sdk-surface-rpc-")), "extension.ts");
  await writeFile(
    extensionFile,
    `
      import { defineExtension, runExtension } from ${JSON.stringify(path.resolve("src/index.ts"))};

      runExtension(defineExtension((ext) => {
        ext.registerCommand({
          name: "game",
          description: "Open a persistent surface",
          async execute(_, ctx) {
            const surface = await ctx.ui.openSurface({ id: "game", initialLines: ["loading"], width: "50%" });
            surface.onResize((event) => {
              surface.update(["size=" + event.width + "x" + event.height]);
              void ctx.ui.appendTranscript({ title: "Resized", message: event.width + "x" + event.height });
            });
            surface.onInput((event) => {
              surface.update(["key=" + event.key + ";size=" + surface.size?.width + "x" + surface.size?.height]);
              if (event.key === "q") setTimeout(() => void surface.close(), 0);
            });
            return { action: "respond", response: "opened" };
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
  await client.call("extension.initialize", {
    protocolVersion: "2026-05-30",
    extension: { id: "surface", cwd: process.cwd(), dataDir: "" },
    capabilities: { ui: { surfaces: true, transcript: true } },
  });
  const result = await client.call("extension.command.execute", {
    name: "game",
    input: {},
    invocation: { raw: "/game", commandName: "game", args: [], flags: {} },
  });
  assert.deepEqual(result, { action: "respond", response: "opened" });
  const openRequest = client.hostRequests.find((request) => request.method === "kodelet.ui.surface.open");
  assert.equal(openRequest?.parentId, 2);

  client.notify("extension.ui.surface.resize", { id: "game", sequence: 1, width: 60, height: 18 });
  await client.waitForHostNotifications(1);
  assert.deepEqual(client.hostNotifications[0], {
    method: "kodelet.ui.surface.frame",
    params: { id: "game", frame: { sequence: 2, lines: ["size=60x18"] } },
  });
  await client.waitForHostRequests(2);
  const transcriptRequest = client.hostRequests.find((request) => request.method === "kodelet.ui.transcript.append");
  assert.equal(transcriptRequest?.parentId, undefined);
  assert.deepEqual(transcriptRequest?.params, { title: "Resized", message: "60x18" });

  const requestCountBeforeInput = client.hostRequests.length;
  client.notify("extension.ui.surface.input", { id: "game", sequence: 2, kind: "key", key: "q", text: "q" });
  await client.waitForHostNotifications(2);
  assert.deepEqual(client.hostNotifications[1], {
    method: "kodelet.ui.surface.frame",
    params: { id: "game", frame: { sequence: 3, lines: ["key=q;size=60x18"] } },
  });
  await client.waitForHostRequests(requestCountBeforeInput + 1);
  const closeRequest = client.hostRequests.find((request) => request.method === "kodelet.ui.surface.close");
  assert.equal(closeRequest?.parentId, undefined);
  assert.deepEqual(closeRequest?.params, { id: "game", sequence: 4 });
});

test("runtime cancellation aborts handlers and blocks late host RPC", async (t) => {
  const extensionFile = path.join(await mkdtemp(path.join(os.tmpdir(), "kodelet-sdk-cancel-rpc-")), "extension.ts");
  await writeFile(
    extensionFile,
    `
      import { defineExtension, runExtension, z } from ${JSON.stringify(path.resolve("src/index.ts"))};

      runExtension(defineExtension((ext) => {
        ext.registerTool({
          name: "wait",
          description: "Wait for cancellation",
          inputSchema: z.object({ finishAbort: z.boolean().optional(), quick: z.boolean().optional() }),
          async execute(input, ctx) {
            if (input.quick) return "quick result";
            if (input.finishAbort) {
              ctx.signal.addEventListener("abort", () => {
                void ctx.update("completion update").catch(() => undefined);
              }, { once: true });
              return "finish result";
            }
            await ctx.update("started");
            await new Promise((resolve) => {
              if (ctx.signal.aborted) resolve(undefined);
              else ctx.signal.addEventListener("abort", () => resolve(undefined), { once: true });
            });
            try { await ctx.update("stale update"); } catch {}
            process.stderr.write("CANCELLED\\n");
            return "late result";
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
  const cancelled = new Promise<void>((resolve) => {
    child.stderr.on("data", (chunk) => {
      if (String(chunk).includes("CANCELLED")) resolve();
    });
  });
  const client = new RpcTestClient(child.stdout, child.stdin);
  await client.call("extension.initialize", {
    protocolVersion: "2026-05-30",
    extension: { id: "cancellable", cwd: process.cwd(), dataDir: "" },
    capabilities: { toolUpdates: true },
  });

  const pending = client.beginCall("extension.tool.execute", { name: "wait", input: {} });
  void pending.response.catch(() => undefined);
  await client.waitForHostRequests(1);
  client.notify("$/cancelRequest", { id: pending.id });
  await cancelled;

  const result = await client.callWithId(pending.id, "extension.tool.execute", {
    name: "wait",
    input: { quick: true },
  });
  assert.deepEqual(result, { content: "quick result" });
  const finishResult = await client.call("extension.tool.execute", {
    name: "wait",
    input: { finishAbort: true },
  });
  assert.deepEqual(finishResult, { content: "finish result" });
  await new Promise((resolve) => setTimeout(resolve, 20));
  assert.deepEqual(client.hostRequests.map((request) => request.params), [{ content: "started" }]);
});

class RpcTestClient {
  private buffer = Buffer.alloc(0);
  private nextId = 0;
  private waiters = new Map<number, { resolve(value: any): void; reject(error: Error): void }>();
  hostRequests: Array<{ id: number | string; parentId?: number | string; method: string; params?: unknown }> = [];
  hostNotifications: Array<{ method: string; params?: unknown }> = [];

  constructor(stdout: NodeJS.ReadableStream, private stdin: NodeJS.WritableStream) {
    stdout.on("data", (chunk: Buffer) => {
      this.buffer = Buffer.concat([this.buffer, chunk]);
      this.drain();
    });
  }

  call(method: string, params: unknown): Promise<any> {
    return this.beginCall(method, params).response;
  }

  beginCall(method: string, params: unknown): { id: number; response: Promise<any> } {
    const id = ++this.nextId;
    return this.beginCallWithId(id, method, params);
  }

  callWithId(id: number, method: string, params: unknown): Promise<any> {
    return this.beginCallWithId(id, method, params).response;
  }

  private beginCallWithId(id: number, method: string, params: unknown): { id: number; response: Promise<any> } {
    this.nextId = Math.max(this.nextId, id);
    const payload = JSON.stringify({ jsonrpc: "2.0", id, method, params });
    this.stdin.write(`Content-Length: ${Buffer.byteLength(payload)}\r\n\r\n${payload}`);
    const response = new Promise<any>((resolve, reject) => {
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

  async waitForHostNotifications(count: number): Promise<void> {
    while (this.hostNotifications.length < count) {
      await new Promise((resolve) => setTimeout(resolve, 5));
    }
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
        if (response.id === undefined || response.id === null) {
          this.hostNotifications.push({ method: response.method, params: response.params });
          continue;
        }
        this.hostRequests.push(response);
        let result: unknown;
        switch (response.method) {
          case "kodelet.ui.input":
            result = { status: "submitted", value: "from-host" };
            break;
          case "kodelet.ui.confirm":
            result = { status: "submitted", confirmed: true };
            break;
          case "kodelet.ui.select":
            result = { status: "submitted", value: "Pizza" };
            break;
          case "kodelet.ui.notify":
            result = { status: "submitted" };
            break;
          case "kodelet.ui.widget.set":
          case "kodelet.ui.widget.remove":
          case "kodelet.ui.surface.open":
          case "kodelet.ui.surface.close":
            result = { accepted: true, latestSequence: response.params?.sequence ?? response.params?.frame?.sequence ?? 0 };
            break;
        }
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
