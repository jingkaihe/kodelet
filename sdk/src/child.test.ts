import assert from "node:assert/strict";
import test from "node:test";
import { ExtensionHost } from "./api.js";
import { createChildClient, type ChildResult } from "./child.js";
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
  assert.deepEqual(calls.at(-1), { method: "kodelet.child.cancel", params: { childId: "child", leaseId: "lease" }, persistent: true });
  await assert.rejects(createChildClient(undefined).start({ profile: "search", message: "query" }), /no local fallback/);
  await assert.rejects(ctx.children.start({ profile: "search", message: "query", lease: { close: async () => {} } }), /real runner background lease/);
});

test("foreground child abort cancels the exact ID and unauthorized admission fails closed", async () => {
  const calls: string[] = [];
  const child = await createChildClient({ async request(method) { calls.push(method); return identity; } }).start({ profile: "search", message: "query" });
  await assert.rejects(child.wait({ signal: AbortSignal.abort(new Error("stop")) }), /stop/);
  assert.deepEqual(calls, ["kodelet.child.start", "kodelet.child.cancel"]);
  await assert.rejects(createChildClient({ async request() { throw new Error("stale authority"); } }).start({ profile: "search", message: "query" }), /stale authority/);
});
