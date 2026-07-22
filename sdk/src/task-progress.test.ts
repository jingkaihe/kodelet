import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import test from "node:test";

import type { Session } from "./agent.js";
import { formatTaskToolActivity, TaskProgress } from "./task-progress.js";
import type { TaskProgressContext } from "./task-progress.js";

function testContext(): TaskProgressContext & { updates: Array<{ content: string; data?: Record<string, unknown> }> } {
  const updates: Array<{ content: string; data?: Record<string, unknown> }> = [];
  return {
    updates,
    log: {
      debug() {},
      info() {},
      warn() {},
      error() {},
    },
    async update(content, data) {
      updates.push({ content, data });
    },
  };
}

test("TaskProgress tracks bounded child-session activity", async () => {
  const context = testContext();
  const session = new EventEmitter();
  const progress = new TaskProgress(context, {
    kind: "code_search",
    task: "Find the update path",
    cwd: "/workspace",
    runningTitle: "Searching code",
    completedTitle: "Searched code",
    failedTitle: "Code search failed",
    respondingDetail: "writing summary",
  });
  progress.attach(session as Pick<Session, "on" | "off">);
  await progress.start();

  for (let index = 0; index < 5; index += 1) {
    session.emit("tool.call", {
      type: "tool.call",
      data: {
        toolCallId: `call-${index}`,
        toolName: "file_read",
        input: { file_path: `/workspace/pkg/file-${index}.go` },
      },
    });
    session.emit("tool.result", {
      type: "tool.result",
      data: { toolCallId: `call-${index}`, toolName: "file_read", status: "completed", result: "done" },
    });
  }
  session.emit("tool.call", {
    type: "tool.call",
    data: {
      toolCallId: "running",
      toolName: "grep_tool",
      input: { pattern: "HandleToolUpdate", path: "/workspace/pkg" },
    },
  });
  await progress.flush();

  const snapshot = progress.snapshot();
  assert.deepEqual(snapshot.counts, { succeeded: 5, failed: 0, running: 1 });
  assert.equal(snapshot.omittedSucceeded, 2);
  assert.equal(snapshot.detail, "searching pkg");
  assert.equal(snapshot.activities.length, 4);
  assert.equal(context.updates.at(-1)?.content, "Searching code - searching pkg");
  assert.ok(context.updates.at(-1)?.data?.taskRun);

  const final = await progress.finish({ success: true });
  assert.equal(final.status, "completed");
  assert.equal(final.counts.running, 0);
  assert.equal(session.listenerCount("tool.call"), 0);
  assert.equal(session.listenerCount("tool.update"), 0);
  assert.equal(session.listenerCount("tool.result"), 0);
  assert.equal(session.listenerCount("assistant.message_delta"), 0);
});

test("TaskProgress supports direct non-agent task activity", async () => {
  const context = testContext();
  const progress = new TaskProgress(context, {
    kind: "download",
    task: "Fetch artifacts",
    cwd: "/workspace",
    runningTitle: "Downloading",
    completedTitle: "Downloaded",
    failedTitle: "Download failed",
    respondingDetail: "writing manifest",
  });
  await progress.start();
  progress.startActivity("artifact-1", {
    kind: "download",
    label: "Download artifact.tar.zst",
    detail: "downloading artifact.tar.zst",
  });
  progress.finishActivity("artifact-1", { success: true });
  await progress.flush();

  assert.equal(progress.snapshot().counts.succeeded, 1);
});

test("TaskProgress uses bounded task detail between activities", async () => {
  const context = testContext();
  const instruction = `Investigate the task progress renderer ${"carefully ".repeat(30)}`;
  const progress = new TaskProgress(context, {
    kind: "subagent",
    task: instruction,
    cwd: "/workspace",
    runningTitle: "Delegated task",
    completedTitle: "Delegated task",
    failedTitle: "Delegated task failed",
    respondingDetail: "writing response",
  });
  await progress.start();

  const detail = progress.snapshot().detail;
  assert.ok(Array.from(detail).length <= 160);
  assert.ok(detail.endsWith("…"));
  assert.equal(context.updates.at(-1)?.content, `Delegated task - ${detail}`);

  progress.startActivity("read-1", { kind: "file_read", label: "Read renderer" });
  progress.finishActivity("read-1", { success: true });
  await progress.flush();
  assert.equal(progress.snapshot().detail, detail);

  await progress.finish({ success: true });
});

test("TaskProgress skips Markdown fences in failed activity previews", async () => {
  const context = testContext();
  const progress = new TaskProgress(context, {
    kind: "build",
    task: "Run tests",
    cwd: "/workspace",
    runningTitle: "Running tests",
    completedTitle: "Ran tests",
    failedTitle: "Tests failed",
    respondingDetail: "writing summary",
  });
  await progress.start();
  progress.startActivity("test-1", { kind: "bash", label: "Run tests" });
  progress.finishActivity("test-1", {
    success: false,
    result: "```text\nTypeScript tests failed\n```",
  });
  progress.startActivity("test-2", { kind: "bash", label: "Run more tests" });
  progress.finishActivity("test-2", { success: false, result: "```" });
  await progress.flush();

  const failed = progress.snapshot().activities.filter((activity) => activity.status === "failed");
  assert.equal(failed[0]?.preview, "TypeScript tests failed");
  assert.equal(failed[1]?.preview, undefined);

  progress.startActivity("test-3", { kind: "bash", label: "Run final tests" });
  const final = await progress.finish({ success: false, error: "Final tests failed\n```" });
  assert.equal(
    final.activities.find((activity) => activity.id === "test-3")?.preview,
    "Final tests failed",
  );
});

test("formatTaskToolActivity uses workspace-relative paths", () => {
  assert.deepEqual(
    formatTaskToolActivity(
      "grep_tool",
      { pattern: "HandleToolUpdate", path: "/workspace/pkg" },
      "/workspace",
    ),
    ['Search "HandleToolUpdate" in pkg', "searching pkg"],
  );
});
