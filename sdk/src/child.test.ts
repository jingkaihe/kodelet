import assert from "node:assert/strict";
import test from "node:test";
import { setTimeout as delay } from "node:timers/promises";
import { ExtensionHost } from "./api.js";
import { createChildClient, type ChildEvent, type ChildRequest, type ChildResult } from "./child.js";
import { createToolContext, runWithHostRPCClient } from "./context.js";
import type { ExecutionProfile } from "./execution.js";

const identity: ChildResult = { conversationId: "child", runId: "run-child", parentConversationId: "parent", parentRunId: "run-parent", extensionId: "search", profile: "code_search", done: false };

test("presets are isolated, strict and snapshotted at registration", () => {
  const profile: ExecutionProfile = { name: "code_search", systemPromptPath: "search.md", options: { model: "gpt-4o-mini", allowedTools: ["file_read", "grep_tool", "glob_tool"], noExtensions: true, noSkills: true } };
  const one = new ExtensionHost();
  const two = new ExtensionHost();
  one.registerProfile(profile);
  two.registerProfile({ name: profile.name });
  profile.options!.allowedTools!.push("bash");
  const snapshot = one.initialize({ protocolVersion: "1", extension: { id: "one" } });
  assert.deepEqual(snapshot.profiles?.[0]?.options?.allowedTools, ["file_read", "grep_tool", "glob_tool"]);
  snapshot.profiles![0].name = "mutated";
  assert.equal(one.initialize({ protocolVersion: "1", extension: { id: "one" } }).profiles?.[0]?.name, "code_search");
  assert.throws(() => one.registerProfile(profile), /Duplicate/);
  assert.throws(() => two.registerProfile({ name: "bad", options: { apiKey: "secret" } } as ExecutionProfile));
  assert.throws(() => two.registerProfile({ name: "bad", options: { allowedTools: null } } as unknown as ExecutionProfile));
});

test("child RPC uses tool context and explicit retained lease, without subprocess fallback", async () => {
  const calls: Array<{ method: string; params: unknown; persistent: boolean }> = [];
  const client = {
    async request(method: string, params: unknown) { calls.push({ method, params, persistent: false }); return identity; },
    async requestPersistent(method: string, params: unknown) { calls.push({ method, params, persistent: true }); return { ...identity, done: true, output: "found", events: [{ sequence: 1, kind: "tool-call", toolName: "grep_tool" }] }; },
  };
  const ctx = await runWithHostRPCClient(client, async () => createToolContext(undefined));
  const child = await ctx.children.start({ profile: "code_search", message: "find parser", requestId: "once", lease: { id: "lease", close: async () => {} } });
  const events: string[] = [];
  assert.equal((await child.wait({ onEvent: (event) => { events.push(event.kind); } })).output, "found");
  assert.equal(child.runId, "run-child");
  assert.deepEqual(events, ["tool-call"]);
  assert.equal(calls[0].persistent, false);
  assert.equal(calls[1].persistent, true);
  await child.cancel();
  assert.deepEqual(calls.at(-1), { method: "kodelet.child.cancel", params: { childId: "child", childRunId: "run-child", leaseId: "lease" }, persistent: true });
  await assert.rejects(createChildClient(undefined).start({ profile: "search", message: "query" }), /Delegated tasks require an authenticated runner connection/);
  await assert.rejects(ctx.children.start({ profile: "search", message: "query", lease: { close: async () => {} } }), /valid runner background task lease/);
});

test("foreground child abort cancels the exact ID and unauthorized admission fails closed", async () => {
  const calls: string[] = [];
  const child = await createChildClient({ async request(method) { calls.push(method); return identity; } }).start({ profile: "search", message: "query" });
  await assert.rejects(child.wait({ signal: AbortSignal.abort(new Error("stop")) }), /stop/);
  assert.deepEqual(calls, ["kodelet.child.start", "kodelet.child.cancel"]);
  await assert.rejects(createChildClient({ async request() { throw new Error("stale authority"); } }).start({ profile: "search", message: "query" }), /stale authority/);
});

class Host {
  calls: Array<{ method: string; params: Record<string, unknown>; persistent: boolean }> = [];
  result: ChildResult = { ...identity };
  steerResult: unknown = { outcome: "injected" };
  async request(method: string, params: unknown) { return this.reply(method, params, false); }
  async requestPersistent(method: string, params: unknown) { return this.reply(method, params, true); }
  reply(method: string, params: unknown, persistent: boolean): unknown {
    this.calls.push({ method, params: params as Record<string, unknown>, persistent });
    return method === "kodelet.child.steer" ? this.steerResult : this.result;
  }
}

test("child progress preserves optional tool metadata and monotonic callbacks on the exact run", async () => {
  for (const retained of [false, true]) {
    const events: ChildEvent[] = [
      { sequence: 1, kind: "tool-call", toolName: "grep_tool", toolCallId: "search-1", input: '{ "pattern": "π.*", "path": "src" }\n' },
      { sequence: 2, kind: "tool-result", toolName: "grep_tool", toolCallId: "search-1", toolOutput: "src/parser.ts:2: π\n", success: true },
      { sequence: 3, kind: "tool-call", toolName: "file_read", toolCallId: "read-1", input: '{"file_path":"missing.ts"' },
      { sequence: 4, kind: "tool-result", toolName: "file_read", toolCallId: "read-1", toolOutput: "", success: false, error: "missing.ts: not found" },
      { sequence: 130, kind: "tool-result", toolName: "glob_tool", toolCallId: "legacy-1", text: "legacy result without metadata" },
    ];
    const final: ChildResult = { ...identity, done: true, output: "summary", events: events.slice(2) };
    const pages: ChildResult[] = [
      { ...identity, events: events.slice(0, 1) },
      { ...identity, events: events.slice(0, 3) },
      final,
    ];
    const host = new Host();
    host.reply = (method, params, persistent) => {
      host.calls.push({ method, params: params as Record<string, unknown>, persistent });
      const page = pages.shift();
      assert.ok(page, "unexpected extra RPC");
      return JSON.parse(JSON.stringify(page));
    };
    const lease = retained ? { id: "lease", close: async () => {} } : undefined;
    const child = await createChildClient(host).start({ profile: "search", message: "query", lease });
    const observed: ChildEvent[] = [];
    const result = await child.wait({ onEvent: async (event) => { await Promise.resolve(); observed.push(event); } });
    assert.deepEqual(result, final);
    assert.deepEqual(observed, events);
    assert.equal(observed[3].success, false);
    assert.equal(Object.hasOwn(observed[4], "success"), false);
    assert.deepEqual(host.calls.slice(1), [1, 3].map((after) => ({
      method: "kodelet.child.read", persistent: retained,
      params: { childId: "child", childRunId: "run-child", after, ...(retained ? { leaseId: "lease" } : {}) },
    })));
    // Direct reads keep the wire metadata too; a later run cannot replace it.
    pages.push(final);
    assert.deepEqual(await child.read(), final);
    assert.equal(host.calls.at(-1)?.params.after, 130);
    pages.push({ ...final, runId: "later-run", events: [{ ...events[3], sequence: 131 }] });
    await assert.rejects(child.read(), /does not match this execution/);
    assert.equal(child.runId, "run-child");
    assert.deepEqual(await child.wait({ onEvent: (event) => { observed.push(event); } }), final);
    assert.deepEqual(observed, events);
    assert.deepEqual(pages, []);
  }
});

test("fresh and fork context selection preserve typed options and exact read identity", async () => {
  for (const mode of [{}, { contextMode: "fresh" as const }, { contextMode: "fork" as const }]) {
    const host = new Host();
    const request = { profile: "search", message: "query", requestId: "first", ...mode,
      options: { noTools: false, allowedCommands: [], maxTurns: 0 }, cwd: "/runner/project", systemPrompt: "runner-owned prompt" };
    const child = await createChildClient(host).start(request);
    assert.deepEqual(host.calls, [{ method: "kodelet.child.start", params: request, persistent: false }]);
    await child.read();
    assert.deepEqual(host.calls.at(-1), { method: "kodelet.child.read", params: { childId: "child", childRunId: "run-child", after: 0 }, persistent: false });
  }
});

test("retained followup keeps the conversation but gets a new run and request identity", async () => {
  const host = new Host();
  const client = createChildClient(host);
  const lease = { id: "lease", close: async () => {} };
  const first = await client.start({ profile: "search", message: "first", contextMode: "fork", lease });
  host.result = { ...identity, runId: "second-run" };
  const second = await client.start({ profile: "search", message: "next", resume: first.conversationId, lease });
  assert.equal(first.conversationId, second.conversationId);
  assert.notEqual(first.runId, second.runId);
  assert.notEqual(host.calls[0].params.requestId, host.calls[1].params.requestId);
  assert.equal(host.calls[0].persistent, false);
  assert.equal(host.calls[1].persistent, true);
  assert.equal(host.calls[1].params.resume, first.conversationId);
  await assert.rejects(first.read(), /does not match/);
  await first.cancel();
  assert.deepEqual(host.calls.at(-1)?.params, { childId: "child", childRunId: "run-child", leaseId: "lease" });
  await second.cancel();
  assert.equal(host.calls.at(-1)?.params.childRunId, "second-run");
  await createChildClient(host).start({ profile: "search", message: "later", resume: second.conversationId, lease });
  assert.equal(host.calls.at(-1)?.persistent, false);
});

test("invalid child context and identities fail before RPC", async () => {
  const host = new Host();
  for (const invalid of [
    { profile: "" }, { profile: null }, { message: "  " }, { message: null },
    { requestId: "" }, { requestId: null }, { requestId: "x".repeat(129) },
    { resume: "" }, { resume: "  " }, { resume: null }, { resume: 12 }, { resume: "bad\0id" },
    { contextMode: null }, { contextMode: "other" }, { contextMode: false }, { contextMode: "fork", resume: "child" },
    { cwd: "  " }, { options: { apiKey: "not-allowed" } }, { options: null }, { lease: null },
  ]) {
    await assert.rejects(createChildClient(host).start({ profile: "search", message: "query", ...invalid } as unknown as ChildRequest));
  }
  assert.deepEqual(host.calls, []);
});

test("repeated fork on a retained lease still uses originating-tool authority", async () => {
  const host = new Host();
  const children = createChildClient(host);
  const lease = { id: "lease", close: async () => {} };
  await children.start({ profile: "search", message: "first", lease });
  await children.start({ profile: "search", message: "fork again", contextMode: "fork", lease });
  assert.equal(host.calls.at(-1)?.persistent, false);
  host.request = async () => { throw new Error("originating tool ended"); };
  await assert.rejects(children.start({ profile: "search", message: "late fork", contextMode: "fork", lease }), /originating tool ended/);
  assert.equal(host.calls.length, 2);
});

test("malformed or wrong resume identities never create a handle", async () => {
  const host = new Host();
  for (const invalid of [{ conversationId: null }, { runId: 123 }, { runId: " " }, { done: null }]) {
    host.result = { ...identity, ...invalid } as unknown as ChildResult;
    await assert.rejects(createChildClient(host).start({ profile: "search", message: "query" }), /Invalid response from the delegated task/);
  }
  host.result = { ...identity, conversationId: "other-child" };
  await assert.rejects(createChildClient(host).start({ profile: "search", message: "next", resume: "child" }), /resumed conversation/);
  assert.equal(host.calls.length, 5);
});

test("steer uses exact identities and stable IDs without starting an idle turn", async () => {
  for (const retained of [false, true]) {
    const host = new Host();
    const lease = retained ? { id: "lease", close: async () => {} } : undefined;
    const child = await createChildClient(host).start({ profile: "search", message: "query", lease });
    for (let i = 0; i < 2; i++) assert.deepEqual(await child.steer("Check errors", { requestId: "guidance-1" }), { outcome: "injected" });
    assert.deepEqual(host.calls.at(-1), host.calls.at(-2));
    assert.deepEqual(host.calls.at(-1), { method: "kodelet.child.steer", persistent: retained, params: {
      childId: "child", childRunId: "run-child", message: "Check errors", requestId: "guidance-1", ...(retained ? { leaseId: "lease" } : {}),
    } });
    host.steerResult = { outcome: "promptRequired", reason: "noRunningTurn" };
    assert.deepEqual(await child.steer("A new task"), host.steerResult);
    assert.notEqual(host.calls.at(-1)?.params.requestId, "guidance-1");
    assert.equal(host.calls.filter((call) => call.method === "kodelet.child.start").length, 1);
  }
});

test("invalid steering inputs and responses are rejected without retry", async () => {
  const host = new Host();
  const child = await createChildClient(host).start({ profile: "search", message: "query" });
  for (const message of ["", " \n ", null, "x".repeat(512 * 1024 + 1)]) await assert.rejects(child.steer(message as string));
  for (const requestId of [null, "", "bad\0id", "x".repeat(129)]) await assert.rejects(child.steer("guidance", { requestId: requestId as string }));
  assert.equal(host.calls.length, 1);
  for (const response of [null, {}, { outcome: "started" }, { outcome: "injected", reason: null }]) {
    host.steerResult = response;
    const count = host.calls.length;
    await assert.rejects(child.steer("guidance"));
    assert.equal(host.calls.length, count + 1);
  }
});

test("stale authority never retries or switches the handle to a later run", async () => {
  const host = new Host();
  const child = await createChildClient(host).start({ profile: "search", message: "query" });
  host.request = async (method, params) => {
    assert.equal((params as Record<string, unknown>).childRunId, "run-child");
    host.reply(method, params, false);
    throw new Error("stale child run");
  };
  for (const operation of [() => child.read(), () => child.cancel(), () => child.steer("guidance")]) {
    await assert.rejects(operation(), /stale child run/);
  }
  assert.equal(host.calls.length, 4);
});

test("abort while polling cancels the exact run even when its read is blocked", { timeout: 2000 }, async () => {
  const host = new Host();
  let entered!: () => void;
  const reading = new Promise<void>((resolve) => { entered = resolve; });
  let finishRead!: (result: ChildResult) => void;
  const blocked = new Promise<ChildResult>((resolve) => { finishRead = resolve; });
  host.request = async (method, params) => {
    host.reply(method, params, false);
    if (method === "kodelet.child.read") { entered(); return await blocked; }
    return identity;
  };
  const child = await createChildClient(host).start({ profile: "search", message: "query" });
  const controller = new AbortController();
  const waiting = child.wait({ signal: controller.signal });
  await reading;
  controller.abort(new Error("stop now"));
  await assert.rejects(waiting, /stop now/);
  assert.deepEqual(host.calls.at(-1), { method: "kodelet.child.cancel", params: { childId: "child", childRunId: "run-child" }, persistent: false });
  finishRead({ ...identity, done: true });
  await delay(60);
  assert.equal(host.calls.length, 3);
});

test("abort during the polling delay does not start an unnecessary read", async () => {
  const host = new Host();
  const child = await createChildClient(host).start({ profile: "search", message: "query" });
  const controller = new AbortController();
  const waiting = child.wait({ signal: controller.signal });
  controller.abort(new Error("stop"));
  await assert.rejects(waiting, /stop/);
  assert.deepEqual(host.calls.map((call) => call.method), ["kodelet.child.start", "kodelet.child.cancel"]);
});

test("uncertain admission errors are not automatically retried", async () => {
  const host = new Host();
  host.request = async (method, params) => { host.reply(method, params, false); throw new Error("transport lost after admission"); };
  await assert.rejects(createChildClient(host).start({ profile: "search", message: "query", requestId: "reserved", lease: { id: "lease", close: async () => {} } }), /transport lost/);
  assert.deepEqual(host.calls, [{ method: "kodelet.child.start", persistent: false, params: { profile: "search", message: "query", requestId: "reserved", leaseId: "lease" } }]);
});
