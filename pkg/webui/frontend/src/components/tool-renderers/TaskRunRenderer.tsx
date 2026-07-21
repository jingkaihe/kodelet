import React from 'react';
import { Check, LoaderCircle, X } from 'lucide-react';
import type {
  TaskRunActivity,
  TaskRunSnapshot,
  ExtensionToolMetadata,
  ToolRenderProps,
  ToolResult,
} from '../../types';
import { cn } from '../../utils';
import { ReferenceToolNote, renderSafeMarkdown } from './reference';

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);

const isNonNegativeInteger = (value: unknown): value is number =>
  typeof value === 'number' && Number.isInteger(value) && value >= 0;

const MAX_TASK_RUN_ACTIVITIES = 14;
const MAX_TASK_RUN_KIND_LENGTH = 64;
const MAX_TASK_RUN_TITLE_LENGTH = 160;
const MAX_TASK_RUN_DETAIL_LENGTH = 160;
const MAX_TASK_RUN_TASK_LENGTH = 1000;
const MAX_TASK_RUN_CWD_LENGTH = 4096;
const MAX_TASK_RUN_ACTIVITY_ID_LENGTH = 256;
const MAX_TASK_RUN_PREVIEW_LENGTH = 180;

const isBoundedString = (value: unknown, maxLength: number, required = false): value is string =>
  typeof value === 'string' &&
  Array.from(value).length <= maxLength &&
  (!required || value.length > 0);

const isTaskRunActivity = (value: unknown): value is TaskRunActivity =>
  isRecord(value) &&
  isBoundedString(value.id, MAX_TASK_RUN_ACTIVITY_ID_LENGTH, true) &&
  isNonNegativeInteger(value.sequence) &&
  isBoundedString(value.kind, MAX_TASK_RUN_KIND_LENGTH) &&
  isBoundedString(value.label, MAX_TASK_RUN_TITLE_LENGTH, true) &&
  (value.detail === undefined || isBoundedString(value.detail, MAX_TASK_RUN_DETAIL_LENGTH)) &&
  ['running', 'succeeded', 'failed'].includes(String(value.status)) &&
  (value.preview === undefined || isBoundedString(value.preview, MAX_TASK_RUN_PREVIEW_LENGTH));

export const getTaskRunSnapshot = (toolResult?: ToolResult): TaskRunSnapshot | undefined => {
  const metadata = toolResult?.metadata as ExtensionToolMetadata | undefined;
  const raw = metadata?.data?.taskRun;
  if (
    !isRecord(raw) ||
    raw.version !== 1 ||
    !isNonNegativeInteger(raw.revision) ||
    !isBoundedString(raw.kind, MAX_TASK_RUN_KIND_LENGTH, true) ||
    !['starting', 'working', 'responding', 'completed', 'failed'].includes(String(raw.phase)) ||
    !isBoundedString(raw.title, MAX_TASK_RUN_TITLE_LENGTH, true) ||
    (raw.detail !== undefined && !isBoundedString(raw.detail, MAX_TASK_RUN_DETAIL_LENGTH)) ||
    (raw.task !== undefined && !isBoundedString(raw.task, MAX_TASK_RUN_TASK_LENGTH)) ||
    (raw.cwd !== undefined && !isBoundedString(raw.cwd, MAX_TASK_RUN_CWD_LENGTH)) ||
    !isRecord(raw.counts) ||
    !isNonNegativeInteger(raw.counts.succeeded) ||
    !isNonNegativeInteger(raw.counts.failed) ||
    !isNonNegativeInteger(raw.counts.running) ||
    !['running', 'completed', 'failed'].includes(String(raw.status)) ||
    !isNonNegativeInteger(raw.elapsedMs) ||
    !Array.isArray(raw.activities) ||
    raw.activities.length > MAX_TASK_RUN_ACTIVITIES ||
    !raw.activities.every(isTaskRunActivity) ||
    (raw.omittedSucceeded !== undefined && !isNonNegativeInteger(raw.omittedSucceeded)) ||
    (raw.omittedFailed !== undefined && !isNonNegativeInteger(raw.omittedFailed)) ||
    (raw.omittedRunning !== undefined && !isNonNegativeInteger(raw.omittedRunning))
  ) {
    return undefined;
  }
  return raw as unknown as TaskRunSnapshot;
};

export const formatTaskRunElapsed = (elapsedMs?: number): string => {
  if (!elapsedMs || elapsedMs <= 0) {
    return '';
  }
  const roundedSeconds = Math.round(elapsedMs / 1000);
  if (roundedSeconds < 60) {
    return `${roundedSeconds}s`;
  }
  if (roundedSeconds < 3600) {
    const minutes = Math.floor(roundedSeconds / 60);
    const seconds = roundedSeconds % 60;
    return `${minutes}m ${String(seconds).padStart(2, '0')}s`;
  }
  const hours = Math.floor(roundedSeconds / 3600);
  const minutes = Math.floor((roundedSeconds % 3600) / 60);
  return `${hours}h ${String(minutes).padStart(2, '0')}m`;
};

const ActivityMarker: React.FC<{ status: TaskRunActivity['status'] }> = ({ status }) => {
  if (status === 'failed') {
    return <X aria-hidden="true" className="task-run-activity-marker is-failed" size={14} />;
  }
  if (status === 'running') {
    return (
      <LoaderCircle
        aria-hidden="true"
        className="task-run-activity-marker is-running"
        size={14}
      />
    );
  }
  return <Check aria-hidden="true" className="task-run-activity-marker is-done" size={14} />;
};

const TaskRunActivityList: React.FC<{ snapshot: TaskRunSnapshot }> = ({ snapshot }) => {
  const omitted = [
    snapshot.omittedSucceeded
      ? `+${snapshot.omittedSucceeded} earlier completed`
      : undefined,
    snapshot.omittedFailed ? `+${snapshot.omittedFailed} earlier failed` : undefined,
    snapshot.omittedRunning ? `+${snapshot.omittedRunning} more running` : undefined,
  ].filter((value): value is string => Boolean(value));

  if (snapshot.activities.length === 0 && omitted.length === 0) {
    return <div className="task-run-empty">Waiting for the first activity…</div>;
  }

  return (
    <div className="task-run-activities">
      {snapshot.activities.map((activity) => (
        <div
          className={cn('task-run-activity', `is-${activity.status}`)}
          key={activity.id || `${activity.sequence}-${activity.label}`}
        >
          <ActivityMarker status={activity.status} />
          <div className="task-run-activity-copy">
            <div className="task-run-activity-label">{activity.label}</div>
            {activity.status === 'failed' && activity.preview ? (
              <div className="task-run-activity-preview">{activity.preview}</div>
            ) : null}
          </div>
        </div>
      ))}
      {omitted.map((label) => (
        <div className="task-run-omitted" key={label}>
          {label}
        </div>
      ))}
    </div>
  );
};

const TaskRunStats: React.FC<{ snapshot: TaskRunSnapshot }> = ({ snapshot }) => {
  const values = [
    snapshot.counts.succeeded ? `${snapshot.counts.succeeded} done` : undefined,
    snapshot.counts.failed ? `${snapshot.counts.failed} failed` : undefined,
    snapshot.counts.running ? `${snapshot.counts.running} running` : undefined,
    formatTaskRunElapsed(snapshot.elapsedMs) || undefined,
  ].filter((value): value is string => Boolean(value));

  return values.length > 0 ? <div className="task-run-stats">{values.join(' · ')}</div> : null;
};

const TaskRunRenderer: React.FC<ToolRenderProps> = ({ toolResult, isPartial = false }) => {
  const snapshot = getTaskRunSnapshot(toolResult);
  const metadata = toolResult.metadata as ExtensionToolMetadata | undefined;
  if (!snapshot || !metadata) {
    return null;
  }

  if (isPartial) {
    return (
      <div className="task-run-progress">
        <div className="task-run-headline">
          <span className="task-run-title">{snapshot.title}</span>
          {snapshot.detail ? <span className="task-run-detail">{snapshot.detail}</span> : null}
        </div>
        <TaskRunStats snapshot={snapshot} />
        <TaskRunActivityList snapshot={snapshot} />
      </div>
    );
  }

  const output = metadata.output || '';
  const renderedOutput =
    !toolResult.success && toolResult.error?.trim() === output.trim() ? '' : output;
  const hasActivity =
    snapshot.activities.length > 0 ||
    Boolean(snapshot.omittedSucceeded || snapshot.omittedFailed || snapshot.omittedRunning);

  return (
    <div className="quiet-tool-detail task-run-result">
      <div className="quiet-tool-line">
        <span className="quiet-tool-emphasis">{snapshot.title}</span>
        {formatTaskRunElapsed(snapshot.elapsedMs) ? (
          <span className="quiet-tool-muted">{formatTaskRunElapsed(snapshot.elapsedMs)}</span>
        ) : null}
      </div>

      {!toolResult.success && toolResult.error ? <ReferenceToolNote text={toolResult.error} /> : null}

      {renderedOutput ? (
        <div
          className="tool-compact-markdown task-run-response"
          dangerouslySetInnerHTML={{ __html: renderSafeMarkdown(renderedOutput) }}
        />
      ) : toolResult.success ? (
        <div className="quiet-tool-empty">Agent completed without a response.</div>
      ) : null}

      {hasActivity ? (
        <details className="task-run-history">
          <summary>Show activity</summary>
          <TaskRunStats snapshot={snapshot} />
          <TaskRunActivityList snapshot={snapshot} />
        </details>
      ) : null}
    </div>
  );
};

export default TaskRunRenderer;
