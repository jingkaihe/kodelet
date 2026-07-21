import os from "node:os";
import path from "node:path";
import { performance } from "node:perf_hooks";

import type { AgentStreamEvent, Session, ToolCallData, ToolResultData } from "./agent.js";

const UPDATE_INTERVAL_MS = 200;
const MAX_VISIBLE_RUNNING = 8;
const MAX_VISIBLE_FAILED = 3;
const MAX_VISIBLE_SUCCEEDED = 3;
const MAX_ACTIVITY_ID_LENGTH = 256;
const MAX_KIND_LENGTH = 64;
const MAX_LABEL_LENGTH = 160;
const MAX_PREVIEW_LENGTH = 180;
const MAX_TASK_LENGTH = 1000;
const MAX_CWD_LENGTH = 4096;

export type TaskRunStatus = "running" | "completed" | "failed";
export type TaskRunPhase = "starting" | "working" | "responding" | "completed" | "failed";
export type TaskActivityStatus = "running" | "succeeded" | "failed";

export interface TaskRunCounts {
  succeeded: number;
  failed: number;
  running: number;
}

export interface TaskActivity {
  id: string;
  sequence: number;
  kind: string;
  label: string;
  detail: string;
  status: TaskActivityStatus;
  preview?: string;
}

export interface TaskRunSnapshot {
  version: 1;
  revision: number;
  kind: string;
  status: TaskRunStatus;
  phase: TaskRunPhase;
  title: string;
  detail: string;
  task: string;
  cwd: string;
  elapsedMs: number;
  counts: TaskRunCounts;
  activities: TaskActivity[];
  omittedSucceeded: number;
  omittedFailed?: number;
  omittedRunning?: number;
}

export interface TaskProgressLogger {
  warn(message: string, fields?: Record<string, unknown>): void;
}

export interface TaskProgressContext {
  log: TaskProgressLogger;
  update(content: string, data?: Record<string, unknown>): Promise<void>;
}
export type TaskProgressSession = Pick<Session, "on" | "off">;
export type TaskActivityLabeler = (
  activityKind: string,
  input: Record<string, unknown>,
  cwd: string,
) => [label: string, detail: string];

export interface TaskProgressOptions {
  kind: string;
  task: string;
  cwd: string;
  runningTitle: string;
  completedTitle: string;
  failedTitle: string;
  respondingDetail: string;
  labeler?: TaskActivityLabeler;
}

export interface StartTaskActivityOptions {
  label: string;
  detail?: string;
  kind?: string;
}

export interface FinishTaskOptions {
  success: boolean;
  error?: string;
}

export interface FinishTaskActivityOptions {
  success: boolean;
  result?: string;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function text(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function singleLine(value: string, limit = MAX_LABEL_LENGTH): string {
  const collapsed = value.trim().split(/\s+/).filter(Boolean).join(" ");
  const characters = Array.from(collapsed);
  if (characters.length <= limit) {
    return collapsed;
  }
  return `${characters.slice(0, Math.max(1, limit - 1)).join("").trimEnd()}…`;
}

function quoted(value: string): string {
  return JSON.stringify(singleLine(value, 80));
}

function displayPath(value: string, cwd: string): string {
  if (!value.trim()) {
    return ".";
  }
  const expanded = value.startsWith("~/") ? path.join(os.homedir(), value.slice(2)) : value;
  const root = path.resolve(cwd);
  const resolved = path.resolve(root, expanded);
  const relative = path.relative(root, resolved);
  if (relative === "") {
    return ".";
  }
  if (relative.startsWith(`..${path.sep}`) || relative === ".." || path.isAbsolute(relative)) {
    return value;
  }
  return relative;
}

function lastLine(value: unknown): string | undefined {
  if (typeof value !== "string") {
    return undefined;
  }
  const lines = value.split(/\r?\n/).map((line) => line.trim()).filter(Boolean);
  return lines.length > 0 ? singleLine(lines.at(-1) ?? "", MAX_PREVIEW_LENGTH) : undefined;
}

export function formatTaskToolActivity(
  toolName: string,
  toolInput: Record<string, unknown>,
  cwd: string,
): [label: string, detail: string] {
  const normalized = toolName.trim().toLowerCase();
  const activityPath = displayPath(text(toolInput.file_path) || text(toolInput.path), cwd);
  let label: string;
  let detail: string;

  switch (normalized) {
    case "grep":
    case "grep_tool": {
      const pattern = text(toolInput.pattern);
      label = pattern ? `Search ${quoted(pattern)} in ${activityPath}` : `Search in ${activityPath}`;
      detail = `searching ${activityPath}`;
      break;
    }
    case "glob":
    case "glob_tool": {
      const pattern = text(toolInput.pattern);
      label = pattern ? `Find files ${quoted(pattern)} in ${activityPath}` : `Find files in ${activityPath}`;
      detail = `finding files in ${activityPath}`;
      break;
    }
    case "file_read":
      label = `Read ${activityPath}`;
      detail = `reading ${activityPath}`;
      break;
    case "file_write":
      label = `Write ${activityPath}`;
      detail = `writing ${activityPath}`;
      break;
    case "file_edit":
      label = `Edit ${activityPath}`;
      detail = `editing ${activityPath}`;
      break;
    case "apply_patch":
      label = "Apply patch";
      detail = "applying a patch";
      break;
    case "bash": {
      const description = text(toolInput.description);
      const command = text(toolInput.command);
      label = `Bash: ${singleLine(description || command || "command", 120)}`;
      detail = singleLine(description || "running a command", 120).toLowerCase();
      break;
    }
    case "web_fetch": {
      const url = text(toolInput.url);
      label = url ? `Fetch ${url}` : "Fetch web page";
      detail = url ? `fetching ${url}` : "fetching a web page";
      break;
    }
    case "web_search":
    case "openai_web_search": {
      const query = text(toolInput.query);
      label = query ? `Search web for ${quoted(query)}` : "Search web";
      detail = "searching the web";
      break;
    }
    case "view_image":
      label = `View image ${activityPath}`;
      detail = `viewing ${activityPath}`;
      break;
    case "skill": {
      const skillName = text(toolInput.skill_name) || text(toolInput.skillName);
      label = skillName ? `Load skill ${skillName}` : "Load skill";
      detail = skillName ? `loading ${skillName}` : "loading a skill";
      break;
    }
    case "code_search": {
      const query = text(toolInput.query);
      label = query ? `Search code: ${singleLine(query, 120)}` : "Search code";
      detail = "searching code";
      break;
    }
    default: {
      const displayName = normalized.replaceAll("_", " ").trim() || "activity";
      label = `${displayName.slice(0, 1).toUpperCase()}${displayName.slice(1)}`;
      detail = `running ${displayName}`;
    }
  }

  return [singleLine(label), singleLine(detail)];
}

export class TaskProgress {
  private readonly startedAt = performance.now();
  private readonly running = new Map<string, TaskActivity>();
  private readonly recentSucceeded: TaskActivity[] = [];
  private readonly recentFailed: TaskActivity[] = [];
  private readonly labeler: TaskActivityLabeler;
  private revision = 0;
  private sequence = 0;
  private succeededCount = 0;
  private failedCount = 0;
  private status: TaskRunStatus = "running";
  private phase: TaskRunPhase = "starting";
  private dirty = false;
  private publishPromise?: Promise<void>;
  private attachedSession?: TaskProgressSession;
  private readonly sessionToolCallListener = (event: AgentStreamEvent<ToolCallData>) => this.onToolCall(event);
  private readonly sessionToolUpdateListener = (event: AgentStreamEvent<ToolResultData>) => this.onToolUpdate(event);
  private readonly sessionToolResultListener = (event: AgentStreamEvent<ToolResultData>) => this.onToolResult(event);
  private readonly sessionMessageListener = (event: AgentStreamEvent) => {
    if (text((event.data as { deltaContent?: unknown }).deltaContent)) {
      this.markResponding();
    }
  };

  constructor(
    private readonly context: TaskProgressContext,
    private readonly options: TaskProgressOptions,
  ) {
    this.labeler = options.labeler ?? formatTaskToolActivity;
  }

  async start(): Promise<void> {
    this.changed(true);
    await this.flush();
  }

  attach(session: TaskProgressSession): void {
    this.detach();
    this.attachedSession = session;
    session.on("tool.call", this.sessionToolCallListener);
    session.on("tool.update", this.sessionToolUpdateListener);
    session.on("tool.result", this.sessionToolResultListener);
    session.on("assistant.message_delta", this.sessionMessageListener);
  }

  startActivity(activityId: string, options: StartTaskActivityOptions): void {
    if (!activityId) {
      return;
    }
    const existing = this.running.get(activityId);
    if (existing) {
      existing.kind = options.kind ?? "";
      existing.label = singleLine(options.label);
      existing.detail = singleLine(options.detail ?? "");
      this.phase = "working";
      this.changed(true);
      return;
    }
    this.sequence += 1;
    this.running.set(activityId, {
      id: activityId,
      sequence: this.sequence,
      kind: options.kind ?? "",
      label: singleLine(options.label),
      detail: singleLine(options.detail ?? ""),
      status: "running",
    });
    this.phase = "working";
    this.changed(true);
  }

  updateActivity(activityId: string, result?: string): void {
    const activity = this.running.get(activityId);
    if (!activity) {
      return;
    }
    const preview = lastLine(result);
    if (preview && activity.preview !== preview) {
      activity.preview = preview;
      this.changed(false);
    }
  }

  finishActivity(activityId: string, options: FinishTaskActivityOptions): void {
    const activity = this.running.get(activityId);
    if (!activity) {
      return;
    }
    this.running.delete(activityId);
    activity.status = options.success ? "succeeded" : "failed";
    const preview = lastLine(options.result);
    if (!options.success && preview) {
      activity.preview = preview;
    } else if (options.success) {
      delete activity.preview;
    }
    if (options.success) {
      this.succeededCount += 1;
      this.recentSucceeded.push(activity);
      this.recentSucceeded.splice(0, Math.max(0, this.recentSucceeded.length - MAX_VISIBLE_SUCCEEDED));
    } else {
      this.failedCount += 1;
      this.recentFailed.push(activity);
      this.recentFailed.splice(0, Math.max(0, this.recentFailed.length - MAX_VISIBLE_FAILED));
    }
    this.changed(true);
  }

  markResponding(): void {
    if (this.phase === "responding") {
      return;
    }
    this.phase = "responding";
    this.changed(true);
  }

  async finish(options: FinishTaskOptions): Promise<TaskRunSnapshot> {
    this.detach();
    await this.flush();
    this.status = options.success ? "completed" : "failed";
    this.phase = options.success ? "completed" : "failed";
    const terminalActivities = [...this.running.values()];
    this.running.clear();
    for (const activity of terminalActivities) {
      activity.status = options.success ? "succeeded" : "failed";
      if (!options.success && options.error) {
        activity.preview = singleLine(options.error, MAX_PREVIEW_LENGTH);
      }
    }
    if (options.success) {
      this.succeededCount += terminalActivities.length;
      this.recentSucceeded.push(...terminalActivities);
      this.recentSucceeded.splice(0, Math.max(0, this.recentSucceeded.length - MAX_VISIBLE_SUCCEEDED));
    } else {
      this.failedCount += terminalActivities.length;
      this.recentFailed.push(...terminalActivities);
      this.recentFailed.splice(0, Math.max(0, this.recentFailed.length - MAX_VISIBLE_FAILED));
    }
    this.revision += 1;
    return this.snapshot();
  }

  async flush(): Promise<void> {
    while (true) {
      if (this.publishPromise) {
        await this.publishPromise;
        continue;
      }
      if (!this.dirty) {
        return;
      }
      this.schedulePublish(true);
    }
  }

  snapshot(): TaskRunSnapshot {
    const running = [...this.running.values()].sort((a, b) => a.sequence - b.sequence);
    const selected = [
      ...this.recentSucceeded,
      ...this.recentFailed,
      ...running.slice(-MAX_VISIBLE_RUNNING),
    ].sort((a, b) => a.sequence - b.sequence);

    let title = this.options.runningTitle;
    if (this.status === "completed") {
      title = this.options.completedTitle;
    } else if (this.status === "failed") {
      title = this.options.failedTitle;
    }

    const snapshot: TaskRunSnapshot = {
      version: 1,
      revision: this.revision,
      kind: singleLine(this.options.kind, MAX_KIND_LENGTH),
      status: this.status,
      phase: this.phase,
      title: singleLine(title),
      detail: singleLine(this.detail(running)),
      task: singleLine(this.options.task, MAX_TASK_LENGTH),
      cwd: singleLine(this.options.cwd, MAX_CWD_LENGTH),
      elapsedMs: Math.max(0, Math.round(performance.now() - this.startedAt)),
      counts: { succeeded: this.succeededCount, failed: this.failedCount, running: running.length },
      activities: selected.map((activity) => ({
        ...activity,
        id: singleLine(activity.id, MAX_ACTIVITY_ID_LENGTH),
        kind: singleLine(activity.kind, MAX_KIND_LENGTH),
        label: singleLine(activity.label),
        detail: singleLine(activity.detail),
        ...(activity.preview ? { preview: singleLine(activity.preview, MAX_PREVIEW_LENGTH) } : {}),
      })),
      omittedSucceeded: Math.max(0, this.succeededCount - this.recentSucceeded.length),
    };
    const omittedFailed = Math.max(0, this.failedCount - this.recentFailed.length);
    const omittedRunning = Math.max(0, running.length - MAX_VISIBLE_RUNNING);
    if (omittedFailed > 0) {
      snapshot.omittedFailed = omittedFailed;
    }
    if (omittedRunning > 0) {
      snapshot.omittedRunning = omittedRunning;
    }
    return snapshot;
  }

  private onToolCall(event: AgentStreamEvent<ToolCallData>): void {
    const activityId = text(event.data.toolCallId);
    if (!activityId) {
      return;
    }
    const activityKind = text(event.data.toolName) || "tool";
    let input = isRecord(event.data.input) ? event.data.input : {};
    if (Object.keys(input).length === 0 && event.data.rawInput) {
      try {
        const parsed = JSON.parse(event.data.rawInput) as unknown;
        input = isRecord(parsed) ? parsed : {};
      } catch {
        input = {};
      }
    }
    const [label, detail] = this.labeler(activityKind, input, this.options.cwd);
    this.startActivity(activityId, { kind: activityKind, label, detail });
  }

  private onToolUpdate(event: AgentStreamEvent<ToolResultData>): void {
    this.updateActivity(text(event.data.toolCallId), text(event.data.result));
  }

  private onToolResult(event: AgentStreamEvent<ToolResultData>): void {
    this.finishActivity(text(event.data.toolCallId), {
      success: text(event.data.status) !== "failed",
      result: text(event.data.result),
    });
  }

  private detach(): void {
    const session = this.attachedSession;
    if (!session) {
      return;
    }
    session.off("tool.call", this.sessionToolCallListener);
    session.off("tool.update", this.sessionToolUpdateListener);
    session.off("tool.result", this.sessionToolResultListener);
    session.off("assistant.message_delta", this.sessionMessageListener);
    this.attachedSession = undefined;
  }

  private detail(running: TaskActivity[]): string {
    if (this.status === "failed") {
      return "failed";
    }
    if (this.status === "completed") {
      return "";
    }
    if (this.phase === "starting") {
      return "starting task";
    }
    if (this.phase === "responding") {
      return this.options.respondingDetail;
    }
    if (running.length === 1) {
      return running[0]?.detail || "1 action running";
    }
    if (running.length > 1) {
      return `${running.length} actions running`;
    }
    return "planning next step";
  }

  private changed(immediate: boolean): void {
    if (this.status !== "running") {
      return;
    }
    this.revision += 1;
    this.dirty = true;
    this.schedulePublish(immediate);
  }

  private schedulePublish(immediate: boolean): void {
    if (this.publishPromise) {
      return;
    }
    const task = this.publish(immediate ? 0 : UPDATE_INTERVAL_MS);
    this.publishPromise = task;
    const clear = () => {
      if (this.publishPromise === task) {
        this.publishPromise = undefined;
      }
      if (this.dirty) {
        this.schedulePublish(false);
      }
    };
    void task.then(clear, clear);
  }

  private async publish(delayMs: number): Promise<void> {
    if (delayMs > 0) {
      await new Promise((resolve) => setTimeout(resolve, delayMs));
    }
    while (this.dirty) {
      this.dirty = false;
      const snapshot = this.snapshot();
      const content = snapshot.detail ? `${snapshot.title} — ${snapshot.detail}` : snapshot.title;
      try {
        await this.context.update(content, { taskRun: snapshot });
      } catch (error) {
        this.context.log.warn("failed to publish tool update", {
          error: error instanceof Error ? error.message : String(error),
        });
      }
      if (this.dirty) {
        await new Promise((resolve) => setTimeout(resolve, UPDATE_INTERVAL_MS));
      }
    }
  }
}
